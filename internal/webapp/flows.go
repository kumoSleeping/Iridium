package webapp

import (
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/kumoSleeping/iridium/internal/gate"
)

// flowTTL bounds a WebAuthn ceremony, from options to verify.
const (
	flowTTL  = 5 * time.Minute
	maxFlows = 1000
)

type flowKind int

const (
	flowJoin flowKind = iota + 1
	flowLogin
)

// flow is one pending ceremony. Its ID goes to the page; it is taken once.
type flow struct {
	kind     flowKind
	session  *webauthn.SessionData
	expires  time.Time
	inviteID string // join
	name     string // join
	handle   []byte // join: the new endpoint's user handle
}

type flows struct {
	mu sync.Mutex
	m  map[string]*flow
}

func newFlows() *flows { return &flows{m: map[string]*flow{}} }

func (f *flows) put(fl *flow, now time.Time) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.m) >= maxFlows {
		f.pruneLocked(now)
	}
	for len(f.m) >= maxFlows { // still full: drop the oldest
		var oldest string
		for id, x := range f.m {
			if oldest == "" || x.expires.Before(f.m[oldest].expires) {
				oldest = id
			}
		}
		delete(f.m, oldest)
	}
	fl.expires = now.Add(flowTTL)
	id := gate.NewSecret()
	f.m[id] = fl
	return id
}

// take removes and returns the flow if it exists, is of the kind and has not expired.
func (f *flows) take(id string, kind flowKind, now time.Time) *flow {
	f.mu.Lock()
	defer f.mu.Unlock()
	fl := f.m[id]
	if fl == nil {
		return nil
	}
	delete(f.m, id)
	if fl.kind != kind || !now.Before(fl.expires) {
		return nil
	}
	return fl
}

func (f *flows) prune(now time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pruneLocked(now)
}

func (f *flows) pruneLocked(now time.Time) {
	for id, fl := range f.m {
		if !now.Before(fl.expires) {
			delete(f.m, id)
		}
	}
}
