package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/config"
	"github.com/kumoSleeping/iridium/internal/hub"
	"github.com/kumoSleeping/iridium/internal/platform"
	"github.com/kumoSleeping/iridium/internal/session"
	"github.com/kumoSleeping/iridium/internal/store"
	"github.com/kumoSleeping/iridium/internal/testagent"
)

// daemon is one run of the daemon on a state that outlives it.
type daemon struct {
	base string
	stop func()
}

func runDaemon(t *testing.T, cfg config.Config, st *store.Store, l *platform.Direct) *daemon {
	t.Helper()
	h := hub.New()
	var srv *Server
	m, err := session.New(session.Options{Config: cfg, Store: st, Hub: h, Launcher: l, Exe: os.Args[0], UnitPrefix: "iridium-test-", Logf: t.Logf, Poll: 10 * time.Millisecond, ReadEvery: 20 * time.Millisecond, Tag: func(id string) string { return srv.Tag(id) }})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Start(); err != nil {
		t.Fatal(err)
	}
	srv = New(cfg, m, h, t.Logf)
	tcp, unix, err := srv.Listen()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, tcp, unix) }()
	d := &daemon{base: "http://" + tcp.Addr().String()}
	d.stop = func() { cancel(); <-done; srv.Close(); m.Close() }
	return d
}

// After a restart, over HTTP: the open ask is back with its card and POST /v1/internal/sessions/{id}/answer ends the
// turn; the context and the quota come back as last read, on GET and on the stream.
func TestRestartOverHTTP(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	os.MkdirAll(bin, 0o700)
	cfg := config.Default()
	cfg.Listen = "127.0.0.1:0"
	cfg.StateDir = filepath.Join(root, "state")
	cfg.Socket = filepath.Join(root, "run", "ir.sock")
	cfg.ClaudeHome = filepath.Join(root, "claude")
	cfg.Programs = testagent.Programs(t, bin)
	work := filepath.Join(root, "work")
	os.MkdirAll(work, 0o700)
	os.MkdirAll(cfg.StateDir, 0o700)
	st, err := store.Open(filepath.Join(cfg.StateDir, "iridium.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	l := &platform.Direct{}
	t.Cleanup(func() { testagent.KillAll(l, "iridium-test-") })
	c := &http.Client{Timeout: 10 * time.Second}
	state := func(base, id string) api.SessionState {
		var s api.SessionState
		if code := call(t, c, "GET", base+"/v1/internal/sessions/"+id, nil, &s); code != 200 {
			t.Fatalf("state: %d", code)
		}
		return s
	}
	wait := func(base, id, what string, ok func(api.SessionState) bool) api.SessionState {
		t.Helper()
		for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
			if s := state(base, id); ok(s) {
				return s
			}
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s: %+v", what, state(base, id))
			}
		}
	}

	d := runDaemon(t, cfg, st, l)
	var created api.NewSessionReply
	call(t, c, "POST", d.base+"/v1/internal/sessions", api.NewSession{Agent: api.AgentClaude, Cwd: work, Text: "first"}, &created)
	id := created.ID
	before := wait(d.base, id, "the context", func(s api.SessionState) bool { return s.Context != nil && s.Status == api.StatusIdle })
	call(t, c, "POST", d.base+"/v1/internal/sessions/"+id+"/send", api.Send{Text: "please ASK"}, nil)
	wait(d.base, id, "the ask", func(s api.SessionState) bool { return s.Status == api.StatusWaiting })
	d.stop()

	d = runDaemon(t, cfg, st, l)
	defer d.stop()
	// Past the replayed output and the new handshake both: an ask answered before the handshake
	// is told open again by it (session.TestAskAfterRestart waits the same).
	time.Sleep(300 * time.Millisecond)
	s := wait(d.base, id, "the ask again", func(s api.SessionState) bool { return s.Live && len(s.Asks) == 1 && s.Asks[0].Card != nil })
	if s.Context == nil || s.Context.At != before.Context.At || s.Context.Used != before.Context.Used {
		t.Errorf("context after the restart: %+v, before %+v", s.Context, before.Context)
	}

	// The stream's first states: the context's parts and the quota, as last read.
	got := map[string]json.RawMessage{}
	states := watchStates(t, d.base, nil, "context:"+id, "quota")
	for deadline := time.After(5 * time.Second); len(got) < 2; {
		select {
		case st := <-states:
			var e struct {
				Key   string          `json:"key"`
				Value json.RawMessage `json:"value"`
			}
			json.Unmarshal(st.Data, &e)
			got[e.Key] = e.Value
		case <-deadline:
			t.Fatalf("states after the restart: %d", len(got))
		}
	}
	var cx api.Context
	var q api.Quota
	json.Unmarshal(got["context:"+id], &cx)
	json.Unmarshal(got["quota"], &q)
	if cx.Used != 100 || len(cx.Categories) == 0 || cx.At != before.Context.At {
		t.Errorf("context:<id> after the restart: %s", got["context:"+id])
	}
	if len(q.Agents) != 1 || len(q.Agents[0].Windows) != 1 || q.Agents[0].At == 0 {
		t.Errorf("quota after the restart: %s", got["quota"])
	}

	var cr api.CardReply
	if code := call(t, c, "POST", d.base+"/v1/internal/sessions/"+id+"/answer", api.CardAnswer{Card: s.Asks[0].Card.ID, Option: "allow"}, &cr); code != 200 || !cr.OK {
		t.Fatalf("answer: %d %+v", code, cr)
	}
	wait(d.base, id, "the reply", func(s api.SessionState) bool {
		return s.Last != nil && s.Last.Text == "ok: please ASK" && s.Status == api.StatusIdle
	})
}
