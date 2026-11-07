package session

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
)

// A step Claude's auto mode classifier turned down never reaches Iridium as an ask: the CLI
// answers the model itself (the user, 2026-11-07). Release lets you allow it from the page: the
// running process goes to manual mode (only it: the session's setting stays, so a restart is back
// in its own mode), Claude is told to run it again, the ask that comes of it the same call is
// allowed by itself, any other is asked as ever, and once the turn ends the mode goes back.

// Denied is in the tool result Claude writes for a call its classifier denied.
const Denied = "Permission for this action was denied by the Claude Code auto mode classifier"

// releaseMode is the mode a release runs in: every call not allowed by a rule is asked.
const releaseMode = "manual"

// released is a release under way: the call allowed once, the mode to go back to.
type released struct {
	tool  string
	input map[string]any
	prev  string
	at    time.Time
	used  bool
}

// Release lets Claude run call tool (a tool_use id) again, which its auto mode classifier denied.
func (m *Manager) Release(ctx context.Context, id, tool, from string) error {
	h, err := m.take(ctx, id)
	if err != nil {
		return err
	}
	if h.meta.Agent != api.AgentClaude {
		return fmt.Errorf("%w: only Claude's auto mode denies steps", ErrBadRequest)
	}
	f, err := m.readable(id)
	if err != nil {
		return err
	}
	if f == nil {
		return fmt.Errorf("%w: no call %s", ErrBadRequest, tool)
	}
	name, input, denied, err := deniedCall(f.Before, f.Full, tool)
	if err != nil {
		return err
	}
	if !denied {
		return fmt.Errorf("%w: call %s was not denied by auto mode", ErrBadRequest, tool)
	}
	if err := h.start(ctx); err != nil {
		return err
	}
	h.mu.Lock()
	prev := h.mode
	h.mu.Unlock()
	if prev != releaseMode && prev != "default" {
		if err := h.drv.SetMode(ctx, releaseMode); err != nil {
			return err
		}
	}
	h.mu.Lock()
	if h.rel != nil {
		prev = h.rel.prev // a second release in the same one: back to the first's mode
	}
	h.mode = releaseMode
	h.rel = &released{tool: name, input: input, prev: prev, at: time.Now()}
	h.mu.Unlock()
	h.publish()
	h.logf("released %s %s (from %s mode)", name, tool, prev)
	what := name
	if c, _ := input["command"].(string); c != "" {
		what = "`" + oneLine(c, 200) + "`"
	} else if p := subjectOf(api.Ask{Tool: name, Input: input}); p != "" {
		what = name + " " + p
	}
	text := "刚才被 auto 模式拦下的那一步（" + what + "），我放行了：请原样再做一次。"
	_, _, err = m.SendWith(ctx, id, text, from, true)
	return err
}

// deniedCall finds call id in the transcript, the latest pages first: its tool, its input, and
// whether its result is the classifier's denial.
func deniedCall(before func(int64) (api.MessagePage, error), full func(string) (string, error), id string) (string, map[string]any, bool, error) {
	at := int64(-1)
	var call *api.Block
	denied := false
	for range 50 {
		p, err := before(at)
		if err != nil {
			return "", nil, false, err
		}
		for i := range p.Blocks {
			b := p.Blocks[i]
			if b.ID != id {
				continue
			}
			switch b.Kind {
			case api.BlockKindTool:
				call = &p.Blocks[i]
				if b.Result != nil && b.Result.Error && strings.Contains(b.Result.Text, Denied) {
					denied = true
				}
			case api.BlockKindResult:
				if b.Error && strings.Contains(b.Text, Denied) {
					denied = true
				}
			}
		}
		if call != nil {
			break
		}
		if !p.More {
			break
		}
		at = p.Start
	}
	if call == nil {
		return "", nil, false, fmt.Errorf("%w: no call %s", ErrBadRequest, id)
	}
	raw := call.Input
	if call.Length > 0 {
		if s, err := full(call.At); err == nil {
			raw = s
		}
	}
	var input map[string]any
	if err := json.Unmarshal([]byte(raw), &input); err != nil {
		return "", nil, false, fmt.Errorf("%w: call %s's input: %v", ErrBadRequest, id, err)
	}
	return call.Name, input, denied, nil
}

// sameCall: ask is the call released, run again. Its description (Bash's) may be worded anew.
func sameCall(r *released, a api.Ask) bool {
	if a.Kind != "permission" || a.Tool != r.tool {
		return false
	}
	in, _ := a.Input.(map[string]any)
	strip := func(m map[string]any) map[string]any {
		out := make(map[string]any, len(m))
		for k, v := range m {
			if k != "description" {
				out[k] = v
			}
		}
		return out
	}
	return reflect.DeepEqual(strip(in), strip(r.input))
}

// releaseOver ends a release with the turn: once the call was allowed, or the turn began after
// it (Claude did not run it again). It gives the mode to go back to, "" for none.
func (h *Hosted) releaseOverLocked() string {
	r := h.rel
	if r == nil || (!r.used && !h.turnAt.After(r.at)) {
		return ""
	}
	h.rel = nil
	if h.mode != releaseMode || r.prev == releaseMode || r.prev == "default" {
		return ""
	}
	return r.prev
}

// restoreMode puts the process back in mode after a release.
func (h *Hosted) restoreMode(mode string) {
	ctx, cancel := context.WithTimeout(h.m.ctx, 30*time.Second)
	defer cancel()
	if !h.isLive() {
		return
	}
	if err := h.drv.SetMode(ctx, mode); err != nil {
		h.logf("release: back to %s mode: %v", mode, err)
		return
	}
	h.mu.Lock()
	if h.rel == nil {
		h.mode = mode
	}
	h.mu.Unlock()
	h.publish()
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
