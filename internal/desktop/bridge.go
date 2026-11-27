package desktop

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/egoist/mygo"
)

// The pages' side of the app on mygo. An Iridium's page (web/src/irapp.ts) and the app's own
// (web/src/appwin/bridge.ts) speak to the app as they did in Wails: window._wails.invoke("ir:…")
// out, "ir-app" events in. The preload keeps window._wails over mygo's bound method, so pages
// and Iridiums of any version are as they were.

// bridge is the bound service the preload calls (IRApp.Invoke).
type bridge struct {
	u    *ui
	logf func(string, ...any)
}

// Invoke is a page's message: the page and its window are mygo's, from the call; the origin is
// the page's own URL's. mygo has already refused pages it does not trust (the app's own, and each
// window's own Iridium: TrustedOrigins).
func (b *bridge) Invoke(ctx context.Context, msg string) {
	p := mygo.CallerPage(ctx)
	if p == nil || len(msg) > 1<<20 {
		return
	}
	b.u.message(p, msg, originOf(p.URL()), b.logf)
}

// originOf is scheme://host[:port] of a page's URL; "": none.
func originOf(raw string) string {
	p, err := url.Parse(raw)
	if err != nil || p.Scheme == "" || p.Host == "" {
		return ""
	}
	return p.Scheme + "://" + p.Host
}

// pathOf is a URL's path alone, for the log (no query: whatever a link carries stays out of it).
func pathOf(raw string) string {
	if p, err := url.Parse(raw); err == nil {
		return p.Path
	}
	return ""
}

// ownPage: is origin the app's own pages' (mygo's frontend: mygo://localhost, or on Windows
// http://mygo.localhost)?
func ownPage(origin string) bool {
	return origin == "mygo://localhost" || origin == "http://mygo.localhost"
}

// shim is the preload of every window: window._wails.invoke over mygo's bound method, messages
// queued till mygo's bridge is there, and the event the pages wait for (wails:runtime-config-ready)
// once the page has loaded. Only "ir:" messages go: Wails's own (wails:drag) are mygo's CSS here
// (data-app-drag, chrome.go).
var shim = `(function(){
if(window._wails)return;
var q=[];
function flush(){var g=window.mygo;if(!g||!g.call)return false;while(q.length){g.call("IRApp.Invoke",q.shift()).catch(function(){})}return true}
function wait(){if(!flush())setTimeout(wait,50)}
window._wails={invoke:function(m){m=String(m);if(m.indexOf("ir:")!==0)return;q.push(m);wait()}};
function fire(){window.dispatchEvent(new Event("wails:runtime-config-ready"))}
if(document.readyState==="loading")document.addEventListener("DOMContentLoaded",fire);else fire();
})();` + css(`[data-app-drag]{--app-region:drag}`)

// css is JavaScript that puts style in the page as it starts. WebView2 runs the preload before the
// document has its <html> (WebKit after): appended to nothing it threw, and the rest of the preload
// after it never ran (QGI, 2026-11-23: on Windows no window dragged, and no page error was logged).
// So the style waits for the element there.
func css(style string) string {
	b, _ := json.Marshal(style)
	return `(function(){var s=document.createElement("style");s.textContent=` + string(b) +
		`;function put(){var p=document.head||document.documentElement;if(!p)return false;p.appendChild(s);return true}` +
		`if(!put()){var o=new MutationObserver(function(){if(put())o.disconnect()});o.observe(document,{childList:true})}})();`
}

// replies go to the pages in order, one at a time, off the main thread: a page's Eval waits for
// it, and the main thread must not.
var replies struct {
	once sync.Once
	ch   chan func()
}

// reply hands the page an "ir-app" event with v as its detail.
func reply(p *mygo.Page, v any) {
	if p == nil {
		return
	}
	b, _ := json.Marshal(v)
	js := fmt.Sprintf(`window.dispatchEvent(new CustomEvent("ir-app",{detail:%s}))`, b)
	replies.once.Do(func() {
		replies.ch = make(chan func(), 256)
		go func() {
			for f := range replies.ch {
				f()
			}
		}()
	})
	replies.ch <- func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = p.EvalContext(ctx, js)
	}
}

// nameOf is a page's name (picker, ask, w1…); "": not one of the app's.
func (u *ui) nameOf(p *mygo.Page) string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.names[p]
}

// named records p's name.
func (u *ui) named(p *mygo.Page, name string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.names == nil {
		u.names = map[*mygo.Page]string{}
	}
	u.names[p] = name
}

// pickFolder asks the person here for one folder, in this computer's own picker.
func pickFolder(title string) (string, error) {
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Title: title, Directory: true, CreateDirectories: true})
	if err != nil || len(paths) == 0 {
		return "", err
	}
	return paths[0], nil
}

// menuBar is the app's menu (see gui): on a Mac the menu bar's, elsewhere each window's, hidden.
func (u *ui) menuBar() *mygo.Menu {
	type item = mygo.MenuItem
	click := func(f func()) func(*mygo.MenuItem, *mygo.Window) {
		return func(*mygo.MenuItem, *mygo.Window) { f() }
	}
	// The page the keys are for: the one shown in the window a menu says, else in the one in front.
	on := func(f func(*mygo.Page)) func(*mygo.MenuItem, *mygo.Window) {
		return func(it *mygo.MenuItem, w *mygo.Window) {
			given := w != nil
			if w == nil {
				w = mygo.FocusedWindow()
			}
			p := u.pageIn(w)
			appLog()("key %s %s: page %q (given %v)", it.Label, it.Accelerator, u.nameOf(p), given)
			f(p)
		}
	}
	// ⌘W closes the page in front, the window only from its first page; ⌘R, F5 reload (chrome.go).
	closeItem := &item{Label: "关闭", Accelerator: "CmdOrCtrl+W", Click: on(u.closePage)}
	view := []*item{
		{Label: "重新载入", Accelerator: "CmdOrCtrl+R", Click: on(u.reload)},
		{Label: "重新载入", Accelerator: "F5", Hidden: true, Click: on(u.reload)},
		{Label: "开发者工具", Accelerator: "F12", Hidden: true, Click: on(devTools)},
		mygo.Separator(),
		{Label: "放大", Accelerator: "CmdOrCtrl+=", Click: click(func() { u.zoom(+1) })},
		{Label: "放大", Accelerator: "CmdOrCtrl+Plus", Hidden: true, Click: click(func() { u.zoom(+1) })},
		{Label: "缩小", Accelerator: "CmdOrCtrl+-", Click: click(func() { u.zoom(-1) })},
		{Label: "实际大小", Accelerator: "CmdOrCtrl+0", Click: click(func() { u.zoom(0) })},
	}
	// The rail's (mainwin.go): ⌘1–⌘9 its first nine (not hidden: GTK runs no hidden item's key);
	// ⌘, 本机与授权. Ctrl+Tab and Ctrl+Shift+Tab are the pages' (railKeysJS): GTK keeps them.
	servers := []*item{}
	for i := range 9 {
		servers = append(servers, &item{Label: fmt.Sprintf("第 %d 台", i+1), Accelerator: fmt.Sprintf("CmdOrCtrl+%d", i+1),
			Click: click(func() { go u.railPick(i) })})
	}
	view = append(view, mygo.Separator(), &item{Label: "服务器", Submenu: servers})
	if runtime.GOOS != "darwin" {
		file := []*item{
			{Label: "本机与授权", Accelerator: "CmdOrCtrl+,", Click: click(func() { go u.showHome("local") })},
			{Label: "添加服务器…", Accelerator: "CmdOrCtrl+N", Click: click(func() { go u.showHome("add") })},
			closeItem,
		}
		return mygo.NewMenu([]*item{{Label: "文件", Submenu: file}, {Label: "显示", Submenu: view}})
	}
	view = append(view, mygo.Separator(), &item{Role: mygo.RoleToggleFullScreen})
	return mygo.NewMenu([]*item{
		{Label: "Iridium", Submenu: []*item{
			{Role: mygo.RoleAbout}, mygo.Separator(),
			{Label: "本机与授权…", Accelerator: "CmdOrCtrl+,", Click: click(func() { go u.showHome("local") })}, mygo.Separator(),
			{Role: mygo.RoleHide}, {Role: mygo.RoleHideOthers}, {Role: mygo.RoleUnhide}, mygo.Separator(),
			{Role: mygo.RoleQuit},
		}},
		{Label: "文件", Submenu: []*item{
			{Label: "添加服务器…", Accelerator: "CmdOrCtrl+N", Click: click(func() { go u.showHome("add") })},
			closeItem,
		}},
		{Role: mygo.RoleEditMenu},
		{Label: "显示", Submenu: view},
		{Role: mygo.RoleWindowMenu},
	})
}

// devTools opens or closes the web inspector of p (F12).
func devTools(p *mygo.Page) {
	if p != nil {
		go p.ToggleDevTools()
	}
}

// zoom makes the window in front's page larger (+1), smaller (-1) or as it was (0), and keeps it
// for the next start (kumo, 2026-11-20).
func (u *ui) zoom(step int) {
	u.mu.Lock()
	w := u.win
	u.mu.Unlock()
	p := u.pageIn(w)
	if p == nil {
		return
	}
	go func() {
		z := p.ZoomFactor()
		switch {
		case step > 0:
			z = min(z+0.1, 3)
		case step < 0:
			z = max(z-0.1, 0.3)
		default:
			z = 1
		}
		p.SetZoomFactor(z)
		_, _ = updateConfig(func(c *Config) { c.Zoom = z })
	}()
}

// pageOptions is a window's page: the shim, and what else is put in it; trusted, besides the app's
// own pages, origin (an Iridium's window: that Iridium).
func pageOptions(origin, extra string, zoom float64) mygo.PageOptions {
	// The web inspector in every build, release ones too (mygo's production builds leave it out):
	// F12 opens it (kumo, 2026-11-26).
	o := mygo.PageOptions{PreloadScript: shim + extra, ZoomFactor: zoom, DevTools: mygo.DevToolsEnabled}
	if origin != "" {
		o.TrustedOrigins = []string{origin}
	}
	return o
}

// hideOnClose: w, asked to close, hides (its page keeps its state), unless the app quits.
func (u *ui) hideOnClose(w *mygo.Window) {
	w.OnClose(func(e *mygo.CloseEvent) {
		u.mu.Lock()
		quitting := u.quitting
		u.mu.Unlock()
		if quitting {
			return
		}
		e.PreventDefault()
		go w.Hide()
	})
}

// pageLog is the app's log (appLog), for what has no logf handed to it.
var pageLog = sync.OnceValue(appLog)

// guard sets up the page of a window on the Iridium at origin ("": the app's own pages):
//
//   - its title stays the window's (the Iridium's name here, retitle), not the page's <title>;
//   - a link to another site opens in the browser, the window staying on its Iridium (Wails let
//     the page go there, and did nothing for target=_blank: kumo, 2026-11-23);
//   - a download is saved in Downloads (one from its Iridium fetched by the app, its toast saying
//     how far: download.go), then shown in Finder or Explorer (Wails dropped them);
//   - the microphone and the camera are its own Iridium's (voice rooms), nothing else is; no
//     other page gets anything;
//   - its web process gone (killed by the system: WebKit's 8 GB a page, kumo, 2026-11-23, the
//     window white from then on, its keys dead with the page), the page is loaded again, and
//     the log says so. Not one gone again within a minute: that page does not stay up, and the
//     window is left as it is rather than loaded over and over.
func guard(p *mygo.Page, origin string) {
	var gone time.Time
	p.OnRenderProcessGone(func(reason string) {
		again := time.Since(gone) < time.Minute
		gone = time.Now()
		pageLog()("page %s: web process gone (%s), reload: %v", origin, reason, !again)
		if !again {
			p.Reload()
		}
	})
	p.OnPageTitleUpdated(func(e *mygo.TitleEvent) { e.PreventDefault() })
	p.OnWillNavigate(func(e *mygo.NavigateEvent) {
		o := originOf(e.URL)
		if !e.UserInitiated || o == "" || o == origin || ownPage(o) {
			return
		}
		if u, err := url.Parse(e.URL); err == nil && (u.Scheme == "http" || u.Scheme == "https") {
			e.PreventDefault()
			go func() { _ = mygo.Shell.OpenExternal(e.URL) }()
		}
	})
	// Each download in the log, its end too, and told to the page as it starts, goes and ends (its
	// toast: web/src/irapp.ts; the web view shows none, and a download went on unseen: QGI,
	// 2026-11-23). One from the window's own Iridium the app fetches itself (download.go), saying
	// how far it is; others are the web view's. One that failed goes to the system's browser, whose
	// own download asks what the app could not.
	tell := func(u, name, state string, pct int) {
		d := map[string]any{"id": u, "name": name, "state": state}
		if pct >= 0 {
			d["pct"] = pct
		}
		reply(p, map[string]any{"download": d})
	}
	failed := func(raw, name string, err error) {
		pageLog()("download %s failed: %v; in the browser instead", pathOf(raw), err)
		tell(raw, name, "failed", -1)
		if u, err := url.Parse(raw); err == nil && (u.Scheme == "http" || u.Scheme == "https") {
			go func() { _ = mygo.Shell.OpenExternal(raw) }()
		}
	}
	done := func(raw, path string) {
		pageLog()("download %s done", pathOf(raw))
		tell(raw, filepath.Base(path), "done", -1)
		go mygo.Shell.ShowItemInFolder(path)
	}
	p.OnWillDownload(func(e *mygo.DownloadEvent) {
		pageLog()("download %s → %s", pathOf(e.URL), e.Path)
		name := filepath.Base(e.Path)
		if origin == "" || e.Path == "" || originOf(e.URL) != origin {
			tell(e.URL, name, "start", -1)
			return
		}
		e.PreventDefault()
		raw, path := e.URL, e.Path
		tell(raw, name, "start", 0)
		go func() {
			var cs []*http.Cookie
			if list, err := mygo.Cookies.List(mygo.CookieFilter{URL: raw}); err == nil {
				for _, c := range list {
					cs = append(cs, &http.Cookie{Name: c.Name, Value: c.Value})
				}
			}
			err := fetch(context.Background(), raw, path, cs, func(got, total int64) {
				if total > 0 {
					tell(raw, name, "progress", int(got*100/total))
				}
			})
			if err != nil {
				failed(raw, name, err)
				return
			}
			done(raw, path)
		}()
	})
	p.OnDownloadDone(func(d *mygo.Download) {
		switch {
		case d.Err == nil && d.Path != "":
			done(d.URL, d.Path)
		case d.Err != nil:
			failed(d.URL, filepath.Base(d.Path), d.Err)
		default:
			tell(d.URL, filepath.Base(d.Path), "gone", -1)
		}
	})
	p.SetPermissionHandler(func(r mygo.PermissionRequest) bool {
		if origin == "" || r.Origin != origin {
			return false
		}
		for _, k := range r.Permissions {
			if k != mygo.PermissionMicrophone && k != mygo.PermissionCamera {
				return false
			}
		}
		return true
	})
}
