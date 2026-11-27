//go:build !windows

package dsh

import (
	"os/exec"
	"syscall"
)

// noWindow: cmd in a session of its own, so a signal to Iridium's terminal is not its.
func noWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
