package desktop

import (
	"fmt"
	"runtime"
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo"
)

// The windows' frame (kumo, 2026-11-22): no title bar on any system. The window's keys are drawn
// by the page itself at its top right, its top bar's icon keys (web/src/winkey.tsx, the picker's
// in web/src/appwin); a Mac's own three buttons are hidden. Three: minimise (QGI and kumo,
// 2026-11-23), full screen (a Mac's own, its green button's; Windows' maximise), close.
// The page's top bar drags the window (chromeJS); on Windows and Linux, where the window has no
// frame of its own, mygo resizes it from its edges.

// bare is o with no title bar: on a Mac a titled window whose bar and buttons are hidden (a
// frameless one misbehaved there: docs/app.md "窗口框架"), elsewhere a frameless one, the
// system's shadow and snapping kept.
func bare(o mygo.WindowOptions) mygo.WindowOptions {
	if runtime.GOOS == "darwin" {
		o.TitleBarStyle = mygo.TitleBarHidden
		o.HideWindowButtons = true
	} else {
		o.Frameless = true
	}
	return o
}

// winKey is a window key pressed in a page: min, full (a Mac's full screen), max
// (maximised or back), close, reload. The keys are the window's the page is in.
func (u *ui) winKey(p *mygo.Page, key string) {
	if p == nil || p.Window() == nil {
		return
	}
	w, name := p.Window(), u.nameOf(p)
	switch key {
	case "min":
		w.Minimize()
	case "max":
		w.ToggleMaximize()
	case "full":
		w.ToggleFullScreen()
	case "close":
		appLog()("window %q closed by its page's key", name)
		if name != "ask" {
			w.Close()
		}
	case "reload":
		u.reload(p)
	}
}

// followMax tells the pages in w (pages: those there now) whether w is full screen (a Mac) or
// maximised, as it changes: their key is drawn as it is.
func followMax(w *mygo.Window, pages func() []*mygo.Page) {
	tell := func() {
		go func() {
			full := w.IsMaximized()
			if runtime.GOOS == "darwin" {
				full = w.IsFullScreen()
			}
			for _, p := range pages() {
				reply(p, map[string]any{"max": full})
			}
		}()
	}
	w.OnMaximize(tell)
	w.OnUnmaximize(tell)
	w.OnRestore(tell)
	w.OnEnterFullScreen(tell)
	w.OnLeaveFullScreen(tell)
}

// reload loads the page in w again (⌘R, Ctrl+R, F5; kumo, 2026-11-22).
func (u *ui) reload(p *mygo.Page) {
	if p != nil && u.nameOf(p) != "ask" {
		appLog()("reload %q", u.nameOf(p))
		go p.Reload()
	}
}

// closePage is ⌘W, Ctrl+W (kumo, 2026-11-22): the page in front shut, not the window; only from
// the page's first one (nothing open) the window. The page says which (appkey.tsx closePage); one
// that cannot, an Iridium from before, is taken back to its first page by its address.
func (u *ui) closePage(p *mygo.Page) {
	if p == nil {
		return
	}
	switch u.nameOf(p) {
	case homeName:
		// The app's own page: back to the Iridium shown before, else the window closes.
		go func() {
			c, _ := loadConfig()
			u.mu.Lock()
			m := u.main
			here := m != nil && m.panes[c.current()] != nil
			u.mu.Unlock()
			switch {
			case here:
				u.showServer(c.current(), "")
			case m != nil:
				m.win.Close()
			}
		}()
		return
	case "ask", "":
		return
	}
	go func() {
		// A page from before (no __irClosePage): an open chat or settings page goes back to the
		// first page, as the new pages do; from the first page the window closes.
		v, err := p.Eval(`if(window.__irClosePage){window.__irClosePage();return "page"}
var h=location.hash.replace(/^#/,"");if(h&&h!=="/"){location.hash="#/";return "home"}
window._wails.invoke("ir:win:close");return "window"`)
		appLog()("close page in %q: %v %v", u.nameOf(p), v, err)
	}()
}

// retitle names address's windows here (Server.Title; "": Iridium), the app's own name for it.
func (u *ui) retitle(address, title string) {
	title = strings.TrimSpace(strings.Join(strings.Fields(title), " "))
	if utf8.RuneCountInString(title) > 40 {
		title = string([]rune(title)[:40])
	}
	if _, err := updateConfig(func(c *Config) {
		if s := c.server(address); s != nil {
			s.Title = title
		}
	}); err != nil {
		return
	}
	u.mu.Lock()
	var ws []*mygo.Window
	for _, p := range u.panes {
		if u.shows[p.name] == address {
			ws = append(ws, p.win)
		}
	}
	u.mu.Unlock()
	for _, w := range ws {
		w.SetTitle(windowTitle(title))
	}
	u.redraw()
}

// windowTitle is a window's title: its Iridium's name here, else Iridium.
func windowTitle(title string) string {
	if title == "" {
		return "Iridium"
	}
	return title
}

// chromeJS is put in every page a window shows: its top bar drags the window (mygo's --app-region;
// a double click there does as the system's title bar does). The top bar is what the page marks
// data-app-drag (the keys, fields and links in it excepted). A page that draws no window keys of
// its own (data-win-keys; an Iridium from before) gets a strip of its own above it instead, which
// drags the window, the keys at its right, the page moved down under it: nothing of the page is
// covered (kumo, 2026-11-23: keys laid over the page's own top right hid its app key). Only once
// the page has been drawn without keys for a few seconds: a page still starting, its keys a
// moment away, had the strip come and go over it (kumo, 2026-11-24).
var chromeJS = css(`[data-app-drag]{--app-region:drag}`) + fmt.Sprintf(`(function(){
var mac=%t,H=32,miss=0;
function inv(m){var w=window._wails;if(w&&w.invoke)w.invoke(m)}
function plain(){var have=document.querySelector("[data-win-keys]:not([data-win-keys=app])"),ours=document.getElementById("ir-strip");
if(have){miss=0;if(ours){ours.remove();document.documentElement.style.removeProperty("--ir-strip")}return}
if(ours||!document.body||!document.getElementById("root")||!document.getElementById("root").children.length){miss=0;return}
if(++miss<4)return;
var d=document.createElement("div");d.id="ir-strip";d.setAttribute("data-app-drag","");
d.style.cssText="position:fixed;top:0;left:0;right:0;height:"+H+"px;z-index:2147483647;display:flex;justify-content:flex-end;align-items:center;gap:2px;padding-right:6px;box-sizing:border-box;background:Canvas;color:CanvasText";
var k=document.createElement("span");k.setAttribute("data-win-keys","app");k.style.cssText="display:flex;gap:2px";
[["min","\u2013"],[mac?"full":"max",mac?"\u2922":"\u25a1"],["close","\u2715"]].forEach(function(x){var b=document.createElement("button");b.type="button";b.setAttribute("aria-label",x[0]);b.textContent=x[1];
b.style.cssText="width:28px;height:24px;border-radius:6px;border:0;padding:0;cursor:default;background:none;color:inherit;opacity:.6;font:13px system-ui";b.onclick=function(){inv("ir:win:"+x[0])};k.appendChild(b)});
d.appendChild(k);document.body.appendChild(d);document.documentElement.style.setProperty("--ir-strip",H+"px")}
setInterval(plain,1000);
})();
`, runtime.GOOS == "darwin") + css(`#root{box-sizing:border-box;padding-top:var(--ir-strip,0px)}`)
