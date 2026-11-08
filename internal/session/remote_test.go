package session

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/config"
	"github.com/kumoSleeping/iridium/internal/hold"
	"github.com/kumoSleeping/iridium/internal/remote"
	"github.com/kumoSleeping/iridium/internal/remote/executor"
	"github.com/kumoSleeping/iridium/internal/remote/wire"
)

// A dsh session at an executor (docs/exec.md): started there, its turn relayed, its transcript
// mirrored here, stopped. The executor is the reference one in Go, its dsh a replay of a recorded
// turn (testagent.Dsh).
func TestRemoteDsh(t *testing.T) {
	hub := &remote.Hub{Logf: t.Logf}
	r := newRig(t, func(_ *config.Config, o *Options) { o.Remote = hub })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		hub.Serve(w, req, "m1", "pc")
	}))
	t.Cleanup(srv.Close)

	abs := func(p string) string {
		a, err := filepath.Abs(p)
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	there := t.TempDir() // the other machine
	work := filepath.Join(there, "work")
	home := filepath.Join(there, ".dsh")
	os.MkdirAll(work, 0o700)
	env := append(os.Environ(),
		"IRIDIUM_FAKE_DSH="+abs("../agent/dsh/testdata/turn"),
		"IRIDIUM_FAKE_DSH_TRANSCRIPT="+abs("../transcript/testdata/dsh/sessions/--tmp-dsh-probe-w--/sess-allow/session.v4.jsonl.zstd"))
	exe, err := executor.New(executor.Options{
		URL: "ws" + strings.TrimPrefix(srv.URL, "http"), State: filepath.Join(there, "executor"), Env: env, Logf: t.Logf,
		Agents: []wire.AgentInfo{{Agent: "dsh", Argv: []string{os.Args[0]}, Version: "0.2.2-rc.1",
			Scope: abs("../agent/dsh/testdata/pkg/node_modules/@deepseek-ai"), Home: home}},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = exe.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done; exe.Close() })
	waitFor(t, "the executor", func() bool { _, ok := hub.Machine("m1").Hello(); return ok })

	if _, err := r.m.Create(bg, api.NewSession{Agent: api.AgentDsh, Cwd: "relative", Machine: "pc"}, "web"); err == nil {
		t.Fatal("a relative cwd there was taken")
	}
	if _, err := r.m.Create(bg, api.NewSession{Agent: api.AgentClaude, Cwd: work, Machine: "pc"}, "web"); err == nil {
		t.Fatal("Claude at an executor was taken")
	}
	id, err := r.m.Create(bg, api.NewSession{Agent: api.AgentDsh, Cwd: work, Machine: "pc"}, "web")
	if err != nil {
		t.Fatal(err)
	}
	r.waitState(id, "started", func(s *api.SessionState) bool { return s.Live && s.Status == api.StatusIdle })
	if _, err := r.m.Send(bg, id, "RUNTOOL:bash say hi", "web"); err != nil {
		t.Fatal(err)
	}
	h, _ := r.m.get(id)
	waitFor(t, "the turn's output", func() bool {
		b, _ := os.ReadFile(filepath.Join(h.dir, hold.FileOutLog))
		return strings.Contains(string(b), `"turn/end"`)
	})
	s := r.waitState(id, "the turn's end", func(s *api.SessionState) bool { return s.Status == api.StatusIdle })
	if s.Machine != "m1" || s.Offline {
		t.Errorf("machine %q, offline %v", s.Machine, s.Offline)
	}
	// The patch went there, the transcript came here.
	if _, err := os.Stat(filepath.Join(there, "executor", "scratch", id, "dsh-patch.yml")); err != nil {
		t.Errorf("patch there: %v", err)
	}
	if len(r.messages(id)) == 0 {
		t.Error("no messages from the mirrored transcript")
	}
	if _, err := os.Stat(filepath.Join(h.dir, "remote", "session.v4.jsonl.zstd")); err != nil {
		t.Errorf("mirror: %v", err)
	}

	// The executor goes: the session shows it, and its agent is still there when it is back.
	exe.Drop()
	if s, _ := r.m.State(id); !s.Live {
		t.Error("not live while away")
	}

	if err := r.m.Stop(bg, id); err != nil {
		t.Fatal(err)
	}
	r.waitState(id, "stopped", func(s *api.SessionState) bool { return !s.Live })
	if s, _ := r.m.State(id); s.Error != "" {
		t.Errorf("stopped with an error: %s", s.Error)
	}
}
