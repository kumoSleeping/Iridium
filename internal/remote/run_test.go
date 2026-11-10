package remote_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/machineid"
	"github.com/kumoSleeping/iridium/internal/remote"
	"github.com/kumoSleeping/iridium/internal/remote/executor"
	"github.com/kumoSleeping/iridium/internal/remote/wire"
)

// runAll reads run till its end: its stdout, its stderr, and its last event.
func runAll(t *testing.T, r *remote.Run) (string, string, remote.RunEvent) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var out, errs strings.Builder
	var last remote.RunEvent
	for {
		ev, err := r.Next(ctx)
		if err != nil {
			if ctx.Err() != nil {
				t.Fatal("the run did not end")
			}
			return out.String(), errs.String(), last
		}
		if ev.Err {
			errs.WriteString(ev.Data)
		} else {
			out.WriteString(ev.Data)
		}
		last = ev
	}
}

// waitData reads run till what it wrote has want.
func waitData(t *testing.T, r *remote.Run, want string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var got string
	for !strings.Contains(got, want) {
		ev, err := r.Next(ctx)
		if err != nil {
			t.Fatalf("no %q in %q: %v", want, got, err)
		}
		got += ev.Data
	}
}

// gone: process pid has ended (and been waited for).
func gone(pid int) bool { return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) }

func pidIn(t *testing.T, file string) int {
	t.Helper()
	var pid int
	waitFor(t, "the pid", func() bool {
		b, _ := os.ReadFile(file)
		pid, _ = strconv.Atoi(strings.TrimSpace(string(b)))
		return pid > 0
	})
	return pid
}

// run (docs/exec.md "命令"): a command line at the machine, its output as it comes, its exit code;
// stopped, and ended with the connection.
func TestRun(t *testing.T) {
	r := newRig(t, "go")
	ctx := context.Background()
	m := r.machine()
	if !m.Has(wire.CapRun) {
		t.Fatal("no run in the caps")
	}
	hi, _ := m.Hello()
	if !hi.ShellOn() || hi.Shell == nil || hi.NoShell || !hi.AgentShell {
		t.Errorf("the hello's shell: %+v", hi)
	}

	// Its output, stdout and stderr apart, and its code; in the folder asked, else its home.
	run, err := m.Run(ctx, "echo out; echo err >&2; printf 'é'; pwd; exit 3", r.state, nil)
	if err != nil {
		t.Fatal(err)
	}
	out, errs, last := runAll(t, run)
	if out != "out\né"+r.state+"\n" || errs != "err\n" || last.Exit == nil || last.Exit.Code == nil || *last.Exit.Code != 3 {
		t.Errorf("run: %q %q %+v", out, errs, last.Exit)
	}
	home, _ := os.UserHomeDir()
	run, _ = m.Run(ctx, "pwd", "", nil)
	if out, _, _ := runAll(t, run); out != home+"\n" {
		t.Errorf("in its home: %q", out)
	}
	// Not there: as a shell says.
	run, _ = m.Run(ctx, "no-such-program-here", "", nil)
	if _, errs, last := runAll(t, run); last.Exit == nil || last.Exit.Code == nil || *last.Exit.Code != 127 || errs == "" {
		t.Errorf("not there: %q %+v", errs, last.Exit)
	}
	// A folder that is not: it cannot start.
	run, _ = m.Run(ctx, "true", filepath.Join(r.state, "none"), nil)
	if _, _, last := runAll(t, run); last.Exit == nil || last.Exit.Error == "" || *last.Exit.Code != 127 {
		t.Errorf("no folder: %+v", last.Exit)
	}

	// Stopped: TERM.
	pidFile := filepath.Join(t.TempDir(), "pid")
	run, err = m.Run(ctx, "echo $$ > "+pidFile+"; echo started; exec sleep 30", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	waitData(t, run, "started")
	if err := run.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, last := runAll(t, run); last.Exit == nil || last.Exit.Signal == nil || *last.Exit.Signal != "TERM" {
		t.Errorf("stopped: %+v", last.Exit)
	}

	// The connection ends: the reader hears so, and the process there is ended.
	os.Remove(pidFile)
	run, err = m.Run(ctx, "echo $$ > "+pidFile+"; echo started; exec sleep 30", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	waitData(t, run, "started")
	pid := pidIn(t, pidFile)
	r.drop()
	if _, _, last := runAll(t, run); !last.Gone {
		t.Errorf("cut off: %+v", last)
	}
	waitFor(t, "the process ended", func() bool { return gone(pid) })
}

// An executor whose owner keeps command lines off runs none, by run or as a holder; one on this
// very computer has its agents left out, its connection kept.
func TestRunShellOff(t *testing.T) {
	self := machineid.Of("this computer")
	r := newRigWith(t, "go", func(h *remote.Hub) { h.Self = self }, func(o *executor.Options) {
		o.Shell = false
		o.MachineID = self
		o.Agents = []wire.AgentInfo{{Agent: "pi", Argv: []string{"/bin/pi"}}}
	})
	m := r.machine()
	hi, ok := m.Hello()
	if !ok || hi.ShellOn() || !hi.NoShell || hi.AgentShell || hi.MachineID != self {
		t.Fatalf("the hello: %+v", hi)
	}
	if len(hi.Agents) != 0 {
		t.Errorf("an app on the host keeps its agents: %+v", hi.Agents)
	}
	if _, err := m.Run(context.Background(), "id", "", nil); !wire.IsCode(err, wire.CodeDenied) {
		t.Errorf("run with shell off: %v", err)
	}
}

// Found by its app's name now (NameOf), not the one it connected with; nothing for an ID only ever
// looked up.
func TestFindByName(t *testing.T) {
	name := "mini"
	r := newRigWith(t, "go", func(h *remote.Hub) {
		h.NameOf = func(id string) string {
			if id == "m1" {
				return name
			}
			return ""
		}
	}, nil)
	if r.hub.Find("mini") == nil || r.hub.Find("MINI") == nil || r.hub.Find("m1") == nil {
		t.Error("not found by its name or ID")
	}
	if r.hub.Find("pc") != nil {
		t.Error("found by the name it connected with")
	}
	r.hub.Machine("nothing") // looked up, never connected, no app
	if r.hub.Find("nothing") != nil {
		t.Error("a machine of nothing found")
	}
	r.hub.Drop("nothing")
}

// scripts/dev/exec-mini.py, the executor in plain Python: run with --shell (output, code, stop,
// ended with its connection), its hello's shell and machine ID; a 403 keeps its TOKEN, only a 401
// voids it.
func TestExecMini(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("no python3")
	}
	home := t.TempDir()
	var mode atomic.Value
	mode.Store("ok")
	var pairs atomic.Int32
	hub := &remote.Hub{Logf: t.Logf}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/api/exec":
			switch mode.Load() {
			case "403":
				w.WriteHeader(http.StatusForbidden)
				io.WriteString(w, `{"code":"no_permission"}`)
				return
			case "401":
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if req.Header.Get("Authorization") != "Bearer tok" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			hub.Serve(w, req, "m1", "mini")
		case "/api/pair":
			pairs.Add(1)
			w.WriteHeader(http.StatusForbidden)
			io.WriteString(w, `{"code":"pair_closed"}`)
		default:
			http.NotFound(w, req)
		}
	}))
	t.Cleanup(srv.Close)
	tokFile := filepath.Join(home, ".iridium-exec-mini", strings.ReplaceAll(strings.TrimPrefix(srv.URL, "http://"), ":", "-"), "token")
	os.MkdirAll(filepath.Dir(tokFile), 0o700)
	if err := os.WriteFile(tokFile, []byte("tok"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(py, "-I", "../../scripts/dev/exec-mini.py", srv.URL, "mini", "no-such-agent", "--shell")
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "HOME=") && !strings.HasPrefix(kv, "IRIDIUM_") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	cmd.Env = append(cmd.Env, "HOME="+home, "IRIDIUM_MACHINE_ID=xyz")
	cmd.Stdout, cmd.Stderr = testWriter{t}, testWriter{t}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	m := func() *remote.Machine { return hub.Machine("m1") }
	waitFor(t, "exec-mini", func() bool { _, ok := m().Hello(); return ok })
	hi, _ := m().Hello()
	if !hi.ShellOn() || !m().Has(wire.CapRun) || hi.MachineID != machineid.Of("xyz") {
		t.Fatalf("the hello: %+v", hi)
	}
	ctx := context.Background()
	run, err := m().Run(ctx, "echo out; echo err >&2; printf 'é'; exit 3", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if out, errs, last := runAll(t, run); out != "out\né" || errs != "err\n" || last.Exit == nil || last.Exit.Code == nil || *last.Exit.Code != 3 {
		t.Errorf("run: %q %q %+v", out, errs, last.Exit)
	}
	run, _ = m().Run(ctx, "echo started; exec sleep 30", "", nil)
	waitData(t, run, "started")
	if err := run.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, last := runAll(t, run); last.Exit == nil || last.Exit.Signal == nil || *last.Exit.Signal != "TERM" {
		t.Errorf("stopped: %+v", last.Exit)
	}
	pidFile := filepath.Join(t.TempDir(), "pid")
	run, _ = m().Run(ctx, "echo $$ > "+pidFile+"; echo started; exec sleep 30", "", nil)
	waitData(t, run, "started")
	pid := pidIn(t, pidFile)

	// Turned away with a 403: the connection ends (its run with it), the TOKEN stays.
	mode.Store("403")
	hub.Drop("m1")
	if _, _, last := runAll(t, run); !last.Gone {
		t.Errorf("cut off: %+v", last)
	}
	waitFor(t, "the run ended there", func() bool { return gone(pid) })
	time.Sleep(3 * time.Second) // it tries again every 2s
	if _, err := os.Stat(tokFile); err != nil {
		t.Fatalf("a 403 voided the TOKEN: %v", err)
	}
	// A 401: the TOKEN is void, it asks to pair again.
	mode.Store("401")
	waitFor(t, "the TOKEN forgotten", func() bool { _, err := os.Stat(tokFile); return os.IsNotExist(err) })
	waitFor(t, "a pairing request", func() bool { return pairs.Load() > 0 })
}
