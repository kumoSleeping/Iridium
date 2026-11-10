package discover

import (
	"errors"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/remote"
	"github.com/kumoSleeping/iridium/internal/store"
	"github.com/kumoSleeping/iridium/internal/transcript"
)

// classify puts each session shown for the first time in a folder group (a workspace; the
// user, 2026-11-08: a group is only a folder), once: its launcher's, if the group's folder holds
// it or it is the launcher's subagent; else the group of the outermost folder holding it; else a new group of its own folder. The
// home folder and / hold only sessions right in them, so one session there does not swallow every
// project. Hidden ones (the archive) wait for their first activity. Oldest first, so a launcher
// seen in the same look is put somewhere before the sessions it started. A session in none
// (made before groups were folders, or its group deleted) is put in one too, unless you moved it
// there; so is one discovery put in a group of another machine (its executor paired again, a new
// terminal ID: its sessions came back under it; or put with its caller's across machines, as once
// was: the user, 2026-11-10). A dsh session in none of DSH Desktop's workspaces there (loose: its
// 未分组) stays in none, not listed (the user, 2026-11-10). Then groups inside another's folder go
// into it (nest).
func (x *Index) classify() {
	assigned, err := x.o.Store.Assignments()
	if err != nil {
		x.o.Logf("discover: %v", err)
		return
	}
	vis, err := x.o.Store.Visibility()
	if err != nil {
		x.o.Logf("discover: %v", err)
		return
	}
	roots := map[int64]string{}
	if ws, err := x.o.Store.Workspaces(); err == nil {
		roots = x.rootsOf(ws)
	}
	defer x.nest(roots, assigned)
	type fresh struct {
		id, cwd, launcher string
		sub               bool
		at                time.Time
		from              int64 // the group of another machine it is in; 0: in none
	}
	var news []fresh
	x.mu.Lock()
	for id, e := range x.entries {
		if x.loose(e) {
			continue
		}
		a, ok := assigned[id]
		// Put by discovery in a group of another machine.
		root := roots[a.Workspace]
		stray := ok && !a.Manual && a.Workspace != 0 && root != "" && machineOf(root) != e.machine
		if (!ok || a.Workspace == 0 && !a.Manual || stray) && e.cwd != "" && vis.Shown(string(e.agent), id) {
			n := fresh{id: id, cwd: remote.GroupPath(e.machine, e.cwd), launcher: x.launcherOf(e), sub: e.sub, at: e.mtime}
			if stray {
				n.from = a.Workspace
			}
			news = append(news, n)
		}
	}
	x.mu.Unlock()
	slices.SortFunc(news, func(a, b fresh) int { return a.at.Compare(b.at) })
	for _, n := range news {
		ws := int64(0)
		// A subagent goes with its parent wherever it works.
		if a, ok := assigned[n.launcher]; ok && n.launcher != "" && a.Workspace != 0 && (n.sub || x.holds(roots[a.Workspace], n.cwd)) {
			ws = a.Workspace
		}
		if ws == 0 {
			ws = x.outer(x.groupOf(n.cwd, roots), roots)
		}
		if ws == 0 {
			w, err := x.groupFor(n.cwd)
			if err != nil {
				x.o.Logf("discover: a group for %s: %v", n.cwd, err)
				continue
			}
			ws, roots[w.ID] = w.ID, w.Path
		}
		var ok bool
		if n.from != 0 {
			ok, err = x.o.Store.Regroup(n.id, n.from, ws)
		} else {
			ok, err = x.o.Store.AssignAuto(n.id, ws)
		}
		if err != nil {
			x.o.Logf("discover: %v", err)
			return
		}
		if ok {
			assigned[n.id] = store.Assignment{Workspace: ws}
			x.Changed(n.id)
		}
	}
}

// holds: a group of folder root holds a session in cwd. The home folder and / hold only their own;
// so does another machine's home, or a drive's root there.
func (x *Index) holds(root, cwd string) bool {
	if root == "" || cwd == "" {
		return false
	}
	if root == "/" || root == x.o.Home || x.remoteTop(root) || x.isFlat(root) {
		return cwd == root
	}
	return within(cwd, root)
}

// rootsOf is each group's folder, by ID; it notes those split, for holds.
func (x *Index) rootsOf(ws []store.Workspace) map[int64]string {
	roots, flat := map[int64]string{}, map[string]bool{}
	for _, w := range ws {
		roots[w.ID] = w.Path
		if w.Flat {
			flat[w.Path] = true
		}
	}
	x.flatMu.Lock()
	x.flat = flat
	x.flatMu.Unlock()
	return roots
}

// isFlat: root is the folder of a group split, which holds only its own (SplitWorkspace).
func (x *Index) isFlat(root string) bool {
	x.flatMu.Lock()
	defer x.flatMu.Unlock()
	return x.flat[root]
}

// SplitWorkspace takes a group apart at its folder (the user, 2026-11-09): it keeps only the
// sessions right in its folder; those in its subfolders are grouped again by their folders, each
// a group of its own (a subfolder's in its parent's, as ever). It is not made whole again by
// discovery: a split group holds only its own folder's.
func (x *Index) SplitWorkspace(id int64) error {
	x.scanMu.Lock()
	defer x.scanMu.Unlock()
	w, err := x.o.Store.Workspace(id)
	if err != nil {
		return err
	}
	assigned, err := x.o.Store.Assignments()
	if err != nil {
		return err
	}
	var out []string
	x.mu.Lock()
	for sid, a := range assigned {
		if a.Workspace != id {
			continue
		}
		var p string
		if e := x.entries[sid]; e != nil {
			p = remote.GroupPath(e.machine, e.cwd)
		}
		if p != "" && p != w.Path {
			out = append(out, sid)
		}
	}
	x.mu.Unlock()
	if err := x.o.Store.SplitWorkspace(id, out); err != nil {
		return err
	}
	x.classify()
	x.Changed(out...)
	x.Changed()
	return nil
}

// remoteTop: root ("<machine>:<path>") is that machine's home or a root of its file system.
func (x *Index) remoteTop(root string) bool {
	machine, p, ok := strings.Cut(root, ":")
	if !ok || strings.HasPrefix(root, "/") {
		return false
	}
	if p == "/" || len(p) <= 3 && strings.HasSuffix(strings.TrimSuffix(p, "/"), ":") { // "/", "C:", "C:/"
		return true
	}
	if x.hub != nil {
		if mc := x.hub.Find(machine); mc != nil {
			if hi, _ := mc.Hello(); hi != nil && hi.Home != "" {
				return root == remote.GroupPath(machine, hi.Home)
			}
		}
	}
	// Not connected now: a folder where homes are.
	return homeLike.MatchString(p)
}

// homeLike: a home folder on Windows, macOS or Linux, '/'-separated.
var homeLike = regexp.MustCompile(`^(?:[A-Za-z]:/Users/[^/]+|/Users/[^/]+|/home/[^/]+|/root)/?$`)

// groupOf is the group of the deepest folder holding cwd; 0 if none does.
func (x *Index) groupOf(cwd string, roots map[int64]string) int64 {
	best, depth := int64(0), -1
	for ws, root := range roots {
		if x.holds(root, cwd) && len(root) > depth {
			best, depth = ws, len(root)
		}
	}
	return best
}

// outer is the outermost group holding group id's folder: a subfolder's sessions are a folder in
// its parent's group (the user, 2026-11-08). id itself if none does.
func (x *Index) outer(id int64, roots map[int64]string) int64 {
	if id == 0 {
		return id
	}
	best := id
	for ws, root := range roots {
		if len(root) < len(roots[best]) && x.holds(root, roots[id]) {
			best = ws
		}
	}
	return best
}

// nest puts each group inside another group's folder into the outermost such: a group is
// made for the first session seen in its folder, oldest first, so one of a subfolder may be made
// before its parent's (an executor's sessions all seen at once). Sessions you moved stay, with
// their group; one left empty is dropped.
func (x *Index) nest(roots map[int64]string, assigned map[string]store.Assignment) {
	held := map[int64]bool{} // groups a session you moved is in
	for _, a := range assigned {
		if a.Manual {
			held[a.Workspace] = true
		}
	}
	for id := range roots {
		to := x.outer(id, roots)
		if to == id {
			continue
		}
		auto := false
		for _, a := range assigned {
			if a.Workspace == id && !a.Manual {
				auto = true
				break
			}
		}
		if !auto && held[id] {
			continue
		}
		moved, dropped, err := x.o.Store.Nest(id, to)
		if err != nil {
			x.o.Logf("discover: %v", err)
			return
		}
		for _, s := range moved {
			assigned[s] = store.Assignment{Workspace: to}
		}
		if len(moved) > 0 {
			x.Changed(moved...)
		}
		if dropped {
			delete(roots, id)
			x.Changed()
		}
	}
}

// GroupFor is the group a session in cwd goes in as it is made (the session manager, before its
// transcript is there): prefer (its caller's) if it holds cwd; else the outermost holding it; else
// a new group of cwd. 0 if it cannot be had. One started from a session at another machine is
// grouped by its own folder there; the page hangs it under its launcher (the user, 2026-11-10).
func (x *Index) GroupFor(cwd string, prefer int64) int64 {
	ws, err := x.o.Store.Workspaces()
	if err != nil {
		x.o.Logf("discover: %v", err)
		return 0
	}
	roots := x.rootsOf(ws)
	if prefer != 0 && x.holds(roots[prefer], cwd) {
		return prefer
	}
	if id := x.outer(x.groupOf(cwd, roots), roots); id != 0 {
		return id
	}
	w, err := x.groupFor(cwd)
	if err != nil {
		x.o.Logf("discover: a group for %s: %v", cwd, err)
		return 0
	}
	return w.ID
}

// groupFor makes the group of folder cwd, named as the folder; the one there if it is.
func (x *Index) groupFor(cwd string) (store.Workspace, error) {
	cwd = filepath.Clean(cwd)
	id, err := x.o.Store.CreateWorkspace(oneLine(filepath.Base(cwd)), cwd)
	if errors.Is(err, store.ErrWorkspacePath) {
		ws, err := x.o.Store.Workspaces()
		if err != nil {
			return store.Workspace{}, err
		}
		for _, w := range ws {
			if w.Path == cwd {
				return w, nil
			}
		}
		return store.Workspace{}, store.ErrWorkspacePath
	}
	if err != nil {
		return store.Workspace{}, err
	}
	x.Changed()
	return store.Workspace{ID: id, Name: filepath.Base(cwd), Path: cwd}, nil
}

// within: path is dir or below it.
func within(path, dir string) bool {
	if path == dir {
		return true
	}
	if dir == "/" {
		return strings.HasPrefix(path, "/")
	}
	return strings.HasPrefix(path, dir+"/")
}

// ---- workspaces ----

func (x *Index) Workspaces() ([]api.Workspace, error) {
	ws, err := x.o.Store.Workspaces()
	if err != nil {
		return nil, err
	}
	out := make([]api.Workspace, 0, len(ws))
	for _, w := range ws {
		out = append(out, api.Workspace{ID: w.ID, Name: w.Name, Path: w.Path})
	}
	return out, nil
}

// CreateWorkspace makes the group of folder path (absolute); name "" is the folder's name. It
// takes no session already grouped. Sessions make their groups (classify, GroupFor); this is for
// tests and a group made ahead.
func (x *Index) CreateWorkspace(path, name string) (api.Workspace, error) {
	path = filepath.Clean(path)
	if name = oneLine(name); name == "" {
		name = filepath.Base(path)
	}
	id, err := x.o.Store.CreateWorkspace(name, path)
	if err != nil {
		return api.Workspace{}, err
	}
	x.Changed()
	return api.Workspace{ID: id, Name: name, Path: path}, nil
}

func (x *Index) RenameWorkspace(id int64, name string) error {
	if name = oneLine(name); name == "" {
		return errors.New("a workspace needs a name")
	}
	if err := x.o.Store.RenameWorkspace(id, name); err != nil {
		return err
	}
	x.Changed()
	return nil
}

// OrderGroups lists the groups' rows of keys in that order, dragged so on the page, and carries it.
func (x *Index) OrderGroups(keys []string) error {
	if len(keys) > 10000 {
		return errors.New("too many groups")
	}
	if err := x.o.Store.SetGroupOrder(keys); err != nil {
		return err
	}
	x.publishPlaces()
	return nil
}

// Places is where the rows of the list were put by hand: the carried state "places".
func (x *Index) Places() (api.Places, error) {
	st := x.o.Store
	groups, err := st.GroupOrder()
	if err != nil {
		return api.Places{}, err
	}
	order, err := st.FolderOrder()
	if err != nil {
		return api.Places{}, err
	}
	pinned, err := st.FolderPins()
	if groups == nil {
		groups = []string{}
	}
	if pinned == nil {
		pinned = []string{}
	}
	return api.Places{Groups: groups, Order: order, Pinned: pinned}, err
}

// PlaceFolder sets where the folder of p.Path is, dragged or pinned so on the page, and carries it.
func (x *Index) PlaceFolder(p api.FolderPlace) error {
	if p.Path == "" || len(p.Path) > 4096 {
		return errors.New("no folder path")
	}
	if p.Order != nil {
		if err := x.o.Store.SetFolderOrder(p.Path, *p.Order); err != nil {
			return err
		}
	}
	if p.Pinned != nil {
		if err := x.o.Store.SetFolderPinned(p.Path, *p.Pinned); err != nil {
			return err
		}
	}
	x.publishPlaces()
	return nil
}

func (x *Index) publishPlaces() {
	if x.o.Hub == nil {
		return
	}
	p, err := x.Places()
	if err != nil {
		x.o.Logf("discover: places: %v", err)
		return
	}
	x.o.Hub.Set("places", p)
}

// DeleteWorkspace drops a group; its sessions are grouped again by their folders (classify).
func (x *Index) DeleteWorkspace(id int64) error {
	before, err := x.o.Store.Assignments()
	if err != nil {
		return err
	}
	if err := x.o.Store.DeleteWorkspace(id); err != nil {
		return err
	}
	var ids []string
	for sid, a := range before {
		if a.Workspace == id {
			ids = append(ids, sid)
		}
	}
	x.Changed(ids...)
	x.Changed()
	return nil
}

// ErrNoName: an agent's name for its session that is nothing once cleaned.
var ErrNoName = errors.New("no name")

// Patch changes what lies over session id: its name, pin, colour, workspace (archiving is the session
// manager's: it is the shown marker's, and stops a hosted agent). A move sticks.
func (x *Index) Patch(id string, p api.SessionPatch) error {
	st := x.o.Store
	if p.Color != nil && !validColor(*p.Color) {
		return ErrColor
	}
	if p.Workspace != nil {
		if err := st.Move(id, *p.Workspace); err != nil {
			return err
		}
	}
	if p.Name != nil && p.ByAgent {
		name := transcript.CleanName(*p.Name)
		if name == "" {
			return ErrNoName
		}
		if err := st.SetAgentName(id, name); err != nil {
			return err
		}
	} else if p.Name != nil {
		if err := st.SetName(id, oneLine(*p.Name)); err != nil {
			return err
		}
	}
	if p.Pinned != nil {
		if err := st.SetPinned(id, *p.Pinned); err != nil {
			return err
		}
	}
	if p.Color != nil {
		if err := st.SetColor(id, string(*p.Color)); err != nil {
			return err
		}
	}
	if p.Order != nil {
		if err := st.SetOrder(id, *p.Order); err != nil {
			return err
		}
	}
	x.Changed(id)
	return nil
}

// machineOf is the machine of a group's path: "" for this one's (absolute here), else the
// "<machine>" of "<machine>:<path>" (remote.GroupPath).
func machineOf(path string) string {
	if strings.HasPrefix(path, "/") {
		return ""
	}
	m, _, _ := strings.Cut(path, ":")
	return m
}
