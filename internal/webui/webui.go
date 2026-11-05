// Package webui serves the web app web/ builds into dist (npm run build), embedded into the
// binary (plan "弱网"): the hashed files under /assets/ kept by browsers for good, everything else
// asked afresh (no-cache, with an ETag, so asking is cheap), a script or style sent as its brotli or
// gzip twin when the browser takes one. Wrap puts it in front of the API, whose JSON it gzips.
//
// Mount (cmd/iridium, internal/server):
//
//	http.Serve(tcp, webui.Wrap(apiHandler)) // /api/ and /_ir/ go to apiHandler, the rest is the app
package webui

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"
)

//go:embed all:dist
var dist embed.FS

// Built is the embedded build, rooted at dist.
func Built() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}

// CSP is the page's: its own scripts only, no frames.
const CSP = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; media-src 'self' blob:; connect-src 'self'; worker-src 'self'; manifest-src 'self'; font-src 'self' data:; object-src 'none'; base-uri 'none'; frame-ancestors 'none'"

type file struct {
	raw, br, gz []byte
	etag, ctype string
}

// App serves the build in fsys (Built(), or a test's). Without index.html (web/ not built) every
// page answers 503 saying so.
type App struct {
	fsys  fs.FS
	once  sync.Once
	files map[string]*file
}

func New(fsys fs.FS) *App { return &App{fsys: fsys} }

// Handler is the embedded build's app.
func Handler() *App { return New(Built()) }

func (a *App) load() {
	a.files = map[string]*file{}
	_ = fs.WalkDir(a.fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasPrefix(p, ".") || strings.HasSuffix(p, ".br") || strings.HasSuffix(p, ".gz") || strings.HasPrefix(path.Base(p), ".") {
			return nil
		}
		raw, err := fs.ReadFile(a.fsys, p)
		if err != nil {
			return nil
		}
		f := &file{raw: raw, ctype: contentType(p)}
		f.br, _ = fs.ReadFile(a.fsys, p+".br")
		f.gz, _ = fs.ReadFile(a.fsys, p+".gz")
		sum := sha256.Sum256(raw)
		f.etag = `"` + hex.EncodeToString(sum[:8]) + `"`
		a.files["/"+p] = f
		return nil
	})
}

func contentType(p string) string {
	switch path.Ext(p) {
	case ".webmanifest":
		return "application/manifest+json"
	case ".js":
		return "text/javascript; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	}
	if t := mime.TypeByExtension(path.Ext(p)); t != "" {
		return t
	}
	return "application/octet-stream"
}

// Built says whether the app has a build to serve.
func (a *App) Built() bool {
	a.once.Do(a.load)
	return a.files["/index.html"] != nil
}

func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.once.Do(a.load)
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	p := r.URL.Path
	if p == "/" {
		p = "/index.html"
	}
	f := a.files[p]
	if f == nil {
		if a.files["/index.html"] == nil {
			w.Header().Set("Cache-Control", "no-store")
			http.Error(w, "The web app is not built: run npm run build in web/, then build Iridium again.", http.StatusServiceUnavailable)
			return
		}
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	h.Set("Content-Type", f.ctype)
	h.Set("X-Content-Type-Options", "nosniff")
	if strings.HasPrefix(p, "/assets/") {
		h.Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		h.Set("Cache-Control", "no-cache")
	}
	if p == "/index.html" {
		h.Set("Content-Security-Policy", CSP)
		h.Set("Referrer-Policy", "same-origin")
	}
	h.Set("ETag", f.etag)
	h.Add("Vary", "Accept-Encoding")
	if match := r.Header.Get("If-None-Match"); match != "" && strings.Contains(match, f.etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	body := f.raw
	switch enc := r.Header.Get("Accept-Encoding"); {
	case f.br != nil && accepts(enc, "br"):
		h.Set("Content-Encoding", "br")
		body = f.br
	case f.gz != nil && accepts(enc, "gzip"):
		h.Set("Content-Encoding", "gzip")
		body = f.gz
	}
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(body))
}

// accepts says whether an Accept-Encoding takes coding (not with q=0).
func accepts(header, coding string) bool {
	for _, part := range strings.Split(header, ",") {
		name, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if strings.EqualFold(strings.TrimSpace(name), coding) {
			q := strings.ReplaceAll(strings.TrimSpace(params), " ", "")
			return q != "q=0" && q != "q=0.0" && q != "q=0.00" && q != "q=0.000"
		}
	}
	return false
}

// Wrap is the app in front of api: /api/ and /_ir/ go to api, their JSON gzipped for a browser
// that takes it (never an event stream: gzip holds events back till a block fills); the rest is
// the app.
func Wrap(api http.Handler) http.Handler {
	return WrapApp(api, Handler())
}

func WrapApp(api http.Handler, app http.Handler) http.Handler {
	zipped := Gzip(api)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/_ir/") {
			zipped.ServeHTTP(w, r)
			return
		}
		app.ServeHTTP(w, r)
	})
}
