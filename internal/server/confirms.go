package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"slices"
	"sync"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/hub"
)

// ConfirmTTL is how long a confirmation waits for a panel; KeepAnswered how long its outcome
// stays for the asker to read.
const (
	ConfirmTTL   = 10 * time.Minute
	KeepAnswered = 10 * time.Minute
)

// confirms are what agents' ir asked for that a panel must give (plan "ir": the token never
// enters the agent's conversation). Kept in memory: a restart drops them, and the asker sees
// expired.
type confirms struct {
	hub *hub.Hub
	mu  sync.Mutex
	all map[string]*confirm
}

type confirm struct {
	c     api.Confirm
	state api.ConfirmState
	done  chan struct{} // closed once answered or expired
	ended time.Time
}

func newConfirms(h *hub.Hub) *confirms {
	cs := &confirms{hub: h, all: map[string]*confirm{}}
	cs.publish()
	return cs
}

func (cs *confirms) add(kind, name string, owner bool, asker, session string) api.Confirm {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	now := time.Now()
	c := api.Confirm{ID: hex.EncodeToString(b), Kind: kind, Name: name, Owner: owner, Asker: asker, Session: session,
		CreatedAt: now.UnixMilli(), ExpiresAt: now.Add(ConfirmTTL).UnixMilli()}
	cs.mu.Lock()
	cs.pruneLocked(now)
	cs.all[c.ID] = &confirm{c: c, state: api.ConfirmState{ID: c.ID, Kind: kind, State: "pending", Name: name, Owner: owner}, done: make(chan struct{})}
	cs.mu.Unlock()
	cs.publish()
	return c
}

// pruneLocked expires what waited too long and forgets what was answered long ago.
func (cs *confirms) pruneLocked(now time.Time) bool {
	changed := false
	for id, c := range cs.all {
		switch {
		case c.state.State == "pending" && now.UnixMilli() >= c.c.ExpiresAt:
			c.state.State, c.ended = "expired", now
			close(c.done)
			changed = true
		case c.state.State != "pending" && c.state.State != "answering" && now.Sub(c.ended) > KeepAnswered:
			delete(cs.all, id)
		}
	}
	return changed
}

func (cs *confirms) pending() api.Confirms {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	out := api.Confirms{Pending: []api.Confirm{}}
	for _, c := range cs.all {
		if c.state.State == "pending" {
			out.Pending = append(out.Pending, c.c)
		}
	}
	slices.SortFunc(out.Pending, func(a, b api.Confirm) int { return int(a.CreatedAt - b.CreatedAt) })
	return out
}

func (cs *confirms) publish() { cs.hub.Set("confirms", cs.pending()) }

// peek is pending confirmation id, unclaimed.
func (cs *confirms) peek(id string) (api.Confirm, bool) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	c := cs.all[id]
	if c == nil || c.state.State != "pending" || time.Now().UnixMilli() >= c.c.ExpiresAt {
		return api.Confirm{}, false
	}
	return c.c, true
}

// take claims pending confirmation id for answering: no one else answers it meanwhile.
func (cs *confirms) take(id string) (api.Confirm, bool) {
	cs.mu.Lock()
	expired := cs.pruneLocked(time.Now())
	c := cs.all[id]
	ok := c != nil && c.state.State == "pending"
	if ok {
		c.state.State = "answering"
	}
	cs.mu.Unlock()
	if expired {
		cs.publish()
	}
	if !ok {
		return api.Confirm{}, false
	}
	return c.c, true
}

// finish ends confirmation id as st says.
func (cs *confirms) finish(id string, st api.ConfirmState) {
	cs.mu.Lock()
	c := cs.all[id]
	if c != nil && c.state.State == "answering" {
		c.state, c.ended = st, time.Now()
		close(c.done)
	}
	cs.mu.Unlock()
	cs.publish()
}

// wait is how confirmation id stands, after waiting up to d for an answer.
func (cs *confirms) wait(ctx context.Context, id string, d time.Duration) (api.ConfirmState, bool) {
	cs.mu.Lock()
	if cs.pruneLocked(time.Now()) {
		defer cs.publish()
	}
	c := cs.all[id]
	cs.mu.Unlock()
	if c == nil {
		return api.ConfirmState{}, false
	}
	if d > 0 {
		left := time.Until(time.UnixMilli(c.c.ExpiresAt))
		t := time.NewTimer(min(d, max(left, 0)+50*time.Millisecond))
		select {
		case <-c.done:
		case <-t.C:
		case <-ctx.Done():
		}
		t.Stop()
	}
	cs.mu.Lock()
	expired := cs.pruneLocked(time.Now())
	st := c.state
	cs.mu.Unlock()
	if expired {
		cs.publish()
	}
	if st.State == "answering" {
		st.State = "pending"
	}
	return st, true
}
