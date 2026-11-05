package session

import (
	"context"
	"testing"

	"github.com/kumoSleeping/iridium/internal/api"
)

// A hosted session is shown from the start. Archived, its agent stops as idle reaping does, and
// what it writes ending does not bring it back; a message for it does.
func TestArchiveHosted(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	id, err := r.m.Create(ctx, api.NewSession{Cwd: r.cwd, Text: "hello"}, "网页")
	if err != nil {
		t.Fatal(err)
	}
	r.waitState(id, "the reply", func(s *api.SessionState) bool {
		return s.Last != nil && s.Last.Text == "ok: hello" && s.Status == api.StatusIdle
	})
	listed := func() api.Session {
		t.Helper()
		l, err := r.m.List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range l {
			if s.ID == id {
				return s
			}
		}
		t.Fatalf("%s not listed", id)
		return api.Session{}
	}
	if s := listed(); s.Archived || !s.Live {
		t.Fatalf("new: %+v", s)
	}
	if err := r.m.SetArchived(ctx, id, true); err != nil {
		t.Fatal(err)
	}
	s := r.waitState(id, "its agent stopped", func(s *api.SessionState) bool { return !s.Live })
	if !s.Archived || !listed().Archived || s.Status == api.StatusError {
		t.Fatalf("archived: %+v", s)
	}
	row, ok, _ := r.store.ShownRow(string(api.AgentClaude), id)
	if !ok || row.Shown || row.Size == 0 || row.QuietUntil != 0 {
		t.Fatalf("its marker once it ended: %+v %v", row, ok)
	}
	// A message brings it back and starts it again.
	if _, err := r.m.Send(ctx, id, "again", "网页"); err != nil {
		t.Fatal(err)
	}
	r.waitState(id, "back with its reply", func(s *api.SessionState) bool { return !s.Archived && s.Last != nil && s.Last.Text == "ok: again" })
	// Restored by hand.
	r.m.SetArchived(ctx, id, true)
	r.waitState(id, "stopped again", func(s *api.SessionState) bool { return !s.Live && s.Archived })
	if err := r.m.SetArchived(ctx, id, false); err != nil {
		t.Fatal(err)
	}
	if listed().Archived {
		t.Fatal("restored, still archived")
	}
}
