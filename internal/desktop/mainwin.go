package desktop

import (
	"fmt"
	"runtime"
	"slices"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
)

// The main window (otae-c, 霓虹, 2026-11-26; docs/app.md "主窗口"): a rail of the Iridiums at its
// left, as Discord's servers (rail.go), the page of the one chosen at its right. Each Iridium's
// page is a web view of its own (a pane: pane.go), made as it is first shown and kept as another
// is shown: its connections, where it was scrolled, what was typed. The app's own page
// (web/src/appwin) is a pane too, the home: who one is (the first start, no rail then), 添加服务器
// (the rail's +), 本机与授权 (the rail's foot, ⌘,). There is no small window of the app's own
// any more (the picker).
//
// An Iridium may have a window of its own (在新窗口打开 in its menu): no rail there, its icon in
// the rail framed twice; pressed, that window comes forward. 合回主窗口, or that window closed,
// and it is in the main window again. A web view cannot move from one window to another: the page
// is loaded again where it goes, at the address it was at (what was typed is the page's own to
// keep: web/src/chat/composer.tsx keeps drafts).

// mainWin is the main window. Its fields are u.mu's, but draw: the main thread's.
type mainWin struct {
	win   *mygo.Window
	home  *pane            // the app's own page
	panes map[string]*pane // each Iridium's page here, by address
	shown string           // the Iridium shown; "": the home
	view  string           // the home's part: who, add, local, grant:<address>
	left  string           // assembled (assemble.go): the Iridium whose list is beside shown's chat
	sideW map[string]int   // each Iridium's list's width as its page says it

	draw mainDraw // what the window draws (main thread only)
	// grip: the line between two pages side by side, dragged (main thread only): to w.
	grip struct {
		on bool
		w  float32
	}
	edit railEdit // a name being given in the rail (main thread only)
	// editing: a field of the rail has the keyboard, which the window coming to the front (as the
	// rail's menu closes) does not give the page.
	editing atomic.Bool
}

// mainDraw is what the main window draws, set on the main thread (win.Update).
type mainDraw struct {
	page  *mygo.WebView // the page shown
	left  *mygo.WebView // assembled: the page whose list is beside it; nil: none
	leftW float32       // its width
	leftS string        // its Iridium
	zoom  float32       // the pages' zoom: their points to the window's
	rail  *railState    // nil: no rail (no Iridium in the list)
}

// homeName is the home's page's name (u.names).
const homeName = "home"

// mainWindow is the main window, made (and shown) if there is none.
func (u *ui) mainWindow() *mainWin {
	u.mu.Lock()
	m := u.main
	u.mu.Unlock()
	if m != nil {
		return m
	}
	c, _ := loadConfig()
	m = &mainWin{panes: map[string]*pane{}}
	opts := bare(mygo.WindowOptions{
		Title: "Iridium", Width: 1260, Height: 800, MinWidth: 420, MinHeight: 480,
		Content: m.content(u),
	})
	if runtime.GOOS != "darwin" {
		opts.AutoHideMenuBar = true // the menu only for its keys (bridge.go menuBar)
	}
	if f := c.Frame; f.W >= 420 && f.H >= 480 {
		opts.Width, opts.Height, opts.X, opts.Y = f.W, f.H, f.X, f.Y
	}
	if !onScreen(mygo.Rectangle{X: opts.X, Y: opts.Y, Width: opts.Width, Height: opts.Height}) {
		opts.X, opts.Y = 0, 0 // mygo centres it
		if wa := mygo.Screen.PrimaryDisplay().WorkArea; wa.Width > 0 {
			opts.Width, opts.Height = min(opts.Width, wa.Width), min(opts.Height, wa.Height)
		}
	}
	w := mygo.NewWindow(opts)
	if runtime.GOOS != "darwin" && len(u.icon) > 0 {
		_ = w.SetIcon(u.icon)
	}
	m.win = w
	view := "add"
	if len(c.Servers) == 0 {
		view = "who"
	}
	m.view = view
	hv, err := w.NewWebView(mygo.WebViewOptions{URL: themed("/#" + view), Page: pageOptions("", railKeysJS, 0)})
	if err != nil {
		appLog()("main window: %v", err)
		w.Close()
		return nil
	}
	m.home = &pane{name: homeName, win: w}
	m.home.view.Store(hv)
	guard(hv.Page(), "")
	u.named(hv.Page(), homeName)
	u.mu.Lock()
	u.main = m
	u.wins = append(u.wins, w)
	u.win = w
	u.mu.Unlock()
	w.OnFocus(func() {
		// The page shown takes the keyboard, unless a field of the rail has it: a moment later,
		// as the rail's menu, closing (the window in front again), says first what was chosen.
		go func() {
			time.Sleep(80 * time.Millisecond)
			if p := u.pageIn(w); p != nil && !m.editing.Load() {
				if v := p.WebView(); v != nil {
					v.Focus()
				}
			}
		}()
		u.mu.Lock()
		if slices.Contains(u.wins, w) {
			u.win = w
		}
		at := m.shown
		u.mu.Unlock()
		if at != "" {
			go updateConfig(func(c *Config) {
				if c.server(at) != nil {
					c.Current = at
				}
			})
		}
		u.railRefresh() // a window of an Iridium's own no longer the one in front
	})
	w.OnClosed(func() {
		u.mu.Lock()
		defer u.mu.Unlock()
		u.wins = slices.DeleteFunc(u.wins, func(x *mygo.Window) bool { return x == w })
		for _, x := range u.panes {
			if x.win == w {
				delete(u.shows, x.name)
				delete(u.names, x.page())
			}
		}
		delete(u.names, hv.Page())
		u.panes = slices.DeleteFunc(u.panes, func(x *pane) bool { return x.win == w })
		if u.main == m {
			u.main = nil
		}
		if u.win == w {
			u.win = nil
			if n := len(u.wins); n > 0 {
				u.win = u.wins[n-1]
			}
		}
	})
	u.keepFrame(w, func(f Frame) { _, _ = updateConfig(func(c *Config) { c.Frame = f }) })
	followMax(w, func() []*mygo.Page { return u.pagesIn(w) })
	u.railRefresh()
	w.Focus()
	return m
}

// showServer shows address in the main window, at link ("": as its page is; its first page if
// it has none yet): in its window of its own if it has one.
func (u *ui) showServer(address, link string) {
	u.unassemble()
	if w := u.awayWindow(address); w != nil {
		if link != "" {
			if p := u.pageIn(w); p != nil {
				_ = p.LoadURL(themed(link))
			}
		}
		w.Show()
		w.Focus()
		return
	}
	m := u.mainWindow()
	if m == nil {
		return
	}
	u.mu.Lock()
	p := m.panes[address]
	u.mu.Unlock()
	if p == nil {
		if link == "" {
			link = address + "/"
		}
		p = &pane{}
		if err := u.newPane(p, m.win, address, link); err != nil {
			appLog()("pane %s: %v", address, err)
			return
		}
		u.mu.Lock()
		m.panes[address] = p
		u.mu.Unlock()
	} else if link != "" {
		_ = p.page().LoadURL(themed(link))
	}
	u.mu.Lock()
	m.shown = address
	u.mu.Unlock()
	_, _ = updateConfig(func(c *Config) { c.Current = address })
	c, _ := loadConfig()
	title := "Iridium"
	if s := c.server(address); s != nil {
		title = windowTitle(s.Title)
	}
	m.win.SetTitle(title)
	u.railRefresh()
	m.win.Show()
	m.win.Focus()
	p.view.Load().Focus()
	u.redraw()
}

// showHome shows the app's own page in the main window, at part view (who, add, local,
// grant:<address>).
func (u *ui) showHome(view string) {
	u.unassemble()
	m := u.mainWindow()
	if m == nil {
		return
	}
	u.mu.Lock()
	m.shown, m.view = "", view
	u.mu.Unlock()
	reply(m.home.page(), map[string]any{"view": view})
	m.win.SetTitle("Iridium")
	u.railRefresh()
	m.win.Show()
	m.win.Focus()
	m.home.view.Load().Focus()
	c, _ := loadConfig()
	reply(m.home.page(), u.pageState(c))
}

// homePage is the app's own page in the main window; nil: no main window.
func (u *ui) homePage() *mygo.Page {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.main == nil {
		return nil
	}
	return u.main.home.page()
}

// newPane makes p an Iridium's page in w, at link.
func (u *ui) newPane(p *pane, w *mygo.Window, address, link string) error {
	c, _ := loadConfig()
	u.mu.Lock()
	u.nwin++
	name := fmt.Sprintf("w%d", u.nwin)
	u.mu.Unlock()
	v, err := w.NewWebView(mygo.WebViewOptions{
		URL: themed(link),
		// Trusted: this pane's Iridium, no other (Wails took any origin's message and looked).
		Page: pageOptions(originOf(address), css(pageCSS)+pageJS+railKeysJS, c.Zoom),
	})
	if err != nil {
		return err
	}
	p.name, p.win = name, w
	p.view.Store(v)
	guard(v.Page(), originOf(address))
	u.named(v.Page(), name)
	u.mu.Lock()
	if u.shows == nil {
		u.shows = map[string]string{}
	}
	u.shows[name] = address
	u.panes = append(u.panes, p)
	u.mu.Unlock()
	return nil
}

// dropPane forgets p and closes its page (its window stays).
func (u *ui) dropPane(p *pane) {
	u.mu.Lock()
	u.panes = slices.DeleteFunc(u.panes, func(x *pane) bool { return x == p })
	delete(u.shows, p.name)
	delete(u.names, p.page())
	u.mu.Unlock()
	p.view.Load().Destroy()
}

// awayWindow is address's window of its own; nil: none.
func (u *ui) awayWindow(address string) *mygo.Window {
	u.mu.Lock()
	defer u.mu.Unlock()
	for _, p := range u.panes {
		if u.shows[p.name] == address && (u.main == nil || p.win != u.main.win) {
			return p.win
		}
	}
	return nil
}

// pagesIn are the pages in w: the main window's every pane's, else its one.
func (u *ui) pagesIn(w *mygo.Window) []*mygo.Page {
	u.mu.Lock()
	defer u.mu.Unlock()
	var ps []*mygo.Page
	if m := u.main; m != nil && m.win == w {
		ps = append(ps, m.home.page())
	}
	for _, p := range u.panes {
		if p.win == w {
			ps = append(ps, p.page())
		}
	}
	return ps
}

// leave takes address out of the main window (logged out there, removed, gone to a window of its
// own): its page closed; shown, the next in the rail is shown instead, else the home.
func (u *ui) leave(address string) {
	u.mu.Lock()
	m := u.main
	var p *pane
	shown, assembled := false, false
	if m != nil {
		p = m.panes[address]
		delete(m.panes, address)
		shown = m.shown == address
		assembled = m.left != "" && (m.left == address || shown)
	}
	u.mu.Unlock()
	if assembled {
		u.unassemble()
		u.mu.Lock()
		if m.left == "" && m.shown != address && m.panes[m.shown] == nil {
			shown = true
		}
		u.mu.Unlock()
	}
	if m == nil {
		return
	}
	if shown {
		if next := u.nextShown(address); next != "" {
			u.showServer(next, "")
		} else {
			u.showHome("add")
		}
	}
	if p != nil {
		u.dropPane(p)
	}
	u.railRefresh()
}

// nextShown is the Iridium shown after address leaves the main window: the one after it in the
// rail, else the one before, logged in to and not away; "": none.
func (u *ui) nextShown(address string) string {
	c, _ := loadConfig()
	list := u.railOrder(c)
	at := slices.Index(list, address)
	ok := func(a string) bool {
		s := c.server(a)
		return a != address && s != nil && (s.LoggedIn || s.Local && loopback(s.URL)) && u.awayWindow(a) == nil
	}
	for i := at + 1; i < len(list); i++ {
		if ok(list[i]) {
			return list[i]
		}
	}
	for i := at - 1; i >= 0; i-- {
		if ok(list[i]) {
			return list[i]
		}
	}
	return ""
}

// detach gives address a window of its own, its page loaded there where it was here.
func (u *ui) detach(address string) {
	if u.awayWindow(address) != nil {
		return
	}
	link := ""
	u.mu.Lock()
	if m := u.main; m != nil {
		if p := m.panes[address]; p != nil {
			link = p.page().URL()
		}
	}
	u.mu.Unlock()
	if link == "" {
		link = address + "/"
	}
	_, _ = updateConfig(func(c *Config) { c.setAway(address, true, Frame{}) })
	u.newWindow(address, link)
	u.leave(address)
}

// merge brings address back from its window of its own into the main window, shown there.
func (u *ui) merge(address string) {
	w := u.awayWindow(address)
	link := ""
	if w != nil {
		if p := u.pageIn(w); p != nil {
			link = p.URL()
		}
		_, _ = updateConfig(func(c *Config) { c.setAway(address, false, Frame{}) })
		w.Close()
	}
	u.showServer(address, link)
}

// railOrder is the Iridiums in the rail's order: this computer's first (unless hidden), then the
// others as the list has them.
func (u *ui) railOrder(c Config) []string {
	var list []string
	if l := c.local(); l != nil && !c.HideLocal {
		list = append(list, l.URL)
	}
	for _, s := range c.Servers {
		if !s.Local {
			list = append(list, s.URL)
		}
	}
	return list
}

// railPick is the rail's n-th Iridium (from 0) pressed: ⌘1–9.
func (u *ui) railPick(n int) {
	c, _ := loadConfig()
	if list := u.railOrder(c); n >= 0 && n < len(list) {
		u.switchTo(list[n])
	}
}

// railStep shows the Iridium after the one shown (back: before), round: Ctrl+Tab, Ctrl+Shift+Tab.
func (u *ui) railStep(back bool) {
	c, _ := loadConfig()
	list := u.railOrder(c)
	if len(list) == 0 {
		return
	}
	u.mu.Lock()
	at := ""
	if u.main != nil {
		at = u.main.shown
	}
	u.mu.Unlock()
	i := slices.Index(list, at)
	d := 1
	if back {
		d = len(list) - 1
	}
	if i < 0 {
		i = 0
		if back {
			i = len(list) - 1
		}
	} else {
		i = (i + d) % len(list)
	}
	u.switchTo(list[i])
}

// reorder puts address before before in the list (the rail dragged), kept in app.json.
func (u *ui) reorder(address, before string) {
	_, _ = updateConfig(func(c *Config) {
		i := slices.IndexFunc(c.Servers, func(s Server) bool { return s.URL == address })
		if i < 0 || address == before {
			return
		}
		s := c.Servers[i]
		c.Servers = slices.Delete(c.Servers, i, i+1)
		j := slices.IndexFunc(c.Servers, func(s Server) bool { return s.URL == before })
		if j < 0 {
			j = len(c.Servers)
		}
		c.Servers = slices.Insert(c.Servers, j, s)
	})
	u.redraw()
}

// openAway opens the windows of their own left open last time.
func (u *ui) openAway(c Config) {
	for _, a := range c.Away {
		if s := c.server(a.URL); s != nil && (s.LoggedIn || s.Local && loopback(s.URL)) {
			u.newWindow(a.URL, a.URL+"/")
		}
	}
}
