package server

import (
	"net/http"
	"strings"
)

// legacy serves the three 0.1 paths a program installed elsewhere may still call, as the
// /v1/internal routes they became: an executor's /api/exec, ir join's (or the dsh plugin's)
// /api/pair and /api/pair/{request}, a remote session's ir /api/cli. Those programs do not update
// themselves, so these stay till they have; not yet removed (docs/legacy.md). Any other /api path
// is no route: 404, not the web app's page.
func legacy(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if p == "/api/exec" || p == "/api/cli" || p == "/api/pair" || strings.HasPrefix(p, "/api/pair/") {
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/v1/internal" + strings.TrimPrefix(p, "/api")
			r2.URL.RawPath = ""
			next.ServeHTTP(w, r2)
			return
		}
		if p == "/api" || strings.HasPrefix(p, "/api/") {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}
