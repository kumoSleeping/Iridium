package desktop

import (
	"runtime"
	"slices"

	"github.com/egoist/mygo"
)

// An Iridium's window of its own (mainwin.go): its page alone, no rail; opened from its menu in
// the main window's rail (在新窗口打开), where and as large as it was last. Closed, the Iridium is
// in the main window again. The one in front of all the app's windows is u.win (nil: none), and
// the app's next start shows the one used last (Config.Current). With none left the app stays in
// the tray.

// watch takes p's window among the windows: in front as it is focused, its Iridium then the last
// used, its page given the keyboard; the rail shows it framed, and darker as it is in front.
func (u *ui) watch(p *pane) {
	w, name := p.win, p.name
	u.mu.Lock()
	u.wins = append(u.wins, w)
	u.win = w
	u.mu.Unlock()
	w.OnFocus(func() {
		// The window's UI takes the keyboard as it comes to the front; the page shown wants it.
		p.view.Load().Focus()
		u.mu.Lock()
		if slices.Contains(u.wins, w) {
			u.win = w
		}
		at := u.shows[name]
		u.mu.Unlock()
		if at != "" {
			go updateConfig(func(c *Config) {
				if c.server(at) != nil {
					c.Current = at
				}
			})
		}
		u.railRefresh()
	})
	w.OnClosed(func() {
		u.mu.Lock()
		at := u.shows[name]
		quitting := u.quitting
		u.wins = slices.DeleteFunc(u.wins, func(x *mygo.Window) bool { return x == w })
		for _, x := range u.panes {
			if x.win == w {
				delete(u.shows, x.name)
				delete(u.names, x.page())
			}
		}
		u.panes = slices.DeleteFunc(u.panes, func(x *pane) bool { return x.win == w })
		if u.win == w {
			u.win = nil
			if n := len(u.wins); n > 0 {
				u.win = u.wins[n-1]
			}
		}
		u.mu.Unlock()
		// Closed by the person: back in the main window at the next start (quitting keeps it).
		if !quitting && at != "" {
			go func() {
				_, _ = updateConfig(func(c *Config) { c.setAway(at, false, Frame{}) })
				u.railRefresh()
			}()
		}
	})
	u.keepFrame(w, func(f Frame) {
		u.mu.Lock()
		at := u.shows[name]
		u.mu.Unlock()
		if at != "" {
			_, _ = updateConfig(func(c *Config) {
				if c.away(at) != nil {
					c.setAway(at, true, f)
				}
			})
		}
	})
}

// newWindow opens a window of its own on address, at link: where and as large as it was last,
// else a little off the one in front.
func (u *ui) newWindow(address, link string) {
	c, _ := loadConfig()
	u.mu.Lock()
	front := u.win
	u.mu.Unlock()
	title := "Iridium"
	if s := c.server(address); s != nil {
		title = windowTitle(s.Title)
	}
	// No title bar on any system (chrome.go): the page goes up to the window's top, its own keys
	// at its top right. The window is the app's own UI, the page a web view in it (pane.go).
	p := &pane{}
	opts := bare(mygo.WindowOptions{
		Title: title, Width: 1200, Height: 800, MinWidth: 360, MinHeight: 480,
		Content: p.content(),
	})
	if runtime.GOOS != "darwin" {
		opts.AutoHideMenuBar = true // the menu only for its keys (bridge.go menuBar)
	}
	if a := c.away(address); a != nil && a.Frame.W >= 360 && a.Frame.H >= 480 {
		f := a.Frame
		opts.Width, opts.Height, opts.X, opts.Y = f.W, f.H, f.X, f.Y
	} else if front != nil {
		x, y := front.Position()
		opts.Width, opts.Height = front.Size()
		opts.X, opts.Y = x+28, y+28
	}
	// A place left on a screen no longer there (a second screen taken away) is not kept: the
	// window comes in the middle of the main screen instead (QGI, 2026-11-23: off every screen).
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
	if err := u.newPane(p, w, address, link); err != nil {
		appLog()("window on %s: %v", address, err)
		w.Close()
		return
	}
	w.Invalidate()
	u.watch(p)
	followMax(w, func() []*mygo.Page { return []*mygo.Page{p.page()} })
	w.Focus()
	p.view.Load().Focus()
	u.railRefresh()
}

// onScreen: is enough of r on a screen there now to take hold of it (its top strip, which drags
// it: 120 by 40 points of it)? A zero place (X and Y 0) is mygo's centred, always on one.
func onScreen(r mygo.Rectangle) bool {
	if r.X == 0 && r.Y == 0 {
		return true
	}
	top := mygo.Rectangle{X: r.X, Y: r.Y, Width: r.Width, Height: 40}
	for _, d := range mygo.Screen.Displays() {
		wa := d.WorkArea
		w := min(top.X+top.Width, wa.X+wa.Width) - max(top.X, wa.X)
		h := min(top.Y+top.Height, wa.Y+wa.Height) - max(top.Y, wa.Y)
		if w >= 120 && h >= 20 {
			return true
		}
	}
	return false
}

// keepOnScreen brings back every window of the app left off the screens there now: as the
// screens change (one taken away), and as the app starts.
func (u *ui) keepOnScreen() {
	u.mu.Lock()
	ws := append([]*mygo.Window(nil), u.wins...)
	u.mu.Unlock()
	wa := mygo.Screen.PrimaryDisplay().WorkArea
	for _, w := range ws {
		r := w.Bounds()
		if onScreen(r) || wa.Width == 0 {
			continue
		}
		r.Width, r.Height = min(r.Width, wa.Width), min(r.Height, wa.Height)
		r.X, r.Y = wa.X+(wa.Width-r.Width)/2, wa.Y+(wa.Height-r.Height)/2
		w.SetBounds(r)
	}
}
