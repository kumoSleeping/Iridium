package desktop

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/url"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/kumoSleeping/iridium/internal/join"
	"github.com/kumoSleeping/iridium/internal/macapp"
	"github.com/kumoSleeping/iridium/internal/remote/executor"
	"github.com/kumoSleeping/iridium/internal/update"
	"github.com/kumoSleeping/iridium/internal/version"
)

//go:embed assets
var assets embed.FS

// ui is the windowed app: one window on the Iridium shown, a tray icon with the joins' states and
// the app's own controls.
type ui struct {
	win  *mygo.Window
	tray *mygo.Tray
	// trayMenu: the tray's menu as drawn last; on a Mac and Windows it pops up from a click (trayClick).
	trayMenu *mygo.Menu
	joins    *Joins
	icon     []byte // the windows' own (Linux, Windows; a Mac's is its bundle's)

	here localState  // this computer's own Iridium (local.go)
	pub  publicState // its 公网访问 (public.go)

	upd *selfUpdate // the app's own update (selfupdate.go)

	mu        sync.Mutex
	waiting   bool                  // this computer's Iridium is waited for: shown once it runs
	main      *mainWin              // the main window (mainwin.go); nil: closed
	wins      []*mygo.Window        // the windows (the main one, Iridiums' own), the one in front (win) among them
	panes     []*pane               // the Iridiums' pages in them (pane.go)
	shows     map[string]string     // each pane's (by name) Iridium, as its page says
	names     map[*mygo.Page]string // each page's name (picker, ask, w1…): mygo's have none
	lamps     map[string]railLamps  // what each Iridium's page last said for the rail (rail.go)
	nwin      int
	toldReady bool // the Iridiums' pages told an update is ready (updateChanged)
	quitting  bool // the app quits: its windows of their own are opened again at the next start
}

func gui(o Options) int {
	logf := appLog()
	loginEnv(logf)
	// The app's own pages: web/src/appwin, built into assets/ui (npm run build:app).
	page, _ := fs.Sub(assets, "assets/ui")
	icon, _ := assets.ReadFile("assets/icon.png")
	if runtime.GOOS == "darwin" {
		// On a Mac icon's grid, room around the square (sign/icons.go): the Dock's is this one.
		icon = macapp.Icon
	}
	trayIcon, _ := assets.ReadFile("assets/tray.png")
	u := &ui{icon: icon}
	u.upd = newSelfUpdate(update.Options{}, version.Release(), u.updateChanged, u.restartApp, logf)
	u.joins = &Joins{Logf: logf, View: true, Record: &Record{Kind: "window"}}
	u.joins.Record.save(nil)
	// What the runtime is asked for more of this computer comes here to be answered (askfile.go).
	go answerAsks(context.Background(), func(ctx context.Context, server, by string, items []executor.Item) []bool {
		if len(items) == 1 && items[0].Kind == "text" {
			return []bool{u.ask(ctx, items[0].Value)}
		}
		return u.askEach(ctx, server, by, items)
	})
	go u.follow()
	go smallFaces()
	// The name keys the single-instance lock, as Wails's UniqueID did.
	mygo.App.SetName("Iridium")
	mygo.App.SetVersion(version.Short(version.Version))
	mygo.SetFrontend(page)
	// What a page sends (window._wails.invoke, kept by the preload: bridge.go) comes as one bound
	// method; mygo answers only the app's own pages and each window's own Iridium (TrustedOrigins).
	mygo.BindAs("IRApp", &bridge{u: u, logf: logf})
	// A second start (the app opened again) shows this one's window.
	if !mygo.App.RequestSingleInstanceLock() {
		return 0
	}
	mygo.App.OnSecondInstance(func([]string, string) { go u.show() })
	// The last window closed: the app stays, in the tray (a listener keeps it from quitting).
	mygo.App.OnWindowAllClosed(func() {})
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) {
		u.mu.Lock()
		u.quitting = true
		u.mu.Unlock()
	})
	mygo.App.OnQuitCanceled(func() {
		u.mu.Lock()
		u.quitting = false
		u.mu.Unlock()
	})
	mygo.App.OnQuit(func() { u.joins.Stop() }) // the window's record; the runtime runs on

	c, err := loadConfig()
	if err != nil {
		logf("config: %v", err)
	}
	// This computer's own Iridium: put in the list and shown at the app's first start (kumo,
	// 2026-11-22); on later starts the one shown last is, and the local one is kept at its address
	// (its public one turned on or off).
	// A first start runs nothing (kumo, 2026-11-22): the app's page offers this computer's own
	// Iridium and adding another; nothing runs till one is chosen.
	first := !configExists()
	if addr, err := localAddress(); err != nil {
		logf("local: %v", err)
	} else if c.local() != nil {
		if c2, err := updateConfig(func(c *Config) { c.setLocal(addr, false) }); err == nil {
			c = c2
		}
	}
	// --join: in the list, joined, under the terms given; the first shown on a first start.
	for i, a := range o.Join.Addrs {
		s := o.Join.server(a, u.joins.Agents())
		if c2, err := updateConfig(func(c *Config) {
			if c.server(a) == nil {
				c.Servers = append(c.Servers, Server{URL: a})
			}
			e := c.server(a)
			e.Off, e.NoShell, e.Roots, e.AgentsOff = false, s.NoShell, s.Roots, s.AgentsOff
			if o.Join.Name != "" {
				c.Name = o.Join.Name
			}
			if first && i == 0 && c.local() == nil {
				c.Current = a
			}
		}); err == nil {
			c = c2
		}
	}
	// Each start shows the picker alone (kumo, 2026-11-22). Only ordinary windows, each made as
	// it is first shown (kumo, 2026-11-22: Wails with what it supports and no more): none made
	// hidden ahead, none without a frame, none see-through. A window on an Iridium is made once
	// one is picked (newWindow).
	// The menu bar, with the zoom keys (⌘= or ⌘+ larger, ⌘- smaller, ⌘0 as it was; Ctrl on
	// Windows and Linux), kept for the next start (kumo, 2026-11-20). A Mac's is the app's (kumo,
	// 2026-11-22): ⌘, the control page, ⌘W closes the window (the app stays in the menu bar), ⌘Q
	// quits, ⌘H hides, ⌘M minimises, ⌃⌘F full screen; Edit's for ⌘C ⌘V ⌘A ⌘Z in the pages, which a
	// Mac's web view gets only through it. On Windows and Linux the menu is a window's, kept out of
	// sight (WindowOptions.AutoHideMenuBar): only its keys are wanted there.
	mygo.App.SetMenu(u.menuBar())
	// The Dock's icon pressed with no window shown: the window again.
	mygo.App.OnActivate(func(visible bool) {
		if !visible {
			go u.show()
		}
	})
	mygo.App.WhenReady(func() {
		go u.startWindows()
		// A screen taken away: the windows on it come back to the main one.
		mygo.Screen.OnDisplaysChanged(func() { go u.keepOnScreen() })
		icon, tmpl := trayIcon, false
		if runtime.GOOS == "darwin" {
			// The menu bar's own kind of icon: the mark's shape alone, the bar tinting it, at its
			// size (kumo, 2026-11-20: the coloured one stood out too large).
			icon, _ = assets.ReadFile("assets/tray-mac.png")
			tmpl = true
		}
		t, err := mygo.NewTray(mygo.TrayOptions{ID: "iridium", Icon: icon, IconIsTemplate: tmpl, ToolTip: "Iridium"})
		if err != nil {
			logf("tray: %v", err)
		}
		u.mu.Lock()
		u.tray = t
		u.mu.Unlock()
		if t != nil && runtime.GOOS != "linux" {
			// No window open: a click brings the picker, its 新建窗口 (kumo, 2026-11-23); else, and on
			// a right click, the menu. Linux's tray opens its menu on a click itself.
			t.OnClick(func() { u.trayClick(t, false) })
			t.OnRightClick(func() { u.trayClick(t, true) })
		}
		u.drawMenu(c)
		// 登录时打开 is gone (kumo, 2026-11-23: what runs at login is the runtime, a service of its
		// own): a login item left from before is taken away, once, there being no switch for it.
		go dropLoginItem()
	})
	u.joins.Sync(c)
	if l := c.local(); l != nil && c.serverOn() && !o.NoServer {
		go u.startLocal(l.URL, logf)
	}
	go u.syncRuntime(logf)
	if o.NoServer {
		_ = SetServer(false)
	}
	go u.upd.run()
	go u.probe()
	go u.syncFaces()
	if err := mygo.App.Run(); err != nil {
		logf("%v", err)
		return 1
	}
	return 0
}

// themed is address with the theme chosen on an Iridium's page (Config.Theme) for its first
// paint (theme.js ?theme=, which keeps it there too); none chosen: auto, the system's.
func themed(address string) string {
	c, _ := loadConfig()
	if c.Theme != "dark" && c.Theme != "light" {
		// None chosen yet: the system's, here and on every Iridium's page (kumo, 2026-11-22: a
		// page's own old choice on this computer gives way, so nothing flashes).
		c.Theme = "auto"
	}
	base, frag, _ := strings.Cut(address, "#")
	sep := "?"
	if strings.Contains(base, "?") {
		sep = "&"
	}
	out := base + sep + "theme=" + c.Theme
	if frag != "" {
		out += "#" + frag
	}
	return out
}

// loopback: is address on this computer's loopback (no public address: no login)?
func loopback(address string) bool {
	u, err := url.Parse(address)
	if err != nil {
		return false
	}
	h := u.Hostname()
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// startLocal has this computer's Iridium running (localState.ensure) and, the window waiting for
// it, shows it.
func (u *ui) startLocal(address string, logf func(string, ...any)) {
	u.redraw()
	err := u.here.ensure(context.Background(), address, logf)
	if errors.Is(err, errBusy) {
		// Already being started (the app's own start): that one, as it ends, shows it if waited
		// for. Taken as a failure, the window picked never came (2026-11-22).
		return
	}
	if err != nil {
		logf("local: %v", err)
	}
	u.redraw()
	u.mu.Lock()
	waiting := u.waiting
	if err == nil {
		u.waiting = false
	}
	u.mu.Unlock()
	c, _ := loadConfig()
	if err == nil && waiting && c.current() == address {
		u.switchTo(address)
		return
	}
	// The app's own page says how it went.
	st := u.pageState(c)
	if err != nil {
		st["error"], st["server"] = "未能启动："+err.Error(), address
	}
	reply(u.homePage(), st)
}

// useLocal puts this computer's Iridium in the list (asked from a page: kumo, 2026-11-22, the
// top right adds and switches) and shows it once it runs.
func (u *ui) useLocal(logf func(string, ...any)) error {
	addr, err := localAddress()
	if err != nil {
		return err
	}
	if _, err := updateConfig(func(c *Config) { c.setLocal(addr, true); c.NoServer = false }); err != nil {
		return err
	}
	u.switchTo(addr)
	return nil
}

// localStatus is this computer's Iridium's state, short.
func (u *ui) localStatus() string {
	st, _ := u.here.get()
	if st == "" {
		st = localStarting
	}
	return st
}

// show brings the app forward: the window in front; with none, the main window again, on the
// Iridium used last.
func (u *ui) show() {
	u.mu.Lock()
	w := u.win
	u.mu.Unlock()
	if w == nil {
		u.startWindows()
		return
	}
	w.Show()
	w.Focus()
}

// startWindows opens the main window as the app starts (or comes back with none open): on the
// Iridium used last, else the first in the rail; with none in the list, the app's first page
// (who one is); and the Iridiums' windows of their own left open last time.
func (u *ui) startWindows() {
	c, _ := loadConfig()
	if len(c.Servers) == 0 {
		u.showHome("who")
		return
	}
	u.mu.Lock()
	first := u.main == nil && len(u.wins) == 0
	u.mu.Unlock()
	if first {
		u.openAway(c)
	}
	// In the main window: not one in a window of its own.
	at := c.current()
	if s := c.server(at); s == nil || c.HideLocal && s.Local || c.away(at) != nil {
		at = ""
		for _, a := range u.railOrder(c) {
			if c.away(a) == nil {
				at = a
				break
			}
		}
	}
	if at == "" {
		u.showHome("add")
	} else {
		u.switchTo(at)
	}
}

// message is what a page sends (window._wails.invoke). The address page (the app's own) adds an
// Iridium; an Iridium's own page (its origin one in the list) may ask what the app is, switch to
// another in the list, add one (with a dialog of this computer's: joining is this computer's
// to allow), and log in from the browser. Anything else, from any other origin, is dropped: a
// page is never trusted for what only a person here allows.
func (u *ui) message(p *mygo.Page, msg, origin string, logf func(string, ...any)) {
	c, _ := loadConfig()
	server := serverOf(c, origin)
	verb, arg, _ := strings.Cut(strings.TrimPrefix(msg, "ir:"), ":")
	if !strings.HasPrefix(msg, "ir:") {
		return
	}
	if verb == "jserror" {
		// What went wrong in a page the window shows (pageJS), for the log: nothing is done on it.
		if len(arg) > 2000 {
			arg = arg[:2000]
		}
		logf("page %q: %s", origin, arg)
		return
	}
	logf("message %s from %q", verb, origin)
	if name := u.nameOf(p); server != "" && name != "control" && name != "ask" && name != homeName {
		u.mu.Lock()
		{
			if u.shows == nil {
				u.shows = map[string]string{}
			}
			u.shows[name] = server
		}
		u.mu.Unlock()
	}
	if verb == "rail" {
		// Ctrl+Tab, Ctrl+Shift+Tab in a page of the main window (railKeysJS).
		u.mu.Lock()
		inMain := u.main != nil && p.Window() == u.main.win
		u.mu.Unlock()
		if inMain && (ownPage(origin) || server != "") {
			go u.railStep(arg == "prev")
		}
		return
	}
	if server == "" {
		// Not an Iridium's page: the app's own login page (assets), or nothing.
		if ownPage(origin) {
			u.local(p, verb, arg)
		}
		return
	}
	switch verb {
	case "win":
		u.winKey(p, arg)
	case "title":
		// The name this window's Iridium goes by here, the app's alone (kumo, 2026-11-22): the
		// windows' titles (the task bar's), the picker's row. "": Iridium again.
		u.retitle(server, arg)
		c, _ := loadConfig()
		reply(p, u.hello(c, server))
	case "favs":
		// Its favorites (favs.go), for every page's list.
		u.favsSaid(server, arg)
	case "favorder":
		u.favsOrder(arg)
	case "fav":
		u.favAsk(p, server, arg)
	case "assemble":
		go u.assembledSaid(server, arg)
	case "sidebar":
		if w, err := strconv.Atoi(arg); err == nil {
			u.sideSaid(server, w)
		}
	case "status":
		// What the rail shows of it (web/src/railstatus.ts): running, asks, unread.
		var l railLamps
		if json.Unmarshal([]byte(arg), &l) == nil {
			u.mu.Lock()
			if u.lamps == nil {
				u.lamps = map[string]railLamps{}
			}
			changed := u.lamps[server] != l
			u.lamps[server] = l
			u.mu.Unlock()
			if changed {
				u.railRefresh()
			}
		}
	case "theme":
		// The day or night chosen on an Iridium's page (auto: the system's), the app's own pages
		// and every Iridium's after it the same (kumo, 2026-11-22).
		if arg == "dark" || arg == "light" || arg == "auto" {
			_, _ = updateConfig(func(c *Config) { c.Theme = arg })
			u.railRefresh()
		}
	case "me":
		// Who the window is there, for the app's own page.
		var me struct{ Name, Avatar string }
		if json.Unmarshal([]byte(arg), &me) == nil && len(me.Avatar) < 512<<10 && (me.Avatar == "" || strings.HasPrefix(me.Avatar, "data:image/")) {
			_, _ = updateConfig(func(c *Config) {
				if s := c.server(server); s != nil {
					s.LoggedIn, s.Name, s.Avatar = true, me.Name, smallFace(me.Avatar)
				}
			})
		}
	case "home":
		// The app's key of a page that does not know of the rail (one from before): 本机与授权.
		go u.showHome("local")
	case "needlogin":
		// The Iridium's login page, in the app: the app's own instead (its passkey is in the browser).
		// Its window is given up (a window shows an Iridium's page only); the picker logs in, at
		// once: the window gone and nothing more looked like a crash, the login only at the next
		// press (QGI, 2026-11-23, an Iridium first opened in a new app).
		if _, err := updateConfig(func(c *Config) { c.Current = server; c.server(server).LoggedIn = false }); err == nil {
			go func() {
				u.gone(server)
				u.showHome("add")
				if hp := u.homePage(); hp != nil {
					u.login(hp, server)
				}
			}()
		}
	case "hello":
		reply(p, u.hello(c, server))
		reply(p, u.favsPayload(c, server))
		if a := u.partOf(server, p); a["assemble"].(map[string]any)["part"] != "" {
			reply(p, a)
		}
	case "switch":
		for _, s := range c.Servers {
			if s.URL == arg {
				u.switchTo(arg)
			}
		}
	case "add":
		go u.add(p, arg, true)
	case "local":
		// This computer's own Iridium, in the list and shown.
		if err := u.useLocal(logf); err != nil {
			h := u.hello(c, server)
			h["error"] = err.Error()
			reply(p, h)
		}
	case "remove":
		if arg == server {
			return // not the one the page is on: from another's
		}
		stopLogin(arg)
		if c, err := updateConfig(func(c *Config) { c.remove(arg) }); err == nil {
			u.joins.Sync(c)
			u.gone(arg)
			u.redraw()
			reply(p, u.hello(c, server))
		}
	case "set":
		go u.set(p, server, arg)
	case "pickroot":
		to := server
		if arg != "" && c.server(arg) != nil {
			to = arg
		}
		go u.pickRoot(p, to, server)
	case "update":
		if arg == "restart" {
			u.upd.restart()
			return
		}
		go func() {
			u.upd.check(true)
			c, _ := loadConfig()
			h := u.hello(c, server)
			if h["update"] == "" && h["updatePct"] == nil {
				h["notice"] = "已是最新"
			}
			reply(p, h)
		}()
	case "public":
		// 公网访问 on or off: the steps go to the page as they come.
		go u.pub.set(arg == "on", func() {
			c, _ := loadConfig()
			reply(p, u.hello(c, server))
		}, logf)
	case "server":
		// This computer's own Iridium on or off (ir server): the runtime follows the settings.
		if err := SetServer(arg == "on"); err == nil && arg == "on" {
			if c, _ := loadConfig(); c.local() != nil {
				go u.startLocal(c.local().URL, logf)
			}
		}
		c, _ := loadConfig()
		reply(p, u.hello(c, server))
	}
}

// local answers the app's own page (web/src/appwin): the Iridiums in the list, with who the
// window is at those logged in to; opening one, logging in to one from the browser (an address
// typed is checked to be an Iridium and put in the list first), removing one.
func (u *ui) local(p *mygo.Page, verb, arg string) {
	state := u.pageState
	switch verb {
	case "askstate":
		st := askState()
		c, _ := loadConfig()
		st["theme"] = c.Theme
		reply(p, st)
	case "answer":
		answerAsk(arg)
	case "hidelocal":
		// 此电脑 out of the list (its row's 隐藏) or back (添加服务器's key; kumo, 2026-11-23), as
		// the tray's 此电脑 › 隐藏. Nothing of it starts or stops.
		u.hideLocal(arg == "on")
		c, _ := loadConfig()
		reply(p, state(c))
	case "title":
		// An Iridium's name in this app, from the picker (any Iridium, of any version).
		var t struct{ URL, Title string }
		if json.Unmarshal([]byte(arg), &t) == nil && t.URL != "" {
			u.retitle(t.URL, t.Title)
		}
		c, _ := loadConfig()
		reply(p, state(c))
	case "win":
		u.winKey(p, arg)
	case "clipboard":
		// Its text field's ⌘V (web/src/appwin/bridge.ts): this page only, the app's own.
		text := mygo.Clipboard.ReadText()
		reply(p, map[string]string{"clipboard": text})
	case "copy":
		mygo.Clipboard.WriteText(arg)
	case "hello":
		c, _ := loadConfig()
		reply(p, state(c))
		// The list opened: a newer release looked for (not again for a while), told as it comes.
		go u.upd.check(false)
		// The Iridiums' faces, fetched afresh; the list and rail drawn again if one changed.
		go func() {
			if c, changed := refreshFaces(c); changed {
				reply(p, state(c))
				u.railRefresh()
				u.favsTell()
			}
		}()
	case "update":
		if arg == "restart" {
			u.upd.restart()
		} else {
			go u.upd.check(true)
		}
	case "open":
		// An Iridium's row pressed: it is shown.
		go u.switchTo(arg)
	case "local":
		if err := u.useLocal(appLog()); err != nil {
			reply(p, map[string]string{"error": err.Error()})
			return
		}
		c, _ := loadConfig()
		reply(p, state(c))
	case "remove":
		stopLogin(arg)
		if c, err := updateConfig(func(c *Config) { c.remove(arg) }); err == nil {
			u.joins.Sync(c)
			u.gone(arg)
			u.redraw()
			reply(p, state(c))
		}
	case "terms":
		// This computer for one Iridium, from the app's own page: the person's own hand, so
		// widening needs no dialog here (kumo, 2026-11-22); the page shows it again.
		var t terms
		if json.Unmarshal([]byte(arg), &t) != nil || t.URL == "" {
			return
		}
		_, _ = updateConfig(func(c *Config) {
			s := c.server(t.URL)
			if s == nil || s.Local {
				return
			}
			if t.Joined != nil {
				s.Off = !*t.Joined
			}
			if t.Shell != nil {
				s.NoShell = !*t.Shell
			}
			if t.Roots != nil {
				s.Roots = t.Roots
			}
			if t.All {
				s.Roots = nil
			}
			for a, on := range t.Agents {
				s.AgentsOff = slices.DeleteFunc(s.AgentsOff, func(x string) bool { return x == a })
				if !on {
					s.AgentsOff = append(s.AgentsOff, a)
				}
			}
		})
		c, _ := loadConfig()
		reply(p, state(c))
	case "pickroot":
		go func() {
			dir, err := pickFolder("允许 " + hostOf(arg) + " 访问的文件夹")
			if err == nil && dir != "" {
				_, _ = updateConfig(func(c *Config) {
					if s := c.server(arg); s != nil && !slices.Contains(s.Roots, dir) {
						s.Roots = append(s.Roots, dir)
					}
				})
			}
			c, _ := loadConfig()
			reply(p, state(c))
		}()
	case "server":
		// 此电脑's switch: this computer's Iridium started or stopped, and no more (kumo,
		// 2026-11-22: it took the window away); its row pressed shows it.
		if err := SetServer(arg == "on"); err != nil {
			reply(p, map[string]string{"error": err.Error()})
		} else if arg == "on" {
			if c, _ := loadConfig(); c.local() != nil {
				if st, _ := u.here.get(); st != localRunning {
					u.here.set(localStarting, nil)
				}
				go u.startLocal(c.local().URL, appLog())
			}
		}
		c, _ := loadConfig()
		reply(p, state(c))
		u.redraw()
	case "public":
		go u.pub.set(arg == "on", func() {
			c, _ := loadConfig()
			reply(p, state(c))
		}, appLog())
	case "enter":
		// The first page's 进入: who this is, then this computer's own Iridium, started and shown.
		var me Me
		if json.Unmarshal([]byte(arg), &me) == nil && len(me.Avatar) < 1<<20 && (me.Avatar == "" || strings.HasPrefix(me.Avatar, "data:image/")) {
			me.Name = strings.TrimSpace(me.Name)
			me.Avatar = smallFace(me.Avatar)
			_, _ = updateConfig(func(c *Config) { c.Me = me })
		}
		if err := u.useLocal(appLog()); err != nil {
			reply(p, map[string]string{"error": err.Error()})
			return
		}
		c, _ := loadConfig()
		reply(p, state(c))
	case "me":
		// The first page's name and face, kept as typed and chosen.
		var me Me
		if json.Unmarshal([]byte(arg), &me) == nil && len(me.Avatar) < 1<<20 && (me.Avatar == "" || strings.HasPrefix(me.Avatar, "data:image/")) {
			me.Avatar = smallFace(me.Avatar)
			_, _ = updateConfig(func(c *Config) { c.Me = me })
		}
	case "login":
		go u.login(p, arg)
	}
}

// login logs the window in to arg (an address, or an invite link) from the system's browser: arg
// put in the list first if it is not there, w (the picker) told the code to check and, once the
// person allows it, that it is done; the Iridium is then opened.
func (u *ui) login(p *mygo.Page, arg string) {
	state := u.pageState
	// An invite link (…/_ir/join…): its Iridium's address, the invite registered in the
	// browser on the way to the app's login (kumo, 2026-11-22: pasted, handled).
	invite := ""
	if o, ok := inviteOrigin(arg); ok {
		invite, arg = strings.TrimSpace(arg), o
	}
	a, err := normalize(arg)
	if err != nil {
		reply(p, map[string]string{"error": "地址无效"})
		return
	}
	c, _ := loadConfig()
	// One in the list and logged in, typed or pasted again: opened, its login kept. Logged in
	// anew it asked for the browser's allow again (StarFreedomX, 2026-11-23).
	if s := c.server(a); s != nil && invite == "" && (s.LoggedIn || s.Local && loopback(s.URL)) {
		st := state(c)
		st["done"] = a
		reply(w, st)
		u.switchTo(a)
		return
	}
	if c.server(a) == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		err := isIridium(ctx, a)
		cancel()
		if err != nil {
			reply(p, map[string]string{"error": err.Error()})
			return
		}
		// Typed here by the person: in the list, shown; joined only by its switch.
		if c, err = updateConfig(func(c *Config) { c.add(a) }); err != nil {
			reply(p, map[string]string{"error": err.Error()})
			return
		}
		u.joins.Sync(c)
		u.redraw()
		st := state(c)
		st["added"] = true
		reply(p, st)
	}
	name := c.Name
	if name == "" {
		name = join.MachineName()
	}
	open := mygo.Shell.OpenExternal
	if invite != "" {
		me := c.Me.Name
		if me == "" {
			cu, _ := user.Current()
			me = ownName(cu)
		}
		open = func(appURL string) error { return mygo.Shell.OpenExternal(inviteFor(invite, appURL, me)) }
	}
	browserLogin(a, name, open, func(m map[string]string) {
		m["server"] = a
		reply(p, m)
	}, func(link, who string) {
		if c, _ := loadConfig(); c.server(a) == nil {
			reply(w, state(c)) // removed as it was allowed: not opened
			return
		}
		_, _ = updateConfig(func(c *Config) {
			if s := c.server(a); s != nil {
				s.LoggedIn = true
				if who != "" {
					s.Name = who
				}
			}
			c.Current = a
		})
		// The page's wait is over: its spinner and its code go (kumo, 2026-11-22: it spun
		// on after the login, and the next 添加服务器 began spinning at once).
		c, _ := loadConfig()
		st := state(c)
		st["done"] = a
		reply(p, st)
		u.openIn(a, link)
	})
}

// pageState is what the app's own page shows: the Iridiums in the list, with who the window is at
// each, this computer's own with its state.
func (u *ui) pageState(c Config) map[string]any {
	list := []map[string]any{}
	rec, _ := RuntimeRecord()
	agents := []string{}
	for _, a := range u.joins.Agents() {
		agents = append(agents, a.Agent)
	}
	for _, s := range c.Servers {
		roots := s.Roots
		if roots == nil {
			roots = []string{}
		}
		e := map[string]any{"url": s.URL, "host": hostOf(s.URL), "loggedIn": s.LoggedIn, "name": s.Name, "title": s.Title, "avatar": s.Avatar, "face": s.Face,
			"joined": !s.Off && !s.Local, "shell": !s.NoShell, "roots": roots, "agentsOff": s.AgentsOff, "said": rec.Joins[s.URL]}
		if l, ok := rec.Links[s.URL]; ok {
			e["link"] = l // none: not joined yet, or no runtime; the row says 连接中
		}
		if s.Local {
			e["local"], e["status"] = true, u.localStatus()
			if loopback(s.URL) {
				e["loggedIn"] = true // no login on loopback
			}
		}
		list = append(list, e)
	}
	cu, _ := user.Current()
	return u.upd.view().fields(map[string]any{"os": runtime.GOOS, "servers": list, "current": c.current(), "theme": c.Theme, "hasLocal": c.local() != nil,
		"me": c.Me, "sysName": ownName(cu), "agents": agents, "server": c.serverOn(), "public": publicOn(), "publicState": u.pub.view(),
		"publicAddress": publicAddress(), "hideLocal": c.HideLocal})
}

// hello is what an Iridium's page learns of the app: that it is in it, and the Iridiums in its
// list (the window's switcher, which the page draws in its top bar).
func (u *ui) hello(c Config, server string) map[string]any {
	name := c.Name
	if name == "" {
		name = join.MachineName()
	}
	// Each Iridium in the list, with this computer as joined to it: its state, what it may do here.
	list := []map[string]any{}
	for _, s := range c.Servers {
		agents := []map[string]any{}
		for _, a := range u.joins.Agents() {
			agents = append(agents, map[string]any{"name": a.Agent, "on": !slices.Contains(s.AgentsOff, a.Agent)})
		}
		status := "未接入"
		if s.Local {
			status = u.localStatus()
		} else if !s.Off {
			status = brief(u.joins.Status(s.URL))
		}
		roots := s.Roots
		if roots == nil {
			roots = []string{}
		}
		list = append(list, map[string]any{"url": s.URL, "host": hostOf(s.URL), "current": s.URL == server, "local": s.Local,
			"joined": !s.Off && !s.Local, "status": status, "shell": !s.NoShell, "roots": roots, "agents": agents})
	}
	title := ""
	if s := c.server(server); s != nil {
		title = s.Title
	}
	return u.upd.view().fields(map[string]any{"app": "ir-app", "version": version.Short(version.Version), "name": name, "servers": list, "hasLocal": c.local() != nil, "server": !c.NoServer,
		"public": publicOn(), "publicState": u.pub.view(), "title": title, "os": runtime.GOOS,
		// The app's own key is the rail's (mainwin.go): the page draws none (web/src/appkey.tsx).
		"rail": true})
}

// terms are what a page may ask to change of this computer for its Iridium: narrowing is done at
// once; widening (joining it, command lines on, a folder more) only once the person here allows it
// in a dialog of this computer's.
type terms struct {
	URL    string `json:"url"` // the Iridium they are for; "": the page's own
	Joined *bool  `json:"joined"`
	// Agents: each named turned on or off.
	Agents map[string]bool `json:"agents"`
	Shell  *bool           `json:"shell"`
	Roots  []string        `json:"roots"` // the folders kept: only fewer (a folder more is pickroot's)
	All    bool            `json:"all"`   // every folder again
}

func (u *ui) set(p *mygo.Page, server, arg string) {
	var t terms
	if json.Unmarshal([]byte(arg), &t) != nil {
		return
	}
	page := server
	if t.URL != "" {
		server = t.URL
	}
	c, _ := loadConfig()
	s := c.server(server)
	if s == nil {
		return
	}
	var wider []string
	for a, on := range t.Agents {
		if on && slices.Contains(s.AgentsOff, a) {
			wider = append(wider, "允许 "+hostOf(server)+" 在这台电脑上启动 "+a)
		}
	}
	if t.Joined != nil && *t.Joined && s.Off {
		wider = append(wider, "让这台电脑接入 "+hostOf(server))
	}
	if t.Shell != nil && *t.Shell && s.NoShell {
		wider = append(wider, "允许 "+hostOf(server)+" 在这台电脑上执行命令")
	}
	if t.All && len(s.Roots) > 0 {
		wider = append(wider, "允许 "+hostOf(server)+" 访问这台电脑的全部文件夹")
	}
	if t.Roots != nil {
		// Only folders it had: a page cannot name a new one.
		for _, r := range t.Roots {
			if !slices.Contains(s.Roots, r) {
				return
			}
		}
		if len(s.Roots) > 0 && len(t.Roots) == 0 {
			return // none left would be all: that is no narrowing
		}
	}
	if len(wider) > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		ok := u.confirm(ctx, strings.Join(wider, "；")+"？")
		cancel()
		if !ok {
			reply(p, u.hello(c, page))
			return
		}
	}
	c, err := updateConfig(func(c *Config) {
		s := c.server(server)
		if s == nil {
			return
		}
		if t.Joined != nil {
			s.Off = !*t.Joined
		}
		if t.Shell != nil {
			s.NoShell = !*t.Shell
		}
		if t.Roots != nil {
			s.Roots = t.Roots
		}
		if t.All {
			s.Roots = nil
		}
		for a, on := range t.Agents {
			s.AgentsOff = slices.DeleteFunc(s.AgentsOff, func(x string) bool { return x == a })
			if !on {
				s.AgentsOff = append(s.AgentsOff, a)
			}
		}
	})
	if err != nil {
		return
	}
	u.joins.Sync(c)
	u.redraw()
	reply(p, u.hello(c, page))
}

// pickRoot: a folder for server, chosen by the person here in this computer's own picker (the
// choosing is the allowing): one more, or, with all of them open, the only one from now on.
func (u *ui) pickRoot(p *mygo.Page, server, page string) {
	dir, err := pickFolder("允许 " + hostOf(server) + " 访问的文件夹")
	if err != nil || dir == "" {
		return
	}
	c, err := updateConfig(func(c *Config) {
		if s := c.server(server); s != nil && !slices.Contains(s.Roots, dir) {
			s.Roots = append(s.Roots, dir)
		}
	})
	if err != nil {
		return
	}
	u.joins.Sync(c)
	reply(p, u.hello(c, page))
}

// add checks that address is an Iridium, then (asked by a page, with this computer's dialog)
// puts it in the list, joins it and shows it.
func (u *ui) add(p *mygo.Page, address string, ask bool) {
	a, err := normalize(address)
	if err != nil {
		reply(p, map[string]string{"error": "地址无效"})
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := isIridium(ctx, a); err != nil {
		reply(p, map[string]string{"error": err.Error()})
		return
	}
	if ask && !u.confirm(ctx, fmt.Sprintf("添加 %s？", hostOf(a))) {
		reply(p, map[string]string{"error": "已取消"})
		return
	}
	c, err := updateConfig(func(c *Config) { c.add(a) })
	if err != nil {
		reply(p, map[string]string{"error": err.Error()})
		return
	}
	u.joins.Sync(c)
	u.redraw()
	u.switchTo(c.current())
}

func (u *ui) switchTo(address string) {
	c, _ := loadConfig()
	s := c.server(address)
	if s == nil {
		return
	}
	if s.Local {
		if st, _ := u.here.get(); st != localRunning {
			// Not running yet: 本机与授权 says so, and it is shown once it runs.
			_, _ = updateConfig(func(c *Config) { c.Current = address; c.NoServer = false })
			u.mu.Lock()
			u.waiting = true
			u.mu.Unlock()
			u.here.set(localStarting, nil)
			u.showHome("local")
			go u.startLocal(address, appLog())
			return
		}
	}
	if !s.LoggedIn && !(s.Local && loopback(s.URL)) {
		u.showHome("add") // logged in to there
		return
	}
	u.openIn(address, "")
}

// serverOf is the Iridium in the list whose origin (scheme://host[:port]) origin is; "": none.
func serverOf(c Config, origin string) string {
	if origin == "" {
		return ""
	}
	o, err := url.Parse(origin)
	if err != nil {
		return ""
	}
	for _, s := range c.Servers {
		if su, err := url.Parse(s.URL); err == nil && su.Scheme == o.Scheme && strings.EqualFold(su.Host, o.Host) {
			return s.URL
		}
	}
	return ""
}

func isServer(c Config, origin string) bool { return serverOf(c, origin) != "" }

// ask asks the person here, in a dialog of this computer's, never on a page: an Iridium asking
// for more than it was given (docs/exec.md "放开").
func (u *ui) ask(ctx context.Context, q string) bool { return u.confirm(ctx, q+"。允许？") }

// confirm is a question to the person here, in a dialog of this computer's: allowed or not.
func (u *ui) confirm(ctx context.Context, q string) bool {
	answer := make(chan bool, 1)
	go func() {
		r, err := mygo.Dialog.Message(mygo.MessageOptions{Type: mygo.MessageQuestion, Title: "Iridium", Message: q,
			Buttons: []string{"允许", "拒绝"}, DefaultButton: 0, CancelButton: 1})
		answer <- err == nil && r.Button == 0
	}()
	select {
	case a := <-answer:
		return a
	case <-ctx.Done():
		return false
	}
}

// redraw builds the tray's menu again: each Iridium with its state, which is shown, the app's own.
//
// What it reads (the settings, the runtime's record) is read off the main thread, and only the menu
// is built on it: read there, every window of the app stood still for it, some 150 ms each time
// (kumo, 2026-11-22: 两边都同时卡一下).
func (u *ui) redraw() {
	go func() {
		t0 := time.Now()
		c, _ := loadConfig()
		st := u.pageState(c)
		reply(u.homePage(), st)
		u.railRefresh()
		t1 := time.Now()
		u.drawMenu(c)
		if d := time.Since(t1); d > 30*time.Millisecond {
			appLog()("redraw: the menu took %v (state %v)", d, t1.Sub(t0))
		}
	}()
}

// drawMenu builds the tray's menu and gives it to the tray: mygo's menus are made whole, not
// cleared and added to.
func (u *ui) drawMenu(c Config) {
	u.mu.Lock()
	t := u.tray
	u.mu.Unlock()
	if t == nil {
		return // the app not ready yet: WhenReady draws it
	}
	type item = mygo.MenuItem
	click := func(f func()) func(*mygo.MenuItem, *mygo.Window) {
		return func(*mygo.MenuItem, *mygo.Window) { f() }
	}
	check := func(label string, on bool, f func()) *item {
		return &item{Label: label, Type: mygo.MenuItemCheckbox, Checked: on, Click: click(f)}
	}
	cur := c.current()
	// The same two kinds as the app's pages (kumo, 2026-11-22): 服务器 (此电脑 and each Iridium:
	// pressed, its window; the picker to add one), then 授权操作 (each Iridium logged in to, ticked
	// when it may work on this computer).
	// 隐藏: 此电脑 out of the picker's list, the tray keeping it (kumo, 2026-11-23).
	hide := check("隐藏", c.HideLocal, func() { go u.hideLocal(!c.HideLocal) })
	m := []*item{{Label: "服务器", Disabled: true}}
	if l := c.local(); l != nil {
		state := u.localStatus()
		if c.NoServer {
			state = "已停止"
		}
		show := check("在窗口中显示", l.URL == cur, func() { u.switchTo(l.URL) })
		show.Disabled = c.NoServer
		m = append(m, &item{Label: "此电脑  " + state, Submenu: []*item{show,
			check("运行", !c.NoServer, func() {
				on := c.NoServer
				if SetServer(on) == nil && on {
					go u.startLocal(l.URL, appLog())
				}
				u.redraw()
			}), hide}})
	} else {
		m = append(m, &item{Label: "此电脑  已停止", Submenu: []*item{check("运行", false, func() {
			if SetServer(true) == nil {
				if c, _ := loadConfig(); c.local() != nil {
					go u.startLocal(c.local().URL, appLog())
				}
			}
			u.redraw()
		}), hide}})
	}
	for _, s := range c.Servers {
		if s.Local {
			continue
		}
		name := s.label()
		if !s.LoggedIn {
			name += "  未登录"
		}
		m = append(m, check(name, s.URL == cur, func() { u.switchTo(s.URL) }))
	}
	// One key for a window on another Iridium, or one added (kumo, 2026-11-23: 添加服务器… and
	// 打开窗口 were one thing): the picker.
	m = append(m, &item{Label: "添加服务器…", Click: click(func() { go u.showHome("add") })}, mygo.Separator(), &item{Label: "授权操作", Disabled: true})
	for _, s := range c.Servers {
		if s.Local || !s.LoggedIn {
			continue
		}
		state := "未授权"
		if !s.Off {
			state = brief(u.joins.Status(s.URL))
		}
		m = append(m, check(fmt.Sprintf("%s  %s", s.label(), state), !s.Off, func() {
			_, _ = updateConfig(func(c *Config) { c.setOff(s.URL, !s.Off) })
			u.redraw()
		}))
	}
	switch v := u.upd.view(); v.Phase {
	case "ready":
		m = append(m, &item{Label: "重启以更新到 " + version.Short(v.Version), Click: click(u.upd.restart)})
	case "downloading":
		m = append(m, &item{Label: fmt.Sprintf("下载中 %d%%", v.Pct), Disabled: true})
	case "failed":
		m = append(m, &item{Label: "更新失败", Click: click(func() { go u.upd.check(true) })})
	}
	m = append(m, mygo.Separator(), &item{Label: "Iridium " + version.Short(version.Version), Disabled: true},
		&item{Label: "退出", Click: click(mygo.App.Quit)})
	menu := mygo.NewMenu(m)
	if runtime.GOOS == "linux" {
		t.SetMenu(menu)
		return
	}
	u.mu.Lock()
	u.trayMenu = menu
	u.mu.Unlock()
}

func hostOf(address string) string {
	if p, err := url.Parse(address); err == nil && p.Host != "" {
		return p.Host
	}
	return address
}

// appLog is the app's log: a file beside its config (no terminal to print to).
func appLog() func(string, ...any) {
	p, err := configPath()
	if err == nil {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err == nil {
			if f, err := os.OpenFile(filepath.Join(filepath.Dir(p), "app.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
				return log.New(f, "", log.LstdFlags).Printf
			}
		}
	}
	return log.Printf
}

// brief is a join's last word as the tray's menu says it: a state, short; the whole line is in
// the log.
func brief(line string) string {
	switch {
	case line == "":
		return "连接中"
	case strings.HasPrefix(line, "已连接"), strings.HasPrefix(line, "可访问"):
		return "已连接"
	case strings.HasPrefix(line, "配对码"):
		code, _, _ := strings.Cut(strings.TrimPrefix(line, "配对码 "), "：")
		return "配对码 " + code
	case strings.Contains(line, "runs here"):
		return "已由其他程序接入"
	case strings.HasPrefix(line, "连接中"), strings.HasPrefix(line, "已断开"):
		return line
	case strings.Contains(line, "没开配对"):
		return "未开配对"
	}
	if r := []rune(line); len(r) > 16 {
		return string(r[:16]) + "…"
	}
	return line
}

// pageCSS is put in every page the window shows: the page itself never scrolls or bounces (only
// what is in it: kumo, 2026-11-20, the whole page went with the wheel). No title bar above it on
// any system (kumo, 2026-11-22): the window's keys are the page's own (chrome.go).
const pageCSS = "html,body{overflow:hidden!important;overscroll-behavior:none!important}"

// pageJS is put in every page the window shows: its errors, and a page still empty some seconds
// after it loaded, go to the app's log (ir:jserror), there being no console to read in the app;
// and the window's frame (chromeJS).
var pageJS = chromeJS + `(function(){
var q=[];function send(m){m=String(m).slice(0,2000);var w=window._wails;if(w&&w.invoke){try{w.invoke("ir:jserror:"+m)}catch(e){}}else{q.push(m)}}
setInterval(function(){var w=window._wails;while(q.length&&w&&w.invoke){w.invoke("ir:jserror:"+q.shift())}},1000);
addEventListener("error",function(e){var t=e.target;if(t&&t!==window&&t.tagName){var u=String(t.currentSrc||t.src||t.href||"");if(/\/avatar\?|cloudflareinsights/.test(u))return;send("load failed: "+t.tagName.toLowerCase()+" "+u.slice(0,200));return}
var m=e.message||"";if(m==="Script error."||/^ResizeObserver loop/.test(m))return;send((e.message||"error")+" @ "+(e.filename||"")+":"+(e.lineno||"")+(e.error&&e.error.stack?"\n"+e.error.stack:""))},true);
addEventListener("unhandledrejection",function(e){var r=e.reason;send("unhandled: "+(r&&(r.stack||r.message)||r))});
var ce=console.error;console.error=function(){try{send("console.error: "+Array.prototype.map.call(arguments,String).join(" "))}catch(e){}return ce.apply(console,arguments)};
addEventListener("load",function(){setTimeout(function(){var r=document.getElementById("root");if(r&&!r.children.length)send("empty page: "+location.href)},6000)});
})();`

// probe does what IRIDIUM_APP_PROBE lists, 3 seconds apart, for looking at the app's windows from
// outside (a screenshot): picker, open (the Iridium used last).
func (u *ui) probe() {
	steps := os.Getenv("IRIDIUM_APP_PROBE")
	if steps == "" {
		return
	}
	for _, step := range strings.Split(steps, ",") {
		time.Sleep(3 * time.Second)
		switch step {
		case "picker", "add":
			u.showHome("add")
		case "local":
			u.showHome("local")
		case "open":
			c, _ := loadConfig()
			u.switchTo(c.current())
		}
	}
}

// trayClick is a click on the tray's icon (Mac, Windows), on the main thread: with no window of the
// app open and a plain click, the picker; else the menu, popped up and taken back, so that the next
// click comes here again.
func (u *ui) trayClick(t *mygo.Tray, right bool) {
	u.mu.Lock()
	m, open := u.trayMenu, len(u.wins) > 0
	u.mu.Unlock()
	if !right && !open {
		go u.show()
		return
	}
	if m == nil {
		return
	}
	t.SetMenu(m)
	t.PopUpMenu()
	t.SetMenu(nil)
}
