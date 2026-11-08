package cli

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
)

// ir machines, ir sh: the other machines connected here, and a command line at one, for an agent
// (docs/api.md "命令"). ir sh waits for it, prints what it prints, and exits with its code.

const shUsage = "usage: ir sh <machine> [--cwd dir] [--timeout 2m] -- command …"

// exitTimeout: ir sh's command ran out of time and was stopped (as timeout(1)).
const exitTimeout = 124

func cmdMachines(e *env, args []string) int {
	if len(args) > 0 {
		return e.failf("usage: ir machines")
	}
	var ms []api.Machine
	if err := e.do("GET", "/api/machines", nil, &ms); err != nil {
		return e.fail(err)
	}
	for _, m := range ms {
		state := "offline"
		if m.Online {
			state = "online"
		}
		sh := "no ir sh"
		if m.AgentShell {
			sh = "ir sh"
		}
		fmt.Fprintf(e.out, "%s\t%s/%s\t%s\t%s\t%s\n", m.Name, m.OS, m.Arch, state, sh, m.Home)
	}
	return exitDone
}

func cmdSh(e *env, args []string) int {
	var machine, cwd string
	timeout := 2 * time.Minute
	var cmd []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			cmd = append(cmd, args[i+1:]...)
			i = len(args)
		case a == "--cwd" || a == "--timeout":
			if i+1 >= len(args) {
				return e.failf("%s", shUsage)
			}
			i++
			if a == "--cwd" {
				cwd = args[i]
			} else if d, err := parseTimeout(args[i]); err == nil {
				timeout = d
			} else {
				return e.failf("--timeout %s: %v", args[i], err)
			}
		case strings.HasPrefix(a, "--cwd="):
			cwd = strings.TrimPrefix(a, "--cwd=")
		case strings.HasPrefix(a, "--timeout="):
			d, err := parseTimeout(strings.TrimPrefix(a, "--timeout="))
			if err != nil {
				return e.failf("%s: %v", a, err)
			}
			timeout = d
		case machine == "":
			machine = a
		default:
			cmd = append(cmd, a)
		}
	}
	line := strings.TrimSpace(strings.Join(cmd, " "))
	if machine == "" || line == "" {
		return e.failf("%s", shUsage)
	}
	var st api.ShellStarted
	if err := e.do("POST", "/api/machines/"+url.PathEscape(machine)+"/shell", api.ShellRun{Line: line, Cwd: cwd}, &st); err != nil {
		return e.fail(err)
	}
	deadline := time.Now().Add(timeout)
	var from int64
	for wait := 100 * time.Millisecond; ; {
		var o api.ShellOutput
		if err := e.do("GET", "/api/shell/"+url.PathEscape(st.Run)+"?from="+strconv.FormatInt(from, 10), nil, &o); err != nil {
			return e.fail(err)
		}
		fmt.Fprint(e.out, o.Data)
		from = o.Next
		if o.Done {
			if o.Err != "" {
				fmt.Fprint(e.err, o.Err)
			}
			switch {
			case o.Code != nil:
				return *o.Code
			case o.Signal != "":
				return e.failf("ended by %s", o.Signal)
			}
			return exitError
		}
		if time.Now().After(deadline) {
			_ = e.do("DELETE", "/api/shell/"+url.PathEscape(st.Run), nil, nil)
			fmt.Fprintf(e.err, "ir: still running after %s: stopped (--timeout for more)\n", timeout)
			return exitTimeout
		}
		if o.Data != "" {
			wait = 100 * time.Millisecond
		} else {
			wait = min(wait*3/2, time.Second)
		}
		time.Sleep(wait)
	}
}

// parseTimeout: a duration ("90s", "5m") or seconds ("90").
func parseTimeout(s string) (time.Duration, error) {
	if n, err := strconv.Atoi(s); err == nil && n > 0 {
		return time.Duration(n) * time.Second, nil
	}
	d, err := time.ParseDuration(s)
	if err == nil && d <= 0 {
		err = fmt.Errorf("not above zero")
	}
	return d, err
}
