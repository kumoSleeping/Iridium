package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/kumoSleeping/iridium/internal/access"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/gate"
	"github.com/kumoSleeping/iridium/internal/satori"
	"github.com/kumoSleeping/iridium/internal/session"
	"github.com/kumoSleeping/iridium/internal/webapp"
	"github.com/kumoSleeping/iridium/internal/webui"
)

// Headers the local ir sends over the unix socket. HeaderAgent: an agent ran it (its environment
// says so, or it has no terminal), so no secret may go back: what needs one becomes a
// confirmation a panel answers. HeaderSession: the session it ran in (IRIDIUM_SESSION, or the
// agent's own session variable).
const (
	HeaderAgent   = "X-Iridium-Agent"
	HeaderSession = "X-Iridium-Session"
)

// caller is who sent a request: the gate's identity, and for the socket whether an agent ran ir.
type caller struct {
	gate.Identity
	socket  bool
	agent   bool
	session string
}

// panel reports whether the caller may approve apps, widen permissions and see secrets: a person
// on a login of ir_web, or at the host (ir, or the page with no public origin). Until S2: what 0.1
// let a panel and 本机 do.
func (c caller) panel() bool { return !c.agent && (c.Web() || c.Host()) }

// machine reports whether a person at the host sent it: ir over the socket, not run by an agent.
func (c caller) machine() bool { return c.socket && c.Host() && !c.agent }

// ownerSays is how the caller may say a terminal is the owner's own and give it Claude (Claude's
// terms: the owner's own terminals only), "" if it may not: a person at the machine (ByLocal), or
// a person on a passkey panel that has Claude, being the owner's own itself (ByPanel). Never an
// agent, not even in a session such a panel opened: its --owner waits for one of these to confirm
// it. Nor the local page (no public origin): anything on the machine reaches it, an agent too.
func (s *Server) ownerSays(c caller) string {
	switch {
	case c.agent:
		return ""
	case c.machine():
		return access.ByLocal
	case c.panel() && c.Web() && access.HasClaude(s.rulesFor(c)):
		return access.ByPanel
	}
	return ""
}

type callerKey struct{}

func callerOf(r *http.Request) caller {
	if c, ok := r.Context().Value(callerKey{}).(caller); ok {
		return c
	}
	id, _ := gate.FromContext(r.Context())
	// A session's own TOKEN: its agent's ir, at the machine (docs/exec.md "远端的 ir").
	return caller{Identity: id, agent: id.Session != "", session: id.Session}
}

var localIdentity = gate.HostIdentity

// socketHandler serves the local ir, past the gate: the host, or, when an agent's ir names the
// session it runs in and an app or login opened that session, that one. What an agent does
// through ir is its session's opener's doing: its permissions, its usage.
func (s *Server) socketHandler(mux http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := caller{Identity: localIdentity, socket: true, agent: r.Header.Get(HeaderAgent) != "", session: r.Header.Get(HeaderSession)}
		// An agent's ir acts for its session's turn: as the login that turn is for (origin.go).
		if c.session != "" {
			if o, ok := s.originOf(c.session); ok && !o.Host() {
				c.Identity = o
				c.agent = true
			}
		}
		ctx := context.WithValue(gate.NewContext(r.Context(), c.Identity), callerKey{}, c)
		mux.ServeHTTP(w, r.WithContext(ctx))
	})
}

// webHandler serves the TCP address: ir_web (package webapp, whichever is in place now) in front of
// the core. The core is the gate (TOKEN only, or the host by gate.AsHost), the login an app speaks
// as, Satori, and the API; ir_web serves its own pages under /_ir/ and the web app (package webui,
// or Server.App) itself, and forwards a logged-in browser as ir_web speaking for its login.
func (s *Server) webHandler(mux http.Handler) http.Handler {
	app := s.App
	if app == nil {
		app = webui.Handler()
	}
	core := s.gate().Wrap(s.withLogin(s.withSatori(webui.Gzip(mux))))
	return legacy(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.web().Handler(core, app).ServeHTTP(w, r)
	}))
}

func (s *Server) gate() *gate.Gate { return s.g }

func (s *Server) web() *webapp.Web { return s.w.Load() }

func (s *Server) gateLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(logWriter{s.logf}, nil))
}

// openGate opens the core's gate over the store, and ir_web's TOKEN with it.
func (s *Server) openGate() error {
	g, err := gate.New(gate.Config{
		Store:          s.st.Gate(),
		SessionToken:   s.sessionToken,
		OnRevoke:       s.revoked,
		OnTerminals:    s.terminalsChanged,
		Open:           openPath,
		LoginNameTaken: func(name string) bool { w := s.web(); return w != nil && w.NameTaken(name) },
		Logger:         s.gateLog(),
	})
	if err != nil {
		return err
	}
	s.g = g
	s.webToken, err = webapp.AppToken(g, s.cfg.StatePath())
	return err
}

// newWeb opens ir_web for origin ("" is off: this machine only) over the store.
func (s *Server) newWeb(origin string) (*webapp.Web, error) {
	return webapp.New(webapp.Config{
		PublicOrigin: origin,
		Core:         s.g,
		Token:        s.webToken,
		Store:        s.st.Web(),
		StateDir:     s.cfg.StatePath(),
		Hosts:        s.cfg.Hosts,
		OnJoin:       s.joined,
		Open:         openPath,
		Viewer:       http.HandlerFunc(s.viewer),
		Logger:       s.gateLog(),
	})
}

// terminalsChanged tells the pages the apps changed (an app's avatar came, a login renamed, its
// computer connected): they fetch GET /v1/internal/apps again. The key "terminals" carries a count,
// nothing of them.
func (s *Server) terminalsChanged() {
	s.hub.Set("terminals", s.terminalsSeq.Add(1))
}

// revoked forgets a revoked app's or login's web sessions, browsers' push subscriptions and its
// machine. An executor's connection ends now, not when it next connects.
func (s *Server) revoked(id string) {
	if w := s.web(); w != nil {
		w.Forget(id)
	}
	if s.Remote != nil {
		s.Remote.Drop(id)
	}
	if s.st == nil {
		return
	}
	if err := s.st.DeleteTerminalPush(id); err != nil {
		s.logf("server: dropping %s's push subscriptions: %v", id, err)
	}
	if err := s.st.ForgetMachine(id); err != nil {
		s.logf("server: forgetting %s's machine: %v", id, err)
	}
}

type logWriter struct{ logf func(string, ...any) }

func (l logWriter) Write(b []byte) (int, error) {
	l.logf("%s", strings.TrimRight(string(b), "\n"))
	return len(b), nil
}

// fromOf is who a message is from, for session.Manager's Send and Create: the caller's terminal;
// for ir on the socket run inside a session (the X-Iridium-Session header, else the request's
// caller), the terminal and that session, which the agent sees as "[ir:<session>]" and the page
// draws as a letter from that session. The terminal (本机, or the one that opened the session)
// still counts the message and its usage. Whether the agent flag is set does not matter: ir send
// does not send it, and a session in the environment is an agent's.
func (s *Server) fromOf(c caller, callerSession string) string {
	if !c.socket && c.session == "" {
		// A login of an app (not ir_web's): counted to the app, the login kept with the message.
		if c.Login != "" && !c.Web() && !c.Host() {
			return session.FromLogin(c.App, c.Login, sourceTag(satori.Caller{AppName: s.Tag(c.App), LoginName: c.Name}))
		}
		return c.ID()
	}
	from := c.session
	if from == "" {
		from = callerSession
	}
	if from == "" || strings.ContainsAny(from, "[] \n") {
		return c.ID()
	}
	return session.From(c.ID(), "ir:"+from)
}

// Tag is the name terminal goes by in source tags: its name now.
func (s *Server) Tag(terminal string) string {
	if info, ok := s.gate().Endpoint(terminal); ok {
		return info.Name
	}
	return ""
}

// rulesFor is what the caller may do: Claude or not.
func (s *Server) rulesFor(c caller) api.Rules { return s.rulesOf(c.ID(), c.Host()) }

func (s *Server) rulesOf(id string, local bool) api.Rules {
	if local || id == gate.HostID {
		return access.Full()
	}
	if b, ok, err := s.st.TerminalRules(id); ok && err == nil {
		var r api.Rules
		if json.Unmarshal(b, &r) == nil {
			return r
		}
	}
	return access.Default()
}

// startOfDay is local midnight today, in ms.
func startOfDay() int64 {
	y, m, d := time.Now().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.Local).UnixMilli()
}

func (s *Server) usedToday(id string) int64 {
	n, _ := s.st.TokensSince(id, startOfDay())
	return n
}

// forbid refuses what the caller may not do: 403 no_permission.
func forbid(w http.ResponseWriter, err error) bool {
	refuse(w, http.StatusForbidden, "no_permission", err.Error())
	return false
}

// using is session id if the caller may use it: any but a Claude session, which only the owner's
// own terminals may (start, write to, answer, steer). Every terminal sees every session.
func (s *Server) using(c caller, id string) (*api.SessionState, error) {
	st, err := s.m.State(id)
	if err != nil {
		return nil, err
	}
	if !c.Host() {
		if !s.sees(s.permsFor(c), st.Session) {
			return nil, errNoSession
		}
		if err = access.Agent(s.rulesFor(c), st.Agent); err == nil {
			err = s.checkPerms(c, st.Agent, st.Machine, st.Cwd)
		}
	}
	return st, err
}

// use lets h act on session {id} only for a caller that may use it.
func (s *Server) use(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, err := s.using(callerOf(r), r.PathValue("id")); err != nil {
			s.error(w, err)
			return
		}
		h(w, r)
	}
}

// manages lets h through for panels and 本机 only.
func (s *Server) manages(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !callerOf(r).CanManage() {
			s.error(w, access.ErrManage)
			return
		}
		h(w, r)
	}
}

// checkCreate checks a new session: Claude's only for the owner's own.
func (s *Server) checkCreate(c caller, req *api.NewSession) error {
	if err := s.checkMachine(req); err != nil {
		return err
	}
	if c.Host() {
		return nil
	}
	if err := access.Agent(s.rulesFor(c), req.Agent); err != nil {
		return err
	}
	app := gate.HostID
	if req.Machine != "" {
		if info, ok := s.gate().Find(req.Machine); ok {
			app = info.ID
		}
	}
	return s.checkPerms(c, req.Agent, app, req.Cwd)
}

// checkMachine: Claude runs at another machine only if it was allowed as the owner's own
// (docs/exec.md "连接"), and no agent turned off there (its app's page), whoever asks.
func (s *Server) checkMachine(req *api.NewSession) error {
	if req.Machine == "" {
		return nil
	}
	info, ok := s.gate().Find(req.Machine)
	if !ok || info.Kind != gate.KindChannel || info.Host() {
		return fmt.Errorf("no machine %q", req.Machine)
	}
	if err := access.Agent(s.rulesOf(info.ID, false), req.Agent); err != nil {
		return err
	}
	return s.agentOff(info.ID, req.Agent)
}

// checkSend checks a message for session id.
func (s *Server) checkSend(c caller, id string) error {
	_, err := s.using(c, id)
	return err
}

// mayWatch checks the states an event stream asks for. It has answered when false.
func (s *Server) mayWatch(w http.ResponseWriter, c caller, keys []string) bool {
	for _, k := range keys {
		if (k == "needs" || k == "pairing") && !c.panel() {
			return forbid(w, errors.New("only a person on ir_web or at the host sees what waits for one"))
		}
	}
	return true
}

// deviceOf is the device id is on: what it set, else a computer.
func (s *Server) deviceOf(id string) api.Device {
	if d, _ := s.st.Device(id); api.Device(d).Valid() {
		return api.Device(d)
	}
	return api.DeviceComputer
}

// appOf is an app (or the host) as the API lists it, with Claude or not and today's use.
func (s *Server) appOf(info gate.EndpointInfo) api.App {
	a := api.App{
		ID: info.ID, Name: info.Name, Type: info.Type, Avatar: info.Avatar, Online: info.Online,
		CreatedAt: ms(info.CreatedAt), LastSeenAt: ms(info.LastSeenAt),
		Rules: s.rulesOf(info.ID, false), UsedToday: s.usedToday(info.ID),
	}
	if info.Host() {
		a.Host, a.Rules, a.Device = true, access.Full(), s.deviceOf(info.ID)
	}
	return a
}

// loginOf is a login of ir_web as the API lists it.
func (s *Server) loginOf(info gate.EndpointInfo) api.Login {
	passkeys, sessions := s.web().Counts(info.ID)
	return api.Login{
		App: gate.WebApp, ID: info.ID, Name: info.Name, Avatar: info.Avatar, Passkeys: passkeys,
		Sessions: sessions, Online: info.Online, CreatedAt: ms(info.CreatedAt), LastSeenAt: ms(info.LastSeenAt),
		ApprovedAt: ms(info.ApprovedAt), Rules: s.rulesOf(info.ID, false), UsedToday: s.usedToday(info.ID), Device: s.deviceOf(info.ID),
		Perms: s.loginPermsOf(gate.WebApp, info.ID),
	}
}

// meOf is the caller as GET /v1/internal/me has it.
func (s *Server) meOf(c caller, info gate.EndpointInfo) api.Me {
	id := c.Identity
	m := api.Me{ID: info.ID, Name: info.Name, App: id.App, Login: id.Login, Avatar: info.Avatar, Host: id.Host(), Web: id.Web(),
		Rules: s.rulesOf(info.ID, id.Host()), UsedToday: s.usedToday(info.ID), Public: s.web().Public()}
	if id.Host() || id.Web() {
		m.Device = s.deviceOf(info.ID) // an app has none (k_glossary "电脑")
	}
	return m
}

func ms(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

// sessionToken is the gate's Config.SessionToken: the hosted session whose own TOKEN it is, and
// the Bot of the machine it runs at.
func (s *Server) sessionToken(tok string) (endpointID, session string, ok bool) {
	if s.tokenFor != nil {
		return s.tokenFor(tok)
	}
	if s.m == nil {
		return "", "", false
	}
	return s.m.SessionToken(tok)
}
