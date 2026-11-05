package session

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/config"
	"github.com/kumoSleeping/iridium/internal/discover"
	"github.com/kumoSleeping/iridium/internal/transcript"
)

// Codex's and pi's sessions on disk: a hosted Codex session is listed once, under Iridium's id,
// though discovery finds it under its thread; one found on disk is taken up to resume, Codex's
// with its thread as its agent id.
func TestTakeCodexAndPi(t *testing.T) {
	codexHome, piRoot := t.TempDir(), t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	t.Setenv("PI_CODING_AGENT_DIR", piRoot)
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", "")
	var d *discover.Index
	r := newRig(t, func(c *config.Config, o *Options) {
		d = discover.New(discover.Options{ClaudeDir: c.ClaudeHome, CodexHome: codexHome, PiSessions: filepath.Join(piRoot, "sessions"), DshHome: t.TempDir(), Store: o.Store, Procs: &procs{}, UID: os.Getuid(), Logf: t.Logf})
		o.Discovery = d
	})
	d.SetAlias(r.m.AgentID)
	ctx := context.Background()
	rollout := func(thread string) {
		day := filepath.Join(codexHome, "sessions", "2026", "01", "01")
		os.MkdirAll(day, 0o700)
		os.WriteFile(filepath.Join(day, "rollout-2026-01-01T00-00-00-"+thread+".jsonl"), []byte(
			`{"timestamp":"2026-01-01T00:00:00Z","type":"session_meta","payload":{"id":"`+thread+`","cwd":"`+r.cwd+`","source":"vscode"}}`+"\n"+
				`{"timestamp":"2026-01-01T00:00:01Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","id":"u","content":[{"type":"text","text":"hi codex"}]}}}`+"\n"+
				`{"timestamp":"2026-01-01T00:00:02Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"AgentMessage","id":"a","content":[{"type":"Text","text":"hello"}],"phase":"final_answer"}}}`+"\n"), 0o600)
	}

	// Hosted, its thread made: discovery's row of the thread is the hosted session's.
	const hosted, thread = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "01a1ab66-6df6-7fc2-a494-bfa5329eadb4"
	rollout(thread)
	h := r.m.newHosted(meta{ID: hosted, Agent: api.AgentCodex, Cwd: r.cwd, Created: time.Now().UnixMilli(), AgentID: thread})
	r.m.mu.Lock()
	r.m.hosted[hosted] = h
	r.m.mu.Unlock()
	d.Scan()
	l, _ := r.m.List(ctx)
	if len(l) != 1 || l[0].ID != hosted || !l[0].Hosted {
		t.Fatalf("list: %+v", l)
	}
	if r.m.AgentID(hosted) != thread {
		t.Errorf("agent id %q", r.m.AgentID(hosted))
	}
	if got, err := r.m.take(ctx, thread); err != nil || got != h {
		t.Errorf("take by its thread: %v %v", got, err)
	}
	if b := r.messages(hosted); len(b) != 2 || b[1].Text != "hello" {
		t.Errorf("messages: %+v", b)
	}

	// Found on disk, no process: taken up, the thread its agent id.
	const other = "01a1ab66-0000-7000-8000-000000000001"
	rollout(other)
	d.Scan()
	got, err := r.m.take(ctx, other)
	if err != nil {
		t.Fatal(err)
	}
	if got.meta.Agent != api.AgentCodex || got.meta.AgentID != other || got.meta.agentID() != other {
		t.Errorf("taken codex: %+v", got.meta)
	}
	if p, _ := got.findTranscript(); filepath.Base(p) != "rollout-2026-01-01T00-00-00-"+other+".jsonl" {
		t.Errorf("transcript %q", p)
	}

	// pi: its session id is the hosted one's.
	const piID = "00000000-0000-4000-8000-4c4a2a950dc8"
	piDir := transcript.PiSessions(os.Environ(), r.cwd)
	os.MkdirAll(piDir, 0o700)
	os.WriteFile(filepath.Join(piDir, "2026-01-01T00-00-00-386Z_"+piID+".jsonl"), []byte(
		`{"type":"session","version":3,"id":"`+piID+`","timestamp":"2026-01-01T00:00:00.386Z","cwd":"`+r.cwd+`"}`+"\n"+
			`{"type":"message","id":"1","parentId":null,"timestamp":"2026-01-01T00:00:01Z","message":{"role":"user","content":[{"type":"text","text":"hi pi"}]}}`+"\n"), 0o600)
	d.Scan()
	got, err = r.m.take(ctx, piID)
	if err != nil {
		t.Fatal(err)
	}
	if got.meta.Agent != api.AgentPi || got.meta.agentID() != piID {
		t.Errorf("taken pi: %+v", got.meta)
	}
	if p, _ := got.findTranscript(); filepath.Base(p) != "2026-01-01T00-00-00-386Z_"+piID+".jsonl" {
		t.Errorf("pi transcript %q", p)
	}
	if l, _ := r.m.List(ctx); len(l) != 3 {
		t.Errorf("list: %+v", l)
	}
}
