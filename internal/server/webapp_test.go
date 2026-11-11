package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/kumoSleeping/iridium/internal/webui"
)

// The TCP address serves the web app past the API, and the gate stands in front of both: with a
// public origin a browser is sent to the login, the app's files included.
func TestWebApp(t *testing.T) {
	s, _, _ := setup(t)
	s.App = webui.New(fstest.MapFS{
		"index.html":      {Data: []byte("<!doctype html><title>Iridium</title>")},
		"assets/app-1.js": {Data: []byte("console.log(1)")},
	})
	h := s.Handler(false)
	get := func(path, accept string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "http://127.0.0.1:7810"+path, nil)
		if accept != "" {
			req.Header.Set("Accept", accept)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}
	if w := get("/", "text/html"); w.Code != 200 || !strings.Contains(w.Body.String(), "<title>Iridium") || w.Header().Get("Content-Security-Policy") == "" {
		t.Fatalf("/: %d %q", w.Code, w.Body.String())
	}
	if w := get("/assets/app-1.js", ""); w.Code != 200 || !strings.Contains(w.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("asset: %d %q", w.Code, w.Header().Get("Cache-Control"))
	}
	if w := get("/v1/internal/health", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"ok":true`) {
		t.Fatalf("health: %d %q", w.Code, w.Body.String())
	}
	if w := get("/_ir/api/session", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"local":true`) {
		t.Fatalf("gate session: %d %q", w.Code, w.Body.String())
	}

	if err := s.SetPublicOrigin("https://ir.example.com"); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "https://ir.example.com/", nil)
	req.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusFound || !strings.HasPrefix(w.Header().Get("Location"), "/_ir/login") {
		t.Fatalf("public /: %d %q", w.Code, w.Header().Get("Location"))
	}
	req = httptest.NewRequest("GET", "https://ir.example.com/assets/app-1.js", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("public asset without a login: %d", w.Code)
	}
}
