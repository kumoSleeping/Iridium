package session

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/agent"
	"github.com/kumoSleeping/iridium/internal/api"
)

// A release ends with the turn under way at it, or with a turn begun after it; the mode goes
// back only if it is still the release's.
func TestReleaseOver(t *testing.T) {
	t.Parallel()
	at := time.Now()
	h := &Hosted{mode: releaseMode, turnAt: at.Add(-time.Minute)}
	h.rel = &released{prev: "auto", at: at}
	if m := h.releaseOverLocked(); m != "" || h.rel == nil {
		t.Errorf("a turn before it, none under way: %q", m)
	}
	h.rel.during = true
	if m := h.releaseOverLocked(); m != "auto" || h.rel != nil {
		t.Errorf("the turn under way at it: %q", m)
	}
	h.rel, h.turnAt = &released{prev: "auto", at: at}, at.Add(time.Second)
	if m := h.releaseOverLocked(); m != "auto" {
		t.Errorf("a turn after it: %q", m)
	}
	h.rel, h.mode = &released{prev: "auto", at: at, during: true}, "plan"
	if m := h.releaseOverLocked(); m != "" || h.rel != nil {
		t.Errorf("mode changed meanwhile: %q", m)
	}
}

// A release is kept on disk: the process taken up in manual mode goes on with it, its steps
// as they were left; a new process, or one back in its own mode, ends it.
func TestReleaseTakenUp(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	h := &Hosted{dir: dir, proc: 3, mode: releaseMode}
	h.setRelLocked(&released{prev: "auto", at: time.Now(), left: 2})

	m := &Manager{o: Options{Logf: t.Logf}}
	h2 := &Hosted{m: m, dir: dir, turn: agent.Idle}
	h2.helloReleaseLocked(3, "default")
	if h2.rel == nil || h2.rel.prev != "auto" || h2.mode != releaseMode || h2.rel.during || h2.rel.left != 2 {
		t.Fatalf("taken up: %+v %s", h2.rel, h2.mode)
	}
	h2.turnAt = time.Now().Add(time.Second)
	if mode := h2.releaseOverLocked(); mode != "auto" {
		t.Fatalf("taken up, then a turn: %q", mode)
	}
	if _, err := os.Stat(filepath.Join(dir, fileRelease)); !os.IsNotExist(err) {
		t.Fatalf("over, still on disk: %v", err)
	}

	for _, c := range []struct {
		proc int
		mode string
	}{{4, "default"}, {3, "auto"}} {
		h.setRelLocked(&released{prev: "auto", at: time.Now()})
		h3 := &Hosted{m: m, dir: dir, mode: c.mode}
		h3.helloReleaseLocked(c.proc, c.mode)
		if h3.rel != nil || h3.mode != c.mode {
			t.Errorf("process %d in %s: %+v %s", c.proc, c.mode, h3.rel, h3.mode)
		}
		if _, err := os.Stat(filepath.Join(dir, fileRelease)); !os.IsNotExist(err) {
			t.Errorf("process %d in %s: still on disk", c.proc, c.mode)
		}
	}
}

// A step auto mode denied is a failed call, nothing more: no card, the session idle after.
func TestDeniedIsFailed(t *testing.T) {
	t.Parallel()
	r := newRig(t, nil)
	id, err := r.m.Create(bg, api.NewSession{Cwd: r.cwd, Text: "go DENIED", Mode: "auto"}, "网页")
	if err != nil {
		t.Fatal(err)
	}
	r.waitState(id, "the turn's end", func(s *api.SessionState) bool { return s.Status == api.StatusIdle && s.Last != nil })
	var call *api.Block
	for _, b := range r.messages(id) {
		if b.ID == "toolu_denied" && b.Kind == api.BlockKindTool {
			call = &b
		}
	}
	if call == nil || call.Result == nil || !call.Result.Error {
		t.Fatalf("the denied call: %+v", call)
	}
}

// askManual opens a manual ask in a turn under way (HOLD): the session, the ask's state.
func askManual(t *testing.T, r *rig, steps int) (string, api.ManualState) {
	t.Helper()
	id, err := r.m.Create(bg, api.NewSession{Cwd: r.cwd, Text: "go HOLD", Mode: "auto"}, "网页")
	if err != nil {
		t.Fatal(err)
	}
	r.waitState(id, "the turn", func(s *api.SessionState) bool { return s.Status == api.StatusRunning && s.Mode == "auto" })
	st, err := r.m.AskManual(bg, id, "  npx is needed\n to build ", steps)
	if err != nil || st.State != "pending" || st.RequestID == "" || st.Until == 0 {
		t.Fatalf("asked: %+v %v", st, err)
	}
	s := r.waitState(id, "the ask", func(s *api.SessionState) bool { return len(s.Asks) == 1 })
	a := s.Asks[0]
	if a.Kind != AskManual || a.RequestID != st.RequestID || a.Description != "npx is needed to build" || s.Status != api.StatusWaiting {
		t.Fatalf("the ask: %+v %s", a, s.Status)
	}
	if c := CardOf(id, a); c.Title != "切换至手动审批" || len(c.Options) != 2 || c.Options[0].ID != OptionAllow || c.Options[1].ID != OptionDeny {
		t.Fatalf("its card: %+v", c)
	}
	// One at a time: asked again, the same.
	if again, err := r.m.AskManual(bg, id, "again", 2); err != nil || again.RequestID != st.RequestID {
		t.Fatalf("asked again: %+v %v", again, err)
	}
	return id, st
}

// Approved: manual mode at once, the ask gone, the wait over; not after the first step asked but
// through the turn: its end brings auto mode back.
func TestManualApproved(t *testing.T) {
	t.Parallel()
	r := newRig(t, nil)
	id, st := askManual(t, r, 0)
	if st.Steps != manualDefault {
		t.Errorf("steps %d", st.Steps)
	}
	got := make(chan api.ManualState, 1)
	go func() {
		s, _ := r.m.ManualState(bg, id, 10*time.Second)
		got <- s
	}()
	card := AskCardID(id, st.RequestID)
	if err := r.m.AnswerCard(bg, api.CardAnswer{Card: card, Option: OptionAlways}); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("always: %v", err)
	}
	if err := r.m.AnswerCard(bg, api.CardAnswer{Card: card, Option: OptionAllow}); err != nil {
		t.Fatal(err)
	}
	select {
	case s := <-got:
		if s.State != "approved" || s.Until != 0 {
			t.Fatalf("waited: %+v", s)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the wait did not end")
	}
	s := r.waitState(id, "manual", func(s *api.SessionState) bool { return s.Mode == releaseMode && len(s.Asks) == 0 })
	if s.Status != api.StatusRunning {
		t.Errorf("approved: %s", s.Status)
	}
	if _, err := os.Stat(filepath.Join(r.m.holdRoot(), id, fileRelease)); err != nil {
		t.Errorf("not on disk: %v", err)
	}
	if err := r.m.AnswerCard(bg, api.CardAnswer{Card: card, Option: OptionAllow}); !errors.Is(err, ErrGone) {
		t.Errorf("answered twice: %v", err)
	}
	if _, _, err := r.m.SendWith(bg, id, "go on", "网页", true); err != nil {
		t.Fatal(err)
	}
	r.waitState(id, "auto again", func(s *api.SessionState) bool { return s.Status == api.StatusIdle && s.Mode == "auto" })
	if _, err := os.Stat(filepath.Join(r.m.holdRoot(), id, fileRelease)); !os.IsNotExist(err) {
		t.Errorf("over, still on disk: %v", err)
	}
}

// Refused: nothing changes.
func TestManualRefused(t *testing.T) {
	t.Parallel()
	r := newRig(t, nil)
	id, st := askManual(t, r, 3)
	if err := r.m.AnswerCard(bg, api.CardAnswer{Card: AskCardID(id, st.RequestID), Option: OptionDeny}); err != nil {
		t.Fatal(err)
	}
	s := r.waitState(id, "refused", func(s *api.SessionState) bool { return len(s.Asks) == 0 })
	if s.Mode != "auto" || s.Status != api.StatusRunning {
		t.Errorf("refused: %s %s", s.Mode, s.Status)
	}
	if ms, err := r.m.ManualState(bg, id, 0); err != nil || ms.State != "refused" || ms.Until != 0 {
		t.Errorf("state: %+v %v", ms, err)
	}
}

// Only Claude asks; a reason and steps in range; not in manual mode already.
func TestManualAskRefused(t *testing.T) {
	t.Parallel()
	r := newRig(t, nil)
	if id, err := r.m.Create(bg, api.NewSession{Agent: api.AgentCodex, Cwd: r.cwd}, "网页"); err == nil {
		if _, err := r.m.AskManual(bg, id, "x", 0); !errors.Is(err, ErrBadRequest) {
			t.Errorf("codex: %v", err)
		}
	}
	id, err := r.m.Create(bg, api.NewSession{Cwd: r.cwd, Text: "hi", Mode: "manual"}, "网页")
	if err != nil {
		t.Fatal(err)
	}
	r.waitState(id, "its turn", func(s *api.SessionState) bool { return s.Status == api.StatusIdle && s.Last != nil })
	for _, c := range []struct {
		reason string
		steps  int
	}{{" ", 0}, {"x", 4}, {"x", -1}, {"x", 0}} {
		if _, err := r.m.AskManual(bg, id, c.reason, c.steps); !errors.Is(err, ErrBadRequest) {
			t.Errorf("%q %d: %v", c.reason, c.steps, err)
		}
	}
	if _, err := r.m.ManualState(bg, id, 0); !errors.Is(err, ErrNotFound) {
		t.Errorf("none asked: %v", err)
	}
}

// Not answered within its wait: withdrawn, as refused. (Not parallel: the wait is shorter here.)
func TestManualUnanswered(t *testing.T) {
	was := manualWait
	manualWait = 300 * time.Millisecond
	defer func() { manualWait = was }()
	r := newRig(t, nil)
	id, st := askManual(t, r, 1)
	if ms, err := r.m.ManualState(bg, id, 5*time.Second); err != nil || ms.State != "expired" || ms.RequestID != st.RequestID {
		t.Fatalf("unanswered: %+v %v", ms, err)
	}
	s := r.waitState(id, "withdrawn", func(s *api.SessionState) bool { return len(s.Asks) == 0 })
	if s.Mode != "auto" {
		t.Errorf("unanswered: %s", s.Mode)
	}
}

// Approved for steps: manual mode lasts till a person has answered that many, within the turn;
// one before the last leaves it.
func TestManualSteps(t *testing.T) {
	t.Parallel()
	for _, steps := range []int{1, 2} {
		r := newRig(t, nil)
		id, err := r.m.Create(bg, api.NewSession{Cwd: r.cwd, Text: "hi", Mode: "auto"}, "网页")
		if err != nil {
			t.Fatal(err)
		}
		r.waitState(id, "its turn", func(s *api.SessionState) bool { return s.Status == api.StatusIdle && s.Last != nil })
		st, err := r.m.AskManual(bg, id, "two steps to run", steps)
		if err != nil || st.Steps != steps {
			t.Fatalf("asked: %+v %v", st, err)
		}
		if err := r.m.AnswerCard(bg, api.CardAnswer{Card: AskCardID(id, st.RequestID), Option: OptionAllow}); err != nil {
			t.Fatal(err)
		}
		r.waitState(id, "manual", func(s *api.SessionState) bool { return s.Mode == releaseMode && len(s.Asks) == 0 })
		if _, _, err := r.m.SendWith(bg, id, "please ASKASK", "网页", true); err != nil {
			t.Fatal(err)
		}
		s := r.waitState(id, "the first step", func(s *api.SessionState) bool { return len(s.Asks) == 1 })
		first := s.Asks[0].RequestID
		if s.Mode != releaseMode {
			t.Fatalf("%d steps, the first asked: %s", steps, s.Mode)
		}
		if err := r.m.Answer(bg, id, api.Answer{RequestID: first, Kind: "permission", Allow: true}); err != nil {
			t.Fatal(err)
		}
		s = r.waitState(id, "the second step", func(s *api.SessionState) bool {
			return len(s.Asks) == 1 && s.Asks[0].RequestID != first && (steps == 2 || s.Mode == "auto")
		})
		if want := map[int]string{1: "auto", 2: releaseMode}[steps]; s.Mode != want {
			t.Fatalf("%d steps, one answered: %s", steps, s.Mode)
		}
		if err := r.m.Answer(bg, id, api.Answer{RequestID: s.Asks[0].RequestID, Kind: "permission", Allow: false, Message: "no"}); err != nil {
			t.Fatal(err)
		}
		r.waitState(id, "auto again", func(s *api.SessionState) bool { return s.Mode == "auto" })
		if _, err := os.Stat(filepath.Join(r.m.holdRoot(), id, fileRelease)); !os.IsNotExist(err) {
			t.Errorf("%d steps, over, still on disk: %v", steps, err)
		}
	}
}
