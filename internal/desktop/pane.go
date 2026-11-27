package desktop

import (
	"sync/atomic"

	"github.com/egoist/mygo"
	mui "github.com/egoist/mygo/ui"
)

// Windows on Iridiums are the app's own (kumo, otae-c, 2026-11-26: several Iridiums in one
// window, a rail of them at its left as Discord's; docs/app.md "一个窗口多台服务器"): a window
// of native UI (mygo's Content) showing the page of one of its panes, each an Iridium's page in
// a web view (mygo.WebView). A pane not shown keeps its page: its connections, where it was
// scrolled, what was typed in it. Window things (focus, close, its frame, maximised) are the
// window's; page things (replies, reload, ⌘W, downloads) the pane's page's.
//
// Step one: one pane a window, the window as it was.

// pane is an Iridium's page in a window.
type pane struct {
	name string                       // w1, w2…: its Iridium is u.shows[name]
	win  *mygo.Window                 // the window it is in
	view atomic.Pointer[mygo.WebView] // its page, once made
}

func (p *pane) page() *mygo.Page { return p.view.Load().Page() }

// content is the window's UI: the pane's page, the whole of it.
func (p *pane) content() mygo.Content {
	return mui.View(func(c *mui.Context) {
		if v := p.view.Load(); v != nil { // none in a frame drawn before it is made
			mui.WebView(c, v).Fill()
		}
	})
}

// pageIn is the page window w shows: an Iridium's pane's, else w's own (the picker, ask); nil:
// none.
func (u *ui) pageIn(w *mygo.Window) *mygo.Page {
	if w == nil {
		return nil
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	for _, p := range u.panes {
		if p.win == w && p.view.Load() != nil {
			return p.page()
		}
	}
	return w.Page()
}

// paneOn is the pane on address (u.shows); nil: none. u.mu held.
func (u *ui) paneOn(address string) *pane {
	for _, p := range u.panes {
		if u.shows[p.name] == address {
			return p
		}
	}
	return nil
}
