// iridium-qq is Iridium's official QQ plugin: a QQ bot whose private chats with its owners go to
// Iridium's sessions (docs/qq.md). It is not built into Iridium: a binary of its own, in the same
// release, installed when wanted (from the web's 终端管理, or by running it).
package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
	"rsc.io/qr"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/config"
	"github.com/kumoSleeping/iridium/internal/qq"
	"github.com/kumoSleeping/iridium/internal/qqbridge"
	"github.com/kumoSleeping/iridium/internal/version"
)

const usage = `iridium-qq: Iridium's QQ plugin. A QQ bot whose private chats with you go to Iridium.
  iridium-qq setup [--wait] [--rebind]   scan a QR code to make a bot, get Iridium's TOKEN, start the service
                                         (no terminal: prints the QR image and link, exit 2; run again once scanned)
  iridium-qq status [--json]             the bot, the owners, the service, the connections
  iridium-qq start | stop | restart      the service (systemd --user unit iridium-qq)
  iridium-qq logs [-n lines]             the service's log
  iridium-qq pair                        a code to send the bot, making that QQ account an owner
  iridium-qq remove                      stop the service, delete the bot's config (ir revoke <name> for the TOKEN)
  iridium-qq run                         run in the foreground (what the service runs)
  iridium-qq version
One bot, one connection: a bot in use elsewhere (marked 在线 on the scan page) is pushed off there.
`

func main() {
	log.SetFlags(0)
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		fmt.Print(usage)
		return 2
	}
	home := qqbridge.Home()
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "setup":
		return setup(home, rest)
	case "bind":
		return bind(home, rest)
	case "status":
		return status(home, rest)
	case "start", "restart":
		return start(home)
	case "stop":
		return stop()
	case "logs":
		return logs(rest)
	case "pair":
		return pair(home)
	case "remove":
		return remove(home)
	case "run":
		return serve(home)
	case "version", "--version", "-v":
		fmt.Println(version.Version)
		return 0
	case "help", "--help", "-h":
		fmt.Print(usage)
		return 0
	}
	fmt.Fprint(os.Stderr, usage)
	return 2
}

func isTerminal(f *os.File) bool {
	_, err := unix.IoctlGetTermios(int(f.Fd()), unix.TCGETS)
	return err == nil
}

// ---- setup ----

func setup(home string, args []string) int {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	wait := fs.Bool("wait", false, "wait for the scan")
	rebind := fs.Bool("rebind", false, "scan for a bot again")
	name := fs.String("name", "", "the terminal's name in Iridium (default QQ)")
	irURL := fs.String("iridium", "", "Iridium's address (default: its config's listen)")
	noStart := fs.Bool("no-start", false, "do not start the service")
	if fs.Parse(args) != nil {
		return 2
	}
	c, bound, err := qqbridge.LoadConfig(home)
	if err != nil {
		return fail(err)
	}
	if *name != "" {
		c.Name = *name
	}
	if *irURL != "" {
		c.Iridium = *irURL
	}
	if c.Iridium == "" {
		c.Iridium = iridiumURL()
	}
	if !bound || *rebind {
		waiting := *wait || isTerminal(os.Stdout)
		b, code := scan(home, waiting, *rebind)
		if code != 0 {
			return code
		}
		c.AppID, c.Secret = b.AppID, b.Secret
		if b.Owner != "" && !c.IsOwner(b.Owner) {
			c.Owners = append(c.Owners, b.Owner)
		}
		if err := qqbridge.Write(home, qqbridge.ConfigFile, &c); err != nil {
			return fail(err)
		}
		fmt.Printf("扫码完成：Bot %s。\n", b.AppID)
	} else if err := qqbridge.Write(home, qqbridge.ConfigFile, &c); err != nil {
		return fail(err)
	}
	if err := check(c); err != nil {
		fmt.Fprintf(os.Stderr, "QQ 不认这个 Bot：%v\n", err)
		return 1
	}
	if qqbridge.Token(home) == "" || *rebind {
		fmt.Printf("向 Iridium 要 Bot TOKEN（终端名 %s，带 Claude）……\n", c.Name)
		cmd := exec.Command("ir", "token", c.Name, "--owner", "--into", filepath.Join(home, qqbridge.TokenFile))
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil || qqbridge.Token(home) == "" {
			fmt.Fprintln(os.Stderr, "没拿到 TOKEN；之后再运行 iridium-qq setup。")
			return 1
		}
	}
	if len(c.Owners) == 0 {
		code := newPair(home)
		fmt.Printf("还没有主人：在 QQ 里给这个 Bot 发 %s（10 分钟内有效），发的人成为主人。\n", code)
	}
	if *noStart {
		return 0
	}
	if code := start(home); code != 0 {
		return code
	}
	fmt.Println("在 QQ 里给 Bot 发一句话试试。")
	return 0
}

// iridiumURL is the local Iridium's address, from its config.
func iridiumURL() string {
	listen := "127.0.0.1:7810"
	if cfg, err := config.Load(config.DefaultPath()); err == nil && cfg.Listen != "" {
		listen = cfg.Listen
	}
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "http://" + listen
	}
	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port)
}

// check asks QQ for the bot's access token: the AppID and secret work.
func check(c qqbridge.Config) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_, err := (&qq.Client{AppID: c.AppID, Secret: c.Secret}).Token(ctx)
	return err
}

// scan runs the QR flow: a task waiting from before is polled, else a new one made. Waiting, it
// polls till done; else it prints the QR code and gives exit code 2.
func scan(home string, waiting, fresh bool) (qq.Bound, int) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	p := qq.Portal{}
	var b qq.Bind
	have, _ := qqbridge.Read(home, qqbridge.BindFile, &b)
	if fresh || !have || time.Since(time.UnixMilli(b.At)) > 30*time.Minute {
		have = false
	}
	for {
		if !have {
			var err error
			if b, err = p.NewBind(ctx); err != nil {
				return qq.Bound{}, fail(err)
			}
			if err := qqbridge.Write(home, qqbridge.BindFile, &b); err != nil {
				return qq.Bound{}, fail(err)
			}
			png, err := qrPNG(b.URL)
			if err == nil {
				err = qqbridge.WriteFile(home, qqbridge.QRFile, png)
			}
			if err != nil {
				return qq.Bound{}, fail(err)
			}
			if isTerminal(os.Stdout) {
				fmt.Print(qrText(b.URL))
			}
			fmt.Printf("用手机 QQ 扫码，选“创建新机器人”（或在 QQ 里打开链接）：\n  %s\n二维码图片：%s\n标“在线”的 Bot 正被别处使用，选了会从那边断开。\n", b.URL, filepath.Join(home, qqbridge.QRFile))
			have = true
			if !waiting {
				fmt.Println("扫码后再运行 iridium-qq setup。")
				return qq.Bound{}, 2
			}
		}
		st, bound, err := p.Poll(ctx, b)
		if err != nil {
			return qq.Bound{}, fail(err)
		}
		switch st {
		case qq.BindDone:
			os.Remove(filepath.Join(home, qqbridge.BindFile))
			os.Remove(filepath.Join(home, qqbridge.QRFile))
			return bound, 0
		case qq.BindExpired:
			fmt.Println("二维码已过期，换一个。")
			have = false
			continue
		}
		if !waiting {
			fmt.Printf("还没扫码：%s\n二维码图片：%s\n", b.URL, filepath.Join(home, qqbridge.QRFile))
			return qq.Bound{}, 2
		}
		select {
		case <-ctx.Done():
			return qq.Bound{}, 130
		case <-time.After(2 * time.Second):
		}
	}
}

func qrPNG(text string) ([]byte, error) {
	c, err := qr.Encode(text, qr.M)
	if err != nil {
		return nil, err
	}
	c.Scale = 8
	return c.PNG(), nil
}

// qrText draws text's QR code with half blocks, two rows a line, a quiet zone round it.
func qrText(text string) string {
	c, err := qr.Encode(text, qr.L)
	if err != nil {
		return ""
	}
	black := func(x, y int) bool { return x >= 0 && y >= 0 && x < c.Size && y < c.Size && c.Black(x, y) }
	var sb strings.Builder
	for y := -2; y < c.Size+2; y += 2 {
		for x := -2; x < c.Size+2; x++ {
			top, bottom := black(x, y), black(x, y+1)
			switch {
			case top && bottom:
				sb.WriteRune(' ')
			case top:
				sb.WriteRune('▄')
			case bottom:
				sb.WriteRune('▀')
			default:
				sb.WriteRune('█')
			}
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}

// bind is the QR flow for Iridium's page: JSON lines on stdout, {"state":"qr","url":…} for each
// QR code, then {"state":"bound","appId":…,"owner":bool} once scanned (the config saved), or
// {"state":"error","error":…}. Iridium puts the TOKEN in place and starts the service after.
func bind(home string, args []string) int {
	fs := flag.NewFlagSet("bind", flag.ContinueOnError)
	irURL := fs.String("iridium", "", "Iridium's address")
	name := fs.String("name", "", "the terminal's name in Iridium")
	if fs.Parse(args) != nil {
		return 2
	}
	out := json.NewEncoder(os.Stdout)
	say := func(v map[string]any) { _ = out.Encode(v) }
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	p := qq.Portal{}
	for {
		b, err := p.NewBind(ctx)
		if err != nil {
			say(map[string]any{"state": "error", "error": err.Error()})
			return 1
		}
		say(map[string]any{"state": "qr", "url": b.URL})
		for {
			select {
			case <-ctx.Done():
				say(map[string]any{"state": "error", "error": "已取消"})
				return 1
			case <-time.After(2 * time.Second):
			}
			st, bound, err := p.Poll(ctx, b)
			if err != nil {
				say(map[string]any{"state": "error", "error": err.Error()})
				return 1
			}
			if st == qq.BindExpired {
				break
			}
			if st != qq.BindDone {
				continue
			}
			c, _, _ := qqbridge.LoadConfig(home)
			if c.AppID != bound.AppID {
				c.Owners = nil // another bot: its openids are not this one's
			}
			c.AppID, c.Secret = bound.AppID, bound.Secret
			if bound.Owner != "" && !c.IsOwner(bound.Owner) {
				c.Owners = append(c.Owners, bound.Owner)
			}
			if *irURL != "" {
				c.Iridium = *irURL
			}
			if c.Iridium == "" {
				c.Iridium = iridiumURL()
			}
			if *name != "" {
				c.Name = *name
			}
			if err := qqbridge.Write(home, qqbridge.ConfigFile, &c); err != nil {
				say(map[string]any{"state": "error", "error": err.Error()})
				return 1
			}
			if err := check(c); err != nil {
				say(map[string]any{"state": "error", "error": "QQ 不认这个 Bot：" + err.Error()})
				return 1
			}
			res := map[string]any{"state": "bound", "appId": c.AppID, "owner": len(c.Owners) > 0}
			if len(c.Owners) == 0 {
				res["pair"] = newPair(home)
			}
			say(res)
			return 0
		}
	}
}

// ---- pairing ----

func newPair(home string) string {
	n, _ := rand.Int(rand.Reader, big.NewInt(900000))
	code := fmt.Sprintf("%06d", n.Int64()+100000)
	_ = qqbridge.Write(home, qqbridge.PairFile, qqbridge.Pair{Code: code, Expires: time.Now().Add(10 * time.Minute).UnixMilli()})
	return code
}

func pair(home string) int {
	if _, ok, err := qqbridge.LoadConfig(home); err != nil || !ok {
		return fail(errors.New("还没有 Bot：先运行 iridium-qq setup"))
	}
	fmt.Printf("在 QQ 里给 Bot 发 %s（10 分钟内有效），发的人成为主人。\n", newPair(home))
	return 0
}

// ---- status ----

// State is iridium-qq status --json, which Iridium's page reads too.
type State = api.ExtensionStatus

func state(home string) State {
	c, ok, _ := qqbridge.LoadConfig(home)
	s := State{Version: version.Version, Home: home, Bound: ok, AppID: c.AppID, Name: c.Name, Owners: len(c.Owners), Iridium: c.Iridium, Token: qqbridge.Token(home) != ""}
	s.Service = systemctlOut("show", "-p", "ActiveState", "--value", unit+".service")
	if s.Service == "inactive" && !unitExists() {
		s.Service = ""
	}
	s.Enabled = systemctlOut("is-enabled", unit+".service") == "enabled"
	if s.Service == "active" {
		var st qqbridge.Status
		if ok, _ := qqbridge.Read(home, qqbridge.StatusFile, &st); ok {
			s.Running = &st
		}
	}
	var p qqbridge.Pair
	if ok, _ := qqbridge.Read(home, qqbridge.PairFile, &p); ok && time.Now().UnixMilli() < p.Expires {
		s.Pair = p.Code
	}
	return s
}

func status(home string, args []string) int {
	s := state(home)
	if len(args) > 0 && args[0] == "--json" {
		b, _ := json.Marshal(s)
		fmt.Println(string(b))
		return 0
	}
	if !s.Bound {
		fmt.Println("还没有 Bot：运行 iridium-qq setup。")
		return 1
	}
	fmt.Printf("Bot %s，终端名 %s，主人 %d 个，Iridium %s，TOKEN %s\n", s.AppID, s.Name, s.Owners, s.Iridium, map[bool]string{true: "有", false: "无"}[s.Token])
	switch {
	case s.Service == "":
		fmt.Println("服务：没装（iridium-qq start）")
	case s.Running != nil:
		qqs, irs := "已连接", "已连接"
		if !s.Running.QQ {
			qqs = "未连接 " + s.Running.QQError
		}
		if !s.Running.Iridium {
			irs = "未连接 " + s.Running.IridiumError
		}
		fmt.Printf("服务：%s；QQ %s；Iridium %s\n", s.Service, qqs, irs)
	default:
		fmt.Printf("服务：%s\n", s.Service)
	}
	if s.Pair != "" {
		fmt.Printf("配对码 %s 等着\n", s.Pair)
	}
	return 0
}

// ---- the service ----

// unit is the systemd --user unit's name (IRIDIUM_QQ_UNIT for tests).
var unit = func() string {
	if u := os.Getenv("IRIDIUM_QQ_UNIT"); u != "" {
		return u
	}
	return "iridium-qq"
}()

func unitPath() string {
	d, err := os.UserConfigDir()
	if err != nil {
		d = filepath.Join(os.Getenv("HOME"), ".config")
	}
	return filepath.Join(d, "systemd", "user", unit+".service")
}

func unitExists() bool {
	_, err := os.Stat(unitPath())
	return err == nil
}

func systemctl(args ...string) error {
	cmd := exec.Command("systemctl", append([]string{"--user"}, args...)...)
	cmd.Stdout, cmd.Stderr = io.Discard, os.Stderr
	return cmd.Run()
}

func systemctlOut(args ...string) string {
	out, _ := exec.Command("systemctl", append([]string{"--user"}, args...)...).Output()
	return strings.TrimSpace(string(out))
}

func start(home string) int {
	c, ok, err := qqbridge.LoadConfig(home)
	if err != nil || !ok {
		return fail(errors.New("还没有 Bot：先运行 iridium-qq setup"))
	}
	if qqbridge.Token(home) == "" {
		return fail(errors.New("没有 Iridium 的 TOKEN：先运行 iridium-qq setup"))
	}
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		return fail(err)
	}
	env := ""
	if h := os.Getenv("IRIDIUM_QQ_HOME"); h != "" {
		env = "Environment=IRIDIUM_QQ_HOME=" + h + "\n"
	}
	body := fmt.Sprintf(`[Unit]
Description=Iridium QQ plugin (%s)
After=network-online.target

[Service]
ExecStart=%s run
%sRestart=always
RestartSec=5

[Install]
WantedBy=default.target
`, c.AppID, exe, env)
	if err := os.MkdirAll(filepath.Dir(unitPath()), 0o755); err != nil {
		return fail(err)
	}
	if err := os.WriteFile(unitPath(), []byte(body), 0o644); err != nil {
		return fail(err)
	}
	for _, a := range [][]string{{"daemon-reload"}, {"enable", unit + ".service"}, {"restart", unit + ".service"}} {
		if err := systemctl(a...); err != nil {
			return fail(fmt.Errorf("systemctl --user %s: %w", strings.Join(a, " "), err))
		}
	}
	fmt.Printf("服务已启动（%s）。\n", unit)
	return 0
}

func stop() int {
	if !unitExists() {
		fmt.Println("服务没装。")
		return 0
	}
	if err := systemctl("disable", "--now", unit+".service"); err != nil {
		return fail(err)
	}
	fmt.Println("服务已停止。")
	return 0
}

func logs(args []string) int {
	n := "100"
	if len(args) == 2 && args[0] == "-n" {
		n = args[1]
	}
	cmd := exec.Command("journalctl", "--user", "-u", unit+".service", "-n", n, "--no-pager", "-o", "short-iso")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return 1
	}
	return 0
}

func remove(home string) int {
	c, _, _ := qqbridge.LoadConfig(home)
	if unitExists() {
		_ = systemctl("disable", "--now", unit+".service")
		os.Remove(unitPath())
		_ = systemctl("daemon-reload")
	}
	if err := os.RemoveAll(home); err != nil {
		return fail(err)
	}
	fmt.Printf("已删除服务和 %s。Iridium 里的终端用 ir revoke %s 撤销。\n", home, c.Name)
	return 0
}

// serve runs the bridge in the foreground till a signal.
func serve(home string) int {
	c, ok, err := qqbridge.LoadConfig(home)
	if err != nil || !ok {
		return fail(errors.New("还没有 Bot：先运行 iridium-qq setup"))
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	b := &qqbridge.Bridge{
		Home:   home,
		Config: c,
		QQ:     &qq.Client{AppID: c.AppID, Secret: c.Secret},
		IR:     &qqbridge.Iridium{URL: c.Iridium, Token: func() string { return qqbridge.Token(home) }},
		Logf:   log.Printf,
	}
	log.Printf("iridium-qq %s: bot %s, Iridium %s", version.Version, c.AppID, c.Iridium)
	_ = b.Run(ctx)
	return 0
}

func fail(err error) int {
	fmt.Fprintln(os.Stderr, "iridium-qq:", err)
	return 1
}
