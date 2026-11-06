package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kumoSleeping/iridium/internal/api"
)

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"1.2.0", "1.1.9", true}, {"v1.10.0", "1.9.0", true}, {"1.2", "1.2.0", false}, {"1.2.0", "1.2.0", false},
		{"1.2.0", "1.2.0-rc.1", true}, {"1.2.0-rc.2", "1.2.0-rc.1", true}, {"1.2.0-rc.10", "1.2.0-rc.9", true},
		{"1.2.0-rc.1", "1.2.0", false}, {"1.1.0", "1.2.0", false}, {"1.0.0", "dev", false}, {"dev", "1.0.0", false},
		{"1.0.0+build", "0.9", true},
	} {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

type source struct {
	manifest Manifest
	binary   []byte
}

func serve(t *testing.T, s *source) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest.json":
			json.NewEncoder(w).Encode(s.manifest)
		case "/iridium":
			w.Write(s.binary)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func script(version string) []byte { return []byte("#!/bin/sh\necho iridium " + version + "\n") }

func sum(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func TestApply(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "iridium")
	os.WriteFile(exe, []byte("old"), 0o755)
	src := &source{binary: script("1.2.0")}
	srv := serve(t, src)
	src.manifest = Manifest{Version: "1.2.0", Notes: "faster", URL: srv.URL + "/iridium", SHA256: strings.Repeat("0", 64)}
	restarts := 0
	var published []api.UpdateState
	u := New(Options{Source: srv.URL + "/latest.json", Current: "1.1.0", Exe: exe, Restart: func() error { restarts++; return nil },
		Publish: func(s api.UpdateState) { published = append(published, s) }, Logf: t.Logf})
	ctx := context.Background()

	st, err := u.Check(ctx)
	if err != nil || !st.Available || st.Latest != "1.2.0" || st.Notes != "faster" || !st.Source || st.CheckedAt == 0 {
		t.Fatalf("check: %+v %v", st, err)
	}
	// A wrong sha256: nothing replaced, nothing left behind.
	if st, err := u.Apply(ctx, false); err == nil || !strings.Contains(err.Error(), "sha256") || st.Phase != "failed" {
		t.Fatalf("bad sum: %+v %v", st, err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "old" || restarts != 0 {
		t.Fatalf("replaced after a bad sum: %q", b)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".iridium-update-*")); len(left) != 0 {
		t.Fatalf("left %v", left)
	}
	// The right sum, but a binary that says another version.
	src.binary = script("1.1.5")
	src.manifest.SHA256 = sum(src.binary)
	if _, err := u.Apply(ctx, false); err == nil || !strings.Contains(err.Error(), "says it is") {
		t.Fatalf("wrong version: %v", err)
	}
	// The real thing, for this platform.
	src.binary = script("1.2.0")
	src.manifest.SHA256 = ""
	src.manifest.Platforms = map[string]Asset{platform(): {URL: srv.URL + "/iridium", SHA256: strings.ToUpper(sum(src.binary))}}
	st, err = u.Apply(ctx, false)
	if err != nil || st.Phase != "restarting" || restarts != 1 {
		t.Fatalf("apply: %+v %v", st, err)
	}
	if b, _ := os.ReadFile(exe); string(b) != string(src.binary) {
		t.Fatalf("not replaced: %q", b)
	}
	if fi, _ := os.Stat(exe); fi.Mode().Perm() != 0o755 {
		t.Errorf("mode %v", fi.Mode())
	}
	if len(published) == 0 || published[len(published)-1].Phase != "restarting" {
		t.Errorf("published %+v", published)
	}
	// Nothing newer: nothing done, unless forced.
	u2 := New(Options{Source: srv.URL + "/latest.json", Current: "1.2.0", Exe: exe, Restart: func() error { restarts++; return nil }})
	if _, err := u2.Apply(ctx, false); !errors.Is(err, ErrUpToDate) || restarts != 1 {
		t.Fatalf("up to date: %v", err)
	}
	if _, err := u2.Apply(ctx, true); err != nil || restarts != 2 {
		t.Fatalf("forced: %v", err)
	}
	// No source: no checks.
	if _, err := New(Options{Current: "1.0.0"}).Check(ctx); err == nil {
		t.Error("checked without a source")
	}
}

func platform() string { return runtime.GOOS + "-" + runtime.GOARCH }

// A GitHub release: the archive found by its name, its sum in SHA256SUMS, downloaded ahead with
// auto on, so Apply only swaps it in.
func TestGitHubAuto(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "iridium")
	os.WriteFile(exe, []byte("old"), 0o755)
	archive := tarGz(t, "iridium", script("v1.3.0"))
	name := "iridium-" + platform() + ".tar.gz"
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/o/r/releases/latest":
			fmt.Fprintf(w, `{"tag_name":"v1.3.0","body":"notes\n<!-- iridium:assets -->\n| sizes |","assets":[
				{"name":"%s","browser_download_url":"%s/dl/%s","size":%d},
				{"name":"iridium-plan9-mips","browser_download_url":"%s/dl/x","size":1},
				{"name":"SHA256SUMS","browser_download_url":"%s/dl/SHA256SUMS","size":1}]}`,
				name, srv.URL, name, len(archive), srv.URL, srv.URL)
		case "/dl/SHA256SUMS":
			fmt.Fprintf(w, "%s  %s\n", sum(archive), name)
		case "/dl/" + name:
			w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	if repo, ok := GitHubRepo("https://github.com/o/r/releases"); !ok || repo != "o/r" {
		t.Fatalf("repo %q", repo)
	}
	restarts := 0
	u := New(Options{Source: "github:o/r", GitHubAPI: srv.URL, Current: "v1.2.0", Auto: true, Exe: exe, Restart: func() error { restarts++; return nil }, Logf: t.Logf})
	ctx := context.Background()
	st, err := u.Check(ctx)
	if err != nil || !st.Available || st.Latest != "v1.3.0" || st.Notes != "notes" || st.Size != int64(len(archive)) {
		t.Fatalf("check: %+v %v", st, err)
	}
	u.prep.Lock() // the download ahead is done
	u.prep.Unlock()
	if st := u.State(); !st.Ready || st.Phase != "idle" {
		t.Fatalf("not ready: %+v", st)
	}
	if st, err := u.Apply(ctx, false); err != nil || st.Phase != "restarting" || restarts != 1 {
		t.Fatalf("apply: %+v %v", st, err)
	}
	if b, _ := os.ReadFile(exe); string(b) != string(script("v1.3.0")) {
		t.Fatalf("not replaced: %q", b)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".iridium-update-*")); len(left) != 0 {
		t.Fatalf("left %v", left)
	}
}

func tarGz(t *testing.T, name string, b []byte) []byte {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(b)), Typeflag: tar.TypeReg})
	tw.Write(b)
	tw.Close()
	zw.Close()
	return buf.Bytes()
}
