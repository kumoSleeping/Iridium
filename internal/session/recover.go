package session

import (
	"context"
	"math/rand/v2"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/store"
)

// Crash auto-continue and quota auto-continue (Rhodium §4.8, product §4.5, §4.6), each behind its
// config switch.

const (
	// RecoveryPrompt is what Iridium tells an agent that cannot run an interrupted turn again by
	// itself (the agent sees it as Iridium's own line).
	RecoveryPrompt = "上一回合被打断，先检查现状再继续"
	// QuotaPrompt goes when the quota is back and nothing else waits.
	QuotaPrompt = "继续"
	// The circuit breaker: this many crashes of a session within BreakerWindow, and it is not
	// continued again.
	BreakerCrashes = 3
	BreakerWindow  = 10 * time.Minute
)

// crashed decides on a crash of the agent process: continue it if the switch is on, the agent
// had the turn (it took a message of it in, or began it itself; else there is nothing to go on
// with), and the breaker has not tripped. Called with h.mu held.
func (h *Hosted) crashedLocked(hadTurn bool) bool {
	now := time.Now()
	kept := h.crashes[:0]
	for _, t := range h.crashes {
		if now.Sub(t) < BreakerWindow {
			kept = append(kept, t)
		}
	}
	h.crashes = append(kept, now)
	if !h.m.o.Config.CrashResume || !hadTurn {
		return false
	}
	if len(h.crashes) >= BreakerCrashes {
		h.logf("crashed %d times in %s: not continuing it", len(h.crashes), BreakerWindow)
		return false
	}
	return true
}

// resume starts the agent again after a crash, asking it to run the interrupted turn again by
// itself (Claude: CLAUDE_CODE_RESUME_INTERRUPTED_TURN). Once it answers the handshake, if no turn
// began, Iridium sends its recovery prompt.
func (h *Hosted) resume() {
	h.mu.Lock()
	h.crashResume = true
	h.mu.Unlock()
	h.logf("continuing after the crash")
	ctx, cancel := context.WithTimeout(h.m.ctx, time.Minute)
	defer cancel()
	if err := h.start(ctx); err != nil {
		h.logf("continuing after the crash: %v", err)
	}
}

// helloAfterResume: the handshake of a process started to continue a crashed turn is done.
func (h *Hosted) helloAfterResume(turnSeen bool) {
	if turnSeen {
		return // the agent runs the turn again by itself
	}
	h.say(RecoveryPrompt)
}

// say sends Iridium's own line now, joining a running turn if there is one.
func (h *Hosted) say(text string) {
	ctx, cancel := context.WithTimeout(h.m.ctx, time.Minute)
	defer cancel()
	h.qmu.Lock()
	defer h.qmu.Unlock()
	in := store.Input{ID: newUUID(), Session: h.id, Terminal: api.SourceIridium, Text: text, State: "sent"}
	if err := h.m.o.Store.AddInput(in); err != nil {
		h.logf("%v", err)
		return
	}
	if err := h.write(ctx, in, nil); err != nil {
		h.logf("writing %q: %v", text, err)
	}
}

// quotaOut: the agent was refused for its quota until resetsAt (ms). With the switch on, the
// session waits until then (staggered, so sessions waiting on one account do not all go at once),
// messages queue meanwhile, and then the queue goes, or "继续" if nothing waits. Called with h.mu
// held.
func (h *Hosted) quotaOutLocked(resetsAt int64) {
	if !h.m.o.Config.QuotaResume {
		return
	}
	until := time.UnixMilli(resetsAt)
	if s := h.m.o.QuotaStagger; s > 0 {
		until = until.Add(rand.N(s))
	}
	if h.quotaTimer != nil {
		h.quotaTimer.Stop()
	}
	h.quotaUntil, h.quotaStop = until.UnixMilli(), false
	h.quotaTimer = time.AfterFunc(max(0, time.Until(until)), h.quotaBack)
	h.logf("quota out: going on at %s", until.Format(time.DateTime))
}

// quotaEndLocked ends the wait for the quota (the agent began a turn by itself meanwhile).
func (h *Hosted) quotaEndLocked() {
	if h.quotaTimer != nil {
		h.quotaTimer.Stop()
		h.quotaTimer = nil
	}
	h.quotaUntil = 0
}

func (h *Hosted) quotaBack() {
	h.mu.Lock()
	if h.quotaUntil == 0 {
		h.mu.Unlock()
		return
	}
	h.quotaUntil, h.quotaTimer = 0, nil
	stop := h.quotaStop
	waiting := false
	for _, q := range h.queue {
		waiting = waiting || !q.Paused
	}
	h.mu.Unlock()
	h.publish()
	switch {
	case waiting:
		ctx, cancel := context.WithTimeout(h.m.ctx, time.Minute)
		defer cancel()
		h.release(ctx, false)
	case !stop:
		h.say(QuotaPrompt)
	}
}
