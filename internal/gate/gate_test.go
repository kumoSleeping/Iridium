package gate

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const testOrigin = "https://ir.example.com"

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// harness runs a gate in front of an app that echoes the caller's identity.
type harness struct {
	t       *testing.T
	g       *Gate
	h       http.Handler
	clock   *clock
	logs    *bytes.Buffer
	dir     string
	store   *JSONStore
	cookie  string // the browser's session cookie value
	secrets []string
	revoked []string
}

type appReply struct {
	Identity
	Method string
}

func newHarness(t *testing.T, origin string) *harness {
	t.Helper()
	h := &harness{t: t, clock: &clock{t: time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)}, logs: &bytes.Buffer{}, dir: t.TempDir()}
	h.open(origin)
	return h
}

func (h *harness) open(origin string) {
	h.t.Helper()
	if h.g != nil {
		h.g.Close()
	}
	store, err := OpenJSONStore(filepath.Join(h.dir, "state", "gate.json"))
	if err != nil {
		h.t.Fatal(err)
	}
	h.store = store
	g, err := New(Config{
		PublicOrigin: origin,
		Store:        store,
		StateDir:     filepath.Join(h.dir, "state"),
		Logger:       slog.New(slog.NewTextHandler(h.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		Now:          h.clock.Now,
		OnRevoke:     func(id string) { h.revoked = append(h.revoked, id) },
	})
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() { g.Close() })
	h.g = g
	h.h = g.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := FromContext(r.Context())
		if !ok {
			http.Error(w, "no identity", 500)
			return
		}
		if r.URL.Path == "/api/me/profile" {
			g.ServeProfile(w, r, "")
			return
		}
		if rest, ok := strings.CutPrefix(r.URL.Path, "/api/endpoints/"); ok {
			g.ServeProfile(w, r, strings.TrimSuffix(rest, "/profile"))
			return
		}
		w.Header().Set("Cache-Control", "public, max-age=60")
		json.NewEncoder(w).Encode(appReply{id, r.Method})
	}))
}

type req struct {
	method, path string
	body         any
	header       map[string]string
	remote       string
	noCookie     bool
}

// do sends a request as the browser on the public origin would.
func (h *harness) do(q req) *httptest.ResponseRecorder {
	h.t.Helper()
	if q.method == "" {
		q.method = http.MethodGet
	}
	var body io.Reader
	switch b := q.body.(type) {
	case nil:
	case []byte:
		body = bytes.NewReader(b)
	default:
		data, _ := json.Marshal(b)
		body = bytes.NewReader(data)
	}
	r := httptest.NewRequest(q.method, q.path, body)
	r.Host = "ir.example.com"
	r.RemoteAddr = "198.51.100.7:40000"
	if q.remote != "" {
		r.RemoteAddr = q.remote
	}
	if q.method != http.MethodGet && q.method != http.MethodHead {
		r.Header.Set("Origin", testOrigin)
		r.Header.Set("Sec-Fetch-Site", "same-origin")
		if q.body != nil {
			r.Header.Set("Content-Type", "application/json")
		}
	}
	if h.cookie != "" && !q.noCookie {
		r.AddCookie(&http.Cookie{Name: CookieName, Value: h.cookie})
	}
	for k, v := range q.header {
		if v == "" {
			r.Header.Del(k)
		} else {
			r.Header.Set(k, v)
		}
	}
	w := httptest.NewRecorder()
	h.h.ServeHTTP(w, r)
	for _, c := range w.Result().Cookies() {
		if c.Name == CookieName {
			h.cookie = c.Value
			if c.Value != "" {
				h.secrets = append(h.secrets, c.Value)
			}
		}
	}
	return w
}

func decode[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %q: %v", w.Body.String(), err)
	}
	return v
}

func wantStatus(t *testing.T, w *httptest.ResponseRecorder, code int) {
	t.Helper()
	if w.Code != code {
		t.Fatalf("status %d, want %d: %s", w.Code, code, w.Body.String())
	}
}

// tokenOf takes the secret out of a join link.
func tokenOf(t *testing.T, link string) string {
	t.Helper()
	_, token, ok := strings.Cut(link, "/_ir/join#")
	if !ok || !strings.HasPrefix(link, testOrigin) {
		t.Fatalf("bad join link")
	}
	return token
}

func (h *harness) setupToken() string {
	h.t.Helper()
	path := filepath.Join(h.dir, "state", setupFile)
	fi, err := os.Stat(path)
	if err != nil {
		h.t.Fatalf("setup file: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		h.t.Fatalf("setup file mode %v", fi.Mode().Perm())
	}
	data, _ := os.ReadFile(path)
	tok := tokenOf(h.t, strings.TrimSpace(string(data)))
	h.secrets = append(h.secrets, tok)
	return tok
}

type ceremonyReply struct {
	Flow    string          `json:"flow"`
	Options json.RawMessage `json:"options"`
}

// join registers a panel through the join page's API calls.
func (h *harness) join(a *authenticator, token string, name *string) *httptest.ResponseRecorder {
	h.t.Helper()
	w := h.do(req{method: "POST", path: "/_ir/api/join/options", body: joinRequest{Token: token, Name: name}})
	if w.Code != http.StatusOK {
		return w
	}
	c := decode[ceremonyReply](h.t, w)
	return h.do(req{method: "POST", path: "/_ir/api/join/verify", body: verifyRequest{Flow: c.Flow, Response: a.create(c.Options)}})
}

func (h *harness) login(a *authenticator, c *vcred) *httptest.ResponseRecorder {
	h.t.Helper()
	w := h.do(req{method: "POST", path: "/_ir/api/login/options", body: map[string]any{}})
	wantStatus(h.t, w, http.StatusOK)
	cer := decode[ceremonyReply](h.t, w)
	if strings.Contains(string(cer.Options), "allowCredentials") {
		h.t.Fatalf("login options list credentials: %s", cer.Options)
	}
	return h.do(req{method: "POST", path: "/_ir/api/login/verify", body: verifyRequest{Flow: cer.Flow, Response: a.get(cer.Options, c)}})
}

func (h *harness) whoami() (appReply, int) {
	h.t.Helper()
	w := h.do(req{path: "/api/whoami"})
	if w.Code != http.StatusOK {
		return appReply{}, w.Code
	}
	return decode[appReply](h.t, w), w.Code
}

// noSecretsLogged checks that no token or cookie value reached the log.
func (h *harness) noSecretsLogged() {
	h.t.Helper()
	logs := h.logs.String()
	for _, s := range h.secrets {
		if s != "" && strings.Contains(logs, s) {
			h.t.Fatalf("a secret was logged")
		}
	}
}

func ptr(s string) *string { return &s }

func TestRegisterAndLogin(t *testing.T) {
	h := newHarness(t, testOrigin)
	a := &authenticator{t: t, origin: testOrigin}

	// Nothing registered yet: the app is closed, a setup link waits in the state dir.
	if _, code := h.whoami(); code != http.StatusUnauthorized {
		t.Fatalf("whoami before login: %d", code)
	}
	w := h.do(req{path: "/sessions/1?x=y", header: map[string]string{"Accept": "text/html"}})
	wantStatus(t, w, http.StatusFound)
	if loc := w.Header().Get("Location"); loc != "/_ir/login?next=%2Fsessions%2F1%3Fx%3Dy" {
		t.Fatalf("redirect to %q", loc)
	}
	if s := decode[sessionReply](t, h.do(req{path: "/_ir/api/session"})); s.Authenticated || !s.Setup {
		t.Fatalf("session before setup: %+v", s)
	}
	acc := h.g.Access()
	if len(acc.Invites) != 1 || !acc.Invites[0].Setup {
		t.Fatalf("setup invite not listed: %+v", acc.Invites)
	}
	setup := h.setupToken()

	// The join page checks the link, then names as you type.
	reply := decode[joinReply](t, h.do(req{method: "POST", path: "/_ir/api/join/check", body: joinRequest{Token: setup}}))
	if reply.Name != "" {
		t.Fatalf("setup invite suggests %q", reply.Name)
	}
	for name, code := range map[string]string{"本机": "reserved", "UNKNOWN": "reserved", "a/b": "invalid", "1234567890123": "too_long", "Kumo Mac": ""} {
		r := decode[joinReply](t, h.do(req{method: "POST", path: "/_ir/api/join/check", body: joinRequest{Token: setup, Name: ptr(name)}}))
		if r.NameError != code {
			t.Fatalf("name %q: %q, want %q", name, r.NameError, code)
		}
	}

	w = h.join(a, setup, ptr("  Kumo   Mac "))
	wantStatus(t, w, http.StatusOK)
	set := w.Result().Header.Get("Set-Cookie")
	for _, attr := range []string{CookieName + "=", "Path=/", "HttpOnly", "Secure", "SameSite=Strict", "Max-Age=604800"} {
		if !strings.Contains(set, attr) {
			t.Fatalf("cookie %q lacks %s", set, attr)
		}
	}
	if strings.Contains(set, "Domain") {
		t.Fatalf("__Host- cookie with a Domain")
	}
	if _, err := os.Stat(filepath.Join(h.dir, "state", setupFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("setup file still there: %v", err)
	}
	me, code := h.whoami()
	if code != 200 || me.Name != "Kumo Mac" || me.Kind != KindPanel || me.Local {
		t.Fatalf("whoami after join: %d %+v", code, me)
	}
	mac := me.EndpointID

	// The setup token was used up.
	wantStatus(t, h.do(req{method: "POST", path: "/_ir/api/join/check", body: joinRequest{Token: setup}}), http.StatusForbidden)

	// A named invite for a second device. Its name is reserved meanwhile.
	inv, err := h.g.CreateInvite("手机")
	if err != nil {
		t.Fatal(err)
	}
	if inv.ExpiresAt != h.clock.Now().Add(InviteTTL) {
		t.Fatalf("invite expires %v", inv.ExpiresAt)
	}
	tok := tokenOf(t, inv.URL)
	h.secrets = append(h.secrets, tok)
	if _, err := h.g.CheckName("手机"); err != ErrNameTaken {
		t.Fatalf("pending invite name not reserved: %v", err)
	}
	if _, err := h.g.CreateInvite("kumo mac"); err != ErrNameTaken {
		t.Fatalf("invite with a taken name: %v", err)
	}
	if r := decode[joinReply](t, h.do(req{method: "POST", path: "/_ir/api/join/check", body: joinRequest{Token: tok}})); r.Name != "手机" {
		t.Fatalf("invite suggests %q", r.Name)
	}

	phone := &authenticator{t: t, origin: testOrigin}
	h.cookie = "" // another browser
	w = h.join(phone, tok, nil)
	wantStatus(t, w, http.StatusOK)
	me, _ = h.whoami()
	if me.Name != "手机" {
		t.Fatalf("second panel: %+v", me)
	}
	phoneID := me.EndpointID
	// One use only.
	wantStatus(t, h.join(phone, tok, nil), http.StatusForbidden)

	// Log out, log back in with the first passkey.
	wantStatus(t, h.do(req{method: "POST", path: "/_ir/api/logout"}), http.StatusOK)
	if h.cookie != "" {
		t.Fatalf("logout did not clear the cookie")
	}
	wantStatus(t, h.login(a, nil), http.StatusOK)
	if me, _ = h.whoami(); me.EndpointID != mac {
		t.Fatalf("logged in as %+v", me)
	}
	if s := decode[sessionReply](t, h.do(req{path: "/_ir/api/session"})); !s.Authenticated || s.Name != "Kumo Mac" {
		t.Fatalf("session: %+v", s)
	}
	acc = h.g.Access()
	if len(acc.Endpoints) != 2 || len(acc.Invites) != 0 {
		t.Fatalf("access: %+v", acc)
	}

	// Revoking the phone ends its sessions and closes its live connections.
	phoneCookie := h.cookie
	h.cookie = ""
	wantStatus(t, h.login(phone, nil), http.StatusOK)
	var closed bool
	r := httptest.NewRequest("GET", "/events", nil)
	r.Host = "ir.example.com"
	r.AddCookie(&http.Cookie{Name: CookieName, Value: h.cookie})
	tracked := false
	h.g.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.g.Track(r, func() { closed = true })
		tracked = true
	})).ServeHTTP(httptest.NewRecorder(), r)
	if !tracked {
		t.Fatal("events request not served")
	}
	if info, _ := h.g.Endpoint(phoneID); info.Name != "手机" {
		t.Fatalf("endpoint lookup: %+v", info)
	}
	for _, e := range h.g.Access().Endpoints {
		if want := map[bool]int{true: 1, false: 0}[e.ID == phoneID]; e.Online != want {
			t.Fatalf("%s online %d, want %d", e.Name, e.Online, want)
		}
	}
	if err := h.g.Revoke("手机"); err != nil {
		t.Fatal(err)
	}
	if !closed {
		t.Fatal("revoke did not close the connection")
	}
	if len(h.revoked) != 1 || h.revoked[0] != phoneID {
		t.Fatalf("OnRevoke: %v", h.revoked)
	}
	if _, code := h.whoami(); code != http.StatusUnauthorized {
		t.Fatalf("revoked session still works: %d", code)
	}
	wantStatus(t, h.login(phone, nil), http.StatusUnauthorized)

	// The last passkey stays.
	if err := h.g.Revoke(mac); err != ErrLastPasskey {
		t.Fatalf("revoke last passkey: %v", err)
	}
	h.cookie = phoneCookie
	h.noSecretsLogged()
}

func TestCounterRollback(t *testing.T) {
	h := newHarness(t, testOrigin)
	a := &authenticator{t: t, origin: testOrigin, count: true}
	wantStatus(t, h.join(a, h.setupToken(), ptr("key")), http.StatusOK)
	wantStatus(t, h.login(a, nil), http.StatusOK) // counter 1
	wantStatus(t, h.login(a, nil), http.StatusOK) // counter 2
	a.creds[0].signed = 0                         // a clone replays an older counter (next use signs 1)
	wantStatus(t, h.login(a, nil), http.StatusUnauthorized)
	a.creds[0].signed = 5
	wantStatus(t, h.login(a, nil), http.StatusOK) // the stored counter was not lowered
}

func TestLoginRejectsForgeries(t *testing.T) {
	h := newHarness(t, testOrigin)
	a := &authenticator{t: t, origin: testOrigin}
	wantStatus(t, h.join(a, h.setupToken(), ptr("mac")), http.StatusOK)
	h.cookie = ""

	// A passkey for this site that the gate never registered.
	stranger := &authenticator{t: t, origin: testOrigin}
	other := newHarness(t, testOrigin)
	wantStatus(t, other.join(stranger, other.setupToken(), ptr("x")), http.StatusOK)
	wantStatus(t, h.login(stranger, nil), http.StatusUnauthorized)

	// A phishing origin.
	phish := &authenticator{t: t, origin: "https://ir.example.com.evil.test", creds: a.creds}
	wantStatus(t, h.login(phish, nil), http.StatusUnauthorized)

	// A flow is single use.
	w := h.do(req{method: "POST", path: "/_ir/api/login/options", body: map[string]any{}})
	c := decode[ceremonyReply](t, w)
	resp := a.get(c.Options, nil)
	wantStatus(t, h.do(req{method: "POST", path: "/_ir/api/login/verify", body: verifyRequest{Flow: c.Flow, Response: resp}}), http.StatusOK)
	wantStatus(t, h.do(req{method: "POST", path: "/_ir/api/login/verify", body: verifyRequest{Flow: c.Flow, Response: resp}}), http.StatusUnauthorized)

	// Flows expire.
	w = h.do(req{method: "POST", path: "/_ir/api/login/options", body: map[string]any{}})
	c = decode[ceremonyReply](t, w)
	h.clock.Advance(flowTTL)
	wantStatus(t, h.do(req{method: "POST", path: "/_ir/api/login/verify", body: verifyRequest{Flow: c.Flow, Response: a.get(c.Options, nil)}}), http.StatusUnauthorized)
}

func TestInviteExpiry(t *testing.T) {
	h := newHarness(t, testOrigin)
	a := &authenticator{t: t, origin: testOrigin}
	wantStatus(t, h.join(a, h.setupToken(), ptr("mac")), http.StatusOK)
	inv, _ := h.g.CreateInvite("")
	tok := tokenOf(t, inv.URL)

	// Options fetched in time, verified after expiry: refused, nothing registered.
	h.clock.Advance(InviteTTL - time.Minute)
	w := h.do(req{method: "POST", path: "/_ir/api/join/options", body: joinRequest{Token: tok, Name: ptr("late")}})
	wantStatus(t, w, http.StatusOK)
	c := decode[ceremonyReply](t, w)
	h.clock.Advance(2 * time.Minute)
	wantStatus(t, h.do(req{method: "POST", path: "/_ir/api/join/verify", body: verifyRequest{Flow: c.Flow, Response: a.create(c.Options)}}), http.StatusForbidden)
	wantStatus(t, h.do(req{method: "POST", path: "/_ir/api/join/check", body: joinRequest{Token: tok}}), http.StatusForbidden)
	if acc := h.g.Access(); len(acc.Endpoints) != 1 || len(acc.Invites) != 0 {
		t.Fatalf("after expiry: %+v", acc)
	}

	// A revoked invite is gone too.
	inv, _ = h.g.CreateInvite("tablet")
	if err := h.g.Revoke("TABLET"); err != nil {
		t.Fatal(err)
	}
	wantStatus(t, h.do(req{method: "POST", path: "/_ir/api/join/check", body: joinRequest{Token: tokenOf(t, inv.URL)}}), http.StatusForbidden)
}

func TestNameRaceAtVerify(t *testing.T) {
	h := newHarness(t, testOrigin)
	a := &authenticator{t: t, origin: testOrigin}
	wantStatus(t, h.join(a, h.setupToken(), ptr("mac")), http.StatusOK)
	inv1, _ := h.g.CreateInvite("")
	inv2, _ := h.g.CreateInvite("")
	w := h.do(req{method: "POST", path: "/_ir/api/join/options", body: joinRequest{Token: tokenOf(t, inv1.URL), Name: ptr("pad")}})
	c := decode[ceremonyReply](t, w)
	h.cookie = ""
	wantStatus(t, h.join(&authenticator{t: t, origin: testOrigin}, tokenOf(t, inv2.URL), ptr("PAD")), http.StatusOK)
	w = h.do(req{method: "POST", path: "/_ir/api/join/verify", body: verifyRequest{Flow: c.Flow, Response: a.create(c.Options)}})
	wantStatus(t, w, http.StatusConflict)
	if r := decode[joinReply](t, w); r.NameError != "taken" {
		t.Fatalf("race: %+v", r)
	}
}

func TestSessionTimeouts(t *testing.T) {
	h := newHarness(t, testOrigin)
	a := &authenticator{t: t, origin: testOrigin}
	wantStatus(t, h.join(a, h.setupToken(), ptr("mac")), http.StatusOK)

	// Idle: 12 hours without a request.
	h.clock.Advance(IdleTimeout - time.Minute)
	if _, code := h.whoami(); code != 200 {
		t.Fatalf("idle 11:59: %d", code)
	}
	h.clock.Advance(IdleTimeout - time.Second)
	if _, code := h.whoami(); code != 200 {
		t.Fatalf("activity did not refresh the idle timer: %d", code)
	}
	h.clock.Advance(IdleTimeout)
	if _, code := h.whoami(); code != http.StatusUnauthorized {
		t.Fatalf("idle 12h: %d", code)
	}

	// Absolute: 7 days, however active.
	wantStatus(t, h.login(a, nil), http.StatusOK)
	for i := 0; i < 15; i++ { // 15 × 11h = 6.9 days
		h.clock.Advance(11 * time.Hour)
		if _, code := h.whoami(); code != 200 {
			t.Fatalf("active day %d: %d", i, code)
		}
	}
	h.clock.Advance(4 * time.Hour)
	if _, code := h.whoami(); code != http.StatusUnauthorized {
		t.Fatalf("after 7 days: %d", code)
	}
}

func TestSweeperClosesExpiredConnections(t *testing.T) {
	h := newHarness(t, testOrigin)
	a := &authenticator{t: t, origin: testOrigin}
	wantStatus(t, h.join(a, h.setupToken(), ptr("mac")), http.StatusOK)
	var closed bool
	r := httptest.NewRequest("GET", "/events", nil)
	r.Host = "ir.example.com"
	r.AddCookie(&http.Cookie{Name: CookieName, Value: h.cookie})
	h.g.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.g.Track(r, func() { closed = true })
	})).ServeHTTP(httptest.NewRecorder(), r)
	h.g.sweep()
	if closed {
		t.Fatal("closed a live session's connection")
	}
	h.clock.Advance(IdleTimeout)
	h.g.sweep()
	if !closed {
		t.Fatal("expired session's connection left open")
	}
}

func TestStateSurvivesRestart(t *testing.T) {
	h := newHarness(t, testOrigin)
	a := &authenticator{t: t, origin: testOrigin}
	setup := h.setupToken()
	h.open(testOrigin) // restart before anyone registered: a new setup token
	if h.setupToken() == setup {
		t.Fatal("setup token not rotated on restart")
	}
	wantStatus(t, h.join(a, h.setupToken(), ptr("mac")), http.StatusOK)
	h.open(testOrigin)
	if _, err := os.Stat(filepath.Join(h.dir, "state", setupFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("setup file written although a passkey exists")
	}
	if me, code := h.whoami(); code != 200 || me.Name != "mac" {
		t.Fatalf("session lost on restart: %d %+v", code, me)
	}
	h.cookie = ""
	wantStatus(t, h.login(a, nil), http.StatusOK)
	fi, err := os.Stat(filepath.Join(h.dir, "state", "gate.json"))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("state file: %v %v", err, fi.Mode())
	}
	data, _ := os.ReadFile(filepath.Join(h.dir, "state", "gate.json"))
	for _, s := range append(h.secrets, setup) {
		if bytes.Contains(data, []byte(s)) {
			t.Fatal("a raw token is stored")
		}
	}
}

func TestLocalMode(t *testing.T) {
	h := newHarness(t, "")
	if h.g.Public() {
		t.Fatal("public without an origin")
	}
	if _, err := os.Stat(filepath.Join(h.dir, "state", setupFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("setup file in local mode")
	}
	do := func(host, method, origin, site string) int {
		r := httptest.NewRequest(method, "/api/x", nil)
		r.Host = host
		r.RemoteAddr = "127.0.0.1:5000"
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if site != "" {
			r.Header.Set("Sec-Fetch-Site", site)
		}
		w := httptest.NewRecorder()
		h.h.ServeHTTP(w, r)
		if w.Code == 200 {
			if id := decode[appReply](t, w); !id.Local || id.Name != LocalName {
				t.Fatalf("local identity: %+v", id)
			}
		}
		return w.Code
	}
	for _, c := range []struct {
		host, method, origin, site string
		want                       int
	}{
		{"127.0.0.1:7810", "GET", "", "", 200},
		{"localhost:7810", "POST", "http://localhost:7810", "same-origin", 200},
		{"[::1]:7810", "POST", "", "", 200},                          // curl
		{"evil.example:7810", "GET", "", "", 421},                    // DNS rebinding
		{"127.0.0.1:7810", "POST", "https://evil.example", "", 403},  // CSRF
		{"127.0.0.1:7810", "POST", "", "cross-site", 403},            // CSRF
		{"127.0.0.1:7810", "POST", "http://localhost:5173", "", 403}, // another local site
		{"127.0.0.1:7810", "POST", "http://127.0.0.1:7810", "", 200},
	} {
		if got := do(c.host, c.method, c.origin, c.site); got != c.want {
			t.Errorf("%s %s origin=%q site=%q: %d, want %d", c.method, c.host, c.origin, c.site, got, c.want)
		}
	}
	r := httptest.NewRequest("GET", "/_ir/login", nil)
	r.Host = "127.0.0.1:7810"
	w := httptest.NewRecorder()
	h.h.ServeHTTP(w, r)
	wantStatus(t, w, http.StatusFound)
	if _, err := h.g.CreateInvite(""); err != ErrNotPublic {
		t.Fatalf("invite in local mode: %v", err)
	}
}

func TestPublicHostAndOrigin(t *testing.T) {
	h := newHarness(t, testOrigin)
	a := &authenticator{t: t, origin: testOrigin}
	wantStatus(t, h.join(a, h.setupToken(), ptr("mac")), http.StatusOK)

	for _, c := range []struct {
		name string
		q    req
		host string
		want int
	}{
		{"ok", req{method: "POST", path: "/api/x"}, "", 200},
		{"default port", req{path: "/api/x"}, "ir.example.com:443", 200},
		{"loopback host", req{path: "/api/x"}, "127.0.0.1:7810", 421},
		{"other host", req{path: "/api/x"}, "evil.example", 421},
		{"no origin", req{method: "POST", path: "/api/x", header: map[string]string{"Origin": ""}}, "", 403},
		{"foreign origin", req{method: "POST", path: "/api/x", header: map[string]string{"Origin": "https://evil.example"}}, "", 403},
		{"http origin", req{method: "POST", path: "/api/x", header: map[string]string{"Origin": "http://ir.example.com"}}, "", 403},
		{"cross-site", req{method: "DELETE", path: "/api/x", header: map[string]string{"Sec-Fetch-Site": "same-site"}}, "", 403},
		{"ws ok", req{path: "/ws", header: map[string]string{"Upgrade": "websocket", "Connection": "Upgrade", "Origin": testOrigin}}, "", 200},
		{"ws foreign", req{path: "/ws", header: map[string]string{"Upgrade": "websocket", "Connection": "Upgrade", "Origin": "https://evil.example"}}, "", 403},
		{"ws no origin", req{path: "/ws", header: map[string]string{"Upgrade": "websocket", "Connection": "Upgrade"}}, "", 403},
		{"ws to gate", req{path: "/_ir/api/session", header: map[string]string{"Upgrade": "websocket", "Origin": testOrigin}}, "", 404},
		{"ws no session", req{path: "/ws", noCookie: true, header: map[string]string{"Upgrade": "websocket", "Origin": testOrigin}}, "", 401},
		{"gate api foreign", req{method: "POST", path: "/_ir/api/login/options", body: map[string]any{}, header: map[string]string{"Origin": "https://evil.example"}}, "", 403},
		{"logout foreign", req{method: "POST", path: "/_ir/api/logout", header: map[string]string{"Origin": "https://evil.example"}}, "", 403},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := h.doHost(c.q, c.host)
			if w.Code != c.want {
				t.Fatalf("%d, want %d: %s", w.Code, c.want, w.Body.String())
			}
		})
	}
	if _, code := h.whoami(); code != 200 {
		t.Fatal("foreign logout ended the session")
	}
}

func (h *harness) doHost(q req, host string) *httptest.ResponseRecorder {
	if host == "" {
		return h.do(q)
	}
	r := httptest.NewRequest(cmp(q.method, "GET"), q.path, nil)
	r.Host = host
	r.RemoteAddr = "198.51.100.7:40000"
	r.AddCookie(&http.Cookie{Name: CookieName, Value: h.cookie})
	w := httptest.NewRecorder()
	h.h.ServeHTTP(w, r)
	return w
}

func cmp(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func TestRateLimit(t *testing.T) {
	h := newHarness(t, testOrigin)
	bad := joinRequest{Token: strings.Repeat("A", tokenLen)}
	check := func(remote, cf string) int {
		q := req{method: "POST", path: "/_ir/api/join/check", body: bad, remote: remote}
		if cf != "" {
			q.header = map[string]string{"CF-Connecting-IP": cf}
		}
		return h.do(q).Code
	}
	for i := 0; i < maxFailures; i++ {
		if code := check("198.51.100.7:1", ""); code != http.StatusForbidden {
			t.Fatalf("attempt %d: %d", i, code)
		}
	}
	w := h.do(req{method: "POST", path: "/_ir/api/join/check", body: bad})
	wantStatus(t, w, http.StatusTooManyRequests)
	if w.Header().Get("Retry-After") != "900" {
		t.Fatalf("Retry-After %q", w.Header().Get("Retry-After"))
	}
	// Even a good token waits now.
	if code := h.do(req{method: "POST", path: "/_ir/api/join/check", body: joinRequest{Token: h.setupToken()}}).Code; code != http.StatusTooManyRequests {
		t.Fatalf("locked client with a good token: %d", code)
	}
	// Another client is unaffected.
	if code := check("203.0.113.9:1", ""); code != http.StatusForbidden {
		t.Fatalf("other client: %d", code)
	}
	// CF-Connecting-IP counts only from loopback (cloudflared); otherwise the peer does.
	if code := check("203.0.113.10:1", "198.51.100.7"); code != http.StatusForbidden {
		t.Fatalf("spoofed header from a remote peer: %d", code)
	}
	if code := check("127.0.0.1:1", "198.51.100.7"); code != http.StatusTooManyRequests {
		t.Fatalf("cloudflared-forwarded locked client: %d", code)
	}
	if code := check("127.0.0.1:1", "192.0.2.1"); code != http.StatusForbidden {
		t.Fatalf("cloudflared-forwarded other client: %d", code)
	}
	// The lock lifts after 15 minutes.
	h.clock.Advance(lockout)
	if code := check("198.51.100.7:1", ""); code != http.StatusForbidden {
		t.Fatalf("after lockout: %d", code)
	}
	h.noSecretsLogged()
}

func TestPagesAndHeaders(t *testing.T) {
	h := newHarness(t, testOrigin)
	for path, ctype := range map[string]string{"/_ir/login": "text/html", "/_ir/join": "text/html", "/_ir/gate.js": "text/javascript", "/_ir/gate.css": "text/css"} {
		w := h.do(req{path: path})
		wantStatus(t, w, 200)
		hd := w.Header()
		if !strings.HasPrefix(hd.Get("Content-Type"), ctype) || hd.Get("Cache-Control") != "no-store" ||
			!strings.Contains(hd.Get("Content-Security-Policy"), "script-src 'self'") ||
			!strings.Contains(hd.Get("Content-Security-Policy"), "frame-ancestors 'none'") ||
			hd.Get("X-Frame-Options") != "DENY" || hd.Get("Referrer-Policy") != "no-referrer" ||
			!strings.HasPrefix(hd.Get("Strict-Transport-Security"), "max-age=") {
			t.Fatalf("%s headers: %v", path, hd)
		}
	}
	w := h.do(req{path: "/_ir/"})
	wantStatus(t, w, http.StatusFound)
	wantStatus(t, h.do(req{path: "/_ir/nothing"}), http.StatusNotFound)
	wantStatus(t, h.do(req{path: "/_ir/health"}), http.StatusOK)
	if cc := h.do(req{path: "/_ir/api/session"}).Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("session api cache-control %q", cc)
	}
}

func TestAuthenticatedResponsesArePrivate(t *testing.T) {
	h := newHarness(t, testOrigin)
	a := &authenticator{t: t, origin: testOrigin}
	wantStatus(t, h.join(a, h.setupToken(), ptr("mac")), http.StatusOK)
	w := h.do(req{path: "/assets/app.js"}) // the app says public, max-age=60
	if cc := w.Header().Get("Cache-Control"); cc != "private, max-age=60" {
		t.Fatalf("cache-control %q", cc)
	}
	for in, want := range map[string]string{
		"":                                    "private, no-cache",
		"public, max-age=31536000, immutable": "private, max-age=31536000, immutable",
		"no-store":                            "private, no-store",
		"s-maxage=600, max-age=10":            "private, max-age=10",
		"private":                             "private",
	} {
		if got := privateCacheControl(in); got != want {
			t.Errorf("privateCacheControl(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSafeNext(t *testing.T) {
	for in, want := range map[string]string{
		"/":                  "/",
		"/sessions/1?x=y":    "/sessions/1?x=y",
		"":                   "/",
		"//evil.example":     "/",
		"/\\evil.example":    "/",
		"https://evil.test/": "/",
		"/_ir/login":         "/",
		"/_ir":               "/",
		"/_irx":              "/_irx",
		"/a\nb":              "/",
	} {
		if got := safeNext(in); got != want {
			t.Errorf("safeNext(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestOriginConfig(t *testing.T) {
	for _, bad := range []string{"http://ir.example.com", "https://ir.example.com/path", "ftp://x", "https://user@ir.example.com", "ir.example.com", "https://127.0.0.1"} {
		if _, err := New(Config{PublicOrigin: bad, Store: &JSONStore{cur: newMemState(State{})}}); err == nil {
			t.Errorf("accepted origin %q", bad)
		}
	}
	for in, want := range map[string]string{
		"https://IR.Example.com/":     "https://ir.example.com",
		"https://ir.example.com:443":  "https://ir.example.com",
		"https://ir.example.com:8443": "https://ir.example.com:8443",
		"http://localhost:7810":       "http://localhost:7810",
	} {
		g, err := New(Config{PublicOrigin: in, Store: &JSONStore{path: filepath.Join(t.TempDir(), "g.json"), cur: newMemState(State{})}})
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if g.origin != want {
			t.Errorf("origin %q → %q, want %q", in, g.origin, want)
		}
		g.Close()
	}
}
