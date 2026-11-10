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

// NeedTTL is how long a need waits for a person; KeepAnswered how long its outcome stays for the
// asker to read.
const (
	NeedTTL      = 10 * time.Minute
	KeepAnswered = 10 * time.Minute
)

// needs are what waits for a person (k_glossary "需要"): apps asking to pair or for other
// permissions, logins speaking the first time, and what agents' ir asked for that a person must
// give (plan "ir": the token never enters the agent's conversation). Kept in memory: a restart
// drops them, and the asker sees expired (a login asks again as it next speaks).
type needs struct {
	hub *hub.Hub
	mu  sync.Mutex
	all map[string]*need
}

type need struct {
	c     api.Need
	state api.NeedState
	done  chan struct{} // closed once answered or expired
	ended time.Time
}

func newNeeds(h *hub.Hub) *needs {
	cs := &needs{hub: h, all: map[string]*need{}}
	cs.publish()
	return cs
}

func (cs *needs) add(kind api.NeedKind, name string, owner bool, asker, session string) api.Need {
	return cs.addNeed(api.Need{Kind: kind, Name: name, Owner: owner, Asker: asker, Session: session})
}

// addNeed adds c, given its ID and times.
func (cs *needs) addNeed(c api.Need) api.Need {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	now := time.Now()
	c.ID, c.CreatedAt, c.ExpiresAt = hex.EncodeToString(b), now.UnixMilli(), now.Add(NeedTTL).UnixMilli()
	cs.mu.Lock()
	cs.pruneLocked(now)
	cs.all[c.ID] = &need{c: c, state: api.NeedState{ID: c.ID, Kind: c.Kind, State: "pending", Name: c.Name, Owner: c.Owner}, done: make(chan struct{})}
	cs.mu.Unlock()
	cs.publish()
	return c
}

// pruneLocked expires what waited too long and forgets what was answered long ago.
func (cs *needs) pruneLocked(now time.Time) bool {
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

func (cs *needs) pending() api.Needs {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	out := api.Needs{Pending: []api.Need{}}
	for _, c := range cs.all {
		if c.state.State == "pending" {
			out.Pending = append(out.Pending, c.c)
		}
	}
	slices.SortFunc(out.Pending, func(a, b api.Need) int { return int(a.CreatedAt - b.CreatedAt) })
	return out
}

func (cs *needs) publish() { cs.hub.Set("needs", cs.pending()) }

// peek is pending need id, unclaimed.
func (cs *needs) peek(id string) (api.Need, bool) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	c := cs.all[id]
	if c == nil || c.state.State != "pending" || time.Now().UnixMilli() >= c.c.ExpiresAt {
		return api.Need{}, false
	}
	return c.c, true
}

// take claims pending need id for answering: no one else answers it meanwhile.
func (cs *needs) take(id string) (api.Need, bool) {
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
		return api.Need{}, false
	}
	return c.c, true
}

// finish ends need id as st says.
func (cs *needs) finish(id string, st api.NeedState) {
	cs.mu.Lock()
	c := cs.all[id]
	if c != nil && c.state.State == "answering" {
		c.state, c.ended = st, time.Now()
		close(c.done)
	}
	cs.mu.Unlock()
	cs.publish()
}

// wait is how need id stands, after waiting up to d for an answer.
func (cs *needs) wait(ctx context.Context, id string, d time.Duration) (api.NeedState, bool) {
	cs.mu.Lock()
	if cs.pruneLocked(time.Now()) {
		defer cs.publish()
	}
	c := cs.all[id]
	cs.mu.Unlock()
	if c == nil {
		return api.NeedState{}, false
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

// find is the first pending need match takes.
func (cs *needs) find(match func(api.Need) bool) (api.Need, bool) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	now := time.Now().UnixMilli()
	for _, c := range cs.all {
		if c.state.State == "pending" && now < c.c.ExpiresAt && match(c.c) {
			return c.c, true
		}
	}
	return api.Need{}, false
}

// count is how many pending needs match takes.
func (cs *needs) count(match func(api.Need) bool) int {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	n, now := 0, time.Now().UnixMilli()
	for _, c := range cs.all {
		if c.state.State == "pending" && now < c.c.ExpiresAt && match(c.c) {
			n++
		}
	}
	return n
}

// drop forgets pending need id: what asked it asked again.
func (cs *needs) drop(id string) {
	cs.mu.Lock()
	if c := cs.all[id]; c != nil && c.state.State == "pending" {
		c.state.State, c.ended = "expired", time.Now()
		close(c.done)
	}
	cs.mu.Unlock()
	cs.publish()
}

// addUnless adds c unless a pending need matches: that one, and false.
func (cs *needs) addUnless(match func(api.Need) bool, c api.Need) (api.Need, bool) {
	cs.mu.Lock()
	now := time.Now().UnixMilli()
	for _, o := range cs.all {
		if o.state.State == "pending" && now < o.c.ExpiresAt && match(o.c) {
			cs.mu.Unlock()
			return o.c, false
		}
	}
	cs.mu.Unlock()
	return cs.addNeed(c), true
}
