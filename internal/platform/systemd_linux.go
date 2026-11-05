//go:build linux

package platform

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Systemd runs holders as transient user services (systemd-run --user), children of the user's
// systemd and not of the daemon. KillMode=mixed: a stop sends SIGTERM to the holder alone, which
// closes the agent's stdin, and SIGKILL to whatever is left once StopTimeout passes.
// OOMPolicy=continue: the kernel killing one process (say a test run by the agent) leaves the
// unit running. No memory limit (Rhodium §4.2: a hard one kills big sessions as they resume).
type Systemd struct{}

func (Systemd) Start(ctx context.Context, u Unit) error {
	timeout := u.StopTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	args := []string{
		"--user", "--unit=" + u.Name, "--collect", "--quiet", "--service-type=exec",
		"-p", "KillMode=mixed", "-p", "OOMPolicy=continue",
		"-p", fmt.Sprintf("TimeoutStopSec=%dms", timeout.Milliseconds()),
	}
	if len(u.StopPost) > 0 {
		args = append(args, "-p", "ExecStopPost="+quote(u.StopPost))
	}
	args = append(append(args, "--"), u.Argv...)
	return run(ctx, "systemd-run", args...)
}

func (Systemd) Stop(ctx context.Context, name string) error {
	return run(ctx, "systemctl", "--user", "stop", "--no-block", name)
}

func (Systemd) Kill(ctx context.Context, name, sig string, main bool) error {
	whom := "all"
	if main {
		whom = "main"
	}
	return run(ctx, "systemctl", "--user", "kill", "--kill-whom="+whom, "--signal="+sig, name)
}

func (Systemd) State(ctx context.Context, name string) (State, error) {
	out, err := exec.CommandContext(ctx, "systemctl", "--user", "show", "--property=LoadState,ActiveState,MainPID", name).Output()
	if err != nil {
		return State{}, fmt.Errorf("systemctl show %s: %w", name, err)
	}
	var s State
	load := ""
	for line := range strings.Lines(string(out)) {
		k, v, _ := strings.Cut(strings.TrimSpace(line), "=")
		switch k {
		case "LoadState":
			load = v
		case "ActiveState":
			s.Active = v
		case "MainPID":
			s.MainPID, _ = strconv.Atoi(v)
		}
	}
	// A unit collected after it ended shows as not-found and inactive.
	s.Exists = load == "loaded" || (s.Active != "inactive" && s.Active != "")
	return s, nil
}

func (Systemd) List(ctx context.Context, prefix string) ([]string, error) {
	out, err := exec.CommandContext(ctx, "systemctl", "--user", "list-units", "--all", "--plain", "--no-legend", "--type=service", prefix+"*").Output()
	if err != nil {
		return nil, fmt.Errorf("systemctl list-units: %w", err)
	}
	var names []string
	for line := range strings.Lines(string(out)) {
		f := strings.Fields(line)
		if len(f) > 0 && strings.HasPrefix(f[0], prefix) {
			names = append(names, strings.TrimSuffix(f[0], ".service"))
		}
	}
	return names, nil
}

func run(ctx context.Context, name string, args ...string) error {
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// quote makes argv one systemd command line: each word double-quoted, \ and " escaped, and %
// doubled so that no specifier is expanded.
func quote(argv []string) string {
	words := make([]string, len(argv))
	for i, a := range argv {
		a = strings.ReplaceAll(a, `\`, `\\`)
		a = strings.ReplaceAll(a, `"`, `\"`)
		a = strings.ReplaceAll(a, "%", "%%")
		words[i] = `"` + a + `"`
	}
	return strings.Join(words, " ")
}
