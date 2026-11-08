package session

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/kumoSleeping/iridium/internal/agent"
	"github.com/kumoSleeping/iridium/internal/api"
)

// A step Claude's auto mode classifier turned down never reaches Iridium as an ask: the CLI
// answers the model itself (the user, 2026-11-07). Each one is kept (Denials) till you switch to
// manual for it, let it be, or send the session a message (the user, 2026-11-08). They need you
// only when the turn stopped on one, no call done after it (stuckOn): else Claude went another
// way, and the page shows them folded, the session idle (the user, 2026-11-08). Switching: the
// running process goes to manual mode (only it: the session's setting stays, so a restart is back
// in its own mode) and nothing is said to Claude: a message in your name told it to run the step
// again, though it may have gone another way meanwhile (the user, 2026-11-08). The first step
// asked then is the one it was for (the call run again, or another way to it): once that call is
// done the mode goes back (the user, 2026-11-08); none asked, the mode goes back when the turn
// ends: the one under way, or the next if none is. A release is kept on disk (fileRelease): a
// daemon taking the process up goes on with it, rather than leave the process in manual mode.

// Denied is in the tool result Claude writes for a call its classifier denied.
const Denied = agent.AutoDenied

// releaseMode is the mode a release runs in: every call not allowed by a rule is asked.
const releaseMode = "manual"

// maxDenials: the most denial cards a session keeps; the oldest go.
const maxDenials = 20

// fileRelease: a release under way (releaseMeta), in the hold directory.
const fileRelease = "release.json"

// releaseMeta is a release on disk: the process it put in manual mode, the call, the mode to go
// back to.
type releaseMeta struct {
	Proc  int            `json:"proc"`
	Tool  string         `json:"tool"`
	Input map[string]any `json:"input,omitempty"`
	Prev  string         `json:"prev"`
}

// released is a release under way: the call denied, the mode to go back to; during: a turn was
// under way then, its end ends the release; call: the call's id run again.
type released struct {
	tool   string
	input  map[string]any
	prev   string
	at     time.Time
	during bool
	call   string
}

// Release puts the process in manual mode for call tool (a tool_use id), which Claude's auto mode
// classifier denied; dismiss only takes its card away.
func (m *Manager) Release(ctx context.Context, id, tool string, dismiss bool) error {
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
	if !isManual(prev) {
		if err := h.drv.SetMode(ctx, releaseMode); err != nil {
			return err
		}
	}
	h.mu.Lock()
	if h.rel != nil {
		prev = h.rel.prev // a second release in the same one: back to the first's mode
	}
	h.mode = releaseMode
	busy := h.turn != "" && h.turn != agent.Idle
	h.setRelLocked(&released{tool: name, input: input, prev: prev, at: time.Now(), during: busy})
	h.denials = slices.DeleteFunc(h.denials, func(d api.Denial) bool { return d.ToolUseID == tool })
	h.mu.Unlock()
	h.publish()
	m.publishList()
	h.logf("released %s %s (from %s mode)", name, tool, prev)
	return nil
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

// stuckLocked: the turn stopped on a call auto mode denied, its card still there.
func (h *Hosted) stuckLocked() bool {
	return h.stuckOn != "" && slices.ContainsFunc(h.denials, func(d api.Denial) bool { return d.ToolUseID == h.stuckOn })
}

// dropDenials takes every card away (a message sent: you went on); false if there were none.
func (h *Hosted) dropDenials() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := len(h.denials)
	h.denials, h.stuckOn = nil, ""
	return n > 0
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
	h.setRelLocked(nil)
	return h.backLocked(r), true
}

// releaseAskedLocked: the first step asked in a release is the one it is for, whichever call it
// is (Claude may run the step another way): once that call is done, the release is over.
func (h *Hosted) releaseAskedLocked(a api.Ask) {
	if r := h.rel; r != nil && r.call == "" && a.Kind == "permission" && a.ToolUseID != "" {
		r.call = a.ToolUseID
	}
}

// setRelLocked starts a release, or ends it (nil), on disk too.
func (h *Hosted) setRelLocked(r *released) {
	h.rel = r
	if h.dir == "" {
		return
	}
	path := filepath.Join(h.dir, fileRelease)
	if r == nil {
		_ = os.Remove(path)
		return
	}
	if err := writeJSON(path, releaseMeta{Proc: h.proc, Tool: r.tool, Input: r.input, Prev: r.prev}); err != nil {
		h.logf("release: %v", err)
	}
}

// helloReleaseLocked: process proc said hello in mode. The process an earlier daemon put in manual
// mode for a release, taken up, is in it still: the release goes on (from now, the turn under way
// ending it). Else none is under way.
func (h *Hosted) helloReleaseLocked(proc int, mode string) {
	h.rel = nil
	if h.dir == "" {
		return
	}
	var m releaseMeta
	b, err := os.ReadFile(filepath.Join(h.dir, fileRelease))
	if err != nil || json.Unmarshal(b, &m) != nil || m.Proc != proc || !isManual(mode) || isManual(m.Prev) {
		h.setRelLocked(nil)
		return
	}
	busy := h.turn != "" && h.turn != agent.Idle
	h.rel = &released{tool: m.Tool, input: m.Input, prev: m.Prev, at: time.Now(), during: busy}
	h.mode = releaseMode
	h.logf("release of %s goes on (back to %s mode after)", m.Tool, m.Prev)
}

// isManual: Claude's mode that asks for every call not allowed by a rule ("default" its old name).
func isManual(mode string) bool { return mode == releaseMode || mode == "default" }

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

// releaseOverLocked ends a release with the turn under way at it, or begun after it (Claude did
// not run the call again, or did without its id seen). It gives the mode to go back to, "" for
// none.
func (h *Hosted) releaseOverLocked() string {
	r := h.rel
	if r == nil || (!r.during && !h.turnAt.After(r.at)) {
		return ""
	}
	h.setRelLocked(nil)
	return h.backLocked(r)
}

// backLocked is the mode to go back to after release r, "" for none: you set one meanwhile, or
// it was asking already.
func (h *Hosted) backLocked(r *released) string {
	if h.mode != releaseMode || isManual(r.prev) {
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
