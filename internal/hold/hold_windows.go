//go:build windows

package hold

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/kumoSleeping/iridium/internal/platform"
)

// Windows has no FIFOs in the file system: the input is a named pipe the holder serves, named
// after the hold directory. Each client (a daemon's Input) connects to an instance of it; whole
// lines from any of them go to the agent's stdin.

// pipeName is the input pipe of the hold directory dir.
func pipeName(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	sum := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(abs))))
	return `\\.\pipe\iridium-hold-` + hex.EncodeToString(sum[:16])
}

// pipeSecurity lets this user (and the system) alone at the pipe.
func pipeSecurity() (*windows.SecurityAttributes, error) {
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;GA;;;SY)(A;;GA;;;" + u.User.Sid.String() + ")")
	if err != nil {
		return nil, err
	}
	sa := &windows.SecurityAttributes{SecurityDescriptor: sd}
	sa.Length = uint32(unsafe.Sizeof(*sa))
	return sa, nil
}

func newInstance(name string, sa *windows.SecurityAttributes, first bool) (windows.Handle, error) {
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return 0, err
	}
	flags := uint32(windows.PIPE_ACCESS_INBOUND)
	if first {
		// Not a pipe someone else made first under this name.
		flags |= windows.FILE_FLAG_FIRST_PIPE_INSTANCE
	}
	mode := uint32(windows.PIPE_TYPE_BYTE | windows.PIPE_READMODE_BYTE | windows.PIPE_WAIT | windows.PIPE_REJECT_REMOTE_CLIENTS)
	return windows.CreateNamedPipe(p, flags, mode, windows.PIPE_UNLIMITED_INSTANCES, 0, 64<<10, 0, sa)
}

// pipeInput is what the holder reads: the lines of every client, one whole line at a time.
type pipeInput struct {
	name      string
	r         *io.PipeReader
	w         *io.PipeWriter
	mu        sync.Mutex
	conns     map[*os.File]struct{}
	closed    atomic.Bool
	closeOnce sync.Once
	done      chan struct{}
}

func openInput(dir string) (io.ReadCloser, error) {
	name := pipeName(dir)
	sa, err := pipeSecurity()
	if err != nil {
		return nil, err
	}
	h, err := newInstance(name, sa, true)
	if err != nil {
		return nil, fmt.Errorf("the input pipe %s: %w", name, err)
	}
	r, w := io.Pipe()
	p := &pipeInput{name: name, r: r, w: w, conns: map[*os.File]struct{}{}, done: make(chan struct{})}
	go p.serve(name, sa, h)
	return p, nil
}

// serve waits for a client on h, hands it to a reader, and opens the next instance.
func (p *pipeInput) serve(name string, sa *windows.SecurityAttributes, h windows.Handle) {
	defer close(p.done)
	for {
		if p.closed.Load() {
			windows.CloseHandle(h)
			return
		}
		err := windows.ConnectNamedPipe(h, nil)
		if p.closed.Load() {
			windows.CloseHandle(h)
			return
		}
		if err != nil && !errors.Is(err, windows.ERROR_PIPE_CONNECTED) {
			windows.CloseHandle(h)
			if p.closed.Load() {
				return
			}
			time.Sleep(100 * time.Millisecond)
		} else {
			f := os.NewFile(uintptr(h), name)
			p.mu.Lock()
			if p.closed.Load() {
				p.mu.Unlock()
				f.Close()
				return
			}
			p.conns[f] = struct{}{}
			p.mu.Unlock()
			go p.read(f)
		}
		if p.closed.Load() {
			return
		}
		if h, err = newInstance(name, sa, false); err != nil {
			p.w.CloseWithError(err)
			return
		}
	}
}

// read passes a client's whole lines on; a line it leaves unfinished is dropped.
func (p *pipeInput) read(f *os.File) {
	defer func() {
		p.mu.Lock()
		delete(p.conns, f)
		p.mu.Unlock()
		f.Close()
	}()
	r := bufio.NewReaderSize(f, 64<<10)
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			return
		}
		p.mu.Lock()
		_, err = p.w.Write(line)
		p.mu.Unlock()
		if err != nil {
			return
		}
	}
}

func (p *pipeInput) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if err != nil && p.closed.Load() {
		err = os.ErrClosed
	}
	return n, err
}

func (p *pipeInput) Close() error {
	var err error
	p.closeOnce.Do(func() {
		p.closed.Store(true)
		err = p.r.CloseWithError(os.ErrClosed)
		p.mu.Lock()
		for f := range p.conns {
			_ = f.Close()
		}
		p.mu.Unlock()
		// Wake serve()'s blocking ConnectNamedPipe so the last server handle closes cleanly and a
		// subsequent holder on the same directory can create FILE_FLAG_FIRST_PIPE_INSTANCE.
		if pName, perr := windows.UTF16PtrFromString(p.name); perr == nil {
			if c, cerr := windows.CreateFile(pName, windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, 0, 0); cerr == nil {
				_ = windows.CloseHandle(c)
			}
		}
		select {
		case <-p.done:
		case <-time.After(time.Second):
		}
	})
	return err
}

// groupAttr starts the agent in a process group of its own: Ctrl+Break reaches it alone.
func groupAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

func notifyStop(sigs chan<- os.Signal) { platform.NotifyStop(sigs) }

// group is the agent and all it starts: a job, which ends with the holder.
type group struct {
	pid int
	job *platform.Job
}

func newGroup(pid int, logf func(string, ...any)) *group {
	g := &group{pid: pid}
	job, err := platform.NewJob("", true)
	if err == nil {
		err = job.Add(pid)
		if err != nil {
			job.Close()
		} else {
			g.job = job
		}
	}
	if err != nil {
		logf("agent %d: no job: %v", pid, err)
	}
	return g
}

// term is Ctrl+Break to the agent's group: what Node and most console programs end on.
func (g *group) term() {
	_ = windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(g.pid))
}

func (g *group) kill() {
	if g.job != nil {
		_ = g.job.Kill(1)
		return
	}
	_ = platform.TerminatePID(g.pid, 1)
}

func (g *group) close() {
	if g.job != nil {
		g.job.Close()
	}
}

func exitOf(ps *os.ProcessState) Exit {
	return Exit{From: "agent", Code: ptr(ps.ExitCode())}
}

// SignalName is the signal's name as systemd writes it: KILL, TERM.
func SignalName(s syscall.Signal) string {
	switch s {
	case syscall.SIGKILL:
		return "KILL"
	case syscall.SIGTERM:
		return "TERM"
	case syscall.SIGINT:
		return "INT"
	case syscall.SIGHUP:
		return "HUP"
	}
	return strconv.Itoa(int(s))
}

// lookPath finds name as exec.LookPath would, but on the agent's Path, not the holder's, with
// PATHEXT's extensions (an npm shim is name.cmd).
func lookPath(name string, env []string) (string, error) {
	path, pathext := "", ""
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		switch {
		case strings.EqualFold(k, "PATH"):
			path = v
		case strings.EqualFold(k, "PATHEXT"):
			pathext = v
		}
	}
	exts := []string{""}
	if filepath.Ext(name) == "" {
		for _, e := range strings.Split(pathext, ";") {
			if e != "" {
				exts = append(exts, strings.ToLower(e))
			}
		}
		if len(exts) == 1 {
			exts = append(exts, ".com", ".exe", ".bat", ".cmd")
		}
	}
	find := func(base string) (string, bool) {
		for _, e := range exts {
			if st, err := os.Stat(base + e); err == nil && !st.IsDir() {
				return base + e, true
			}
		}
		return "", false
	}
	if strings.ContainsAny(name, `/\:`) {
		if p, ok := find(name); ok {
			return p, nil
		}
		return "", fmt.Errorf("%s not found", name)
	}
	for _, d := range filepath.SplitList(path) {
		if d == "" {
			continue
		}
		if p, ok := find(filepath.Join(d, name)); ok {
			return p, nil
		}
	}
	return "", fmt.Errorf("%s not found on PATH %q", name, path)
}

// prepareInput: the holder makes its pipe itself.
func prepareInput(string) error { return nil }

var procWaitNamedPipe = windows.NewLazySystemDLL("kernel32.dll").NewProc("WaitNamedPipeW")

// openWriter connects to the holder's pipe; none serving it is ErrNoHolder.
func openWriter(path string) (*os.File, error) {
	name := pipeName(filepath.Dir(path))
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	for try := 0; ; try++ {
		h, err := windows.CreateFile(p, windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, 0, 0)
		if err == nil {
			return os.NewFile(uintptr(h), name), nil
		}
		switch {
		case errors.Is(err, windows.ERROR_FILE_NOT_FOUND):
			return nil, ErrNoHolder
		case errors.Is(err, windows.ERROR_PIPE_BUSY) && try < 10:
			// Every instance taken a moment: the holder is opening the next one.
			_, _, _ = procWaitNamedPipe.Call(uintptr(unsafe.Pointer(p)), 200)
		default:
			return nil, err
		}
	}
}

// noReader: a write found no holder reading.
func noReader(err error) bool {
	return errors.Is(err, windows.ERROR_NO_DATA) || errors.Is(err, windows.ERROR_BROKEN_PIPE) ||
		errors.Is(err, windows.ERROR_PIPE_NOT_CONNECTED)
}
