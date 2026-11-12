package server

import (
	"bytes"
	"cmp"
	"errors"
	"net/http"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/gate"
	"github.com/kumoSleeping/iridium/internal/store"
	"github.com/kumoSleeping/iridium/internal/webapp"
	"github.com/kumoSleeping/iridium/internal/webui"
)

// A session's public link (docs/api.md "公开链接"): anyone with it reads what was said in that
// session, people's words and the agent's, and nothing else; no one sends through it. A person
// who may use the session makes one (a new one replaces it) or revokes it; it works only while
// the public address is set, so the Passkey gate is on for everything else. Only the token's hash
// is kept. The view (package webapp's Viewer, /p/<token>) is the page, its messages, the pictures
// they gave and the page's own files; no other route takes the token.

const (
	// publicPageMessages: a page of the view holds about this many messages, read back over the
	// transcript's pages till it does (publicPages at most).
	publicPageMessages = 30
	publicPages        = 20
	// publicPictureSide: a picture's longest side as the view gets it (re-encoded).
	publicPictureSide = 2048
)

func (s *Server) publicRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/internal/sessions/{id}/public", s.publicGet)
	mux.HandleFunc("POST /v1/internal/sessions/{id}/public", s.publicMake)
	mux.HandleFunc("DELETE /v1/internal/sessions/{id}/public", s.publicRevoke)
}

// publicReady: the public address is set (ir_web on, every other web request needs a passkey).
func (s *Server) publicReady() bool {
	w := s.web()
	return w != nil && w.Public()
}

// publicOwner is session {id} if the caller may make it public: a person (not an agent's ir) who
// may use it.
func (s *Server) publicOwner(w http.ResponseWriter, r *http.Request) (*api.SessionState, bool) {
	c := callerOf(r)
	if c.agent {
		forbid(w, errors.New("an agent's ir may not make a session public"))
		return nil, false
	}
	st, err := s.using(c, r.PathValue("id"))
	if err != nil {
		s.error(w, err)
		return nil, false
	}
	return st, true
}

func (s *Server) publicGet(w http.ResponseWriter, r *http.Request) {
	st, ok := s.publicOwner(w, r)
	if !ok {
		return
	}
	l, on, err := s.st.PublicLinkOf(st.ID)
	if err != nil {
		s.fault(w, err)
		return
	}
	out := api.PublicLink{Ready: s.publicReady(), On: on}
	if on {
		out.By, out.CreatedAt = l.Creator, l.CreatedAt
	}
	reply(w, http.StatusOK, out)
}

func (s *Server) publicMake(w http.ResponseWriter, r *http.Request) {
	st, ok := s.publicOwner(w, r)
	if !ok {
		return
	}
	web := s.web()
	if web == nil || !web.Public() {
		refuse(w, http.StatusForbidden, "not_public", webapp.ErrNotPublic.Error())
		return
	}
	token := gate.NewSecret()
	l := store.PublicLink{Session: st.ID, Hash: gate.HashSecret(token), Creator: callerOf(r).ID(), CreatedAt: time.Now().UnixMilli()}
	if err := s.st.SetPublicLink(l); err != nil {
		s.fault(w, err)
		return
	}
	reply(w, http.StatusOK, api.PublicLink{Ready: true, On: true, URL: web.Origin() + webapp.ViewerPrefix + token, By: l.Creator, CreatedAt: l.CreatedAt})
}

func (s *Server) publicRevoke(w http.ResponseWriter, r *http.Request) {
	st, ok := s.publicOwner(w, r)
	if !ok {
		return
	}
	if _, err := s.st.DeletePublicLink(st.ID); err != nil {
		s.fault(w, err)
		return
	}
	reply(w, http.StatusOK, map[string]any{"ok": true})
}

// viewer serves /p/<token>[/…] (webapp.Config.Viewer): the page, api/messages, files/<at>/<ref>
// (a picture a message gave), and the page's own files (assets/…, theme.js). An unknown or
// revoked token, a session gone, or the public address off: 404, the same for each.
func (s *Server) viewer(w http.ResponseWriter, r *http.Request) {
	token, sub, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, webapp.ViewerPrefix), "/")
	l, ok := s.publicLinkFor(token)
	if !ok {
		publicNotFound(w)
		return
	}
	switch {
	case sub == "":
		s.viewerPage(w, r, token)
	case sub == "api/messages":
		s.viewerMessages(w, r, l, token)
	case strings.HasPrefix(sub, "files/"):
		at, ref, ok := strings.Cut(strings.TrimPrefix(sub, "files/"), "/")
		if !ok || at == "" || ref == "" || strings.Contains(ref, "/") {
			publicNotFound(w)
			return
		}
		s.viewerPicture(w, r, l, at, ref)
	case sub == "theme.js" || strings.HasPrefix(sub, "assets/") && path.Clean("/"+sub) == "/"+sub && strings.Count(sub, "/") == 1:
		r2 := r.Clone(r.Context())
		r2.URL.Path, r2.URL.RawPath = "/"+sub, ""
		s.app().ServeHTTP(w, r2)
	default:
		publicNotFound(w)
	}
}

// publicLinkFor is the link token opens, if the public address is set and its session is there.
func (s *Server) publicLinkFor(token string) (store.PublicLink, bool) {
	if !s.publicReady() || !gate.WellFormedSecret(token) {
		return store.PublicLink{}, false
	}
	l, ok, err := s.st.PublicLinkByHash(gate.HashSecret(token))
	if err != nil || !ok {
		return store.PublicLink{}, false
	}
	if _, err := s.m.State(l.Session); err != nil {
		return store.PublicLink{}, false
	}
	return l, true
}

func publicNotFound(w http.ResponseWriter) {
	refuse(w, http.StatusNotFound, "not_found", "no such link")
}

func (s *Server) app() http.Handler {
	if s.App != nil {
		return s.App
	}
	return webui.Handler()
}

// viewerPage is the web app's view.html (web/view.html, src/view.tsx), its files under the link.
func (s *Server) viewerPage(w http.ResponseWriter, r *http.Request, token string) {
	var page bufWriter
	r2, err := http.NewRequestWithContext(r.Context(), http.MethodGet, "/view.html", nil)
	if err != nil {
		publicNotFound(w)
		return
	}
	s.app().ServeHTTP(&page, r2)
	if page.status != http.StatusOK {
		fail(w, http.StatusServiceUnavailable, "the web app is not built")
		return
	}
	under := webapp.ViewerPrefix + token + "/"
	html := bytes.ReplaceAll(page.body.Bytes(), []byte(`"/assets/`), []byte(`"`+under+`assets/`))
	html = bytes.ReplaceAll(html, []byte(`"/theme.js"`), []byte(`"`+under+`theme.js"`))
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Security-Policy", webui.CSP)
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(html)
	}
}

// bufWriter keeps a response in memory.
type bufWriter struct {
	h      http.Header
	status int
	body   bytes.Buffer
}

func (b *bufWriter) Header() http.Header {
	if b.h == nil {
		b.h = http.Header{}
	}
	return b.h
}

func (b *bufWriter) WriteHeader(code int) {
	if b.status == 0 {
		b.status = code
	}
}

func (b *bufWriter) Write(p []byte) (int, error) {
	b.WriteHeader(http.StatusOK)
	return b.body.Write(p)
}

// said reports whether b is something said: a person's words or the agent's, not its thinking,
// a tool, or a line of the system's.
func said(b api.Block) bool {
	return b.Kind == api.BlockKindUser || b.Kind == api.BlockKindAssistant
}

// publicMessage is said block b of session id as the view gets it, credentials masked; nil if
// it says nothing.
func (s *Server) publicMessage(id, token string, b api.Block, local bool) *api.PublicMessage {
	text := b.Text
	if b.Length > len([]rune(text)) {
		if full, err := s.m.Full(id, b.At); err == nil {
			text = full
		}
	}
	m := api.PublicMessage{At: b.At, Time: b.Time, Role: "agent", Text: s.MaskText(text)}
	if b.Kind == api.BlockKindUser {
		from := b.From
		switch {
		case from == "Iridium": // Iridium's own word to the agent, not a person's
			return nil
		case strings.HasPrefix(from, "ir:"), strings.HasPrefix(from, "session:"): // another session's ir: not its ID
			from = "ir"
		}
		m.Role, m.From = "user", s.MaskText(from)
	}
	if local {
		for _, f := range b.Files {
			if publicPicture(f) {
				m.Pictures = append(m.Pictures, api.PublicImage{URL: webapp.ViewerPrefix + token + "/files/" + b.At + "/" + f.Ref, Name: s.MaskText(f.Name), W: f.W, H: f.H})
			}
		}
	}
	if strings.TrimSpace(m.Text) == "" && len(m.Pictures) == 0 {
		return nil
	}
	return &m
}

// publicPicture: f is a picture the view may show (re-encoded; not an SVG, which can hold script).
func publicPicture(f api.FileRef) bool {
	return f.Ref != "" && f.Kind == api.FileKindImage && f.Mime != "image/svg+xml"
}

// viewerMessages is GET /p/<token>/api/messages: the latest page, ?before=<offset> the one
// before, ?after=<offset> what came after (api.PublicPage).
func (s *Server) viewerMessages(w http.ResponseWriter, r *http.Request, l store.PublicLink, token string) {
	st, err := s.m.State(l.Session)
	if err != nil {
		publicNotFound(w)
		return
	}
	offset := func(k string) (int64, bool) {
		v := r.URL.Query().Get(k)
		if v == "" {
			return -1, true
		}
		n, err := strconv.ParseInt(v, 10, 64)
		return n, err == nil && n >= 0
	}
	before, ok1 := offset("before")
	after, ok2 := offset("after")
	if !ok1 || !ok2 || before >= 0 && after >= 0 {
		fail(w, http.StatusBadRequest, "before or after: an offset")
		return
	}
	id, local := st.ID, st.Machine == ""
	out := api.PublicPage{Title: cmp.Or(st.Name, st.Title), Agent: st.Agent, Status: st.Status, Messages: []api.PublicMessage{}}
	keep := func(bs []api.Block) []api.PublicMessage {
		var ms []api.PublicMessage
		for _, b := range bs {
			if !said(b) {
				continue
			}
			if m := s.publicMessage(id, token, b, local); m != nil {
				ms = append(ms, *m)
			}
		}
		return ms
	}
	b := satoriBackend{s}
	if after >= 0 {
		out.End = after
		for range publicPages {
			p, err := b.Messages(id, -1, out.End)
			if err != nil {
				s.fault(w, err)
				return
			}
			out.Messages = append(out.Messages, keep(p.Blocks)...)
			if p.End <= out.End {
				break
			}
			out.End = p.End
		}
		reply(w, http.StatusOK, out)
		return
	}
	at := before
	for i := range publicPages {
		p, err := b.Messages(id, at, -1)
		if err != nil {
			s.fault(w, err)
			return
		}
		if i == 0 {
			out.End = p.End
		}
		out.Messages = append(keep(p.Blocks), out.Messages...)
		out.Before = ""
		if !p.More || p.Start >= at && at >= 0 {
			break
		}
		at, out.Before = p.Start, strconv.FormatInt(p.Start, 10)
		if len(out.Messages) >= publicPageMessages {
			break
		}
	}
	reply(w, http.StatusOK, out)
}

// viewerPicture is GET /p/<token>/files/<at>/<ref>: a picture said block at gave, re-encoded
// (publicPictureSide); only one of the link's session at this machine.
func (s *Server) viewerPicture(w http.ResponseWriter, r *http.Request, l store.PublicLink, at, ref string) {
	st, err := s.m.State(l.Session)
	if err != nil || st.Machine != "" {
		publicNotFound(w)
		return
	}
	off, _, _ := strings.Cut(at, ".")
	n, err := strconv.ParseInt(off, 10, 64)
	if err != nil || n < 0 {
		publicNotFound(w)
		return
	}
	p, err := satoriBackend{s}.Messages(st.ID, -1, n)
	if err != nil {
		publicNotFound(w)
		return
	}
	i := slices.IndexFunc(p.Blocks, func(b api.Block) bool { return b.At == at })
	if i < 0 || !said(p.Blocks[i]) || !slices.ContainsFunc(p.Blocks[i].Files, func(f api.FileRef) bool { return f.Ref == ref && publicPicture(f) }) {
		publicNotFound(w)
		return
	}
	file, err := s.files.Lookup(st.ID, ref)
	if err != nil {
		publicNotFound(w)
		return
	}
	s.files.Thumb(w, r, file, publicPictureSide)
}
