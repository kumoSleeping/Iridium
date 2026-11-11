package webapp

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/kumoSleeping/iridium/internal/gate"
)

// heard is what the core got of a request: who it is, and the headers it came with.
type heard struct {
	id     gate.Identity
	header http.Header
}

func (h *harness) listen() (http.Handler, *heard) {
	got := &heard{}
	return h.chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.id, _ = gate.FromContext(r.Context())
		got.header = r.Header.Clone()
	})), got
}

// A logged-in browser reaches the core as ir_web speaking for its login: ir_web's TOKEN and
// Satori's headers, whatever the browser sent of them, and no cookie.
func TestForwardsBrowserAsWebLogin(t *testing.T) {
	h := newHarness(t, testOrigin)
	wantStatus(t, h.join(&authenticator{t: t, origin: testOrigin}, h.setupToken(), ptr("mac")), http.StatusOK)
	me, _ := h.whoami()
	srv, got := h.listen()
	r := httptest.NewRequest("POST", "/v1/internal/x", nil)
	r.Host = "ir.example.com"
	r.Header.Set("Origin", testOrigin)
	r.Header.Set(HeaderPlatform, "qq")
	r.Header.Set(HeaderUser, "someone-else")
	r.AddCookie(&http.Cookie{Name: CookieName, Value: h.cookie})
	r.AddCookie(&http.Cookie{Name: "theme", Value: "dark"})
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	wantStatus(t, w, http.StatusOK)
	if !got.id.Web() || got.id.Login != me.ID || got.id.Name != "mac" {
		t.Fatalf("identity: %+v", got.id)
	}
	b, _ := os.ReadFile(h.dir + "/state/web-token")
	tok := strings.TrimSpace(string(b))
	if got.header.Get("Authorization") != "Bearer "+tok || got.header.Get(HeaderPlatform) != gate.WebApp || got.header.Get(HeaderUser) != me.ID {
		t.Fatalf("headers: platform %q user %q", got.header.Get(HeaderPlatform), got.header.Get(HeaderUser))
	}
	// A start makes it anew, kept in the file.
	if again, err := AppToken(h.g, h.dir+"/state"); err != nil || again == tok {
		t.Fatalf("made anew: %v", err)
	} else if b, _ := os.ReadFile(h.dir + "/state/web-token"); strings.TrimSpace(string(b)) != again {
		t.Fatal("the file holds another")
	}
	if c := got.header.Get("Cookie"); strings.Contains(c, CookieName) || !strings.Contains(c, "theme=dark") {
		t.Fatalf("cookie forwarded: %q", c)
	}
	// A name an invite holds is no login's to take by a rename.
	if _, err := h.w.CreateInvite("pad", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := h.g.UpdateProfile(me.ID, gate.Profile{Name: ptr("PAD")}); err != gate.ErrNameTaken {
		t.Fatalf("renamed to an invite's name: %v", err)
	}
	// Logged out, the same browser is no one.
	h.cookie = ""
	if _, code := h.whoami(); code != http.StatusUnauthorized {
		t.Fatalf("no cookie: %d", code)
	}
}

// A request with a TOKEN of its own goes through ir_web untouched, whatever cookie it carries.
func TestTokenPassesThrough(t *testing.T) {
	h := newHarness(t, testOrigin)
	wantStatus(t, h.join(&authenticator{t: t, origin: testOrigin}, h.setupToken(), ptr("mac")), http.StatusOK)
	app, tok, err := h.g.CreateApp("bot", "")
	if err != nil {
		t.Fatal(err)
	}
	h.secrets = append(h.secrets, tok)
	srv, got := h.listen()
	for _, host := range []string{"ir.example.com", "127.0.0.1:7810"} {
		r := httptest.NewRequest("POST", "/v1/internal/x", nil)
		r.Host = host
		r.Header.Set("Authorization", "Bearer "+tok)
		r.Header.Set(HeaderUser, "u1")
		r.AddCookie(&http.Cookie{Name: CookieName, Value: h.cookie})
		want := r.Header.Clone()
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		wantStatus(t, w, http.StatusOK)
		if got.id.App != app.ID || got.id.Web() {
			t.Fatalf("%s: identity %+v", host, got.id)
		}
		for k := range want {
			if got.header.Get(k) != want.Get(k) {
				t.Fatalf("%s: header %s changed: %q", host, k, got.header.Get(k))
			}
		}
	}
	h.noSecretsLogged()
}

// With ir_web off, a browser on this machine is the host's page (gate.AsHost); a cookie is
// nothing, and there are no login pages.
func TestOffIsHostPage(t *testing.T) {
	h := newHarness(t, "")
	srv, got := h.listen()
	r := httptest.NewRequest("GET", "/v1/internal/x", nil)
	r.Host = "localhost:7810"
	r.AddCookie(&http.Cookie{Name: CookieName, Value: gate.NewSecret()})
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	wantStatus(t, w, http.StatusOK)
	if !got.id.Host() || strings.Contains(got.header.Get("Cookie"), CookieName) {
		t.Fatalf("off: %+v %q", got.id, got.header.Get("Cookie"))
	}
	wantStatus(t, h.do(req{method: "POST", path: "/_ir/api/login/options", body: map[string]any{}, header: map[string]string{"Origin": ""}}), http.StatusMisdirectedRequest)
	r = httptest.NewRequest("POST", "/_ir/api/login/options", strings.NewReader("{}"))
	r.Host = "localhost:7810"
	w = httptest.NewRecorder()
	h.h.ServeHTTP(w, r)
	wantStatus(t, w, http.StatusNotFound)
}
