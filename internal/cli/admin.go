package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	irAccess "github.com/kumoSleeping/iridium/internal/access"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/config"
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
	stdin   io.Reader
	client  *http.Client // to the unix socket
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
	e.agent = !tty || os.Getenv("CLAUDECODE") != "" || os.Getenv("CODEX_THREAD_ID") != "" || os.Getenv("IRIDIUM_SESSION") != ""
	e.client = socketClient(cfg.SocketPath())
	return e, nil
}

func isTerminal(f *os.File) bool {
	_, err := unix.IoctlGetTermios(int(f.Fd()), unix.TCGETS)
	return err == nil
}

func socketClient(path string) *http.Client {
	return &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", path)
	}}}
}

// adminCommands are the ones this file runs.
var adminCommands = map[string]func(adminEnv, []string, io.Writer, io.Writer) int{
	"invite":  invite,
	"token":   token,
	"access":  access,
	"profile": profile,
	"revoke":  revoke,
	"public":  publicCmd,
	"update":  updateCmd,
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
		var ae struct {
			Error     string `json:"error"`
			NameError string `json:"nameError"`
		}
		if json.Unmarshal(data, &ae) == nil && ae.NameError != "" {
			return resp.StatusCode, errors.New(nameErrors[ae.NameError] + " (" + ae.NameError + ")")
		}
		if ae.Error != "" {
			return resp.StatusCode, errors.New(ae.Error)
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
	"taken":    "名字已经有终端或邀请在用",
}

func failed(stderr io.Writer, cmd string, err error) int {
	fmt.Fprintf(stderr, "ir %s: %v\n", cmd, err)
	return 1
}

// ownerFlag takes --owner out of args: the terminal is the owner's own, so it gets Claude.
func ownerFlag(args []string) (rest []string, owner bool) {
	for _, a := range args {
		if a == "--owner" {
			owner = true
		} else {
			rest = append(rest, a)
		}
	}
	return rest, owner
}

// personOnly refuses what only a person at the machine may do (give a terminal Claude) when an
// agent runs ir, by the same signs that keep secrets from agents. The daemon refuses it too.
func personOnly(e adminEnv, cmd, what string, stderr io.Writer) bool {
	if !e.agent {
		return true
	}
	fmt.Fprintf(stderr, "ir %s: %s只能你本人在本机的终端里跑；agent 不能给终端 Claude（Claude 只给本人的终端）。\n", cmd, what)
	return false
}

// invite: ir invite [名字] [--owner]. A person gets the link; an agent, only what the panel
// decided. --owner (a person only): the panel is the owner's own and gets Claude.
func invite(e adminEnv, args []string, stdout, stderr io.Writer) int {
	args, owner := ownerFlag(args)
	if len(args) > 1 || (len(args) == 1 && strings.HasPrefix(args[0], "-")) {
		fmt.Fprintln(stderr, "用法：ir invite [名字] [--owner]\n  --owner：确认这是我本人用的终端，带 Claude")
		return 2
	}
	if owner && !personOnly(e, "invite", "--owner ", stderr) {
		return 2
	}
	req := api.NewInvite{Owner: owner}
	if len(args) == 1 {
		req.Name = args[0]
	}
	var raw json.RawMessage
	code, err := e.call("POST", "/api/terminals/invites", req, &raw)
	if err != nil {
		return failed(stderr, "invite", err)
	}
	if code == http.StatusAccepted {
		return waitConfirm(e, raw, stdout, stderr)
	}
	var inv api.Invite
	if err := json.Unmarshal(raw, &inv); err != nil {
		return failed(stderr, "invite", err)
	}
	claude := "不带 Claude（要带：ir invite --owner）"
	if owner {
		claude = "带 Claude"
	}
	fmt.Fprintf(stdout, "新网页面板的邀请（%s 前有效，只能用一次，%s）：\n\n  %s\n\n在那台设备的浏览器里打开，起名、建 Passkey。\n", hhmm(inv.ExpiresAt), claude, inv.URL)
	return 0
}

// token: ir token <名字> [--owner]. A person gets the token; an agent, only what the panel
// decided. --owner (a person only): the plugin is the owner's own and gets Claude.
func token(e adminEnv, args []string, stdout, stderr io.Writer) int {
	args, owner := ownerFlag(args)
	if len(args) != 1 || strings.HasPrefix(args[0], "-") {
		fmt.Fprintln(stderr, "用法：ir token <名字> [--owner]\n  --owner：确认这是我本人用的终端，带 Claude")
		return 2
	}
	if owner && !personOnly(e, "token", "--owner ", stderr) {
		return 2
	}
	var raw json.RawMessage
	code, err := e.call("POST", "/api/terminals/tokens", api.NewInvite{Name: args[0], Owner: owner}, &raw)
	if err != nil {
		return failed(stderr, "token", err)
	}
	if code == http.StatusAccepted {
		return waitConfirm(e, raw, stdout, stderr)
	}
	var t api.Token
	if err := json.Unmarshal(raw, &t); err != nil {
		return failed(stderr, "token", err)
	}
	claude := "不带 Claude"
	if irAccess.HasClaude(t.Terminal.Rules) {
		claude = "带 Claude"
	}
	fmt.Fprintf(stdout, "%s 的 TOKEN（只显示这一次；再跑一次就换新的，旧的作废；%s）：\n\n  %s\n\n请求带 Authorization: Bearer <TOKEN>。\n", t.Terminal.Name, claude, t.Token)
	return 0
}

// waitConfirm waits for a panel to answer what an agent asked for, and says only the outcome.
func waitConfirm(e adminEnv, raw json.RawMessage, stdout, stderr io.Writer) int {
	var p api.Pending
	if err := json.Unmarshal(raw, &p); err != nil {
		return failed(stderr, "confirm", err)
	}
	what := map[string]string{"invite": "新网页面板的邀请", "token": "插件 TOKEN"}[p.Confirm.Kind]
	if p.Confirm.Name != "" {
		what += "（" + p.Confirm.Name + "）"
	}
	fmt.Fprintf(stdout, "%s要在网页面板上确认：已发到“需要你”，%s 前有效。等确认……\n", what, hhmm(p.Confirm.ExpiresAt))
	for {
		var st api.ConfirmState
		if _, err := e.call("GET", "/api/confirms/"+url.PathEscape(p.Confirm.ID)+"?wait=60", nil, &st); err != nil {
			return failed(stderr, "confirm", err)
		}
		switch st.State {
		case "pending":
			continue
		case "allowed":
			if st.Kind == "invite" {
				fmt.Fprintf(stdout, "同意了：邀请链接只显示在确认的那个面板上（%s 前有效）。\n", hhmm(st.ExpiresAt))
			} else {
				fmt.Fprintf(stdout, "同意了：%s 的 TOKEN 只显示在确认的那个面板上。\n", st.Name)
			}
			return 0
		case "denied":
			fmt.Fprintln(stdout, "网页面板拒绝了。")
		case "expired":
			fmt.Fprintln(stdout, "没人确认，过期了。")
		default:
			fmt.Fprintf(stdout, "没成：%s\n", st.Error)
		}
		return 1
	}
}

func hhmm(ms int64) string {
	if ms == 0 {
		return "—"
	}
	return time.UnixMilli(ms).Format("15:04")
}

// access: ir access, every terminal and unused invite (nothing secret); ir access <名字> --claude
// on|off, Claude for a terminal (on: a person only).
func access(e adminEnv, args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		var ref, val string
		switch {
		case len(args) == 3 && args[1] == "--claude":
			ref, val = args[0], args[2]
		case len(args) == 2 && strings.HasPrefix(args[1], "--claude="):
			ref, val = args[0], strings.TrimPrefix(args[1], "--claude=")
		}
		if ref == "" || strings.HasPrefix(ref, "-") || (val != "on" && val != "off") {
			fmt.Fprintln(stderr, "用法：ir access [<名字> --claude on|off]")
			return 2
		}
		return claudeSwitch(e, ref, val == "on", stdout, stderr)
	}
	var ts api.Terminals
	if _, err := e.call("GET", "/api/terminals", nil, &ts); err != nil {
		return failed(stderr, "access", err)
	}
	rows := [][]string{{"名字", "类型", "ID", "Claude", "在线", "今天用量", "上次"}}
	for _, t := range ts.Terminals {
		kind := map[api.TerminalKind]string{api.TerminalPanel: "面板", api.TerminalChannel: "通道"}[t.Kind]
		if t.Local {
			kind = "内置"
		}
		claude := "—"
		if irAccess.HasClaude(t.Rules) {
			claude = "开"
		}
		rows = append(rows, []string{t.Name, kind, t.ID, claude, strconv.Itoa(t.Online), strconv.FormatInt(t.UsedToday, 10), when(t.LastSeenAt)})
	}
	table(stdout, rows)
	if len(ts.Invites) > 0 {
		fmt.Fprintln(stdout, "\n未用的邀请：")
		rows = rows[:0]
		for _, i := range ts.Invites {
			until := "到 " + hhmm(i.ExpiresAt)
			if i.Setup {
				until = "首次设置（状态目录里的 setup-token）"
			}
			if i.Owner {
				until += "，带 Claude"
			}
			rows = append(rows, []string{"", i.ID, i.Name, until})
		}
		table(stdout, rows)
	}
	return 0
}

// claudeSwitch turns Claude on or off for terminal ref (name or ID): its permission table with
// claude added or taken out. Only a person at the machine turns it on; the daemon checks too.
func claudeSwitch(e adminEnv, ref string, on bool, stdout, stderr io.Writer) int {
	if on && !personOnly(e, "access", "--claude on ", stderr) {
		return 2
	}
	var ts api.Terminals
	if _, err := e.call("GET", "/api/terminals", nil, &ts); err != nil {
		return failed(stderr, "access", err)
	}
	var t *api.Terminal
	for i, x := range ts.Terminals {
		if x.ID == ref || strings.EqualFold(x.Name, ref) {
			t = &ts.Terminals[i]
			break
		}
	}
	switch {
	case t == nil:
		return failed(stderr, "access", fmt.Errorf("没有叫 %s 的终端", ref))
	case t.Local:
		fmt.Fprintln(stdout, "本机一直有 Claude，不用开关。")
		return 0
	case irAccess.HasClaude(t.Rules) == on:
		fmt.Fprintf(stdout, "%s 的 Claude 本来就%s。\n", t.Name, map[bool]string{true: "开着", false: "关着"}[on])
		return 0
	}
	r := irAccess.WithoutClaude(t.Rules)
	if on {
		r.Agents = append(r.Agents, api.AgentClaude) // the daemon marks it the owner's own
	}
	if _, err := e.call("PUT", "/api/terminals/"+url.PathEscape(t.ID)+"/rules", api.SetRules{Rules: r}, nil); err != nil {
		return failed(stderr, "access", err)
	}
	if on {
		fmt.Fprintf(stdout, "给 %s 开了 Claude：确认它是你本人用的终端。\n", t.Name)
	} else {
		fmt.Fprintf(stdout, "关了 %s 的 Claude；要再开，在本机跑 ir access %s --claude on。\n", t.Name, t.Name)
	}
	return 0
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

func when(ms int64) string {
	if ms == 0 {
		return "—"
	}
	return time.UnixMilli(ms).Format("01-02 15:04")
}

type multipartBody struct {
	buf bytes.Buffer
	ct  string
}

// profile: ir profile <名字> [--name 新名] [--avatar 图片] [--no-avatar].
func profile(e adminEnv, args []string, stdout, stderr io.Writer) int {
	usage := func() int {
		fmt.Fprintln(stderr, "用法：ir profile <名字> [--name 新名] [--avatar 图片] [--no-avatar]")
		return 2
	}
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return usage()
	}
	ref, rest := args[0], args[1:]
	body := &multipartBody{}
	mw := multipart.NewWriter(&body.buf)
	changed := false
	for i := 0; i < len(rest); i++ {
		flag, val, hasVal := strings.Cut(rest[i], "=")
		next := func() (string, bool) {
			if hasVal {
				return val, true
			}
			if i+1 < len(rest) {
				i++
				return rest[i], true
			}
			return "", false
		}
		switch flag {
		case "--name":
			v, ok := next()
			if !ok {
				return usage()
			}
			mw.WriteField("name", v)
		case "--avatar":
			v, ok := next()
			if !ok {
				return usage()
			}
			data, err := os.ReadFile(v)
			if err != nil {
				return failed(stderr, "profile", err)
			}
			w, _ := mw.CreateFormFile("avatar", filepath.Base(v))
			w.Write(data)
		case "--no-avatar":
			mw.WriteField("removeAvatar", "1")
		default:
			return usage()
		}
		changed = true
	}
	if !changed {
		return usage()
	}
	mw.Close()
	body.ct = mw.FormDataContentType()
	var info struct {
		Name   string `json:"name"`
		Avatar string `json:"avatar"`
	}
	if _, err := e.call("PUT", "/api/endpoints/"+url.PathEscape(ref)+"/profile", body, &info); err != nil {
		return failed(stderr, "profile", err)
	}
	fmt.Fprintf(stdout, "改好了：%s%s\n", info.Name, map[bool]string{true: "（有头像）", false: ""}[info.Avatar != ""])
	return 0
}

// revoke: ir revoke <名字>, a terminal or an unused invite.
func revoke(e adminEnv, args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "用法：ir revoke <名字或 ID>")
		return 2
	}
	if _, err := e.call("DELETE", "/api/terminals/"+url.PathEscape(args[0]), nil, nil); err != nil {
		return failed(stderr, "revoke", err)
	}
	fmt.Fprintf(stdout, "撤销了 %s。\n", args[0])
	return 0
}

// updateCmd: ir update [--check] [--force].
func updateCmd(e adminEnv, args []string, stdout, stderr io.Writer) int {
	check, force := false, false
	for _, a := range args {
		switch a {
		case "--check":
			check = true
		case "--force":
			force = true
		default:
			fmt.Fprintln(stderr, "用法：ir update [--check] [--force]")
			return 2
		}
	}
	var st api.UpdateState
	if _, err := e.call("POST", "/api/update/check", nil, &st); err != nil {
		return failed(stderr, "update", err)
	}
	if !st.Available && !force {
		fmt.Fprintf(stdout, "已是最新：%s（最新 %s）。\n", st.Current, st.Latest)
		return 0
	}
	if check {
		fmt.Fprintf(stdout, "有新版 %s（现在 %s）：\n%s\n跑 ir update 更新。\n", st.Latest, st.Current, st.Notes)
		return 0
	}
	fmt.Fprintf(stdout, "更新到 %s……\n", st.Latest)
	if _, err := e.call("POST", "/api/update", api.ApplyUpdate{Force: force}, &st); err != nil {
		return failed(stderr, "update", err)
	}
	fmt.Fprintf(stdout, "装好了 %s，服务在重启；托管的会话不受影响。\n", st.Latest)
	return 0
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
	if _, err := e.call("GET", "/api/health", nil, nil); err != nil {
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
	return public.Options{
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
	}, nil
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
	_, err := d.e.call("PUT", "/api/public", api.SetPublic{Origin: origin}, nil)
	return err
}

func (d daemon) Invite(context.Context) (string, time.Time, error) {
	var inv api.Invite
	if _, err := d.e.call("POST", "/api/terminals/invites", api.NewInvite{}, &inv); err != nil {
		return "", time.Time{}, err
	}
	return inv.URL, time.UnixMilli(inv.ExpiresAt), nil
}
