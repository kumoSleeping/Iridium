//go:build windows

package executor

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// setGroup starts the process in a group of its own; its tree is ended with taskkill /T.
func setGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

// killTree ends the process and its children. Windows has no TERM: both are taskkill, the hard
// one forced.
func killTree(cmd *exec.Cmd, hard bool) {
	if cmd.Process == nil {
		return
	}
	args := []string{"/T", "/PID", strconv.Itoa(cmd.Process.Pid)}
	if hard {
		args = append([]string{"/F"}, args...)
	}
	_ = exec.Command("taskkill", args...).Run()
}

func signalOf(ps *os.ProcessState) string { return "" }

func exts() []string {
	out := []string{""}
	for _, e := range strings.Split(os.Getenv("PATHEXT"), ";") {
		if e != "" {
			out = append(out, strings.ToLower(e))
		}
	}
	if len(out) == 1 {
		out = append(out, ".exe", ".cmd", ".bat")
	}
	return out
}
