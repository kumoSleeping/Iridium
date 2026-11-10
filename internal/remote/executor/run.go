package executor

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/kumoSleeping/iridium/internal/remote/wire"
)

// run (wire.CapRun): a command line run now, its output pushed as it comes (run.out), then its
// end (run.exit). A process each, no files: it lives as long as the connection that asked for it.

// runChunk: the most one run.out carries.
const runChunk = 32 << 10

// runLinger: how long after its process ends a run's output is still read, from what it left
// running in the background (sh -c "x &"), before that is ended too.
var runLinger = 2 * time.Second

// runGrace: how long run.stop waits after TERM before KILL.
var runGrace = 2 * time.Second

type running struct {
	c    *wire.Conn
	cmd  *exec.Cmd
	stop sync.Once
}

// runShell is the argv that runs line: a POSIX shell, or cmd.exe on Windows.
func runShell(line string) []string {
	if runtime.GOOS == "windows" {
		return []string{"cmd.exe", "/d", "/s", "/c", line}
	}
	return []string{"/bin/sh", "-c", line}
}

// startRun starts p on c, which hears of it.
func (e *Executor) startRun(c *wire.Conn, p wire.RunParams) (any, error) {
	if !e.o.Shell {
		return nil, &wire.Error{Code: wire.CodeDenied, Message: "command lines do not run here"}
	}
	if p.Run == "" || p.Line == "" || c == nil {
		return nil, bad("bad run")
	}
	e.mu.Lock()
	if e.runs[p.Run] != nil {
		e.mu.Unlock()
		return nil, &wire.Error{Code: wire.CodeExists, Message: p.Run}
	}
	r := &running{c: c}
	e.runs[p.Run] = r
	e.mu.Unlock()
	env := p.Env
	if env == nil {
		env = helloEnv(e.o.Env)
	}
	env = startEnv(env, e.o.Env)
	cwd := p.Cwd
	if cwd == "" {
		cwd, _ = os.UserHomeDir()
	}
	argv := runShell(p.Line)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env, cmd.Dir = env, cwd
	if lp := lookPath(argv[0], env); lp != "" {
		cmd.Path, cmd.Err = lp, nil
	}
	setGroup(cmd)
	or, ow, err := os.Pipe()
	if err != nil {
		return nil, e.endRun(p.Run, err)
	}
	er, ew, err := os.Pipe()
	if err != nil {
		or.Close()
		ow.Close()
		return nil, e.endRun(p.Run, err)
	}
	cmd.Stdout, cmd.Stderr = ow, ew
	err = cmd.Start()
	ow.Close()
	ew.Close()
	if err != nil {
		or.Close()
		er.Close()
		e.mu.Lock()
		delete(e.runs, p.Run)
		e.mu.Unlock()
		// Said as its end, as a holder's that cannot start: the run is there, and over.
		go func() {
			_ = c.Notify(context.Background(), "run.exit", wire.RunExit{Run: p.Run, Code: ptr(codeCannotStart), Error: err.Error()})
		}()
		return struct{}{}, nil
	}
	e.mu.Lock()
	r.cmd = cmd
	e.mu.Unlock()
	var pumps sync.WaitGroup
	pumps.Add(2)
	go e.pump(&pumps, c, p.Run, or, false)
	go e.pump(&pumps, c, p.Run, er, true)
	go func() {
		werr := cmd.Wait()
		read := make(chan struct{})
		go func() { pumps.Wait(); close(read) }()
		select {
		case <-read:
		case <-time.After(runLinger):
			killTree(cmd, true) // what it left in the background, holding its output
			select {
			case <-read:
			case <-time.After(time.Second):
				or.Close()
				er.Close()
				<-read
			}
		}
		or.Close()
		er.Close()
		x := exitOf(cmd.ProcessState, werr)
		e.mu.Lock()
		delete(e.runs, p.Run)
		e.mu.Unlock()
		_ = c.Notify(context.Background(), "run.exit", wire.RunExit{Run: p.Run, Code: x.Code, Signal: x.Signal})
	}()
	e.o.Logf("executor: running a command line (%s)", p.Run)
	return struct{}{}, nil
}

func (e *Executor) endRun(id string, err error) error {
	e.mu.Lock()
	delete(e.runs, id)
	e.mu.Unlock()
	return err
}

// pump pushes what f gives as run.out, whole UTF-8 characters each.
func (e *Executor) pump(wg *sync.WaitGroup, c *wire.Conn, id string, f *os.File, stderr bool) {
	defer wg.Done()
	buf := make([]byte, runChunk)
	rest := buf[:0]
	for {
		n, err := f.Read(buf[len(rest):])
		data := buf[:len(rest)+n]
		whole, part := cutRunes(data)
		if err != nil {
			whole, part = data, nil
		}
		if len(whole) > 0 {
			// A connection gone ends the run (dropRuns): read on till then, so it is not held.
			_ = c.Notify(context.Background(), "run.out", wire.RunOut{Run: id, Data: string(whole), Err: stderr})
		}
		rest = rest[:copy(buf, part)]
		if err != nil {
			return
		}
	}
}

// cutRunes splits b before a character its end cuts short.
func cutRunes(b []byte) ([]byte, []byte) {
	for i := len(b) - 1; i >= 0 && i >= len(b)-utf8.UTFMax; i-- {
		if !utf8.RuneStart(b[i]) {
			continue
		}
		if !utf8.FullRune(b[i:]) {
			return b[:i], b[i:]
		}
		break
	}
	return b, nil
}

// stopRun ends run id: TERM, then KILL after runGrace.
func (e *Executor) stopRun(id string) (any, error) {
	e.mu.Lock()
	r := e.runs[id]
	var cmd *exec.Cmd
	if r != nil {
		cmd = r.cmd
	}
	e.mu.Unlock()
	if cmd == nil {
		return struct{}{}, nil // over already
	}
	r.stop.Do(func() {
		killTree(cmd, false)
		go func() {
			time.Sleep(runGrace)
			e.mu.Lock()
			still := e.runs[id] == r
			e.mu.Unlock()
			if still {
				killTree(cmd, true)
			}
		}()
	})
	return struct{}{}, nil
}

// dropRuns ends the runs c asked for: the connection is gone.
func (e *Executor) dropRuns(c *wire.Conn) {
	e.mu.Lock()
	var cmds []*exec.Cmd
	for _, r := range e.runs {
		if r.c == c && r.cmd != nil {
			cmds = append(cmds, r.cmd)
		}
	}
	e.mu.Unlock()
	for _, cmd := range cmds {
		killTree(cmd, true)
	}
}
