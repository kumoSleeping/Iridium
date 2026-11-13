package gate

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"
)

// Failure limits, per client: this many failures within the window make it wait a minute. They
// slow a client that keeps failing; they are no defence of their own (a TOKEN is 256 random bits, a
// Passkey a signature), so a client that retries now and then never meets them.
const (
	maxFailures   = 10
	failureWindow = time.Minute
	lockout       = time.Minute
	maxBuckets    = 10000
)

// Limiter counts failures (bad tokens, failed ceremonies) per client key; package webapp has one
// of its own for ir_web's ceremonies.
type Limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	start  time.Time
	fails  int
	locked time.Time // locked until
}

// NewLimiter is an empty Limiter.
func NewLimiter() *Limiter { return &Limiter{buckets: map[string]*bucket{}} }

// wait returns how long key must wait before trying again; 0 if it may try now.
func (l *Limiter) Wait(key string, now time.Time) time.Duration {
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

func (l *Limiter) Fail(key string, now time.Time) {
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

func (l *Limiter) Success(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.buckets, key)
}

func (l *Limiter) Prune(now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneLocked(now)
}

// pruneLocked drops finished buckets and, if still full, any unlocked ones.
func (l *Limiter) pruneLocked(now time.Time) {
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

// ClientKey identifies the client for rate limiting. The forwarded-IP header is trusted
// only when the peer is loopback (cloudflared or a reverse proxy on this machine). IPv6
// clients are grouped by /64, which one client can cycle through freely.
func ClientKey(r *http.Request, header string) string {
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
