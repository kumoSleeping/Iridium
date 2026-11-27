package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/kumoSleeping/iridium/internal/access"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/config"
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
	// script: the script of a channel the local ir runs in ("<channel>/<name>", HeaderScript).
	script string
	// far: the computer's app whose session TOKEN it came by (withAgent).
	far string
	// web: the browser's session of ir_web (webapp.SessionOf) the request came in.
	web string
}

// panel reports whether the caller may approve apps, widen permissions and see secrets: a person
// on a login of ir_web, or at the host (ir, or the page with no public origin). Until S2: what 0.1
// let a panel and 本机 do.
func (c caller) panel() bool { return !c.agent && (c.Web() || c.Host()) }

// byAgent reports whether an agent sent it, not a person: its ir (the agent flag), or anything
// that names the session it runs in (a session's own TOKEN, ir inside a session).
func (c caller) byAgent() bool { return c.agent || c.session != "" }

// machine reports whether a person at the host sent it: ir over the socket, not run by an agent.
func (c caller) machine() bool { return c.socket && c.Host() && !c.agent }

type callerKey struct{}

func callerOf(r *http.Request) caller {
	if c, ok := r.Context().Value(callerKey{}).(caller); ok {
		return c
	}
	id, _ := gate.FromContext(r.Context())
	// A session's own TOKEN: its agent's ir, at the machine (docs/exec.md "远端的 ir").
	c := caller{Identity: id, agent: id.Session != "", session: id.Session}
	if id.Web() {
		c.web = webapp.SessionOf(r.Context())
	}
	return c
}

var localIdentity = gate.HostIdentity

// withAgent makes a session's own TOKEN (its agent's ir at another computer, docs/exec.md "远端的
// ir") the caller an agent's ir on the host's socket is: the host, or the login its session's
// turn is for (kumo, 2026-11-18: not its computer's app, whose rights kept it from the host, the
// other computers and the sessions it works with). Not the socket's: what only reads the host's
// own paths, and the credentials, stay the socket's.
func (s *Server) withAgent(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := gate.FromContext(r.Context())
		if !ok || id.Session == "" {
			next.ServeHTTP(w, r)
			return
		}
		c := caller{Identity: localIdentity, agent: true, session: id.Session, far: id.App}
		if o, ok := s.originOf(c.session); ok && !o.Host() {
			c.Identity = o
		}
		c.Identity.Session = id.Session
		next.ServeHTTP(w, r.WithContext(context.WithValue(gate.NewContext(r.Context(), c.Identity), callerKey{}, c)))
	})
}

// socketHandler serves the local ir, past the gate: the host, or, when an agent's ir names the
// session it runs in and an app or login opened that session, that one. What an agent does
// through ir is its session's opener's doing: its permissions, its usage.
func (s *Server) socketHandler(mux http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := caller{Identity: localIdentity, socket: true, agent: r.Header.Get(HeaderAgent) != "", session: r.Header.Get(HeaderSession)}
		// A script's ir: no agent's session, whatever the environment it was run in said.
		if sc := r.Header.Get(HeaderScript); sc != "" {
			if x, ok := s.scriptOfCaller(caller{script: sc}); ok {
				c = caller{Identity: localIdentity, socket: true, agent: true, script: x.Channel + "/" + x.Name}
			}
		}
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
	core := s.gate().Wrap(s.withLogin(s.withAgent(s.withSatori(webui.Gzip(mux)))))
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
		LocalExecToken: s.matchLocalExec,
		OnRevoke:       s.revoked,
		OnTerminals:    s.terminalsChanged,
		Open:           openPath,
		LoginNameTaken: func(name string) bool { w := s.web(); return w != nil && w.NameTaken(name) },
		Host:           hostStore{s},
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
	// public.localWindow as the file has it now: the desktop app may have set it since the start.
	localWindow := s.cfg.Public.LocalWindow
	if s.ConfigPath != "" {
		if c, err := config.Load(s.ConfigPath); err == nil {
			localWindow = c.Public.LocalWindow
		}
	}
	return webapp.New(webapp.Config{
		PublicOrigin: origin,
		LocalWindow:  localWindow,
		Core:         s.g,
		Token:        s.webToken,
		Store:        s.st.Web(),
		StateDir:     s.cfg.StatePath(),
		Hosts:        s.cfg.Hosts,
		OnJoin:       s.joined,
		OnPasskey:    func(string) { s.terminalsChanged() },
		Open:         openPath,
		Logger:       s.gateLog(),
	})
}

// terminalsChanged tells the pages the apps changed (an app's avatar came, a login renamed, its
// computer connected): they fetch GET /v1/internal/apps again. The key "terminals" carries a count,
// nothing of them.
func (s *Server) terminalsChanged() {
	s.hub.Set("terminals", s.terminalsSeq.Add(1))
}

// revoked forgets a revoked app's or login's web sessions and its machine. An executor's
// connection ends now, not when it next connects.
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
		if d := s.deviceOfCaller(c); d != "" {
			return session.FromDevice(c.ID(), d)
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

// using is session id if the caller may use it (start, write to, answer, steer): one whose agent
// and folder its Perms allow. Every terminal sees every session.
func (s *Server) using(c caller, id string) (*api.SessionState, error) {
	st, err := s.m.State(id)
	if err != nil {
		return nil, err
	}
	if !s.seenBy(c, st.Session) {
		return nil, errNoSession
	}
	if !c.Host() || c.far != "" {
		if st.Bare {
			return st, nil // no agent of its own to have the right to: whoever sees it uses it
		}
		err = s.checkPerms(c, st.Agent, st.Machine, st.Cwd)
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
		// To change anything of it, one of its channel's people (docs/channels.md "人").
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if err := s.inChannel(callerOf(r), r.PathValue("id")); err != nil {
				s.error(w, err)
				return
			}
		}
		h(w, r)
	}
}

// stopping lets h stop session {id}'s turn for a caller that may use it, or for the agent of a
// session in a channel with it: one agent here stops another going round or off track (kumo,
// 2026-11-26), what it may not otherwise touch.
func (s *Server) stopping(h http.HandlerFunc) http.HandlerFunc {
	use := s.use(h)
	return func(w http.ResponseWriter, r *http.Request) {
		if c := callerOf(r); s.peers(c.session, r.PathValue("id")) {
			h(w, r)
			return
		}
		use(w, r)
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

// checkCreate checks a new session: its agent and folder within the caller's Perms.
func (s *Server) checkCreate(c caller, req *api.NewSession) error {
	if err := s.checkMachine(req); err != nil {
		return err
	}
	if c.Host() && c.far == "" {
		return nil
	}
	app := gate.HostID
	if req.Machine != "" {
		if info, ok := s.gate().Find(req.Machine); ok {
			app = info.ID
		}
	}
	return s.checkPerms(c, req.Agent, app, req.Cwd)
}

// checkMachine: a session at another machine runs no agent turned off there (its app's page),
// whoever asks.
func (s *Server) checkMachine(req *api.NewSession) error {
	if req.Machine == "" {
		return nil
	}
	info, ok := s.gate().Find(req.Machine)
	if !ok || info.Kind != gate.KindChannel || info.Host() {
		return fmt.Errorf("no machine %q", req.Machine)
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

// deviceOf is the device id is on: a login of ir_web's, that of its browser seen last (the
// passkey it logged in with); else what it set, else a computer.
func (s *Server) deviceOf(id string) api.Device {
	if w := s.web(); w != nil {
		if d := api.Device(w.LoginDevice(id)); d.Valid() {
			return d
		}
	}
	if d, _ := s.st.Device(id); api.Device(d).Valid() {
		return api.Device(d)
	}
	return api.DeviceComputer
}

// deviceOfCaller is the device a person's browser of ir_web is on (its passkey's), "" for any
// other caller or a browser logged in before passkeys were told apart.
func (s *Server) deviceOfCaller(c caller) string {
	w := s.web()
	if c.web == "" || c.byAgent() || w == nil {
		return ""
	}
	return w.SessionDevice(c.web)
}

// appOf is an app (or the host) as the API lists it, with today's use.
func (s *Server) appOf(info gate.EndpointInfo) api.App {
	a := api.App{
		ID: info.ID, Name: info.Name, Type: info.Type, Avatar: info.Avatar,
		Online: info.Online > 0 || info.Host(), Connections: info.Online, CreatedAt: ms(info.CreatedAt), LastSeenAt: ms(info.LastSeenAt),
		UsedToday: s.usedToday(info.ID),
	}
	if info.Host() {
		a.Host, a.Device = true, s.deviceOf(info.ID)
		a.Named = s.gate().HostNamed()
	}
	return a
}

// loginOf is a login of ir_web as the API lists it.
func (s *Server) loginOf(info gate.EndpointInfo) api.Login {
	passkeys, sessions := s.web().Counts(info.ID)
	return api.Login{
		App: gate.WebApp, ID: info.ID, Name: info.Name, Avatar: info.Avatar, Passkeys: passkeys,
		Sessions: sessions, Online: info.Online > 0, Connections: info.Online, CreatedAt: ms(info.CreatedAt), LastSeenAt: ms(info.LastSeenAt),
		ApprovedAt: ms(info.ApprovedAt), UsedToday: s.usedToday(info.ID), Device: s.deviceOf(info.ID),
		Perms: s.loginPermsOf(gate.WebApp, info.ID),
	}
}

// meOf is the caller as GET /v1/internal/me has it.
func (s *Server) meOf(c caller, info gate.EndpointInfo) api.Me {
	id := c.Identity
	m := api.Me{ID: info.ID, Name: info.Name, App: id.App, Login: id.Login, Avatar: info.Avatar, Host: id.Host(), Web: id.Web(),
		UsedToday: s.usedToday(info.ID), Public: s.web().Public()}
	if id.Host() || id.Web() {
		m.Device = s.deviceOf(info.ID) // an app has none (k_glossary "电脑")
	}
	m.Named = id.Host() && s.gate().HostNamed()
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
