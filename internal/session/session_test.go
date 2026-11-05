package session

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/config"
	"github.com/kumoSleeping/iridium/internal/hold"
	"github.com/kumoSleeping/iridium/internal/hub"
	"github.com/kumoSleeping/iridium/internal/platform"
	"github.com/kumoSleeping/iridium/internal/store"
	"github.com/kumoSleeping/iridium/internal/testagent"
)

// The test binary is also the holder and, through a link named claude, a fake Claude.
func TestMain(m *testing.M) {
	if code, ok := testagent.Main(); ok {
		os.Exit(code)
	}
	os.Exit(m.Run())
}

type rig struct {
	t     *testing.T
	m     *Manager
	hub   *hub.Hub
	store *store.Store
	cfg   config.Config
	l     *platform.Direct
	cwd   string
}

func newRig(t *testing.T, edit func(*config.Config, *Options)) *rig {
	t.Helper()
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	os.MkdirAll(bin, 0o700)
	if err := os.Symlink(os.Args[0], filepath.Join(bin, "claude")); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.StateDir = filepath.Join(root, "state")
	cfg.ClaudeHome = filepath.Join(root, "claude")
	cfg.Programs["claude"] = filepath.Join(bin, "claude")
	cfg.StopGrace = config.Duration(time.Second)
	os.MkdirAll(cfg.StateDir, 0o700)
	st, err := store.Open(filepath.Join(cfg.StateDir, "iridium.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	r := &rig{t: t, store: st, cfg: cfg, l: &platform.Direct{}, cwd: filepath.Join(root, "work")}
	os.MkdirAll(r.cwd, 0o700)
	o := Options{Config: cfg, Store: st, Launcher: r.l, Exe: os.Args[0], UnitPrefix: "iridium-test-", Logf: t.Logf, Poll: 10 * time.Millisecond}
	if edit != nil {
		edit(&o.Config, &o)
	}
	r.cfg = o.Config
	r.start(o)
	t.Cleanup(func() {
		// Stop every agent this test started.
		names, _ := r.l.List(context.Background(), "iridium-test-")
		for _, n := range names {
			r.l.Kill(context.Background(), n, "KILL", true)
		}
	})
	return r
}

func (r *rig) start(o Options) {
	r.hub = hub.New()
	o.Hub = r.hub
	m, err := New(o)
	if err != nil {
		r.t.Fatal(err)
	}
	if err := m.Start(); err != nil {
		r.t.Fatal(err)
	}
	r.m = m
	r.t.Cleanup(m.Close)
}

func (r *rig) waitState(id string, what string, ok func(*api.SessionState) bool) *api.SessionState {
	r.t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		s, err := r.m.State(id)
		if err != nil {
			r.t.Fatal(err)
		}
		if ok(s) {
			return s
		}
	}
	s, _ := r.m.State(id)
	r.t.Fatalf("timed out waiting for %s: %+v last %+v", what, s, s.Last)
	return nil
}

func (r *rig) messages(id string) []api.Block {
	r.t.Helper()
	p, err := r.m.Messages(id, -1, -1)
	if err != nil {
		r.t.Fatal(err)
	}
	return p.Blocks
}

func TestHostedTurn(t *testing.T) {
	r := newRig(t, nil)
	sub := r.hub.Subscribe([]string{"list"})
	defer sub.Close()
	id, err := r.m.Create(context.Background(), api.NewSession{Agent: api.AgentClaude, Cwd: r.cwd, Text: "hello"}, "网页")
	if err != nil {
		t.Fatal(err)
	}
	s := r.waitState(id, "the reply", func(s *api.SessionState) bool {
		return s.Status == api.StatusIdle && s.Last != nil && s.Last.Text == "ok: hello"
	})
	if !s.Live || s.Model != "fake-model" || s.Mode != "default" || s.Proc != 1 || s.Title != "hello" {
		t.Errorf("state: %+v", s)
	}
	b := r.messages(id)
	if len(b) != 2 || b[0].Kind != api.BlockUser || b[0].Text != "hello" || b[0].From != "网页" || b[1].Text != "ok: hello" {
		t.Fatalf("blocks: %+v", b)
	}
	// Each turn's share of the session's running totals.
	waitFor(t, "the usage row", func() bool { u, _ := r.store.UsageSince(0); return len(u) == 1 })
	u, _ := r.store.UsageSince(0)
	if u[0].Model != "fake-model" || u[0].Terminal != "网页" || u[0].CostUSD == nil || *u[0].CostUSD < 0.0099 || *u[0].CostUSD > 0.0101 {
		t.Errorf("usage: %+v", u[0])
	}
	waitFor(t, "context and quota", func() bool {
		return string(r.hub.Get("context:"+id)) != "null" && strings.Contains(string(r.hub.Get("quota")), `"percent":10`)
	})
	// A second message, from another terminal: one more row, a cent again.
	if _, err := r.m.Send(context.Background(), id, "again", "本机"); err != nil {
		t.Fatal(err)
	}
	r.waitState(id, "the second reply", func(s *api.SessionState) bool { return s.Last != nil && s.Last.Text == "ok: again" })
	waitFor(t, "the second usage row", func() bool { u, _ := r.store.UsageSince(0); return len(u) == 2 })
	u, _ = r.store.UsageSince(0)
	if u[1].Terminal != "本机" || *u[1].CostUSD < 0.0099 || *u[1].CostUSD > 0.0101 {
		t.Errorf("second usage: %+v", u[1])
	}
	// The agent read the source before each message.
	inLog, _ := os.ReadFile(filepath.Join(r.cfg.StatePath(), "hold", id, hold.FileInLog))
	if !strings.Contains(string(inLog), `"text":"[网页] hello"`) || !strings.Contains(string(inLog), `"text":"[本机] again"`) {
		t.Errorf("in.log: %s", inLog)
	}
	list, _ := r.m.List(context.Background())
	if len(list) != 1 || list[0].ID != id || !list[0].Hosted {
		t.Errorf("list: %+v", list)
	}
}

func TestAskAnswered(t *testing.T) {
	r := newRig(t, nil)
	id, err := r.m.Create(context.Background(), api.NewSession{Cwd: r.cwd, Text: "please ASK"}, "网页")
	if err != nil {
		t.Fatal(err)
	}
	s := r.waitState(id, "the ask", func(s *api.SessionState) bool { return s.Status == api.StatusWaiting })
	if len(s.Asks) != 1 || s.Asks[0].Kind != "permission" || s.Asks[0].Tool != "Bash" {
		t.Fatalf("asks: %+v", s.Asks)
	}
	if err := r.m.Answer(context.Background(), id, api.Answer{RequestID: s.Asks[0].RequestID, Allow: true}); err != nil {
		t.Fatal(err)
	}
	s = r.waitState(id, "the reply", func(s *api.SessionState) bool { return s.Last != nil && s.Last.Text == "ok: please ASK" })
	if len(s.Asks) != 0 {
		t.Errorf("asks after the answer: %+v", s.Asks)
	}
}

// Stopped, the session stays; the next message starts the agent again, resuming the session.
func TestStopAndResume(t *testing.T) {
	r := newRig(t, nil)
	id, _ := r.m.Create(context.Background(), api.NewSession{Cwd: r.cwd, Text: "one"}, "网页")
	r.waitState(id, "the reply", func(s *api.SessionState) bool {
		return s.Last != nil && s.Last.Text == "ok: one" && s.Status == api.StatusIdle
	})
	if err := r.m.Stop(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	s := r.waitState(id, "the stop", func(s *api.SessionState) bool { return !s.Live })
	if s.Status != api.StatusIdle || s.Error != "" {
		t.Fatalf("stopped: %+v", s)
	}
	spec, _ := os.ReadFile(filepath.Join(r.cfg.StatePath(), "hold", id, hold.FileSpec))
	if !strings.Contains(string(spec), "--session-id="+id) {
		t.Errorf("first spec: %s", spec)
	}
	if _, err := r.m.Send(context.Background(), id, "two", "网页"); err != nil {
		t.Fatal(err)
	}
	s = r.waitState(id, "the second reply", func(s *api.SessionState) bool { return s.Last != nil && s.Last.Text == "ok: two" })
	spec, _ = os.ReadFile(filepath.Join(r.cfg.StatePath(), "hold", id, hold.FileSpec))
	if s.Proc != 2 || !strings.Contains(string(spec), "--resume="+id) || !strings.Contains(string(spec), "IRIDIUM_SESSION="+id) {
		t.Errorf("proc %d, spec %s", s.Proc, spec)
	}
	if b := r.messages(id); len(b) != 4 {
		t.Errorf("blocks after resume: %+v", b)
	}
	// The resumed process counted on from its running totals: still a cent a turn.
	waitFor(t, "two usage rows", func() bool { u, _ := r.store.UsageSince(0); return len(u) == 2 })
	u, _ := r.store.UsageSince(0)
	if u[1].CostUSD == nil || *u[1].CostUSD > 0.0101 {
		t.Errorf("usage after resume: %+v", u[1])
	}
}

func TestCrash(t *testing.T) {
	r := newRig(t, nil)
	id, _ := r.m.Create(context.Background(), api.NewSession{Cwd: r.cwd, Text: "crash"}, "网页")
	s := r.waitState(id, "the crash", func(s *api.SessionState) bool { return !s.Live && s.Status == api.StatusError })
	if !strings.Contains(s.Error, "agent exited 3") {
		t.Errorf("error: %q", s.Error)
	}
}

func TestIdleReap(t *testing.T) {
	r := newRig(t, func(c *config.Config, o *Options) {
		c.IdleReap = config.Duration(200 * time.Millisecond)
		o.ReapEvery = 50 * time.Millisecond
	})
	id, _ := r.m.Create(context.Background(), api.NewSession{Cwd: r.cwd, Text: "hi"}, "网页")
	r.waitState(id, "the reply", func(s *api.SessionState) bool { return s.Last != nil && s.Last.Text == "ok: hi" })
	s := r.waitState(id, "the reap", func(s *api.SessionState) bool { return !s.Live })
	if s.Status != api.StatusIdle || s.Error != "" {
		t.Errorf("reaped: %+v", s)
	}
	// A session's own setting overrides: never.
	off := time.Duration(0)
	r.store.SetSettings(id, store.Settings{IdleReap: &off})
	r.m.Send(context.Background(), id, "again", "网页")
	r.waitState(id, "the reply", func(s *api.SessionState) bool { return s.Last != nil && s.Last.Text == "ok: again" })
	time.Sleep(500 * time.Millisecond)
	if s, _ := r.m.State(id); !s.Live {
		t.Error("reaped a session set never to be")
	}
}

// A new daemon takes up a holder the last one started: its state comes back from out.log, the
// turns it saw are not counted twice, and it goes on taking messages.
func TestDaemonRestart(t *testing.T) {
	r := newRig(t, nil)
	id, _ := r.m.Create(context.Background(), api.NewSession{Cwd: r.cwd, Text: "before"}, "网页")
	r.waitState(id, "the reply", func(s *api.SessionState) bool {
		return s.Last != nil && s.Last.Text == "ok: before" && s.Status == api.StatusIdle
	})
	waitFor(t, "the usage row", func() bool { u, _ := r.store.UsageSince(0); return len(u) == 1 })
	r.m.Close() // the daemon goes; the holder and the agent stay

	o := Options{Config: r.cfg, Store: r.store, Launcher: r.l, Exe: os.Args[0], UnitPrefix: "iridium-test-", Logf: t.Logf, Poll: 10 * time.Millisecond}
	r.start(o)
	s := r.waitState(id, "the session taken up", func(s *api.SessionState) bool { return s.Live && s.Mode == "default" })
	if s.Proc != 1 || s.Model != "fake-model" {
		t.Errorf("taken up: %+v", s)
	}
	if _, err := r.m.Send(context.Background(), id, "after", "网页"); err != nil {
		t.Fatal(err)
	}
	r.waitState(id, "the reply after", func(s *api.SessionState) bool { return s.Last != nil && s.Last.Text == "ok: after" })
	waitFor(t, "the second usage row", func() bool { u, _ := r.store.UsageSince(0); return len(u) >= 2 })
	time.Sleep(100 * time.Millisecond)
	if u, _ := r.store.UsageSince(0); len(u) != 2 {
		t.Errorf("usage rows: %+v", u)
	}
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}
