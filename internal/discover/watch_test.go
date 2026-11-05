package discover

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/store"
)

// Watching begins with the first look even when it finds nothing: a transcript written after
// it is activity, though the look that finds it comes later.
func TestWatchingBeginsAtTheFirstLook(t *testing.T) {
	home := t.TempDir()
	claude := filepath.Join(home, ".claude")
	os.MkdirAll(filepath.Join(claude, "projects"), 0o700)
	st, err := store.Open(filepath.Join(t.TempDir(), "iridium.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	x := New(Options{ClaudeDir: claude, CodexHome: filepath.Join(home, ".codex"), PiSessions: filepath.Join(home, ".pi"), DshHome: filepath.Join(home, ".dsh"),
		Store: st, Procs: &fakeProcs{}, UID: me, Logf: t.Logf, Settle: 10 * time.Millisecond, ProcScan: time.Hour, Rescan: time.Hour})
	x.Scan()
	time.Sleep(20 * time.Millisecond)
	dir := filepath.Join(claude, "projects", "-w-new")
	os.MkdirAll(dir, 0o700)
	line := `{"type":"user","uuid":"u1","timestamp":"2026-01-01T00:00:00Z","cwd":"/w/new","message":{"role":"user","content":"hello"}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, idNew+".jsonl"), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	x.Scan()
	v, err := st.Visibility()
	if err != nil {
		t.Fatal(err)
	}
	if !v.Shown(string(api.AgentClaude), idNew) {
		t.Error("a transcript written after the first look is not shown")
	}
}

const idNew = "99999999-9999-4999-8999-999999999999"
