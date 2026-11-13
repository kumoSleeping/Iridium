// Package gate is the core's door: who a request on the TCP address is, by TOKEN alone, and the
// registry of apps and ir_web's logins (names, avatars, revocation, tracked connections).
//
// Wrap identifies an app by its TOKEN (Authorization: Bearer ir_…), an agent's ir at an app's
// machine by its session's TOKEN (irs_…), and the host only through the one in-process trust edge
// (AsHost). It knows no cookie, passkey, invite or public origin: those are ir_web's, package
// webapp, which stands in front of it on the TCP address. See docs/gate.md and docs/web.md.
package gate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// HostID is the host's storage ID (docs/next/decisions.md 4): the machine Iridium runs on, the
	// local `ir`, and the web page in local-only mode. Shown as HostName.
	HostID   = "local"
	HostName = "主机"
	// WebApp is the built-in app ir_web (package webapp): an app row of its own, with a TOKEN; a
	// browser with a passkey is one of its logins. It speaks for a login with Satori-User-ID.
	WebApp = "ir_web"
	// AvatarPrefix is where avatars are served (to anyone identified).
	AvatarPrefix = "/_ir/avatar/"

	touchEvery = time.Minute // how often an app's or login's last-seen time is written
	sweepEvery = 30 * time.Second
	maxBody    = 64 << 10
)

// Config configures a Gate.
type Config struct {
	// Store holds the apps and ir_web's logins. Required.
	Store Store

	// ClientIPHeader carries the client address for rate limiting. It is trusted only from
	// loopback peers. Default "CF-Connecting-IP"; behind Caddy use what it sets (see docs).
	ClientIPHeader string

	// OnRevoke runs after an app or login of ir_web is removed and its tracked connections closed.
	OnRevoke func(endpointID string)

	// SessionToken resolves a session's own bearer token (one with its prefix, never a channel's):
	// the channel whose machine the session runs at, and the session. nil: none is taken.
	SessionToken func(token string) (endpointID, session string, ok bool)

	// OnTerminals runs, on its own goroutine, after a terminal came, went, or changed what the
	// pages show of it (name, kind, avatar); not on a last-seen time alone.
	OnTerminals func()

	// Open says which requests pass with no TOKEN, as no one: executors asking to pair
	// (docs/exec.md), whose handlers check and limit them themselves; Satori's /v1/events, whose
	// IDENTIFY carries the TOKEN; the app's icons and manifest.
	Open func(*http.Request) bool

	// LoginNameTaken: a name ir_web holds for a login yet to join (an invite's), which a login
	// renamed may not take. nil: none. Called without the gate's lock (ir_web calls in holding its own).
	LoginNameTaken func(name string) bool

	Logger *slog.Logger     // default slog.Default()
	Now    func() time.Time // default time.Now; for tests
}

// Gate is the core's middleware and the registry of apps and ir_web's logins.
type Gate struct {
	cfg      Config
	ipHeader string
	store    Store
	log      *slog.Logger
	now      func() time.Time

	mu sync.Mutex // guards st; held across store.Apply
	st *memState

	lim *Limiter

	connMu sync.Mutex
	conns  map[*conn]struct{}

	stop      chan struct{}
	done      chan struct{}
	closeOnce sync.Once
}

// Identity is who sent a request, as Wrap found it: an app, and the login within it that spoke.
type Identity struct {
	// App: an app's ID (its TOKEN), WebApp (a browser with a passkey), or HostID (the host).
	App string
	// Login: the login within App; for WebApp the browser's (a former panel's ID). "" for an
	// app's TOKEN and for the host.
	Login string
	Name  string // the login's name, else the app's
	// Session: the request came with a session's own TOKEN (Config.SessionToken): the ir of an
	// agent hosted at that app's machine. It acts as that app, for that session.
	Session string
}

// Host: the host (the local ir, or the page with no public origin); full rights.
func (id Identity) Host() bool { return id.App == HostID }

// Web: a login of ir_web, a browser with a passkey.
func (id Identity) Web() bool { return id.App == WebApp && id.Login != "" }

// ID is the identity's storage ID, which permissions, usage and openers are kept by: the login's
// for ir_web, else the app's (HostID for the host). Another app's login counts as its app till S2.
func (id Identity) ID() string {
	if id.Web() {
		return id.Login
	}
	return id.App
}

// CanManage reports whether the identity may manage apps: ir_web's logins and the host. (Until
// S2's permissions: what 0.1 let panels and 本机 do.) An agent's ir has the identity of the host
// or of its session's opener: the server keeps it from approving (decision 15).
func (id Identity) CanManage() bool { return id.Host() || id.Web() }

// HostIdentity is the host's identity.
var HostIdentity = Identity{App: HostID, Name: HostName}

type ctxKey struct{}

// FromContext returns the identity Wrap attached to the request context.
func FromContext(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(ctxKey{}).(Identity)
	return id, ok
}

// NewContext attaches id: the unix socket's handler (the host, or an agent's session's opener),
// and the server as it narrows an app to the login it speaks as.
func NewContext(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

type hostEdge struct{}

// AsHost is the one trust edge into the core from the network side, in-process only: package
// webapp, with ir_web off (no public origin), hands over a browser on this machine whose Host and
// Origin it checked (against DNS rebinding) as the host. Nothing a request carries can set it.
func AsHost(r *http.Request) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), hostEdge{}, true))
}

func asHost(r *http.Request) bool { v, _ := r.Context().Value(hostEdge{}).(bool); return v }

// New opens the gate: loads the store. Close stops its sweeper.
func New(cfg Config) (*Gate, error) {
	if cfg.Store == nil {
		return nil, errors.New("gate: Config.Store is required")
	}
	g := &Gate{
		cfg:      cfg,
		store:    cfg.Store,
		log:      cfg.Logger,
		now:      cfg.Now,
		ipHeader: cfg.ClientIPHeader,
		lim:      NewLimiter(),
		conns:    map[*conn]struct{}{},
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
	if g.log == nil {
		g.log = slog.Default()
	}
	if g.now == nil {
		g.now = time.Now
	}
	if g.ipHeader == "" {
		g.ipHeader = "CF-Connecting-IP"
	}
	s, err := cfg.Store.Load()
	if err != nil {
		return nil, fmt.Errorf("gate: load: %w", err)
	}
	g.st = newMemState(s)
	go g.loop()
	return g, nil
}

// Close stops the background sweeper.
func (g *Gate) Close() error {
	g.closeOnce.Do(func() { close(g.stop) })
	<-g.done
	return nil
}

func bearer(r *http.Request) (string, bool) {
	v := r.Header.Get("Authorization")
	if len(v) > 7 && strings.EqualFold(v[:7], "bearer ") {
		return strings.TrimSpace(v[7:]), true
	}
	return "", false
}

// ClientKey is who r comes from, as the gate's rate limits count it.
func (g *Gate) ClientKey(r *http.Request) string { return ClientKey(r, g.ipHeader) }

// Wrap returns next behind the gate: a request with a TOKEN is its app (or its session's), one
// handed over by AsHost the host, one Config.Open lets in no one; the rest 401. Handlers read the
// caller with FromContext. Avatars (AvatarPrefix) are served here to anyone identified.
func (g *Gate) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var id Identity
		switch _, hasToken := bearer(r); {
		case hasToken:
			var ok bool
			if id, ok = g.authorize(w, r); !ok {
				return
			}
		case asHost(r):
			id = HostIdentity
		case g.cfg.Open != nil && g.cfg.Open(r):
			next.ServeHTTP(w, r)
			return
		default:
			writeJSON(w, http.StatusUnauthorized, apiError{"unauthenticated"})
			return
		}
		r = r.WithContext(NewContext(r.Context(), id))
		if strings.HasPrefix(r.URL.Path, AvatarPrefix) && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
			g.serveAvatar(w, r, strings.TrimPrefix(r.URL.Path, AvatarPrefix))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// authorize identifies r by its bearer TOKEN; when it fails it has answered.
func (g *Gate) authorize(w http.ResponseWriter, r *http.Request) (Identity, bool) {
	now := g.now()
	// A good TOKEN always passes: the limit is on failures, and others' at the same address (a
	// shared NAT, ir_web's forwarded browsers) must not shut it out.
	id, ok := g.identify(r, now)
	if ok {
		return id, true
	}
	key := ClientKey(r, g.ipHeader)
	if d := g.lim.Wait(key, now); d > 0 {
		tooMany(w, d)
		return Identity{}, false
	}
	g.lim.Fail(key, now)
	g.log.Info("gate: bad bearer token", "client", key)
	writeJSON(w, http.StatusUnauthorized, apiError{"unauthenticated"})
	return Identity{}, false
}

// identify finds the app behind r's bearer TOKEN, or the session whose own TOKEN it is.
func (g *Gate) identify(r *http.Request, now time.Time) (Identity, bool) {
	tok, _ := bearer(r)
	if g.cfg.SessionToken != nil && strings.HasPrefix(tok, sessionTokenPrefix) {
		id, session, found := g.cfg.SessionToken(tok)
		if !found {
			return Identity{}, false
		}
		g.mu.Lock()
		e, live := g.st.endpoints[id]
		g.mu.Unlock()
		if !live || e.Kind != KindChannel || e.ID == WebApp {
			return Identity{}, false // its machine's app removed
		}
		who := identityOf(e)
		who.Session = session
		return who, true
	}
	if !wellFormedChannel(tok) {
		return Identity{}, false
	}
	h := hashToken(tok)
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, e := range g.st.endpoints {
		if e.Kind == KindChannel && e.TokenHash != "" && hashEqual(e.TokenHash, h) {
			if now.Sub(e.LastSeenAt) >= touchEvery {
				e.LastSeenAt = now
				g.commitLocked(Change{Endpoints: []Endpoint{e}})
			}
			return identityOf(e), true
		}
	}
	return Identity{}, false
}

func identityOf(e Endpoint) Identity { return infoOf(e).Identity() }

// commitLocked persists c, then applies it in memory. g.mu must be held.
func (g *Gate) commitLocked(c Change) error {
	if c.empty() {
		return nil
	}
	shown := len(c.DeleteEndpoints) > 0
	for _, e := range c.Endpoints {
		if e.ID == WebApp {
			continue // never listed
		}
		old, ok := g.st.endpoints[e.ID]
		shown = shown || !ok || old.Name != e.Name || old.Kind != e.Kind || old.AvatarVersion != e.AvatarVersion
	}
	if err := g.store.Apply(c); err != nil {
		g.log.Error("gate: store", "err", err)
		return err
	}
	g.st.apply(c)
	if shown && g.cfg.OnTerminals != nil {
		go g.cfg.OnTerminals()
	}
	return nil
}

// hasWebLoginLocked reports whether any login of ir_web is registered.
func (g *Gate) hasWebLoginLocked() bool {
	for _, e := range g.st.endpoints {
		if e.Kind == KindPanel {
			return true
		}
	}
	return false
}

// conn is a tracked long-lived connection.
type conn struct {
	endpoint string // closes when this app or login is revoked
	close    func()
}

// Track registers a long-lived connection (WebSocket, event stream) of request r, which
// must have come through Wrap. closeFn is called once, from another goroutine, when its app or
// login is revoked. Call untrack when the connection ends by itself. (A browser's connection
// also ends as it logs out: package webapp cancels the request's context.)
func (g *Gate) Track(r *http.Request, closeFn func()) (untrack func()) {
	id, _ := FromContext(r.Context())
	c := &conn{endpoint: id.ID(), close: sync.OnceFunc(closeFn)}
	if id.Host() {
		c.endpoint = "" // the host is never removed
	}
	g.connMu.Lock()
	g.conns[c] = struct{}{}
	g.connMu.Unlock()
	return func() {
		g.connMu.Lock()
		delete(g.conns, c)
		g.connMu.Unlock()
	}
}

func (g *Gate) closeConns(match func(*conn) bool) {
	g.connMu.Lock()
	var hit []*conn
	for c := range g.conns {
		if match(c) {
			hit = append(hit, c)
			delete(g.conns, c)
		}
	}
	g.connMu.Unlock()
	for _, c := range hit {
		c.close()
	}
}

func (g *Gate) loop() {
	defer close(g.done)
	t := time.NewTicker(sweepEvery)
	defer t.Stop()
	for {
		select {
		case <-g.stop:
			return
		case <-t.C:
			g.sweep()
		}
	}
}

// sweep closes connections left without an app or login and forgets old failures.
func (g *Gate) sweep() {
	now := g.now()
	g.mu.Lock()
	endpoints := make(map[string]bool, len(g.st.endpoints))
	for id := range g.st.endpoints {
		endpoints[id] = true
	}
	g.mu.Unlock()
	g.closeConns(func(c *conn) bool { return c.endpoint != "" && !endpoints[c.endpoint] })
	g.lim.Prune(now)
}

// apiError is a refusal: its code (decision 8: 401 unauthenticated only for a TOKEN or login
// that is not valid; the rest 403 or other statuses with a code).
type apiError struct {
	Code string `json:"code"`
}

func tooMany(w http.ResponseWriter, d time.Duration) {
	w.Header().Set("Retry-After", strconv.Itoa(int((d+time.Second-1)/time.Second)))
	writeJSON(w, http.StatusTooManyRequests, apiError{"rate_limited"})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	if h.Get("Cache-Control") == "" {
		h.Set("Cache-Control", "no-store")
	}
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
