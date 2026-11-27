// Package hold is the holder: one small process per agent process, started away from the daemon
// (systemd-run --user on Linux, a supervisor of its own on macOS) so that restarting Iridium never cuts a turn short. It uses files
// only (plan "运行"): a named pipe it reads lines from and writes to the agent's stdin, and the
// files below in the hold directory. It never parses the agent's protocol.
//
// The holder's half is Run and StopPost; the daemon's half (Prepare, Input, Tail, ReadExit) is in
// client.go. Both halves share the file names and formats defined here, which only ever gain
// fields, since a holder started by an older binary may outlive it.
package hold

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kumoSleeping/iridium/internal/platform"
)

// Files of a hold directory.
const (
	FileSpec   = "spec.json" // what to run, written by the daemon before the holder starts (0600)
	FileIn     = "in"        // named pipe: lines for the agent's stdin (on Windows a pipe of its own, pipeName)
	FileInLog  = "in.log"    // each line written to stdin, after the size of out.log at the time and a space
	FileOutLog = "out.log"   // the agent's stdout, byte for byte; appended to by every agent process
	FileErrLog = "err.log"   // the agent's stderr, and the holder's own complaints
	FileExit   = "exit"      // one JSON line: how the agent (or the holder) ended
	FilePID    = "pid"       // the agent's pid, once it runs
	FileLock   = "lock"      // flocked while a holder runs here
)

// Spec is what the holder runs. Env is the agent's whole environment: none of the holder's own
// is passed down. Argv[0] without a slash is looked up on Env's PATH.
type Spec struct {
	Argv []string `json:"argv"`
	Env  []string `json:"env"`
	Cwd  string   `json:"cwd"`
	// How long SIGTERM waits after closing stdin before it signals the process group, and again
	// before SIGKILL. 0: DefaultStopGrace.
	StopGraceMS int `json:"stopGraceMs,omitempty"`
}

// DefaultStopGrace leaves Claude time to finish its transcript after stdin closes (Rhodium §6.2).
const DefaultStopGrace = 5 * time.Second

// Exit is the exit file. From is "agent" when the holder saw its agent end (Code, or Signal if a
// signal killed it), "holder" when the holder itself ended without writing one (StopPost, from
// systemd's view of the holder). Signal names go without "SIG", as systemd writes them: KILL, TERM.
type Exit struct {
	From   string  `json:"from"`
	Code   *int    `json:"code"`
	Signal *string `json:"signal"`
}

// CodeCannotStart is the agent's code when the holder could not start it (no program, bad spec).
const CodeCannotStart = 127

// Main is `iridium hold ...`: `hold <dir>` holds one agent process; `hold --stop-post <dir>` is the
// unit's ExecStopPost; `hold --unit <file>` is the supervisor of a platform.Detached unit.
func Main(args []string) int {
	if code, ok := platform.Supervise(args); ok {
		return code
	}
	switch {
	case len(args) == 1 && !strings.HasPrefix(args[0], "-"):
		return Run(args[0])
	case len(args) == 2 && args[0] == "--stop-post":
		return StopPost(args[1])
	}
	fmt.Fprintln(os.Stderr, "usage: iridium hold <dir> | iridium hold --stop-post <dir>")
	return 2
}

// OpenInput opens the hold directory's input channel (a Unix FIFO or a Windows named pipe).
func OpenInput(dir string) (io.ReadCloser, error) { return openInput(dir) }

// Run holds one agent process in dir until it exits, then writes the exit file. SIGTERM closes the
// agent's stdin, so an idle agent exits by itself; if it has not after the grace, its process
// group gets SIGTERM, then SIGKILL. The holder's own exit code is 0 whenever it wrote the exit file.
func Run(dir string) int {
	errLog, err := os.OpenFile(filepath.Join(dir, FileErrLog), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		fmt.Fprintf(os.Stderr, "iridium-hold: %v\n", err)
		return 1
	}
	logf := func(format string, a ...any) { fmt.Fprintf(errLog, "iridium-hold: "+format+"\n", a...) }

	// One holder per directory: a second would interleave two agents' output.
	unlock, err := platform.TryLock(filepath.Join(dir, FileLock))
	if err != nil {
		logf("another holder holds %s: %v", dir, err)
		return 1
	}
	defer unlock()

	cannot := func(format string, a ...any) int {
		logf(format, a...)
		if err := writeExit(dir, Exit{From: "agent", Code: ptr(CodeCannotStart)}, true); err != nil {
			logf("writing the exit file: %v", err)
			return 1
		}
		return 0
	}

	spec, err := readSpec(dir)
	if err != nil {
		return cannot("bad spec: %v", err)
	}
	out, err := os.OpenFile(filepath.Join(dir, FileOutLog), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return cannot("%v", err)
	}
	defer out.Close()
	// A line the last agent left without its newline would swallow this one's first line.
	if err := endLine(out, filepath.Join(dir, FileOutLog)); err != nil {
		return cannot("%v", err)
	}
	inLog, err := os.OpenFile(filepath.Join(dir, FileInLog), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return cannot("%v", err)
	}
	defer inLog.Close()
	fifo, err := openInput(dir)
	if err != nil {
		return cannot("%v", err)
	}
	defer fifo.Close()

	path, err := lookPath(spec.Argv[0], spec.Env)
	if err != nil {
		return cannot("cannot start %s: %v", spec.Argv[0], err)
	}
	// Stdout and stderr are the files themselves: byte for byte, nothing to drain, and an agent
	// that writes while no one reads (pi) never stalls.
	cmd := &exec.Cmd{
		Path: path, Args: spec.Argv, Env: spec.Env, Dir: spec.Cwd,
		Stdout: out, Stderr: errLog,
		SysProcAttr: groupAttr(),
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return cannot("%v", err)
	}
	// Handled before the agent starts, so a stop that comes at once is not lost.
	sigs := make(chan os.Signal, 1)
	notifyStop(sigs)
	if err := cmd.Start(); err != nil {
		return cannot("cannot start %s: %v", spec.Argv[0], err)
	}
	pid := cmd.Process.Pid
	grp := newGroup(pid, logf)
	defer grp.close()
	if err := writeFile(filepath.Join(dir, FilePID), []byte(strconv.Itoa(pid)+"\n")); err != nil {
		logf("writing the pid file: %v", err)
	}
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	go pump(fifo, stdin, inLog, out, logf)

	grace := DefaultStopGrace
	if spec.StopGraceMS > 0 {
		grace = time.Duration(spec.StopGraceMS) * time.Millisecond
	}
	var next <-chan time.Time
	stage := 0 // 1: stdin closed; 2: the group got SIGTERM; 3: SIGKILL
	for {
		select {
		case <-done:
			if err := writeExit(dir, exitOf(cmd.ProcessState), true); err != nil {
				logf("writing the exit file: %v", err)
				return 1
			}
			return 0
		case <-sigs:
			if stage == 0 {
				stage = 1
				_ = stdin.Close()
				next = time.After(grace)
			}
		case <-next:
			stage++
			if stage == 2 {
				logf("agent %d still running %v after its stdin closed: SIGTERM to its group", pid, grace)
				grp.term()
				next = time.After(grace)
			} else {
				logf("agent %d still running: SIGKILL to its group", pid)
				grp.kill()
				next = nil
			}
		}
	}
}

// pump copies whole lines from the pipe to the agent's stdin, splitting on 0x0A only and never
// decoding, recording each in in.log first (Rhodium's recording format: out.log's size, a space,
// the line). A line of any length is taken.
func pump(fifo io.Reader, stdin io.Writer, inLog, out *os.File, logf func(string, ...any)) {
	r := bufio.NewReaderSize(fifo, 64<<10)
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			if !errors.Is(err, os.ErrClosed) { // closed: the holder is ending
				logf("reading the input pipe: %v", err)
			}
			return
		}
		size := int64(-1)
		if st, err := out.Stat(); err == nil {
			size = st.Size()
		}
		rec := append(strconv.AppendInt(make([]byte, 0, len(line)+21), size, 10), ' ')
		if _, err := inLog.Write(append(rec, line...)); err != nil {
			logf("writing in.log: %v", err)
		}
		if _, err := stdin.Write(line); err != nil {
			logf("writing the agent's stdin: %v", err)
			return
		}
	}
}

// StopPost is the unit's ExecStopPost: an exit file for a holder that ended without writing one,
// from systemd's EXIT_CODE (exited, killed, dumped) and EXIT_STATUS (the code, or the signal's
// name). It never replaces one that is there.
func StopPost(dir string) int {
	e := Exit{From: "holder"}
	status := os.Getenv("EXIT_STATUS")
	switch os.Getenv("EXIT_CODE") {
	case "exited":
		if n, err := strconv.Atoi(status); err == nil {
			e.Code = &n
		}
	case "killed", "dumped":
		if status != "" {
			e.Signal = &status
		}
	}
	if err := writeExit(dir, e, false); err != nil {
		fmt.Fprintf(os.Stderr, "iridium-hold: %v\n", err)
		return 1
	}
	return 0
}

func readSpec(dir string) (Spec, error) {
	var s Spec
	b, err := os.ReadFile(filepath.Join(dir, FileSpec))
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return s, err
	}
	if len(s.Argv) == 0 || s.Argv[0] == "" {
		return s, errors.New("no argv")
	}
	return s, nil
}

// endLine appends a newline to path unless it is empty or ends with one.
func endLine(out *os.File, path string) error {
	r, err := os.Open(path)
	if err != nil {
		return err
	}
	defer r.Close()
	st, err := r.Stat()
	if err != nil || st.Size() == 0 {
		return err
	}
	last := make([]byte, 1)
	if _, err := r.ReadAt(last, st.Size()-1); err != nil {
		return err
	}
	if last[0] != '\n' {
		_, err = out.Write([]byte{'\n'})
	}
	return err
}

// writeExit writes the exit file whole or not at all. replace: the agent's, which the holder
// writes once per run; otherwise an exit file already there is kept.
func writeExit(dir string, e Exit, replace bool) error {
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	final := filepath.Join(dir, FileExit)
	tmp := fmt.Sprintf("%s.%d.tmp", final, os.Getpid())
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	if replace {
		return os.Rename(tmp, final)
	}
	defer os.Remove(tmp)
	if err := os.Link(tmp, final); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	return nil
}

// writeFile replaces path with data at once.
func writeFile(path string, data []byte) error {
	tmp := fmt.Sprintf("%s.%d.tmp", path, os.Getpid())
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func ptr[T any](v T) *T { return &v }
