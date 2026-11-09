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
	"slices"
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

const usage = `iridium-qq: Iridium's QQ plugin. QQ bots whose private chats with you go to Iridium.
  iridium-qq setup [--wait] [--new]      scan a QR code to make a bot, get Iridium's TOKEN, start the service
                                         (no terminal: prints the QR image and link, exit 2; run again once scanned)
                                         --new: one more bot when there is one already
  iridium-qq list [--json]               every bot: its terminal, owners, service, connections
  iridium-qq status [--json]             one bot
  iridium-qq start | stop | restart      the services (restart: those running)
  iridium-qq logs [-n lines]             a service's log
  iridium-qq pair                        a code to send the bot, making that QQ account an owner
  iridium-qq remove [--all]              stop a bot's service, delete its config (ir revoke <name> for the TOKEN)
  iridium-qq run                         run a bot in the foreground (what its service runs)
  iridium-qq version
A bot each: a directory (bots/<AppID>) and a service (iridium-qq-<AppID>). --bot <AppID> picks
one; without it a command takes the only bot, and start, stop, restart every bot.
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
	root := qqbridge.Home()
	cmd := args[0]
	bot, rest, err := takeBot(args[1:])
	if err != nil {
		return fail(err)
	}
	switch cmd {
	case "version", "--version", "-v":
		fmt.Println(version.Version)
		return 0
	case "help", "--help", "-h":
		fmt.Print(usage)
		return 0
	}
	migrate(root, cmd != "run")
	switch cmd {
	case "setup":
		return setup(root, bot, rest)
	case "bind":
		return bind(root, rest)
	case "list":
		return list(root, rest)
	case "status":
		return status(root, bot, rest)
	case "start":
		return each(root, bot, false, func(id string) error { return startBot(root, id) }, "服务已启动（%s）。\n")
	case "restart":
		return each(root, bot, true, func(id string) error { return startBot(root, id) }, "服务已重启（%s）。\n")
	case "stop":
		return each(root, bot, false, stopBot, "服务已停止（%s）。\n")
	case "logs", "pair", "run":
		id, err := pick(root, bot)
		if err != nil {
			return fail(err)
		}
		switch cmd {
		case "logs":
			return logs(id, rest)
		case "pair":
			return pair(root, id)
		}
		return serve(root, id)
	case "remove":
		return remove(root, bot, rest)
	}
	fmt.Fprint(os.Stderr, usage)
	return 2
}

// takeBot takes --bot <AppID> (or --bot=<AppID>) out of args.
func takeBot(args []string) (string, []string, error) {
	var bot string
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--bot" || a == "-bot":
			if i+1 == len(args) {
				return "", nil, errors.New("--bot: which bot (its AppID)")
			}
			i++
			bot = args[i]
		case strings.HasPrefix(a, "--bot="):
			bot = strings.TrimPrefix(a, "--bot=")
		default:
			rest = append(rest, a)
		}
	}
	if bot != "" && !qqbridge.ValidAppID(bot) {
		return "", nil, fmt.Errorf("--bot %q: not an AppID", bot)
	}
	return bot, rest, nil
}

// pick is the bot a command is about: the one named, else the only one.
func pick(root, bot string) (string, error) {
	bots := qqbridge.Bots(root)
	if bot != "" {
		if !slices.Contains(bots, bot) {
			return "", fmt.Errorf("没有 Bot %s（iridium-qq list）", bot)
		}
		return bot, nil
	}
	switch len(bots) {
	case 0:
		return "", errors.New("还没有 Bot：先运行 iridium-qq setup")
	case 1:
		return bots[0], nil
	}
	return "", fmt.Errorf("有 %d 个 Bot（%s）：用 --bot 指定一个", len(bots), strings.Join(bots, "、"))
}

// each runs f on the bot named, else on every bot (running: only those whose service is active).
func each(root, bot string, running bool, f func(string) error, done string) int {
	ids := []string{bot}
	if bot == "" {
		ids = qqbridge.Bots(root)
		if len(ids) == 0 {
			return fail(errors.New("还没有 Bot：先运行 iridium-qq setup"))
		}
	} else if _, err := pick(root, bot); err != nil {
		return fail(err)
	}
	code := 0
	for _, id := range ids {
		if running && bot == "" {
			if st, _ := service(unitOf(id)); st != "active" {
				continue
			}
		}
		if err := f(id); err != nil {
			code = fail(fmt.Errorf("Bot %s: %w", id, err))
			continue
		}
		fmt.Printf(done, unitOf(id))
	}
	return code
}

// migrate moves the one bot of before (its files in root, its service iridium-qq) to where a bot
// is now; units: its service too, started again under its new name if it ran (not from run,
// which the old service itself may be running).
func migrate(root string, units bool) {
	id := qqbridge.MoveLegacy(root)
	if !units {
		return
	}
	if id == "" {
		if bots := qqbridge.Bots(root); len(bots) == 1 {
			id = bots[0]
		}
	}
	if !dropUnit(unitPrefix) || id == "" {
		return
	}
	if err := startBot(root, id); err != nil {
		fmt.Fprintf(os.Stderr, "iridium-qq: Bot %s 的服务：%v\n", id, err)
	}
}

func isTerminal(f *os.File) bool {
	_, err := unix.IoctlGetTermios(int(f.Fd()), ioctlGetTermios)
	return err == nil
}

// ---- setup ----

func setup(root, bot string, args []string) int {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	wait := fs.Bool("wait", false, "wait for the scan")
	more := fs.Bool("new", false, "scan for one more bot")
	rebind := fs.Bool("rebind", false, "scan again (the same bot is updated, another added)")
	name := fs.String("name", "", "the terminal's name in Iridium (default QQ, QQ2…)")
	irURL := fs.String("iridium", "", "Iridium's address (default: its config's listen)")
	noStart := fs.Bool("no-start", false, "do not start the service")
	if fs.Parse(args) != nil {
		return 2
	}
	scanning := *more || *rebind
	id := bot
	if !scanning && id == "" {
		switch bots := qqbridge.Bots(root); len(bots) {
		case 0:
			scanning = true
		case 1:
			id = bots[0]
		default:
			return fail(fmt.Errorf("有 %d 个 Bot（%s）：--bot 指定一个，--new 再加一个", len(bots), strings.Join(bots, "、")))
		}
	} else if !scanning {
		if _, err := pick(root, id); err != nil {
			return fail(err)
		}
	}
	var c qqbridge.Config
	had := true
	if scanning {
		b, code := scan(root, *wait || isTerminal(os.Stdout))
		if code != 0 {
			return code
		}
		if !qqbridge.ValidAppID(b.AppID) {
			return fail(fmt.Errorf("QQ gave an AppID of %q", b.AppID))
		}
		id = b.AppID
		var err error
		if c, had, err = qqbridge.LoadConfig(qqbridge.BotHome(root, id)); err != nil {
			return fail(err)
		}
		c.AppID, c.Secret = b.AppID, b.Secret
		if b.Owner != "" && !c.IsOwner(b.Owner) {
			c.Owners = append(c.Owners, b.Owner)
		}
		fmt.Printf("扫码完成：Bot %s。\n", b.AppID)
	} else {
		var err error
		if c, _, err = qqbridge.LoadConfig(qqbridge.BotHome(root, id)); err != nil {
			return fail(err)
		}
	}
	home := qqbridge.BotHome(root, id)
	if *name != "" {
		c.Name = *name
	}
	if *irURL != "" {
		c.Iridium = *irURL
	}
	if c.Iridium == "" {
		c.Iridium = iridiumURL()
	}
	qqName, err := check(c)
	if err != nil {
		fmt.Fprintf(os.Stderr, "QQ 不认这个 Bot：%v\n", err)
		return 1
	}
	if !had && *name == "" {
		c.Name = qqbridge.FreeName(root, id, qqName)
	}
	if err := qqbridge.Write(home, qqbridge.ConfigFile, &c); err != nil {
		return fail(err)
	}
	if qqbridge.Token(home) == "" || tokenDead(home, c) {
		fmt.Printf("向 Iridium 要 Bot TOKEN（终端名 %s，带 Claude）……\n", c.Name)
		cmd := exec.Command("ir", "token", c.Name, "--owner", "--into", filepath.Join(home, qqbridge.TokenFile))
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil || qqbridge.Token(home) == "" {
			fmt.Fprintf(os.Stderr, "没拿到 TOKEN；之后再运行 iridium-qq setup --bot %s。\n", id)
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
	if err := startBot(root, id); err != nil {
		return fail(err)
	}
	fmt.Printf("服务已启动（%s）。在 QQ 里给 Bot 发一句话试试。\n", unitOf(id))
	return 0
}

// irOf is the bot's side of Iridium's plugin API.
func irOf(home string, c qqbridge.Config) *qqbridge.Iridium {
	return &qqbridge.Iridium{URL: c.Iridium, Token: func() string { return qqbridge.Token(home) }}
}

// liveName is the bot's terminal's name in Iridium now (renamed on the page: its config follows),
// else its config's.
func liveName(home string, c qqbridge.Config) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return qqbridge.SyncName(ctx, irOf(home, c), home)
}

// tokenDead: Iridium refuses the bot's TOKEN (its terminal revoked).
func tokenDead(home string, c qqbridge.Config) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := irOf(home, c).Me(ctx)
	return errors.Is(err, qqbridge.ErrToken)
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

// check asks QQ for the bot's access token (the AppID and secret work), and gives its name on QQ
// ("" if QQ does not say).
func check(c qqbridge.Config) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cl := &qq.Client{AppID: c.AppID, Secret: c.Secret}
	if _, err := cl.Token(ctx); err != nil {
		return "", err
	}
	me, _ := cl.Me(ctx)
	return me.Username, nil
}

// scan runs the QR flow: a task waiting from before is polled, else a new one made. Waiting, it
// polls till done; else it prints the QR code and gives exit code 2.
func scan(home string, waiting bool) (qq.Bound, int) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	p := qq.Portal{}
	var b qq.Bind
	have, _ := qqbridge.Read(home, qqbridge.BindFile, &b)
	if !have || time.Since(time.UnixMilli(b.At)) > 30*time.Minute {
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
				fmt.Println("扫码后再运行一次同样的命令。")
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
// QR code, then {"state":"bound","appId":…,"name":…,"owner":bool} once scanned (the config saved
// in the bot's directory; a bot there already keeps its name and owners), or
// {"state":"error","error":…}. Iridium puts the TOKEN in place and starts the service after.
func bind(root string, args []string) int {
	fs := flag.NewFlagSet("bind", flag.ContinueOnError)
	irURL := fs.String("iridium", "", "Iridium's address")
	name := fs.String("name", "", "a new bot's terminal name in Iridium (default: its name on QQ)")
	taken := fs.String("taken", "", "terminal names in use in Iridium, comma separated")
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
			if !qqbridge.ValidAppID(bound.AppID) {
				say(map[string]any{"state": "error", "error": fmt.Sprintf("QQ gave an AppID of %q", bound.AppID)})
				return 1
			}
			home := qqbridge.BotHome(root, bound.AppID)
			c, had, _ := qqbridge.LoadConfig(home)
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
			qqName, err := check(c)
			if err != nil {
				say(map[string]any{"state": "error", "error": "QQ 不认这个 Bot：" + err.Error()})
				return 1
			}
			if !had {
				c.Name = *name
				if c.Name == "" {
					c.Name = qqbridge.FreeName(root, c.AppID, qqName, strings.Split(*taken, ",")...)
				}
			}
			if err := qqbridge.Write(home, qqbridge.ConfigFile, &c); err != nil {
				say(map[string]any{"state": "error", "error": err.Error()})
				return 1
			}
			res := map[string]any{"state": "bound", "appId": c.AppID, "name": c.Name, "owner": len(c.Owners) > 0}
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

func pair(root, id string) int {
	fmt.Printf("在 QQ 里给 Bot %s 发 %s（10 分钟内有效），发的人成为主人。\n", id, newPair(qqbridge.BotHome(root, id)))
	return 0
}

// ---- status ----

// State is iridium-qq status --json (and each of list --json), which Iridium's page reads too.
type State = api.ExtensionStatus

func state(root, id string) State {
	home := qqbridge.BotHome(root, id)
	c, ok, _ := qqbridge.LoadConfig(home)
	s := State{Version: version.Version, Home: home, Bound: ok, AppID: c.AppID, Name: c.Name, Owners: len(c.Owners), Iridium: c.Iridium, Token: qqbridge.Token(home) != ""}
	s.Service, s.Enabled = service(unitOf(id))
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

func list(root string, args []string) int {
	all := []State{}
	for _, id := range qqbridge.Bots(root) {
		all = append(all, state(root, id))
	}
	if len(args) > 0 && args[0] == "--json" {
		b, _ := json.Marshal(all)
		fmt.Println(string(b))
		return 0
	}
	if len(all) == 0 {
		fmt.Println("还没有 Bot：运行 iridium-qq setup。")
		return 1
	}
	for _, s := range all {
		c, _, _ := qqbridge.LoadConfig(s.Home)
		s.Name = liveName(s.Home, c)
		show(s)
	}
	return 0
}

func status(root, bot string, args []string) int {
	asJSON := len(args) > 0 && args[0] == "--json"
	if bot == "" && asJSON && len(qqbridge.Bots(root)) == 0 {
		b, _ := json.Marshal(State{Version: version.Version, Home: root})
		fmt.Println(string(b))
		return 0
	}
	if bot == "" && !asJSON && len(qqbridge.Bots(root)) != 1 {
		return list(root, nil)
	}
	id, err := pick(root, bot)
	if err != nil {
		return fail(err)
	}
	s := state(root, id)
	if asJSON {
		b, _ := json.Marshal(s)
		fmt.Println(string(b))
		return 0
	}
	c, _, _ := qqbridge.LoadConfig(s.Home)
	s.Name = liveName(s.Home, c)
	show(s)
	return 0
}

func show(s State) {
	fmt.Printf("Bot %s，终端名 %s，主人 %d 个，Iridium %s，TOKEN %s\n", s.AppID, s.Name, s.Owners, s.Iridium, map[bool]string{true: "有", false: "无"}[s.Token])
	switch {
	case s.Service == "":
		fmt.Printf("  服务：没装（iridium-qq start --bot %s）\n", s.AppID)
	case s.Running != nil:
		qqs, irs := "已连接", "已连接"
		if !s.Running.QQ {
			qqs = "未连接 " + s.Running.QQError
		}
		if !s.Running.Iridium {
			irs = "未连接 " + s.Running.IridiumError
		}
		fmt.Printf("  服务：%s；QQ %s；Iridium %s\n", s.Service, qqs, irs)
	default:
		fmt.Printf("  服务：%s\n", s.Service)
	}
	if s.Pair != "" {
		fmt.Printf("  配对码 %s 等着\n", s.Pair)
	}
}

// ---- the services ----

// unitPrefix names the services: the one bot of before had it alone, each bot now has
// <prefix>-<AppID> (systemd --user unit, or LaunchAgent label). IRIDIUM_QQ_UNIT for tests.
var unitPrefix = func() string {
	if u := os.Getenv("IRIDIUM_QQ_UNIT"); u != "" {
		return u
	}
	return "iridium-qq"
}()

func unitOf(id string) string { return unitPrefix + "-" + id }

func unitPath(u string) string {
	d, err := os.UserConfigDir()
	if err != nil {
		d = filepath.Join(os.Getenv("HOME"), ".config")
	}
	return filepath.Join(d, "systemd", "user", u+".service")
}

func unitExists(u string) bool {
	_, err := os.Stat(unitPath(u))
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

// service is u's state in systemd's words (active, inactive, failed…; "" not installed), and
// whether it starts at login.
func service(u string) (string, bool) {
	if onMac {
		return macService(u)
	}
	st := systemctlOut("show", "-p", "ActiveState", "--value", u+".service")
	if st == "inactive" && !unitExists(u) {
		st = ""
	}
	return st, systemctlOut("is-enabled", u+".service") == "enabled"
}

// startBot writes the bot's service and (re)starts it.
func startBot(root, id string) error {
	home := qqbridge.BotHome(root, id)
	c, ok, err := qqbridge.LoadConfig(home)
	if err != nil || !ok {
		return fmt.Errorf("没有 Bot %s：先运行 iridium-qq setup", id)
	}
	if qqbridge.Token(home) == "" {
		return fmt.Errorf("Bot %s 没有 Iridium 的 TOKEN：先运行 iridium-qq setup --bot %s", id, id)
	}
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		return err
	}
	u := unitOf(id)
	if onMac {
		return macStart(u, exe, id)
	}
	env := ""
	if h := os.Getenv("IRIDIUM_QQ_HOME"); h != "" {
		env = "Environment=IRIDIUM_QQ_HOME=" + h + "\n"
	}
	body := fmt.Sprintf(`[Unit]
Description=Iridium QQ plugin (%s, %s)
After=network-online.target

[Service]
ExecStart=%s run --bot %s
%sRestart=always
RestartSec=5

[Install]
WantedBy=default.target
`, c.Name, c.AppID, exe, id, env)
	if err := os.MkdirAll(filepath.Dir(unitPath(u)), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(unitPath(u), []byte(body), 0o644); err != nil {
		return err
	}
	for _, a := range [][]string{{"daemon-reload"}, {"enable", u + ".service"}, {"restart", u + ".service"}} {
		if err := systemctl(a...); err != nil {
			return fmt.Errorf("systemctl --user %s: %w", strings.Join(a, " "), err)
		}
	}
	return nil
}

func stopBot(id string) error {
	u := unitOf(id)
	if onMac {
		return macStop(u)
	}
	if !unitExists(u) {
		return nil
	}
	return systemctl("disable", "--now", u+".service")
}

// dropUnit stops service u and deletes it; whether it was running.
func dropUnit(u string) bool {
	if onMac {
		was, _ := macService(u)
		if _, err := os.Stat(macPlist(u)); err == nil {
			_ = macStop(u)
		}
		return was == "active"
	}
	if !unitExists(u) {
		return false
	}
	was := systemctlOut("is-active", u+".service") == "active"
	_ = systemctl("disable", "--now", u+".service")
	os.Remove(unitPath(u))
	_ = systemctl("daemon-reload")
	return was
}

func logs(id string, args []string) int {
	n := "100"
	if len(args) == 2 && args[0] == "-n" {
		n = args[1]
	}
	if onMac {
		return macLogs(unitOf(id), n)
	}
	cmd := exec.Command("journalctl", "--user", "-u", unitOf(id)+".service", "-n", n, "--no-pager", "-o", "short-iso")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return 1
	}
	return 0
}

// remove takes a bot away (--all: every bot): its service and directory; the last one gone, the
// plugin's whole directory.
func remove(root, bot string, args []string) int {
	ids := []string{}
	if len(args) > 0 && args[0] == "--all" {
		ids = qqbridge.Bots(root)
	} else {
		id, err := pick(root, bot)
		if err != nil {
			return fail(err)
		}
		ids = append(ids, id)
	}
	for _, id := range ids {
		home := qqbridge.BotHome(root, id)
		c, _, _ := qqbridge.LoadConfig(home)
		c.Name = liveName(home, c)
		dropUnit(unitOf(id))
		if err := os.RemoveAll(qqbridge.BotHome(root, id)); err != nil {
			return fail(err)
		}
		fmt.Printf("已删除 Bot %s 的服务和配置。Iridium 里的终端用 ir revoke %s 撤销。\n", id, c.Name)
	}
	if len(qqbridge.Bots(root)) == 0 {
		if err := os.RemoveAll(root); err != nil {
			return fail(err)
		}
	}
	return 0
}

// serve runs the bot in the foreground till a signal.
func serve(root, id string) int {
	home := qqbridge.BotHome(root, id)
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
		IR:     irOf(home, c),
		Logf:   log.Printf,
	}
	log.Printf("iridium-qq %s: bot %s (%s), Iridium %s", version.Version, c.AppID, c.Name, c.Iridium)
	_ = b.Run(ctx)
	return 0
}

func fail(err error) int {
	fmt.Fprintln(os.Stderr, "iridium-qq:", err)
	return 1
}
