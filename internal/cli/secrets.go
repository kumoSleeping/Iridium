package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/secrets"
)

// The credentials (plan "凭据"): ir secret ls | set <名> [--note 说明] | rm <名>, and ir run, which
// puts them into a command and masks them in its output.

// runCmd: ir run [--secret 名,…] -- 命令 …. {{名}} in the arguments is the value, {{file:名}} the
// path of a 0600 file holding it (gone once the command ends); each is set in the environment by
// its name too. The command's output comes through with the values masked; its exit code is ir's.
func runCmd(e adminEnv, args []string, stdout, stderr io.Writer) int {
	usage := func() int {
		fmt.Fprintln(stderr, "用法：ir run [--secret 名,…] -- 命令 参数…   （{{名}}、{{file:名}} 换进参数）")
		return 2
	}
	var names []string
	add := func(list string) bool {
		for _, n := range strings.Split(list, ",") {
			if n = strings.TrimSpace(n); n == "" {
				continue
			}
			if !secrets.ValidName(n) {
				fmt.Fprintf(stderr, "ir run: 凭据名 %q 不对（大写字母、数字、_）\n", n)
				return false
			}
			if !contains(names, n) {
				names = append(names, n)
			}
		}
		return true
	}
	i := 0
loop:
	for ; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--":
			i++
			break loop
		case a == "--secret" && i+1 < len(args):
			i++
			if !add(args[i]) {
				return 2
			}
		case strings.HasPrefix(a, "--secret="):
			if !add(strings.TrimPrefix(a, "--secret=")) {
				return 2
			}
		case strings.HasPrefix(a, "-"):
			return usage()
		default:
			break loop
		}
	}
	argv := args[i:]
	if len(argv) == 0 {
		return usage()
	}
	for _, n := range secrets.Refs(argv) {
		if !contains(names, n) {
			names = append(names, n)
		}
	}
	values := map[string]string{}
	if len(names) > 0 {
		var err error
		if values, err = useSecrets(e, names, strings.Join(argv, " "), stderr); err != nil {
			if errors.Is(err, errDenied) {
				return 1
			}
			return failed(stderr, "run", err)
		}
	}
	var files []string
	defer func() {
		for _, f := range files {
			os.Remove(f)
		}
	}()
	var ferr error
	file := func(n string) string {
		f, err := os.CreateTemp("", "ir-secret-*")
		if err != nil {
			ferr = err
			return ""
		}
		files = append(files, f.Name())
		_, err = f.WriteString(values[n])
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err == nil {
			err = os.Chmod(f.Name(), 0o600)
		}
		if err != nil {
			ferr = err
		}
		return f.Name()
	}
	value := func(n string) string { return values[n] }
	run := make([]string, len(argv))
	for k, a := range argv {
		run[k] = secrets.Expand(a, value, file)
	}
	if ferr != nil {
		return failed(stderr, "run", ferr)
	}
	m := secrets.NewMasker(values, 1, false)
	masker := func() *secrets.Masker { return m }
	out, errw := secrets.NewWriter(stdout, masker), secrets.NewWriter(stderr, masker)
	cmd := exec.Command(run[0], run[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = e.stdin, out, errw
	if cmd.Stdin == nil {
		cmd.Stdin = os.Stdin
	}
	cmd.Env = os.Environ()
	for _, n := range names {
		cmd.Env = append(cmd.Env, n+"="+values[n])
	}
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(stderr, "ir run: %s\n", m.MaskString(err.Error()))
		return 127
	}
	// Signals for ir go to the command (it may be a terminal's ^C); ir waits for it to end.
	sig := make(chan os.Signal, 4)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	go func() {
		for s := range sig {
			_ = cmd.Process.Signal(s)
		}
	}()
	err := cmd.Wait()
	signal.Stop(sig)
	close(sig)
	_ = out.Close()
	_ = errw.Close()
	var ee *exec.ExitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &ee):
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return 128 + int(ws.Signal())
		}
		return ee.ExitCode()
	default:
		fmt.Fprintf(stderr, "ir run: %s\n", m.MaskString(err.Error()))
		return 1
	}
}

var errDenied = errors.New("denied")

// useSecrets asks the daemon for names' values; when a panel must allow it, it says so on stderr
// and waits.
func useSecrets(e adminEnv, names []string, command string, stderr io.Writer) (map[string]string, error) {
	req := api.UseSecrets{Session: e.session, Names: names, Command: command}
	for {
		var raw json.RawMessage
		code, err := e.call("POST", "/v1/internal/secrets/use", req, &raw)
		if err != nil {
			return nil, err
		}
		if code != http.StatusAccepted {
			var v api.SecretValues
			if err := json.Unmarshal(raw, &v); err != nil {
				return nil, err
			}
			return v.Values, nil
		}
		if req.Confirm != "" {
			return nil, errors.New("确认过了，还是要再确认")
		}
		var p api.Pending
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		fmt.Fprintf(stderr, "ir run：用 %s 要主机或带 Claude 的网页登录允许：已发到“需要”，%s 前有效。等确认……\n", strings.Join(p.Need.Secrets, "、"), hhmm(p.Need.ExpiresAt))
		st, err := waitState(e, p.Need.ID)
		if err != nil {
			return nil, err
		}
		if st.State != "allowed" {
			fmt.Fprintln(stderr, "ir run："+outcome(st))
			return nil, errDenied
		}
		req.Confirm = p.Need.ID
	}
}

// waitState waits for need id to be answered.
func waitState(e adminEnv, id string) (api.NeedState, error) {
	for {
		var st api.NeedState
		if _, err := e.call("GET", "/v1/internal/needs/"+url.PathEscape(id)+"?wait=60", nil, &st); err != nil {
			return st, err
		}
		if st.State != "pending" {
			return st, nil
		}
	}
}

// outcome says how a need that was not allowed went.
func outcome(st api.NeedState) string {
	switch st.State {
	case "denied":
		return "已拒绝。"
	case "expired":
		return "已过期，无人处理。"
	}
	return "没成：" + st.Error
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}
