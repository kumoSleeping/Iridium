package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/agent"
	"github.com/kumoSleeping/iridium/internal/agent/dsh"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/config"
	"github.com/kumoSleeping/iridium/internal/discover"
	"github.com/kumoSleeping/iridium/internal/harness"
	hdsh "github.com/kumoSleeping/iridium/internal/harness/dsh"
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
	t.Parallel()
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
		URL: "ws" + strings.TrimPrefix(srv.URL, "http"), State: filepath.Join(there, "executor"), Env: env, Logf: t.Logf, Shell: true,
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
	// A session there whose agent has not started: its models are the machine's, probed when the
	// page opens it, before the first message; so are its efforts.
	const idle = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	ih := r.m.newHosted(meta{ID: idle, Agent: api.AgentDsh, Cwd: work, Created: time.Now().UnixMilli(), Machine: "m1"})
	os.MkdirAll(ih.dir, 0o700)
	r.m.mu.Lock()
	r.m.hosted[idle] = ih
	r.m.mu.Unlock()
	if st, _ := r.m.State(idle); st == nil || st.Live {
		t.Fatalf("idle: %+v", st)
	}
	waitFor(t, "the idle session's models", func() bool {
		st, _ := r.m.State(idle)
		return st != nil && len(st.Models) == 2 && st.Model == "deepseek-official/deepseek-flash" && st.Models[1].Value == "deepseek-official/deepseek-v4-pro"
	})
	if err := r.m.SetEffort(bg, idle, "medium"); !errors.Is(err, ErrBadRequest) {
		t.Errorf("an effort the model has not: %v", err)
	}
	if err := r.m.SetModel(bg, idle, "deepseek-official/deepseek-v4-pro"); err != nil {
		t.Errorf("a model before the first message: %v", err)
	}
	if st, _ := r.m.State(idle); st.Pick != "deepseek-official/deepseek-v4-pro" || st.Live {
		t.Errorf("picked: %q live %v", st.Pick, st.Live)
	}

	// No dsh plugin there: the presets its package's files register.
	if l, def, err := r.m.Presets(bg, api.AgentDsh, "m1"); err != nil || def != "standard" || presetIDs(l) != "standard ptc minimal" {
		t.Fatalf("presets there: %q %q %v", presetIDs(l), def, err)
	}
	if _, err := r.m.Create(bg, api.NewSession{Agent: api.AgentDsh, Cwd: work, Machine: "pc", Preset: "mine"}, "web"); err == nil {
		t.Fatal("a preset not there was taken")
	}
	id, err := r.m.Create(bg, api.NewSession{Agent: api.AgentDsh, Cwd: work, Machine: "pc", Preset: "minimal"}, "web")
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
	// Its models, dsh's there as its probe said them, each "<provider>/<model>".
	if len(s.Models) != 2 || s.Models[0].Value != "deepseek-official/deepseek-flash" || s.Model != "deepseek-official/deepseek-flash" {
		t.Errorf("models %+v, model %q", s.Models, s.Model)
	}
	// The patch went there, the transcript came here.
	if b, err := os.ReadFile(filepath.Join(there, "executor", "scratch", id, "dsh-patch.yml")); err != nil || !strings.Contains(string(b), `"minimal" Agent preset`) {
		t.Errorf("patch there: %v\n%s", err, b)
	}
	if len(r.messages(id)) == 0 {
		t.Error("no messages from the mirrored transcript")
	}
	if _, err := os.Stat(filepath.Join(h.dir, "remote", "session.v4.jsonl.zstd")); err != nil {
		t.Errorf("mirror: %v", err)
	}

	testShell(t, r.m, id, work)
	// An agent's ir sh: at the machine, in its home unless told.
	if st, run, err := r.m.MachineShell(bg, "m1", "echo agent; pwd", work, false); err != nil || run != nil {
		t.Error(err, run)
	} else if out, end := shellAll(t, r.m, st.Run); !strings.Contains(out, "agent\n") || end.Code == nil || *end.Code != 0 {
		t.Errorf("ir sh: %q %+v", out, end)
	}
	// Streamed, through run: no holder.
	if st, run, err := r.m.MachineShell(bg, "m1", "echo agent; exit 4", work, true); err != nil || run == nil || !strings.HasPrefix(st.Run, "run-") {
		t.Errorf("ir sh by run: %+v %v %v", st, run, err)
	} else {
		var out string
		var code *int
		for {
			ev, err := run.Next(bg)
			if err != nil {
				break
			}
			out += ev.Data
			if ev.Exit != nil {
				code = ev.Exit.Code
			}
		}
		if out != "agent\n" || code == nil || *code != 4 {
			t.Errorf("ir sh by run: %q %v", out, code)
		}
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
	t.Parallel()
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
	// Before it is taken up: its machine's models, opened from the page; a model picked takes it
	// up, its agent not started till a message.
	waitFor(t, "its models", func() bool {
		st, _ := r.m.State(id)
		return st != nil && len(st.Models) == 2 && !st.Hosted
	})
	if err := r.m.SetModel(bg, id, "deepseek-official/deepseek-v4-pro"); err != nil {
		t.Fatal(err)
	}
	if st, _ := r.m.State(id); !st.Hosted || st.Live || st.Pick != "deepseek-official/deepseek-v4-pro" {
		t.Errorf("picked: hosted %v live %v pick %q", st.Hosted, st.Live, st.Pick)
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

	// 同步 DSH (a remote group's menu): a session Desktop made since is listed now, not at the next
	// look; the machine's models are probed again, not taken as kept.
	const id2 = "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"
	dir2 := filepath.Join(home, "sessions", transcript.DshProjectDir(work), id2)
	os.MkdirAll(dir2, 0o700)
	fx, _ := os.ReadFile(fixture)
	os.WriteFile(filepath.Join(dir2, "session.v4.jsonl.zstd"), fx, 0o600)
	os.WriteFile(filepath.Join(sums, id2+".json"), []byte(`{"version":7,"record":{"identity":{"cwd":"`+work+`"},"rows":{"title":{"ver":1,"seq":1,"val":"made since"}}}}`), 0o600)
	if _, _, ok := r.m.found(id2); ok {
		t.Fatal("listed before a look")
	}
	ch := r.m.o.Channels[api.AgentDsh].(*hdsh.Channel)
	before := ch.ProbedAt("m1")
	if err := r.m.Sync(bg, api.AgentDsh, "m1"); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := r.m.found(id2); !ok {
		t.Error("synced: the new session not listed")
	}
	after := ch.ProbedAt("m1")
	if !after.After(before) {
		t.Errorf("synced: models not probed again (%v, %v)", before, after)
	}
}

// A Codex session at an executor (docs/exec.md, item 12): started there with the argv its hello
// gave and the executor's CODEX_HOME, its rollout found there by the thread (a UUIDv7's date, or
// a walk of the newest day folders for an id with no time) and mirrored here. There is no fake
// Codex app-server: the agent is a shell that notes how it was started.
func TestRemoteCodex(t *testing.T) {
	t.Parallel()
	gone := make(chan struct{}, 1)
	hub := &remote.Hub{Logf: func(format string, a ...any) {
		t.Logf(format, a...)
		if strings.Contains(format, "gone") {
			select {
			case gone <- struct{}{}:
			default:
			}
		}
	}}
	r := newRig(t, func(_ *config.Config, o *Options) { o.Remote = hub })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		hub.Serve(w, req, "m1", "pc")
	}))
	t.Cleanup(srv.Close)
	there := t.TempDir()
	work := filepath.Join(there, "work")
	home := filepath.Join(there, "codex")
	os.MkdirAll(work, 0o700)
	script := `printf '%s\n' "$@" > argv.txt; printf '%s\n' "$CODEX_HOME" > home.txt; exec cat > /dev/null`
	exe, err := executor.New(executor.Options{
		URL: "ws" + strings.TrimPrefix(srv.URL, "http"), State: filepath.Join(there, "executor"), Env: append(os.Environ(), "CODEX_HOME="+home), Logf: t.Logf,
		Agents: []wire.AgentInfo{{Agent: "codex", Argv: []string{"/bin/sh", "-c", script, "codex"}, Version: "0.165.0"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = exe.Run(ctx); close(done) }()
	t.Cleanup(func() {
		cancel()
		<-done
		exe.Close()
		// Its going told before the session layer closes (it lists the session again).
		select {
		case <-gone:
		case <-time.After(5 * time.Second):
		}
	})
	waitFor(t, "the executor", func() bool { _, ok := hub.Machine("m1").Hello(); return ok })

	id, err := r.m.Create(bg, api.NewSession{Agent: api.AgentCodex, Cwd: work, Machine: "pc"}, "web")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "codex there", func() bool {
		b, _ := os.ReadFile(filepath.Join(work, "home.txt"))
		return len(b) > 0
	})
	if b, _ := os.ReadFile(filepath.Join(work, "argv.txt")); string(b) != "app-server\n--listen\nstdio://\n" {
		t.Errorf("argv there: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(work, "home.txt")); string(b) != home+"\n" {
		t.Errorf("CODEX_HOME there: %q", b)
	}

	h, _ := r.m.get(id)
	if p, err := h.findTranscript(); p != "" || err != nil {
		t.Errorf("before the thread: %q %v", p, err)
	}
	line := func(text string) string {
		return `{"timestamp":"2026-11-08T04:31:00.000Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","id":"u1","content":[{"type":"text","text":"` + text + `"}]}}}` + "\n"
	}
	// The thread Codex made (Started): a UUIDv7 of now, its rollout in today's folder there.
	now := time.Now()
	ms := fmt.Sprintf("%012x", now.UnixMilli())
	thread := ms[:8] + "-" + ms[8:] + "-7abc-8def-0123456789ab"
	day := filepath.Join(home, "sessions", now.Format("2006"), now.Format("01"), now.Format("02"))
	os.MkdirAll(day, 0o700)
	os.WriteFile(filepath.Join(day, "rollout-"+now.Format("2006-01-02T15-04-05")+"-"+thread+".jsonl"), []byte(line("hi there")), 0o600)
	// An older Codex's thread, its id with no time in it.
	const old = "0193a3c2-7d1e-4a51-9b8f-0c4d5e6f7a8b"
	oldDay := filepath.Join(home, "sessions", "2024", "05", "06")
	os.MkdirAll(oldDay, 0o700)
	os.WriteFile(filepath.Join(oldDay, "rollout-2024-05-06T10-00-00-"+old+".jsonl"), []byte(line("long ago")), 0o600)
	for thread, want := range map[string]string{thread: "hi there", old: "long ago"} {
		h.mu.Lock()
		h.meta.AgentID = thread
		h.mu.Unlock()
		p, err := h.findTranscript()
		if err != nil || filepath.Dir(p) != filepath.Join(h.dir, "remote") {
			t.Fatalf("%s: %q %v", thread, p, err)
		}
		if b, _ := os.ReadFile(p); !strings.Contains(string(b), want) {
			t.Errorf("%s: mirrored %q", thread, b)
		}
	}

	if err := r.m.Stop(bg, id); err != nil {
		t.Fatal(err)
	}
	r.waitState(id, "stopped", func(s *api.SessionState) bool { return !s.Live })
}

func presetIDs(l []agent.Preset) string {
	ids := make([]string, len(l))
	for i, p := range l {
		ids[i] = p.ID
	}
	return strings.Join(ids, " ")
}

// At a machine with the dsh plugin, the presets are DSH Desktop's own registry's (dsh.presets): a
// bundle's or the user's too, by its order, the default its settings pick; a broken one is left
// out. A session started there runs the preset's rows as the registry has them.
func TestRemotePresets(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("no node")
	}
	hub := &remote.Hub{Logf: t.Logf}
	r := newRig(t, func(_ *config.Config, o *Options) { o.Remote = hub })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { hub.Serve(w, req, "m1", "pc") }))
	t.Cleanup(srv.Close)
	there := t.TempDir()
	desktop := filepath.Join(there, "desktop")
	os.MkdirAll(desktop, 0o700)
	os.WriteFile(filepath.Join(desktop, "open.json"), []byte("[]"), 0o600)
	presets := `{"default": "mine", "presets": [
		{"id": "standard", "plugins": "- id: persona\n  name: '@deepseek-ai/dsh-persona'\n"},
		{"id": "mine", "name": "Mine", "description": "My own", "plugins": "- id: persona\n  name: '@deepseek-ai/dsh-persona'\n  config:\n    prefix: You are mine.\n- id: tool-web\n  name: '@deepseek-ai/dsh-tool-web'\n"},
		{"id": "gone", "plugins": "", "broken": "missing service"}]}`
	os.WriteFile(filepath.Join(desktop, "presets.json"), []byte(presets), 0o600)
	// Its loader's rows: DSH Desktop's settings of its models (a pi-ai provider, the default).
	entries := `[
		{"id": "llm-pi-ai", "name": "@deepseek-ai/dsh-llm-pi-ai", "disabled": false, "config": {"providers": [{"id": "openrouter", "apiKey": {"__jsExpr": "process.env.OR_KEY"}}]}},
		{"id": "agent-default-model", "name": "@deepseek-ai/dsh-agent-default-model", "config": {"provider": "openrouter", "model": "x/y"}},
		{"id": "llm-mine", "name": "@deepseek-ai/dsh-llm-mine", "config": {}},
		{"id": "tool-web", "name": "@deepseek-ai/dsh-tool-web"}]`
	os.WriteFile(filepath.Join(desktop, "entries.json"), []byte(entries), 0o600)
	// Its profile's bundles: dsh's own, a third party's (OMD), the plugin itself, one not installed.
	profile := filepath.Join(desktop, "profile")
	for f, data := range map[string]string{
		"package.json":                                    `{"dsh": {"profile": {"bundles": ["@deepseek-ai/dsh-base", "oh-my-dsh", "dsh-remote-iridium", "gone"]}}}`,
		"node_modules/oh-my-dsh/package.json":             `{"name": "oh-my-dsh", "version": "1.3.9", "dsh": {"bundle": {"patch": ["./cordis.patch.yml", "./presets/cordis.patch.yml"]}}}`,
		"node_modules/oh-my-dsh/cordis.patch.yml":         "- insert:\n    - id: oh-my-dsh\n      name: oh-my-dsh\n",
		"node_modules/oh-my-dsh/presets/cordis.patch.yml": "[]\n",
		"node_modules/dsh-remote-iridium/package.json":    `{"name": "dsh-remote-iridium", "dsh": {"bundle": {"patch": "./cordis.patch.yml"}}}`,
	} {
		os.MkdirAll(filepath.Dir(filepath.Join(profile, f)), 0o700)
		os.WriteFile(filepath.Join(profile, f), []byte(data), 0o600)
	}
	scope, _ := filepath.Abs("../agent/dsh/testdata/pkg/node_modules/@deepseek-ai")
	agents, _ := json.Marshal([]wire.AgentInfo{{Agent: "dsh", Argv: []string{"dsh"}, Version: "0.2.2-rc.1", Scope: scope, Home: filepath.Join(there, ".dsh")}})
	cmd := exec.Command("node", "../../plugins/dsh-remote-iridium/test/run.mjs", "ws"+strings.TrimPrefix(srv.URL, "http"), "tok", filepath.Join(there, "executor"))
	cmd.Env = append(os.Environ(), "IRIDIUM_TEST_AGENTS="+string(agents), "IRIDIUM_TEST_DSH="+desktop)
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Signal(syscall.SIGTERM); _ = cmd.Wait() })
	waitFor(t, "the executor", func() bool { _, ok := hub.Machine("m1").Hello(); return ok })

	// What DSH Desktop composes for a launch there (agent.prepare): its models, then its own
	// part in the process; a preset of its own, here (preset false). The executor serves none.
	launch := agent.SpawnOptions{Session: "x", Cwd: "/w", Remote: &agent.Remote{}}
	if err := r.m.o.Channels[api.AgentDsh].(harness.Launcher).Prepare(bg, harness.Ref{ID: "x", As: "x", Agent: api.AgentDsh, Machine: "m1"}, &launch); err != nil {
		t.Fatal(err)
	}
	if launch.Host == nil {
		t.Fatal("no host")
	}
	patches, in, err := launch.Host.Prepare("standard", "Iridium's prompt")
	if err != nil || in || len(patches) != 2 || patches[0] != filepath.Join(desktop, "llm.patch.yml") {
		t.Fatalf("agent.prepare: %q %v %v", patches, in, err)
	}
	b, _ := os.ReadFile(patches[0])
	for _, line := range []string{
		"- id: llm-pi-ai\n  disabled: false\n  config: {\"providers\": [{\"id\": \"openrouter\", \"apiKey\": !!js \"process.env.OR_KEY\"}]}\n",
		"- id: agent-default-model\n  config: {\"provider\": \"openrouter\", \"model\": \"x/y\"}\n",
	} {
		if !strings.Contains(string(b), line) {
			t.Errorf("llm patch, no %q:\n%s", line, b)
		}
	}
	if strings.Contains(string(b), "llm-mine") || strings.Contains(string(b), "tool-web") {
		t.Errorf("llm patch: rows not the base's llm ones:\n%s", b)
	}
	if _, err := hub.Machine("m1").ReadFile(bg, patches[0], 1<<20); err == nil {
		t.Error("the llm patch was served")
	}

	l, def, err := r.m.Presets(bg, api.AgentDsh, "m1")
	if err != nil || def != "mine" || presetIDs(l) != "standard mine" || l[1].Name != "Mine" || l[1].Description != "My own" {
		t.Fatalf("presets: %+v %q %v", l, def, err)
	}
	if _, err := r.m.checkPreset(bg, api.AgentDsh, "m1", "gone"); !errors.Is(err, ErrBadRequest) {
		t.Errorf("a broken preset: %v", err)
	}
	// What the launch makes of it: the registry's rows, persona folded in.
	mine, _ := dsh.FindPreset(l, "mine")
	pkg := dsh.Package{Scope: scope}
	b, err = pkg.Patch(mine, "", "")
	if err != nil || !strings.Contains(string(b), "    personaPrefix: You are mine.\n") || !strings.Contains(string(b), "    - id: iridium-tool-web\n") {
		t.Errorf("patch: %v\n%s", err, b)
	}
}
