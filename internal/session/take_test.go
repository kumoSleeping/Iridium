package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/config"
	"github.com/kumoSleeping/iridium/internal/discover"
)

// procs is a /proc of one fake claude process, there or not.
type procs struct {
	mu  sync.Mutex
	pid int // 0: none
}

func (p *procs) set(pid int) { p.mu.Lock(); p.pid = pid; p.mu.Unlock() }

func (p *procs) PIDs() ([]int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.pid == 0 {
		return nil, nil
	}
	return []int{p.pid}, nil
}

func (p *procs) Stat(pid int) (discover.Stat, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if pid != p.pid || pid == 0 {
		return discover.Stat{}, fs.ErrNotExist
	}
	return discover.Stat{PPid: 1, Start: 77, UID: os.Getuid()}, nil
}

func (p *procs) Cmdline(int) []string { return []string{"claude"} }
func (p *procs) Exe(int) string       { return "" }
func (p *procs) Cwd(int) string       { return "" }
func (p *procs) Environ(int) []string { return nil }
func (p *procs) Files(int) []string   { return nil }

func TestResumeAfterItExitsElsewhere(t *testing.T) {
	var d *discover.Index
	fake := &procs{}
	var logMu sync.Mutex
	var logs []string
	r := newRig(t, func(c *config.Config, o *Options) {
		do := discover.Options{ClaudeDir: c.ClaudeHome, CodexHome: t.TempDir(), PiSessions: t.TempDir(), DshHome: t.TempDir(), Store: o.Store, Procs: fake, UID: os.Getuid(), Logf: t.Logf}
		d = discover.New(do)
		o.Discovery = d
		o.Logf = func(f string, a ...any) {
			logMu.Lock()
			logs = append(logs, fmt.Sprintf(f, a...))
			logMu.Unlock()
			t.Logf(f, a...)
		}
	})
	ctx := context.Background()
	id := "88888888-8888-4888-8888-888888888888"
	// A session started in a terminal: its transcript, and its process in Claude's registry.
	dir := filepath.Join(r.cfg.ClaudeHome, "projects", strings.ReplaceAll(r.cwd, "/", "-"))
	os.MkdirAll(dir, 0o700)
	var lines []byte
	for _, l := range []map[string]any{
		{"type": "user", "uuid": "00000000-0000-4000-8000-0000000000f1", "timestamp": "2026-01-01T00:00:00Z", "cwd": r.cwd, "sessionId": id, "message": map[string]any{"role": "user", "content": "from the terminal"}},
		{"type": "assistant", "timestamp": "2026-01-01T00:00:01Z", "cwd": r.cwd, "sessionId": id, "message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "done there"}}}},
	} {
		b, _ := json.Marshal(l)
		lines = append(append(lines, b...), '\n')
	}
	os.WriteFile(filepath.Join(dir, id+".jsonl"), lines, 0o600)
	reg := filepath.Join(r.cfg.ClaudeHome, "sessions", "4242.json")
	os.MkdirAll(filepath.Dir(reg), 0o700)
	os.WriteFile(reg, []byte(`{"pid":4242,"sessionId":"`+id+`","cwd":"`+r.cwd+`","procStart":"77"}`), 0o600)
	fake.set(4242)
	d.Scan()

	l, _ := r.m.List(ctx)
	if len(l) != 1 || l[0].ID != id || l[0].Hosted || l[0].Status != api.StatusElsewhere || l[0].Title != "from the terminal" {
		t.Fatalf("list: %+v", l)
	}
	// Read-only while it runs there: its state and messages, no sending.
	st, err := r.m.State(id)
	if err != nil || st.Status != api.StatusElsewhere || st.End == 0 {
		t.Fatalf("state: %+v %v", st, err)
	}
	if b := r.messages(id); len(b) != 2 || b[1].Text != "done there" {
		t.Fatalf("messages: %+v", b)
	}
	if _, err := r.m.Send(ctx, id, "hello", "网页"); !errors.Is(err, ErrElsewhere) {
		t.Fatalf("send while it runs elsewhere: %v", err)
	}

	// It exited there: the next message resumes it, hosted.
	os.Remove(reg)
	fake.set(0)
	if _, err := r.m.Send(ctx, id, "hello", "网页"); err != nil {
		t.Fatal(err)
	}
	s := r.waitState(id, "the reply", func(s *api.SessionState) bool {
		return s.Last != nil && s.Last.Text == "ok: hello" && s.Status == api.StatusIdle
	})
	if !s.Hosted || s.Cwd != r.cwd {
		t.Errorf("state: %+v", s)
	}
	if b := r.messages(id); len(b) != 4 || b[0].Text != "from the terminal" || b[3].Text != "ok: hello" {
		t.Errorf("messages: %+v", b)
	}
	logMu.Lock()
	resumed := strings.Contains(strings.Join(logs, "\n"), "started (resumed")
	logMu.Unlock()
	if !resumed {
		t.Errorf("not started with --resume: %q", logs)
	}

	// Hosted, its agent stopped, and opened in a terminal again: shown elsewhere, not started.
	if err := r.m.Stop(ctx, id); err != nil {
		t.Fatal(err)
	}
	r.waitState(id, "the stop", func(s *api.SessionState) bool { return !s.Live })
	os.WriteFile(reg, []byte(`{"pid":4242,"sessionId":"`+id+`","cwd":"`+r.cwd+`","procStart":"77"}`), 0o600)
	fake.set(4242)
	d.Scan()
	l, _ = r.m.List(ctx)
	if len(l) != 1 || !l[0].Hosted || l[0].Status != api.StatusElsewhere {
		t.Errorf("list: %+v", l)
	}
	if _, err := r.m.Send(ctx, id, "again", "网页"); !errors.Is(err, ErrElsewhere) {
		t.Errorf("send to a hosted one running elsewhere: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
}

func TestCreateInWorkspace(t *testing.T) {
	r := newRig(t, nil)
	ws, err := r.store.CreateWorkspace("w", r.cwd)
	if err != nil {
		t.Fatal(err)
	}
	id, err := r.m.Create(context.Background(), api.NewSession{Agent: api.AgentClaude, Cwd: r.cwd, Workspace: ws}, "网页")
	if err != nil {
		t.Fatal(err)
	}
	if s, _ := r.m.State(id); s.Workspace != ws {
		t.Errorf("workspace: %+v", s)
	}
	if _, err := r.m.Create(context.Background(), api.NewSession{Agent: api.AgentClaude, Cwd: r.cwd, Workspace: 999}, "网页"); err == nil {
		t.Errorf("created in a workspace that is not there")
	}
}
