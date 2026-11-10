package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"github.com/kumoSleeping/iridium/internal/access"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/config"
	"github.com/kumoSleeping/iridium/internal/gate"
	"github.com/kumoSleeping/iridium/internal/secrets"
)

// appRoutes are the apps' and their logins' (docs/api.md "应用"): who is calling, the list (ir
// apps), invites of ir_web, names and avatars, devices, removing, the confirmations agents' ir asks
// a person for, and public.origin. An app comes by pairing only (routes_pair.go).
func (s *Server) appRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/me", s.me)
	mux.HandleFunc("PUT /api/me/profile", func(w http.ResponseWriter, r *http.Request) { s.gate().ServeProfile(w, r, "") })
	// Another's: ir_web's logins and the host only (the gate checks).
	mux.HandleFunc("PUT /api/apps/{id}/profile", func(w http.ResponseWriter, r *http.Request) { s.gate().ServeProfile(w, r, r.PathValue("id")) })
	// The device it runs on: its own, or (ir_web's logins and the host) another's.
	mux.HandleFunc("PUT /api/me/device", func(w http.ResponseWriter, r *http.Request) { s.setDevice(w, r, "") })
	mux.HandleFunc("PUT /api/apps/{id}/device", func(w http.ResponseWriter, r *http.Request) { s.setDevice(w, r, r.PathValue("id")) })
	mux.HandleFunc("PUT /api/apps/{id}/follows", s.panelOnly(s.setFollows))
	mux.HandleFunc("GET /api/apps", s.manage(s.apps))
	mux.HandleFunc("DELETE /api/apps/{id}", s.manage(s.revoke))
	mux.HandleFunc("POST /api/invites", s.manage(s.invite))
	mux.HandleFunc("GET /api/confirms", s.panelOnly(func(w http.ResponseWriter, r *http.Request) { reply(w, http.StatusOK, s.asks.pending()) }))
	mux.HandleFunc("POST /api/confirms/{id}", s.panelOnly(s.answerConfirm))
	// How one went, never a secret: for whoever manages, and any ir on the socket (an agent's in a
	// session an app opened waits for its ir run's too).
	mux.HandleFunc("GET /api/confirms/{id}", func(w http.ResponseWriter, r *http.Request) {
		if c := callerOf(r); !c.CanManage() && !c.socket {
			forbid(w, errManageApps)
			return
		}
		s.confirmState(w, r)
	})
	mux.HandleFunc("GET /api/usage", s.usage) // routes_usage.go
	mux.HandleFunc("GET /api/public", s.manage(s.publicState))
	// Opening or closing ir_web: a person's ir at the host only (decision 3), never a login of it.
	mux.HandleFunc("PUT /api/public", func(w http.ResponseWriter, r *http.Request) {
		if !callerOf(r).machine() {
			forbid(w, errors.New("only the host's ir sets public.origin"))
			return
		}
		s.setPublic(w, r)
	})
}

// errManageApps answers an app that would manage the others.
var errManageApps = errors.New("only ir_web's logins and the host manage apps")

// manage lets ir_web's logins and the host (also an agent's ir) through; apps manage nothing.
func (s *Server) manage(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !callerOf(r).CanManage() {
			forbid(w, errManageApps)
			return
		}
		h(w, r)
	}
}

// panelOnly lets through what may approve, widen and see secrets: a person on a login of ir_web,
// or at the host.
func (s *Server) panelOnly(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !callerOf(r).panel() {
			forbid(w, errors.New("only a person on ir_web or at the host may do this"))
			return
		}
		h(w, r)
	}
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	c := callerOf(r)
	info, ok := s.gate().Endpoint(c.ID())
	if !ok {
		fail(w, http.StatusNotFound, "no such app or login")
		return
	}
	reply(w, http.StatusOK, s.meOf(c, info))
}

// apps lists the host, ir_web (while there is a public origin, or a login of it), the apps, the
// logins of ir_web and their invites.
func (s *Server) apps(w http.ResponseWriter, r *http.Request) {
	a := s.gate().Access()
	out := api.Apps{Apps: []api.App{}, Logins: []api.Login{}, Invites: []api.PendingInvite{}}
	host, _ := s.gate().Endpoint(gate.HostID)
	out.Apps = append(out.Apps, s.appOf(host))
	follows, _ := s.st.Follows()
	var chats map[string]bool
	if s.Chat != nil {
		chats = s.Chat.Bots()
	}
	web := api.App{ID: gate.WebApp, Name: gate.WebApp, Web: true, Rules: access.Default(), Device: api.DeviceComputer}
	var apps []api.App
	for _, e := range a.Endpoints {
		if e.Kind == gate.KindPanel {
			l := s.loginOf(e)
			out.Logins = append(out.Logins, l)
			web.Logins++
			web.Online += l.Online
			web.UsedToday += l.UsedToday
			web.LastSeenAt = max(web.LastSeenAt, l.LastSeenAt)
			continue
		}
		app := s.appOf(e)
		app.Follows = follows[e.ID]
		if on, ok := chats[e.ID]; ok {
			app.Chat = map[bool]api.AppChat{false: api.AppChatIdle, true: api.AppChatOn}[on]
		}
		apps = append(apps, app)
	}
	if s.gate().Public() || web.Logins > 0 {
		out.Apps = append(out.Apps, web)
	}
	out.Apps = append(out.Apps, apps...)
	for _, i := range a.Invites {
		out.Invites = append(out.Invites, api.PendingInvite{ID: i.ID, Name: i.Name, Setup: i.Setup, Claude: permsClaude(i.Perms),
			CreatedAt: ms(i.CreatedAt), ExpiresAt: ms(i.ExpiresAt)})
	}
	reply(w, http.StatusOK, out)
}

// revoke removes an app, a login of ir_web ("ir_web/名字", or its ID) or an unused invite. The
// host and ir_web are built in.
func (s *Server) revoke(w http.ResponseWriter, r *http.Request) {
	ref := r.PathValue("id")
	if gate.IsHostRef(ref) || ref == gate.WebApp {
		fail(w, http.StatusConflict, ref+" is built in")
		return
	}
	t, found := s.gate().Find(ref)
	hash := ""
	if found {
		hash = s.gate().TokenHash(t.ID) // an official extension's bot holding it goes with it
	}
	err := s.gate().Revoke(ref)
	if err == nil && hash != "" {
		go s.extDetach(hash)
	}
	s.gateDone(w, err)
}

// setFollows puts the logins of ir_web app ref (ID or name) follows in place. Until S4.
func (s *Server) setFollows(w http.ResponseWriter, r *http.Request) {
	var req api.SetFollows
	if !readJSON(w, r, &req) {
		return
	}
	bot, ok := s.gate().Find(r.PathValue("id"))
	if !ok || bot.Host() {
		fail(w, http.StatusNotFound, "no such app")
		return
	}
	if bot.Kind != gate.KindChannel {
		fail(w, http.StatusBadRequest, "only an app follows logins")
		return
	}
	panels := []string{}
	for _, ref := range req.Panels {
		p, ok := s.gate().Find(ref)
		if !ok || p.Host() || p.Kind != gate.KindPanel {
			fail(w, http.StatusBadRequest, "not a login of ir_web: "+ref)
			return
		}
		if !slices.Contains(panels, p.ID) {
			panels = append(panels, p.ID)
		}
	}
	if err := s.st.SetFollows(bot.ID, panels); err != nil {
		s.error(w, err)
		return
	}
	s.terminalsChanged()
	reply(w, http.StatusOK, api.SetFollows{Panels: panels})
}

// setDevice sets the device app or login ref (ID or name; "" the caller) runs on.
func (s *Server) setDevice(w http.ResponseWriter, r *http.Request, ref string) {
	var req api.SetDevice
	if !readJSON(w, r, &req) {
		return
	}
	if !req.Device.Valid() {
		fail(w, http.StatusBadRequest, `device is "computer", "laptop", "phone" or "none"`)
		return
	}
	c := callerOf(r)
	if ref == "" {
		ref = c.ID()
	}
	info, ok := s.gate().Find(ref)
	if !ok {
		fail(w, http.StatusNotFound, "no such app or login")
		return
	}
	if info.ID != c.ID() && !c.CanManage() {
		forbid(w, errors.New("only ir_web's logins and the host set another's device"))
		return
	}
	if err := s.st.SetDevice(info.ID, string(req.Device)); err != nil {
		s.error(w, err)
		return
	}
	reply(w, http.StatusOK, api.SetDevice{Device: s.deviceOf(info.ID)})
}

// publishList makes every stream's list again: what an app sees may have changed.
func (s *Server) publishList() {
	if l, err := s.m.List(context.Background()); err == nil {
		s.hub.Set("list", l)
	}
}

func (s *Server) invite(w http.ResponseWriter, r *http.Request) {
	var req api.NewInvite
	if !readJSON(w, r, &req) {
		return
	}
	c := callerOf(r)
	if req.Claude && c.panel() && s.ownerSays(c) == "" {
		forbid(w, errOwnerFlag)
		return
	}
	if !s.gate().Public() {
		fail(w, http.StatusConflict, "no public origin: ir_web needs one for its passkeys (ir public)")
		return
	}
	if _, err := s.gate().CheckLoginName(req.Name); req.Name != "" && err != nil {
		s.gateDone(w, err)
		return
	}
	if !c.panel() {
		s.ask(w, c, "invite", req.Name, req.Claude)
		return
	}
	by := ""
	if req.Claude {
		by = s.ownerSays(c)
	}
	inv, err := s.makeInvite(req.Name, by)
	if err != nil {
		s.gateDone(w, err)
		return
	}
	reply(w, http.StatusOK, inv)
}

// tokenInto writes tok's TOKEN into the file path (0600, in place of what is there) and takes
// it out of tok: no one is shown it. For extensions.go only (S1b removes it).
func (s *Server) tokenInto(tok *api.Token, path string) error {
	p, err := s.files.CheckPath(path)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(p), ".token-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.WriteString(tok.Token + "\n")
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(f.Name(), 0o600)
	}
	if err == nil {
		err = os.Rename(f.Name(), p)
	}
	if err != nil {
		return err
	}
	tok.Token = ""
	s.logf("server: the token of %s went into %s", tok.App.Name, p)
	return nil
}

// ask turns an agent's request for a secret into a confirmation for a person: 202 and no secret.
// owner (Claude): only a login of ir_web that has Claude, or the host, may allow it (answerConfirm).
func (s *Server) ask(w http.ResponseWriter, c caller, kind, name string, owner bool) {
	cf := s.asks.add(kind, name, owner, c.Name, c.session)
	s.logf("server: %s asks a panel for a %s (%s, owner %v), confirmation %s", c.Name, kind, name, owner, cf.ID)
	reply(w, http.StatusAccepted, api.Pending{Confirm: cf})
}

// errOwnerSecret answers allowing a secret-use from a login that has no Claude itself.
var errOwnerSecret = errors.New("only a person at the host or on a login of ir_web that has Claude lets a session use the credentials")

// errOwnerFlag answers giving Claude from a login that has no Claude itself.
var errOwnerFlag = errors.New("only a person at the host or on a login of ir_web that has Claude gives Claude")

// makeInvite makes a slot for a login of ir_web; claudeBy (ownerSays's, "" none): the login that
// takes it gets Claude, as that one said (joined).
func (s *Server) makeInvite(name, claudeBy string) (*api.Invite, error) {
	var perms []byte
	if claudeBy != "" {
		var err error
		if perms, err = json.Marshal(access.WithClaude(access.Default(), claudeBy, time.Now().UnixMilli())); err != nil {
			return nil, err
		}
	}
	inv, err := s.gate().CreateInvite(name, perms)
	if err != nil {
		return nil, err
	}
	out := &api.Invite{ID: inv.ID, Name: inv.Name, URL: inv.URL, ExpiresAt: ms(inv.ExpiresAt)}
	// The same link as a QR code, for a phone to open it; without one the link alone still works.
	if out.QR, err = qrSVG(inv.URL); err != nil {
		s.logf("server: the invite's QR code: %v", err)
	}
	return out, nil
}

// errClaudeFixed answers Claude for an app there already is without it: Claude is set as an app is
// made, never after (the user, 2026-11-08).
var errClaudeFixed = errors.New("claude is set as an app is made: this one has none, and a new TOKEN does not change that")

// makeToken gives app name its TOKEN; ownerBy (as ownerSays found it): a new app gets Claude too.
// One there already keeps what it had, Claude or none. For extensions.go only (S1b removes it).
func (s *Server) makeToken(name, ownerBy string) (*api.Token, error) {
	old, found := s.gate().Find(name)
	if found && ownerBy != "" && old.Kind == gate.KindChannel && !access.HasClaude(s.rulesOf(old.ID, false)) {
		return nil, errClaudeFixed
	}
	info, tok, err := s.gate().CreateToken(name)
	if err != nil {
		return nil, err
	}
	if ownerBy != "" && !found {
		if err := s.giveClaude(info, ownerBy); err != nil {
			return nil, err
		}
	}
	return &api.Token{App: s.appOf(info), Token: tok}, nil
}

// permsClaude: an invite's perms give Claude.
func permsClaude(perms []byte) bool {
	var r api.Rules
	return len(perms) > 0 && json.Unmarshal(perms, &r) == nil && access.HasClaude(r)
}

// giveClaude turns Claude on for app or login info: it is the owner's own, as by says.
func (s *Server) giveClaude(info gate.EndpointInfo, by string) error {
	r := s.rulesOf(info.ID, false)
	if access.HasClaude(r) {
		return nil
	}
	b, err := json.Marshal(access.WithClaude(r, by, time.Now().UnixMilli()))
	if err != nil {
		return err
	}
	if err := s.st.SetTerminalRules(info.ID, b); err != nil {
		return err
	}
	s.logf("server: claude for %s (%s), the owner's own (%s)", info.Name, info.ID, by)
	return nil
}

// joined is the gate's word that a login of ir_web registered: it takes its invite's perms; the
// first one is the owner's own and gets Claude.
func (s *Server) joined(j gate.Joined) {
	if j.First {
		if err := s.giveClaude(j.Endpoint, access.ByFirst); err != nil {
			s.logf("server: claude for the new login %s: %v", j.Endpoint.ID, err)
		}
		return
	}
	if len(j.Perms) == 0 {
		return
	}
	if err := s.st.SetTerminalRules(j.Endpoint.ID, j.Perms); err != nil {
		s.logf("server: the perms of the new login %s: %v", j.Endpoint.ID, err)
	}
}

func (s *Server) answerConfirm(w http.ResponseWriter, r *http.Request) {
	var a api.ConfirmAnswer
	if !readJSON(w, r, &a) {
		return
	}
	id := r.PathValue("id")
	c := callerOf(r)
	by := s.ownerSays(c)
	// An ask for Claude is allowed only by one that may say so; it stays waiting for one that may.
	// Denying it is anyone's who answers.
	cf, ok := s.asks.peek(id)
	// An executor allowed as the owner's own machine runs Claude: only one that may say so.
	if ok && (cf.Kind == "exec-pair" || cf.Kind == "bot-pair") && a.Allow && a.Owner && by == "" {
		forbid(w, errOwnerFlag)
		return
	}
	if ok && cf.Owner && a.Allow && by == "" {
		if cf.Kind == "secret-use" {
			forbid(w, errOwnerSecret)
		} else {
			forbid(w, errOwnerFlag)
		}
		return
	} else if ok && cf.Kind == "secret-set" && a.Allow && (a.Value == "" || len(a.Value) > secrets.MaxValue) {
		fail(w, http.StatusBadRequest, "type the value (64 KiB at most)")
		return
	}
	cf, ok = s.asks.take(id)
	if !ok {
		fail(w, http.StatusNotFound, "no such confirmation waiting")
		return
	}
	owner := by
	if !cf.Owner {
		by = ""
	}
	st := api.ConfirmState{ID: id, Kind: cf.Kind, Name: cf.Name, Owner: cf.Owner, State: "denied"}
	out := api.ConfirmReply{}
	if a.Allow {
		var err error
		switch cf.Kind {
		case "invite":
			out.Invite, err = s.makeInvite(cf.Name, by)
			if err == nil {
				st.Name, st.ExpiresAt = out.Invite.Name, out.Invite.ExpiresAt
			}
		case "secret-set":
			err = s.storeAsked(cf, a.Value, c)
		case "secret-use":
			s.allowSecretUse(cf, a.ForSession)
		case "exec-pair", "bot-pair":
			// The TOKEN goes to the asker alone, by its request; the panel never sees it.
			if !a.Owner {
				owner = ""
			}
			var tok *api.Token
			if tok, err = s.makePairedBot(cf.Name, owner, a.Replace && cf.Replace); err == nil {
				s.pairAllowed(id, tok.Token)
				st.Name = tok.App.Name
			}
		}
		st.State = "allowed"
		if err != nil {
			st.State, st.Error = "failed", err.Error()
		}
	}
	s.asks.finish(id, st)
	s.logf("server: %s answered confirmation %s: %s", c.Name, id, st.State)
	out.ConfirmState = st
	reply(w, http.StatusOK, out)
}

// confirmState is how a confirmation stands; ?wait=<seconds> (at most 60) waits for an answer.
func (s *Server) confirmState(w http.ResponseWriter, r *http.Request) {
	var d time.Duration
	if v := r.URL.Query().Get("wait"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			fail(w, http.StatusBadRequest, "bad wait")
			return
		}
		d = time.Duration(min(n, 60)) * time.Second
	}
	st, ok := s.asks.wait(r.Context(), r.PathValue("id"), d)
	if !ok {
		fail(w, http.StatusNotFound, "no such confirmation")
		return
	}
	reply(w, http.StatusOK, st)
}

func (s *Server) publicState(w http.ResponseWriter, r *http.Request) {
	st := api.PublicState{Origin: s.origin()}
	a := s.gate().Access()
	for _, e := range a.Endpoints {
		if e.Passkeys > 0 {
			st.Passkeys = true
		}
	}
	reply(w, http.StatusOK, st)
}

func (s *Server) origin() string {
	s.gmu.Lock()
	defer s.gmu.Unlock()
	return s.cfg.Public.Origin
}

// setPublic sets public.origin: a new gate takes over, every connection the old one let in is
// closed, and the config file says it for the next start.
func (s *Server) setPublic(w http.ResponseWriter, r *http.Request) {
	var req api.SetPublic
	if !readJSON(w, r, &req) {
		return
	}
	if err := s.SetPublicOrigin(req.Origin); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	s.logf("server: %s set public.origin to %q", callerOf(r).Name, req.Origin)
	s.publicState(w, r)
}

// SetPublicOrigin opens the gate for origin ("" is this machine only) in place of the one there.
func (s *Server) SetPublicOrigin(origin string) error {
	s.gmu.Lock()
	defer s.gmu.Unlock()
	g, err := s.newGate(origin)
	if err != nil {
		return err
	}
	if err := s.SaveConfig(func(c *config.Config) { c.Public.Origin = origin }); err != nil {
		g.Close()
		return err
	}
	old := s.g.Swap(g)
	old.CloseAll()
	old.Close()
	return nil
}

// gateDone answers a gate call's outcome.
func (s *Server) gateDone(w http.ResponseWriter, err error) {
	var ne *gate.NameError
	switch {
	case err == nil:
		reply(w, http.StatusOK, map[string]any{"ok": true})
	case errors.As(err, &ne):
		reply(w, http.StatusConflict, map[string]string{"error": err.Error(), "nameError": ne.Code})
	case errors.Is(err, gate.ErrNotFound):
		fail(w, http.StatusNotFound, err.Error())
	case errors.Is(err, gate.ErrLastPasskey), errors.Is(err, gate.ErrNotPublic), errors.Is(err, gate.ErrLocal), errors.Is(err, errClaudeFixed):
		fail(w, http.StatusConflict, err.Error())
	default:
		s.error(w, err)
	}
}
