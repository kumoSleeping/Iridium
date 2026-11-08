//go:build !windows

package executor

import (
	"os"
	"os/exec"
	"syscall"
)

// setGroup starts the process in a group of its own, so its whole tree can be ended.
func setGroup(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

// killTree signals the process's group: TERM, or KILL if hard.
func killTree(cmd *exec.Cmd, hard bool) {
	if cmd.Process == nil {
		return
	}
	sig := syscall.SIGTERM
	if hard {
		sig = syscall.SIGKILL
	}
	_ = syscall.Kill(-cmd.Process.Pid, sig)
}

// signalOf is the signal's name (without SIG) that ended the process, "" if it exited.
func signalOf(ps *os.ProcessState) string {
	ws, ok := ps.Sys().(syscall.WaitStatus)
	if !ok || !ws.Signaled() {
		return ""
	}
	switch s := ws.Signal(); s {
	case syscall.SIGKILL:
		return "KILL"
	case syscall.SIGTERM:
		return "TERM"
	case syscall.SIGINT:
		return "INT"
	default:
		return s.String()
	}
}

func exts() []string { return []string{""} }
