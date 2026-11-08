package remote_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/hold"
	"github.com/kumoSleeping/iridium/internal/platform"
	"github.com/kumoSleeping/iridium/internal/remote"
	"github.com/kumoSleeping/iridium/internal/remote/executor"
	"github.com/kumoSleeping/iridium/internal/remote/wire"
)

// rig is a hub behind an HTTP server, and an executor connected to it.
type rig struct {
	t      *testing.T
	root   string // hold directories, by unit
	srv    *httptest.Server
	mu     sync.Mutex
	hub    *remote.Hub
	kind   string // the executor: "go" (internal/remote/executor) or "node" (the dsh plugin's)
	drop   func()
	stop   func()
	state  string
	refuse bool
}

func (r *rig) setRefuse(v bool) {
	r.mu.Lock()
	r.refuse = v
	r.mu.Unlock()
}

// kinds runs test with each executor: the Go one, and the dsh plugin's if node is there.
func kinds(t *testing.T, test func(t *testing.T, kind string)) {
	for _, k := range []string{"go", "node"} {
		t.Run(k, func(t *testing.T) {
			if _, err := exec.LookPath("node"); k == "node" && err != nil {
				t.Skip("no node")
			}
			test(t, k)
		})
	}
}

func newRig(t *testing.T, kind string) *rig {
	t.Helper()
	r := &rig{t: t, root: t.TempDir(), state: t.TempDir(), kind: kind}
	r.hub = r.newHub()
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		r.mu.Lock()
		h, refuse := r.hub, r.refuse
		r.mu.Unlock()
		if refuse {
			http.Error(w, "away", http.StatusServiceUnavailable)
			return
		}
		h.Serve(w, req, "m1", "pc")
	}))
	t.Cleanup(r.srv.Close)
	r.startExecutor()
	t.Cleanup(func() { r.stop() })
	r.waitOnline()
	return r
}

func (r *rig) newHub() *remote.Hub {
	return &remote.Hub{Dir: func(unit string) string { return filepath.Join(r.root, unit) }, Logf: r.t.Logf}
}

func (r *rig) startExecutor() {
	url := "ws" + strings.TrimPrefix(r.srv.URL, "http") + "/api/exec"
	if r.kind == "node" {
		cmd := exec.Command("node", "../../plugins/dsh-remote-iridium/test/run.mjs", url, "tok", r.state)
		cmd.Stderr = testWriter{r.t}
		if err := cmd.Start(); err != nil {
			r.t.Fatal(err)
		}
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		r.drop = func() { _ = cmd.Process.Signal(syscall.SIGUSR1) }
		r.stop = func() {
			_ = cmd.Process.Signal(syscall.SIGTERM)
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				_ = cmd.Process.Kill()
				<-done
			}
		}
		return
	}
	exe, err := executor.New(executor.Options{URL: url, Token: "tok", State: r.state, Logf: r.t.Logf})
	if err != nil {
		r.t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = exe.Run(ctx); close(done) }()
	r.drop, r.stop = exe.Drop, func() { cancel(); <-done; exe.Close() }
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(b []byte) (int, error) {
	w.t.Log(strings.TrimRight(string(b), "\n"))
	return len(b), nil
}

func (r *rig) machine() *remote.Machine {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.hub.Machine("m1")
}

func (r *rig) waitOnline() {
	r.t.Helper()
	waitFor(r.t, "online", func() bool { _, ok := r.machine().Hello(); return ok })
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for end := time.Now().Add(10 * time.Second); !ok(); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func readOut(dir string) string {
	b, _ := os.ReadFile(filepath.Join(dir, hold.FileOutLog))
	return string(b)
}

// start prepares unit's hold directory with a shell echoing its input, and starts it there.
func (r *rig) start(unit string) (string, *hold.Input) {
	r.t.Helper()
	dir := filepath.Join(r.root, unit)
	spec := hold.Spec{Argv: []string{"/bin/sh", "-c", `echo ready; while read l; do echo "got $l"; done; echo bye`}, Env: os.Environ(), Cwd: r.state}
	if err := hold.Prepare(dir, spec); err != nil {
		r.t.Fatal(err)
	}
	if err := r.machine().Launcher().Start(context.Background(), platform.Unit{Name: unit}); err != nil {
		r.t.Fatal(err)
	}
	in := hold.NewInput(dir)
	waitFor(r.t, "ready", func() bool { return strings.Contains(readOut(dir), "ready\n") })
	return dir, in
}

func TestRelay(t *testing.T) { kinds(t, testRelay) }

func testRelay(t *testing.T, kind string) {
	r := newRig(t, kind)
	dir, in := r.start("u1")
	for _, l := range []string{"one", "two"} {
		if err := in.WriteLine([]byte(l)); err != nil {
			t.Fatal(err)
		}
	}
	waitFor(t, "two", func() bool { return strings.Contains(readOut(dir), "got two\n") })

	// The connection goes and comes back: input written meanwhile goes after it, output is not
	// lost or doubled.
	r.setRefuse(true)
	r.drop()
	waitFor(t, "offline", func() bool { _, ok := r.machine().Hello(); return !ok })
	st, _ := r.machine().Launcher().State(context.Background(), "u1")
	if !st.Exists || st.Active != "disconnected" {
		t.Fatalf("state while away: %+v", st)
	}
	if err := in.WriteLine([]byte("three")); err != nil {
		t.Fatal(err)
	}
	r.setRefuse(false)
	r.waitOnline()
	waitFor(t, "three", func() bool { return strings.Contains(readOut(dir), "got three\n") })

	// This side restarts: a new hub takes the holder up from its files.
	r.mu.Lock()
	r.hub = r.newHub()
	r.mu.Unlock()
	r.drop()
	r.waitOnline()
	st, _ = r.machine().Launcher().State(context.Background(), "u1")
	if !st.Exists {
		t.Fatal("not taken up")
	}
	if err := in.WriteLine([]byte("four")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "four", func() bool { return strings.Contains(readOut(dir), "got four\n") })

	if err := r.machine().Launcher().Stop(context.Background(), "u1"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "exit", func() bool { e, _ := hold.ReadExit(dir); return e != nil })
	e, _ := hold.ReadExit(dir)
	if e.Code == nil || *e.Code != 0 {
		t.Fatalf("exit %+v", e)
	}
	if got, want := readOut(dir), "ready\ngot one\ngot two\ngot three\ngot four\nbye\n"; got != want {
		t.Fatalf("out.log %q, want %q", got, want)
	}
	if st, _ := r.machine().Launcher().State(context.Background(), "u1"); st.Exists {
		t.Fatal("still there after its exit")
	}
	// The executor forgets it.
	waitFor(t, "forgotten", func() bool {
		ents, _ := os.ReadDir(filepath.Join(r.state, "holds"))
		return len(ents) == 0
	})
}

// An executor that restarts loses its processes (their pipes went with it): the holder ends, not
// cleanly.
func TestExecutorRestart(t *testing.T) { kinds(t, testExecutorRestart) }

func testExecutorRestart(t *testing.T, kind string) {
	r := newRig(t, kind)
	dir, _ := r.start("u3")
	r.stop()
	r.startExecutor()
	waitFor(t, "exit", func() bool { e, _ := hold.ReadExit(dir); return e != nil })
	// Gone with it (the new one finds it unended), or killed as it stopped: a crash either way.
	if e, _ := hold.ReadExit(dir); e.From != "holder" && e.Signal == nil {
		t.Fatalf("exit %+v", e)
	}
}

func TestCannotStart(t *testing.T) { kinds(t, testCannotStart) }

func testCannotStart(t *testing.T, kind string) {
	r := newRig(t, kind)
	dir := filepath.Join(r.root, "u2")
	if err := hold.Prepare(dir, hold.Spec{Argv: []string{"/no/such/program"}, Env: os.Environ(), Cwd: r.state}); err != nil {
		t.Fatal(err)
	}
	if err := r.machine().Launcher().Start(context.Background(), platform.Unit{Name: "u2"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "exit", func() bool { e, _ := hold.ReadExit(dir); return e != nil })
	e, _ := hold.ReadExit(dir)
	if e.Code == nil || *e.Code != hold.CodeCannotStart {
		t.Fatalf("exit %+v", e)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, hold.FileErrLog)); !strings.Contains(string(b), "cannot start") {
		t.Fatalf("err.log %q", b)
	}
}

func TestMirror(t *testing.T) { kinds(t, testMirror) }

func testMirror(t *testing.T, kind string) {
	r := newRig(t, kind)
	ctx := context.Background()
	src := filepath.Join(r.state, "t.jsonl")
	local := filepath.Join(r.root, "mirror", "t.jsonl")
	m := r.machine()
	if err := os.WriteFile(src, []byte("a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := m.Mirror(ctx, src, local); err != nil {
		t.Fatal(err)
	}
	f, _ := os.OpenFile(src, os.O_WRONLY|os.O_APPEND, 0)
	_, _ = f.WriteString(strings.Repeat("b", 5000) + "\n")
	f.Close()
	if err := m.Mirror(ctx, src, local); err != nil {
		t.Fatal(err)
	}
	if a, b := mustRead(t, src), mustRead(t, local); a != b {
		t.Fatal("not the same after growing")
	}
	// Rewritten, longer: fetched whole again.
	if err := os.WriteFile(src, []byte(strings.Repeat("c", 6000)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := m.Mirror(ctx, src, local); err != nil {
		t.Fatal(err)
	}
	if a, b := mustRead(t, src), mustRead(t, local); a != b {
		t.Fatal("not the same after a rewrite")
	}
	// Shrunk.
	if err := os.WriteFile(src, []byte("d\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := m.Mirror(ctx, src, local); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, local); got != "d\n" {
		t.Fatalf("after shrinking %q", got)
	}
	if _, err := m.Stat(ctx, filepath.Join(os.Getenv("HOME"), ".ssh", "id")); !wire.IsCode(err, wire.CodeDenied) {
		t.Fatalf("~/.ssh: %v", err)
	}
}

func mustRead(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
