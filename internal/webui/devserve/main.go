// Command devserve serves a web build from disk as the daemon serves its embedded one (package
// webui: hashed files kept for good, brotli and gzip twins, JSON gzipped) in front of a running
// daemon, for working on the page without building Go again after each npm run build:
//
//	go run ./internal/webui/devserve -api 127.0.0.1:7831 -listen 127.0.0.1:7832 -dir internal/webui/dist
//
// The daemon serves the app itself on its own address; this is only for the loop of changing it.
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
	flag.Parse()
	target := &url.URL{Scheme: "http", Host: *apiAddr}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(target)
			// The daemon's guard checks Host against Origin: both stay the page's.
			r.Out.Host = r.In.Host
		},
		FlushInterval: -1, // the event stream as it comes
	}
	var app http.Handler = webui.Handler()
	if *dir != "" {
		app = fresh(*dir)
	}
	log.Printf("devserve: http://%s → %s", *listen, target)
	log.Fatal(http.ListenAndServe(*listen, webui.WrapApp(proxy, app)))
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
