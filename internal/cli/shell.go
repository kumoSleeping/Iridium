package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/place"
)

// ir sh: a command line at an app's computer, for an agent (docs/api.md "命令"); ir apps lists
// the computers. ir sh waits for it, prints what it prints, and exits with its code.

const shUsage = "usage: ir sh <app>[:dir] [--cwd dir] [--timeout 2m] -- command …"

// exitTimeout: ir sh's command ran out of time and was stopped (as timeout(1)).
const exitTimeout = 124

// cmdApps is ir apps for an agent at another machine (RunAt; at the host, ir apps is admin.go's):
// the apps and their computers, as any caller sees them.
func cmdApps(e *env, args []string) int {
	if len(args) > 0 {
		return e.failf("usage: ir apps")
	}
	var as api.Apps
	if err := e.do("GET", "/v1/internal/apps", nil, &as); err != nil {
		return e.fail(err)
	}
	printApps(e.out, as)
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
	// A place (mac:~/dir) is the machine and the folder there.
	if name, path, ok := place.Split(machine); ok {
		machine = name
		if cwd == "" {
			cwd = path
		}
	}
	if place.IsHost(machine) {
		return e.failf("ir sh runs at another app's computer; at the host, run the command line yourself")
	}
	deadline := time.Now().Add(timeout)
	// Streamed, at an executor that speaks run; else (an older one) a holder, read by polling.
	resp, err := e.request("POST", "/v1/internal/machines/"+url.PathEscape(machine)+"/shell", api.ShellRun{Line: line, Cwd: cwd}, "Accept", ndjson)
	if err != nil {
		return e.fail(err)
	}
	defer resp.Body.Close()
	if strings.HasPrefix(resp.Header.Get("Content-Type"), ndjson) {
		return e.shStream(resp.Body, deadline, timeout)
	}
	var st api.ShellStarted
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		return e.fail(err)
	}
	var from int64
	for wait := 100 * time.Millisecond; ; {
		var o api.ShellOutput
		if err := e.do("GET", "/v1/internal/shell/"+url.PathEscape(st.Run)+"?from="+strconv.FormatInt(from, 10), nil, &o); err != nil {
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
			_ = e.do("DELETE", "/v1/internal/shell/"+url.PathEscape(st.Run), nil, nil)
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

const ndjson = "application/x-ndjson"

// shStream prints a run's ShellEvent lines as they come, and exits with its code; past deadline
// it hangs up, which stops the run.
func (e *env) shStream(body io.ReadCloser, deadline time.Time, timeout time.Duration) int {
	timer := time.AfterFunc(time.Until(deadline), func() { body.Close() })
	dec := json.NewDecoder(body)
	for {
		var ev api.ShellEvent
		if err := dec.Decode(&ev); err != nil {
			if !timer.Stop() {
				fmt.Fprintf(e.err, "ir: still running after %s: stopped (--timeout for more)\n", timeout)
				return exitTimeout
			}
			return e.failf("the command line's output ended early: %v", err)
		}
		fmt.Fprint(e.out, ev.Out)
		fmt.Fprint(e.err, ev.Err)
		if !ev.Done {
			continue
		}
		timer.Stop()
		switch {
		case ev.Exit != nil && ev.Error == "":
			return *ev.Exit
		case ev.Error != "":
			fmt.Fprintf(e.err, "ir: %s\n", ev.Error)
			if ev.Exit != nil {
				return *ev.Exit
			}
			return exitError
		case ev.Signal != "":
			return e.failf("ended by %s", ev.Signal)
		}
		return exitError
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
