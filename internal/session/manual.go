package session

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/kumoSleeping/iridium/internal/agent"
	"github.com/kumoSleeping/iridium/internal/api"
)

// Manual approval asked for (kumo, 2026-11-25): Claude whose auto mode denied a step it needs asks
// a person, with ir, to switch it to manual approval for a while (AskManual). The ask is one of the
// session's asks, of kind "manual", carried and answered as the others are; only a person answers
// it (the server sees to that). Approved, the process goes to manual mode (a release, release.go)
// till the turn ends or a person has answered its steps (at most three; kumo, 2026-11-26: steps,
// not minutes); refused, nothing changes. No answer within manualWait, or the turn over first: it
// is withdrawn, as refused.

// AskManual is the kind of the ask.
const AskManual = "manual"

// The steps an agent may give (ManualAsk.Steps): at least, at most, none given.
const (
	manualMin     = 1
	manualMax     = 3
	manualDefault = 3
)

// manualWait is how long a manual ask waits for its answer (Claude's Bash waits ten minutes at
// most): shorter in the tests.
var manualWait = 10 * time.Minute

// manualAsk is the latest manual ask of a session's: its state (api.ManualState's, or answering:
// being approved), done closed once it is over; steps: how many steps manual mode lasts at most;
// until: its wait's end; timer: the wait for its answer.
type manualAsk struct {
	req   string
	steps int
	until time.Time
	state string
	done  chan struct{}
	timer *time.Timer
}

func (a *manualAsk) view() api.ManualState {
	st := api.ManualState{RequestID: a.req, State: a.state, Steps: a.steps}
	if a.state == "answering" {
		st.State = "pending" // being approved: the mode is being switched
	}
	if st.State == "pending" {
		st.Until = a.until.UnixMilli()
	}
	return st
}

// AskManual opens session id's ask for manual approval, its agent's (Claude's, live): reason, a
// line; steps, how many steps asked of a person manual mode lasts at most. One already pending is
// given back as it is.
func (m *Manager) AskManual(ctx context.Context, id, reason string, steps int) (api.ManualState, error) {
	h, err := m.get(id)
	if err != nil {
		return api.ManualState{}, err
	}
	if h.meta.Agent != api.AgentClaude {
		return api.ManualState{}, fmt.Errorf("%w: only Claude has auto mode to switch from", ErrBadRequest)
	}
	reason = oneLine(reason, 300)
	if reason == "" {
		return api.ManualState{}, fmt.Errorf("%w: say why: reason", ErrBadRequest)
	}
	if steps == 0 {
		steps = manualDefault
	}
	if steps < manualMin || steps > manualMax {
		return api.ManualState{}, fmt.Errorf("%w: steps: %d to %d", ErrBadRequest, manualMin, manualMax)
	}
	h.mu.Lock()
	if !h.live {
		h.mu.Unlock()
		return api.ManualState{}, ErrNotLive
	}
	if a := h.manual; a != nil && a.state == "pending" {
		st := a.view()
		h.mu.Unlock()
		return st, nil
	}
	if isManual(h.mode) {
		mode := h.mode
		h.mu.Unlock()
		return api.ManualState{}, fmt.Errorf("%w: in %s mode already: each step not allowed by a rule is asked", ErrBadRequest, mode)
	}
	a := &manualAsk{req: AskManual + "-" + newUUID(), steps: steps, until: time.Now().Add(manualWait), state: "pending", done: make(chan struct{})}
	ask := api.Ask{RequestID: a.req, Kind: AskManual, Input: map[string]any{"reason": reason, "steps": steps}, Description: reason}
	h.asks = append(h.asks, ask)
	h.manual = a
	a.timer = time.AfterFunc(time.Until(a.until), func() { h.manualOver(a, "expired") })
	st := a.view()
	from, via := h.turnFrom, h.turnVia
	h.mu.Unlock()
	h.logf("its agent asks for manual mode for %d steps: %s", steps, reason)
	h.publish()
	m.publishList()
	m.notify(Notice{Session: id, Kind: NoticeAsk, Ask: &ask, From: from, Via: via})
	return st, nil
}

// ManualState is how session id's latest manual ask stands; while it is pending, waited on for
// as long as wait.
func (m *Manager) ManualState(ctx context.Context, id string, wait time.Duration) (api.ManualState, error) {
	h, err := m.get(id)
	if err != nil {
		return api.ManualState{}, err
	}
	h.mu.Lock()
	a := h.manual
	h.mu.Unlock()
	if a == nil {
		return api.ManualState{}, fmt.Errorf("%w: no manual ask", ErrNotFound)
	}
	if wait > 0 {
		t := time.NewTimer(wait)
		defer t.Stop()
		select {
		case <-a.done:
		case <-t.C:
		case <-ctx.Done():
		}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return a.view(), nil
}

// AskKind is the kind of session id's open ask request, "" if there is none.
func (m *Manager) AskKind(id, request string) string {
	h, err := m.get(id)
	if err != nil {
		return ""
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if i := slices.IndexFunc(h.asks, func(a api.Ask) bool { return a.RequestID == request }); i >= 0 {
		return h.asks[i].Kind
	}
	return ""
}

// endManualLocked ends manual ask a as state, if it is pending (or being answered): its ask is
// taken away. False if it was over already.
func (h *Hosted) endManualLocked(a *manualAsk, state string) bool {
	if a.state != "pending" && a.state != "answering" {
		return false
	}
	a.state = state
	if a.timer != nil {
		a.timer.Stop()
	}
	close(a.done)
	h.asks = slices.DeleteFunc(h.asks, func(x api.Ask) bool { return x.RequestID == a.req })
	return true
}

// manualOver ends manual ask a unanswered (its wait passed): withdrawn.
func (h *Hosted) manualOver(a *manualAsk, state string) {
	h.mu.Lock()
	if a.state != "pending" || !h.endManualLocked(a, state) {
		h.mu.Unlock()
		return
	}
	h.mu.Unlock()
	h.logf("manual mode not answered: %s", state)
	h.publish()
	h.m.publishList()
	h.m.notify(Notice{Session: h.id, Kind: NoticeAskDone, RequestID: a.req})
}

// manualTurnOverLocked: the turn ended; a manual ask still pending is withdrawn (its notice
// given to note).
func (h *Hosted) manualTurnOverLocked(note func(Notice)) {
	if a := h.manual; a != nil && a.state == "pending" && h.endManualLocked(a, "expired") {
		note(Notice{Kind: NoticeAskDone, RequestID: a.req})
	}
}

// answerManual answers manual ask request: allow, the process goes to manual mode (a release)
// till the turn ends or its steps are answered; else nothing changes.
func (h *Hosted) answerManual(ctx context.Context, request string, allow bool) error {
	h.mu.Lock()
	a := h.manual
	if a == nil || a.req != request || a.state != "pending" {
		h.mu.Unlock()
		return fmt.Errorf("%w: the ask is no longer open", ErrGone)
	}
	if !allow {
		h.endManualLocked(a, "refused")
		h.mu.Unlock()
		h.logf("manual mode refused")
		h.publish()
		h.m.publishList()
		h.m.notify(Notice{Session: h.id, Kind: NoticeAskDone, RequestID: a.req})
		return nil
	}
	a.state = "answering" // its wait's end does not take it now
	prev := h.mode
	h.mu.Unlock()
	if !isManual(prev) {
		if err := h.drv.SetMode(ctx, releaseMode); err != nil {
			h.mu.Lock()
			a.state = "pending"
			late := time.Now().After(a.until) // its timer found it being answered
			h.mu.Unlock()
			if late {
				h.manualOver(a, "expired")
			}
			return err
		}
	}
	h.mu.Lock()
	if h.rel != nil {
		prev = h.rel.prev // one under way still: back to its mode
	}
	now := time.Now()
	busy := h.turn != "" && h.turn != agent.Idle
	h.mode = releaseMode
	h.setRelLocked(&released{prev: prev, at: now, during: busy, left: a.steps})
	h.endManualLocked(a, "approved")
	h.mu.Unlock()
	h.logf("manual mode approved for %d steps (from %s mode)", a.steps, prev)
	h.publish()
	h.m.publishList()
	h.m.notify(Notice{Session: h.id, Kind: NoticeAskDone, RequestID: a.req})
	return nil
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
