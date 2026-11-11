package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/gate"
)

// CLIAt runs ir with args for a session at another machine (package cli's RunAt, which needs this
// package's API and so is given, not imported): through hc, as machine's Bot, for session, in cwd
// there; the exit code.
type CLIAt func(args []string, stdout, stderr io.Writer, hc *http.Client, machine, session, cwd string) int

// cliRoutes: POST /v1/internal/cli, the ir of an agent hosted at another machine (docs/exec.md "远端的
// ir"). It comes with its session's own TOKEN, so the caller is that machine's Bot for that
// session; ir runs here, its requests answered by mux (the API as that Bot has it, not the
// socket's), its output streamed back a line at a time.
func (s *Server) cliRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/internal/cli", func(w http.ResponseWriter, r *http.Request) {
		c := callerOf(r)
		if c.session == "" || c.socket {
			forbid(w, errIRAt)
			return
		}
		if s.CLI == nil {
			refuse(w, http.StatusUnprocessableEntity, "not_here", "no ir here")
			return
		}
		var req api.CLIRun
		if !readJSON(w, r, &req) {
			return
		}
		rc := http.NewResponseController(w)
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		out := &cliLines{w: w, flush: func() { _ = rc.Flush() }}
		// Through the session guard as every request is: its scope and agents (S2).
		hc := &http.Client{Transport: inProcess{h: s.sessionGuard(mux), ctx: r.Context(), who: c}}
		code := s.CLI(req.Args, out.as("out"), out.as("err"), hc, c.ID(), c.session, req.Cwd)
		out.line(api.CLIOutput{Exit: &code})
	})
}

var errIRAt = cliError("POST /v1/internal/cli is the ir of a session at another machine, with that session's TOKEN")

type cliError string

func (e cliError) Error() string { return string(e) }

// cliLines writes ir's output as CLIOutput lines, flushed as they go; out and err share it.
type cliLines struct {
	mu    sync.Mutex
	w     io.Writer
	flush func()
}

func (l *cliLines) line(o api.CLIOutput) {
	b, _ := json.Marshal(o)
	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = l.w.Write(append(b, '\n'))
	l.flush()
}

func (l *cliLines) as(stream string) io.Writer {
	return writerFunc(func(p []byte) (int, error) {
		if stream == "out" {
			l.line(api.CLIOutput{Out: string(p)})
		} else {
			l.line(api.CLIOutput{Err: string(p)})
		}
		return len(p), nil
	})
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

// inProcess is an http.RoundTripper that answers with h itself, as who, under ctx: what ir sends
// for a session at another machine. The answer streams (ir wait reads events as they come).
type inProcess struct {
	h   http.Handler
	ctx context.Context
	who caller
}

func (t inProcess) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := context.WithValue(gate.NewContext(t.ctx, t.who.Identity), callerKey{}, t.who)
	ctx, cancel := context.WithCancel(ctx)
	r := req.Clone(ctx)
	r.RequestURI = req.URL.RequestURI()
	r.RemoteAddr = "in-process"
	pr, pw := io.Pipe()
	rw := &pipeWriter{header: http.Header{}, pw: pw, ready: make(chan struct{})}
	go func() {
		defer func() {
			rw.WriteHeader(http.StatusOK)
			pw.Close()
		}()
		t.h.ServeHTTP(rw, r)
	}()
	select {
	case <-rw.ready:
	case <-ctx.Done():
		cancel()
		return nil, ctx.Err()
	}
	return &http.Response{StatusCode: rw.code, Status: http.StatusText(rw.code), Header: rw.header, Body: cancelBody{pr, cancel}, Request: req, ProtoMajor: 1, ProtoMinor: 1}, nil
}

// pipeWriter is the handler's side of an inProcess answer.
type pipeWriter struct {
	header http.Header
	pw     *io.PipeWriter
	once   sync.Once
	code   int
	ready  chan struct{}
}

func (p *pipeWriter) Header() http.Header { return p.header }

func (p *pipeWriter) WriteHeader(code int) {
	p.once.Do(func() {
		p.code = code
		close(p.ready)
	})
}

func (p *pipeWriter) Write(b []byte) (int, error) {
	p.WriteHeader(http.StatusOK)
	return p.pw.Write(b)
}

func (p *pipeWriter) Flush() {}

// cancelBody ends the handler's request when the body is closed (ir done with an event stream).
type cancelBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b cancelBody) Close() error {
	b.cancel()
	return b.ReadCloser.Close()
}
