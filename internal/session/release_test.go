package session

import (
	"errors"
	"testing"
	"time"

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
