package session

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/kumoSleeping/iridium/internal/agent"
	"github.com/kumoSleeping/iridium/internal/api"
)

// A step Claude's auto mode classifier turned down never reaches Iridium as an ask: the CLI
// answers the model itself (the user, 2026-11-07). Each one waits as a card above the composer
// (Denials) till you release it or let it be (the user, 2026-11-08). Release: the running process
// goes to manual mode (only it: the session's setting stays, so a restart is back in its own
// mode), Claude is told to run it again, the ask that comes of it the same call is allowed by
// itself, and once that call is done the mode goes back at once (not at the turn's end: the rest
// of the turn in manual mode asked for every step, 2026-11-08). Claude not running it again, the
// mode goes back when the turn that took the message ends.

// Denied is in the tool result Claude writes for a call its classifier denied.
const Denied = agent.AutoDenied

// releaseMode is the mode a release runs in: every call not allowed by a rule is asked.
const releaseMode = "manual"

// maxDenials: the most denial cards a session keeps; the oldest go.
const maxDenials = 20

// released is a release under way: the call allowed once, the mode to go back to; input: the
// message that asks Claude to run it again, taken: Claude took it; call: the call's id run again.
type released struct {
	tool  string
	input map[string]any
	prev  string
	at    time.Time
	used  bool
	msg   string
	taken bool
	call  string
}

// Release lets Claude run call tool (a tool_use id) again, which its auto mode classifier denied;
// dismiss only takes its card away.
func (m *Manager) Release(ctx context.Context, id, tool, from string, dismiss bool) error {
	if dismiss {
		h, err := m.get(id)
		if err != nil {
			return err
		}
		if h.dropDenial(tool) {
			h.publish()
			m.publishList()
		}
		return nil
	}
	h, err := m.take(ctx, id)
	if err != nil {
		return err
	}
	if h.meta.Agent != api.AgentClaude {
		return fmt.Errorf("%w: only Claude's auto mode denies steps", ErrBadRequest)
	}
	name, input, err := h.deniedOf(m, id, tool)
	if err != nil {
		return err
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
	h.denials = slices.DeleteFunc(h.denials, func(d api.Denial) bool { return d.ToolUseID == tool })
	h.mu.Unlock()
	h.publish()
	m.publishList()
	h.logf("released %s %s (from %s mode)", name, tool, prev)
	what := name
	if c, _ := input["command"].(string); c != "" {
		what = "`" + oneLine(c, 200) + "`"
	} else if p := subjectOf(api.Ask{Tool: name, Input: input}); p != "" {
		what = name + " " + p
	}
	text := "刚才被 auto 模式拦下的那一步（" + what + "），我放行了：请原样再做一次。"
	msg, _, err := m.SendWith(ctx, id, text, from, true)
	h.mu.Lock()
	if r := h.rel; r != nil && msg != "" {
		r.msg = msg
		r.taken = r.taken || h.lastTaken == msg
	}
	h.mu.Unlock()
	return err
}

// deniedOf is the call tool auto mode denied: from its card, else from the transcript.
func (h *Hosted) deniedOf(m *Manager, id, tool string) (string, map[string]any, error) {
	h.mu.Lock()
	i := slices.IndexFunc(h.denials, func(d api.Denial) bool { return d.ToolUseID == tool })
	var d api.Denial
	if i >= 0 {
		d = h.denials[i]
	}
	h.mu.Unlock()
	if i >= 0 && d.Input != nil {
		if in := asMap(d.Input); in != nil {
			return d.Tool, in, nil
		}
	}
	f, err := m.readable(id)
	if err != nil {
		return "", nil, err
	}
	if f == nil {
		return "", nil, fmt.Errorf("%w: no call %s", ErrBadRequest, tool)
	}
	name, input, denied, err := deniedCall(f.Before, f.Full, tool)
	if err != nil {
		return "", nil, err
	}
	if !denied {
		return "", nil, fmt.Errorf("%w: call %s was not denied by auto mode", ErrBadRequest, tool)
	}
	return name, input, nil
}

// asMap is a call's input as a map, through JSON (the driver's may be raw).
func asMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	var raw []byte
	switch x := v.(type) {
	case json.RawMessage:
		raw = x
	case []byte:
		raw = x
	case string:
		raw = []byte(x)
	default:
		raw, _ = json.Marshal(v)
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	return m
}

// callLocked keeps a call of the turn: what a denial of it was; a release's call run again.
func (h *Hosted) callLocked(e agent.ToolStart) {
	if h.calls == nil {
		h.calls = map[string]agent.ToolStart{}
	}
	if len(h.calls) < 500 {
		h.calls[e.ID] = e
	}
	if r := h.rel; r != nil && r.call == "" && e.Name == r.tool && sameInput(asMap(e.Input), r.input) {
		r.call = e.ID
	}
}

// deniedLocked adds the card of a call auto mode denied; false if it has one.
func (h *Hosted) deniedLocked(e agent.ToolDone) bool {
	if slices.ContainsFunc(h.denials, func(d api.Denial) bool { return d.ToolUseID == e.ID }) {
		return false
	}
	d := api.Denial{ToolUseID: e.ID, Tool: e.Name, At: time.Now().UnixMilli()}
	if e.Denied != "denied" {
		d.Reason = e.Denied
	}
	if c, ok := h.calls[e.ID]; ok {
		d.Tool, d.Input = c.Name, asMap(c.Input)
	}
	h.denials = append(h.denials, d)
	if n := len(h.denials); n > maxDenials {
		h.denials = slices.Delete(h.denials, 0, n-maxDenials)
	}
	return true
}

// dropDenial takes call tool's card away; false if there was none.
func (h *Hosted) dropDenial(tool string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := len(h.denials)
	h.denials = slices.DeleteFunc(h.denials, func(d api.Denial) bool { return d.ToolUseID == tool })
	return len(h.denials) != n
}

// releaseRanLocked: call id is done; if it is the released call run again the release is over,
// and mode is the one to go back to ("" for none).
func (h *Hosted) releaseRanLocked(id string) (mode string, over bool) {
	r := h.rel
	if r == nil || r.call == "" || r.call != id {
		return "", false
	}
	h.rel = nil
	return h.backLocked(r), true
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

// sameCall: ask is the call released, run again.
func sameCall(r *released, a api.Ask) bool {
	if a.Kind != "permission" || a.Tool != r.tool {
		return false
	}
	in, _ := a.Input.(map[string]any)
	return sameInput(in, r.input)
}

// sameInput: a call's input is another's. Its description (Bash's) may be worded anew.
func sameInput(a, b map[string]any) bool {
	strip := func(m map[string]any) map[string]any {
		out := make(map[string]any, len(m))
		for k, v := range m {
			if k != "description" {
				out[k] = v
			}
		}
		return out
	}
	return reflect.DeepEqual(strip(a), strip(b))
}

// releaseOverLocked ends a release with the turn, if the turn had the message (Claude did not
// run the call again, or did without its id seen). It gives the mode to go back to, "" for none.
func (h *Hosted) releaseOverLocked() string {
	r := h.rel
	if r == nil || (!r.used && !r.taken && !h.turnAt.After(r.at)) {
		return ""
	}
	h.rel = nil
	return h.backLocked(r)
}

// backLocked is the mode to go back to after release r, "" for none: you set one meanwhile, or
// it was asking already.
func (h *Hosted) backLocked(r *released) string {
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
