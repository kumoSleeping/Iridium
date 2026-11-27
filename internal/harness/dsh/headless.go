package dsh

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// DSH Desktop without its window (docs/exec.md): with Desktop closed on this computer, its host
// process is started as Desktop starts it, with nothing to show it: Desktop's own runtime as a
// Node (ELECTRON_RUN_AS_NODE) running dsh-desktop-host on the desktop profile. That profile has
// the dsh plugin, which connects here as Desktop's would (Hello), so a session runs with what
// the user set up in Desktop (presets, plugins, models), Desktop open or not. The dsh command
// line refuses that profile ("managed exclusively by the Electron application") and the host
// takes no patch, so there is no such host without the plugin in the profile: ErrNoDesktop, and
// the session runs dsh's sdk profile as before.
//
// Two hosts must not write dsh's home at once: its JSON storages (the workspace registry) are
// each process's whole memory written over the file, no lock between processes (measured
// 2026-11-27: a session one filed was gone at the other's next write). So when Desktop itself
// comes, this one yields: asked to (Yield), it stops writing its storages and takes no new
// session at once, and ends when its turns are over. It yields the same way after Idle with
// no session at work.

var (
	// ErrNoDesktop: no DSH Desktop here whose profile has the dsh plugin.
	ErrNoDesktop = errors.New("dsh: no DSH Desktop here with the Iridium plugin")
	// ErrDesktopRuns: DSH Desktop runs here but is not connected (its plugin off): a second host
	// would write over its storages.
	ErrDesktopRuns = errors.New("dsh: DSH Desktop runs here, not connected")
)

// HeadlessEnv marks the host started here to its plugin: it says so in its hello, and yields
// when asked.
const HeadlessEnv = "IRIDIUM_DESKTOP_HEADLESS"

// HeadlessProc is the host's process.
type HeadlessProc interface {
	Wait() error // till it ends
	Kill() error
}

type HeadlessOptions struct {
	// Find is DSH Desktop's executable on this computer (desktop_path.go).
	Find func() (exe string, ok bool)
	Home string   // dsh's home: $DSH_HOME, else ~/.dsh
	Env  []string // what the host starts from; nil: this process's
	// Start runs argv in env with no window; nil: as a process of this one's.
	Start func(argv, env []string) (HeadlessProc, error)
	// Yield asks the connected host to stop writing its storages, take no new session and end
	// once idle (the plugin's desktop.freeze).
	Yield func(ctx context.Context) error
	// Running: a DSH Desktop of the user's runs here now, connected or not; nil: not known.
	Running func() bool
	// Busy: a dsh session is at work in the host; nil: never.
	Busy    func() bool
	Connect time.Duration // how long its plugin may take to connect; 0: a minute
	Idle    time.Duration // yields after this long with no session at work; 0: ten minutes
	Grace   time.Duration // killed this long after it was asked to yield; 0: an hour (a turn's length)
	Logf    func(format string, a ...any)
}

// Headless keeps at most one windowless host.
type Headless struct {
	o HeadlessOptions

	mu       sync.Mutex
	proc     HeadlessProc
	up       bool          // its plugin said hello
	yielding bool          // asked to yield, not ended yet
	desktop  bool          // DSH Desktop itself is connected
	change   chan struct{} // closed and replaced when any of the above changes
	idle     *time.Timer
	closed   bool
}

func NewHeadless(o HeadlessOptions) *Headless {
	if o.Connect == 0 {
		o.Connect = time.Minute
	}
	if o.Idle == 0 {
		o.Idle = 10 * time.Minute
	}
	if o.Grace == 0 {
		o.Grace = time.Hour
	}
	if o.Logf == nil {
		o.Logf = func(string, ...any) {}
	}
	if o.Start == nil {
		o.Start = startHeadless
	}
	return &Headless{o: o, change: make(chan struct{})}
}

// changed tells the waiters; h.mu held.
func (h *Headless) changed() {
	close(h.change)
	h.change = make(chan struct{})
}

// Ensure has a host on this computer for a session: DSH Desktop itself if it is connected, else
// the windowless one, started and waited for. ErrNoDesktop, ErrDesktopRuns: there is none to be
// had, the session runs dsh's sdk profile.
func (h *Headless) Ensure(ctx context.Context) error {
	deadline := time.Now().Add(h.o.Connect)
	for {
		h.mu.Lock()
		switch {
		case h.closed:
			h.mu.Unlock()
			return context.Canceled
		case h.desktop, h.up && !h.yielding:
			h.touch()
			h.mu.Unlock()
			return nil
		case h.proc == nil:
			if err := h.start(); err != nil {
				h.mu.Unlock()
				return err
			}
			deadline = time.Now().Add(h.o.Connect)
		}
		// Starting, or yielding (the next one starts once it ended).
		ch, yielding := h.change, h.yielding
		h.mu.Unlock()
		wait := time.Until(deadline)
		if yielding {
			wait = h.o.Grace
		}
		t := time.NewTimer(wait)
		select {
		case <-ch:
			t.Stop()
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
			if !yielding {
				h.stop("its plugin did not connect")
				return fmt.Errorf("dsh: DSH Desktop's host did not connect within %s", h.o.Connect)
			}
		}
	}
}

// start starts the host; h.mu held.
func (h *Headless) start() error {
	argv, err := h.argv()
	if err != nil {
		return err
	}
	if h.o.Running != nil && h.o.Running() {
		return ErrDesktopRuns
	}
	p, err := h.o.Start(argv, h.env())
	if err != nil {
		return fmt.Errorf("dsh: starting DSH Desktop's host: %w", err)
	}
	h.proc, h.up, h.yielding = p, false, false
	h.o.Logf("dsh: DSH Desktop's host started without a window (%s)", argv[0])
	go func() {
		err := p.Wait()
		h.mu.Lock()
		if h.proc == p {
			h.proc, h.up, h.yielding = nil, false, false
			if h.idle != nil {
				h.idle.Stop()
			}
			h.changed()
		}
		h.mu.Unlock()
		h.o.Logf("dsh: DSH Desktop's windowless host ended: %v", err)
	}()
	return nil
}

// Hello: an executor of DSH Desktop on this computer connected: the windowless host's
// (headless, as its hello says) or Desktop's own, for which the windowless one yields.
func (h *Headless) Hello(headless bool) {
	h.mu.Lock()
	if headless {
		h.up = h.proc != nil
		h.touch()
	} else {
		h.desktop = true
	}
	yield := !headless && h.proc != nil
	h.changed()
	h.mu.Unlock()
	if yield {
		h.yield("DSH Desktop came")
	}
}

// Bye: that executor is gone.
func (h *Headless) Bye(headless bool) {
	h.mu.Lock()
	if headless {
		h.up = false
	} else {
		h.desktop = false
	}
	h.changed()
	h.mu.Unlock()
}

// Desktop: DSH Desktop itself is connected.
func (h *Headless) Desktop() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.desktop
}

// Up: the windowless host runs and takes sessions.
func (h *Headless) Up() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.up && !h.yielding
}

// touch puts off the idle yield; h.mu held.
func (h *Headless) touch() {
	if h.proc == nil || h.yielding {
		return
	}
	if h.idle != nil {
		h.idle.Stop()
	}
	h.idle = time.AfterFunc(h.o.Idle, func() {
		if h.o.Busy != nil && h.o.Busy() {
			h.mu.Lock()
			h.touch()
			h.mu.Unlock()
			return
		}
		h.yield("idle")
	})
}

// yield asks the host to make way, and kills it if it has not ended after Grace (at once if it
// cannot be asked: it must not go on writing beside Desktop).
func (h *Headless) yield(why string) {
	h.mu.Lock()
	p := h.proc
	if p == nil || h.yielding {
		h.mu.Unlock()
		return
	}
	h.yielding = true
	if h.idle != nil {
		h.idle.Stop()
	}
	h.changed()
	h.mu.Unlock()
	h.o.Logf("dsh: DSH Desktop's windowless host yields: %s", why)
	grace := h.o.Grace
	if h.o.Yield == nil {
		grace = 0
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := h.o.Yield(ctx)
		cancel()
		if err != nil {
			h.o.Logf("dsh: DSH Desktop's windowless host could not be asked to yield, stopped: %v", err)
			grace = 0
		}
	}
	time.AfterFunc(grace, func() {
		h.mu.Lock()
		still := h.proc == p
		h.mu.Unlock()
		if still {
			_ = p.Kill()
		}
	})
}

// stop ends the host now.
func (h *Headless) stop(why string) {
	h.mu.Lock()
	p := h.proc
	h.mu.Unlock()
	if p != nil {
		h.o.Logf("dsh: DSH Desktop's windowless host stopped: %s", why)
		_ = p.Kill()
	}
}

// Close ends the host: Iridium is stopping.
func (h *Headless) Close() {
	h.mu.Lock()
	h.closed = true
	if h.idle != nil {
		h.idle.Stop()
	}
	h.changed()
	h.mu.Unlock()
	h.stop("Iridium stops")
}

func (h *Headless) env() []string {
	base := h.o.Env
	if base == nil {
		base = os.Environ()
	}
	set := map[string]string{"ELECTRON_RUN_AS_NODE": "1", HeadlessEnv: "1"}
	if h.o.Home != "" {
		set["DSH_HOME"] = h.o.Home
	}
	out := make([]string, 0, len(base)+len(set))
	for _, kv := range base {
		k, _, _ := strings.Cut(kv, "=")
		if runtime.GOOS == "windows" {
			k = strings.ToUpper(k)
		}
		if _, ours := set[k]; !ours {
			out = append(out, kv)
		}
	}
	for _, k := range []string{"ELECTRON_RUN_AS_NODE", HeadlessEnv, "DSH_HOME"} {
		if v, ok := set[k]; ok {
			out = append(out, k+"="+v)
		}
	}
	return out
}

// argv is how DSH Desktop starts its host (its main process's fork of dsh-desktop-host, as a
// running Desktop 0.2.2-rc.5 shows it): the runtime's dsh directory, the desktop profile, the
// primary runtime, its package manager and its bin. ErrNoDesktop if Desktop is not found, is
// not such a one, or its profile has no dsh plugin.
func (h *Headless) argv() ([]string, error) {
	if h.o.Find == nil {
		return nil, ErrNoDesktop
	}
	exe, ok := h.o.Find()
	if !ok {
		return nil, ErrNoDesktop
	}
	res, ok := desktopResources(exe)
	if !ok {
		return nil, fmt.Errorf("%w: %s is not a DSH Desktop this Iridium knows", ErrNoDesktop, exe)
	}
	profile := filepath.Join(h.o.Home, "profiles", "desktop")
	if !profileHasPlugin(profile) {
		return nil, fmt.Errorf("%w: %s", ErrNoDesktop, profile)
	}
	return hostArgv(exe, res, profile), nil
}

// hostArgv: see argv. The host and dsh are inside app.asar, which only Desktop's runtime reads.
func hostArgv(exe, resources, profile string) []string {
	dsh := filepath.Join(resources, "app.asar", "dsh")
	primary := filepath.Join(resources, "runtime", "primary-runtime")
	return []string{
		exe, "--expose-internals",
		filepath.Join(dsh, "node_modules", "@deepseek-ai", "dsh-desktop-host", "lib", "index.js"),
		dsh, profile, primary,
		filepath.Join(primary, "dependencies", "pnpm", "bin", "pnpm.mjs"),
		filepath.Join(resources, "runtime", "bin"),
	}
}

// desktopResources is the resources directory of the DSH Desktop whose executable is exe
// (beside it; in a macOS bundle, Contents/Resources), if it holds what the host is started
// with: app.asar and the primary runtime.
func desktopResources(exe string) (string, bool) {
	dir := filepath.Dir(exe)
	for _, res := range []string{filepath.Join(dir, "resources"), filepath.Join(filepath.Dir(dir), "Resources")} {
		asar, err := os.Stat(filepath.Join(res, "app.asar"))
		rt, err2 := os.Stat(filepath.Join(res, "runtime", "primary-runtime"))
		if err == nil && asar.Mode().IsRegular() && err2 == nil && rt.IsDir() {
			return res, true
		}
	}
	return "", false
}

// profileHasPlugin: the profile at dir depends on the dsh plugin (dsh-remote-iridium).
func profileHasPlugin(dir string) bool {
	b, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return false
	}
	var p struct {
		Dependencies map[string]string `json:"dependencies"`
	}
	if json.Unmarshal(b, &p) != nil {
		return false
	}
	_, ok := p.Dependencies["dsh-remote-iridium"]
	return ok
}

type headlessCmd struct{ cmd *exec.Cmd }

func (c headlessCmd) Wait() error { return c.cmd.Wait() }
func (c headlessCmd) Kill() error { return c.cmd.Process.Kill() }

// startHeadless runs argv in env as a process of this one's, with no window and nothing read
// from it.
func startHeadless(argv, env []string) (HeadlessProc, error) {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = env
	noWindow(cmd)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return headlessCmd{cmd}, nil
}
