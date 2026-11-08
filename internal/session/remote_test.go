package session

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/config"
	"github.com/kumoSleeping/iridium/internal/discover"
	"github.com/kumoSleeping/iridium/internal/hold"
	"github.com/kumoSleeping/iridium/internal/remote"
	"github.com/kumoSleeping/iridium/internal/remote/executor"
	"github.com/kumoSleeping/iridium/internal/remote/wire"
	"github.com/kumoSleeping/iridium/internal/transcript"
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
		URL: "ws" + strings.TrimPrefix(srv.URL, "http"), State: filepath.Join(there, "executor"), Env: env, Logf: t.Logf, AgentShell: true,
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

	testShell(t, r.m, id, work)
	// An agent's ir sh: at the machine, in its home unless told.
	if st, err := r.m.MachineShell(bg, "m1", "echo agent; pwd", work); err != nil {
		t.Error(err)
	} else if out, end := shellAll(t, r.m, st.Run); !strings.Contains(out, "agent\n") || end.Code == nil || *end.Code != 0 {
		t.Errorf("ir sh: %q %+v", out, end)
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

// A dsh session DSH Desktop made at a machine (docs/exec.md "远端 DSH"): listed and read through
// discovery, then taken up by a message: resumed there, hosted here from then on.
func TestTakeRemoteDsh(t *testing.T) {
	hub := &remote.Hub{Logf: t.Logf}
	var d *discover.Index
	r := newRig(t, func(c *config.Config, o *Options) {
		o.Remote = hub
		d = discover.New(discover.Options{ClaudeDir: c.ClaudeHome, CodexHome: t.TempDir(), PiSessions: t.TempDir(), DshHome: t.TempDir(),
			State: t.TempDir(), RemoteScan: time.Hour, Store: o.Store, Procs: &procs{}, UID: os.Getuid(), Logf: t.Logf})
		d.SetRemote(hub)
		o.Discovery = d
	})
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
	there := t.TempDir()
	work := filepath.Join(there, "work")
	home := filepath.Join(there, ".dsh")
	os.MkdirAll(work, 0o700)
	fixture := abs("../transcript/testdata/dsh/sessions/--tmp-dsh-probe-w--/sess-allow/session.v4.jsonl.zstd")
	env := append(os.Environ(), "IRIDIUM_FAKE_DSH="+abs("../agent/dsh/testdata/turn"), "IRIDIUM_FAKE_DSH_TRANSCRIPT="+fixture)
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

	// DSH Desktop's session: its transcript, and its summary saying its folder and title.
	const id = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	b, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, "sessions", transcript.DshProjectDir(work), id)
	os.MkdirAll(dir, 0o700)
	os.WriteFile(filepath.Join(dir, "session.v4.jsonl.zstd"), b, 0o600)
	sums := filepath.Join(home, "storages", "session_projcache", "sessions")
	os.MkdirAll(sums, 0o700)
	os.WriteFile(filepath.Join(sums, id+".json"), []byte(`{"version":7,"record":{"identity":{"cwd":"`+work+`"},"rows":{"title":{"ver":1,"seq":1,"val":"from desktop"}}}}`), 0o600)
	if err := d.ScanMachine(bg, "m1"); err != nil {
		t.Fatal(err)
	}
	l, _ := r.m.List(bg)
	if len(l) != 1 || l[0].ID != id || l[0].Machine != "m1" || l[0].Hosted || l[0].Cwd != work || l[0].Title != "from desktop" || l[0].Archived {
		t.Fatalf("list: %+v", l)
	}
	if len(r.messages(id)) == 0 {
		t.Fatal("no messages: not mirrored")
	}

	if _, err := r.m.Send(bg, id, "RUNTOOL:bash say hi", "web"); err != nil {
		t.Fatal(err)
	}
	h, err := r.m.get(id)
	if err != nil {
		t.Fatal(err)
	}
	if h.meta.Machine != "m1" || h.meta.Cwd != work {
		t.Errorf("taken: %+v", h.meta)
	}
	waitFor(t, "the turn's output", func() bool {
		b, _ := os.ReadFile(filepath.Join(h.dir, hold.FileOutLog))
		return strings.Contains(string(b), `"turn/end"`)
	})
	b, _ = os.ReadFile(filepath.Join(h.dir, hold.FileInLog))
	if !strings.Contains(string(b), `"session/resume"`) {
		t.Errorf("not resumed: %s", b)
	}
	if l, _ := r.m.List(bg); len(l) != 1 || !l[0].Hosted || l[0].Machine != "m1" {
		t.Errorf("list after: %+v", l)
	}
	if err := r.m.Stop(bg, id); err != nil {
		t.Fatal(err)
	}
	r.waitState(id, "stopped", func(s *api.SessionState) bool { return !s.Live })
}

// A dsh session Iridium started at a machine is filed under DSH Desktop's workspace of its folder
// once its transcript is there (dsh.attach, the dsh plugin's), once; the plugin stands in for
// DSH Desktop in a directory (run.mjs).
func TestFileInDesktop(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("no node")
	}
	hub := &remote.Hub{Logf: t.Logf}
	r := newRig(t, func(_ *config.Config, o *Options) { o.Remote = hub })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { hub.Serve(w, req, "m1", "pc") }))
	t.Cleanup(srv.Close)
	there := t.TempDir()
	work, home, desktop := filepath.Join(there, "work"), filepath.Join(there, ".dsh"), filepath.Join(there, "desktop")
	os.MkdirAll(work, 0o700)
	os.MkdirAll(desktop, 0o700)
	os.WriteFile(filepath.Join(desktop, "open.json"), []byte("[]"), 0o600)
	agents, _ := json.Marshal([]wire.AgentInfo{{Agent: "dsh", Argv: []string{"dsh"}, Version: "0.2.2-rc.1", Home: home}})
	cmd := exec.Command("node", "../../plugins/dsh-remote-iridium/test/run.mjs", "ws"+strings.TrimPrefix(srv.URL, "http"), "tok", filepath.Join(there, "executor"))
	cmd.Env = append(os.Environ(), "IRIDIUM_TEST_AGENTS="+string(agents), "IRIDIUM_TEST_DSH="+desktop)
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Signal(syscall.SIGTERM); _ = cmd.Wait() })
	waitFor(t, "the executor", func() bool { _, ok := hub.Machine("m1").Hello(); return ok })

	const id = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	h := r.m.newHosted(meta{ID: id, Agent: api.AgentDsh, Cwd: work, Created: time.Now().UnixMilli(), Machine: "m1"})
	os.MkdirAll(h.dir, 0o700)
	if p, err := h.findRemoteTranscript(hub.Machine("m1")); err != nil || p != "" {
		t.Fatalf("before its transcript: %q %v", p, err)
	}
	dir := filepath.Join(home, "sessions", transcript.DshProjectDir(work), id)
	os.MkdirAll(dir, 0o700)
	os.WriteFile(filepath.Join(dir, "session.v4.jsonl.zstd"), []byte("x"), 0o600)
	for range 2 {
		if _, err := h.findRemoteTranscript(hub.Machine("m1")); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "filed", func() bool { _, err := os.Stat(filepath.Join(h.dir, fileFiled)); return err == nil })
	}
	b, _ := os.ReadFile(filepath.Join(desktop, "attached.jsonl"))
	if got := strings.TrimSpace(string(b)); got != `{"session":"`+id+`","cwd":"`+work+`"}` {
		t.Errorf("attached: %s", got)
	}
}
