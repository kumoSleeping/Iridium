package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/secrets"
)

// The credentials (plan "凭据"): ir secret ls | set <名> [--note 说明] | rm <名>, and ir run, which
// puts them into a command and masks them in its output.

func secretCmd(e adminEnv, args []string, stdout, stderr io.Writer) int {
	usage := func() int {
		fmt.Fprintln(stderr, "用法：ir secret ls | set <名> [--note 说明] | rm <名>")
		return 2
	}
	if len(args) == 0 {
		return usage()
	}
	switch args[0] {
	case "ls":
		if len(args) != 1 {
			return usage()
		}
		return secretLs(e, stdout, stderr)
	case "rm":
		if len(args) != 2 {
			return usage()
		}
		if _, err := e.call("DELETE", "/api/secrets/"+url.PathEscape(args[1]), nil, nil); err != nil {
			return failed(stderr, "secret rm", err)
		}
		fmt.Fprintf(stdout, "删了 %s。\n", args[1])
		return 0
	case "set":
		var name string
		var note *string
		for i := 1; i < len(args); i++ {
			switch a := args[i]; {
			case a == "--note" && i+1 < len(args):
				i++
				note = &args[i]
			case strings.HasPrefix(a, "--note="):
				v := strings.TrimPrefix(a, "--note=")
				note = &v
			case name == "" && !strings.HasPrefix(a, "-"):
				name = a
			default:
				return usage()
			}
		}
		if name == "" {
			return usage()
		}
		return secretSet(e, name, note, stdout, stderr)
	}
	return usage()
}

func secretLs(e adminEnv, stdout, stderr io.Writer) int {
	var l api.Secrets
	if _, err := e.call("GET", "/api/secrets", nil, &l); err != nil {
		return failed(stderr, "secret ls", err)
	}
	if len(l.Secrets) == 0 {
		fmt.Fprintln(stdout, "还没有凭据（ir secret set <名>）。")
		return 0
	}
	rows := [][]string{{"名字", "说明", "谁能用", "用过", "最近"}}
	for _, s := range l.Secrets {
		last := "—"
		if s.LastUse != nil {
			last = when(s.LastUse.At)
		}
		rows = append(rows, []string{s.Name, s.Note, scopeName(s), strconv.Itoa(s.Uses), last})
	}
	table(stdout, rows)
	return 0
}

func scopeName(s api.SecretInfo) string {
	out := map[api.SecretScope]string{api.ScopeAll: "全部会话", api.ScopeOwner: "只本人终端开的", api.ScopeWorkspaces: "某些工作区"}[s.Scope]
	if s.Always {
		out += "，每次确认"
	}
	return out
}

// asAgent reports whether an agent runs this ir secret set: its environment says so, or nothing
// of it is a terminal. A person may pipe the value in (stdin no terminal, the rest one).
func (e adminEnv) asAgent() bool {
	return e.envAgent || (e.agent && !e.outTTY)
}

// secretSet stores name: a person types the value (or pipes it in); an agent never gives one, a
// panel types it once it confirms.
func secretSet(e adminEnv, name string, note *string, stdout, stderr io.Writer) int {
	if !secrets.ValidName(name) {
		fmt.Fprintln(stderr, "ir secret set: 名字只能有大写字母、数字、_，字母开头，最多 64 个")
		return 2
	}
	req := api.SetSecret{Note: note}
	agent := e.asAgent()
	if !agent {
		v, err := readValue(e, name, stderr)
		if err != nil {
			return failed(stderr, "secret set", err)
		}
		if v == "" {
			fmt.Fprintln(stderr, "ir secret set: 值是空的，没存")
			return 1
		}
		req.Value = v
		e.agent = false // a person piping the value in
	} else {
		e.agent = true
	}
	var raw json.RawMessage
	code, err := e.call("PUT", "/api/secrets/"+url.PathEscape(name), req, &raw)
	if err != nil {
		return failed(stderr, "secret set", err)
	}
	if code == http.StatusAccepted {
		return waitConfirm(e, raw, stdout, stderr)
	}
	fmt.Fprintf(stdout, "已存 %s。用法：ir run -- 命令 …{{%s}}…\n", name, name)
	return 0
}

// readValue reads the value: from a terminal, one line without echo; else all of stdin, less a
// final newline (an ssh key fits).
func readValue(e adminEnv, name string, stderr io.Writer) (string, error) {
	if f, ok := e.stdin.(*os.File); ok && isTerminal(f) {
		fmt.Fprintf(stderr, "%s 的值（不回显）：", name)
		v, err := readHidden(f)
		fmt.Fprintln(stderr)
		return v, err
	}
	b, err := io.ReadAll(io.LimitReader(e.stdin, secrets.MaxValue+2))
	if err != nil {
		return "", err
	}
	if len(b) > secrets.MaxValue+1 {
		return "", errors.New("值太长（最多 64 KiB）")
	}
	v := strings.TrimSuffix(string(b), "\n")
	return strings.TrimSuffix(v, "\r"), nil
}

// readHidden reads a line from terminal f with its echo off, and turns it on again (on ^C too).
func readHidden(f *os.File) (string, error) {
	fd := int(f.Fd())
	old, err := unix.IoctlGetTermios(fd, ioctlGetTermios)
	if err != nil {
		return "", err
	}
	quiet := *old
	quiet.Lflag &^= unix.ECHO
	quiet.Lflag |= unix.ICANON | unix.ISIG
	if err := unix.IoctlSetTermios(fd, ioctlSetTermios, &quiet); err != nil {
		return "", err
	}
	restore := func() { _ = unix.IoctlSetTermios(fd, ioctlSetTermios, old) }
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	stop := make(chan struct{})
	go func() {
		select {
		case <-sig:
			restore()
			fmt.Fprintln(os.Stderr)
			os.Exit(130)
		case <-stop:
		}
	}()
	line, err := bufio.NewReader(f).ReadString('\n')
	close(stop)
	signal.Stop(sig)
	restore()
	if err != nil && !(errors.Is(err, io.EOF) && line != "") {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

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
		code, err := e.call("POST", "/api/secrets/use", req, &raw)
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
		fmt.Fprintf(stderr, "ir run：用 %s 要在带 Claude 的网页面板上允许：已发到“需要你”，%s 前有效。等确认……\n", strings.Join(p.Confirm.Secrets, "、"), hhmm(p.Confirm.ExpiresAt))
		st, err := waitState(e, p.Confirm.ID)
		if err != nil {
			return nil, err
		}
		if st.State != "allowed" {
			fmt.Fprintln(stderr, "ir run："+outcome(st))
			return nil, errDenied
		}
		req.Confirm = p.Confirm.ID
	}
}

// waitState waits for confirmation id to be answered.
func waitState(e adminEnv, id string) (api.ConfirmState, error) {
	for {
		var st api.ConfirmState
		if _, err := e.call("GET", "/api/confirms/"+url.PathEscape(id)+"?wait=60", nil, &st); err != nil {
			return st, err
		}
		if st.State != "pending" {
			return st, nil
		}
	}
}

// outcome says how a confirmation that was not allowed went.
func outcome(st api.ConfirmState) string {
	switch st.State {
	case "denied":
		return "网页面板拒绝了。"
	case "expired":
		return "没人确认，过期了。"
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
