package desktop

import (
	"encoding/base64"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/egoist/mygo"
	mui "github.com/egoist/mygo/ui"
)

// The main window's rail (otae-c, 霓虹, 2026-11-26; docs/app.md "主窗口"): the Iridiums at its
// left, as Discord's servers, drawn by the app itself (mygo's native UI): its tooltips and menus
// show over the page beside it, which a page of 60 points could not. This computer's first
// (unless hidden: Config.HideLocal), a line, the others in the list's order; + (添加服务器) after
// them; 本机与授权 at its foot. An Iridium's icon is its face (Server.Face), else its name's first
// letter (Ir for this computer's):
//   - resting: a circle; pointed at, it rounds into a square, its name beside it;
//   - shown: a rounded square, a long bar at its left;
//   - in a window of its own: a rounded square framed again behind it, the frame darker while
//     that window is in front;
//   - not logged in to: faded, grey, a dashed ring;
//   - a short bar at its left: something new there; a green dot: something running; a number:
//     asks waiting (railItem.unread, running, asks: told by its page, a later step).
// Pressed, it is shown (or its window comes forward); dragged, it moves in the list (app.json);
// its menu: 在新窗口打开 or 合回主窗口, 授权操作, 改名, 重新载入, 移除.

const (
	railW    = 60 // points
	railIcon = 40
	railSlot = 48
)

// railItem is an Iridium in the rail, as drawn.
type railItem struct {
	url, label, letter string
	face               mui.ImageSource // nil: none
	local, loggedIn    bool
	away, awayFront    bool // in a window of its own; that window in front
	unread, running    bool
	asks               int
}

// railLamps is what an Iridium's page says for the rail (web/src/railstatus.ts): something
// running, sessions waiting on you, something unread. None said (an Iridium from before, or its
// page not loaded yet): nothing lit.
type railLamps struct {
	Running bool `json:"running"`
	Asks    int  `json:"asks"`
	Unread  bool `json:"unread"`
}

// railState is the rail as drawn: its Iridiums, which is shown, and the colors.
type railState struct {
	items []railItem
	line  int    // the line after this many (this computer's); 0: none
	shown string // the Iridium shown; "": the app's own page
	view  string // the app's own page's part, shown
	pal   railPalette
}

// railEdit is a name being given in the rail (改名), main thread only.
type railEdit struct {
	url     string
	open    bool
	text    string
	focused bool // the field took the keyboard (from the page beside it)
}

// railPalette: web/src/styles.css's greys (--paper, --ink…), day and night.
type railPalette struct {
	bg, edge, ink, ink2, ink3, fill, fill2, paper, plus, warn mui.Color
}

var (
	railDay = railPalette{bg: mui.Hex("#f7f7f7"), edge: mui.RGBA(0, 0, 0, 0.08), ink: mui.Hex("#1b1b1b"),
		ink2: mui.Hex("#585858"), ink3: mui.Hex("#808080"), fill: mui.Hex("#ebebeb"), fill2: mui.Hex("#e4e4e4"),
		paper: mui.Hex("#fdfdfd"), plus: mui.Hex("#2e8a55"), warn: mui.Hex("#d9902a")}
	railNight = railPalette{bg: mui.Hex("#0c0c0c"), edge: mui.RGBA(255, 255, 255, 0.09), ink: mui.Hex("#ebebeb"),
		ink2: mui.Hex("#b7b7b7"), ink3: mui.Hex("#8f8f8f"), fill: mui.Hex("#232323"), fill2: mui.Hex("#2a2a2a"),
		paper: mui.Hex("#121212"), plus: mui.Hex("#4fb57a"), warn: mui.Hex("#e0a03c")}
)

// faces are the Iridiums' faces decoded, by their data: URL.
var faces struct {
	sync.Mutex
	m map[string]mui.ImageSource
}

// faceImage is a data: URL's image; nil: none, or not one.
func faceImage(data string) mui.ImageSource {
	if data == "" {
		return nil
	}
	faces.Lock()
	defer faces.Unlock()
	if img, ok := faces.m[data]; ok {
		return img
	}
	var img mui.ImageSource
	if _, b64, ok := strings.Cut(data, ";base64,"); ok {
		if raw, err := base64.StdEncoding.DecodeString(b64); err == nil {
			img, _ = mui.DecodeImage(raw)
		}
	}
	if faces.m == nil {
		faces.m = map[string]mui.ImageSource{}
	}
	faces.m[data] = img
	return img
}

// letterOf is a name's first letter, as an icon shows it.
func letterOf(s string) string {
	s = strings.TrimSpace(s)
	r, _ := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError {
		return "?"
	}
	return string(unicode.ToUpper(r))
}

// railRefresh draws the rail again from the list as it is now (off the main thread).
func (u *ui) railRefresh() {
	go func() {
		u.mu.Lock()
		m := u.main
		u.mu.Unlock()
		if m == nil {
			return
		}
		c, _ := loadConfig()
		u.mu.Lock()
		shown, view := m.shown, m.view
		var page, left *mygo.WebView
		if p := m.panes[shown]; shown != "" && p != nil {
			page = p.view.Load()
		} else {
			page = m.home.view.Load()
		}
		leftW := float32(sideWidth)
		if p := m.panes[m.left]; m.left != "" && p != nil {
			left = p.view.Load()
			if w := m.sideW[m.left]; w > 0 {
				leftW = float32(w)
			}
			shown = m.left // the rail stays on the list's
		}
		// Said by a page still there: one closed (its window, logged out) says nothing any more.
		lamps := map[string]railLamps{}
		for _, a := range u.shows {
			if l, ok := u.lamps[a]; ok {
				lamps[a] = l
			}
		}
		// In a window of its own; true: that window the app's one in front.
		away := map[string]bool{}
		for _, p := range u.panes {
			if p.win != m.win {
				away[u.shows[p.name]] = p.win == u.win
			}
		}
		u.mu.Unlock()
		var rail *railState
		if len(c.Servers) > 0 {
			rail = &railState{shown: shown, view: view, pal: railDay}
			if railDark(c) {
				rail.pal = railNight
			}
			for _, a := range u.railOrder(c) {
				s := c.server(a)
				it := railItem{url: a, label: s.label(), local: s.Local, loggedIn: s.LoggedIn || s.Local && loopback(s.URL), face: faceImage(s.Face)}
				it.letter = letterOf(it.label)
				if s.Local {
					it.letter, it.label = "Ir", "此电脑"
					if s.Title != "" {
						it.label = s.Title
					}
				}
				front, ok := away[a]
				it.away, it.awayFront = ok, front
				if l, ok := lamps[a]; ok && it.loggedIn {
					it.running, it.asks, it.unread = l.Running, max(l.Asks, 0), l.Unread
				}
				rail.items = append(rail.items, it)
			}
			if l := c.local(); l != nil && !c.HideLocal && len(rail.items) > 1 {
				rail.line = 1
			}
		}
		zoom := float32(1)
		if c.Zoom > 0 {
			zoom = float32(c.Zoom)
		}
		leftS := ""
		if left != nil {
			leftS = m.left
		}
		m.win.Update(func() {
			m.draw = mainDraw{page: page, left: left, leftW: leftW * zoom, leftS: leftS, zoom: zoom, rail: rail}
		})
	}()
}

// railDark: is the rail drawn at night (the Iridiums' pages' theme, Config.Theme; auto: the
// system's)?
func railDark(c Config) bool {
	switch c.Theme {
	case "dark":
		return true
	case "light":
		return false
	}
	return mygo.Theme.IsDark()
}

// content is the main window's UI: the rail, and the page shown beside it.
func (m *mainWin) content(u *ui) mygo.Content {
	return mui.View(func(c *mui.Context) {
		d := m.draw
		root := mui.Row(c).Fill().AlignItems(mui.Stretch)
		root.Children(func() {
			if d.rail != nil {
				m.railView(c, u, d.rail)
			}
			if d.left != nil {
				// Assembled (assemble.go): the left page's list, as wide as it, beside the chat.
				mui.WebView(c, d.left).Width(d.leftW)
				m.gripView(c, u, d)
			}
			if d.page != nil {
				mui.WebView(c, d.page).Grow(1).MinWidth(0)
			}
			if m.grip.on {
				// While the line is dragged the app's UI covers the pages: the pointer, past the
				// line, would be theirs, and the drag lost.
				mui.Box(c).Absolute().Left(0).Top(0).Fill().Cursor(mui.CursorResizeEW).Background(mui.RGBA(0, 0, 0, 0.004))
			}
		})
	})
}

var (
	// irLetters: web/src/ui.tsx IrMark's "Ir", in the text's color, on the 64 of its square.
	irLetters = mui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="64" height="64" viewBox="0 0 64 64"><path d="M20 18h6v28h-6zM32 28h5.6v3.2c1.4-2.4 3.6-3.6 6.4-3.6v5.8c-3.8 0-6.2 1.6-6.2 6V46H32z" fill="currentColor"/></svg>`))
	iconPlus  = mui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M5 12h14"/><path d="M12 5v14"/></svg>`))
	iconLocal = mui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M18 8V6a2 2 0 0 0-2-2H4a2 2 0 0 0-2 2v7a2 2 0 0 0 2 2h8"/><path d="M10 19v-3.96 3.15"/><path d="M7 19h5"/><rect width="6" height="10" x="16" y="12" rx="2"/></svg>`))
)

// gripView draws the line between two pages side by side, which drags the left one's width: a
// pointer leaving a page while it drags its own line goes to the page beside it (WebKitGTK), so
// the app's own UI drags it. While it is dragged a line shows where it goes; let go, both pages
// take their widths once (no page laid out at every step), and the left page keeps it as its
// list's (sidebarW). Pressed twice, as the page's own line: the width it starts at.
func (m *mainWin) gripView(c *mui.Context, u *ui, d mainDraw) {
	edge, ink := railDay.edge, railDay.ink3
	if d.rail != nil {
		edge, ink = d.rail.pal.edge, d.rail.pal.ink3
	}
	lo, hi := 220*d.zoom, 640*d.zoom
	// The grip, 7 points wide over the line, its own place in the row given back to the pages
	// beside it (a child of a box 1 point wide is not pressed outside that point).
	h := mui.Box(c).Width(7).MarginX(-3).ZIndex(1).Cursor(mui.CursorResizeEW).Background(mui.RGBA(0, 0, 0, 0.004))
	if dx, _, ok := h.Dragged(); ok {
		if !m.grip.on {
			m.grip.on, m.grip.w = true, d.leftW
		}
		m.grip.w += dx
	}
	if _, _, ok := h.DragEnded(); ok && m.grip.on {
		w := min(max(m.grip.w, lo), hi)
		m.grip.on = false
		server, zoom := d.leftS, d.zoom
		go u.sideSet(server, int(w/zoom+0.5))
	}
	if h.DoubleClicked() {
		server := d.leftS
		go u.sideSet(server, sideWidth)
	}
	h.Children(func() {
		mui.Box(c).Absolute().Left(3).Top(0).Width(1).FillHeight().Background(edge)
		if m.grip.on {
			at := min(max(m.grip.w, lo), hi) - d.leftW
			mui.Box(c).Absolute().Left(3 + at - 1).Top(0).Width(2).FillHeight().Background(ink)
		}
	})
}

// railView draws the rail. Its foot keeps 16 points: on Windows a frameless window's last points
// may be under its frame's edge.
func (m *mainWin) railView(c *mui.Context, u *ui, r *railState) {
	p := r.pal
	mui.Column(c).Width(railW).Background(p.bg).AlignItems(mui.Center).Padding(10, 0, 16, 0).Gap(4).DragWindow().Children(func() {
		for i, it := range r.items {
			if i == r.line && r.line > 0 {
				mui.Box(c.Key("line")).Size(28, 2).Radius(1).Background(p.edge).Margin(4, 0, 4, 0)
			}
			m.railIcon(c.Key(it.url), u, r, it)
		}
		m.railKey(c.Key("add"), u, r, iconPlus, "添加服务器", "add")
		mui.Spacer(c).Grow(1)
		m.railKey(c.Key("local"), u, r, iconLocal, "本机与授权", "local")
	})
	mui.Box(c).Width(1).Background(p.edge)
}

// railIcon draws an Iridium in the rail.
func (m *mainWin) railIcon(c *mui.Context, u *ui, r *railState, it railItem) {
	p := r.pal
	shown := r.shown == it.url
	slot := mui.Box(c).Size(railW, railSlot)
	hover := slot.Hovered()
	square := shown || it.away || hover
	radius := slot.Animate("r", map[bool]float32{true: 12, false: railIcon / 2}[square], 140*time.Millisecond)
	bar := float32(0)
	switch {
	case shown:
		bar = 30
	case hover:
		bar = 16
	case it.unread:
		bar = 8
	}
	bar = slot.Animate("bar", bar, 140*time.Millisecond)
	top := float32(railSlot-railIcon) / 2
	left := float32(railW-railIcon) / 2
	slot.Children(func() {
		if bar > 0.5 {
			mui.Box(c).Absolute().Left(0).Top((railSlot-bar)/2).Size(4, bar).Radius(0, 2, 2, 0).Background(p.ink)
		}
		if it.away {
			// Framed again behind it: in a window of its own.
			frame := p.ink3
			if it.awayFront {
				frame = p.ink
			}
			mui.Box(c).Absolute().Left(left+4).Top(top-4).Size(railIcon, railIcon).Radius(12).Border(1.5, frame)
		}
		icon := mui.Column(c).Absolute().Left(left).Top(top).Size(railIcon, railIcon).Radius(radius).Clip().
			AlignItems(mui.Center).Justify(mui.Center)
		if it.face == nil {
			bg, fg := p.fill2, p.ink2
			if shown {
				bg, fg = p.ink, p.paper
			} else if hover {
				fg = p.ink
			}
			icon.Background(bg).TextColor(fg)
		} else {
			icon.Background(p.paper)
		}
		if !it.loggedIn {
			icon.Opacity(0.45)
		}
		icon.Children(func() {
			if it.face != nil {
				img := mui.Image(c, it.face).Size(railIcon, railIcon)
				if !it.loggedIn {
					img.Grayscale()
				}
			} else if it.local {
				// Iridium's mark (web/src/ui.tsx IrMark), its letters alone: the icon is its ground.
				mui.Icon(c, irLetters).Size(railIcon, railIcon)
			} else {
				size := float32(16)
				if utf8.RuneCountInString(it.letter) > 1 {
					size = 14
				}
				mui.Text(c, it.letter).FontSize(size).Bold()
			}
		})
		if !it.loggedIn {
			mui.Box(c).Absolute().Left(left-3).Top(top-3).Size(railIcon+6, railIcon+6).Radius(radius+3).
				Border(1.2, p.ink3).BorderStyle(mui.BorderDashed)
		}
		if it.running && it.asks == 0 {
			mui.Box(c).Absolute().Left(left+railIcon-10).Top(top+railIcon-10).Size(12, 12).Radius(6).
				Background(p.plus).Border(2.5, p.bg)
		}
		if it.asks > 0 {
			n := "9+"
			if it.asks < 10 {
				n = string(rune('0' + it.asks))
			}
			mui.Column(c).Absolute().Left(left+railIcon-12).Top(top+railIcon-12).MinWidth(17).Height(17).Radius(9).
				PaddingX(4).Background(p.warn).Border(2.5, p.bg).AlignItems(mui.Center).Justify(mui.Center).
				Children(func() { mui.Text(c, n).FontSize(10).Bold().TextColor(mui.Hex("#ffffff")) })
		}
	})
	label := it.label
	if !it.loggedIn {
		label += " · 未登录"
	}
	railTip(c, slot, label, p)
	slot.Label(label).Cursor(mui.CursorPointer).Drag(it.url)
	if from, ok := mui.Drop[string](slot); ok && from != it.url {
		go u.reorder(from, it.url)
	}
	if slot.Clicked() {
		go u.switchTo(it.url)
	}
	url := it.url
	slot.ContextMenu(func(menu *mui.Menu) {
		if it.away {
			if menu.Item("合回主窗口").Chosen() {
				go u.merge(url)
			}
		} else if menu.Item("在新窗口打开").Disabled(!it.loggedIn).Chosen() {
			go u.detach(url)
		}
		menu.Separator()
		if menu.Item("授权操作").Disabled(it.local || !it.loggedIn).Chosen() {
			go u.showHome("grant:" + url)
		}
		if menu.Item("改名").Chosen() {
			m.edit = railEdit{url: url, open: true, text: strings.TrimSuffix(it.label, " · 未登录")}
			m.editing.Store(true)
			if it.local && it.label == "此电脑" {
				m.edit.text = ""
			}
		}
		if menu.Item("重新载入").Disabled(!it.loggedIn).Chosen() {
			go u.reloadServer(url)
		}
		menu.Separator()
		if it.local {
			if menu.Item("在左侧栏隐藏").Chosen() {
				go u.hideLocal(true)
			}
		} else if menu.Item("移除").Danger().Chosen() {
			go u.removeServer(url, it.label)
		}
	})
	if m.edit.url == it.url {
		mui.Popover(c, slot, &m.edit.open, func() {
			in := mui.TextInput(c, &m.edit.text).Width(200).AutoFocus().Placeholder(it.label)
			if !m.edit.focused {
				// The page had the keyboard: AutoFocus alone left it there.
				m.edit.focused = true
				in.Focus()
			}
			if in.Submitted() {
				m.edit.open = false
				name, url := m.edit.text, it.url
				go func() { u.retitle(url, name); u.railRefresh(); u.tellTitle(url) }()
			}
		})
		if !m.edit.open {
			m.edit = railEdit{}
			m.editing.Store(false)
		}
	}
}

// railKeysJS is put in the pages of the main window: Ctrl+Tab shows the next Iridium in the rail,
// Ctrl+Shift+Tab the one before (ir:rail:next, prev). GTK keeps Ctrl+Tab from a menu's keys.
const railKeysJS = `addEventListener("keydown",function(e){if(e.ctrlKey&&!e.metaKey&&!e.altKey&&e.key==="Tab"){e.preventDefault();e.stopPropagation();var w=window._wails;if(w&&w.invoke)w.invoke("ir:rail:"+(e.shiftKey?"prev":"next"))}},true);`

// railKey draws one of the rail's keys for the app's own page: + (添加服务器), 本机与授权.
func (m *mainWin) railKey(c *mui.Context, u *ui, r *railState, icon *mui.SVG, label, view string) {
	p := r.pal
	on := r.shown == "" && (r.view == view || view == "local" && strings.HasPrefix(r.view, "grant:"))
	slot := mui.Box(c).Size(railW, railSlot)
	hover := slot.Hovered()
	radius := slot.Animate("r", map[bool]float32{true: 12, false: railIcon / 2}[on || hover], 140*time.Millisecond)
	left := float32(railW-railIcon) / 2
	top := float32(railSlot-railIcon) / 2
	slot.Children(func() {
		if on {
			mui.Box(c).Absolute().Left(0).Top((railSlot-30)/2).Size(4, 30).Radius(0, 2, 2, 0).Background(p.ink)
		}
		key := mui.Column(c).Absolute().Left(left).Top(top).Size(railIcon, railIcon).Radius(radius).
			AlignItems(mui.Center).Justify(mui.Center).TextColor(p.ink2)
		switch {
		case on:
			key.Background(p.ink).TextColor(p.paper)
		case hover:
			key.Background(p.fill2).TextColor(p.ink)
		}
		key.Children(func() { mui.Icon(c, icon).Size(18, 18) })
	})
	railTip(c, slot, label, p)
	slot.Label(label).Cursor(mui.CursorPointer)
	if slot.Clicked() {
		go u.showHome(view)
	}
}

// railTip is a name shown at the right of what the pointer rests on in the rail, over the page
// beside it: ink, the paper's color for its letters.
func railTip(c *mui.Context, at mui.Element, label string, p railPalette) {
	mui.TooltipBase(c, at, func(tip mui.Element) {
		tip.AttachTo(at, mui.AnchorEnd, mui.AnchorStart).MarginStart(2)
		tip.Padding(5, 9).Radius(7).Background(p.ink).TextColor(p.paper)
		tip.Children(func() { mui.Text(c, label).FontSize(12).Bold() })
	})
}

// syncFaces fetches each Iridium's face in the background and refreshes the rail and favorites if
// any changed (since the app opens straight into an Iridium without opening the built-in page).
func (u *ui) syncFaces() {
	c, _ := loadConfig()
	if _, changed := refreshFaces(c); changed {
		u.railRefresh()
		u.favsTell()
		u.redraw()
	}
}

// reloadServer loads address's page again, wherever it is.
func (u *ui) reloadServer(address string) {
	go u.syncFaces()
	u.mu.Lock()
	var ps []*mygo.Page
	for _, p := range u.panes {
		if u.shows[p.name] == address {
			ps = append(ps, p.page())
		}
	}
	u.mu.Unlock()
	for _, p := range ps {
		p.Reload()
	}
}

// removeServer takes address out of the list once the person says so here (as 移除 in the app's
// page, pressed twice there).
func (u *ui) removeServer(address, label string) {
	u.mu.Lock()
	var parent *mygo.Window
	if u.main != nil {
		parent = u.main.win
	}
	u.mu.Unlock()
	r, err := mygo.Dialog.Message(mygo.MessageOptions{Parent: parent, Type: mygo.MessageWarning, Message: "移除 " + label + "？",
		Detail: "它从这里的列表中移除，这台电脑与它的连接断开。", Buttons: []string{"移除", "取消"}, DefaultButton: 1, CancelButton: 1,
		DestructiveButtons: []int{0}})
	if err != nil || r.Button != 0 {
		return
	}
	if c, err := updateConfig(func(c *Config) { c.remove(address) }); err == nil {
		u.joins.Sync(c)
		u.gone(address)
		u.redraw()
	}
}

// hideLocal hides this computer's Iridium in the rail (on), or shows it again.
func (u *ui) hideLocal(on bool) {
	_, _ = updateConfig(func(c *Config) { c.HideLocal = on })
	if on {
		c, _ := loadConfig()
		if l := c.local(); l != nil {
			u.leave(l.URL)
		}
	}
	u.redraw()
}

// tellTitle tells address's pages the name it goes by here, given in the rail.
func (u *ui) tellTitle(address string) {
	c, _ := loadConfig()
	u.mu.Lock()
	var ps []*mygo.Page
	for _, p := range u.panes {
		if u.shows[p.name] == address {
			ps = append(ps, p.page())
		}
	}
	u.mu.Unlock()
	for _, p := range ps {
		reply(p, u.hello(c, address))
	}
}
