// Package hub carries small states to the pages' event streams (plan "实时数据（携带）"). A page
// opens one stream and names the states it wants; it gets each whole at once, then whole again
// whenever it changes, never a patch, at most once per Coalesce per state. A slow reader skips
// the states in between: it is sent only the latest. Drafts, the text an agent is writing, are the
// one thing sent as deltas, batched per DraftEvery; they are never dropped, only joined.
package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
)

const (
	Coalesce   = 250 * time.Millisecond
	DraftEvery = 100 * time.Millisecond
	Ping       = 15 * time.Second
)

type Hub struct {
	mu     sync.Mutex
	states map[string][]byte // key: JSON of its value
	subs   map[*Sub]struct{}
	// Provide, if set, makes a state no one has set yet, the first time a stream asks for it.
	Provide func(key string) (any, bool)
}

func New() *Hub { return &Hub{states: map[string][]byte{}, subs: map[*Sub]struct{}{}} }

// Set replaces state key with v, for every stream that wants it. A value equal to the last one
// is no change, and nothing is sent.
func (h *Hub) Set(key string, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("hub: state %s: %v", key, err))
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if bytes.Equal(h.states[key], b) {
		return
	}
	h.states[key] = b
	for s := range h.subs {
		s.mark(key)
	}
}

// Delete drops state key; streams that want it get null.
func (h *Hub) Delete(key string) { h.Set(key, nil) }

// Get is state key's JSON, null if there is none.
func (h *Hub) Get(key string) []byte {
	h.mu.Lock()
	b, ok := h.states[key]
	provide := h.Provide
	h.mu.Unlock()
	if ok {
		return b
	}
	if provide != nil {
		if v, ok := provide(key); ok {
			if b, err := json.Marshal(v); err == nil {
				// Kept without telling the streams: the one asking is about to send it.
				h.mu.Lock()
				if old, ok := h.states[key]; ok {
					b = old // set meanwhile
				} else {
					h.states[key] = b
				}
				h.mu.Unlock()
				return b
			}
		}
	}
	return []byte("null")
}

// Draft appends text to draft n of session's agent process proc, for the streams that want the
// session ("session:<id>").
func (h *Hub) Draft(session string, proc, n int, text string) {
	key := "session:" + session
	h.mu.Lock()
	defer h.mu.Unlock()
	for s := range h.subs {
		if s.wants[key] {
			s.draft(api.DraftEvent{Session: session, Proc: proc, N: n, Text: text})
		}
	}
}

// Sub is one stream.
type Sub struct {
	h      *Hub
	keys   []string
	wants  map[string]bool
	mu     sync.Mutex
	dirty  map[string]bool
	drafts []api.DraftEvent
	wake   chan struct{}
	// Filter, if set before Serve, gives what this stream may see of a state's JSON (a terminal
	// that sees only some sessions gets only those in "list").
	Filter func(key string, value []byte) []byte
}

// Subscribe opens a stream for keys. Every key is due at once: the whole state on connect, and
// on every reconnect, which is a new stream.
func (h *Hub) Subscribe(keys []string) *Sub {
	s := &Sub{h: h, keys: keys, wants: map[string]bool{}, dirty: map[string]bool{}, wake: make(chan struct{}, 1)}
	for _, k := range keys {
		s.wants[k] = true
		s.dirty[k] = true
	}
	h.mu.Lock()
	h.subs[s] = struct{}{}
	h.mu.Unlock()
	s.poke()
	return s
}

func (s *Sub) Close() {
	s.h.mu.Lock()
	delete(s.h.subs, s)
	s.h.mu.Unlock()
}

func (s *Sub) mark(key string) {
	if !s.wants[key] {
		return
	}
	s.mu.Lock()
	s.dirty[key] = true
	s.mu.Unlock()
	s.poke()
}

func (s *Sub) draft(d api.DraftEvent) {
	s.mu.Lock()
	if n := len(s.drafts); n > 0 && s.drafts[n-1].Session == d.Session && s.drafts[n-1].Proc == d.Proc && s.drafts[n-1].N == d.N {
		s.drafts[n-1].Text += d.Text
	} else {
		s.drafts = append(s.drafts, d)
	}
	s.mu.Unlock()
	s.poke()
}

func (s *Sub) poke() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Serve writes the stream as server-sent events until ctx ends or a write fails: "state" events
// (api.StateEvent) and "draft" events (api.DraftEvent), and a comment every Ping. flush pushes
// what was written to the reader.
func (s *Sub) Serve(ctx context.Context, w io.Writer, flush func()) error {
	sent := map[string]time.Time{}
	var drafted time.Time
	ping := time.NewTicker(Ping)
	defer ping.Stop()
	var buf strings.Builder
	for {
		now := time.Now()
		next := time.Duration(-1)
		soonest := func(d time.Duration) {
			if next < 0 || d < next {
				next = d
			}
		}
		var due []string
		var drafts []api.DraftEvent
		s.mu.Lock()
		for _, k := range s.keys {
			if !s.dirty[k] {
				continue
			}
			if wait := sent[k].Add(Coalesce).Sub(now); wait > 0 {
				soonest(wait)
				continue
			}
			delete(s.dirty, k)
			due = append(due, k)
		}
		if len(s.drafts) > 0 {
			if wait := drafted.Add(DraftEvery).Sub(now); wait > 0 {
				soonest(wait)
			} else {
				drafts, s.drafts, drafted = s.drafts, nil, now
			}
		}
		s.mu.Unlock()

		buf.Reset()
		for _, k := range due {
			// The value now, not when it was marked: anything set since is in it.
			v := s.h.Get(k)
			if s.Filter != nil {
				v = s.Filter(k, v)
			}
			fmt.Fprintf(&buf, "event: state\ndata: {\"key\":%q,\"value\":%s}\n\n", k, v)
			sent[k] = now
		}
		for _, d := range drafts {
			b, _ := json.Marshal(d)
			fmt.Fprintf(&buf, "event: draft\ndata: %s\n\n", b)
		}
		if buf.Len() > 0 {
			if _, err := io.WriteString(w, buf.String()); err != nil {
				return err
			}
			flush()
		}

		var timer <-chan time.Time
		if next >= 0 {
			timer = time.After(next)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-s.wake:
		case <-timer:
		case <-ping.C:
			if _, err := io.WriteString(w, ": ping\n\n"); err != nil {
				return err
			}
			flush()
		}
	}
}
