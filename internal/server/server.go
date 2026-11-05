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
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/chat"
	"github.com/kumoSleeping/iridium/internal/config"
	"github.com/kumoSleeping/iridium/internal/hub"
	"github.com/kumoSleeping/iridium/internal/push"
	"github.com/kumoSleeping/iridium/internal/session"
	"github.com/kumoSleeping/iridium/internal/transcript"
)

// Terminal names until interactive terminals exist: the page on the loopback address, and the
// built-in channel terminal of the local ir.
const (
	TerminalWeb   = "网页"
	TerminalLocal = "本机"
)

type Server struct {
	cfg  config.Config
	m    *session.Manager
	hub  *hub.Hub
	logf func(string, ...any)

	// Set before Handler, if at all: the plugin chat layer, Web Push, and who a request is from.
	Chat *chat.Service
	Push *push.Service
	Who  Identities
}

func New(cfg config.Config, m *session.Manager, h *hub.Hub, logf func(string, ...any)) *Server {
	if logf == nil {
		logf = log.Printf
	}
	return &Server{cfg: cfg, m: m, hub: h, logf: logf}
}

type ctxKey struct{}

func terminal(r *http.Request) string { return r.Context().Value(ctxKey{}).(string) }

// Handler is the API. local: requests come from the unix socket, from the local ir.
func (s *Server) Handler(local bool) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		reply(w, http.StatusOK, map[string]any{"ok": true})
	})
	mux.HandleFunc("GET /api/sessions", s.list)
	mux.HandleFunc("POST /api/sessions", s.create)
	mux.HandleFunc("GET /api/sessions/{id}", s.get)
	mux.HandleFunc("GET /api/sessions/{id}/messages", s.messages)
	mux.HandleFunc("GET /api/sessions/{id}/block", s.block)
	mux.HandleFunc("POST /api/sessions/{id}/send", s.send)
	mux.HandleFunc("POST /api/sessions/{id}/interrupt", s.interrupt)
	mux.HandleFunc("POST /api/sessions/{id}/stop", s.stop)
	mux.HandleFunc("POST /api/sessions/{id}/answer", s.answer)
	mux.HandleFunc("GET /api/events", s.events)
	s.sessionRoutes(mux)
	s.pushRoutes(mux)
	s.pluginRoutes(mux)
	name := TerminalWeb
	if local {
		name = TerminalLocal
	}
	var h http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mux.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, name)))
	})
	if !local {
		h = s.guard(h)
	}
	return h
}

// guard keeps the loopback address for this machine's own pages: only known Host names (no DNS
// rebinding), and no writes from another origin.
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		host = strings.Trim(host, "[]")
		if !slices.Contains(append([]string{"localhost", "127.0.0.1", "::1"}, s.cfg.Hosts...), strings.ToLower(host)) {
			fail(w, http.StatusMisdirectedRequest, "unknown host")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if o := r.Header.Get("Origin"); o != "" {
				if u, err := url.Parse(o); err != nil || u.Host != r.Host {
					fail(w, http.StatusForbidden, "cross-origin request")
					return
				}
			}
			if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
				fail(w, http.StatusForbidden, "cross-site request")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// Listen opens the TCP address and the unix socket. A socket file left by a daemon that is gone
// is replaced; one a running daemon answers on is an error.
func (s *Server) Listen() (tcp, unix net.Listener, err error) {
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
	id, err := s.m.Create(r.Context(), req, terminal(r))
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
	input, queued, err := s.m.SendWith(r.Context(), r.PathValue("id"), req.Text, terminal(r), req.Steer)
	if err != nil {
		s.error(w, err)
		return
	}
	reply(w, http.StatusOK, api.SendReply{Input: input, Queued: queued})
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

var stateKey = regexp.MustCompile(`^(list|quota|(session|context):[0-9a-fA-F-]{1,64})$`)

// events is the page's one event stream: ?sub=<key> for each state it wants (list, quota,
// session:<id>, context:<id>). See package hub.
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
	for _, k := range keys {
		if !stateKey.MatchString(k) {
			fail(w, http.StatusBadRequest, "unknown state "+k)
			return
		}
	}
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{})
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	io.WriteString(w, "retry: 2000\n\n")
	_ = rc.Flush()
	sub := s.hub.Subscribe(keys)
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
	case errors.Is(err, session.ErrNotLive):
		fail(w, http.StatusConflict, err.Error())
	case errors.Is(err, context.DeadlineExceeded):
		fail(w, http.StatusGatewayTimeout, "the agent did not answer in time")
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
