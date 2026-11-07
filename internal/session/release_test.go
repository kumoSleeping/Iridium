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

// The call run again is allowed whatever its description now says; another is not.
func TestSameCall(t *testing.T) {
	r := &released{tool: "Bash", input: map[string]any{"command": "rm -rf build", "description": "clean"}}
	for _, c := range []struct {
		a    api.Ask
		want bool
	}{
		{api.Ask{Kind: "permission", Tool: "Bash", Input: map[string]any{"command": "rm -rf build", "description": "clean it again"}}, true},
		{api.Ask{Kind: "permission", Tool: "Bash", Input: map[string]any{"command": "rm -rf build ~"}}, false},
		{api.Ask{Kind: "permission", Tool: "Write", Input: map[string]any{"command": "rm -rf build"}}, false},
		{api.Ask{Kind: "question", Tool: "Bash", Input: map[string]any{"command": "rm -rf build"}}, false},
	} {
		if got := sameCall(r, c.a); got != c.want {
			t.Errorf("%+v: %v", c.a, got)
		}
	}
}

// A release ends with a turn once its call was allowed, or with a turn begun after it; the mode
// goes back only if it is still the release's.
func TestReleaseOver(t *testing.T) {
	at := time.Now()
	h := &Hosted{mode: releaseMode, turnAt: at.Add(-time.Minute)}
	h.rel = &released{prev: "auto", at: at}
	if m := h.releaseOverLocked(); m != "" || h.rel == nil {
		t.Errorf("the turn it was steered into, not run again: %q", m)
	}
	h.rel.used = true
	if m := h.releaseOverLocked(); m != "auto" || h.rel != nil {
		t.Errorf("run again: %q", m)
	}
	h.rel, h.turnAt = &released{prev: "auto", at: at}, at.Add(time.Second)
	if m := h.releaseOverLocked(); m != "auto" {
		t.Errorf("a turn after it: %q", m)
	}
	h.rel, h.mode = &released{prev: "auto", at: at, used: true}, "plan"
	if m := h.releaseOverLocked(); m != "" || h.rel != nil {
		t.Errorf("mode changed meanwhile: %q", m)
	}
}

// A denied call gets its card, once, with what the call was; a release is over as soon as the
// call run again is done, not at the turn's end; a turn that took its message ends it too.
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

	h.rel = &released{prev: "auto", at: time.Now(), taken: true}
	h.turnAt = h.rel.at.Add(-time.Minute)
	if m := h.releaseOverLocked(); m != "auto" {
		t.Errorf("the turn that took the message ended: %q", m)
	}
}

// A step auto mode denied waits as a card and the session waits on you; released, Claude asks
// for it again in manual mode, Iridium allows it, and the mode is auto again; dismissed, only the
// card goes.
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
	if err := r.m.Release(ctx, id, d.ToolUseID, "网页", false); err != nil {
		t.Fatal(err)
	}
	s = r.waitState(id, "run again, back in auto", func(s *api.SessionState) bool {
		return s.Status == api.StatusIdle && s.Mode == "auto" && s.Last != nil && strings.Contains(s.Last.Text, "请原样再做一次")
	})
	if len(s.Denials) != 0 || len(s.Asks) != 0 {
		t.Fatalf("after the release: %+v %+v", s.Denials, s.Asks)
	}

	if _, err := r.m.Send(ctx, id, "again DENIED", "网页"); err != nil {
		t.Fatal(err)
	}
	r.waitState(id, "a second card", func(s *api.SessionState) bool { return len(s.Denials) == 1 })
	if err := r.m.Release(ctx, id, "toolu_denied", "网页", true); err != nil {
		t.Fatal(err)
	}
	s = r.waitState(id, "dismissed", func(s *api.SessionState) bool { return len(s.Denials) == 0 })
	if s.Status != api.StatusIdle || s.Mode != "auto" {
		t.Errorf("dismissed: %s %s", s.Status, s.Mode)
	}
}
