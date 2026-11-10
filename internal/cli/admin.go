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
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	irAccess "github.com/kumoSleeping/iridium/internal/access"
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
	"invite":  invite,
	"apps":    apps,
	"profile": profile,
	"public":  publicCmd,
	"update":  updateCmd,
	"secret":  secretCmd, // secrets.go
	"run":     runCmd,
	"usage":   usageCmd, // usage.go
	"needs":   needsCmd, // needs.go
	"allow":   allowCmd,
	"deny":    denyCmd,
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

// claudeFlag takes --claude out of args: the new login is the user's own, so it gets Claude.
func claudeFlag(args []string) (rest []string, claude bool) {
	for _, a := range args {
		if a == "--claude" {
			claude = true
		} else {
			rest = append(rest, a)
		}
	}
	return rest, claude
}

// invite: ir invite [名字] [--claude], a slot for a new browser of ir_web. A person gets the link;
// an agent, only what the person who confirms decided. --claude: the user's own, with Claude; an
// agent's waits for a login that has Claude (or the host) to allow it.
func invite(e adminEnv, args []string, stdout, stderr io.Writer) int {
	args, claude := claudeFlag(args)
	if len(args) > 1 || (len(args) == 1 && strings.HasPrefix(args[0], "-")) {
		fmt.Fprintln(stderr, "用法：ir invite [名字] [--claude]\n  --claude：本人用的浏览器，带 Claude")
		return 2
	}
	req := api.NewInvite{Claude: claude}
	if len(args) == 1 {
		req.Name = args[0]
	}
	var raw json.RawMessage
	code, err := e.call("POST", "/api/invites", req, &raw)
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
	with := "不带 Claude"
	if claude {
		with = "带 Claude"
	}
	fmt.Fprintf(stdout, "网页的邀请（%s 前有效，只能用一次，%s）：\n\n  %s\n\n在那台设备的浏览器里打开，起名、建 Passkey。\n", hhmm(inv.ExpiresAt), with, inv.URL)
	return 0
}

// waitConfirm waits for a person to answer what an agent asked for, and says only the outcome.
func waitConfirm(e adminEnv, raw json.RawMessage, stdout, stderr io.Writer) int {
	var p api.Pending
	if err := json.Unmarshal(raw, &p); err != nil {
		return failed(stderr, "need", err)
	}
	if p.Need.Kind == api.NeedSecretSet {
		fmt.Fprintf(stdout, "%s 的值要人填：已发到“需要”，%s 前有效。等待中\n", p.Need.Name, hhmm(p.Need.ExpiresAt))
		st, err := waitState(e, p.Need.ID)
		if err != nil {
			return failed(stderr, "secret set", err)
		}
		if st.State == "allowed" {
			fmt.Fprintf(stdout, "已存 %s。用法：ir run -- 命令 …{{%s}}…\n", st.Name, st.Name)
			return 0
		}
		fmt.Fprintln(stdout, outcome(st))
		return 1
	}
	what := "网页的邀请"
	if p.Need.Name != "" {
		what += "（" + p.Need.Name + "）"
	}
	if p.Need.Owner {
		what += "，带 Claude"
	}
	fmt.Fprintf(stdout, "%s要主机或网页登录允许：已发到“需要”（%s），%s 前有效。等待中\n", what, p.Need.ID, hhmm(p.Need.ExpiresAt))
	for {
		var st api.NeedState
		if _, err := e.call("GET", "/api/needs/"+url.PathEscape(p.Need.ID)+"?wait=60", nil, &st); err != nil {
			return failed(stderr, "need", err)
		}
		switch st.State {
		case "pending":
			continue
		case "allowed":
			claude := ""
			if st.Owner {
				claude = "，带 Claude"
			}
			fmt.Fprintf(stdout, "已允许%s：邀请链接只显示在允许的那一处（%s 前有效）。\n", claude, hhmm(st.ExpiresAt))
			return 0
		case "denied":
			fmt.Fprintln(stdout, "已拒绝。")
		case "expired":
			fmt.Fprintln(stdout, "已过期，无人处理。")
		default:
			fmt.Fprintf(stdout, "失败：%s\n", st.Error)
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

const appsUsage = `用法：ir apps                       应用、主机和网页；带电脑的再列系统、agent、命令行
      ir apps agents <应用> [agent=on|off …]  它带的 agent 开关
      ir apps logins [应用]           登录和未用的邀请
      ir apps rename <应用> <新名>     改名；登录写 应用/登录，如 ir_web/手机
      ir apps rm <应用>               删除应用、登录或未用的邀请
      ir apps open [时长]             开放配对（默认 10m，最多 60m）
      ir apps close                  关闭配对`

// apps: ir apps [logins [应用] | rename <应用> <新名> | rm <应用>]: the apps, the host and ir_web,
// their logins, renaming and removing (nothing secret). Claude is set as each was made, never
// changed (the user, 2026-11-08).
func apps(e adminEnv, args []string, stdout, stderr io.Writer) int {
	usage := func() int { fmt.Fprintln(stderr, appsUsage); return 2 }
	sub := ""
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case "", "ls":
		if len(args) > 0 {
			return usage()
		}
		return appsList(e, stdout, stderr)
	case "logins":
		if len(args) > 1 {
			return usage()
		}
		app := ""
		if len(args) == 1 {
			app = args[0]
		}
		return appsLogins(e, app, stdout, stderr)
	case "rename":
		if len(args) != 2 {
			return usage()
		}
		var info struct {
			Name string `json:"name"`
		}
		if _, err := e.call("PUT", "/api/apps/"+url.PathEscape(args[0])+"/profile", map[string]string{"name": args[1]}, &info); err != nil {
			return failed(stderr, "apps rename", err)
		}
		fmt.Fprintf(stdout, "已改名：%s\n", info.Name)
		return 0
	case "agents":
		if len(args) < 1 {
			return usage()
		}
		return appsAgents(e, args, stdout, stderr)
	case "open", "close":
		return pairingCmd(e, sub == "open", args, stdout, stderr)
	case "rm":
		if len(args) != 1 {
			return usage()
		}
		if _, err := e.call("DELETE", "/api/apps/"+url.PathEscape(args[0]), nil, nil); err != nil {
			return failed(stderr, "apps rm", err)
		}
		fmt.Fprintf(stdout, "已删除 %s。\n", args[0])
		return 0
	}
	return usage()
}

func claudeMark(r api.Rules) string {
	if irAccess.HasClaude(r) {
		return "开"
	}
	return "—"
}

func appsList(e adminEnv, stdout, stderr io.Writer) int {
	var as api.Apps
	if _, err := e.call("GET", "/api/apps", nil, &as); err != nil {
		return failed(stderr, "apps", err)
	}
	printApps(stdout, as)
	return 0
}

var systems = map[string]string{"windows": "Windows", "darwin": "macOS", "linux": "Linux"}

// printApps prints the apps, then the computers of those that have one: system, connection, agents
// (those turned off marked), command lines, home. Rules and usage only in the list of one that
// manages (Full).
func printApps(w io.Writer, as api.Apps) {
	rows := [][]string{{"名字", "类型", "ID", "在线", "上次"}}
	if as.Full {
		rows[0] = []string{"名字", "类型", "ID", "Claude", "在线", "今天用量", "上次"}
	}
	type comp struct {
		name string
		c    *api.Computer
	}
	var comps []comp
	for _, a := range as.Apps {
		name, typ, claude := a.Name, a.Type, claudeMark(a.Rules)
		switch {
		case a.Host:
			name, typ = "主机", "内置"
		case a.Web:
			name, typ, claude = "网页", "内置", "按登录"
			if a.Logins > 0 {
				typ = fmt.Sprintf("内置，%d 个登录", a.Logins)
			}
		}
		if typ == "" {
			typ = "—"
		}
		row := []string{name, typ, a.ID, strconv.Itoa(a.Online), when(a.LastSeenAt)}
		if as.Full {
			row = []string{name, typ, a.ID, claude, strconv.Itoa(a.Online), strconv.FormatInt(a.UsedToday, 10), when(a.LastSeenAt)}
		}
		rows = append(rows, row)
		if a.Computer != nil && !a.Host {
			comps = append(comps, comp{a.Name, a.Computer})
		}
	}
	table(w, rows)
	if len(comps) == 0 {
		return
	}
	fmt.Fprintln(w, "\n电脑：")
	rows = [][]string{{"应用", "系统", "连接", "agent", "命令行", "家目录"}}
	for _, x := range comps {
		c := x.c
		sys := strings.TrimSpace(cmp.Or(systems[c.OS], c.OS) + " " + c.Arch)
		link := "已断开"
		if c.Online {
			link = "在线"
			if c.Latency > 0 {
				link = fmt.Sprintf("%d ms", c.Latency)
			}
		}
		var ags []string
		for _, a := range c.Agents {
			s := strings.TrimSpace(a.Agent + " " + a.Version)
			if a.Off {
				s += "（关）"
			}
			ags = append(ags, s)
		}
		sh := "关"
		if c.Shell {
			sh = "开"
		}
		rows = append(rows, []string{x.name, cmp.Or(sys, "—"), link, cmp.Or(strings.Join(ags, "、"), "—"), sh, c.Home})
	}
	table(w, rows)
}

// appsAgents is ir apps agents <应用> [agent=on|off …]: the agents app carries, each on or off
// there; with arguments, turns those on or off first.
func appsAgents(e adminEnv, args []string, stdout, stderr io.Writer) int {
	var as api.Apps
	if _, err := e.call("GET", "/api/apps", nil, &as); err != nil {
		return failed(stderr, "apps agents", err)
	}
	i := slices.IndexFunc(as.Apps, func(a api.App) bool {
		return !a.Host && !a.Web && (a.ID == args[0] || strings.EqualFold(a.Name, args[0]))
	})
	if i < 0 {
		return failed(stderr, "apps agents", fmt.Errorf("没有应用 %s", args[0]))
	}
	a := as.Apps[i]
	if a.Computer == nil {
		return failed(stderr, "apps agents", fmt.Errorf("%s 没有报过电脑和 agent", a.Name))
	}
	off := map[string]bool{}
	for _, x := range a.Computer.Agents {
		off[x.Agent] = x.Off
	}
	for _, arg := range args[1:] {
		name, v, ok := strings.Cut(arg, "=")
		if _, there := off[name]; !ok || !there || (v != "on" && v != "off") {
			fmt.Fprintln(stderr, appsUsage)
			return 2
		}
		off[name] = v == "off"
	}
	if len(args) > 1 {
		req := api.AppAgents{Off: []string{}}
		for _, x := range a.Computer.Agents {
			if off[x.Agent] {
				req.Off = append(req.Off, x.Agent)
			}
		}
		if _, err := e.call("PUT", "/api/apps/"+url.PathEscape(a.ID)+"/agents", req, nil); err != nil {
			return failed(stderr, "apps agents", err)
		}
	}
	rows := [][]string{}
	for _, x := range a.Computer.Agents {
		state := "开"
		if off[x.Agent] {
			state = "关"
		}
		rows = append(rows, []string{x.Agent, x.Version, state})
	}
	if len(rows) == 0 {
		fmt.Fprintf(stdout, "%s 没有可用的 agent。\n", a.Name)
		return 0
	}
	table(stdout, rows)
	return 0
}

// appsLogins lists the logins (of app, "" all) and the invites not taken.
func appsLogins(e adminEnv, app string, stdout, stderr io.Writer) int {
	var as api.Apps
	if _, err := e.call("GET", "/api/apps", nil, &as); err != nil {
		return failed(stderr, "apps logins", err)
	}
	if app != "" {
		id := ""
		for _, a := range as.Apps {
			if !a.Host && (a.ID == app || strings.EqualFold(a.Name, app)) {
				id = a.ID
			}
		}
		if id == "" {
			return failed(stderr, "apps logins", fmt.Errorf("没有应用 %s", app))
		}
		app = id
	}
	names := map[string]string{api.WebApp: api.WebApp}
	for _, a := range as.Apps {
		if !a.Host && !a.Web {
			names[a.ID] = a.Name
		}
	}
	rows := [][]string{{"登录", "ID", "Claude", "浏览器", "今天用量", "上次"}}
	for _, l := range as.Logins {
		if app != "" && l.App != app {
			continue
		}
		name := names[l.App] + "/" + l.Name
		if l.ApprovedAt == 0 {
			name += "（待批）"
		}
		rows = append(rows, []string{name, l.ID, claudeMark(l.Rules), strconv.Itoa(l.Sessions), strconv.FormatInt(l.UsedToday, 10), when(l.LastSeenAt)})
	}
	table(stdout, rows)
	if len(as.Invites) > 0 && (app == "" || app == api.WebApp) {
		fmt.Fprintln(stdout, "\n未用的邀请：")
		rows = rows[:0]
		for _, i := range as.Invites {
			until := "到 " + hhmm(i.ExpiresAt)
			if i.Setup {
				until = "首次设置（状态目录里的 setup-token）"
			}
			if i.Claude {
				until += "，带 Claude"
			}
			rows = append(rows, []string{"", i.ID, i.Name, until})
		}
		table(stdout, rows)
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

// profile: ir profile <应用> [--avatar 图片] [--no-avatar] [--device computer|laptop|phone|none]: an
// app's, a login's (应用/登录) or the host's (device only) avatar and device. Renaming is ir apps
// rename.
func profile(e adminEnv, args []string, stdout, stderr io.Writer) int {
	usage := func() int {
		fmt.Fprintln(stderr, "用法：ir profile <应用> [--avatar 图片] [--no-avatar] [--device computer|laptop|phone|none]\n  登录写 应用/登录；改名用 ir apps rename")
		return 2
	}
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return usage()
	}
	ref, rest := args[0], args[1:]
	body := &multipartBody{}
	mw := multipart.NewWriter(&body.buf)
	changed, device := false, ""
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
		case "--device":
			v, ok := next()
			if !ok || !api.Device(v).Valid() {
				return usage()
			}
			device = v
			continue
		default:
			return usage()
		}
		changed = true
	}
	if !changed && device == "" {
		return usage()
	}
	if device != "" {
		var d api.SetDevice
		if _, err := e.call("PUT", "/api/apps/"+url.PathEscape(ref)+"/device", api.SetDevice{Device: api.Device(device)}, &d); err != nil {
			return failed(stderr, "profile", err)
		}
		if !changed {
			fmt.Fprintf(stdout, "已改：%s（%s）\n", ref, deviceName(d.Device))
			return 0
		}
	}
	mw.Close()
	body.ct = mw.FormDataContentType()
	var info struct {
		Name   string `json:"name"`
		Avatar string `json:"avatar"`
	}
	if _, err := e.call("PUT", "/api/apps/"+url.PathEscape(ref)+"/profile", body, &info); err != nil {
		return failed(stderr, "profile", err)
	}
	fmt.Fprintf(stdout, "已改：%s%s\n", info.Name, map[bool]string{true: "（有头像）", false: ""}[info.Avatar != ""])
	return 0
}

func deviceName(d api.Device) string {
	switch d {
	case api.DeviceLaptop:
		return "笔记本"
	case api.DevicePhone:
		return "手机"
	case api.DeviceNone:
		return "不展示"
	}
	return "电脑"
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
	_, err := d.e.call("PUT", "/api/public", api.SetPublic{Origin: origin}, nil)
	return err
}

func (d daemon) Invite(context.Context) (string, time.Time, error) {
	var inv api.Invite
	if _, err := d.e.call("POST", "/api/invites", api.NewInvite{}, &inv); err != nil {
		return "", time.Time{}, err
	}
	return inv.URL, time.UnixMilli(inv.ExpiresAt), nil
}
