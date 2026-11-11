package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/config"
	"github.com/kumoSleeping/iridium/internal/launchd"
	"github.com/kumoSleeping/iridium/internal/public"
)

// Headers of the socket API (package server).
const (
	headerAgent   = "X-Iridium-Agent"
	headerSession = "X-Iridium-Session"
)

// adminEnv is where ir runs: its daemon, and whether an agent runs it (then nothing secret is
// printed: plan "ir", "agent 调这些命令时 TOKEN 不进对话").
type adminEnv struct {
	cfg     config.Config
	cfgPath string
	agent   bool
	session string // IRIDIUM_SESSION
	tty     bool   // a person at a terminal: stdin and stdout are terminals
	// envAgent: the environment says an agent runs ir (not only that it has no terminal); outTTY:
	// stdout or stderr is a terminal (a person may pipe a value in).
	envAgent bool
	outTTY   bool
	stdin    io.Reader
	client   *http.Client // to the unix socket
}

// detect reads the config and the environment.
func detect() (adminEnv, error) {
	path := os.Getenv("IRIDIUM_CONFIG")
	if path == "" {
		path = config.DefaultPath()
	}
	cfg, err := config.Load(path)
	if err != nil {
		return adminEnv{}, err
	}
	tty := isTerminal(os.Stdin) && isTerminal(os.Stdout)
	e := adminEnv{cfg: cfg, cfgPath: path, session: caller(), tty: tty, stdin: os.Stdin}
	e.envAgent = os.Getenv("CLAUDECODE") != "" || caller() != ""
	e.agent = !tty || e.envAgent
	e.outTTY = isTerminal(os.Stdout) || isTerminal(os.Stderr)
	e.client = socketClient(cfg.SocketPath())
	return e, nil
}

func isTerminal(f *os.File) bool {
	_, err := unix.IoctlGetTermios(int(f.Fd()), ioctlGetTermios)
	return err == nil
}

func socketClient(path string) *http.Client {
	return &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", path)
	}}}
}

// adminCommands are the ones this file runs.
var adminCommands = map[string]func(adminEnv, []string, io.Writer, io.Writer) int{
	"public": publicCmd,
	"run":    runCmd,
}

// call sends one request to the daemon and decodes its JSON reply into out; it gives the status.
func (e adminEnv) call(method, path string, body any, out any) (int, error) {
	var r io.Reader
	ct := ""
	switch b := body.(type) {
	case nil:
	case *multipartBody:
		r, ct = &b.buf, b.ct
	default:
		data, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		r, ct = bytes.NewReader(data), "application/json"
	}
	req, err := http.NewRequest(method, "http://ir"+path, r)
	if err != nil {
		return 0, err
	}
	if ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	if e.agent {
		req.Header.Set(headerAgent, "1")
		if e.session != "" {
			req.Header.Set(headerSession, e.session)
		}
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("Iridium 没在运行（%s）：%w", e.cfg.SocketPath(), err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode >= 300 {
		var ae api.Error
		if json.Unmarshal(data, &ae) == nil && ae.NameError != "" {
			return resp.StatusCode, errors.New(nameErrors[ae.NameError] + " (" + ae.NameError + ")")
		}
		if ae.Text() != "" {
			return resp.StatusCode, errors.New(ae.Text())
		}
		return resp.StatusCode, errors.New(resp.Status)
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return resp.StatusCode, err
		}
	}
	return resp.StatusCode, nil
}

var nameErrors = map[string]string{
	"empty":    "名字是空的",
	"too_long": "名字最多 12 个字",
	"invalid":  "名字只能有字母、数字、空格和 - _ .",
	"reserved": "这个名字是保留的",
	"letter":   "单个英文字母不能当名字",
	"taken":    "名字已有人用",
}

func failed(stderr io.Writer, cmd string, err error) int {
	fmt.Fprintf(stderr, "ir %s: %v\n", cmd, err)
	return 1
}

func hhmm(ms int64) string {
	if ms == 0 {
		return "—"
	}
	return time.UnixMilli(ms).Format("15:04")
}

// table prints rows in columns, by display width (a CJK character takes two).
func table(w io.Writer, rows [][]string) {
	var widths []int
	for _, r := range rows {
		for i, c := range r {
			if i == len(widths) {
				widths = append(widths, 0)
			}
			widths[i] = max(widths[i], width(c))
		}
	}
	for _, r := range rows {
		var b strings.Builder
		for i, c := range r {
			b.WriteString(c)
			if i < len(r)-1 {
				b.WriteString(strings.Repeat(" ", widths[i]-width(c)+2))
			}
		}
		fmt.Fprintln(w, strings.TrimRight(b.String(), " "))
	}
}

func width(s string) int {
	n := 0
	for _, r := range s {
		n++
		if r >= 0x1100 && (r <= 0x115f || (r >= 0x2e80 && r <= 0xa4cf) || (r >= 0xac00 && r <= 0xd7a3) ||
			(r >= 0xf900 && r <= 0xfaff) || (r >= 0xfe30 && r <= 0xfe4f) || (r >= 0xff00 && r <= 0xff60) ||
			(r >= 0xffe0 && r <= 0xffe6) || r >= 0x20000) {
			n++
		}
	}
	return n
}

type multipartBody struct {
	buf bytes.Buffer
	ct  string
}

// publicCmd: ir public [off]. Interactive: a person at a terminal only.
func publicCmd(e adminEnv, args []string, stdout, stderr io.Writer) int {
	off := len(args) == 1 && args[0] == "off"
	if len(args) > 1 || (len(args) == 1 && !off) {
		fmt.Fprintln(stderr, "用法：ir public [off]")
		return 2
	}
	if !e.tty || e.agent {
		fmt.Fprintln(stderr, "ir public 要你在终端里亲自跑（它会问问题、要你登录 Cloudflare）；agent 不能替你跑。")
		return 2
	}
	// The daemon sets public.origin at the end: make sure it is there before anything is made.
	if _, err := e.call("GET", "/v1/internal/me", nil, nil); err != nil {
		return failed(stderr, "public", err)
	}
	o, err := publicOptions(e, stdout)
	if err != nil {
		return failed(stderr, "public", err)
	}
	ctx := context.Background()
	if off {
		err = public.Off(ctx, o)
	} else {
		err = public.On(ctx, o)
	}
	if err != nil {
		return failed(stderr, "public", err)
	}
	return 0
}

func publicOptions(e adminEnv, stdout io.Writer) (public.Options, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return public.Options{}, err
	}
	_, portText, err := net.SplitHostPort(e.cfg.Listen)
	if err != nil {
		return public.Options{}, fmt.Errorf("listen %q: %w", e.cfg.Listen, err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return public.Options{}, err
	}
	in := bufio.NewReader(e.stdin)
	o := public.Options{
		Dir:     filepath.Join(e.cfg.StatePath(), "cloudflared"),
		BinDir:  filepath.Join(home, ".local", "bin"),
		UnitDir: filepath.Join(xdgConfig(home), "systemd", "user"),
		Port:    port,
		Home:    home,
		HTTP:    publicHTTP(),
		Systemctl: func(ctx context.Context, args ...string) error {
			return runSystemctl(ctx, args...)
		},
		Ask: func(q, def string) (string, error) {
			if def != "" && !strings.HasSuffix(q, "]") {
				q += " [" + def + "]"
			}
			fmt.Fprint(stdout, q+" ")
			line, err := in.ReadString('\n')
			if err != nil && line == "" {
				return "", err
			}
			line = strings.TrimSpace(line)
			if line == "" {
				line = def
			}
			return line, nil
		},
		Out:    stdout,
		Daemon: daemon{e},
	}
	if runtime.GOOS == "darwin" {
		o.Launchd, o.UnitDir, o.Launchctl = true, launchd.Dir(home), launchd.Run
		o.Domain, o.Log = launchd.Domain(context.Background()), launchd.LogFile(home, "iridium-cloudflared")
	}
	return o, nil
}

func xdgConfig(home string) string {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return d
	}
	return filepath.Join(home, ".config")
}

// daemon is the running Iridium as `ir public` needs it.
type daemon struct{ e adminEnv }

func (d daemon) SetOrigin(_ context.Context, origin string) error {
	_, err := d.e.call("PUT", "/v1/internal/public", api.SetPublic{Origin: origin}, nil)
	return err
}

func (d daemon) Invite(context.Context) (string, time.Time, error) {
	var inv api.Invite
	if _, err := d.e.call("POST", "/v1/internal/invites", api.NewInvite{}, &inv); err != nil {
		return "", time.Time{}, err
	}
	return inv.URL, time.UnixMilli(inv.ExpiresAt), nil
}
