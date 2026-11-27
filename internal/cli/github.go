package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/ghenv"
)

// ir github (docs/api.md "GitHub"): what an agent's git and gh run through, put in its environment
// by Iridium (ghenv): git's credential helper (credential), the gh in front of the real one (gh),
// each asking for the token of the person the agent's turn is for. None bound: git's own helpers
// and gh's own login, as without Iridium. IR_GITHUB_USER=<member> asks for another's.
func githubCmd(e adminEnv, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		var l api.GitHubList
		if _, err := e.call("GET", "/v1/internal/github", nil, &l); err != nil {
			return failed(stderr, "github", err)
		}
		for _, b := range l.Bindings {
			mark := " "
			if b.Member == l.Turn {
				mark = "*"
			}
			fmt.Fprintf(stdout, "%s %s\t@%s\n", mark, b.Name, b.Login)
		}
		if l.Turn == "" {
			fmt.Fprintln(stdout, "this turn: the machine's own git and gh login")
		}
		return 0
	}
	switch args[0] {
	case "credential":
		return githubCredential(e, args[1:], stdout, stderr)
	case "gh":
		return githubGh(e, args[1:], stderr)
	}
	fmt.Fprintln(stderr, "usage: ir github [credential get|store|erase | gh …]")
	return 2
}

// githubToken is the token for this turn (or IR_GITHUB_USER's); ok false: none bound.
func githubToken(e adminEnv) (api.GitHubToken, bool, error) {
	path := "/v1/internal/github/token"
	if u := strings.TrimSpace(os.Getenv(ghenv.UserVar)); u != "" {
		path += "?user=" + url.QueryEscape(u)
	}
	var t api.GitHubToken
	code, err := e.call("GET", path, nil, &t)
	if err != nil {
		return t, false, err
	}
	return t, code == 200 && t.Token != "", nil
}

// githubCredential is git's credential helper (gitcredentials(7)): get answers with the turn's
// token; store and erase of it are dropped. With none bound, or for what is not its own, git's
// helpers as they were (ghenv: those before it in the environment's config) answer.
func githubCredential(e adminEnv, args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		return 2
	}
	op := args[0]
	in, _ := io.ReadAll(io.LimitReader(e.stdin, 64<<10))
	attrs := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(string(in)))
	for sc.Scan() {
		if k, v, ok := strings.Cut(sc.Text(), "="); ok {
			attrs[k] = v
		}
	}
	switch op {
	case "get":
		t, ok, err := githubToken(e)
		if err != nil {
			// A user asked for by name and not bound: said, not passed over.
			if os.Getenv(ghenv.UserVar) != "" {
				fmt.Fprintf(stderr, "ir github: %v\n", err)
				return 1
			}
		}
		if ok {
			fmt.Fprintf(stdout, "username=%s\npassword=%s\n", ghenv.Username, t.Token)
			return 0
		}
		return passCredential("fill", in, stdout)
	case "store", "erase":
		if attrs["username"] == ghenv.Username {
			return 0
		}
		verb := map[string]string{"store": "approve", "erase": "reject"}[op]
		return passCredential(verb, in, io.Discard)
	}
	return 0
}

// passCredential hands what git asked to git's own helpers, the config Iridium put after them
// left out; nothing asked of a person (git does that itself after the helpers).
func passCredential(verb string, in []byte, stdout io.Writer) int {
	cmd := exec.Command("git", "credential", verb)
	cmd.Env = append(ghenv.Without(os.Environ()), "GIT_TERMINAL_PROMPT=0")
	cmd.Stdin = strings.NewReader(string(in))
	out, err := cmd.Output()
	if err != nil {
		return 0 // none answered: git goes on as it would have
	}
	_, _ = stdout.Write(out)
	return 0
}

// githubGh runs the real gh (the first on PATH past Iridium's), as the turn's person where one
// is bound; else as it is.
func githubGh(e adminEnv, args []string, stderr io.Writer) int {
	real := realGh()
	if real == "" {
		fmt.Fprintln(stderr, "gh: not installed")
		return 127
	}
	env := os.Environ()
	t, ok, err := githubToken(e)
	if err != nil && os.Getenv(ghenv.UserVar) != "" {
		fmt.Fprintf(stderr, "ir github: %v\n", err)
		return 1
	}
	if ok {
		env = append(env, "GH_TOKEN="+t.Token)
	}
	cmd := exec.Command(real, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr, cmd.Env = os.Stdin, os.Stdout, os.Stderr, env
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(stderr, "gh: %v\n", err)
		return 127
	}
	sig := make(chan os.Signal, 4)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		for s := range sig {
			_ = cmd.Process.Signal(s)
		}
	}()
	err = cmd.Wait()
	signal.Stop(sig)
	close(sig)
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	if err != nil {
		return 1
	}
	return 0
}

// realGh is the gh on PATH that is not Iridium's (ghenv.ShimVar's directory).
func realGh() string {
	shim := os.Getenv(ghenv.ShimVar)
	name := "gh"
	if runtime.GOOS == "windows" {
		name = "gh.exe"
	}
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" || (shim != "" && filepath.Clean(dir) == filepath.Clean(shim)) {
			continue
		}
		p := filepath.Join(dir, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return p
		}
	}
	return ""
}
