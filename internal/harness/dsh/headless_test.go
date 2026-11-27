package dsh

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"
)

// fakeHost is a host process that ends when killed or told to.
type fakeHost struct {
	end  chan struct{}
	once sync.Once
}

func (f *fakeHost) Wait() error { <-f.end; return nil }
func (f *fakeHost) Kill() error { f.once.Do(func() { close(f.end) }); return nil }

// headlessRig is a made-up DSH Desktop install (Windows' layout) and a dsh home whose desktop
// profile has the plugin.
type headlessRig struct {
	t       *testing.T
	exe     string
	home    string
	mu      sync.Mutex
	started [][]string
	envs    [][]string
	hosts   []*fakeHost
	yields  int
}

func newHeadlessRig(t *testing.T) *headlessRig {
	root := t.TempDir()
	r := &headlessRig{t: t, exe: filepath.Join(root, "App", "DeepSeek Harness.exe"), home: filepath.Join(root, ".dsh")}
	os.MkdirAll(filepath.Join(root, "App", "resources", "runtime", "primary-runtime"), 0o700)
	os.WriteFile(r.exe, nil, 0o700)
	os.WriteFile(filepath.Join(root, "App", "resources", "app.asar"), []byte("asar"), 0o600)
	os.MkdirAll(filepath.Join(r.home, "profiles", "desktop"), 0o700)
	r.plugin(true)
	return r
}

func (r *headlessRig) plugin(on bool) {
	deps := `{}`
	if on {
		deps = `{"dsh-remote-iridium":"0.2.90","oh-my-dsh":"1"}`
	}
	os.WriteFile(filepath.Join(r.home, "profiles", "desktop", "package.json"), []byte(`{"name":"dsh-profile-desktop","dependencies":`+deps+`}`), 0o600)
}

func (r *headlessRig) options() HeadlessOptions {
	return HeadlessOptions{
		Find: func() (string, bool) { return r.exe, true },
		Home: r.home, Env: []string{"PATH=/bin", "DSH_HOME=/elsewhere", "ELECTRON_RUN_AS_NODE=0"},
		Start: func(argv, env []string) (HeadlessProc, error) {
			r.mu.Lock()
			defer r.mu.Unlock()
			f := &fakeHost{end: make(chan struct{})}
			r.started, r.envs, r.hosts = append(r.started, argv), append(r.envs, env), append(r.hosts, f)
			return f, nil
		},
		Yield:   func(context.Context) error { r.mu.Lock(); r.yields++; r.mu.Unlock(); return nil },
		Connect: 2 * time.Second, Idle: time.Hour, Grace: time.Hour, Logf: r.t.Logf,
	}
}

func (r *headlessRig) count() (started, yields int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.started), r.yields
}

// host waits for the n-th host started.
func (r *headlessRig) host(n int) *fakeHost {
	r.t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		r.mu.Lock()
		if len(r.hosts) > n {
			defer r.mu.Unlock()
			return r.hosts[n]
		}
		r.mu.Unlock()
	}
	r.t.Fatalf("host %d not started", n)
	return nil
}

// The host is started as DSH Desktop starts it, once, and taken as up when its plugin says hello.
func TestHeadlessStart(t *testing.T) {
	r := newHeadlessRig(t)
	h := NewHeadless(r.options())
	defer h.Close()
	errs := make(chan error, 2)
	for range 2 {
		go func() { errs <- h.Ensure(context.Background()) }()
	}
	r.host(0)
	if h.Up() {
		t.Fatal("up before its hello")
	}
	h.Hello(true)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if n, _ := r.count(); n != 1 || !h.Up() {
		t.Fatalf("started %d, up %v", n, h.Up())
	}
	res := filepath.Join(filepath.Dir(r.exe), "resources")
	dsh := filepath.Join(res, "app.asar", "dsh")
	want := []string{r.exe, "--expose-internals", filepath.Join(dsh, "node_modules", "@deepseek-ai", "dsh-desktop-host", "lib", "index.js"), dsh,
		filepath.Join(r.home, "profiles", "desktop"), filepath.Join(res, "runtime", "primary-runtime"),
		filepath.Join(res, "runtime", "primary-runtime", "dependencies", "pnpm", "bin", "pnpm.mjs"), filepath.Join(res, "runtime", "bin")}
	if !slices.Equal(r.started[0], want) {
		t.Errorf("argv\n%q\nwant\n%q", r.started[0], want)
	}
	if env := r.envs[0]; !slices.Equal(env, []string{"PATH=/bin", "ELECTRON_RUN_AS_NODE=1", HeadlessEnv + "=1", "DSH_HOME=" + r.home}) {
		t.Errorf("env %q", env)
	}
	// It ended by itself: the next session starts another.
	r.host(0).Kill()
	for deadline := time.Now().Add(2 * time.Second); h.Up() && time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
	}
	go func() { errs <- h.Ensure(context.Background()) }()
	r.host(1)
	h.Hello(true)
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
}

// No host to be had: Desktop not found, not such a one, its profile without the plugin, or
// running unconnected; one whose plugin never connects is stopped.
func TestHeadlessNone(t *testing.T) {
	r := newHeadlessRig(t)
	o := r.options()
	o.Find = func() (string, bool) { return "", false }
	if err := NewHeadless(o).Ensure(context.Background()); !errors.Is(err, ErrNoDesktop) {
		t.Errorf("not found: %v", err)
	}
	o = r.options()
	o.Find = func() (string, bool) { return filepath.Join(t.TempDir(), "other.exe"), true }
	if err := NewHeadless(o).Ensure(context.Background()); !errors.Is(err, ErrNoDesktop) {
		t.Errorf("not a DSH Desktop: %v", err)
	}
	r.plugin(false)
	if err := NewHeadless(r.options()).Ensure(context.Background()); !errors.Is(err, ErrNoDesktop) {
		t.Errorf("no plugin in its profile: %v", err)
	}
	r.plugin(true)
	o = r.options()
	o.Running = func() bool { return true }
	if err := NewHeadless(o).Ensure(context.Background()); !errors.Is(err, ErrDesktopRuns) {
		t.Errorf("Desktop running, not connected: %v", err)
	}
	if n, _ := r.count(); n != 0 {
		t.Fatalf("started %d", n)
	}
	o = r.options()
	o.Connect = 50 * time.Millisecond
	h := NewHeadless(o)
	if err := h.Ensure(context.Background()); err == nil {
		t.Error("its plugin never connected: no error")
	}
	select {
	case <-r.host(0).end:
	case <-time.After(5 * time.Second):
		t.Error("not stopped")
	}
}

// DSH Desktop itself connected: no host is started; coming while one runs, that one is asked
// to yield and no more taken as up, and a session then is Desktop's.
func TestHeadlessYieldsToDesktop(t *testing.T) {
	r := newHeadlessRig(t)
	h := NewHeadless(r.options())
	defer h.Close()
	h.Hello(false)
	if err := h.Ensure(context.Background()); err != nil || !h.Desktop() {
		t.Fatal(err)
	}
	if n, _ := r.count(); n != 0 {
		t.Fatalf("started %d beside Desktop", n)
	}
	h.Bye(false)
	errs := make(chan error, 1)
	go func() { errs <- h.Ensure(context.Background()) }()
	r.host(0)
	h.Hello(true)
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	h.Hello(false)
	if _, y := r.count(); y != 1 || h.Up() {
		t.Fatalf("yields %d, up %v", y, h.Up())
	}
	select {
	case <-r.host(0).end:
		t.Fatal("killed: its turns were not waited for")
	default:
	}
	if err := h.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n, _ := r.count(); n != 1 {
		t.Errorf("started %d", n)
	}
	// Desktop gone again before the yielding one ended: the next session waits for its end, then
	// starts a new one.
	h.Bye(false)
	go func() { errs <- h.Ensure(context.Background()) }()
	time.Sleep(30 * time.Millisecond)
	if n, _ := r.count(); n != 1 {
		t.Fatalf("started beside the yielding one: %d", n)
	}
	r.host(0).Kill()
	r.host(1)
	h.Hello(true)
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
}

// Idle with no session at work, the host yields; one that cannot be asked is stopped at once.
func TestHeadlessIdle(t *testing.T) {
	r := newHeadlessRig(t)
	o := r.options()
	o.Idle = 40 * time.Millisecond
	busy := true
	var mu sync.Mutex
	o.Busy = func() bool { mu.Lock(); defer mu.Unlock(); return busy }
	h := NewHeadless(o)
	defer h.Close()
	errs := make(chan error, 1)
	go func() { errs <- h.Ensure(context.Background()) }()
	r.host(0)
	h.Hello(true)
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
	if _, y := r.count(); y != 0 {
		t.Fatal("yielded with a session at work")
	}
	mu.Lock()
	busy = false
	mu.Unlock()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		if _, y := r.count(); y == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("not asked to yield")
		}
	}
	r.host(0).Kill()

	o = r.options()
	o.Yield = func(context.Context) error { return errors.New("gone") }
	h2 := NewHeadless(o)
	defer h2.Close()
	go func() { errs <- h2.Ensure(context.Background()) }()
	f := r.host(1)
	h2.Hello(true)
	<-errs
	h2.Hello(false)
	select {
	case <-f.end:
	case <-time.After(5 * time.Second):
		t.Error("left writing beside Desktop")
	}
}
