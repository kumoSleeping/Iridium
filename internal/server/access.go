package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"slices"
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
		Logger:       slog.New(slog.NewTextHandler(logWriter{s.logf}, nil)),
	})
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

// rulesFor is the caller's permission table.
func (s *Server) rulesFor(c caller) api.Rules { return s.rulesOf(c.EndpointID, c.Kind, c.Local) }

func (s *Server) rulesOf(id, kind string, local bool) api.Rules {
	if local || id == gate.LocalEndpointID {
		return access.Full()
	}
	if b, ok, err := s.st.TerminalRules(id); ok && err == nil {
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

func (s *Server) underCap(c caller, r api.Rules) error {
	if r.DailyTokens > 0 && s.usedToday(c.EndpointID) >= r.DailyTokens {
		return access.ErrCap
	}
	return nil
}

func forbid(w http.ResponseWriter, err error) bool {
	fail(w, http.StatusForbidden, err.Error())
	return false
}

func (s *Server) canSee(c caller, r api.Rules, sess api.Session) bool {
	return access.Visible(r, c.EndpointID, sess.Agent, sess.Cwd, sess.Workspace, s.st.Opener(sess.ID))
}

// visible is the part of the list the caller may see.
func (s *Server) visible(c caller, l []api.Session) []api.Session {
	if c.Local {
		return l
	}
	r := s.rulesFor(c)
	openers, _ := s.st.Openers()
	out := make([]api.Session, 0, len(l))
	for _, sess := range l {
		if access.Visible(r, c.EndpointID, sess.Agent, sess.Cwd, sess.Workspace, openers[sess.ID]) {
			out = append(out, sess)
		}
	}
	return out
}

// seeing is session id if the caller may see it; one it may not is no such session.
func (s *Server) seeing(c caller, id string) (*api.SessionState, api.Rules, error) {
	rules := s.rulesFor(c)
	st, err := s.m.State(id)
	if err != nil {
		return nil, rules, err
	}
	if !c.Local && !s.canSee(c, rules, st.Session) {
		return nil, rules, session.ErrNotFound
	}
	return st, rules, nil
}

// using is session id if the caller may see it and use its agent.
func (s *Server) using(c caller, id string) (*api.SessionState, api.Rules, error) {
	st, rules, err := s.seeing(c, id)
	if err == nil {
		err = access.Agent(rules, st.Agent)
	}
	return st, rules, err
}

// guarded lets h serve session {id} only when check passes for the caller.
func (s *Server) guarded(check func(caller, api.Rules) error, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c := callerOf(r)
		_, rules, err := s.using(c, r.PathValue("id"))
		if err == nil && check != nil {
			err = check(c, rules)
		}
		if err != nil {
			s.error(w, err)
			return
		}
		h(w, r)
	}
}

// see lets h serve only sessions the caller may see.
func (s *Server) see(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, _, err := s.seeing(callerOf(r), r.PathValue("id")); err != nil {
			s.error(w, err)
			return
		}
		h(w, r)
	}
}

// use lets h act on sessions the caller may see and whose agent it may use.
func (s *Server) use(h http.HandlerFunc) http.HandlerFunc { return s.guarded(nil, h) }

// answering lets h answer asks only for a terminal that may.
func (s *Server) answering(h http.HandlerFunc) http.HandlerFunc {
	return s.guarded(func(_ caller, r api.Rules) error {
		if !r.Answer {
			return access.ErrAnswer
		}
		return nil
	}, h)
}

// filesOut lets h give a session's files only to a terminal that may take files out.
func (s *Server) filesOut(h http.HandlerFunc) http.HandlerFunc {
	return s.guarded(func(_ caller, r api.Rules) error {
		if !r.FilesOut {
			return access.ErrFilesOut
		}
		return nil
	}, h)
}

// filesIn lets h take files into session {id} only from a terminal that may.
func (s *Server) filesIn(h http.HandlerFunc) http.HandlerFunc {
	return s.guarded(func(_ caller, r api.Rules) error {
		if !r.FilesIn {
			return access.ErrFilesIn
		}
		return nil
	}, h)
}

// uploading lets h go on with an upload only for a terminal that may copy files in.
func (s *Server) uploading(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.rulesFor(callerOf(r)).FilesIn {
			s.error(w, access.ErrFilesIn)
			return
		}
		h(w, r)
	}
}

// inDirs checks a path against the caller's directories (AllDirs: any; the rest the files
// service's own guard checks).
func (s *Server) inDirs(c caller, path string) error {
	r := s.rulesFor(c)
	if r.AllDirs {
		return nil
	}
	if path == "" || !filepath.IsAbs(path) {
		return access.ErrDir
	}
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	} else if real, err := filepath.EvalSymlinks(filepath.Dir(path)); err == nil {
		path = filepath.Join(real, filepath.Base(path)) // one not made yet
	}
	return access.Dir(r, path, 0, s.workspacePaths())
}

func (s *Server) workspacePaths() map[int64]string {
	paths := map[int64]string{}
	if ws, err := s.st.Workspaces(); err == nil {
		for _, x := range ws {
			paths[x.ID] = x.Path
		}
	}
	return paths
}

// pathRoute lets h serve only paths in the caller's directories: the query's path (q), or the
// JSON body's fields (fields), and with the files rules in and out.
func (s *Server) pathRoute(q string, fields []string, in, out bool, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c := callerOf(r)
		rules := s.rulesFor(c)
		if in && !rules.FilesIn {
			s.error(w, access.ErrFilesIn)
			return
		}
		if out && !rules.FilesOut {
			s.error(w, access.ErrFilesOut)
			return
		}
		var paths []string
		if q != "" {
			paths = append(paths, r.URL.Query().Get(q))
		}
		if len(fields) > 0 {
			b, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
			if err != nil {
				fail(w, http.StatusBadRequest, "bad request body")
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(b))
			var body map[string]any
			_ = json.Unmarshal(b, &body)
			for _, f := range fields {
				if v, ok := body[f].(string); ok {
					paths = append(paths, v)
				}
			}
		}
		for _, p := range paths {
			// machine:path names another machine's, which its own permissions guard.
			if !strings.HasPrefix(p, "/") && strings.Contains(p, ":") {
				continue
			}
			if err := s.inDirs(c, filepath.Clean(p)); err != nil {
				s.error(w, err)
				return
			}
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

// checkCreate checks a new session against the caller's permissions, and makes a must-approve
// terminal's session one that asks.
func (s *Server) checkCreate(c caller, req *api.NewSession) error {
	r := s.rulesFor(c)
	if err := access.Agent(r, req.Agent); err != nil {
		return err
	}
	if !r.AllDirs {
		if err := access.Dir(r, filepath.Clean(req.Cwd), req.Workspace, s.workspacePaths()); err != nil {
			return err
		}
	}
	if err := access.Mode(r, req.Mode); err != nil {
		return err
	}
	if r.MustApprove && req.Mode == "" {
		req.Mode = access.ApprovingMode(req.Agent)
	}
	if req.Text != "" {
		if err := access.Command(r, req.Text); err != nil {
			return err
		}
	}
	return s.underCap(c, r)
}

// checkSend checks a message for session id against the caller's permissions. files: it carries
// attachments.
func (s *Server) checkSend(c caller, id, text string, files bool) error {
	if c.Local {
		return nil
	}
	st, r, err := s.using(c, id)
	if err != nil {
		return err
	}
	if files && !r.FilesIn {
		return access.ErrFilesIn
	}
	if err := access.Command(r, text); err != nil {
		return err
	}
	if err := access.Mode(r, st.Mode); err != nil {
		return err
	}
	return s.underCap(c, r)
}

// checkMode checks switching session id to an approval mode.
func (s *Server) checkMode(c caller, mode string) error {
	return access.Mode(s.rulesFor(c), mode)
}

// mayWatch checks the states an event stream asks for. It has answered when false.
func (s *Server) mayWatch(w http.ResponseWriter, c caller, keys []string) bool {
	for _, k := range keys {
		switch {
		case k == "confirms" && !c.panel():
			return forbid(w, errors.New("only a panel sees confirmations"))
		case len(k) > 8 && (k[:8] == "session:" || k[:8] == "context:"):
			if c.Local {
				continue
			}
			if _, _, err := s.seeing(c, k[8:]); errors.Is(err, session.ErrNotFound) && s.exists(k[8:]) {
				s.error(w, err)
				return false
			}
		}
	}
	return true
}

func (s *Server) exists(id string) bool { _, err := s.m.State(id); return err == nil }

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
	t.Device = api.DeviceComputer
	if d, _ := s.st.Device(info.ID); d == string(api.DevicePhone) {
		t.Device = api.DevicePhone
	}
	return t
}

func ms(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

// widened is the rules a change asks for, checked: only a panel widens, and only one that may say
// a terminal is the owner's own (ownerBy, from ownerSays) adds claude; taking it away is anyone's
// that manages. The owner mark is the server's to write: one in the request counts for nothing.
func widened(c caller, ownerBy string, old api.Rules, req api.SetRules) (api.Rules, error) {
	r := req.Rules
	r.ClaudeOwner = nil
	if slices.Contains(r.Agents, api.AgentClaude) {
		switch {
		case access.HasClaude(old):
			r.ClaudeOwner = old.ClaudeOwner
		case ownerBy == "":
			return access.Clean(r), access.ErrOwner
		default:
			r.ClaudeOwner = &api.OwnerConfirm{By: ownerBy, At: time.Now().UnixMilli()}
		}
	}
	next := access.Clean(r)
	if err := access.Validate(next); err != nil {
		return next, err
	}
	if access.Widens(old, next) && !c.panel() {
		return next, access.ErrWiden
	}
	return next, nil
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

// ChatGuard is the permission table as the chat layer checks it.
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
	return g.s.checkSend(g.s.callerOfChat(c), id, text, false)
}

func (g chatGuard) Command(c chat.Caller, id, name string) error {
	return access.Command(g.s.rulesFor(g.s.callerOfChat(c)), name)
}

func (g chatGuard) Mode(c chat.Caller, mode string) error {
	return g.s.checkMode(g.s.callerOfChat(c), mode)
}

func (g chatGuard) Use(c chat.Caller, id string) error {
	_, _, err := g.s.using(g.s.callerOfChat(c), id)
	return err
}

func (g chatGuard) Sessions(c chat.Caller, l []api.Session) []api.Session {
	return g.s.visible(g.s.callerOfChat(c), l)
}

func (g chatGuard) Answer(c chat.Caller, id string) error {
	_, r, err := g.s.using(g.s.callerOfChat(c), id)
	if err == nil && !r.Answer {
		err = access.ErrAnswer
	}
	return err
}

func (g chatGuard) FileOut(c chat.Caller, id string) error {
	_, r, err := g.s.seeing(g.s.callerOfChat(c), id)
	if err == nil && !r.FilesOut {
		err = access.ErrFilesOut
	}
	return err
}
