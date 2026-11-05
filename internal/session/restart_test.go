package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/hold"
)

// A mode an agent takes only at launch (pi): the agent restarts with it, at once when idle or at
// the end of the running turn, and what waited in the queue goes after.
func TestRestartForMode(t *testing.T) {
	r := newRig(t, nil)
	id, _ := r.m.Create(bg, api.NewSession{Cwd: r.cwd, Text: "one WAIT"}, "网页")
	r.waitState(id, "the turn", func(s *api.SessionState) bool { return s.Status == api.StatusRunning })
	h, _ := r.m.get(id)
	h.mu.Lock()
	h.meta.Mode = "plan"
	h.mu.Unlock()
	h.restartForMode() // mid-turn: at its end
	if _, queued, _ := r.m.SendWith(bg, id, "two", "网页", false); !queued {
		t.Fatal("not queued")
	}
	s := r.waitState(id, "the restart and the queue", func(s *api.SessionState) bool { return s.Proc == 2 && said(s, "ok: two") })
	if len(s.Queue) != 0 || s.Mode != "plan" || s.Error != "" {
		t.Errorf("state: %+v", s)
	}
	spec, _ := os.ReadFile(filepath.Join(r.cfg.StatePath(), "hold", id, hold.FileSpec))
	if !strings.Contains(string(spec), `"--permission-mode","plan"`) {
		t.Errorf("spec: %s", spec)
	}
	// Idle: at once.
	h.mu.Lock()
	h.meta.Mode = "acceptEdits"
	h.mu.Unlock()
	h.restartForMode()
	r.waitState(id, "the second restart", func(s *api.SessionState) bool { return s.Proc == 3 && s.Live && s.Mode == "acceptEdits" })
}
