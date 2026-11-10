package gate

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

// The core knows a request by its TOKEN alone: a cookie (ir_web's or any) is no one's, refused
// unless the path is open; the host only through AsHost, which nothing on the wire can set.
func TestCoreIsTokenOnly(t *testing.T) {
	st, err := OpenJSONStore(filepath.Join(t.TempDir(), "gate.json"))
	if err != nil {
		t.Fatal(err)
	}
	g, err := New(Config{Store: st, Open: func(r *http.Request) bool { return r.URL.Path == "/api/pair" }})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	_, tok, err := g.CreateApp("bot", "")
	if err != nil {
		t.Fatal(err)
	}
	var seen *Identity
	h := g.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := FromContext(r.Context())
		if ok {
			seen = &id
		}
	}))
	do := func(path string, set func(*http.Request)) int {
		seen = nil
		r := httptest.NewRequest("GET", path, nil)
		r.RemoteAddr = "127.0.0.1:1"
		set(r)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	cookie := func(r *http.Request) { r.AddCookie(&http.Cookie{Name: "__Host-ir", Value: NewSecret()}) }
	if code := do("/api/me", cookie); code != http.StatusUnauthorized || seen != nil {
		t.Errorf("a cookie alone: %d %+v", code, seen)
	}
	if code := do("/api/me", func(*http.Request) {}); code != http.StatusUnauthorized {
		t.Errorf("nothing: %d", code)
	}
	// Satori's headers name a login; alone they are no one.
	if code := do("/api/me", func(r *http.Request) {
		r.Header.Set("Satori-Platform", WebApp)
		r.Header.Set("Satori-User-ID", "someone")
	}); code != http.StatusUnauthorized {
		t.Errorf("Satori headers alone: %d", code)
	}
	if code := do("/api/pair", cookie); code != 200 || seen != nil {
		t.Errorf("an open path with a cookie: %d %+v", code, seen)
	}
	if code := do("/api/me", func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+tok) }); code != 200 || seen == nil || seen.Name != "bot" || seen.Host() {
		t.Errorf("the app's TOKEN: %d %+v", code, seen)
	}
	r := httptest.NewRequest("GET", "/api/me", nil)
	h.ServeHTTP(httptest.NewRecorder(), AsHost(r))
	if seen == nil || !seen.Host() {
		t.Errorf("AsHost: %+v", seen)
	}
	// ir_web's own TOKEN is a built-in app's: it is let in, never listed, found or removed.
	webTok := "ir_" + NewSecret()
	if err := g.SetAppToken(WebApp, webTok); err != nil {
		t.Fatal(err)
	}
	if code := do("/api/me", func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+webTok) }); code != 200 || seen == nil || seen.App != WebApp || seen.Web() {
		t.Errorf("ir_web's TOKEN: %d %+v", code, seen)
	}
	for _, e := range g.Access().Endpoints {
		if e.ID == WebApp {
			t.Error("ir_web listed")
		}
	}
	if _, ok := g.ByToken(webTok); ok {
		t.Error("ir_web found by its TOKEN (Satori's IDENTIFY)")
	}
	if err := g.Revoke(WebApp); err != ErrBuiltIn {
		t.Errorf("removed ir_web: %v", err)
	}
	if _, err := g.UpdateProfile(WebApp, Profile{Name: new(string)}); err != ErrNotFound {
		t.Errorf("ir_web's profile: %v", err)
	}
}
