package hold_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/hold"
	"github.com/kumoSleeping/iridium/internal/platform"
)

// The test binary is also the holder (`hold ...`) and the fake agent (`fake-agent <mode>`).
func TestMain(m *testing.M) {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "hold":
			os.Exit(hold.Main(os.Args[2:]))
		case "fake-agent":
			os.Exit(fakeAgent(os.Args[2]))
		}
	}
	os.Exit(m.Run())
}

// fakeAgent echo: copies each stdin line to stdout; "exit N" exits with N; EOF exits 0.
// stubborn: as echo, but ignores SIGTERM and stays after EOF.
func fakeAgent(mode string) int {
	if mode == "stubborn" {
		signal.Ignore(syscall.SIGTERM)
	}
	r := bufio.NewReader(os.Stdin)
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			if mode == "stubborn" {
				time.Sleep(time.Hour)
			}
			return 0
		}
		if n, ok := strings.CutPrefix(string(line), "exit "); ok {
			code, _ := strconv.Atoi(strings.TrimSpace(n))
			return code
		}
		os.Stdout.Write(line)
	}
}

func spec(mode string) hold.Spec {
	return hold.Spec{Argv: []string{os.Args[0], "fake-agent", mode}, Env: os.Environ(), Cwd: os.TempDir(), StopGraceMS: 300}
}

// startDirect runs a holder for dir as a child of the test, and waits until it reads its pipe.
func startDirect(t *testing.T, dir string, s hold.Spec) (*exec.Cmd, *hold.Input) {
	t.Helper()
	if err := hold.Prepare(dir, s); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "hold", dir)
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	in := hold.NewInput(dir)
	t.Cleanup(func() { in.Close() })
	waitFor(t, "the holder to read its pipe", func() bool { return in.Ready() && hold.ReadPID(dir) != 0 })
	return cmd, in
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

func waitExit(t *testing.T, dir string) *hold.Exit {
	t.Helper()
	var e *hold.Exit
	waitFor(t, "the exit file", func() bool {
		var err error
		e, err = hold.ReadExit(dir)
		if err != nil {
			t.Fatal(err)
		}
		return e != nil
	})
	return e
}

// readLines tails until n lines came.
func readLines(t *testing.T, tail *hold.Tail, n int) []hold.Line {
	t.Helper()
	var got []hold.Line
	waitFor(t, fmt.Sprintf("%d lines of output", n), func() bool {
		l, err := tail.Read()
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, l...)
		return len(got) >= n
	})
	if len(got) != n {
		t.Fatalf("got %d lines, want %d", len(got), n)
	}
	return got
}

func describe(e *hold.Exit) string {
	s := e.From
	if e.Code != nil {
		s += fmt.Sprintf(" code %d", *e.Code)
	}
	if e.Signal != nil {
		s += " signal " + *e.Signal
	}
	return s
}

func TestNormalExit(t *testing.T) {
	dir := t.TempDir()
	cmd, in := startDirect(t, dir, spec("echo"))
	tail := hold.NewTail(filepath.Join(dir, hold.FileOutLog), 0, 0)
	lines := [][]byte{[]byte(`{"a":1}`), []byte(`{"b":"x` + " " + `y"}`), []byte(`{"c":3}`)}
	for _, l := range lines {
		if err := in.WriteLine(l); err != nil {
			t.Fatal(err)
		}
	}
	got := readLines(t, tail, 3)
	var off int64
	for i, l := range got {
		if !bytes.Equal(l.Data, lines[i]) || l.Off != off {
			t.Errorf("line %d: %q at %d, want %q at %d", i, l.Data, l.Off, lines[i], off)
		}
		off += int64(len(lines[i])) + 1
	}
	// SIGTERM closes the agent's stdin; it exits by itself.
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if e := waitExit(t, dir); describe(e) != "agent code 0" {
		t.Fatalf("exit: %s", describe(e))
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("holder: %v", err)
	}
	inLog, _ := os.ReadFile(filepath.Join(dir, hold.FileInLog))
	if !bytes.HasPrefix(inLog, []byte("0 {\"a\":1}\n")) || bytes.Count(inLog, []byte("\n")) != 3 {
		t.Errorf("in.log: %q", inLog)
	}
}

func TestAgentExitCode(t *testing.T) {
	dir := t.TempDir()
	cmd, in := startDirect(t, dir, spec("echo"))
	if err := in.WriteLine([]byte("exit 3")); err != nil {
		t.Fatal(err)
	}
	if e := waitExit(t, dir); describe(e) != "agent code 3" {
		t.Fatalf("exit: %s", describe(e))
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("holder: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, hold.FileIn)); err != nil {
		t.Fatal(err)
	}
	// No holder reads the pipe any more.
	if err := in.WriteLine([]byte("x")); !errors.Is(err, hold.ErrNoHolder) {
		t.Fatalf("write after exit: %v", err)
	}
}

func TestAgentKilled(t *testing.T) {
	dir := t.TempDir()
	cmd, _ := startDirect(t, dir, spec("echo"))
	if err := syscall.Kill(hold.ReadPID(dir), syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	if e := waitExit(t, dir); describe(e) != "agent signal KILL" {
		t.Fatalf("exit: %s", describe(e))
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("holder: %v", err)
	}
}

// An agent that ignores both its stdin closing and SIGTERM is killed after two graces.
func TestStubbornAgent(t *testing.T) {
	dir := t.TempDir()
	cmd, _ := startDirect(t, dir, spec("stubborn"))
	start := time.Now()
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if e := waitExit(t, dir); describe(e) != "agent signal KILL" {
		t.Fatalf("exit: %s", describe(e))
	}
	if d := time.Since(start); d < 600*time.Millisecond {
		t.Errorf("killed after %v, before two graces of 300ms", d)
	}
	errLog, _ := os.ReadFile(filepath.Join(dir, hold.FileErrLog))
	if !bytes.Contains(errLog, []byte("SIGKILL")) {
		t.Errorf("err.log: %q", errLog)
	}
}

func TestCannotStart(t *testing.T) {
	dir := t.TempDir()
	s := spec("echo")
	s.Argv = []string{"/nonexistent/agent"}
	if err := hold.Prepare(dir, s); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(os.Args[0], "hold", dir).CombinedOutput(); err != nil {
		t.Fatalf("holder: %v %s", err, out)
	}
	e, err := hold.ReadExit(dir)
	if err != nil || e == nil || describe(e) != "agent code 127" {
		t.Fatalf("exit: %v %v", e, err)
	}
	errLog, _ := os.ReadFile(filepath.Join(dir, hold.FileErrLog))
	if !bytes.Contains(errLog, []byte("cannot start /nonexistent/agent")) {
		t.Errorf("err.log: %q", errLog)
	}
}

// A line the last agent left unfinished is ended before the next one writes.
func TestUnfinishedLineEnded(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, hold.FileOutLog), []byte("{\"half\":"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, in := startDirect(t, dir, spec("echo"))
	if err := in.WriteLine([]byte("{}")); err != nil {
		t.Fatal(err)
	}
	got := readLines(t, hold.NewTail(filepath.Join(dir, hold.FileOutLog), 0, 0), 2)
	if string(got[0].Data) != "{\"half\":" || string(got[1].Data) != "{}" || got[1].Off != 9 {
		t.Fatalf("lines: %q %q at %d", got[0].Data, got[1].Data, got[1].Off)
	}
}

func TestLongLine(t *testing.T) {
	dir := t.TempDir()
	_, in := startDirect(t, dir, spec("echo"))
	raw := make([]byte, 4<<20)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	long := []byte(`{"data":"` + hex.EncodeToString(raw) + `"}`) // 8 MiB and a bit
	if err := in.WriteLine(long); err != nil {
		t.Fatal(err)
	}
	if err := in.WriteLine([]byte("{}")); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, hold.FileOutLog)
	got := readLines(t, hold.NewTail(out, 0, 0), 2)
	if !bytes.Equal(got[0].Data, long) || string(got[1].Data) != "{}" {
		t.Fatalf("long line: %d bytes back, want %d", len(got[0].Data), len(long))
	}
	// Past a tail's max, a line comes without its bytes, and the next line is whole.
	capped := readLines(t, hold.NewTail(out, 0, 1<<20), 2)
	if capped[0].Data != nil || capped[0].Len != int64(len(long)) || string(capped[1].Data) != "{}" {
		t.Fatalf("capped: %d bytes, len %d; next %q", len(capped[0].Data), capped[0].Len, capped[1].Data)
	}
	inLog, _ := os.ReadFile(filepath.Join(dir, hold.FileInLog))
	if !bytes.Equal(inLog[:len("0 ")+len(long)], append([]byte("0 "), long...)) {
		t.Error("in.log does not hold the long line")
	}
}

// A daemon that restarts reads on from the offset it saved: nothing twice, nothing lost, also
// what the agent wrote while no daemon ran.
func TestRestartFromSavedOffset(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, hold.FileOutLog)
	_, in := startDirect(t, dir, spec("echo"))
	tail := hold.NewTail(out, 0, 0)
	for _, l := range []string{"1", "2"} {
		if err := in.WriteLine([]byte(l)); err != nil {
			t.Fatal(err)
		}
	}
	readLines(t, tail, 2)
	saved := tail.Offset()
	tail.Close()
	in.Close() // the daemon is gone; the holder keeps the agent running

	// Meanwhile the agent writes more.
	other := hold.NewInput(dir)
	if err := other.WriteLine([]byte("3")); err != nil {
		t.Fatal(err)
	}
	other.Close()
	waitFor(t, "line 3 on disk", func() bool { return hold.OutSize(dir) == 6 })

	in2 := hold.NewInput(dir)
	defer in2.Close()
	tail2 := hold.NewTail(out, saved, 0)
	if err := in2.WriteLine([]byte("4")); err != nil {
		t.Fatal(err)
	}
	got := readLines(t, tail2, 2)
	if string(got[0].Data) != "3" || string(got[1].Data) != "4" || got[0].Off != 4 {
		t.Fatalf("after restart: %q at %d, %q", got[0].Data, got[0].Off, got[1].Data)
	}
}

// The holder under systemd: a stop ends the agent cleanly; a killed holder takes its agent with
// it, and ExecStopPost says so.
func TestSystemd(t *testing.T) {
	if _, err := exec.LookPath("systemd-run"); err != nil {
		t.Skip("no systemd-run")
	}
	if err := exec.Command("systemctl", "--user", "is-system-running").Run(); err != nil {
		if out, _ := exec.Command("systemctl", "--user", "is-system-running").Output(); !bytes.HasPrefix(out, []byte("degraded")) {
			t.Skip("no user systemd")
		}
	}
	ctx := context.Background()
	var sd platform.Systemd
	start := func(t *testing.T) (string, string) {
		dir := t.TempDir()
		if err := hold.Prepare(dir, spec("echo")); err != nil {
			t.Fatal(err)
		}
		b := make([]byte, 4)
		_, _ = rand.Read(b)
		name := "iridium-test-" + hex.EncodeToString(b)
		u := platform.Unit{
			Name:        name,
			Argv:        []string{os.Args[0], "hold", dir},
			StopPost:    []string{os.Args[0], "hold", "--stop-post", dir},
			StopTimeout: 5 * time.Second,
		}
		if err := sd.Start(ctx, u); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = sd.Kill(ctx, name, "KILL", false) })
		in := hold.NewInput(dir)
		t.Cleanup(func() { in.Close() })
		waitFor(t, "the holder", func() bool { return in.Ready() && hold.ReadPID(dir) != 0 })
		return dir, name
	}
	gone := func(t *testing.T, name string) {
		waitFor(t, "the unit to go", func() bool {
			s, err := sd.State(ctx, name)
			return err == nil && !s.Exists
		})
	}

	t.Run("stop", func(t *testing.T) {
		dir, name := start(t)
		if names, err := sd.List(ctx, name); err != nil || len(names) != 1 || names[0] != name {
			t.Fatalf("list: %v %v", names, err)
		}
		if err := sd.Stop(ctx, name); err != nil {
			t.Fatal(err)
		}
		if e := waitExit(t, dir); describe(e) != "agent code 0" {
			t.Fatalf("exit: %s", describe(e))
		}
		gone(t, name)
	})

	t.Run("holder killed", func(t *testing.T) {
		dir, name := start(t)
		agent := hold.ReadPID(dir)
		if err := sd.Kill(ctx, name, "KILL", true); err != nil {
			t.Fatal(err)
		}
		if e := waitExit(t, dir); describe(e) != "holder signal KILL" {
			t.Fatalf("exit: %s", describe(e))
		}
		gone(t, name)
		waitFor(t, "the agent to be killed too", func() bool { return syscall.Kill(agent, 0) == syscall.ESRCH })
	})
}
