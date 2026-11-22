package server

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/kumoSleeping/iridium/internal/access"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/channel"
	"github.com/kumoSleeping/iridium/internal/ctx/assemble"
	"github.com/kumoSleeping/iridium/internal/deliver"
	"github.com/kumoSleeping/iridium/internal/gate"
	"github.com/kumoSleeping/iridium/internal/session"
	"github.com/kumoSleeping/iridium/internal/store"
)

// Profiles (docs/channels.md "资料"): a card for every member and every channel: what it is
// called by, a few words on who it is, a face and a background. An agent's and a channel's are
// kept under "c:<id>" (a session's channel has its id), a person's and an app's under
// "u:<its id>". A face or a background is an image, or an emoji or a colour drawn as one.

func (s *Server) profileRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/internal/self", s.selfGet)
	mux.HandleFunc("PATCH /v1/internal/self", s.selfPatch)
	mux.HandleFunc("GET /v1/internal/members/{who}", s.memberCardGet)
	mux.HandleFunc("PATCH /v1/internal/members/{who}", s.memberCardPatch)
	mux.HandleFunc("GET /v1/internal/channels/{id}/profile", s.channelProfileGet)
	mux.HandleFunc("PATCH /v1/internal/channels/{id}", s.channelProfilePatch)
	mux.HandleFunc("GET /v1/internal/channels/{id}/background", s.backgroundGet)
	mux.HandleFunc("GET /v1/internal/people/{id}/background", s.personBackgroundGet)
	mux.HandleFunc("POST /v1/internal/recall", s.recall)
	mux.HandleFunc("POST /v1/internal/remember", s.remember)
	mux.HandleFunc("GET /v1/internal/memory", s.memoryGet)
	mux.HandleFunc("DELETE /v1/internal/memory/{id}", s.memoryDelete)
	s.roleRoutes(mux)
}

// here is the channel "here" names for c: the one its agent is working for (called there),
// else its own.
const here = "here"

func (s *Server) hereOf(c caller) string {
	if c.session == "" {
		return ""
	}
	if ch := s.calls.current(c.session); ch != "" && s.ch.Speaks(ch, c.session) {
		return ch
	}
	return c.session
}

// ---- faces drawn from a glyph ----

var colourRE = regexp.MustCompile(`^#([0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)

// glyph: v is an emoji or a colour, what a face or a background may be in place of an image.
func glyph(v string) bool {
	if colourRE.MatchString(v) {
		return true
	}
	rs := []rune(v)
	if len(rs) == 0 || len(rs) > 8 {
		return false
	}
	for _, r := range rs {
		if r < 0x80 || unicode.IsSpace(r) || unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// shortHash names a text: the first 12 hex digits of its SHA-256.
func shortHash(v string) string {
	sum := sha256.Sum256([]byte(v))
	return hex.EncodeToString(sum[:6])
}

// ground is the flat colour under an emoji: one of a few, by key.
func ground(key string) string {
	grounds := []string{"#e8eef7", "#f3e9e2", "#e6f1ea", "#f1e8f3", "#f5efd9", "#e3eff1", "#f4e6e9", "#e9e9f4"}
	h := 0
	for _, r := range shortHash(key) {
		h = h*31 + int(r)
	}
	return grounds[h%len(grounds)]
}

// glyphImage draws glyph g (an emoji or a colour) as an SVG of w×h.
func glyphImage(key, g string, w, h int) []byte {
	if colourRE.MatchString(g) {
		return []byte(fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d"><rect width="%d" height="%d" fill="%s"/></svg>`, w, h, w, h, g))
	}
	return []byte(fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d"><rect width="%d" height="%d" fill="%s"/><text x="%d" y="%d" font-size="%d" text-anchor="middle" dominant-baseline="central">%s</text></svg>`,
		w, h, w, h, ground(key), w/2, h/2, min(w, h)*6/10, html.EscapeString(g)))
}

// face is the image under key, or the glyph of the profile there drawn as one: the face
// (bg false: 128², else the model's given by fallback) or the background.
func (s *Server) face(key string, bg bool, fallback string) (img []byte, version string, ok bool) {
	p := s.st.Profile(key)
	g, w, h, at := p.Avatar, gate.AvatarSize, gate.AvatarSize, key
	if bg {
		g, w, h, at = p.Background, gate.BannerWidth, gate.BannerHeight, store.BackgroundKey(key)
	}
	if g != "" {
		return glyphImage(key, g, w, h), "g" + shortHash(g), true
	}
	if img, v, ok, err := s.st.Avatar(at); err == nil && ok {
		return img, v, true
	}
	if fallback != "" {
		if img, v, ok, err := s.st.Avatar(fallback); err == nil && ok {
			return img, v, true
		}
	}
	return nil, "", false
}

// serveFace answers with an image and its version.
func serveFace(w http.ResponseWriter, r *http.Request, img []byte, v string) {
	h := w.Header()
	ct := http.DetectContentType(img)
	if bytes.HasPrefix(img, []byte("<svg")) {
		ct = "image/svg+xml"
	}
	h.Set("Content-Type", ct)
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	h.Set("ETag", `"`+v+`"`)
	h.Set("Cache-Control", "private, no-cache")
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(img))
}

func (s *Server) backgroundGet(w http.ResponseWriter, r *http.Request) {
	ch, _, err := s.seeingChannel(callerOf(r), r.PathValue("id"))
	if err != nil {
		s.error(w, err)
		return
	}
	img, v, ok := s.face(store.ChannelProfileKey(ch), true, "")
	if r.URL.Query().Get("of") == "channel" {
		img, v, ok = s.channelFace(ch, nil, true)
	}
	if !ok {
		refuse(w, http.StatusNotFound, "not_found", "no background")
		return
	}
	serveFace(w, r, img, v)
}

func (s *Server) personBackgroundGet(w http.ResponseWriter, r *http.Request) {
	img, v, ok := s.face(store.PersonProfileKey(r.PathValue("id")), true, "")
	if !ok {
		refuse(w, http.StatusNotFound, "not_found", "no background")
		return
	}
	serveFace(w, r, img, v)
}

// faceValue reads what a patch gives for a face or a background: "" none; a glyph; else an
// image: a data: URL or base64, or (from the host's own ir) a file's path, from dir if it is
// not absolute.
func (s *Server) faceValue(c caller, v, dir string) (glyphOf string, image []byte, err error) {
	v = strings.TrimSpace(v)
	switch {
	case v == "":
		return "", nil, nil
	case glyph(v):
		return v, nil, nil
	case strings.HasPrefix(v, "data:"):
		_, b64, _ := strings.Cut(v, ",")
		image, err = base64.StdEncoding.DecodeString(b64)
	case c.socket && (strings.HasPrefix(v, "/") || strings.HasPrefix(v, "~/") || strings.HasPrefix(v, "./") || strings.Contains(v, ".")):
		path := v
		if rest, ok := strings.CutPrefix(path, "~/"); ok {
			home, _ := os.UserHomeDir()
			path = filepath.Join(home, rest)
		} else if !filepath.IsAbs(path) {
			if dir == "" {
				return "", nil, fmt.Errorf("%w: %s: give the image's whole path", session.ErrBadRequest, v)
			}
			path = filepath.Join(dir, path)
		}
		st, serr := os.Stat(path)
		if serr != nil || st.IsDir() || st.Size() > gate.MaxAvatarBytes {
			return "", nil, fmt.Errorf("%w: %s: no image there of at most 2 MB (an image file here, an emoji, or a colour like #2f4f6f)", session.ErrBadRequest, v)
		}
		image, err = os.ReadFile(path)
	default:
		image, err = base64.StdEncoding.DecodeString(v)
	}
	if err != nil || len(image) == 0 {
		return "", nil, fmt.Errorf("%w: an image (a file's path, base64), an emoji, or a colour like #2f4f6f", session.ErrBadRequest)
	}
	return "", image, nil
}

// setFaces writes the face and the background a patch gives into the profile under key (p,
// changed in place); extra: another key the face's image is kept under too (a session's
// model's). It says what changed ("avatar", "background").
func (s *Server) setFaces(c caller, key string, p *store.Profile, avatar, background *string, dir, extra string) (what []string, err error) {
	for _, f := range []struct {
		v    *string
		bg   bool
		name string
	}{{avatar, false, "avatar"}, {background, true, "background"}} {
		if f.v == nil {
			continue
		}
		g, img, err := s.faceValue(c, *f.v, dir)
		if err != nil {
			return what, err
		}
		at := key
		if f.bg {
			at = store.BackgroundKey(key)
		}
		if img != nil {
			var v string
			if f.bg {
				img, v, err = gate.MakeBanner(img)
			} else {
				img, v, err = gate.MakeAvatar(img)
			}
			if err != nil {
				return what, fmt.Errorf("%w: %s: a PNG, JPEG or WebP image of at most 2 MB", session.ErrBadRequest, f.name)
			}
			if err := s.st.PutAvatar(at, img, v); err != nil {
				return what, err
			}
			if !f.bg && extra != "" {
				if err := s.st.PutAvatar(extra, img, v); err != nil {
					return what, err
				}
			}
		} else if err := s.st.DeleteAvatar(at); err != nil {
			return what, err
		}
		if f.bg {
			p.Background = g
		} else {
			p.Avatar = g
		}
		what = append(what, f.name)
	}
	return what, nil
}

// intro checks an intro: at most api.IntroMax characters, one paragraph.
func intro(v string) (string, error) {
	v = strings.Join(strings.Fields(v), " ")
	if len([]rune(v)) > api.IntroMax {
		return "", fmt.Errorf("%w: intro: at most %d characters", session.ErrBadRequest, api.IntroMax)
	}
	return v, nil
}

// persona is v as a persona is kept: its lines as written (an agent's persona is read as it was
// written into its system prompt), trimmed, at most PersonaMax characters.
func persona(v string) (string, error) {
	v = strings.TrimSpace(strings.ReplaceAll(v, "\r\n", "\n"))
	if len([]rune(v)) > api.PersonaMax {
		return "", fmt.Errorf("%w: persona: at most %d characters", session.ErrBadRequest, api.PersonaMax)
	}
	return v, nil
}

// ---- the cards ----

// agentProfile is the card of session id's agent.
func (s *Server) agentProfile(id string) api.Profile {
	key := store.ChannelProfileKey(id)
	p := s.st.Profile(key)
	out := api.Profile{ID: store.AgentMember(id), Kind: api.CardKindAgent, Session: id, Name: s.ch.Nick(id), Intro: p.Intro, Persona: p.Persona, Role: s.st.RoleOf(id)}
	st, err := s.m.State(id)
	if err == nil {
		out.Agent, out.Status = st.Agent, st.Status
		out.Model = s.sessionName(st)
	}
	out.Avatar = s.AgentAvatar(id)
	if _, v, ok := s.face(key, true, ""); ok {
		out.Background = "/v1/internal/channels/" + id + "/background?v=" + v
	}
	return out
}

// channelProfile is the card of channel ch: a session's own channel's its own, not its
// agent's (roomKey); a face or a background it has not been given, its agent's.
func (s *Server) channelProfile(ch string) api.Profile {
	p := s.st.Profile(s.roomKey(ch))
	out := api.Profile{ID: ch, Kind: api.CardKindChannel, Name: s.channelName(ch), Intro: p.Intro}
	st, err := s.m.State(ch)
	if err != nil {
		st = nil
	}
	if _, v, ok := s.channelFace(ch, st, false); ok {
		out.Avatar = "/v1/internal/channels/" + ch + "/avatar?of=channel&v=" + v
	}
	if _, v, ok := s.channelFace(ch, st, true); ok {
		out.Background = "/v1/internal/channels/" + ch + "/background?of=channel&v=" + v
	}
	return out
}

// personProfile is the card of the login or the app id; ok false if there is none such.
func (s *Server) personProfile(id string) (api.Profile, bool) {
	info, ok := s.gate().Endpoint(id)
	if !ok {
		return api.Profile{}, false
	}
	key := store.PersonProfileKey(id)
	out := api.Profile{ID: id, Kind: api.CardKindPerson, Name: info.Name, Intro: s.st.Profile(key).Intro, Avatar: info.Avatar}
	if info.Kind != gate.KindPanel && !info.Host() {
		out.Kind = api.CardKindApp
	}
	if _, v, ok := s.face(key, true, ""); ok {
		out.Background = "/v1/internal/people/" + id + "/background?v=" + v
	}
	return out, true
}

// shares: c is in a channel session id's agent is in (its own too), or is that agent.
func (s *Server) shares(c caller, id string) bool {
	if c.session == id || (c.Host() && !c.byAgent()) {
		return true
	}
	for _, ch := range append([]string{id}, s.ch.In(id)...) {
		if st, err := s.m.State(ch); err == nil && s.seenBy(c, st.Session) && s.inChannel(c, ch) == nil {
			return true
		}
	}
	return false
}

// who resolves what names a member for c: an agent (its nick, "agent:<session>", a session's
// id or the start of one) or a person (a login's or an app's id or name).
func (s *Server) who(ctx context.Context, c caller, name string) (session string, person string, err error) {
	name = strings.TrimPrefix(strings.TrimSpace(name), "@")
	if id, ok := store.SessionOfMember(name); ok {
		name = id
	} else {
		nicks, _ := s.st.Nicks()
		for id, n := range nicks {
			if strings.EqualFold(n, name) {
				return id, "", nil
			}
		}
		if _, login, ok := strings.Cut(name, "/"); ok && login != "*" {
			name = login
		}
		if info, ok := s.gate().Find(name); ok {
			return "", info.ID, nil
		}
	}
	id, err := s.sessionArg(ctx, c, name)
	if err != nil {
		return "", "", err
	}
	st, err := s.m.State(id)
	if err != nil || st.Bare || !s.seenBy(c, st.Session) {
		return "", "", fmt.Errorf("%w: no member %q (an agent's nick, a session's id, a login's name)", errNoSession, name)
	}
	return id, "", nil
}

// ---- changing a card ----

// profileNote says in channel ch that a card changed (a grey line, not counted as unread):
// member's (nick), or the channel's own (member ""). The same one changing the same card again
// before anything else is said there changes the line.
func (s *Server) profileNote(c caller, ch, member, nick string, what []string) {
	if ch == "" || len(what) == 0 {
		return
	}
	names := map[string]string{"nick": "称呼", "name": "名称", "intro": "介绍", "persona": "设定", "avatar": "头像", "background": "背景"}
	var parts []string
	for _, w := range what {
		parts = append(parts, names[w])
	}
	text := "频道资料已更新：" + strings.Join(parts, "、")
	if member != "" {
		text = fmt.Sprintf("@%s 的资料已更新：%s", nick, strings.Join(parts, "、"))
	}
	ev := channel.Event{Type: "profile", Member: member, Nick: nick, What: what, By: s.satoriUser(c).Name}
	if c.session != "" {
		ev.By = cmp.Or(s.ch.Nick(c.session), ev.By)
	}
	// The line before, if it is of the same card by the same one and nothing came after: one line.
	if end := s.ch.End(ch); end > 0 {
		if last, ok := s.ch.Get(ch, end); ok && last.Kind == channel.KindNote && last.D.Event != nil && last.D.Event.Type == "profile" &&
			last.D.Event.Member == member && last.D.Event.By == ev.By {
			for _, w := range last.D.Event.What {
				if !slices.Contains(what, w) {
					what = append(what, w)
				}
			}
			if len(what) == len(last.D.Event.What) {
				return
			}
			_ = s.ch.Drop(ch, end)
			s.profileNote(c, ch, member, nick, what)
			return
		}
	}
	s.sat.Note(ch, text, c.ID(), &ev)
}

// cardsChanged tells the pages cards changed: the cards again ("cards"), the members again; the
// faces ("avatars", which every face's address carries) only when faces did (kumo, 2026-11-16: a
// name given to one session reloaded every face in the list).
func (s *Server) cardsChanged(faces bool) {
	if faces {
		s.hub.Set("avatars", s.avatarsSeq.Add(1))
	}
	s.hub.Set("cards", s.cardsSeq.Add(1))
	s.membersChanged()
}

// patchAgent changes the card of session id's agent as p says, for c; in: the channel it is
// said in ("": none).
func (s *Server) patchAgent(c caller, id, in string, p api.ProfilePatch) error {
	if !s.shares(c, id) {
		return errNotMember
	}
	st, err := s.m.State(id)
	if err != nil {
		return err
	}
	// A person or an app must have the right to use the session too (an agent of a channel it
	// is in need not).
	if c.session == "" {
		if _, err := s.using(c, id); err != nil {
			return err
		}
	}
	var what []string
	nick := cmp.Or(p.Nick, p.Name)
	was := s.ch.Nick(id)
	if nick != nil && *nick != was {
		if !deliver.ValidNick(*nick) {
			return channel.ErrNick
		}
		if err := s.ch.SetNick(id, *nick); err != nil {
			return err
		}
		what = append(what, "nick")
	}
	key := store.ChannelProfileKey(id)
	prof := s.st.Profile(key)
	if p.Intro != nil {
		v, err := intro(*p.Intro)
		if err != nil {
			return err
		}
		if v != prof.Intro {
			prof.Intro = v
			what = append(what, "intro")
		}
	}
	if p.Persona != nil {
		v, err := persona(*p.Persona)
		if err != nil {
			return err
		}
		if v != prof.Persona {
			prof.Persona = v
			what = append(what, "persona")
		}
	}
	dir := ""
	if st.Machine == "" {
		dir = st.Cwd
	}
	extra := ""
	if !c.byAgent() {
		extra = modelAvatarKey(st) // a person's choice of face is its model's too, as before
	}
	faces, err := s.setFaces(c, key, &prof, p.Avatar, p.Background, dir, extra)
	if err != nil {
		return err
	}
	what = append(what, faces...)
	if err := s.st.SetProfile(key, prof); err != nil {
		return err
	}
	if len(what) == 0 {
		return nil
	}
	// An agent is not told of what it changed itself.
	if c.session != "" {
		s.toldNow(c.session, store.AgentMember(id), what...)
	}
	if slices.Contains(what, "nick") && was != "" {
		for _, ch := range s.noteChannels(c, id, in) {
			s.tellMembers(c, ch, fmt.Sprintf("@%s 改名为 @%s", was, *nick), channel.Event{Type: "rename", Member: store.AgentMember(id), Nick: *nick, From: was})
		}
		what = slices.DeleteFunc(what, func(w string) bool { return w == "nick" })
	}
	for _, ch := range s.noteChannels(c, id, in) {
		s.profileNote(c, ch, store.AgentMember(id), s.ch.Nick(id), what)
	}
	s.cardsChanged(len(faces) > 0)
	return nil
}

// noteChannels are the channels a change to session id's agent's card is said in: in, if it
// is one the agent is in; else the one c's agent works in, if the agent is there; else every
// channel of several it is in.
func (s *Server) noteChannels(c caller, id, in string) []string {
	for _, ch := range []string{in, s.hereOf(c)} {
		if ch != "" && (ch == id || s.ch.Speaks(ch, id)) && s.multi(ch) {
			return []string{ch}
		}
	}
	var out []string
	for _, ch := range append([]string{id}, s.ch.In(id)...) {
		if s.multi(ch) {
			out = append(out, ch)
		}
	}
	return out
}

// patchChannel changes the card of channel ch as p says, for c.
func (s *Server) patchChannel(ctx context.Context, c caller, ch string, p api.ProfilePatch) error {
	if err := s.inChannel(c, ch); err != nil {
		return err
	}
	var what []string
	if name := cmp.Or(p.Name, p.Nick); name != nil && strings.TrimSpace(*name) != "" && *name != s.channelName(ch) {
		n := strings.TrimSpace(*name)
		if err := s.patch(ctx, ch, api.SessionPatch{Name: &n}); err != nil {
			return err
		}
		what = append(what, "name")
	}
	// A session's own channel too: its own intro, face and background, not its agent's.
	key := s.roomKey(ch)
	prof := s.st.Profile(key)
	if p.Intro != nil {
		v, err := intro(*p.Intro)
		if err != nil {
			return err
		}
		if v != prof.Intro {
			prof.Intro = v
			what = append(what, "intro")
		}
	}
	dir := ""
	if st, err := s.m.State(ch); err == nil && st.Machine == "" {
		dir = st.Cwd
	}
	faces, err := s.setFaces(c, key, &prof, p.Avatar, p.Background, dir, "")
	if err != nil {
		return err
	}
	what = append(what, faces...)
	if err := s.st.SetProfile(key, prof); err != nil {
		return err
	}
	if len(what) == 0 {
		return nil
	}
	if c.session != "" {
		s.toldNow(c.session, "channel:"+ch, what...)
	}
	if s.multi(ch) {
		s.profileNote(c, ch, "", "", what)
	}
	s.cardsChanged(len(faces) > 0)
	return nil
}

// patchPerson changes c's own card: its intro and its background (its name and its face are
// the gate's: the profile page).
func (s *Server) patchPerson(c caller, p api.ProfilePatch) error {
	if c.byAgent() {
		return &access.Denied{Reason: "a person's card is the person's own to change"}
	}
	key := store.PersonProfileKey(c.ID())
	prof := s.st.Profile(key)
	if p.Intro != nil {
		v, err := intro(*p.Intro)
		if err != nil {
			return err
		}
		prof.Intro = v
	}
	faces, err := s.setFaces(c, key, &prof, nil, p.Background, "", "")
	if err != nil {
		return err
	}
	if err := s.st.SetProfile(key, prof); err != nil {
		return err
	}
	s.cardsChanged(len(faces) > 0)
	return nil
}

// ---- handlers ----

func (s *Server) selfGet(w http.ResponseWriter, r *http.Request) {
	c := callerOf(r)
	if c.session == "" {
		p, _ := s.personProfile(c.ID())
		p.Mine = true
		reply(w, http.StatusOK, api.Self{Profile: p})
		return
	}
	reply(w, http.StatusOK, s.selfOf(c.session))
}

func (s *Server) selfPatch(w http.ResponseWriter, r *http.Request) {
	c := callerOf(r)
	var p api.ProfilePatch
	if !readJSON(w, r, &p) {
		return
	}
	if c.session == "" {
		if err := s.patchPerson(c, p); err != nil {
			s.profileFault(w, err)
			return
		}
		out, _ := s.personProfile(c.ID())
		out.Mine = true
		reply(w, http.StatusOK, out)
		return
	}
	if err := s.patchAgent(c, c.session, r.URL.Query().Get("channel"), p); err != nil {
		s.profileFault(w, err)
		return
	}
	reply(w, http.StatusOK, s.agentProfile(c.session))
}

// profileFault answers with an error of a card's change.
func (s *Server) profileFault(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, channel.ErrNick), errors.Is(err, store.ErrNickTaken):
		s.memberFault(w, err)
	default:
		s.error(w, err)
	}
}

func (s *Server) memberCardGet(w http.ResponseWriter, r *http.Request) {
	c := callerOf(r)
	id, person, err := s.who(r.Context(), c, r.PathValue("who"))
	if err != nil {
		s.error(w, err)
		return
	}
	if person != "" {
		p, _ := s.personProfile(person)
		p.Mine = !c.byAgent() && person == c.ID()
		reply(w, http.StatusOK, api.MemberCard{Profile: p, Said: s.saidBy(c, person, "")})
		return
	}
	p := s.agentProfile(id)
	p.Mine = s.shares(c, id)
	out := api.MemberCard{Profile: p, Said: s.saidBy(c, store.AgentMember(id), id)}
	for _, mc := range s.selfChannels(id, 8) {
		if st, err := s.m.State(mc.Channel); err == nil && s.seenBy(c, st.Session) {
			out.Channels = append(out.Channels, mc)
		}
	}
	reply(w, http.StatusOK, out)
}

func (s *Server) memberCardPatch(w http.ResponseWriter, r *http.Request) {
	c := callerOf(r)
	id, person, err := s.who(r.Context(), c, r.PathValue("who"))
	if err != nil {
		s.error(w, err)
		return
	}
	var p api.ProfilePatch
	if !readJSON(w, r, &p) {
		return
	}
	if person != "" {
		if c.byAgent() || person != c.ID() {
			forbid(w, errors.New("a person's card is the person's own to change"))
			return
		}
		if err := s.patchPerson(c, p); err != nil {
			s.profileFault(w, err)
			return
		}
		out, _ := s.personProfile(person)
		out.Mine = true
		reply(w, http.StatusOK, out)
		return
	}
	in := r.URL.Query().Get("channel")
	if in == here {
		in = s.hereOf(c)
	}
	if err := s.patchAgent(c, id, in, p); err != nil {
		s.profileFault(w, err)
		return
	}
	out := s.agentProfile(id)
	out.Mine = true
	reply(w, http.StatusOK, out)
}

// channelArg is the channel {id} names for c: "here", or as seeingChannel.
func (s *Server) channelArg(c caller, id string) (string, error) {
	if id == here {
		if id = s.hereOf(c); id == "" {
			return "", errSelf
		}
	}
	ch, _, err := s.seeingChannel(c, id)
	return ch, err
}

func (s *Server) channelProfileGet(w http.ResponseWriter, r *http.Request) {
	c := callerOf(r)
	ch, err := s.channelArg(c, r.PathValue("id"))
	if err != nil {
		s.error(w, err)
		return
	}
	p := s.channelProfile(ch)
	p.Mine = s.inChannel(c, ch) == nil
	reply(w, http.StatusOK, p)
}

func (s *Server) channelProfilePatch(w http.ResponseWriter, r *http.Request) {
	c := callerOf(r)
	ch, err := s.channelArg(c, r.PathValue("id"))
	if err != nil {
		s.error(w, err)
		return
	}
	var p api.ProfilePatch
	if !readJSON(w, r, &p) {
		return
	}
	if err := s.patchChannel(r.Context(), c, ch, p); err != nil {
		s.profileFault(w, err)
		return
	}
	out := s.channelProfile(ch)
	out.Mine = true
	reply(w, http.StatusOK, out)
}

// ---- what an agent is told of itself ----

func head(text string, n int) string {
	text = strings.Join(strings.Fields(text), " ")
	if r := []rune(text); len(r) > n {
		return string(r[:n]) + "…"
	}
	return text
}

// selfChannels are the channels session id's agent is in, those it spoke in last first, at
// most limit: when it last spoke there, who else spoke around then, the head of what it said.
func (s *Server) selfChannels(id string, limit int) []api.MeChannel {
	me := store.AgentMember(id)
	var out []api.MeChannel
	for _, ch := range append([]string{id}, s.ch.In(id)...) {
		mc := api.MeChannel{Channel: ch, Name: s.channelName(ch), Own: ch == id}
		if seq := s.st.ChannelLastBy(ch, me); seq > 0 {
			if m, ok := s.ch.Get(ch, seq); ok {
				mc.At, mc.Last = m.At, head(m.Text, 60)
			}
			_, by := s.agentsOf(ch)
			around, _ := s.ch.Between(ch, max(0, seq-12), seq+6, 40)
			for _, m := range around {
				if m.Author == me || (m.Kind != channel.KindUser && m.Kind != channel.KindSaid) {
					continue
				}
				if w := whoOf(m, by); !slices.Contains(mc.With, w) && len(mc.With) < 4 {
					mc.With = append(mc.With, w)
				}
			}
		}
		if ch == id && mc.At == 0 && !s.multi(ch) {
			continue // its own session, nothing said yet
		}
		out = append(out, mc)
	}
	slices.SortStableFunc(out, func(a, b api.MeChannel) int { return cmp.Compare(b.At, a.At) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// selfBefore are the contexts session id had before it was cleared, the latest first.
func (s *Server) selfBefore(id string, limit int) []api.MeBefore {
	var out []api.MeBefore
	for _, b := range s.st.Before(id, limit) {
		mb := api.MeBefore{Session: b.Session, At: b.At}
		if ms, err := s.ch.List(b.Session, 0, 0, 2000); err == nil {
			for _, m := range ms {
				if m.Kind != channel.KindUser {
					continue
				}
				if mb.Turns == 0 {
					mb.First = head(m.Text, 60)
				}
				mb.Turns++
			}
		}
		out = append(out, mb)
	}
	return out
}

// memoriesOf are what session id's agent remembers (its role's, else its own), the latest
// first, at most limit.
func (s *Server) memoriesOf(id string, limit int) []api.Memory {
	rows, _ := s.st.Memories(s.st.MemoryOwner(id), limit)
	out := make([]api.Memory, 0, len(rows))
	for _, m := range rows {
		am := api.Memory{ID: m.ID, Text: m.Text, Session: m.Session, Channel: m.Channel, Seq: m.Seq, At: m.At}
		if m.Channel != "" {
			am.Name = s.channelName(m.Channel)
		}
		out = append(out, am)
	}
	return out
}

// selfOf is what session id's agent is told of itself.
func (s *Server) selfOf(id string) api.Self {
	p := s.agentProfile(id)
	p.Mine = true
	return api.Self{Profile: p, Channels: s.selfChannels(id, 8), Before: s.selfBefore(id, 3), Memory: s.memoriesOf(id, 0)}
}

// ---- memory ----

func (s *Server) remember(w http.ResponseWriter, r *http.Request) {
	c := callerOf(r)
	if c.session == "" {
		refuse(w, http.StatusBadRequest, "bad_request", "an agent's own: from inside a session")
		return
	}
	var req api.Remember
	if !readJSON(w, r, &req) {
		return
	}
	text := strings.TrimSpace(req.Text)
	if text == "" || len([]rune(text)) > api.MemoryMax {
		refuse(w, http.StatusBadRequest, "bad_request", fmt.Sprintf("text: one short note, at most %d characters", api.MemoryMax))
		return
	}
	ch := s.hereOf(c)
	id, err := s.st.AddMemory(store.Memory{Owner: s.st.MemoryOwner(c.session), Text: s.MaskText(text), Session: c.session, Channel: ch, Seq: s.ch.End(ch)})
	if err != nil {
		s.fault(w, err)
		return
	}
	s.hub.Set("memory", s.memorySeq.Add(1))
	reply(w, http.StatusOK, map[string]any{"id": id})
}

// memoryOwner is whose memory a request is of: ?role=, ?session= (one the caller shares a
// channel with), else the calling agent's.
func (s *Server) memoryOwner(c caller, r *http.Request) (string, error) {
	if role := r.URL.Query().Get("role"); role != "" && !c.byAgent() {
		return store.RoleProfileKey(role), nil
	}
	id := cmp.Or(r.URL.Query().Get("session"), c.session)
	if id == "" {
		return "", fmt.Errorf("%w: whose: session=<id>", session.ErrBadRequest)
	}
	id, err := s.sessionArg(r.Context(), c, id)
	if err != nil {
		return "", err
	}
	if !s.shares(c, id) {
		return "", errNotMember
	}
	return s.st.MemoryOwner(id), nil
}

func (s *Server) memoryGet(w http.ResponseWriter, r *http.Request) {
	c := callerOf(r)
	owner, err := s.memoryOwner(c, r)
	if err != nil {
		s.error(w, err)
		return
	}
	rows, err := s.st.Memories(owner, 0)
	if err != nil {
		s.fault(w, err)
		return
	}
	out := make([]api.Memory, 0, len(rows))
	for _, m := range rows {
		am := api.Memory{ID: m.ID, Text: m.Text, Session: m.Session, Channel: m.Channel, Seq: m.Seq, At: m.At}
		if m.Channel != "" {
			am.Name = s.channelName(m.Channel)
		}
		out = append(out, am)
	}
	reply(w, http.StatusOK, out)
}

func (s *Server) memoryDelete(w http.ResponseWriter, r *http.Request) {
	c := callerOf(r)
	owner, err := s.memoryOwner(c, r)
	if err != nil {
		s.error(w, err)
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	ok, err := s.st.DeleteMemory(owner, id)
	if err != nil {
		s.fault(w, err)
		return
	}
	if !ok {
		refuse(w, http.StatusNotFound, "not_found", "no such memory")
		return
	}
	s.hub.Set("memory", s.memorySeq.Add(1))
	reply(w, http.StatusOK, map[string]any{"ok": true})
}

// ---- recall ----

// recallChannels are the channels recall reads for session id's agent: its own, those it is
// in, those of the contexts it had before; all: every channel c sees.
func (s *Server) recallChannels(ctx context.Context, c caller, id string, all bool) []string {
	var out []string
	add := func(ch string) {
		if !slices.Contains(out, ch) {
			out = append(out, ch)
		}
	}
	if id != "" {
		add(id)
		for _, ch := range s.ch.In(id) {
			add(ch)
		}
		for _, b := range s.st.Before(id, 20) {
			add(b.Session)
		}
	}
	if all || id == "" {
		if list, err := s.m.List(ctx); err == nil {
			for _, ss := range list {
				if s.seenBy(c, ss) {
					add(ss.ID)
				}
			}
		}
	}
	return out
}

func (s *Server) recall(w http.ResponseWriter, r *http.Request) {
	c := callerOf(r)
	var req api.Recall
	if !readJSON(w, r, &req) {
		return
	}
	q := strings.TrimSpace(req.Q)
	if q == "" {
		refuse(w, http.StatusBadRequest, "bad_request", "q: the words to look for")
		return
	}
	limit := req.Limit
	if limit <= 0 || limit > 50 {
		limit = 12
	}
	out := []api.RecallHit{}
	// What it chose to remember first: fewest, and written to be found.
	if c.session != "" {
		rows, _ := s.st.Memories(s.st.MemoryOwner(c.session), 0)
		words := strings.Fields(strings.ToLower(q))
		for _, m := range rows {
			low := strings.ToLower(m.Text)
			if slices.ContainsFunc(words, func(w string) bool { return strings.Contains(low, w) }) {
				out = append(out, api.RecallHit{Kind: "memory", ID: m.ID, At: m.At, Head: head(m.Text, 200), Channel: m.Channel, Seq: m.Seq})
			}
		}
	}
	var hits []api.RecallHit
	for _, ch := range s.recallChannels(r.Context(), c, c.session, req.All) {
		st, err := s.m.State(ch)
		if err != nil || !s.seenBy(c, st.Session) {
			continue
		}
		ms, err := s.ch.Search(ch, q, limit)
		if err != nil {
			continue
		}
		_, by := s.agentsOf(ch)
		name := s.channelName(ch)
		for _, m := range ms {
			if m.Gone || (m.Kind != channel.KindUser && m.Kind != channel.KindSaid) {
				continue
			}
			hits = append(hits, api.RecallHit{Kind: "message", Channel: ch, Name: name, Seq: m.Seq, Who: whoOf(m, by), At: m.At, Head: head(s.MaskText(m.Text), 120),
				Get: fmt.Sprintf("ir api message.get channel_id=%s message_id=%d", ch, m.Seq)})
		}
	}
	slices.SortStableFunc(hits, func(a, b api.RecallHit) int { return cmp.Compare(b.At, a.At) })
	if len(hits) > limit {
		hits = hits[:limit]
	}
	reply(w, http.StatusOK, append(out, hits...))
}

// saidBy are the last things member said in the channels c sees: an agent's (session id: the
// channels it is in), a person's (the channels c's agent is in, else none).
func (s *Server) saidBy(c caller, member, id string) []api.RecallHit {
	var chs []string
	if id != "" {
		chs = append([]string{id}, s.ch.In(id)...)
	} else if c.session != "" {
		chs = append([]string{c.session}, s.ch.In(c.session)...)
	}
	var out []api.RecallHit
	for _, ch := range chs {
		st, err := s.m.State(ch)
		if err != nil || !s.seenBy(c, st.Session) {
			continue
		}
		ms, err := s.ch.List(ch, 0, 0, 200)
		if err != nil {
			continue
		}
		n := 0
		for i := len(ms) - 1; i >= 0 && n < 2; i-- {
			m := ms[i]
			if m.Gone || (m.Kind != channel.KindUser && m.Kind != channel.KindSaid) {
				continue
			}
			if m.Author != member && !strings.HasSuffix(member, "/"+m.Author) && m.Author != strings.TrimSuffix(member, "/*") {
				continue
			}
			out = append(out, api.RecallHit{Kind: "message", Channel: ch, Name: s.channelName(ch), Seq: m.Seq, At: m.At, Head: head(s.MaskText(m.Text), 120)})
			n++
		}
	}
	slices.SortStableFunc(out, func(a, b api.RecallHit) int { return cmp.Compare(b.At, a.At) })
	if len(out) > 6 {
		out = out[:6]
	}
	return out
}

// ---- what an agent was told of the cards ----

// card is a card as an agent is told of it: what it compares to say what changed.
type card struct {
	Name       string `json:"name,omitempty"`
	Intro      string `json:"intro,omitempty"`
	Persona    string `json:"persona,omitempty"` // the agent's own alone (cardsFor)
	Avatar     string `json:"avatar,omitempty"`
	Background string `json:"background,omitempty"`
}

// cardOf is the card under key, by name; its images by their versions.
func (s *Server) cardOf(key, name string) card {
	p := s.st.Profile(key)
	c := card{Name: name, Intro: p.Intro, Persona: p.Persona, Avatar: p.Avatar, Background: p.Background}
	if _, v, ok := s.face(key, false, ""); ok {
		c.Avatar = v
	}
	if _, v, ok := s.face(key, true, ""); ok {
		c.Background = v
	}
	return c
}

// cardsIn are the cards of channel ch as an agent there is told of them: the channel's
// ("channel:<id>"), each agent's (its member).
func (s *Server) cardsIn(ch string) map[string]card {
	out := map[string]card{}
	if _, own := s.ch.Own(ch); own {
		out["channel:"+ch] = s.cardOf(s.roomKey(ch), s.channelName(ch))
	} else {
		out["channel:"+ch] = card{Name: s.channelName(ch)}
	}
	for _, id := range s.ch.Agents(ch) {
		out[store.AgentMember(id)] = s.cardOf(store.ChannelProfileKey(id), s.ch.Nick(id))
	}
	return out
}

// cardsFor are the cards of channel ch as session to's agent is told of them: a persona is its
// own agent's alone (otae, 2026-11-21), the others' are not told.
func (s *Server) cardsFor(ch, to string) map[string]card {
	out := s.cardsIn(ch)
	for k, c := range out {
		if k != store.AgentMember(to) && c.Persona != "" {
			c.Persona = ""
			out[k] = c
		}
	}
	return out
}

// ProfileChange is a card changed since an agent was last told: Who ("" the channel), What
// ("name", "intro", "persona": its own, "avatar", "background"), From and To (words; none for
// an image).
type ProfileChange struct{ Who, What, From, To string }

// profileChanges are the cards of channel ch that changed since session to's agent was last
// told of them there; what it is told of now is kept. Nothing the first time.
func (s *Server) profileChanges(ch, to string) []ProfileChange {
	now := s.cardsFor(ch, to)
	var was map[string]card
	told := s.st.ProfileTold(to, ch)
	enc, _ := json.Marshal(now)
	if string(enc) != told {
		if err := s.st.SetProfileTold(to, ch, string(enc)); err != nil {
			s.logf("server: channel %s: what %s was told of the cards: %v", ch, to, err)
		}
	}
	if told == "" || json.Unmarshal([]byte(told), &was) != nil {
		return nil
	}
	var keys []string
	for k := range now {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var out []ProfileChange
	for _, k := range keys {
		n, w := now[k], was[k]
		if _, ok := was[k]; !ok {
			continue // new here: the members' line says so
		}
		who := w.Name
		if strings.HasPrefix(k, "channel:") {
			who = ""
		}
		if n.Name != w.Name {
			out = append(out, ProfileChange{who, "name", w.Name, n.Name})
		}
		if n.Intro != w.Intro {
			out = append(out, ProfileChange{who, "intro", w.Intro, n.Intro})
		}
		if n.Persona != w.Persona {
			out = append(out, ProfileChange{who, "persona", w.Persona, n.Persona})
		}
		if n.Avatar != w.Avatar {
			out = append(out, ProfileChange{Who: who, What: "avatar"})
		}
		if n.Background != w.Background {
			out = append(out, ProfileChange{Who: who, What: "background"})
		}
	}
	return out
}

// toldNow: session's agent changed what of the card key ("agent:<id>", "channel:<id>") itself:
// it is as if told of those fields, in every channel where it was told of the cards.
func (s *Server) toldNow(session, key string, what ...string) {
	for _, ch := range append([]string{session}, s.ch.In(session)...) {
		told := s.st.ProfileTold(session, ch)
		var was map[string]card
		if told == "" || json.Unmarshal([]byte(told), &was) != nil {
			continue
		}
		now, ok := s.cardsFor(ch, session)[key]
		w, had := was[key]
		if !ok || !had {
			continue
		}
		for _, f := range what {
			switch f {
			case "nick", "name":
				w.Name = now.Name
			case "intro":
				w.Intro = now.Intro
			case "persona":
				w.Persona = now.Persona
			case "avatar":
				w.Avatar = now.Avatar
			case "background":
				w.Background = now.Background
			}
		}
		was[key] = w
		if enc, err := json.Marshal(was); err == nil {
			_ = s.st.SetProfileTold(session, ch, string(enc))
		}
	}
}

// peopleOf are the people of channel ch that have an intro (at most 8), as an agent there is
// told of them.
func (s *Server) peopleOf(ch string) []assemble.ChannelPerson {
	profs, err := s.st.Profiles(store.PersonProfileKey(""))
	if err != nil {
		return nil
	}
	var out []assemble.ChannelPerson
	for key, p := range profs {
		if p.Intro == "" {
			continue
		}
		info, ok := s.gate().Endpoint(strings.TrimPrefix(key, store.PersonProfileKey("")))
		if !ok {
			continue
		}
		if id := info.Identity(); !id.Host() && !s.ch.Has(ch, id.App, id.Login) {
			continue
		}
		out = append(out, assemble.ChannelPerson{Name: info.Name, Intro: head(p.Intro, 80)})
	}
	slices.SortFunc(out, func(a, b assemble.ChannelPerson) int { return cmp.Compare(a.Name, b.Name) })
	if len(out) > 8 {
		out = out[:8]
	}
	return out
}
