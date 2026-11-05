package gate

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
)

// Failure limits, per client: this many failures within the window lock it out.
const (
	maxFailures   = 10
	failureWindow = 15 * time.Minute
	lockout       = 15 * time.Minute
	maxBuckets    = 10000
)

// limiter counts failures (bad tokens, failed ceremonies) per client key.
type limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	start  time.Time
	fails  int
	locked time.Time // locked until
}

func newLimiter() *limiter { return &limiter{buckets: map[string]*bucket{}} }

// wait returns how long key must wait before trying again; 0 if it may try now.
func (l *limiter) wait(key string, now time.Time) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.buckets[key]
	if b == nil {
		return 0
	}
	if now.Before(b.locked) {
		return b.locked.Sub(now)
	}
	if now.Sub(b.start) > failureWindow {
		delete(l.buckets, key)
	}
	return 0
}

func (l *limiter) fail(key string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.buckets[key]
	if b == nil && len(l.buckets) >= maxBuckets {
		l.pruneLocked(now)
		if len(l.buckets) >= maxBuckets {
			return
		}
	}
	if b == nil || now.Sub(b.start) > failureWindow && !now.Before(b.locked) {
		b = &bucket{start: now} // a new window
		l.buckets[key] = b
	}
	b.fails++
	if b.fails >= maxFailures {
		b.locked = now.Add(lockout)
	}
}

func (l *limiter) success(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.buckets, key)
}

func (l *limiter) prune(now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneLocked(now)
}

// pruneLocked drops finished buckets and, if still full, any unlocked ones.
func (l *limiter) pruneLocked(now time.Time) {
	for k, b := range l.buckets {
		if now.Sub(b.start) > failureWindow && !now.Before(b.locked) {
			delete(l.buckets, k)
		}
	}
	for k, b := range l.buckets {
		if len(l.buckets) < maxBuckets {
			break
		}
		if !now.Before(b.locked) {
			delete(l.buckets, k)
		}
	}
}

// clientKey identifies the client for rate limiting. The forwarded-IP header is trusted
// only when the peer is loopback (cloudflared or a reverse proxy on this machine). IPv6
// clients are grouped by /64, which one client can cycle through freely.
func clientKey(r *http.Request, header string) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return "peer:" + host
	}
	addr = addr.Unmap()
	if addr.IsLoopback() && header != "" {
		if v := r.Header.Get(header); v != "" {
			if i := strings.LastIndexByte(v, ','); i >= 0 { // X-Forwarded-For style: the last hop is the proxy's
				v = v[i+1:]
			}
			if a, err := netip.ParseAddr(strings.TrimSpace(v)); err == nil {
				addr = a.Unmap()
			}
		}
	}
	if addr.Is6() {
		p, _ := addr.Prefix(64)
		return p.String()
	}
	return addr.String()
}

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
	id := newToken()
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
