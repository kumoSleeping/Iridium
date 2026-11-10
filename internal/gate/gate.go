// Package gate is Iridium's Passkey gate and endpoint (交互终端) registry.
//
// Wrap puts it in front of the web handler. Without a public origin the gate only checks
// Host and Origin, and every request is the built-in endpoint 本机. With one, every web
// request needs a panel's session cookie (from a passkey login) or a channel's bearer
// token; loopback is no exception, because cloudflared connects from loopback. The gate's
// own routes live under /_ir/. See docs/gate.md.
package gate

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

const (
	Prefix          = "/_ir/"     // the gate's routes
	CookieName      = "__Host-ir" // panel session cookie
	IdleTimeout     = 12 * time.Hour
	AbsoluteTimeout = 7 * 24 * time.Hour
	InviteTTL       = 10 * time.Minute
	// HostID is the host's storage ID (docs/next/decisions.md 4): the machine Iridium runs on, the
	// local `ir`, and the web page in local-only mode. Shown as HostName.
	HostID   = "local"
	HostName = "主机"
	// WebApp is the built-in app ir_web: a browser with a passkey is one of its logins. It is
	// there only with a public origin (decisions 3).
	WebApp = "ir_web"

	setupFile  = "setup-token"
	touchEvery = time.Minute // how often a session's or channel's last-seen time is written
	sweepEvery = 30 * time.Second
)

// Config configures a Gate.
type Config struct {
	// PublicOrigin is public.origin, e.g. "https://ir.example.com". Empty means local-only
	// mode. It must be https, or http on localhost for development. Its host name is the
	// passkey RP ID, so changing it invalidates every passkey.
	PublicOrigin string

	// Store holds endpoints, invites and sessions. Required.
	Store Store

	// StateDir receives the first-run setup link, file "setup-token" (0600), while no panel
	// has a passkey. Empty: no setup file (CreateInvite still works).
	StateDir string

	// Hosts adds accepted Host header values: "name" for any port, or "name:port". By
	// default the public mode accepts the public origin's host only, and the local mode
	// loopback names only.
	Hosts []string

	// ClientIPHeader carries the client address for rate limiting. It is trusted only from
	// loopback peers. Default "CF-Connecting-IP"; behind Caddy use what it sets (see docs).
	ClientIPHeader string

	// OnRevoke runs after an endpoint is revoked: its sessions are gone and its tracked
	// connections closed.
	OnRevoke func(endpointID string)

	// SessionToken resolves a session's own bearer token (one with its prefix, never a channel's):
	// the channel whose machine the session runs at, and the session. nil: none is taken.
	SessionToken func(token string) (endpointID, session string, ok bool)

	// OnJoin runs after a new panel registered, before its browser hears so.
	OnJoin func(Joined)

	// OnTerminals runs, on its own goroutine, after a terminal came, went, or changed what the
	// pages show of it (name, kind, avatar); not on a last-seen time alone.
	OnTerminals func()

	// Open says which requests pass without a login, as no one: executors asking to pair
	// (docs/exec.md), whose handlers check and limit them themselves; the app's icons and manifest.
	Open func(*http.Request) bool

	Logger *slog.Logger     // default slog.Default()
	Now    func() time.Time // default time.Now; for tests
}

// Gate is the middleware, the WebAuthn relying party and the endpoint registry.
type Gate struct {
	cfg      Config
	public   bool
	origin   string // "https://ir.example.com"
	host     string // its Host header value
	secure   bool   // https
	hosts    []string
	ipHeader string
	wa       *webauthn.WebAuthn
	store    Store
	log      *slog.Logger
	now      func() time.Time

	mu sync.Mutex // guards st; held across store.Apply
	st *memState

	flows *flows
	lim   *limiter

	connMu sync.Mutex
	conns  map[*conn]struct{}

	stop      chan struct{}
	done      chan struct{}
	closeOnce sync.Once
}

// Joined is a login of ir_web just registered: Perms, its invite's (Invite.Perms); First, no
// other login of ir_web was there (the owner's own, by the first-run setup or the first invite).
type Joined struct {
	Endpoint EndpointInfo
	Perms    []byte
	First    bool
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
// for ir_web, else the app's (HostID for the host).
func (id Identity) ID() string {
	if id.Login != "" {
		return id.Login
	}
	return id.App
}

// CanManage reports whether the identity may manage apps: ir_web's logins and the host. (Until
// S2's permissions: what 0.1 let panels and 本机 do.)
func (id Identity) CanManage() bool { return id.Host() || id.Web() }

// HostIdentity is the host's identity.
var HostIdentity = Identity{App: HostID, Name: HostName}

var localIdentity = HostIdentity

type ctxKey struct{}

type reqAuth struct {
	id      Identity
	session string // session hash, for panels
}

// FromContext returns the identity Wrap attached to the request context.
func FromContext(ctx context.Context) (Identity, bool) {
	a, ok := ctx.Value(ctxKey{}).(reqAuth)
	return a.id, ok
}

// NewContext attaches id, for handlers served without Wrap (the local unix socket is 本机).
func NewContext(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, ctxKey{}, reqAuth{id: id})
}

// New opens the gate: loads the store, drops what has expired and, in public mode with
// no passkey yet, writes a fresh setup link to StateDir. Close stops its sweeper.
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
		flows:    newFlows(),
		lim:      newLimiter(),
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
	for _, h := range cfg.Hosts {
		g.hosts = append(g.hosts, strings.ToLower(h))
	}
	if cfg.PublicOrigin != "" {
		u, err := parseOrigin(cfg.PublicOrigin)
		if err != nil {
			return nil, err
		}
		g.public, g.secure = true, u.Scheme == "https"
		g.origin, g.host = u.Scheme+"://"+u.Host, u.Host
		g.wa, err = webauthn.New(&webauthn.Config{
			RPID:                  u.Hostname(),
			RPDisplayName:         "Iridium",
			RPOrigins:             []string{g.origin},
			AttestationPreference: protocol.PreferNoAttestation,
			AuthenticatorSelection: protocol.AuthenticatorSelection{
				RequireResidentKey: protocol.ResidentKeyRequired(),
				ResidentKey:        protocol.ResidentKeyRequirementRequired,
				UserVerification:   protocol.VerificationRequired,
			},
			Timeouts: webauthn.TimeoutsConfig{
				Login:        webauthn.TimeoutConfig{Timeout: flowTTL},
				Registration: webauthn.TimeoutConfig{Timeout: flowTTL},
			},
			// Some passkey providers add extension outputs nobody asked for; none are used here.
			ExtensionsUnsolicitedOutputPolicy: protocol.UnsolicitedOutputPolicyIgnore,
		})
		if err != nil {
			return nil, fmt.Errorf("gate: public origin: %w", err)
		}
	}
	s, err := cfg.Store.Load()
	if err != nil {
		return nil, fmt.Errorf("gate: load: %w", err)
	}
	g.st = newMemState(s)
	g.mu.Lock()
	err = g.commitLocked(g.expiredLocked(g.now()))
	g.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if cfg.StateDir != "" {
		if err := g.ensureSetup(); err != nil {
			return nil, err
		}
	}
	go g.loop()
	return g, nil
}

// Close stops the background sweeper.
func (g *Gate) Close() error {
	g.closeOnce.Do(func() { close(g.stop) })
	<-g.done
	return nil
}

// Public reports whether a public origin is configured (passkeys required).
func (g *Gate) Public() bool { return g.public }

// parseOrigin accepts a bare origin: https, or http on a loopback name.
func parseOrigin(s string) (*url.URL, error) {
	u, err := url.Parse(s)
	if err != nil || u.Host == "" || u.User != nil || u.Opaque != "" || (u.Path != "" && u.Path != "/") ||
		u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("gate: public origin %q is not an origin like https://ir.example.com", s)
	}
	u.Host = strings.ToLower(u.Host)
	switch u.Scheme {
	case "https":
		u.Host = strings.TrimSuffix(u.Host, ":443")
	case "http":
		if !loopbackName(u.Hostname()) {
			return nil, fmt.Errorf("gate: public origin %q must be https", s)
		}
		u.Host = strings.TrimSuffix(u.Host, ":80")
	default:
		return nil, fmt.Errorf("gate: public origin %q must be https", s)
	}
	return &url.URL{Scheme: u.Scheme, Host: u.Host}, nil
}

func loopbackName(name string) bool {
	name = strings.TrimSuffix(strings.ToLower(name), ".")
	if name == "localhost" || strings.HasSuffix(name, ".localhost") {
		return true
	}
	a, err := netip.ParseAddr(strings.Trim(name, "[]"))
	return err == nil && a.Unmap().IsLoopback()
}

func hostOnly(h string) string {
	if name, _, err := net.SplitHostPort(h); err == nil {
		return name
	}
	return strings.Trim(h, "[]")
}

// hostAllowed checks the Host header, which DNS rebinding cannot forge.
func (g *Gate) hostAllowed(h string) bool {
	h = strings.ToLower(h)
	if g.public {
		if h == g.host || (g.secure && h == g.host+":443") || (!g.secure && h == g.host+":80") {
			return true
		}
	} else if loopbackName(hostOnly(h)) {
		return true
	}
	for _, a := range g.hosts {
		if h == a || hostOnly(h) == a {
			return true
		}
	}
	return false
}

// originOK checks that an unsafe request or WebSocket upgrade comes from our own page.
func (g *Gate) originOK(r *http.Request) bool {
	site := r.Header.Get("Sec-Fetch-Site")
	if site != "" && site != "same-origin" {
		return false
	}
	origin := r.Header.Get("Origin")
	if g.public {
		return strings.EqualFold(origin, g.origin)
	}
	if origin == "" {
		return true // not a browser: local-only mode trusts this machine
	}
	u, err := url.Parse(origin)
	return err == nil && u.Host != "" && strings.EqualFold(u.Host, r.Host)
}

func unsafeMethod(m string) bool {
	return m != http.MethodGet && m != http.MethodHead && m != http.MethodOptions
}

func isUpgrade(r *http.Request) bool { return r.Header.Get("Upgrade") != "" }

func bearer(r *http.Request) (string, bool) {
	v := r.Header.Get("Authorization")
	if len(v) > 7 && strings.EqualFold(v[:7], "bearer ") {
		return strings.TrimSpace(v[7:]), true
	}
	return "", false
}

// ClientKey is who r comes from, as the gate's rate limits count it.
func (g *Gate) ClientKey(r *http.Request) string { return clientKey(r, g.ipHeader) }

// Wrap returns next behind the gate. Handlers can read the caller with FromContext.
func (g *Gate) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A channel's bearer token is a secret no rebound page has: it may come by a loopback
		// name (a plugin on this machine) even when the public origin is the only host.
		_, hasToken := bearer(r)
		if !g.hostAllowed(r.Host) && !(hasToken && loopbackName(hostOnly(r.Host))) {
			writeJSON(w, http.StatusMisdirectedRequest, apiError{"bad_host"})
			return
		}
		if r.URL.Path == "/_ir" || strings.HasPrefix(r.URL.Path, Prefix) {
			g.serveGate(w, r)
			return
		}
		if g.cfg.Open != nil && !hasToken && g.cfg.Open(r) {
			next.ServeHTTP(w, r)
			return
		}
		a, ok := g.authorize(w, r)
		if !ok {
			return
		}
		r = r.WithContext(context.WithValue(r.Context(), ctxKey{}, a))
		if g.public && !isUpgrade(r) {
			w = &privateWriter{ResponseWriter: w}
		}
		next.ServeHTTP(w, r)
	})
}

// authorize identifies r and checks its origin; when it fails it has answered.
func (g *Gate) authorize(w http.ResponseWriter, r *http.Request) (reqAuth, bool) {
	now := g.now()
	key := clientKey(r, g.ipHeader)
	if _, ok := bearer(r); ok {
		if d := g.lim.wait(key, now); d > 0 {
			tooMany(w, d)
			return reqAuth{}, false
		}
	}
	a, ok, bad := g.identify(r, now)
	switch {
	case bad:
		g.lim.fail(key, now)
		g.log.Info("gate: bad bearer token", "client", key)
		writeJSON(w, http.StatusUnauthorized, apiError{"unauthenticated"})
		return reqAuth{}, false
	case !ok && g.public:
		g.unauthenticated(w, r)
		return reqAuth{}, false
	case !ok:
		a = reqAuth{id: localIdentity}
	}
	// Cookies (and 本机) are ambient credentials: a foreign page could make the browser
	// send them. Bearer tokens are not.
	if (a.session != "" || a.id.Host()) && (unsafeMethod(r.Method) || isUpgrade(r)) && !g.originOK(r) {
		writeJSON(w, http.StatusForbidden, apiError{"bad_origin"})
		return reqAuth{}, false
	}
	return a, true
}

// identify finds the endpoint behind r: a channel by its bearer token, or a panel by its
// session cookie. bad means a bearer token was presented and is not valid.
func (g *Gate) identify(r *http.Request, now time.Time) (a reqAuth, ok, bad bool) {
	if tok, has := bearer(r); has {
		if g.cfg.SessionToken != nil && strings.HasPrefix(tok, sessionTokenPrefix) {
			id, session, found := g.cfg.SessionToken(tok)
			if !found {
				return a, false, true
			}
			g.mu.Lock()
			e, live := g.st.endpoints[id]
			g.mu.Unlock()
			if !live || e.Kind != KindChannel {
				return a, false, true // its machine's app removed
			}
			who := identityOf(e)
			who.Session = session
			return reqAuth{id: who}, true, false
		}
		if !wellFormedChannel(tok) {
			return a, false, true
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
				return reqAuth{id: identityOf(e)}, true, false
			}
		}
		return a, false, true
	}
	c, err := r.Cookie(CookieName)
	if err != nil || !wellFormed(c.Value) {
		return a, false, false
	}
	h := hashToken(c.Value)
	g.mu.Lock()
	s, found := g.st.sessions[h]
	if !found {
		g.mu.Unlock()
		return a, false, false
	}
	e, live := g.st.endpoints[s.EndpointID]
	if !live || sessionExpired(s, now) {
		g.commitLocked(Change{DeleteSessions: []string{h}})
		g.mu.Unlock()
		g.closeConns(func(c *conn) bool { return c.session == h })
		return a, false, false
	}
	if now.Sub(s.LastSeenAt) >= touchEvery {
		s.LastSeenAt, e.LastSeenAt = now, now
		g.commitLocked(Change{Sessions: []Session{s}, Endpoints: []Endpoint{e}})
	}
	g.mu.Unlock()
	return reqAuth{id: identityOf(e), session: h}, true, false
}

func identityOf(e Endpoint) Identity { return infoOf(e).Identity() }

func sessionExpired(s Session, now time.Time) bool {
	return !now.Before(s.ExpiresAt) || now.Sub(s.LastSeenAt) >= IdleTimeout
}

// unauthenticated sends a browser navigation to the login page, anything else a 401.
func (g *Gate) unauthenticated(w http.ResponseWriter, r *http.Request) {
	if (r.Method == http.MethodGet || r.Method == http.MethodHead) && !isUpgrade(r) &&
		(r.Header.Get("Sec-Fetch-Mode") == "navigate" || strings.Contains(r.Header.Get("Accept"), "text/html")) {
		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, "/_ir/login?next="+url.QueryEscape(safeNext(r.URL.RequestURI())), http.StatusFound)
		return
	}
	writeJSON(w, http.StatusUnauthorized, apiError{"unauthenticated"})
}

// safeNext keeps a post-login redirect on this site and outside /_ir.
func safeNext(p string) string {
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || len(p) > 2048 ||
		p == "/_ir" || strings.HasPrefix(p, "/_ir/") || strings.HasPrefix(p, "/_ir?") {
		return "/"
	}
	for i := 0; i < len(p); i++ {
		if p[i] < 0x20 || p[i] == 0x7f || p[i] == '\\' {
			return "/"
		}
	}
	return p
}

// commitLocked persists c, then applies it in memory. g.mu must be held.
func (g *Gate) commitLocked(c Change) error {
	if c.empty() {
		return nil
	}
	shown := len(c.DeleteEndpoints) > 0
	for _, e := range c.Endpoints {
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

func (g *Gate) newSessionLocked(endpointID string, now time.Time) (string, Session) {
	token := newToken()
	return token, Session{
		Hash:       hashToken(token),
		EndpointID: endpointID,
		CreatedAt:  now,
		LastSeenAt: now,
		ExpiresAt:  now.Add(AbsoluteTimeout),
	}
}

func (g *Gate) hasPasskeyLocked() bool {
	for _, e := range g.st.endpoints {
		if e.Kind == KindPanel && len(e.Passkeys) > 0 {
			return true
		}
	}
	return false
}

// hasPanelLocked reports whether any panel is registered.
func (g *Gate) hasPanelLocked() bool {
	for _, e := range g.st.endpoints {
		if e.Kind == KindPanel {
			return true
		}
	}
	return false
}

func (g *Gate) joinURL(token string) string { return g.origin + "/_ir/join#" + token }

// setupInvitesLocked are the setup invites' IDs.
func (g *Gate) setupInvitesLocked() []string {
	var ids []string
	for id, inv := range g.st.invites {
		if inv.Setup {
			ids = append(ids, id)
		}
	}
	return ids
}

// ensureSetup replaces any old setup token: a new one in public mode while no panel is
// registered, none after (nor in local mode), and then no file either.
func (g *Gate) ensureSetup() error {
	path := filepath.Join(g.cfg.StateDir, setupFile)
	g.mu.Lock()
	ch := Change{DeleteInvites: g.setupInvitesLocked()}
	need := g.public && !g.hasPanelLocked()
	var token string
	if need {
		token = newToken()
		ch.Invites = []Invite{{ID: "setup", Hash: hashToken(token), Setup: true, CreatedAt: g.now()}}
	}
	err := g.commitLocked(ch)
	g.mu.Unlock()
	if err != nil {
		return err
	}
	if !need {
		g.removeSetupFile()
		return nil
	}
	if err := os.MkdirAll(g.cfg.StateDir, 0o700); err != nil {
		return err
	}
	if err := writeFileAtomic(path, []byte(g.joinURL(token)+"\n")); err != nil {
		return err
	}
	g.log.Info("gate: no passkey yet; open the setup link to register the first panel", "file", path)
	return nil
}

func (g *Gate) removeSetupFile() {
	if g.cfg.StateDir != "" {
		os.Remove(filepath.Join(g.cfg.StateDir, setupFile))
	}
}

// conn is a tracked long-lived connection.
type conn struct {
	session  string // closes when this session ends
	endpoint string // closes when this endpoint is revoked
	close    func()
}

// Track registers a long-lived connection (WebSocket, event stream) of request r, which
// must have come through Wrap. closeFn is called once, from another goroutine, when the
// connection's session ends (logout, expiry) or its endpoint is revoked. Call untrack
// when the connection ends by itself.
func (g *Gate) Track(r *http.Request, closeFn func()) (untrack func()) {
	a, _ := r.Context().Value(ctxKey{}).(reqAuth)
	c := &conn{session: a.session, endpoint: a.id.ID(), close: sync.OnceFunc(closeFn)}
	if a.id.Host() {
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

// CloseAll closes every tracked connection: the gate is being replaced (public.origin changed),
// and what it let in must come in again through the new one.
func (g *Gate) CloseAll() { g.closeConns(func(*conn) bool { return true }) }

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

// sweep drops expired sessions and invites and closes connections left without a
// session or endpoint.
func (g *Gate) sweep() {
	now := g.now()
	g.mu.Lock()
	g.commitLocked(g.expiredLocked(now))
	sessions := make(map[string]bool, len(g.st.sessions))
	for h := range g.st.sessions {
		sessions[h] = true
	}
	endpoints := make(map[string]bool, len(g.st.endpoints))
	for id := range g.st.endpoints {
		endpoints[id] = true
	}
	g.mu.Unlock()
	g.closeConns(func(c *conn) bool {
		if c.session != "" {
			return !sessions[c.session]
		}
		return c.endpoint != "" && !endpoints[c.endpoint]
	})
	g.flows.prune(now)
	g.lim.prune(now)
}

func (g *Gate) expiredLocked(now time.Time) Change {
	var ch Change
	for h, s := range g.st.sessions {
		if _, ok := g.st.endpoints[s.EndpointID]; !ok || sessionExpired(s, now) {
			ch.DeleteSessions = append(ch.DeleteSessions, h)
		}
	}
	for id, inv := range g.st.invites {
		if !inv.Setup && !now.Before(inv.ExpiresAt) {
			ch.DeleteInvites = append(ch.DeleteInvites, id)
		}
	}
	return ch
}

// privateWriter keeps shared caches (Cloudflare caches by file extension) from storing
// authenticated responses: Cache-Control always says private.
type privateWriter struct {
	http.ResponseWriter
	fixed bool
}

func (p *privateWriter) fix() {
	if !p.fixed {
		p.fixed = true
		h := p.ResponseWriter.Header()
		h.Set("Cache-Control", privateCacheControl(h.Get("Cache-Control")))
	}
}

func (p *privateWriter) WriteHeader(code int) {
	if code >= 200 {
		p.fix()
	}
	p.ResponseWriter.WriteHeader(code)
}

func (p *privateWriter) Write(b []byte) (int, error) { p.fix(); return p.ResponseWriter.Write(b) }

func (p *privateWriter) ReadFrom(r io.Reader) (int64, error) {
	p.fix()
	return io.Copy(p.ResponseWriter, r)
}

func (p *privateWriter) Flush() {
	p.fix()
	http.NewResponseController(p.ResponseWriter).Flush()
}

func (p *privateWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(p.ResponseWriter).Hijack()
}

func (p *privateWriter) Unwrap() http.ResponseWriter { return p.ResponseWriter }

// privateCacheControl drops public and s-maxage and adds private; with none set, the
// browser must revalidate.
func privateCacheControl(v string) string {
	if strings.TrimSpace(v) == "" {
		return "private, no-cache"
	}
	out := []string{"private"}
	for _, d := range strings.Split(v, ",") {
		d = strings.TrimSpace(d)
		name := strings.ToLower(d)
		if i := strings.IndexByte(name, '='); i >= 0 {
			name = name[:i]
		}
		if d == "" || name == "public" || name == "private" || name == "s-maxage" {
			continue
		}
		out = append(out, d)
	}
	return strings.Join(out, ", ")
}

type apiError struct {
	Error string `json:"error"`
}

func tooMany(w http.ResponseWriter, d time.Duration) {
	w.Header().Set("Retry-After", strconv.Itoa(int((d+time.Second-1)/time.Second)))
	writeJSON(w, http.StatusTooManyRequests, apiError{"rate_limited"})
}
