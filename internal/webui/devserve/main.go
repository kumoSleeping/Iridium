// Command devserve serves the web app as the daemon will (package webui: hashed files kept for
// good, brotli and gzip twins, JSON gzipped) in front of a running daemon, for trying the page and
// taking screenshots before webui is mounted in the daemon itself:
//
//	go run ./internal/webui/devserve -api 127.0.0.1:7831 -listen 127.0.0.1:7832 [-dir internal/webui/dist]
//
// -dir serves a build from disk, so a new npm run build shows without building Go again.
package main

import (
	"flag"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/kumoSleeping/iridium/internal/webui"
)

func main() {
	apiAddr := flag.String("api", "127.0.0.1:7810", "the daemon's address")
	listen := flag.String("listen", "127.0.0.1:7832", "where to serve")
	dir := flag.String("dir", "", "a build on disk; none: the embedded one")
	mocked := flag.Bool("mock", false, "answer the routes the daemon does not serve yet (mock.go)")
	home := flag.String("home", "", "-mock: the folder picker's ~")
	spaces := flag.String("workspaces", "", "-mock: workspaces, name=/root,...")
	me := flag.Bool("me", false, "-mock: a signed-in panel rather than 本机")
	update := flag.Bool("update", false, "-mock: an update is out")
	flag.Parse()
	if *home == "" {
		*home, _ = os.UserHomeDir()
	}
	target := &url.URL{Scheme: "http", Host: *apiAddr}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(target)
			// The daemon's guard checks Host against Origin: both stay the page's.
			r.Out.Host = r.In.Host
		},
		FlushInterval: -1, // the event stream as it comes
	}
	var m *mock
	if *mocked {
		m = newMock(*home, *spaces, *me, *update)
		proxy.ModifyResponse = func(r *http.Response) error {
			if r.Request.URL.Path == "/api/events" {
				r.Body = m.rewrite(r.Body)
			}
			return nil
		}
	}
	api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if m != nil && m.serve(w, r) {
			return
		}
		proxy.ServeHTTP(w, r)
	})
	var app http.Handler = webui.Handler()
	if *dir != "" {
		app = fresh(*dir)
	}
	log.Printf("devserve: http://%s → %s", *listen, target)
	log.Fatal(http.ListenAndServe(*listen, webui.WrapApp(api, app)))
}

// fresh serves the build in dir, read again whenever its index.html changes.
func fresh(dir string) http.Handler {
	var (
		mu   sync.Mutex
		app  *webui.App
		seen time.Time
	)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		if st, err := os.Stat(filepath.Join(dir, "index.html")); err == nil && !st.ModTime().Equal(seen) || app == nil {
			if st != nil {
				seen = st.ModTime()
			}
			app = webui.New(os.DirFS(dir))
		}
		a := app
		mu.Unlock()
		a.ServeHTTP(w, r)
	})
}
