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
