package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/hold"
	"github.com/kumoSleeping/iridium/internal/platform"
)

// A command line run at a session's machine and folder, without its agent (docs/api.md "命令"):
// one holder each, in a hold directory "sh-<id>", started by the machine's launcher like an
// agent's, so an executor runs it the same way and its output comes back the same way.

var (
	ErrNoShell = errors.New("command lines do not run on that machine")
	ErrNoRun   = errors.New("no such run")
)

// shellKeep: how long an ended run's output stays to be read.
const shellKeep = 10 * time.Minute

// shellRead: the most of a run's output one read gives.
const shellRead = 64 << 10

type shellRun struct {
	dir, unit string
	launcher  platform.Launcher
	ended     time.Time // when its end was seen; zero while it runs
}

type shells struct {
	mu   sync.Mutex
	runs map[string]*shellRun
}

var cdLine = regexp.MustCompile(`^cd(?:\s+(.*))?$`)

// Shell runs line at session id's machine, in cwd ("" the session's folder); "cd <dir>" runs
// nothing and answers the folder, once it is one there.
func (m *Manager) Shell(ctx context.Context, id, line, cwd string) (api.ShellStarted, error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return api.ShellStarted{}, errors.New("no command")
	}
	st, err := m.State(id)
	if err != nil {
		return api.ShellStarted{}, err
	}
	if cwd == "" {
		cwd = st.Session.Cwd
	}
	machine := st.Session.Machine
	launcher, env, osName, home, err := m.shellAt(machine)
	if err != nil {
		return api.ShellStarted{}, err
	}
	if c := cdLine.FindStringSubmatch(line); c != nil {
		dir := shellJoin(osName, home, cwd, strings.Trim(strings.TrimSpace(c[1]), `"'`))
		if err := m.isDir(ctx, machine, dir); err != nil {
			return api.ShellStarted{}, err
		}
		return api.ShellStarted{Cwd: dir}, nil
	}
	m.sweepShells()
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	run := "sh-" + hex.EncodeToString(b)
	unit := m.o.UnitPrefix + run
	dir := m.HoldDir(unit)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return api.ShellStarted{}, err
	}
	spec := hold.Spec{Argv: shellArgv(osName, line), Env: env, Cwd: cwd, StopGraceMS: 2000}
	if err := hold.Prepare(dir, spec); err != nil {
		return api.ShellStarted{}, err
	}
	u := platform.Unit{
		Name:        unit,
		Argv:        []string{m.o.Exe, "hold", dir},
		StopPost:    []string{m.o.Exe, "hold", "--stop-post", dir},
		StopTimeout: 10 * time.Second,
	}
	if err := launcher.Start(ctx, u); err != nil {
		_ = os.RemoveAll(dir)
		return api.ShellStarted{}, err
	}
	m.shell.mu.Lock()
	if m.shell.runs == nil {
		m.shell.runs = map[string]*shellRun{}
	}
	m.shell.runs[run] = &shellRun{dir: dir, unit: unit, launcher: launcher}
	m.shell.mu.Unlock()
	m.o.Logf("session %s: running a command line (%s)", id, run)
	return api.ShellStarted{Run: run, Cwd: cwd}, nil
}

// shellAt is how a command line runs at machine ("" this one): its launcher, environment, OS
// and home.
func (m *Manager) shellAt(machine string) (platform.Launcher, []string, string, string, error) {
	if machine == "" {
		home, _ := os.UserHomeDir()
		return m.o.Launcher, m.agentEnv(), runtime.GOOS, home, nil
	}
	if m.o.Remote == nil {
		return nil, nil, "", "", ErrOffline
	}
	mc := m.o.Remote.Machine(machine)
	if mc == nil {
		return nil, nil, "", "", ErrOffline
	}
	hi, ok := mc.Hello()
	if !ok {
		return nil, nil, "", "", ErrOffline
	}
	if hi.NoShell {
		return nil, nil, "", "", ErrNoShell
	}
	return mc.Launcher(), hi.Env, hi.OS, hi.Home, nil
}

func (m *Manager) isDir(ctx context.Context, machine, dir string) error {
	if machine == "" {
		fi, err := os.Stat(dir)
		if err != nil {
			return err
		}
		if !fi.IsDir() {
			return fmt.Errorf("%s: not a folder", dir)
		}
		return nil
	}
	mc := m.o.Remote.Machine(machine)
	if mc == nil {
		return ErrOffline
	}
	st, err := mc.Stat(ctx, dir)
	if err != nil {
		return err
	}
	if st.Type != "dir" {
		return fmt.Errorf("%s: not a folder", dir)
	}
	return nil
}

// shellArgv: a POSIX shell with its stderr in its stdout, or cmd.exe on Windows (whose stderr
// comes at the end, with the exit).
func shellArgv(osName, line string) []string {
	if osName == "windows" {
		return []string{"cmd.exe", "/d", "/s", "/c", line}
	}
	return []string{"/bin/sh", "-c", "exec 2>&1\n" + line}
}

// shellJoin is where "cd to" goes from cwd at a machine of osName: ~ is home, a relative path is
// under cwd.
func shellJoin(osName, home, cwd, to string) string {
	if osName == "windows" {
		switch {
		case to == "" || to == "~":
			return home
		case strings.HasPrefix(to, `~\`) || strings.HasPrefix(to, "~/"):
			return winClean(home + `\` + to[2:])
		case len(to) >= 2 && to[1] == ':', strings.HasPrefix(to, `\\`):
			return winClean(to)
		case strings.HasPrefix(to, `\`) || strings.HasPrefix(to, "/"):
			if len(cwd) >= 2 && cwd[1] == ':' {
				return winClean(cwd[:2] + to)
			}
			return winClean(to)
		}
		return winClean(cwd + `\` + to)
	}
	switch {
	case to == "" || to == "~":
		return home
	case strings.HasPrefix(to, "~/"):
		return path.Join(home, to[2:])
	case strings.HasPrefix(to, "/"):
		return path.Clean(to)
	}
	return path.Join(cwd, to)
}

// winClean cleans a Windows path with / or \ (path.Clean on its / form), keeping a drive's root.
func winClean(p string) string {
	q := path.Clean(strings.ReplaceAll(p, `\`, "/"))
	if len(q) == 2 && q[1] == ':' {
		q += "/"
	}
	return strings.ReplaceAll(q, "/", `\`)
}

// ShellRead is run's output from offset from.
func (m *Manager) ShellRead(run string, from int64) (api.ShellOutput, error) {
	m.shell.mu.Lock()
	r := m.shell.runs[run]
	m.shell.mu.Unlock()
	if r == nil {
		return api.ShellOutput{}, ErrNoRun
	}
	// The exit is looked at before the output, so output read after it is all there is.
	e, _ := hold.ReadExit(r.dir)
	f, err := os.Open(filepath.Join(r.dir, hold.FileOutLog))
	var data []byte
	if err == nil {
		defer f.Close()
		if _, err = f.Seek(from, io.SeekStart); err == nil {
			data, _ = io.ReadAll(io.LimitReader(f, shellRead))
		}
	}
	out := api.ShellOutput{Data: string(data), Next: from + int64(len(data))}
	if e != nil && len(data) < shellRead {
		out.Done, out.Code = true, e.Code
		if e.Signal != nil {
			out.Signal = *e.Signal
		}
		if b, _ := os.ReadFile(filepath.Join(r.dir, hold.FileErrLog)); len(b) > 0 {
			out.Err = string(b[max(0, len(b)-4096):])
		}
		m.shell.mu.Lock()
		if r.ended.IsZero() {
			r.ended = time.Now()
		}
		m.shell.mu.Unlock()
	}
	return out, nil
}

// ShellStop ends run, and what it started.
func (m *Manager) ShellStop(ctx context.Context, run string) error {
	m.shell.mu.Lock()
	r := m.shell.runs[run]
	m.shell.mu.Unlock()
	if r == nil {
		return ErrNoRun
	}
	if e, _ := hold.ReadExit(r.dir); e != nil {
		return nil
	}
	return r.launcher.Stop(ctx, r.unit)
}

// sweepShells forgets the runs that ended a while ago, and their directories.
func (m *Manager) sweepShells() {
	m.shell.mu.Lock()
	defer m.shell.mu.Unlock()
	for id, r := range m.shell.runs {
		if r.ended.IsZero() {
			if e, _ := hold.ReadExit(r.dir); e != nil {
				r.ended = time.Now() // ended unread
			}
			continue
		}
		if time.Since(r.ended) > shellKeep {
			_ = os.RemoveAll(r.dir)
			delete(m.shell.runs, id)
		}
	}
}

// sweepShellDirs removes the runs an earlier daemon left that have ended.
func (m *Manager) sweepShellDirs() {
	dirs, _ := filepath.Glob(filepath.Join(m.holdRoot(), "sh-*"))
	for _, d := range dirs {
		if e, _ := hold.ReadExit(d); e != nil {
			_ = os.RemoveAll(d)
		}
	}
}
