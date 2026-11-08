package session

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/agent"
	"github.com/kumoSleeping/iridium/internal/api"
)

// The denied call is found on an earlier page, its result on a later one; its whole input read
// when the page has only its start.
func TestDeniedCall(t *testing.T) {
	denial := "Permission for this action was denied by the Claude Code auto mode classifier. Reason: [Git Destructive]."
	pages := map[int64]api.MessagePage{
		-1:  {Start: 100, More: true, Blocks: []api.Block{{Kind: api.BlockKindResult, ID: "t1", Text: denial, Error: true}, {Kind: api.BlockKindTool, ID: "t2", Name: "Bash", Input: `{"command":"ls"}`, Result: &api.Result{Text: "ok"}}}},
		100: {Start: 0, Blocks: []api.Block{{Kind: api.BlockKindTool, At: "0.1", ID: "t1", Name: "Bash", Input: `{"comm`, Length: 40}}},
	}
	before := func(at int64) (api.MessagePage, error) { return pages[at], nil }
	full := func(at string) (string, error) {
		if at != "0.1" {
			return "", errors.New("no block")
		}
		return `{"command":"git push --force","description":"push"}`, nil
	}
	name, in, denied, err := deniedCall(before, full, "t1")
	if err != nil || name != "Bash" || in["command"] != "git push --force" || !denied {
		t.Fatalf("t1: %q %v %v %v", name, in, denied, err)
	}
	if _, _, denied, err := deniedCall(before, full, "t2"); err != nil || denied {
		t.Errorf("t2 was not denied: %v %v", denied, err)
	}
	if _, _, _, err := deniedCall(before, full, "t9"); !errors.Is(err, ErrBadRequest) {
		t.Errorf("t9: %v", err)
	}
}

// A release ends with the turn under way at it, or with a turn begun after it; the mode goes
// back only if it is still the release's.
func TestReleaseOver(t *testing.T) {
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

// A denied call gets its card, once, with what the call was; a release is over as soon as the
// call run again is done, not at the turn's end.
func TestDenialAndRanAgain(t *testing.T) {
	h := &Hosted{mode: "auto"}
	h.callLocked(agent.ToolStart{ID: "t1", Name: "Bash", Input: json.RawMessage(`{"command":"npx x","description":"get x"}`)})
	if !h.deniedLocked(agent.ToolDone{ID: "t1", Denied: "Code from External"}) || h.deniedLocked(agent.ToolDone{ID: "t1", Denied: "x"}) {
		t.Fatal("one card per call")
	}
	d := h.denials[0]
	if d.Tool != "Bash" || d.Reason != "Code from External" || asMap(d.Input)["command"] != "npx x" {
		t.Fatalf("card %+v", d)
	}
	if !h.dropDenial("t1") || len(h.denials) != 0 {
		t.Fatal("dismissed")
	}

	h.mode = releaseMode
	h.rel = &released{tool: "Bash", input: map[string]any{"command": "npx x"}, prev: "auto", at: time.Now()}
	h.callLocked(agent.ToolStart{ID: "t2", Name: "Bash", Input: map[string]any{"command": "ls"}})
	h.callLocked(agent.ToolStart{ID: "t3", Name: "Bash", Input: map[string]any{"command": "npx x", "description": "again"}})
	if _, over := h.releaseRanLocked("t2"); over {
		t.Fatal("another call ended it")
	}
	if m, over := h.releaseRanLocked("t3"); !over || m != "auto" || h.rel != nil {
		t.Fatalf("run again: %q %v", m, over)
	}

}

// A step auto mode denied waits as a card and the session waits on you; switched to manual for
// it, nothing is said to Claude; asked again, it is your ask, and the mode is auto again after;
// dismissed, only the card goes.
func TestReleaseFlow(t *testing.T) {
	t.Parallel()
	r := newRig(t, nil)
	ctx := context.Background()
	id, err := r.m.Create(ctx, api.NewSession{Cwd: r.cwd, Text: "go DENIED", Mode: "auto"}, "网页")
	if err != nil {
		t.Fatal(err)
	}
	s := r.waitState(id, "the denial's card", func(s *api.SessionState) bool { return len(s.Denials) == 1 && s.Status == api.StatusWaiting })
	d := s.Denials[0]
	if d.ToolUseID != "toolu_denied" || d.Tool != "Bash" || d.Reason != "Code from External" || asMap(d.Input)["command"] != "npx -y some-tool" {
		t.Fatalf("denial %+v", d)
	}
	if err := r.m.Release(ctx, id, d.ToolUseID, false); err != nil {
		t.Fatal(err)
	}
	s = r.waitState(id, "manual, nothing said", func(s *api.SessionState) bool { return s.Mode == releaseMode && len(s.Denials) == 0 })
	if s.Last != nil && strings.Contains(s.Last.Text, "放行") {
		t.Fatalf("a message: %+v", s.Last)
	}
	if _, err := r.m.Send(ctx, id, "go AGAIN", "网页"); err != nil {
		t.Fatal(err)
	}
	s = r.waitState(id, "asked", func(s *api.SessionState) bool { return len(s.Asks) == 1 })
	if s.Mode != releaseMode {
		t.Fatalf("asked in %s mode", s.Mode)
	}
	if err := r.m.Answer(ctx, id, api.Answer{RequestID: s.Asks[0].RequestID, Kind: "permission", Allow: true}); err != nil {
		t.Fatal(err)
	}
	r.waitState(id, "back in auto", func(s *api.SessionState) bool { return s.Status == api.StatusIdle && s.Mode == "auto" })

	if _, err := r.m.Send(ctx, id, "again DENIED", "网页"); err != nil {
		t.Fatal(err)
	}
	r.waitState(id, "a second card", func(s *api.SessionState) bool { return len(s.Denials) == 1 })
	if err := r.m.Release(ctx, id, "toolu_denied", true); err != nil {
		t.Fatal(err)
	}
	// The card goes at once; the turn it came in may still be ending (a slow machine).
	s = r.waitState(id, "dismissed", func(s *api.SessionState) bool { return len(s.Denials) == 0 && s.Status == api.StatusIdle })
	if s.Mode != "auto" {
		t.Errorf("dismissed: %s %s", s.Status, s.Mode)
	}
}
