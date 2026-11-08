//go:build linux || darwin

package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// Detached runs each holder under a supervisor of its own, `<Argv[0]> hold --unit <file>`: Argv[0]
// must be a binary whose `hold` passes these arguments to Supervise (iridium is). The supervisor
// runs in a session of its own and is left to init (launchd), so a daemon restarting or updating
// leaves it and its holder running, and a new daemon finds them by their files in Dir. It does for
// its holder what systemd does for a unit: on a stop it sends SIGTERM to the holder alone and,
// once StopTimeout passes, SIGKILL to whatever is left in its session; once the holder ends it
// ends what is left and runs StopPost. macOS uses it; Linux has Systemd.
//
// Dir holds, for a unit name: name.json (the unit, written by Start), name.lock (flocked while
// its supervisor runs: the unit exists exactly then), name.pid ("<supervisor> <holder>") and
// name.log (what the supervisor and the holder write to stderr).
type Detached struct {
	Dir string
}

// The arguments of `hold` that run a supervisor: the first stage, which Start runs and which
// starts the second in a new session and exits, so the second is nobody's child but init's.
const (
	unitStart = "--unit"
	unitRun   = "--unit-run"
)

type unitFile struct {
	Name          string   `json:"name"`
	Argv          []string `json:"argv"`
	StopPost      []string `json:"stopPost,omitempty"`
	StopTimeoutMS int64    `json:"stopTimeoutMs"`
}

func (d *Detached) path(name, ext string) string { return filepath.Join(d.Dir, name+ext) }

func validName(name string) error {
	if name == "" || strings.ContainsAny(name, "/\x00") || name[0] == '.' {
		return fmt.Errorf("bad unit name %q", name)
	}
	return nil
}

func (d *Detached) Start(ctx context.Context, u Unit) error {
	if err := validName(u.Name); err != nil {
		return err
	}
	if len(u.Argv) == 0 || !filepath.IsAbs(u.Argv[0]) {
		return errors.New("a unit needs an absolute path first")
	}
	if err := os.MkdirAll(d.Dir, 0o700); err != nil {
		return err
	}
	if st, err := d.State(ctx, u.Name); err != nil {
		return err
	} else if st.Exists {
		return fmt.Errorf("unit %s exists", u.Name)
	}
	timeout := u.StopTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	b, err := json.Marshal(unitFile{Name: u.Name, Argv: u.Argv, StopPost: u.StopPost, StopTimeoutMS: timeout.Milliseconds()})
	if err != nil {
		return err
	}
	file := d.path(u.Name, ".json")
	if err := writeAtomic(file, b); err != nil {
		return err
	}
	// The supervisor says "ok" on its stdout once the holder runs, or what went wrong, and
	// closes it; the first stage writes to the same pipe and is gone by then.
	r, w, err := os.Pipe()
	if err != nil {
		return err
	}
	defer r.Close()
	cmd := exec.Command(u.Argv[0], "hold", unitStart, file)
	cmd.Stdout, cmd.Stderr = w, w
	err = cmd.Start()
	w.Close()
	if err != nil {
		return err
	}
	deadline := time.Now().Add(10 * time.Second)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	_ = r.SetReadDeadline(deadline)
	out, rerr := io.ReadAll(io.LimitReader(r, 64<<10))
	werr := cmd.Wait()
	said := strings.TrimSpace(string(out))
	switch {
	case said == "ok":
		return nil
	case said != "":
		return fmt.Errorf("unit %s: %s", u.Name, said)
	case rerr != nil:
		return fmt.Errorf("unit %s: %w", u.Name, rerr)
	case werr != nil:
		return fmt.Errorf("unit %s: %w", u.Name, werr)
	}
	return fmt.Errorf("unit %s did not start", u.Name)
}

// pids is the supervisor's and the holder's pid while the unit exists; 0, 0 if it does not.
func (d *Detached) pids(name string) (super, main int, err error) {
	if err := validName(name); err != nil {
		return 0, 0, err
	}
	f, err := os.Open(d.path(name, ".lock"))
	if errors.Is(err, os.ErrNotExist) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	err = unix.Flock(int(f.Fd()), unix.LOCK_SH|unix.LOCK_NB)
	if err == nil {
		_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
		return 0, 0, nil // left by a supervisor that is gone
	}
	if !errors.Is(err, unix.EWOULDBLOCK) {
		return 0, 0, err
	}
	// Running: its pid file may be a moment behind its lock.
	for i := 0; ; i++ {
		b, err := os.ReadFile(d.path(name, ".pid"))
		if err == nil {
			if n, _ := fmt.Sscan(string(b), &super, &main); n == 2 {
				return super, main, nil
			}
		}
		if i == 50 {
			return 0, 0, fmt.Errorf("unit %s has no pid file", name)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (d *Detached) Stop(_ context.Context, name string) error {
	super, _, err := d.pids(name)
	if err != nil {
		return err
	}
	if super == 0 {
		return nil
	}
	return ignoreGone(unix.Kill(super, unix.SIGTERM))
}

func (d *Detached) Kill(_ context.Context, name, sig string, main bool) error {
	s := unix.SignalNum("SIG" + sig)
	if s == 0 {
		return errors.New("no signal " + sig)
	}
	super, holder, err := d.pids(name)
	if err != nil {
		return err
	}
	if super == 0 {
		return errors.New("no unit " + name)
	}
	if main {
		return ignoreGone(unix.Kill(holder, s))
	}
	if sid, err := unix.Getsid(super); err != nil || sid != super {
		return fmt.Errorf("unit %s: its supervisor %d leads no session", name, super)
	}
	signalSession(super, s)
	return nil
}

func (d *Detached) State(_ context.Context, name string) (State, error) {
	super, main, err := d.pids(name)
	if err != nil || super == 0 {
		return State{}, err
	}
	return State{Exists: true, Active: "active", MainPID: main}, nil
}

func (d *Detached) List(ctx context.Context, prefix string) ([]string, error) {
	locks, err := filepath.Glob(filepath.Join(d.Dir, "*.lock"))
	if err != nil {
		return nil, err
	}
	var names []string
	for _, l := range locks {
		n := strings.TrimSuffix(filepath.Base(l), ".lock")
		if !strings.HasPrefix(n, prefix) {
			continue
		}
		if st, err := d.State(ctx, n); err == nil && st.Exists {
			names = append(names, n)
		}
	}
	return names, nil
}

// Supervise is `hold --unit <file>` and `hold --unit-run <file>`, the two stages of a Detached
// unit's supervisor; ok is false for other arguments.
func Supervise(args []string) (code int, ok bool) {
	if len(args) != 2 {
		return 0, false
	}
	switch args[0] {
	case unitStart:
		return superStart(args[1]), true
	case unitRun:
		return superRun(args[1]), true
	}
	return 0, false
}

// superStart starts the supervisor proper in a session of its own, its stdout the pipe Start
// reads, and exits.
func superStart(file string) int {
	exe, err := os.Executable()
	if err != nil {
		fmt.Println(err)
		return 1
	}
	logPath := strings.TrimSuffix(file, ".json") + ".log"
	log, err := os.OpenFile(logPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		fmt.Println(err)
		return 1
	}
	defer log.Close()
	cmd := exec.Command(exe, "hold", unitRun, file)
	cmd.Stdout, cmd.Stderr = os.Stdout, log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		fmt.Println(err)
		return 1
	}
	return 0
}

// superRun is the supervisor: it holds the unit's lock while it runs.
func superRun(file string) int {
	base := strings.TrimSuffix(file, ".json")
	fail := func(err error) int {
		fmt.Println(err)
		return 1
	}
	b, err := os.ReadFile(file)
	if err != nil {
		return fail(err)
	}
	var u unitFile
	if err := json.Unmarshal(b, &u); err != nil {
		return fail(err)
	}
	if len(u.Argv) == 0 {
		return fail(errors.New("no argv"))
	}
	self := os.Getpid()
	if sid, err := unix.Getsid(0); err != nil || sid != self {
		return fail(errors.New("the supervisor must lead a session of its own"))
	}
	lock, err := os.OpenFile(base+".lock", os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return fail(err)
	}
	defer lock.Close() // held open, and so locked, till the end
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return fail(fmt.Errorf("unit %s exists", u.Name))
	}
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT)
	signal.Ignore(syscall.SIGHUP, syscall.SIGPIPE)
	holder := exec.Command(u.Argv[0], u.Argv[1:]...)
	holder.Stderr = os.Stderr
	if err := holder.Start(); err != nil {
		os.Remove(base + ".lock")
		return fail(err)
	}
	if err := writeAtomic(base+".pid", []byte(fmt.Sprintf("%d %d\n", self, holder.Process.Pid))); err != nil {
		fmt.Fprintf(os.Stderr, "iridium unit %s: %v\n", u.Name, err)
	}
	fmt.Println("ok")
	os.Stdout.Close()

	done := make(chan struct{})
	go func() {
		_ = holder.Wait()
		close(done)
	}()
	timeout := time.Duration(u.StopTimeoutMS) * time.Millisecond
	var kill <-chan time.Time
	for running := true; running; {
		select {
		case <-done:
			running = false
		case <-sigs:
			if kill == nil {
				_ = holder.Process.Signal(syscall.SIGTERM)
				kill = time.After(timeout)
			}
		case <-kill:
			signalSession(self, unix.SIGKILL)
		}
	}
	// What the holder left: SIGTERM, and SIGKILL to what is still there after the stop timeout.
	if signalSession(self, unix.SIGTERM) > 0 {
		for end := time.Now().Add(min(timeout, 5*time.Second)); time.Now().Before(end) && signalSession(self, 0) > 0; {
			time.Sleep(50 * time.Millisecond)
		}
		signalSession(self, unix.SIGKILL)
	}
	if len(u.StopPost) > 0 {
		code, status := "exited", strconv.Itoa(holder.ProcessState.ExitCode())
		if ws, ok := holder.ProcessState.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			code, status = "killed", strings.TrimPrefix(unix.SignalName(ws.Signal()), "SIG")
			if ws.CoreDump() {
				code = "dumped"
			}
		}
		post := exec.Command(u.StopPost[0], u.StopPost[1:]...)
		post.Env = append(os.Environ(), "EXIT_CODE="+code, "EXIT_STATUS="+status)
		post.Stdout, post.Stderr = os.Stderr, os.Stderr
		if err := post.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "iridium unit %s: stop-post: %v\n", u.Name, err)
		}
	}
	os.Remove(base + ".pid")
	os.Remove(file)
	if st, err := os.Stat(base + ".log"); err == nil && st.Size() == 0 {
		os.Remove(base + ".log")
	}
	os.Remove(base + ".lock")
	return 0
}

// signalSession sends sig to every process of session sid but its leader, and says how many
// there were; sig 0 only counts them.
func signalSession(sid int, sig syscall.Signal) int {
	n := 0
	for _, pid := range allPIDs() {
		if pid == sid || pid <= 1 {
			continue
		}
		if s, err := unix.Getsid(pid); err == nil && s == sid {
			if unix.Kill(pid, sig) == nil {
				n++
			}
		}
	}
	return n
}

func ignoreGone(err error) error {
	if errors.Is(err, unix.ESRCH) {
		return nil
	}
	return err
}

func writeAtomic(path string, b []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
