package session

import (
	"testing"

	"github.com/kumoSleeping/iridium/internal/api"
)

// A command never steers into a running turn: it waits for the turn's end.
func TestCommandWaitsForTurn(t *testing.T) {
	r := newRig(t, nil)
	id, _ := r.m.Create(bg, api.NewSession{Cwd: r.cwd, Text: "one WAIT"}, "网页")
	r.waitState(id, "the turn", func(s *api.SessionState) bool { return s.Status == api.StatusRunning })
	if _, queued, err := r.m.SendWith(bg, id, " /compact ", "网页", true); err != nil || !queued {
		t.Fatalf("a command steered in: queued %v, %v", queued, err)
	}
	s, _ := r.m.State(id)
	if len(s.Queue) != 1 || s.Queue[0].Text != "/compact" {
		t.Fatalf("queue: %+v", s.Queue)
	}
	r.waitState(id, "the command's turn", func(s *api.SessionState) bool { return said(s, "ok: /compact") && len(s.Queue) == 0 })
}

// /clear goes on in a new session in the same place with the same settings; the old one, idle,
// is archived. One that said nothing yet is clear already.
func TestClear(t *testing.T) {
	r := newRig(t, nil)
	ws, err := r.store.CreateWorkspace("w", r.cwd)
	if err != nil {
		t.Fatal(err)
	}
	fresh, _ := r.m.Create(bg, api.NewSession{Cwd: r.cwd, Workspace: ws}, "网页")
	if id, err := r.m.Clear(bg, fresh, "网页"); err != nil || id != fresh {
		t.Fatalf("a fresh session cleared to %q: %v", id, err)
	}

	old, _ := r.m.Create(bg, api.NewSession{Cwd: r.cwd, Workspace: ws, Text: "hi", Mode: "plan"}, "网页")
	r.waitState(old, "the reply", func(s *api.SessionState) bool { return said(s, "ok: hi") && s.Status == api.StatusIdle })
	if err := r.m.SetEffort(bg, old, "high"); err != nil {
		t.Fatal(err)
	}
	id, err := r.m.Clear(bg, old, "网页")
	if err != nil || id == "" || id == old {
		t.Fatalf("cleared to %q: %v", id, err)
	}
	s := r.waitState(id, "the new session", func(s *api.SessionState) bool { return s.Live && s.Status == api.StatusIdle })
	if s.Cwd != r.cwd || s.Agent != api.AgentClaude || s.Effort != "high" || s.Mode != "plan" || s.Archived {
		t.Errorf("new session: cwd %q agent %q effort %q mode %q archived %v", s.Cwd, s.Agent, s.Effort, s.Mode, s.Archived)
	}
	if over, _ := r.store.Overlays(); over[id].Workspace != ws {
		t.Errorf("workspace %d, not %d", over[id].Workspace, ws)
	}
	if len(r.messages(id)) != 0 {
		t.Errorf("the new session has words: %+v", r.messages(id))
	}
	r.waitState(old, "the old one archived", func(s *api.SessionState) bool { return s.Archived && !s.Live })
	// Its words are still on record.
	if b := r.messages(old); len(b) != 2 {
		t.Errorf("old blocks: %+v", b)
	}
}
