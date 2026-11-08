package session

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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

// The first step asked in a release is the one it is for, though Claude ran it another way: once
// it is done the mode goes back; a later ask does not take its place.
func TestReleaseFirstAsked(t *testing.T) {
	h := &Hosted{mode: releaseMode}
	h.rel = &released{tool: "Bash", input: map[string]any{"command": "git push --force"}, prev: "auto", at: time.Now()}
	h.callLocked(agent.ToolStart{ID: "t2", Name: "Bash", Input: map[string]any{"command": "git push --force-with-lease"}})
	h.releaseAskedLocked(api.Ask{Kind: "question", ToolUseID: "t1"})
	h.releaseAskedLocked(api.Ask{Kind: "permission", ToolUseID: "t2"})
	h.releaseAskedLocked(api.Ask{Kind: "permission", ToolUseID: "t3"})
	if _, over := h.releaseRanLocked("t3"); over {
		t.Fatal("a later ask ended it")
	}
	if m, over := h.releaseRanLocked("t2"); !over || m != "auto" || h.rel != nil {
		t.Fatalf("the first asked: %q %v", m, over)
	}
}

// A release is kept on disk: the process taken up in manual mode goes on with it; a new process,
// or one back in its own mode, ends it.
func TestReleaseTakenUp(t *testing.T) {
	dir := t.TempDir()
	h := &Hosted{dir: dir, proc: 3, mode: releaseMode}
	h.setRelLocked(&released{tool: "Bash", input: map[string]any{"command": "x"}, prev: "auto", at: time.Now()})

	m := &Manager{o: Options{Logf: t.Logf}}
	h2 := &Hosted{m: m, dir: dir, turn: agent.Idle}
	h2.helloReleaseLocked(3, "default")
	if h2.rel == nil || h2.rel.prev != "auto" || h2.rel.tool != "Bash" || h2.mode != releaseMode || h2.rel.during {
		t.Fatalf("taken up: %+v %s", h2.rel, h2.mode)
	}
	h2.releaseAskedLocked(api.Ask{Kind: "permission", ToolUseID: "t9"})
	if m, over := h2.releaseRanLocked("t9"); !over || m != "auto" {
		t.Fatalf("taken up, then asked: %q %v", m, over)
	}
	if _, err := os.Stat(filepath.Join(dir, fileRelease)); !os.IsNotExist(err) {
		t.Fatalf("over, still on disk: %v", err)
	}

	for _, c := range []struct {
		proc int
		mode string
	}{{4, "default"}, {3, "auto"}} {
		h.setRelLocked(&released{tool: "Bash", prev: "auto", at: time.Now()})
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
