// Package webapp is ir_web, the built-in app: Iridium's web page on the TCP address, its own layer
// in front of the core (package gate's Wrap and the API behind it).
//
// It owns what only browsers need: passkeys (register, log in, log out), the session cookie,
// invites and the first-run setup link, the public origin with its Host and Origin checks, private
// caching, and the page itself. A browser logged in with a cookie goes on to the core as ir_web
// speaking for that login: ir_web's own TOKEN and Satori-User-ID. A request with a TOKEN of its
// own (an app, an executor, a remote ir) goes through untouched. With no public origin ir_web is
// off: no login, no cookie, and a browser on this machine is the host's page, handed to the core
// through gate.AsHost. See docs/web.md.
package webapp

import (
	"bufio"
	"context"
	"encoding/json"
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
	"github.com/kumoSleeping/iridium/internal/gate"
)

const (
	Prefix          = "/_ir/"     // ir_web's own routes
	CookieName      = "__Host-ir" // a login's session cookie
	IdleTimeout     = 12 * time.Hour
	AbsoluteTimeout = 7 * 24 * time.Hour
	InviteTTL       = 10 * time.Minute

	// Headers ir_web speaks for a login with (Satori's), set on every browser request it forwards.
	HeaderPlatform = "Satori-Platform"
	HeaderUser     = "Satori-User-ID"

	setupFile  = "setup-token"
	tokenFile  = "web-token" // ir_web's own TOKEN (0600)
	touchEvery = time.Minute
	sweepEvery = 30 * time.Second
)

// Errors.
var (
	ErrNotFound    = errors.New("webapp: no such invite")
	ErrLastPasskey = errors.New("webapp: cannot remove the last login of ir_web with a passkey")
	ErrNotPublic   = errors.New("webapp: no public origin configured")
)

// Config configures a Web.
type Config struct {
	// PublicOrigin is public.origin, e.g. "https://ir.example.com". Empty: ir_web is off (the
	// browser on this machine is the host). It must be https, or http on localhost for
	// development. Its host name is the passkey RP ID: changing it invalidates every passkey.
	PublicOrigin string

	// Core is the core's registry (ir_web's logins are rows there). Required.
	Core *gate.Gate
	// Token is ir_web's own TOKEN (AppToken). Required with a public origin.
	Token string
	// Store holds the passkeys, invites and web sessions. Required.
	Store Store

	// StateDir receives the first-run setup link, file "setup-token" (0600), while ir_web has no
	// login. Empty: no setup file (CreateInvite still works).
	StateDir string

	// Hosts adds accepted Host header values: "name" for any port, or "name:port". By default
	// the public mode accepts the public origin's host only, and the local mode loopback names.
	Hosts []string

	// ClientIPHeader carries the client address for rate limiting (as gate.Config's).
	ClientIPHeader string

	// OnJoin runs after a new login registered, before its browser hears so.
	OnJoin func(Joined)

	// Open says which requests pass with no login, as no one (gate.Config.Open's).
	Open func(*http.Request) bool

	Logger *slog.Logger
	Now    func() time.Time
}

// Joined is a login of ir_web just registered: Perms, its invite's (Invite.Perms); First, no
// other login of ir_web was there (the owner's own, by the first-run setup or the first invite).
type Joined struct {
	Endpoint gate.EndpointInfo
	Perms    []byte
	First    bool
}

// Web is ir_web: the middleware in front of the core, and the WebAuthn relying party.
type Web struct {
	cfg      Config
	core     *gate.Gate
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
	lim   *gate.Limiter

	reqMu sync.Mutex
	reqs  map[*inFlight]struct{}

	stop      chan struct{}
	done      chan struct{}
	closeOnce sync.Once
}

// inFlight is a browser request forwarded to the core: cancelled as its session ends.
type inFlight struct {
	session string // "" for the host's page
	login   string
	cancel  context.CancelFunc
}

// New opens ir_web: loads the store, drops what has expired and, with a public origin and no
// login yet, writes a fresh setup link to StateDir. Close stops its sweeper.
func New(cfg Config) (*Web, error) {
	if cfg.Store == nil || cfg.Core == nil {
		return nil, errors.New("webapp: Config.Store and Config.Core are required")
	}
	w := &Web{
		cfg: cfg, core: cfg.Core, store: cfg.Store, log: cfg.Logger, now: cfg.Now, ipHeader: cfg.ClientIPHeader,
		flows: newFlows(), lim: gate.NewLimiter(), reqs: map[*inFlight]struct{}{},
		stop: make(chan struct{}), done: make(chan struct{}),
	}
	if w.log == nil {
		w.log = slog.Default()
	}
	if w.now == nil {
		w.now = time.Now
	}
	if w.ipHeader == "" {
		w.ipHeader = "CF-Connecting-IP"
	}
	for _, h := range cfg.Hosts {
		w.hosts = append(w.hosts, strings.ToLower(h))
	}
	if cfg.PublicOrigin != "" {
		if cfg.Token == "" {
			return nil, errors.New("webapp: Config.Token is required with a public origin")
		}
		u, err := parseOrigin(cfg.PublicOrigin)
		if err != nil {
			return nil, err
		}
		w.public, w.secure = true, u.Scheme == "https"
		w.origin, w.host = u.Scheme+"://"+u.Host, u.Host
		w.wa, err = webauthn.New(&webauthn.Config{
			RPID:                  u.Hostname(),
			RPDisplayName:         "Iridium",
			RPOrigins:             []string{w.origin},
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
			return nil, fmt.Errorf("webapp: public origin: %w", err)
		}
	}
	s, err := cfg.Store.Load()
	if err != nil {
		return nil, fmt.Errorf("webapp: load: %w", err)
	}
	w.st = newMemState(s)
	w.mu.Lock()
	err = w.commitLocked(w.expiredLocked(w.now()))
	w.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if cfg.StateDir != "" {
		if err := w.ensureSetup(); err != nil {
			return nil, err
		}
	}
	go w.loop()
	return w, nil
}

// AppToken is ir_web's own TOKEN, kept in stateDir (file "web-token", 0600) and made on first
// use; core takes it as the built-in app ir_web's. With no stateDir it lives in memory only.
func AppToken(core *gate.Gate, stateDir string) (string, error) {
	var tok string
	path := ""
	if stateDir != "" {
		path = filepath.Join(stateDir, tokenFile)
		if b, err := os.ReadFile(path); err == nil {
			tok = strings.TrimSpace(string(b))
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
	if !strings.HasPrefix(tok, "ir_") || !gate.WellFormedSecret(strings.TrimPrefix(tok, "ir_")) {
		tok = "ir_" + gate.NewSecret()
		if path != "" {
			if err := os.MkdirAll(stateDir, 0o700); err != nil {
				return "", err
			}
			if err := gate.WriteFileAtomic(path, []byte(tok+"\n")); err != nil {
				return "", err
			}
		}
	}
	if err := core.SetAppToken(gate.WebApp, tok); err != nil {
		return "", err
	}
	return tok, nil
}

// Close stops the background sweeper.
func (w *Web) Close() error {
	w.closeOnce.Do(func() { close(w.stop) })
	<-w.done
	return nil
}

// Public reports whether a public origin is configured: ir_web is on.
func (w *Web) Public() bool { return w.public }

// parseOrigin accepts a bare origin: https, or http on a loopback name.
func parseOrigin(s string) (*url.URL, error) {
	u, err := url.Parse(s)
	if err != nil || u.Host == "" || u.User != nil || u.Opaque != "" || (u.Path != "" && u.Path != "/") ||
		u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("webapp: public origin %q is not an origin like https://ir.example.com", s)
	}
	u.Host = strings.ToLower(u.Host)
	switch u.Scheme {
	case "https":
		u.Host = strings.TrimSuffix(u.Host, ":443")
	case "http":
		if !loopbackName(u.Hostname()) {
			return nil, fmt.Errorf("webapp: public origin %q must be https", s)
		}
		u.Host = strings.TrimSuffix(u.Host, ":80")
	default:
		return nil, fmt.Errorf("webapp: public origin %q must be https", s)
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
func (w *Web) hostAllowed(h string) bool {
	h = strings.ToLower(h)
	if w.public {
		if h == w.host || (w.secure && h == w.host+":443") || (!w.secure && h == w.host+":80") {
			return true
		}
	} else if loopbackName(hostOnly(h)) {
		return true
	}
	for _, a := range w.hosts {
		if h == a || hostOnly(h) == a {
			return true
		}
	}
	return false
}

// originOK checks that an unsafe request or WebSocket upgrade comes from our own page.
func (w *Web) originOK(r *http.Request) bool {
	site := r.Header.Get("Sec-Fetch-Site")
	if site != "" && site != "same-origin" {
		return false
	}
	origin := r.Header.Get("Origin")
	if w.public {
		return strings.EqualFold(origin, w.origin)
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

func hasBearer(r *http.Request) bool {
	v := r.Header.Get("Authorization")
	return len(v) > 7 && strings.EqualFold(v[:7], "bearer ")
}

// toCore: the path is the core's (the API, Satori, avatars), not the page's files.
func toCore(p string) bool {
	return p == "/api" || strings.HasPrefix(p, "/api/") || p == "/v1" || strings.HasPrefix(p, "/v1/") ||
		strings.HasPrefix(p, gate.AvatarPrefix)
}

// Handler is ir_web in front of core (the gate-wrapped API) and app (the page's files). See the
// package doc for who goes where.
func (w *Web) Handler(core, app http.Handler) http.Handler {
	site := func(rw http.ResponseWriter, r *http.Request) {
		if toCore(r.URL.Path) {
			core.ServeHTTP(rw, r)
		} else {
			app.ServeHTTP(rw, r)
		}
	}
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		// An app's TOKEN is a secret no rebound page has: it may come by a loopback name (a plugin
		// on this machine) even when the public origin is the only host.
		token := hasBearer(r)
		if !w.hostAllowed(r.Host) && !(token && loopbackName(hostOnly(r.Host))) {
			writeJSON(rw, http.StatusMisdirectedRequest, apiError{"bad_host"})
			return
		}
		if (r.URL.Path == "/_ir" || strings.HasPrefix(r.URL.Path, Prefix)) && !strings.HasPrefix(r.URL.Path, gate.AvatarPrefix) {
			w.serveGate(rw, r)
			return
		}
		if token { // an app's own: untouched, to the core
			if w.public && !isUpgrade(r) {
				rw = &privateWriter{ResponseWriter: rw}
			}
			core.ServeHTTP(rw, r)
			return
		}
		// Cookies (and the host's page) are ambient credentials: a foreign page could make the
		// browser send them. On a path open to all, such a request still goes, as no one.
		crossSite := (unsafeMethod(r.Method) || isUpgrade(r)) && !w.originOK(r)
		open := w.cfg.Open != nil && w.cfg.Open(r)
		if !w.public { // ir_web off: the browser on this machine is the host's page
			switch {
			case !crossSite:
				w.forward(rw, gate.AsHost(stripped(r)), "", "", site)
			case open:
				site(rw, stripped(r))
			default:
				writeJSON(rw, http.StatusForbidden, apiError{"bad_origin"})
			}
			return
		}
		sess, ok := w.identify(r, w.now())
		switch {
		case ok && !crossSite:
			r = stripped(r)
			r.Header.Set("Authorization", "Bearer "+w.cfg.Token)
			r.Header.Set(HeaderPlatform, gate.WebApp)
			r.Header.Set(HeaderUser, sess.Login)
			if !isUpgrade(r) {
				rw = &privateWriter{ResponseWriter: rw}
			}
			w.forward(rw, r, sess.Hash, sess.Login, site)
		case open:
			site(rw, stripped(r))
		case ok:
			writeJSON(rw, http.StatusForbidden, apiError{"bad_origin"})
		default:
			w.unauthenticated(rw, r)
		}
	})
}

// stripped is r without ir_web's cookie: the core never sees one. (Satori-Platform and
// Satori-User-ID stay: the host's page names itself so; for a login ir_web sets them.)
func stripped(r *http.Request) *http.Request {
	r = r.Clone(r.Context())
	cookies := r.Cookies()
	r.Header.Del("Cookie")
	for _, c := range cookies {
		if c.Name != CookieName {
			r.AddCookie(c)
		}
	}
	r.Header.Del("Authorization")
	return r
}

// forward serves r by site under a context ir_web ends as the browser's session does (logout,
// expiry, removal) or as ir_web is replaced (CloseAll).
func (w *Web) forward(rw http.ResponseWriter, r *http.Request, session, login string, site http.HandlerFunc) {
	ctx, cancel := context.WithCancel(r.Context())
	f := &inFlight{session: session, login: login, cancel: cancel}
	w.reqMu.Lock()
	w.reqs[f] = struct{}{}
	w.reqMu.Unlock()
	defer func() {
		w.reqMu.Lock()
		delete(w.reqs, f)
		w.reqMu.Unlock()
		cancel()
	}()
	site(rw, r.WithContext(ctx))
}

// cancel ends the forwarded requests match picks.
func (w *Web) cancel(match func(*inFlight) bool) {
	w.reqMu.Lock()
	var hit []*inFlight
	for f := range w.reqs {
		if match(f) {
			hit = append(hit, f)
		}
	}
	w.reqMu.Unlock()
	for _, f := range hit {
		f.cancel()
	}
}

// CloseAll ends every browser request ir_web forwarded: it is being replaced (public.origin
// changed), and the browsers must come in again through the new one. Apps' TOKEN traffic stays.
func (w *Web) CloseAll() { w.cancel(func(*inFlight) bool { return true }) }

// identify finds the login behind r's session cookie.
func (w *Web) identify(r *http.Request, now time.Time) (Session, bool) {
	c, err := r.Cookie(CookieName)
	if err != nil || !gate.WellFormedSecret(c.Value) {
		return Session{}, false
	}
	h := gate.HashSecret(c.Value)
	w.mu.Lock()
	s, found := w.st.sessions[h]
	if !found {
		w.mu.Unlock()
		return Session{}, false
	}
	info, live := w.core.Endpoint(s.Login)
	if !live || info.Kind != gate.KindPanel || sessionExpired(s, now) {
		w.commitLocked(Change{DeleteSessions: []string{h}})
		w.mu.Unlock()
		w.cancel(func(f *inFlight) bool { return f.session == h })
		return Session{}, false
	}
	if now.Sub(s.LastSeenAt) >= touchEvery {
		s.LastSeenAt = now
		w.commitLocked(Change{Sessions: []Session{s}})
	}
	w.mu.Unlock()
	return s, true
}

func sessionExpired(s Session, now time.Time) bool {
	return !now.Before(s.ExpiresAt) || now.Sub(s.LastSeenAt) >= IdleTimeout
}

// unauthenticated sends a browser navigation to the login page, anything else a 401.
func (w *Web) unauthenticated(rw http.ResponseWriter, r *http.Request) {
	if (r.Method == http.MethodGet || r.Method == http.MethodHead) && !isUpgrade(r) &&
		(r.Header.Get("Sec-Fetch-Mode") == "navigate" || strings.Contains(r.Header.Get("Accept"), "text/html")) {
		rw.Header().Set("Cache-Control", "no-store")
		http.Redirect(rw, r, "/_ir/login?next="+url.QueryEscape(safeNext(r.URL.RequestURI())), http.StatusFound)
		return
	}
	writeJSON(rw, http.StatusUnauthorized, apiError{"unauthenticated"})
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

// commitLocked persists c, then applies it in memory. w.mu must be held.
func (w *Web) commitLocked(c Change) error {
	if c.empty() {
		return nil
	}
	if err := w.store.Apply(c); err != nil {
		w.log.Error("webapp: store", "err", err)
		return err
	}
	w.st.apply(c)
	return nil
}

func (w *Web) newSessionLocked(login string, now time.Time) (string, Session) {
	token := gate.NewSecret()
	return token, Session{
		Hash:       gate.HashSecret(token),
		Login:      login,
		CreatedAt:  now,
		LastSeenAt: now,
		ExpiresAt:  now.Add(AbsoluteTimeout),
	}
}

func (w *Web) hasPasskeyLocked() bool {
	for _, c := range w.st.creds {
		if len(c.Passkeys) > 0 {
			if _, ok := w.core.Endpoint(c.Login); ok {
				return true
			}
		}
	}
	return false
}

func (w *Web) joinURL(token string) string { return w.origin + "/_ir/join#" + token }

// setupInvitesLocked are the setup invites' IDs.
func (w *Web) setupInvitesLocked() []string {
	var ids []string
	for id, inv := range w.st.invites {
		if inv.Setup {
			ids = append(ids, id)
		}
	}
	return ids
}

// ensureSetup replaces any old setup token: a new one with a public origin while ir_web has no
// login, none after (nor with ir_web off), and then no file either.
func (w *Web) ensureSetup() error {
	path := filepath.Join(w.cfg.StateDir, setupFile)
	w.mu.Lock()
	ch := Change{DeleteInvites: w.setupInvitesLocked()}
	need := w.public && !w.core.HasWebLogin()
	var token string
	if need {
		token = gate.NewSecret()
		ch.Invites = []Invite{{ID: "setup", Hash: gate.HashSecret(token), Setup: true, CreatedAt: w.now()}}
	}
	err := w.commitLocked(ch)
	w.mu.Unlock()
	if err != nil {
		return err
	}
	if !need {
		w.removeSetupFile()
		return nil
	}
	if err := os.MkdirAll(w.cfg.StateDir, 0o700); err != nil {
		return err
	}
	if err := gate.WriteFileAtomic(path, []byte(w.joinURL(token)+"\n")); err != nil {
		return err
	}
	w.log.Info("webapp: no login yet; open the setup link to register the first one", "file", path)
	return nil
}

func (w *Web) removeSetupFile() {
	if w.cfg.StateDir != "" {
		os.Remove(filepath.Join(w.cfg.StateDir, setupFile))
	}
}

func (w *Web) loop() {
	defer close(w.done)
	t := time.NewTicker(sweepEvery)
	defer t.Stop()
	for {
		select {
		case <-w.stop:
			return
		case <-t.C:
			w.sweep()
		}
	}
}

// sweep drops expired sessions and invites and ends the requests left without a session.
func (w *Web) sweep() {
	now := w.now()
	w.mu.Lock()
	w.commitLocked(w.expiredLocked(now))
	sessions := make(map[string]bool, len(w.st.sessions))
	for h := range w.st.sessions {
		sessions[h] = true
	}
	w.mu.Unlock()
	w.cancel(func(f *inFlight) bool { return f.session != "" && !sessions[f.session] })
	w.flows.prune(now)
	w.lim.Prune(now)
}

func (w *Web) expiredLocked(now time.Time) Change {
	var ch Change
	for h, s := range w.st.sessions {
		if info, ok := w.core.Endpoint(s.Login); !ok || info.Kind != gate.KindPanel || sessionExpired(s, now) {
			ch.DeleteSessions = append(ch.DeleteSessions, h)
		}
	}
	for id, inv := range w.st.invites {
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

// apiError is a refusal: its code.
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
