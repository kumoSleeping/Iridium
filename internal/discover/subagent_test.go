package discover

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A subagent its parent starts while Iridium watches is listed at once, though Claude makes
// <id>/subagents/ in one go, and goes in its parent's workspace wherever it works.
func TestSubagentWatchedIntoItsParentsWorkspace(t *testing.T) {
	r := newRig(t)
	if err := r.x.Start(); err != nil {
		t.Fatal(err)
	}
	defer r.x.Close()
	wait := func(what string, ok func() bool) {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			if ok() {
				return
			}
		}
		t.Fatalf("timed out: %s", what)
	}
	ws, err := r.x.CreateWorkspace("/w/c", "")
	if err != nil {
		t.Fatal(err)
	}
	r.grow(idC)
	wait("C in its workspace", func() bool { a, _ := r.assignment(idC); return a.Workspace == ws.ID })

	sub := filepath.Join(r.home, ".claude", "projects", "-w-c", idC, "subagents")
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	line := `{"parentUuid":null,"isSidechain":true,"type":"user","message":{"role":"user","content":"count the files"},"cwd":"/elsewhere","sessionId":"` + idC + `","agentId":"a1"}` + "\n"
	if err := os.WriteFile(filepath.Join(sub, "agent-a1.jsonl"), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	wait("the subagent listed", func() bool {
		s := r.sessions()["agent-a1"]
		return s.Subagent && s.Parent == idC && s.Title == "count the files"
	})
	wait("it in C's workspace", func() bool { a, _ := r.assignment("agent-a1"); return a.Workspace == ws.ID })
}

// A Workflow run's agents, in <id>/subagents/workflows/<run id>/, are listed as they start:
// whether Claude makes those directories in one go or one at a time, and the run's journal is
// no session.
func TestWorkflowAgentsWatched(t *testing.T) {
	r := newRig(t)
	if err := r.x.Start(); err != nil {
		t.Fatal(err)
	}
	defer r.x.Close()
	wait := func(what string, ok func() bool) {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			if ok() {
				return
			}
		}
		t.Fatalf("timed out: %s", what)
	}
	r.grow(idC)
	wait("C listed", func() bool { _, ok := r.sessions()[idC]; return ok })

	line := func(agent, text string) []byte {
		return []byte(`{"parentUuid":null,"isSidechain":true,"type":"user","message":{"role":"user","content":"` + text + `"},"cwd":"/w/c","sessionId":"` + idC + `","agentId":"` + agent + `"}` + "\n")
	}
	write := func(p string, b []byte) {
		t.Helper()
		if err := os.WriteFile(p, b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	mkdir := func(p string) {
		t.Helper()
		if err := os.MkdirAll(p, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	wf := filepath.Join(r.home, ".claude", "projects", "-w-c", idC, "subagents", "workflows")

	// In one go.
	run1 := filepath.Join(wf, "wf_11111111-aaa")
	mkdir(run1)
	write(filepath.Join(run1, "journal.jsonl"), []byte(`{"type":"launched"}`+"\n"))
	write(filepath.Join(run1, "agent-w1.jsonl"), line("w1", "first step"))
	wait("the first run's agent", func() bool {
		s := r.sessions()["agent-w1"]
		return s.Subagent && s.Parent == idC && s.Title == "first step"
	})

	// A second run, its directory made a moment before its agents write.
	run2 := filepath.Join(wf, "wf_22222222-bbb")
	mkdir(run2)
	time.Sleep(300 * time.Millisecond)
	write(filepath.Join(run2, "agent-w2.jsonl"), line("w2", "second step"))
	wait("the second run's agent", func() bool {
		s := r.sessions()["agent-w2"]
		return s.Subagent && s.Parent == idC
	})
	if _, ok := r.sessions()["journal"]; ok {
		t.Error("a run's journal listed as a session")
	}

	// Gone with its file.
	if err := os.Remove(filepath.Join(run2, "agent-w2.jsonl")); err != nil {
		t.Fatal(err)
	}
	wait("the second run's agent gone", func() bool { _, ok := r.sessions()["agent-w2"]; return !ok })
}

// With subagents/workflows/ made empty first (no run yet), a run made in it later is still seen.
func TestWorkflowDirMadeEmptyFirst(t *testing.T) {
	r := newRig(t)
	if err := r.x.Start(); err != nil {
		t.Fatal(err)
	}
	defer r.x.Close()
	r.grow(idC)
	sub := filepath.Join(r.home, ".claude", "projects", "-w-c", idC, "subagents")
	if err := os.MkdirAll(filepath.Join(sub, "workflows"), 0o700); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	run := filepath.Join(sub, "workflows", "wf_33333333-ccc")
	if err := os.Mkdir(run, 0o700); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	b := []byte(`{"parentUuid":null,"isSidechain":true,"type":"user","message":{"role":"user","content":"late step"},"cwd":"/w/c","sessionId":"` + idC + `","agentId":"w3"}` + "\n")
	if err := os.WriteFile(filepath.Join(run, "agent-w3.jsonl"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if s := r.sessions()["agent-w3"]; s.Subagent && s.Parent == idC {
			return
		}
	}
	t.Fatal("timed out: the late run's agent")
}
