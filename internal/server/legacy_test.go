package server

import (
	"net/http"
	"strings"
	"testing"

	"github.com/kumoSleeping/iridium/internal/api"
)

// The three 0.1 paths programs installed elsewhere still call are their /v1/internal routes; any
// other /api path is no route, over TCP and the socket alike.
func TestLegacyPaths(t *testing.T) {
	t.Parallel()
	l := start(t)
	// Pairing: the window is closed, as at /v1/internal/pair.
	for _, path := range []string{"/api/pair", "/v1/internal/pair"} {
		var e api.Error
		if code := l.do("web", "POST", path, api.PairRequest{Name: "old"}, &e); code != 403 {
			t.Errorf("POST %s: %d %+v, want 403", path, code, e)
		}
	}
	if code := l.do("web", "GET", "/api/pair/nope", nil, nil); code != 422 {
		t.Errorf("GET /api/pair/nope: %d, want 422", code)
	}
	// The executor's link and a remote ir: there, refused without a session's TOKEN, not missing.
	for _, p := range [][2]string{{"GET", "/api/exec"}, {"POST", "/api/cli"}} {
		if code := l.do("web", p[0], p[1], nil, nil); code == 404 {
			t.Errorf("%s %s: 404", p[0], p[1])
		}
	}
	for _, as := range []string{"web", "socket"} {
		for _, path := range []string{"/api/sessions", "/api/me", "/api"} {
			var raw string
			if code := l.do(as, "GET", path, nil, &raw); code != 404 || strings.Contains(raw, "<html") {
				t.Errorf("GET %s as %s: %d %.80s, want 404", path, as, code, raw)
			}
		}
	}
	if code := l.do("web", "GET", "/v1/internal/sessions", nil, nil); code != http.StatusOK {
		t.Errorf("GET /v1/internal/sessions: %d", code)
	}
}
