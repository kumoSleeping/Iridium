package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"path/filepath"
	"slices"
	"time"

	"github.com/kumoSleeping/iridium/internal/access"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/gate"
	"github.com/kumoSleeping/iridium/internal/session"
)

// Headers the local ir sends over the unix socket. HeaderAgent: an agent ran it (its environment
// says so, or it has no terminal), so no secret may go back: what needs one becomes a
// confirmation a panel answers. HeaderSession: the IRIDIUM_SESSION it ran in.
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

type callerKey struct{}

func callerOf(r *http.Request) caller {
	if c, ok := r.Context().Value(callerKey{}).(caller); ok {
		return c
	}
	id, _ := gate.FromContext(r.Context())
	return caller{Identity: id}
}

var localIdentity = gate.Identity{EndpointID: gate.LocalEndpointID, Name: gate.LocalName, Kind: gate.KindChannel, Local: true}

// socketHandler serves the local ir: 本机, past the gate.
func (s *Server) socketHandler(mux http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := caller{Identity: localIdentity, socket: true, agent: r.Header.Get(HeaderAgent) != "", session: r.Header.Get(HeaderSession)}
		ctx := context.WithValue(gate.NewContext(r.Context(), c.Identity), callerKey{}, c)
		mux.ServeHTTP(w, r.WithContext(ctx))
	})
}

// webHandler serves the TCP address behind the gate, whichever is in place now.
func (s *Server) webHandler(mux http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.gate().Wrap(mux).ServeHTTP(w, r)
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
		Logger:       slog.New(slog.NewTextHandler(logWriter{s.logf}, nil)),
	})
}

type logWriter struct{ logf func(string, ...any) }

func (l logWriter) Write(b []byte) (int, error) {
	l.logf("%s", string(trimNL(b)))
	return len(b), nil
}

func trimNL(b []byte) []byte {
	for len(b) > 0 && b[len(b)-1] == '\n' {
		b = b[:len(b)-1]
	}
	return b
}

// Tag is the name terminal goes by in source tags: its name now.
func (s *Server) Tag(terminal string) string {
	if info, ok := s.gate().Endpoint(terminal); ok {
		return info.Name
	}
	return ""
}

// rules is the caller's permission table.
func (s *Server) rules(c caller) api.Rules { return s.rulesOf(c.EndpointID, c.Kind, c.Local) }

func (s *Server) rulesOf(id, kind string, local bool) api.Rules {
	if local || id == gate.LocalEndpointID {
		return access.Full()
	}
	if b, ok, err := s.st.Rules(id); ok && err == nil {
		var r api.Rules
		if json.Unmarshal(b, &r) == nil {
			return access.Clean(r)
		}
	}
	return access.Clean(access.Default(api.TerminalKind(kind)))
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

// underCap checks the daily cap; it has answered when false.
func (s *Server) underCap(w http.ResponseWriter, c caller, r api.Rules) bool {
	if r.DailyTokens > 0 && s.usedToday(c.EndpointID) >= r.DailyTokens {
		fail(w, http.StatusTooManyRequests, access.ErrCap.Error())
		return false
	}
	return true
}

func forbid(w http.ResponseWriter, err error) bool {
	fail(w, http.StatusForbidden, err.Error())
	return false
}

// seeing is a session as a terminal's permissions look at it.
func (s *Server) canSee(c caller, r api.Rules, sess api.Session) bool {
	return access.Visible(r, c.EndpointID, sess.Agent, sess.Cwd, sess.Workspace, s.st.Opener(sess.ID))
}

// visible is the part of the list the caller may see.
func (s *Server) visible(c caller, l []api.Session) []api.Session {
	if c.Local {
		return l
	}
	r := s.rules(c)
	openers, _ := s.st.Openers()
	out := make([]api.Session, 0, len(l))
	for _, sess := range l {
		if access.Visible(r, c.EndpointID, sess.Agent, sess.Cwd, sess.Workspace, openers[sess.ID]) {
			out = append(out, sess)
		}
	}
	return out
}

// session is session {id} if the caller may see it; else it has answered (404, as if there
// were none).
func (s *Server) session(w http.ResponseWriter, r *http.Request) (*api.SessionState, api.Rules, bool) {
	c := callerOf(r)
	rules := s.rules(c)
	st, err := s.m.State(r.PathValue("id"))
	if err != nil {
		s.error(w, err)
		return nil, rules, false
	}
	if !c.Local && !s.canSee(c, rules, st.Session) {
		s.error(w, session.ErrNotFound)
		return nil, rules, false
	}
	return st, rules, true
}

// see lets h serve only sessions the caller may see.
func (s *Server) see(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, _, ok := s.session(w, r); ok {
			h(w, r)
		}
	}
}

// use lets h act on sessions the caller may see and whose agent it may use.
func (s *Server) use(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		st, rules, ok := s.session(w, r)
		if !ok {
			return
		}
		if err := access.Agent(rules, st.Agent); err != nil {
			forbid(w, err)
			return
		}
		h(w, r)
	}
}

// answering lets h answer asks only for a terminal that may.
func (s *Server) answering(h http.HandlerFunc) http.HandlerFunc {
	return s.use(func(w http.ResponseWriter, r *http.Request) {
		if !s.rules(callerOf(r)).Answer {
			forbid(w, access.ErrAnswer)
			return
		}
		h(w, r)
	})
}

// mayCreate checks a new session against the caller's permissions, and makes a must-approve
// terminal's session one that asks. It has answered when false.
func (s *Server) mayCreate(w http.ResponseWriter, c caller, req *api.NewSession) bool {
	r := s.rules(c)
	if err := access.Agent(r, req.Agent); err != nil {
		return forbid(w, err)
	}
	if !r.AllDirs {
		paths := map[int64]string{}
		if ws, err := s.st.Workspaces(); err == nil {
			for _, x := range ws {
				paths[x.ID] = x.Path
			}
		}
		if err := access.Dir(r, filepath.Clean(req.Cwd), 0, paths); err != nil {
			return forbid(w, err)
		}
	}
	if err := access.Mode(r, req.Mode); err != nil {
		return forbid(w, err)
	}
	if r.MustApprove && req.Mode == "" {
		req.Mode = access.ApprovingMode(req.Agent)
	}
	if req.Text != "" {
		if err := access.Command(r, req.Text); err != nil {
			return forbid(w, err)
		}
	}
	return s.underCap(w, c, r)
}

// maySend checks a message for session id against the caller's permissions. It has answered
// when false.
func (s *Server) maySend(w http.ResponseWriter, c caller, id, text string) bool {
	if c.Local {
		return true
	}
	r := s.rules(c)
	st, err := s.m.State(id)
	if err != nil {
		s.error(w, err)
		return false
	}
	if !s.canSee(c, r, st.Session) {
		s.error(w, session.ErrNotFound)
		return false
	}
	if err := access.Agent(r, st.Agent); err != nil {
		return forbid(w, err)
	}
	if err := access.Command(r, text); err != nil {
		return forbid(w, err)
	}
	if err := access.Mode(r, st.Mode); err != nil {
		return forbid(w, err)
	}
	return s.underCap(w, c, r)
}

// mayWatch checks the states an event stream asks for. It has answered when false.
func (s *Server) mayWatch(w http.ResponseWriter, c caller, keys []string) bool {
	r := s.rules(c)
	for _, k := range keys {
		switch {
		case k == "confirms" && !c.panel():
			return forbid(w, errors.New("only a panel sees confirmations"))
		case len(k) > 8 && (k[:8] == "session:" || k[:8] == "context:"):
			if c.Local {
				continue
			}
			st, err := s.m.State(k[8:])
			if err == nil && !s.canSee(c, r, st.Session) {
				s.error(w, session.ErrNotFound)
				return false
			}
		}
	}
	return true
}

// filter gives the caller's stream only the sessions it may see.
func (s *Server) filter(c caller) func(string, []byte) []byte {
	if c.Local {
		return nil
	}
	return func(key string, v []byte) []byte {
		if key != "list" {
			return v
		}
		var l []api.Session
		if json.Unmarshal(v, &l) != nil {
			return v
		}
		b, err := json.Marshal(s.visible(c, l))
		if err != nil {
			return []byte("[]")
		}
		return b
	}
}

// terminalOf is an endpoint as the API lists it, with its rules and today's use.
func (s *Server) terminalOf(info gate.EndpointInfo) api.Terminal {
	t := api.Terminal{
		ID: info.ID, Name: info.Name, Kind: api.TerminalKind(info.Kind), Avatar: info.Avatar,
		Passkeys: info.Passkeys, Logins: info.Sessions, Online: info.Online,
		CreatedAt: ms(info.CreatedAt), LastSeenAt: ms(info.LastSeenAt),
		Rules: s.rulesOf(info.ID, info.Kind, false), UsedToday: s.usedToday(info.ID),
	}
	if info.ID == gate.LocalEndpointID {
		t.Local = true
	}
	return t
}

func ms(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

// widened is the rules a change asks for, checked: only a panel widens, adding claude needs its
// word that the terminal is the owner's, and the confirmation itself is the server's to write.
func widened(c caller, old api.Rules, req api.SetRules) (api.Rules, error) {
	next := access.Clean(req.Rules)
	if err := access.Validate(next); err != nil {
		return next, err
	}
	next.ClaudeOwner = nil
	if slices.Contains(next.Agents, api.AgentClaude) {
		if old.ClaudeOwner != nil && slices.Contains(old.Agents, api.AgentClaude) {
			next.ClaudeOwner = old.ClaudeOwner
		} else if !c.panel() {
			return next, access.ErrWiden
		} else if !req.OwnerConfirmed {
			return next, access.ErrOwner
		} else {
			next.ClaudeOwner = &api.OwnerConfirm{By: c.EndpointID, At: time.Now().UnixMilli()}
		}
	}
	if access.Widens(old, next) && !c.panel() {
		return next, access.ErrWiden
	}
	return next, nil
}
