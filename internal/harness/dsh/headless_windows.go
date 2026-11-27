package dsh

import (
	"os/exec"
	"syscall"
)

// noWindow: no console shown for cmd (CREATE_NO_WINDOW).
func noWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000, HideWindow: true}
}
