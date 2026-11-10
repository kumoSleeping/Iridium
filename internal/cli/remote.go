package cli

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/irhelp"
	"github.com/kumoSleeping/iridium/internal/place"
	"github.com/kumoSleeping/iridium/internal/remote"
)

// At is where ir runs when it runs for an agent hosted at another machine (docs/exec.md "远端的
// ir"): the ir there sends its arguments to POST /api/cli with its session's own TOKEN, and the
// daemon runs them here, as that machine's Bot, for that session.
type At struct {
	Machine string // the Bot of the machine (its terminal ID)
	Session string // the session it runs in
	Cwd     string // its working directory there
}

// remoteCommands are what ir does for an agent at another machine: sessions and groups. Not the
// files and command lines of the Iridium's own machine (cp, files, sh, run), its secrets
// or its admin: a person on a panel, or ir on that machine, does those.
var remoteCommands = map[string]bool{
	"new": true, "send": true, "wait": true, "read": true, "ls": true, "find": true,
	"share": true, "stop": true, "archive": true, "unarchive": true, "rename": true, "ws": true, "apps": true,
}

// RunAt runs ir with args for a session at another machine, through hc (the daemon's own API,
// as that machine's Bot); the exit code.
func RunAt(args []string, stdout, stderr io.Writer, hc *http.Client, at At) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Fprint(stdout, irhelp.Remote())
		return exitDone
	}
	if !remoteCommands[args[0]] {
		fmt.Fprintf(stderr, "ir: %s runs on the host, not here\n", args[0])
		return exitError
	}
	return commands[args[0]](&env{out: stdout, err: stderr, hc: hc, at: &at}, args[1:])
}

// where is where ir new starts a session: at place dir (internal/place: <app>:<path>, host:<path>;
// without a prefix the caller's computer: this one, or for ir at another, that one), or with
// machine (--machine, the older way: an app's ID or name) at that one in dir, as that computer
// writes paths. The app's ID ("" for the host) and the folder.
func (e *env) where(machine, dir string) (string, string, error) {
	if machine == "" {
		if name, path, ok := place.Split(dir); ok {
			path = cmp.Or(path, "~")
			if place.IsHost(name) {
				if e.at != nil && !filepath.IsAbs(path) && path != "~" && !strings.HasPrefix(path, "~/") {
					return "", "", fmt.Errorf("a folder at the host is a whole path there, not %q", path)
				}
				p, err := localPath(path)
				return "", p, err
			}
			machine, dir = name, path
		}
	}
	if machine == "" && e.at != nil {
		machine = e.at.Machine
	}
	if machine == "" {
		p, err := localPath(dir)
		return "", p, err
	}
	m, err := e.machineOf(machine)
	if err != nil {
		return "", "", err
	}
	own := e.at != nil && m.ID == e.at.Machine
	switch {
	case dir == "." && own:
		return m.ID, e.at.Cwd, nil
	case dir == "." || dir == "~":
		return m.ID, m.Home, nil
	case strings.HasPrefix(dir, "~/") || (m.Sep == `\` && strings.HasPrefix(dir, `~\`)):
		return m.ID, remote.Join(m.Sep, m.Home, dir[2:]), nil
	case remote.IsAbs(m.Sep, dir):
		return m.ID, dir, nil
	case own:
		return m.ID, remote.Join(m.Sep, e.at.Cwd, dir), nil
	}
	return "", "", fmt.Errorf("a folder at %s is a whole path there, not %q", m.Name, dir)
}

// machine is an app with a computer (docs/exec.md), as ir new and ir sh find one.
type machine struct {
	ID, Name string
	api.Computer
}

// machineOf is the computer of an app, by its ID or name (GET /api/apps lists them to any caller).
func (e *env) machineOf(arg string) (machine, error) {
	var as api.Apps
	if err := e.do("GET", "/api/apps", nil, &as); err != nil {
		return machine{}, err
	}
	for _, a := range as.Apps {
		if a.Computer != nil && !a.Host && (a.ID == arg || strings.EqualFold(a.Name, arg)) {
			return machine{ID: a.ID, Name: a.Name, Computer: *a.Computer}, nil
		}
	}
	return machine{}, fmt.Errorf("no computer %s (ir apps lists them)", arg)
}

// machineNames is each app's name by its ID; none if they cannot be had.
func (e *env) machineNames() map[string]string {
	var as api.Apps
	if e.do("GET", "/api/apps", nil, &as) != nil {
		return nil
	}
	out := make(map[string]string, len(as.Apps))
	for _, a := range as.Apps {
		if !a.Host {
			out[a.ID] = a.Name
		}
	}
	return out
}

// home is the home folder ir ls writes as ~: this machine's; none for ir at another machine.
func (e *env) home() string {
	if e.at != nil {
		return ""
	}
	h, _ := os.UserHomeDir()
	return h
}

// groupOf is the group session id is in (0: none), for ir ls --here.
func (e *env) groupOf(id string) (int64, error) {
	if id == "" {
		return 0, errors.New("--here is for ir run in a session")
	}
	var list []api.Session
	if err := e.do("GET", "/api/sessions", nil, &list); err != nil {
		return 0, err
	}
	for _, s := range list {
		if s.ID == id {
			return s.Workspace, nil
		}
	}
	return 0, fmt.Errorf("no session %s", id)
}

// ForwardAt is ir at another machine (IRIDIUM_URL and IRIDIUM_TOKEN in its environment): its
// arguments and folder go to url's POST /api/cli, what comes back is printed as it comes.
func ForwardAt(url, token string, args []string, stdout, stderr io.Writer) int {
	cwd, _ := os.Getwd()
	body, _ := json.Marshal(api.CLIRun{Args: args, Cwd: cwd})
	req, err := http.NewRequestWithContext(context.Background(), "POST", strings.TrimRight(url, "/")+"/api/cli", bytes.NewReader(body))
	if err != nil {
		fmt.Fprintf(stderr, "ir: %v\n", err)
		return exitError
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(stderr, "ir: the Iridium at %s does not answer: %v\n", url, err)
		return exitError
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		var ae api.Error
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		if json.Unmarshal(b, &ae) != nil || ae.Text() == "" {
			ae.Message = strings.TrimSpace(string(b))
		}
		fmt.Fprintf(stderr, "ir: %s\n", ae.Text())
		return exitError
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	for sc.Scan() {
		var l api.CLIOutput
		if json.Unmarshal(sc.Bytes(), &l) != nil {
			continue
		}
		io.WriteString(stdout, l.Out)
		io.WriteString(stderr, l.Err)
		if l.Exit != nil {
			return *l.Exit
		}
	}
	fmt.Fprintln(stderr, "ir: the answer stopped short")
	return exitError
}
