// Package server is Iridium's HTTP API (docs/api.md), served on a loopback TCP address for the
// web page and on a unix socket (0600) for the local ir, the same API on both.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kumoSleeping/iridium/internal/access"
	"github.com/kumoSleeping/iridium/internal/agents"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/config"
	"github.com/kumoSleeping/iridium/internal/discover"
	"github.com/kumoSleeping/iridium/internal/files"
	"github.com/kumoSleeping/iridium/internal/gate"
	"github.com/kumoSleeping/iridium/internal/hub"
	"github.com/kumoSleeping/iridium/internal/pricing"
	"github.com/kumoSleeping/iridium/internal/push"
	"github.com/kumoSleeping/iridium/internal/remote"
	"github.com/kumoSleeping/iridium/internal/satori"
	"github.com/kumoSleeping/iridium/internal/session"
	"github.com/kumoSleeping/iridium/internal/store"
	"github.com/kumoSleeping/iridium/internal/transcript"
	"github.com/kumoSleeping/iridium/internal/update"
	"github.com/kumoSleeping/iridium/internal/webapp"
)

type Server struct {
	cfg config.Config
	m   *session.Manager
	hub *hub.Hub

	turns        origins      // whose turn each session runs (origin.go)
	terminalsSeq atomic.Int64 // the "terminals" state's count (terminalsChanged)
	// CLI runs the ir of a session at another machine (POST /v1/internal/cli); nil: none.
	CLI CLIAt
	// tokenFor stands in for the session manager's SessionToken in tests.
	tokenFor func(tok string) (endpointID, session string, ok bool)
	st       *store.Store
	logf     func(string, ...any)
	// ConfigPath is where PUT /v1/internal/public saves public.origin; "" saves it nowhere.
	ConfigPath string
	// Updater, if set, serves /v1/internal/update and carries the state "update".
	Updater *update.Updater
	// Prices, if set, prices the usage the agents gave no cost (GET /v1/internal/usage).
	Prices *pricing.Catalog

	cmu      sync.Mutex // one config change at a time (SaveConfig); guards cfg's settings
	gmu      sync.Mutex // one change of ir_web at a time
	g        *gate.Gate // the core's door and registry (access.go)
	w        atomic.Pointer[webapp.Web]
	webToken string     // ir_web's own TOKEN, which it speaks to the core with
	initErr  error      // ir_web for cfg.Public.Origin would not open; Listen gives it
	asks     *needs     // what waits for a person (needs.go)
	more     *moreNeeds // asks' first sightings, shares (needs_more.go)
	pairs    *pairs     // apps asking to pair (pairing.go)
	vault    *vault     // the credentials' masks and allowed uses (routes_secrets.go)

	// Push, if set before Handler, is Web Push (routes_push.go).
	Push *push.Service
	// Discover and Folders, if set, serve the workspaces and the folder picker (routes_discover.go).
	Discover *discover.Index
	Folders  discover.Folders
	// App is the web app on the TCP address, past the API; nil is the embedded build (webui).
	App http.Handler
	// Remote, if set, takes executors' connections at /v1/internal/exec (docs/exec.md).
	Remote *remote.Hub

	files *files.Service  // routes_files.go
	sat   *satori.Service // /v1/: Satori with ir (satori.go)
	// stopList stops giving Satori the session list (its channel events).
	stopList func()
}

// New is the API over m. Apps and logins live in m's store; on the TCP address ir_web (package
// webapp: passkeys, cookies, the page) stands in front of the core's TOKEN gate (package gate);
// the unix socket is the host.
func New(cfg config.Config, m *session.Manager, h *hub.Hub, logf func(string, ...any)) *Server {
	if logf == nil {
		logf = log.Printf
	}
	s := &Server{cfg: cfg, m: m, hub: h, st: m.Store(), files: newFiles(cfg, m, logf), logf: logf}
	s.asks = newNeeds(h)
	s.pairs = newPairs()
	s.publishPairing()
	s.vault = newVault()
	s.reloadSecrets()
	m.SetAnnotate(s.files.AnnotateBlocks) // a state's tail names files as a page of messages does
	s.sat = satori.New(satoriBackend{s}, logf)
	s.sat.Keep(satoriKeeper{s.st})
	m.Watch(s.sat.Notice)
	lctx, lstop := context.WithCancel(context.Background())
	s.stopList = lstop
	go s.watchList(lctx)
	s.wireNeeds()
	if err := s.openGate(); err != nil {
		s.initErr = err
		return s
	}
	web, err := s.newWeb(cfg.Public.Origin)
	if err != nil {
		s.initErr = err
		web, _ = s.newWeb("")
	}
	s.w.Store(web)
	h.Set("update", s.updateState())
	h.Set("terminals", int64(0))
	s.carryNotes()
	return s
}

// Close closes ir_web and the gate and stops Satori's events.
func (s *Server) Close() {
	if w := s.web(); w != nil {
		w.Close()
	}
	if s.g != nil {
		s.g.Close()
	}
	if s.stopList != nil {
		s.stopList()
	}
	s.sat.Close()
}

// Handler is the API. local: requests come from the unix socket, from the local ir.
func (s *Server) Handler(local bool) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/internal/agents", func(w http.ResponseWriter, r *http.Request) {
		l := agents.Describe()
		for i := range l {
			l[i].Missing = s.m.Missing(l[i].Agent)
		}
		reply(w, http.StatusOK, l)
	})
	// What carries transcripts goes out with the credentials' values masked (routes_secrets.go).
	mux.HandleFunc("GET /v1/internal/sessions", s.masked(s.list))
	mux.HandleFunc("POST /v1/internal/sessions", s.create)
	mux.HandleFunc("GET /v1/internal/sessions/{id}", s.masked(s.get))
	mux.HandleFunc("GET /v1/internal/sessions/{id}/messages", s.masked(s.messages))
	mux.HandleFunc("GET /v1/internal/sessions/{id}/block", s.masked(s.block))
	mux.HandleFunc("GET /v1/internal/sessions/{id}/image", s.image)
	mux.HandleFunc("POST /v1/internal/sessions/{id}/send", s.send)
	mux.HandleFunc("POST /v1/internal/sessions/{id}/interrupt", s.use(s.interrupt))
	mux.HandleFunc("POST /v1/internal/sessions/{id}/stop", s.use(s.stop))
	mux.HandleFunc("POST /v1/internal/sessions/{id}/answer", s.use(s.answer))
	mux.HandleFunc("POST /v1/internal/sessions/{id}/release", s.use(s.release))
	// The session's agent's own commands (an app sets its menu from them).
	mux.HandleFunc("GET /v1/internal/sessions/{id}/commands", func(w http.ResponseWriter, r *http.Request) {
		st, err := s.m.State(r.PathValue("id"))
		if err != nil {
			s.error(w, err)
			return
		}
		reply(w, http.StatusOK, st.Commands)
	})
	s.appRoutes(mux)
	s.shareRoutes(mux) // share.go
	s.secretRoutes(mux)
	s.updateRoutes(mux)
	s.settingsRoutes(mux)
	s.sessionRoutes(mux)
	s.pushRoutes(mux)
	mux.HandleFunc("GET /v1/internal/exec", s.execConnect)
	s.pairRoutes(mux)
	s.machineRoutes(mux)
	s.discoverRoutes(mux)
	s.noteRoutes(mux)
	s.fileRoutes(mux, local)
	if !local {
		s.cliRoutes(mux)
	}
	if local {
		// Satori too: ir speaks it (ir api), as the login it acts for (irCaller).
		return s.socketHandler(s.withSatori(s.sessionGuard(mux)))
	}
	return s.webHandler(s.sessionGuard(mux))
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
	if c := callerOf(r); !c.Host() {
		l = s.filterSessions(s.permsFor(c), l)
	}
	reply(w, http.StatusOK, l)
}

func (s *Server) create(w http.ResponseWriter, r *http.Request) {
	var req api.NewSession
	if !readJSON(w, r, &req) {
		return
	}
	c := callerOf(r)
	// Started from inside a session (ir's X-Iridium-Session, a session's own TOKEN): that session
	// is its launcher unless the body names another, so an agent's new session hangs under it.
	if req.Caller == "" {
		req.Caller = c.session
	}
	// Its folder as written: a place, ~, a path from the caller's own session's (places.go).
	if err := s.where(c, &req); err != nil {
		s.error(w, err)
		return
	}
	if err := s.checkCreate(c, &req); err != nil {
		s.error(w, err)
		return
	}
	id, err := s.m.Create(r.Context(), req, s.fromOf(c, req.Caller))
	if id != "" {
		via := c.session
		if via == "" && c.socket {
			via = req.Caller
		}
		s.noteOrigin(c, id, via)
		if err := s.st.SetOpener(id, c.ID()); err != nil {
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
	out, err := s.sendAs(r.Context(), c, s.fromOf(c, req.Caller), r.PathValue("id"), req)
	if err != nil {
		if errors.Is(err, errFiles) {
			s.fileError(w, errors.Unwrap(err))
			return
		}
		s.error(w, err)
		return
	}
	reply(w, http.StatusOK, out)
}

// errFiles wraps what the files service refused of a message's attachments.
var errFiles = errors.New("attachments")

type fileErr struct{ err error }

func (e fileErr) Error() string        { return e.err.Error() }
func (e fileErr) Unwrap() error        { return e.err }
func (e fileErr) Is(target error) bool { return target == errFiles }

// sendAs sends req into session id as c, from (the source tag): what POST /v1/internal/sessions/{id}/send
// and Satori's message.create both do. /clear starts the session anew.
func (s *Server) sendAs(ctx context.Context, c caller, from, id string, req api.Send) (api.SendReply, error) {
	if err := s.checkSend(c, id); err != nil {
		return api.SendReply{}, err
	}
	via := c.session
	if via == "" && c.socket {
		via = req.Caller
	}
	s.noteOrigin(c, id, via)
	// Attachments and all: Claude would clear itself just the same.
	if session.IsClear(req.Text) {
		nid, err := s.m.Clear(ctx, id, from)
		if nid != "" && nid != id {
			if err := s.st.SetOpener(nid, c.ID()); err != nil {
				s.logf("server: opener of %s: %v", nid, err)
			}
		}
		return api.SendReply{Session: nid}, err
	}
	text, images, err := s.files.Message(id, req.Text, req.Attachments)
	if err != nil {
		return api.SendReply{}, fileErr{err}
	}
	input, queued, err := s.m.SendWith(ctx, id, text, from, req.Steer, images...)
	if err != nil {
		return api.SendReply{}, err
	}
	if input == session.HandedInput {
		return api.SendReply{Desktop: true}, nil
	}
	if st, err := s.m.State(id); err == nil {
		// The others who see it see what was said too (Satori's message-created).
		s.sat.Input(st.Session, input, req.Text, s.satoriUser(c), c.ID())
	}
	return api.SendReply{Input: input, Queued: queued, Command: s.m.RunsItself(id, text)}, nil
}

func (s *Server) interrupt(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	s.done(w, s.m.Interrupt(ctx, r.PathValue("id")))
}

func (s *Server) stop(w http.ResponseWriter, r *http.Request) {
	s.done(w, s.m.Stop(r.Context(), r.PathValue("id")))
}

// answer answers one of session {id}'s asks by its card (decision 27): a CardAnswer for the card
// "ask:<id>:<request>", as the web's panel and the apps' buttons give it.
func (s *Server) answer(w http.ResponseWriter, r *http.Request) {
	var a api.CardAnswer
	if !readJSON(w, r, &a) {
		return
	}
	if !strings.HasPrefix(a.Card, "ask:"+r.PathValue("id")+":") {
		fail(w, http.StatusBadRequest, "not a card of this session: "+a.Card)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	if err := s.m.AnswerCard(ctx, a); err != nil {
		s.fault(w, err)
		return
	}
	reply(w, http.StatusOK, api.CardReply{OK: true})
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

var stateKey = regexp.MustCompile(`^(list|quota|workspaces|places|colors|needs|pairing|update|terminals|machines|notes|(session|context):[0-9a-zA-Z_-]{1,128})$`)

// stateKeys splits the states a WATCH asks for into those it carries and those it leaves
// out (unknown, or not this caller's: needs, pairing, colors and notes are a person's on ir_web
// or at the host).
func (s *Server) stateKeys(c caller, keys []string) (carried []string, ignored []api.StateEvent) {
	for _, k := range keys {
		switch {
		case !stateKey.MatchString(k):
			ignored = append(ignored, api.StateEvent{Key: k, Ignored: "unknown"})
		case (k == "needs" || k == "pairing" || k == "colors" || k == "notes") && !c.panel():
			ignored = append(ignored, api.StateEvent{Key: k, Ignored: "forbidden"})
		case !slices.Contains(carried, k):
			carried = append(carried, k)
		}
	}
	return carried, ignored
}

// watchStates sends c the states keys name (at most 16) and the drafts,
// through emit ("state" or "draft", the JSON), till ctx ends: Satori's /v1/events for ir_web.
func (s *Server) watchStates(ctx context.Context, c caller, keys []string, emit func(kind string, data []byte) error) error {
	if len(keys) > 16 {
		keys = keys[:16]
	}
	carried, ignored := s.stateKeys(c, keys)
	for _, e := range ignored {
		b, _ := json.Marshal(e)
		if err := emit("state", b); err != nil {
			return err
		}
	}
	sub := s.hub.Subscribe(carried)
	defer sub.Close()
	sub.Filter = s.streamFilter(c)
	// A session's state carries its transcript's tail: the credentials' values masked, as every
	// answer that carries a transcript is (routes_secrets.go).
	masked := func(kind string, data []byte) error {
		data = s.jsonMask().Mask(data)
		return emit(kind, data)
	}
	return sub.Run(ctx, masked, func() error { return nil }, func() {})
}

func (s *Server) done(w http.ResponseWriter, err error) {
	if err != nil {
		s.error(w, err)
		return
	}
	reply(w, http.StatusOK, map[string]any{"ok": true})
}

// fault answers with err, knowing the errors of the session settings, the queue and push; the
// rest as s.error does.
func (s *Server) fault(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, session.ErrBadRequest), errors.Is(err, push.ErrBadSubscription), errors.Is(err, push.ErrBadRule):
		fail(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, store.ErrNoInput):
		refuse(w, http.StatusUnprocessableEntity, "no_such_input", err.Error())
	case errors.Is(err, session.ErrGone):
		fail(w, http.StatusGone, err.Error())
	default:
		s.error(w, err)
	}
}

func (s *Server) error(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, session.ErrNotFound):
		refuse(w, http.StatusUnprocessableEntity, "no_such_session", err.Error())
	case errors.Is(err, transcript.ErrNoBlock):
		refuse(w, http.StatusUnprocessableEntity, "no_such_message", err.Error())
	case errors.Is(err, session.ErrNotLive), errors.Is(err, session.ErrElsewhere), errors.Is(err, session.ErrSubagent):
		fail(w, http.StatusConflict, err.Error())
	case errors.Is(err, context.DeadlineExceeded):
		fail(w, http.StatusGatewayTimeout, "the agent did not answer in time")
	case access.IsDenied(err):
		refuse(w, http.StatusForbidden, "no_permission", err.Error())
	case errors.Is(err, session.ErrBadRequest):
		refuse(w, http.StatusBadRequest, "bad_request", err.Error())
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

func fail(w http.ResponseWriter, status int, msg string) { reply(w, status, api.Error{Message: msg}) }

// refuse answers a refusal with its code (decision 8): 403 for what the caller may not do now,
// 422 for what is not there or not right (decision 28).
func refuse(w http.ResponseWriter, status int, code, msg string) {
	reply(w, status, api.Error{Code: code, Message: msg})
}

// execConnect takes an executor's WebSocket (docs/exec.md): a terminal with a TOKEN, as which the
// machine is known.
func (s *Server) execConnect(w http.ResponseWriter, r *http.Request) {
	if s.Remote == nil {
		refuse(w, http.StatusUnprocessableEntity, "not_here", "no executors here")
		return
	}
	c := callerOf(r)
	if c.Login != "" || c.Host() || c.socket || c.session != "" {
		refuse(w, http.StatusForbidden, "no_permission", "an executor connects with a TOKEN")
		return
	}
	s.Remote.Serve(w, r, c.ID(), c.Name)
}
