package server

import (
	"strings"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
)

// An agent's reply written while no daemon listened is said where it was called once the next
// one is up, once; its call closed (kumo, 2026-11-25: an update as an agent ended its turn left
// its answer in its own session).
func TestMissedReplySaid(t *testing.T) {
	t.Parallel()
	l, ch, second := two(t)
	// What it wrote, in its own channel alone: as a turn replayed leaves it.
	if code, _ := l.send("web", second, api.Send{Text: "the answer"}); code != 200 {
		t.Fatalf("send: %d", code)
	}
	l.waitReply("web", second, "ok: the answer")
	l.idle(second)
	// Its turn's end done with first: else, handled late, it closed the call put below as one
	// its turn never answered, before the missed reply was looked for (on a loaded box).
	l.settled(second)
	count := func() (n int) {
		for _, m := range l.said("web", ch) {
			if strings.Contains(m, "ok: the answer") {
				n++
			}
		}
		return n
	}
	if count() != 0 {
		t.Fatalf("said in the channel with no call: %v", l.said("web", ch))
	}
	l.s.calls.add(second, called{Channel: ch, Seq: 1, old: true})
	l.s.settleMissed()
	l.waitSaid("web", ch, "ok: the answer")
	l.noCalls(second)
	// Looked for again (another call kept from before): the channel has those words.
	l.s.calls.add(second, called{Channel: ch, Seq: 1, old: true})
	l.s.settleMissed()
	l.noCalls(second)
	time.Sleep(200 * time.Millisecond)
	if n := count(); n != 1 {
		t.Fatalf("said %d times: %v", n, l.said("web", ch))
	}
}

// settled waits until what session id's turns brought is done with: the notices handled, its
// answers (the turn's end) run.
func (l *live) settled(id string) {
	l.t.Helper()
	l.s.sat.Settled(5 * time.Second)
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		l.s.answers.mu.Lock()
		n := len(l.s.answers.of[id])
		l.s.answers.mu.Unlock()
		if n == 0 {
			return
		}
		if time.Now().After(deadline) {
			l.t.Fatalf("%s: answers still waiting", id)
		}
	}
}
