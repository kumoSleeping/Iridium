// Package server is Iridium's HTTP API (docs/api.md), served on a loopback TCP address for the
// web page and on a unix socket (0600) for the local ir, the same API on both.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kumoSleeping/iridium/internal/access"
	"github.com/kumoSleeping/iridium/internal/agents"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/chat"
	"github.com/kumoSleeping/iridium/internal/config"
	"github.com/kumoSleeping/iridium/internal/discover"
	"github.com/kumoSleeping/iridium/internal/files"
	"github.com/kumoSleeping/iridium/internal/gate"
	"github.com/kumoSleeping/iridium/internal/hub"
	"github.com/kumoSleeping/iridium/internal/push"
	"github.com/kumoSleeping/iridium/internal/remote"
	"github.com/kumoSleeping/iridium/internal/session"
	"github.com/kumoSleeping/iridium/internal/store"
	"github.com/kumoSleeping/iridium/internal/transcript"
	"github.com/kumoSleeping/iridium/internal/update"
)

// TerminalLocal is the built-in channel terminal of the local ir (and of the web page while no
// public origin is set).
const TerminalLocal = gate.LocalName

type Server struct {
	cfg config.Config
	m   *session.Manager
	hub *hub.Hub

	terminalsSeq atomic.Int64 // the "terminals" state's count (terminalsChanged)
	// CLI runs the ir of a session at another machine (POST /api/cli); nil: none.
	CLI CLIAt
	// tokenFor stands in for the session manager's SessionToken in tests.
	tokenFor func(tok string) (endpointID, session string, ok bool)
	st       *store.Store
	logf     func(string, ...any)
	// ConfigPath is where PUT /api/public saves public.origin; "" saves it nowhere.
	ConfigPath string
	// Updater, if set, serves /api/update and carries the state "update".
	Updater *update.Updater

	cmu     sync.Mutex // one config change at a time (SaveConfig); guards cfg's settings
	gmu     sync.Mutex // one gate change at a time
	g       atomic.Pointer[gate.Gate]
	initErr error // the gate for cfg.Public.Origin would not open; Listen gives it
	asks    *confirms
	pairs   *pairs // executors asking to pair (routes_pair.go)
	vault   *vault // the credentials' masks and allowed uses (routes_secrets.go)

	// Set before Handler, if at all: the plugin chat layer, Web Push, and who a request is from.
	Chat *chat.Service
	Push *push.Service
	Who  Identities
	// Discover and Folders, if set, serve the workspaces and the folder picker (routes_discover.go).
	Discover *discover.Index
	Folders  discover.Folders
	// App is the web app on the TCP address, past the API; nil is the embedded build (webui).
	App http.Handler
	// Remote, if set, takes executors' connections at /api/exec (docs/exec.md).
	Remote *remote.Hub

	files *files.Service // routes_files.go

	// ExtDir is where the official extensions' binaries go; "" is beside Iridium's (extensions.go).
	ExtDir string
	ext    extensions
}

// New is the API over m. The interactive terminals live in m's store, behind the Passkey gate
// (package gate) in front of the TCP address; the unix socket is 本机.
func New(cfg config.Config, m *session.Manager, h *hub.Hub, logf func(string, ...any)) *Server {
	if logf == nil {
		logf = log.Printf
	}
	s := &Server{cfg: cfg, m: m, hub: h, st: m.Store(), files: newFiles(cfg, m, logf), logf: logf}
	s.asks = newConfirms(h)
	s.pairs = newPairs()
	s.vault = newVault()
	s.reloadSecrets()
	m.SetAnnotate(s.files.AnnotateBlocks) // a state's tail names files as a page of messages does
	g, err := s.newGate(cfg.Public.Origin)
	if err != nil {
		s.initErr = err
		g, _ = s.newGate("")
	}
	s.g.Store(g)
	h.Set("update", s.updateState())
	h.Set("terminals", int64(0))
	s.carryNotes()
	return s
}

// Close closes the gate.
func (s *Server) Close() { s.gate().Close() }

// Handler is the API. local: requests come from the unix socket, from the local ir.
func (s *Server) Handler(local bool) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		reply(w, http.StatusOK, map[string]any{"ok": true})
	})
	mux.HandleFunc("GET /api/agents", func(w http.ResponseWriter, r *http.Request) {
		l := agents.Describe()
		for i := range l {
			l[i].Missing = s.m.Missing(l[i].Agent)
		}
		reply(w, http.StatusOK, l)
	})
	// What carries transcripts goes out with the credentials' values masked (routes_secrets.go).
	mux.HandleFunc("GET /api/sessions", s.masked(s.list))
	mux.HandleFunc("POST /api/sessions", s.create)
	mux.HandleFunc("GET /api/sessions/{id}", s.masked(s.get))
	mux.HandleFunc("GET /api/sessions/{id}/messages", s.masked(s.messages))
	mux.HandleFunc("GET /api/sessions/{id}/block", s.masked(s.block))
	mux.HandleFunc("GET /api/sessions/{id}/image", s.image)
	mux.HandleFunc("POST /api/sessions/{id}/send", s.send)
	mux.HandleFunc("POST /api/sessions/{id}/interrupt", s.use(s.interrupt))
	mux.HandleFunc("POST /api/sessions/{id}/stop", s.use(s.stop))
	mux.HandleFunc("POST /api/sessions/{id}/answer", s.use(s.answer))
	mux.HandleFunc("POST /api/sessions/{id}/release", s.use(s.release))
	mux.HandleFunc("GET /api/events", s.masked(s.events))
	s.terminalRoutes(mux)
	s.secretRoutes(mux)
	s.updateRoutes(mux)
	s.settingsRoutes(mux)
	s.sessionRoutes(mux)
	s.pushRoutes(mux)
	s.pluginRoutes(mux)
	mux.HandleFunc("GET /api/exec", s.execConnect)
	s.pairRoutes(mux)
	s.machineRoutes(mux)
	s.extensionRoutes(mux)
	s.discoverRoutes(mux)
	s.noteRoutes(mux)
	s.fileRoutes(mux, local)
	if !local {
		s.cliRoutes(mux)
	}
	if local {
		return s.socketHandler(mux)
	}
	return s.webHandler(mux)
}

// Listen opens the TCP address and the unix socket. A socket file left by a daemon that is gone
// is replaced; one a running daemon answers on is an error.
func (s *Server) Listen() (tcp, unix net.Listener, err error) {
	if s.initErr != nil {
		return nil, nil, s.initErr
	}
	tcp, err = net.Listen("tcp", s.cfg.Listen)
	if err != nil {
		return nil, nil, err
	}
	path := s.cfg.SocketPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		tcp.Close()
		return nil, nil, err
	}
	if c, err := net.Dial("unix", path); err == nil {
		c.Close()
		tcp.Close()
		return nil, nil, fmt.Errorf("another daemon serves %s", path)
	}
	_ = os.Remove(path)
	unix, err = net.Listen("unix", path)
	if err == nil {
		err = os.Chmod(path, 0o600)
	}
	if err != nil {
		tcp.Close()
		return nil, nil, err
	}
	return tcp, unix, nil
}

// Serve serves both until ctx ends. Requests' contexts end with it, so event streams close at
// once rather than hold the shutdown.
func (s *Server) Serve(ctx context.Context, tcp, unix net.Listener) error {
	base := func(net.Listener) context.Context { return ctx }
	servers := []*http.Server{
		{Handler: s.Handler(false), ReadHeaderTimeout: 10 * time.Second, BaseContext: base},
		{Handler: s.Handler(true), ReadHeaderTimeout: 10 * time.Second, BaseContext: base},
	}
	go s.files.Janitor(ctx)
	errs := make(chan error, 2)
	for i, l := range []net.Listener{tcp, unix} {
		go func() { errs <- servers[i].Serve(l) }()
	}
	select {
	case <-ctx.Done():
	case err := <-errs:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}
	shut, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for _, srv := range servers {
		_ = srv.Shutdown(shut)
	}
	return nil
}

func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	l, err := s.m.List(r.Context())
	if err != nil {
		s.error(w, err)
		return
	}
	reply(w, http.StatusOK, l)
}

func (s *Server) create(w http.ResponseWriter, r *http.Request) {
	var req api.NewSession
	if !readJSON(w, r, &req) {
		return
	}
	c := callerOf(r)
	if err := s.checkCreate(c, &req); err != nil {
		s.error(w, err)
		return
	}
	id, err := s.m.Create(r.Context(), req, s.fromOf(c, req.Caller))
	if id != "" {
		if err := s.st.SetOpener(id, c.EndpointID); err != nil {
			s.logf("server: opener of %s: %v", id, err)
		}
	}
	if err != nil && id == "" {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if err != nil {
		s.error(w, err)
		return
	}
	reply(w, http.StatusOK, api.NewSessionReply{ID: id})
}

func (s *Server) get(w http.ResponseWriter, r *http.Request) {
	st, err := s.m.State(r.PathValue("id"))
	if err != nil {
		s.error(w, err)
		return
	}
	reply(w, http.StatusOK, st)
}

func (s *Server) messages(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	before, after := int64(-1), int64(-1)
	for name, v := range map[string]*int64{"before": &before, "after": &after} {
		if q.Has(name) {
			n, err := strconv.ParseInt(q.Get(name), 10, 64)
			if err != nil || n < 0 {
				fail(w, http.StatusBadRequest, "bad "+name)
				return
			}
			*v = n
		}
	}
	p, err := s.m.Messages(r.PathValue("id"), before, after)
	if err != nil {
		s.error(w, err)
		return
	}
	s.files.Annotate(r.PathValue("id"), &p)
	reply(w, http.StatusOK, p)
}

func (s *Server) block(w http.ResponseWriter, r *http.Request) {
	text, err := s.m.Full(r.PathValue("id"), r.URL.Query().Get("at"))
	if err != nil {
		s.error(w, err)
		return
	}
	reply(w, http.StatusOK, api.FullText{Text: text})
}

func (s *Server) send(w http.ResponseWriter, r *http.Request) {
	var req api.Send
	if !readJSON(w, r, &req) {
		return
	}
	c := callerOf(r)
	// What an agent tells another session joins its turn at once, --queue or not: queued, it would
	// wait for a person to press it in (the user, 2026-11-06).
	// (An agent's ir on the socket names the session it runs in; one at another machine is an
	// agent's by its session's TOKEN.)
	if c.agent || c.socket && c.session != "" {
		req.Steer = true
	}
	if err := s.checkSend(c, r.PathValue("id")); err != nil {
		s.error(w, err)
		return
	}
	// Attachments and all: Claude would clear itself just the same.
	if session.IsClear(req.Text) {
		id, err := s.m.Clear(r.Context(), r.PathValue("id"), s.fromOf(c, req.Caller))
		if id != "" && id != r.PathValue("id") {
			if err := s.st.SetOpener(id, c.EndpointID); err != nil {
				s.logf("server: opener of %s: %v", id, err)
			}
		}
		if err != nil {
			s.error(w, err)
			return
		}
		reply(w, http.StatusOK, api.SendReply{Session: id})
		return
	}
	text, images, err := s.files.Message(r.PathValue("id"), req.Text, req.Attachments)
	if err != nil {
		s.fileError(w, err)
		return
	}
	input, queued, err := s.m.SendWith(r.Context(), r.PathValue("id"), text, s.fromOf(c, req.Caller), req.Steer, images...)
	if err != nil {
		s.error(w, err)
		return
	}
	if input == session.HandedInput {
		reply(w, http.StatusOK, api.SendReply{Desktop: true})
		return
	}
	reply(w, http.StatusOK, api.SendReply{Input: input, Queued: queued, Command: s.m.RunsItself(r.PathValue("id"), text)})
}

func (s *Server) interrupt(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	s.done(w, s.m.Interrupt(ctx, r.PathValue("id")))
}

func (s *Server) stop(w http.ResponseWriter, r *http.Request) {
	s.done(w, s.m.Stop(r.Context(), r.PathValue("id")))
}

func (s *Server) answer(w http.ResponseWriter, r *http.Request) {
	var a api.Answer
	if !readJSON(w, r, &a) {
		return
	}
	s.done(w, s.m.Answer(r.Context(), r.PathValue("id"), a))
}

// release allows a step auto mode denied (session/release.go). Only a person may: an agent's ir
// releasing its own denied steps would undo what the classifier is for.
func (s *Server) release(w http.ResponseWriter, r *http.Request) {
	c := callerOf(r)
	if c.agent || (c.socket && c.session != "") {
		forbid(w, errors.New("only a person may release a step auto mode denied"))
		return
	}
	var req api.Release
	if !readJSON(w, r, &req) {
		return
	}
	if req.ToolUseID == "" {
		fail(w, http.StatusBadRequest, "name the call: toolUseId")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	s.done(w, s.m.Release(ctx, r.PathValue("id"), req.ToolUseID, req.Dismiss))
}

var stateKey = regexp.MustCompile(`^(list|quota|workspaces|places|colors|confirms|update|terminals|machines|notes|(session|context):[0-9a-zA-Z_-]{1,128})$`)

// events is the page's one event stream: ?sub=<key> for each state it wants (list, quota,
// workspaces, colors, session:<id>, context:<id>, confirms, update, notes). See package hub. A key it
// does not know, or one not for this terminal (confirms, colors and notes, but for panels), does not fail the
// stream: it is left out, and the stream's first events say so (StateEvent.Ignored).
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	keys := r.URL.Query()["sub"]
	if len(keys) == 0 && s.Chat != nil {
		s.pluginEvents(w, r) // a plugin's stream (routes_plugin.go)
		return
	}
	if len(keys) == 0 || len(keys) > 16 {
		fail(w, http.StatusBadRequest, "name 1 to 16 states with sub=")
		return
	}
	c := callerOf(r)
	var carried []string
	var ignored []api.StateEvent
	for _, k := range keys {
		switch {
		case !stateKey.MatchString(k):
			ignored = append(ignored, api.StateEvent{Key: k, Ignored: "unknown"})
		case (k == "confirms" || k == "colors" || k == "notes") && !c.panel():
			ignored = append(ignored, api.StateEvent{Key: k, Ignored: "forbidden"})
		case !slices.Contains(carried, k):
			carried = append(carried, k)
		}
	}
	keys = carried
	if !s.mayWatch(w, c, keys) {
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	defer s.gate().Track(r, cancel)() // the stream ends when its terminal is revoked or logs out
	r = r.WithContext(ctx)
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{})
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	io.WriteString(w, "retry: 2000\n\n")
	for _, e := range ignored {
		b, _ := json.Marshal(e)
		fmt.Fprintf(w, "event: state\ndata: %s\n\n", b)
	}
	_ = rc.Flush()
	sub := s.hub.Subscribe(keys) // none it may carry: open all the same, pinging
	defer sub.Close()
	_ = sub.Serve(r.Context(), w, func() { _ = rc.Flush() })
}

func (s *Server) done(w http.ResponseWriter, err error) {
	if err != nil {
		s.error(w, err)
		return
	}
	reply(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) error(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, session.ErrNotFound), errors.Is(err, transcript.ErrNoBlock):
		fail(w, http.StatusNotFound, err.Error())
	case errors.Is(err, session.ErrNotLive), errors.Is(err, session.ErrElsewhere), errors.Is(err, session.ErrSubagent):
		fail(w, http.StatusConflict, err.Error())
	case errors.Is(err, context.DeadlineExceeded):
		fail(w, http.StatusGatewayTimeout, "the agent did not answer in time")
	case access.IsDenied(err):
		fail(w, http.StatusForbidden, err.Error())
	default:
		s.logf("server: %v", err)
		fail(w, http.StatusInternalServerError, err.Error())
	}
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		fail(w, http.StatusBadRequest, "bad request body: "+err.Error())
		return false
	}
	return true
}

func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, status int, msg string) { reply(w, status, api.Error{Error: msg}) }

// Files is the files service (uploads, refs, paths), for the chat layer's attachments.
func (s *Server) Files() *files.Service { return s.files }

// execConnect takes an executor's WebSocket (docs/exec.md): a terminal with a TOKEN, as which the
// machine is known.
func (s *Server) execConnect(w http.ResponseWriter, r *http.Request) {
	if s.Remote == nil {
		fail(w, http.StatusNotFound, "no executors here")
		return
	}
	c := callerOf(r)
	if c.Kind != gate.KindChannel || c.Local || c.socket || c.session != "" {
		fail(w, http.StatusForbidden, "an executor connects with a TOKEN")
		return
	}
	s.Remote.Serve(w, r, c.EndpointID, c.Name)
}
