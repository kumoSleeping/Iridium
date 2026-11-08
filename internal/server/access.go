package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/kumoSleeping/iridium/internal/access"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/chat"
	"github.com/kumoSleeping/iridium/internal/gate"
	"github.com/kumoSleeping/iridium/internal/session"
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

// panel reports whether the caller may approve terminals, widen permissions and see tokens: a web
// panel, or 本机 when a person runs ir (or the page, with no public origin).
func (c caller) panel() bool { return !c.agent && (c.Kind == gate.KindPanel || c.Local) }

// machine reports whether a person at the machine sent it: ir over the socket, as 本机, not run
// by an agent.
func (c caller) machine() bool { return c.socket && c.Local && !c.agent }

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
	case c.panel() && !c.Local && c.Kind == gate.KindPanel && access.HasClaude(s.rulesFor(c)):
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
	return caller{Identity: id}
}

var localIdentity = gate.Identity{EndpointID: gate.LocalEndpointID, Name: gate.LocalName, Kind: gate.KindChannel, Local: true}

// socketHandler serves the local ir, past the gate: 本机, or, when an agent's ir names the session
// it runs in and another terminal opened that session, that terminal. What an agent does through
// ir is its session's terminal's doing: its permissions, its usage.
func (s *Server) socketHandler(mux http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := caller{Identity: localIdentity, socket: true, agent: r.Header.Get(HeaderAgent) != "", session: r.Header.Get(HeaderSession)}
		if c.session != "" {
			if id := s.st.Opener(c.session); id != "" && id != gate.LocalEndpointID {
				if info, ok := s.gate().Endpoint(id); ok {
					c.Identity = gate.Identity{EndpointID: info.ID, Name: info.Name, Kind: info.Kind}
					c.agent = true
				}
			}
		}
		ctx := context.WithValue(gate.NewContext(r.Context(), c.Identity), callerKey{}, c)
		mux.ServeHTTP(w, r.WithContext(ctx))
	})
}

// webHandler serves the TCP address behind the gate, whichever is in place now: the API under
// /api/, the gate's own pages under /_ir/, and the web app (package webui, or Server.App) for the
// rest. The gate stands in front of the app too: with a public origin even its files need a login.
func (s *Server) webHandler(mux http.Handler) http.Handler {
	app := s.App
	if app == nil {
		app = webui.Handler()
	}
	site := webui.WrapApp(mux, app)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.gate().Wrap(site).ServeHTTP(w, r)
	})
}

func (s *Server) gate() *gate.Gate { return s.g.Load() }

// newGate opens a gate for origin ("" is this machine only) over the store.
func (s *Server) newGate(origin string) (*gate.Gate, error) {
	return gate.New(gate.Config{
		PublicOrigin: origin,
		Store:        s.st.Gate(),
		StateDir:     s.cfg.StatePath(),
		Hosts:        s.cfg.Hosts,
		OnJoin:       s.joined,
		OnRevoke:     s.revoked,
		OnTerminals:  s.terminalsChanged,
		Open:         openPath,
		Logger:       slog.New(slog.NewTextHandler(logWriter{s.logf}, nil)),
	})
}

// terminalsChanged tells the pages the terminals changed (a Bot's avatar came, a panel renamed):
// they fetch GET /api/terminals again. The key "terminals" carries a count, nothing of them.
func (s *Server) terminalsChanged() {
	s.hub.Set("terminals", s.terminalsSeq.Add(1))
}

// revoked forgets a revoked terminal's chats (a Bot's): where they pointed, so the list marks no
// session with it, what they last did, their events.
func (s *Server) revoked(id string) {
	if s.st == nil {
		return
	}
	if err := s.st.ForgetMachine(id); err != nil {
		s.logf("server: forgetting %s's machine: %v", id, err)
	}
	if s.Remote != nil {
		s.publishMachines()
	}
	if err := s.st.ForgetChats(id); err != nil {
		s.logf("server: forgetting %s's chats: %v", id, err)
		return
	}
	if s.m != nil {
		s.m.PublishList()
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
	if !c.socket {
		return c.EndpointID
	}
	from := c.session
	if from == "" {
		from = callerSession
	}
	if from == "" || strings.ContainsAny(from, "[] \n") {
		return c.EndpointID
	}
	return session.From(c.EndpointID, "ir:"+from)
}

// Tag is the name terminal goes by in source tags: its name now.
func (s *Server) Tag(terminal string) string {
	if info, ok := s.gate().Endpoint(terminal); ok {
		return info.Name
	}
	return ""
}

// rulesFor is what the caller may do: Claude or not.
func (s *Server) rulesFor(c caller) api.Rules { return s.rulesOf(c.EndpointID, c.Local) }

func (s *Server) rulesOf(id string, local bool) api.Rules {
	if local || id == gate.LocalEndpointID {
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

func forbid(w http.ResponseWriter, err error) bool {
	fail(w, http.StatusForbidden, err.Error())
	return false
}

// using is session id if the caller may use it: any but a Claude session, which only the owner's
// own terminals may (start, write to, answer, steer). Every terminal sees every session.
func (s *Server) using(c caller, id string) (*api.SessionState, error) {
	st, err := s.m.State(id)
	if err != nil {
		return nil, err
	}
	if !c.Local {
		err = access.Agent(s.rulesFor(c), st.Agent)
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
	if c.Local {
		return nil
	}
	return access.Agent(s.rulesFor(c), req.Agent)
}

// checkSend checks a message for session id.
func (s *Server) checkSend(c caller, id string) error {
	_, err := s.using(c, id)
	return err
}

// mayWatch checks the states an event stream asks for. It has answered when false.
func (s *Server) mayWatch(w http.ResponseWriter, c caller, keys []string) bool {
	for _, k := range keys {
		if k == "confirms" && !c.panel() {
			return forbid(w, errors.New("only a panel sees confirmations"))
		}
	}
	return true
}

// terminalOf is an endpoint as the API lists it, with Claude or not and today's use.
func (s *Server) terminalOf(info gate.EndpointInfo) api.Terminal {
	t := api.Terminal{
		ID: info.ID, Name: info.Name, Kind: api.TerminalKind(info.Kind), Avatar: info.Avatar,
		Passkeys: info.Passkeys, Logins: info.Sessions, Online: info.Online,
		CreatedAt: ms(info.CreatedAt), LastSeenAt: ms(info.LastSeenAt),
		Rules: s.rulesOf(info.ID, false), UsedToday: s.usedToday(info.ID),
	}
	if info.ID == gate.LocalEndpointID {
		t.Local = true
	}
	t.Device = api.DeviceComputer
	if d, _ := s.st.Device(info.ID); api.Device(d).Valid() {
		t.Device = api.Device(d)
	}
	return t
}

func ms(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

// ---- the plugin chat layer ----

// chatCaller is a chat request's terminal (routes_plugin.go): the gate's identity.
func chatCaller(c caller) chat.Caller { return chat.Caller{ID: c.EndpointID, Name: c.Name} }

// callerOfChat is the terminal behind a chat.Caller.
func (s *Server) callerOfChat(c chat.Caller) caller {
	if c.ID == gate.LocalEndpointID {
		return caller{Identity: localIdentity}
	}
	info, ok := s.gate().Endpoint(c.ID)
	if !ok {
		return caller{Identity: gate.Identity{EndpointID: c.ID, Name: c.Name, Kind: gate.KindChannel}}
	}
	return caller{Identity: gate.Identity{EndpointID: info.ID, Name: info.Name, Kind: info.Kind}}
}

// ChatGuard is what the chat layer checks: Claude's sessions only for the owner's own.
func (s *Server) ChatGuard() chat.Guard { return chatGuard{s} }

type chatGuard struct{ s *Server }

func (g chatGuard) Open(c chat.Caller, req *api.NewSession) error {
	return g.s.checkCreate(g.s.callerOfChat(c), req)
}

func (g chatGuard) Opened(c chat.Caller, id string) {
	if err := g.s.st.SetOpener(id, c.ID); err != nil {
		g.s.logf("server: opener of %s: %v", id, err)
	}
}

func (g chatGuard) Send(c chat.Caller, id, text string) error {
	return g.s.checkSend(g.s.callerOfChat(c), id)
}

func (g chatGuard) Command(c chat.Caller, id, name string) error { return nil }

func (g chatGuard) Use(c chat.Caller, id string) error {
	_, err := g.s.using(g.s.callerOfChat(c), id)
	return err
}

func (g chatGuard) Sessions(c chat.Caller, l []api.Session) []api.Session { return l }

func (g chatGuard) Answer(c chat.Caller, id string) error {
	_, err := g.s.using(g.s.callerOfChat(c), id)
	return err
}

func (g chatGuard) FileOut(c chat.Caller, id string) error {
	_, err := g.s.m.State(id)
	return err
}
