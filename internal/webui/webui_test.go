package webui

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func build() fstest.MapFS {
	var gz bytes.Buffer
	w := gzip.NewWriter(&gz)
	w.Write([]byte("console.log(1)"))
	w.Close()
	return fstest.MapFS{
		"index.html":                 {Data: []byte(`<script type="module" src="/assets/index-abc.js"></script>`)},
		"assets/index-abc.js":        {Data: []byte("console.log(1)")},
		"assets/index-abc.js.br":     {Data: []byte("BROTLI")},
		"assets/index-abc.js.gz":     {Data: gz.Bytes()},
		"theme.js":                   {Data: []byte("theme")},
		"manifest.webmanifest":       {Data: []byte("{}")},
		".vite/manifest.json":        {Data: []byte("{}")},
		".gitkeep":                   {Data: nil},
		"logos/openai.svg":           {Data: []byte("<svg/>")},
		"assets/index-abc.css":       {Data: []byte("body{}")},
		"assets/index-abc.css.gz":    {Data: []byte("x")},
		"assets/chat-xyz.js":         {Data: []byte("chat")},
		"assets/chat-xyz.js.br":      {Data: []byte("CHATBR")},
		"assets/lib~chat-qqq.js":     {Data: []byte("lib")},
		"assets/lib~chat-qqq.js.br":  {Data: []byte("LIBBR")},
		"assets/settings-abc.css.br": {Data: []byte("orphan")}, // a twin with no file: not served
	}
}

func get(t *testing.T, h http.Handler, path string, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("GET", path, nil)
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestServe(t *testing.T) {
	app := New(build())
	// The page: asked afresh each time, its own scripts only.
	w := get(t, app, "/", nil)
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-cache" || !strings.Contains(w.Header().Get("Content-Security-Policy"), "script-src 'self'") || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("/: %d %v", w.Code, w.Header())
	}
	// Asked again with its ETag: not modified.
	if w2 := get(t, app, "/", map[string]string{"If-None-Match": w.Header().Get("ETag")}); w2.Code != http.StatusNotModified {
		t.Errorf("/ with its etag: %d", w2.Code)
	}
	// A hashed file: kept for good; its brotli for a browser that takes br, its gzip for one that takes gzip.
	w = get(t, app, "/assets/index-abc.js", map[string]string{"Accept-Encoding": "gzip, deflate, br"})
	if w.Code != 200 || !strings.Contains(w.Header().Get("Cache-Control"), "immutable") || w.Header().Get("Content-Encoding") != "br" || w.Body.String() != "BROTLI" || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/javascript") {
		t.Errorf("br: %d %v %q", w.Code, w.Header(), w.Body.String())
	}
	if w.Header().Get("Vary") != "Accept-Encoding" {
		t.Errorf("vary: %q", w.Header().Get("Vary"))
	}
	w = get(t, app, "/assets/index-abc.js", map[string]string{"Accept-Encoding": "gzip"})
	zr, err := gzip.NewReader(w.Body)
	if err != nil || w.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("gzip: %v %v", err, w.Header())
	}
	if b, _ := io.ReadAll(zr); string(b) != "console.log(1)" {
		t.Errorf("gzip body %q", b)
	}
	w = get(t, app, "/assets/index-abc.js", map[string]string{"Accept-Encoding": "br;q=0, identity"})
	if w.Header().Get("Content-Encoding") != "" || w.Body.String() != "console.log(1)" {
		t.Errorf("identity: %v %q", w.Header(), w.Body.String())
	}
	// Files beside the page: no-cache, by their type.
	w = get(t, app, "/manifest.webmanifest", nil)
	if w.Header().Get("Cache-Control") != "no-cache" || w.Header().Get("Content-Type") != "application/manifest+json" {
		t.Errorf("manifest: %v", w.Header())
	}
	if w = get(t, app, "/logos/openai.svg", nil); w.Header().Get("Content-Type") != "image/svg+xml" {
		t.Errorf("svg: %v", w.Header())
	}
	// Never the build's own records, the twins alone, or what is not there.
	for _, p := range []string{"/.vite/manifest.json", "/.gitkeep", "/assets/index-abc.js.br", "/assets/settings-abc.css.br", "/nothing", "/assets/none.js"} {
		if w := get(t, app, p, nil); w.Code != 404 {
			t.Errorf("%s: %d", p, w.Code)
		}
	}
	// Writes are not the app's.
	r := httptest.NewRequest("POST", "/", nil)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, r)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /: %d", rec.Code)
	}
}

func TestNotBuilt(t *testing.T) {
	app := New(fstest.MapFS{".gitkeep": {}})
	if app.Built() {
		t.Fatal("built with no index.html")
	}
	if w := get(t, app, "/", nil); w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "npm run build") {
		t.Errorf("not built: %d %q", w.Code, w.Body.String())
	}
	// The embedded build is there to be served, built or not: no panic.
	_ = Handler().Built()
}

func TestWrap(t *testing.T) {
	api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/internal/events":
			w.Header().Set("Content-Type", "text/event-stream")
			rc := http.NewResponseController(w)
			if err := rc.SetWriteDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
				t.Errorf("deadline through the wrapper: %v", err)
			}
			io.WriteString(w, "event: state\ndata: {}\n\n")
			rc.Flush()
		default:
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"ok":true}`)
		}
	})
	h := WrapApp(api, New(build()))
	// JSON, gzipped for a browser that takes it.
	w := get(t, h, "/v1/internal/sessions", map[string]string{"Accept-Encoding": "gzip"})
	if w.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("json not gzipped: %v", w.Header())
	}
	zr, _ := gzip.NewReader(w.Body)
	if b, _ := io.ReadAll(zr); string(b) != `{"ok":true}` {
		t.Errorf("json %q", b)
	}
	if w := get(t, h, "/v1/internal/sessions", nil); w.Header().Get("Content-Encoding") != "" || w.Body.String() != `{"ok":true}` {
		t.Errorf("plain json: %v %q", w.Header(), w.Body.String())
	}
	// The event stream never: gzip would hold events back.
	w = get(t, h, "/v1/internal/events?sub=list", map[string]string{"Accept-Encoding": "gzip"})
	if w.Header().Get("Content-Encoding") != "" || !strings.HasPrefix(w.Body.String(), "event: state") || !w.Flushed {
		t.Errorf("stream: %v %q flushed=%v", w.Header(), w.Body.String(), w.Flushed)
	}
	// The gate's paths go to it too; the rest is the app.
	if w := get(t, h, "/_ir/login", nil); w.Body.String() != `{"ok":true}` {
		t.Errorf("/_ir/: %q", w.Body.String())
	}
	if w := get(t, h, "/", nil); !strings.Contains(w.Body.String(), "/assets/index-abc.js") {
		t.Errorf("/: %q", w.Body.String())
	}
}

func TestAccepts(t *testing.T) {
	for _, c := range []struct {
		h, coding string
		want      bool
	}{
		{"gzip, deflate, br, zstd", "br", true},
		{"gzip;q=0.8", "gzip", true},
		{"br;q=0", "br", false},
		{"identity", "gzip", false},
		{"", "gzip", false},
		{"GZIP", "gzip", true},
	} {
		if got := accepts(c.h, c.coding); got != c.want {
			t.Errorf("accepts(%q, %q) = %v", c.h, c.coding, got)
		}
	}
}
