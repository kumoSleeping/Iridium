package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/irhelp"
)

// At is where ir runs when it runs for an agent hosted at another machine (docs/exec.md "远端的
// ir"): the ir there sends its arguments to POST /v1/internal/cli with its session's own TOKEN, and the
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
	"api": true,
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

// ForwardAt is ir at another machine (IRIDIUM_URL and IRIDIUM_TOKEN in its environment): its
// arguments and folder go to url's POST /v1/internal/cli, what comes back is printed as it comes.
func ForwardAt(url, token string, args []string, stdout, stderr io.Writer) int {
	cwd, _ := os.Getwd()
	body, _ := json.Marshal(api.CLIRun{Args: args, Cwd: cwd})
	req, err := http.NewRequestWithContext(context.Background(), "POST", strings.TrimRight(url, "/")+"/v1/internal/cli", bytes.NewReader(body))
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
