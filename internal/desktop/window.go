package desktop

import (
	"fmt"
	"os"
	"os/exec"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
)

// follow draws the tray again when what the runtime says changes (its joins' states, this
// computer's Iridium's), looked at every few seconds: the runtime is another process.
func (u *ui) follow() {
	last := ""
	for {
		time.Sleep(3 * time.Second)
		r, ok := RuntimeRecord()
		var b strings.Builder
		fmt.Fprint(&b, ok, r.Server)
		keys := make([]string, 0, len(r.Joins))
		for k := range r.Joins {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprint(&b, k, r.Joins[k])
		}
		if s := b.String(); s != last {
			last = s
			u.redraw()
		}
	}
}

// updateChanged: what the app's update shows changed (selfupdate.go): the tray and the list drawn
// again; the Iridiums' pages told only once one is ready or no longer is (they show nothing of a
// download).
func (u *ui) updateChanged() {
	u.redraw()
	ready := u.upd.view().Phase == "ready"
	u.mu.Lock()
	was := u.toldReady
	u.toldReady = ready
	type shown struct {
		p      *mygo.Page
		server string
	}
	var ps []shown
	for _, pane := range u.panes {
		ps = append(ps, shown{pane.page(), u.shows[pane.name]})
	}
	u.mu.Unlock()
	if ready == was {
		return
	}
	c, _ := loadConfig()
	for _, s := range ps {
		if s.server != "" && s.p != nil {
			reply(s.p, u.hello(c, s.server))
		}
	}
}

// restartApp starts the window again on the binary on disk and quits this one; the runtime is
// another process and runs on. mygo's Relaunch starts the new one once this one has ended (its
// single-instance lock), as IRIDIUM_WAIT_PID did; on a Mac through Launch Services, the app's
// bundle as updated.
func (u *ui) restartApp() { mygo.App.Relaunch() }

// syncRuntime keeps ir's service as the settings need it (kumo, 2026-11-22): started once
// something needs it (an Iridium joined here; this computer's own is started by startLocal),
// taken away once nothing does, the window then only showing others. Looked at as the settings
// change.
func (u *ui) syncRuntime(logf func(string, ...any)) {
	var seen time.Time
	for {
		if p, err := configPath(); err == nil {
			if t := modTime(p); !t.Equal(seen) {
				seen = t
				_, running := RuntimeRecord()
				c, _ := loadConfig()
				need := Needed()
				self, err := os.Executable()
				switch {
				case err != nil:
				case need && !running && !c.serverOn():
					cmd := exec.Command(self, "start", "--headless", "--as-is")
					quiet(cmd)
					out, err := cmd.CombinedOutput()
					logf("runtime: ir start --headless --as-is: %v\n%s", err, out)
				case !need && running:
					cmd := exec.Command(self, "stop")
					quiet(cmd)
					out, err := cmd.CombinedOutput()
					logf("runtime: ir stop: %v\n%s", err, out)
				}
			}
		}
		time.Sleep(2 * time.Second)
	}
}

// keepFrame: a window opens where and as large as it was last (the main window's Config.Frame, an
// Iridium's own Config.Away), given as it is made; keep is given that as w moves and resizes.
func (u *ui) keepFrame(w *mygo.Window, keep func(Frame)) {
	var mu sync.Mutex
	var t *time.Timer
	save := func() {
		mu.Lock()
		defer mu.Unlock()
		if t != nil {
			t.Stop()
		}
		t = time.AfterFunc(700*time.Millisecond, func() {
			u.mu.Lock()
			here := slices.Contains(u.wins, w)
			u.mu.Unlock()
			if !here || w.IsMaximized() || w.IsFullScreen() || w.IsMinimized() {
				return
			}
			x, y := w.Position()
			ww, h := w.Size()
			keep(Frame{X: x, Y: y, W: ww, H: h})
		})
	}
	w.OnMove(save)
	w.OnResize(save)
}
