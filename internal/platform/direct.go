package platform

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

// Direct runs holders as children of this process in their own session, and runs StopPost as
// systemd would when one ends. For tests and for machines without a user systemd: a holder does
// not outlive the daemon's process group being killed, and a restarted daemon cannot find it.
type Direct struct {
	mu    sync.Mutex
	units map[string]*exec.Cmd
}

func (d *Direct) Start(ctx context.Context, u Unit) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.units == nil {
		d.units = map[string]*exec.Cmd{}
	}
	if _, ok := d.units[u.Name]; ok {
		return fmt.Errorf("unit %s exists", u.Name)
	}
	cmd := exec.Command(u.Argv[0], u.Argv[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	d.units[u.Name] = cmd
	go func() {
		_ = cmd.Wait()
		if len(u.StopPost) > 0 {
			post := exec.Command(u.StopPost[0], u.StopPost[1:]...)
			code, status := "exited", strconv.Itoa(cmd.ProcessState.ExitCode())
			if ws, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
				code, status = "killed", unix.SignalName(ws.Signal())[3:]
			}
			post.Env = append(os.Environ(), "EXIT_CODE="+code, "EXIT_STATUS="+status)
			_ = post.Run()
		}
		d.mu.Lock()
		delete(d.units, u.Name)
		d.mu.Unlock()
	}()
	return nil
}

func (d *Direct) Stop(ctx context.Context, name string) error {
	return d.Kill(ctx, name, "TERM", true)
}

func (d *Direct) Kill(_ context.Context, name, sig string, main bool) error {
	d.mu.Lock()
	cmd := d.units[name]
	d.mu.Unlock()
	if cmd == nil {
		return errors.New("no unit " + name)
	}
	s := unix.SignalNum("SIG" + sig)
	if s == 0 {
		return errors.New("no signal " + sig)
	}
	pid := cmd.Process.Pid
	if !main {
		pid = -pid // its session's process group; the agent has a group of its own
	}
	return unix.Kill(pid, s)
}

func (d *Direct) State(_ context.Context, name string) (State, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	cmd := d.units[name]
	if cmd == nil {
		return State{}, nil
	}
	return State{Exists: true, Active: "active", MainPID: cmd.Process.Pid}, nil
}

func (d *Direct) List(_ context.Context, prefix string) ([]string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var names []string
	for n := range d.units {
		if len(n) >= len(prefix) && n[:len(prefix)] == prefix {
			names = append(names, n)
		}
	}
	return names, nil
}
