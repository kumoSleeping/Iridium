package discover

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/store"
)

// classify puts each session shown for the first time in a folder group (a workspace; the
// user, 2026-11-08: a group is only a folder), once: its launcher's, if the group's folder holds
// it or it is the launcher's subagent; else the group of the deepest folder holding it; else a new group of its own folder. The
// home folder and / hold only sessions right in them, so one session there does not swallow every
// project. Hidden ones (the archive) wait for their first activity. Oldest first, so a launcher
// seen in the same look is put somewhere before the sessions it started. A session in none
// (made before groups were folders, or its group deleted) is put in one too, unless you moved it
// there.
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
		for _, w := range ws {
			roots[w.ID] = w.Path
		}
	}
	type fresh struct {
		id, cwd, launcher string
		sub               bool
		at                time.Time
	}
	var news []fresh
	x.mu.Lock()
	for id, e := range x.entries {
		a, ok := assigned[id]
		if (!ok || a.Workspace == 0 && !a.Manual) && e.cwd != "" && vis.Shown(string(e.agent), id) {
			news = append(news, fresh{id, e.cwd, x.launcherOf(e), e.sub, e.mtime})
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
			ws = x.groupOf(n.cwd, roots)
		}
		if ws == 0 {
			w, err := x.groupFor(n.cwd)
			if err != nil {
				x.o.Logf("discover: a group for %s: %v", n.cwd, err)
				continue
			}
			ws, roots[w.ID] = w.ID, w.Path
		}
		ok, err := x.o.Store.AssignAuto(n.id, ws)
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

// holds: a group of folder root holds a session in cwd. The home folder and / hold only their own.
func (x *Index) holds(root, cwd string) bool {
	if root == "" || cwd == "" {
		return false
	}
	if root == "/" || root == x.o.Home {
		return cwd == root
	}
	return within(cwd, root)
}

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

// GroupFor is the group a session in cwd goes in as it is made (the session manager, before its
// transcript is there): prefer (its caller's) if it holds cwd; else the deepest holding it; else a
// new group of cwd. 0 if it cannot be had.
func (x *Index) GroupFor(cwd string, prefer int64) int64 {
	ws, err := x.o.Store.Workspaces()
	if err != nil {
		x.o.Logf("discover: %v", err)
		return 0
	}
	roots := map[int64]string{}
	for _, w := range ws {
		roots[w.ID] = w.Path
	}
	if prefer != 0 && x.holds(roots[prefer], cwd) {
		return prefer
	}
	if id := x.groupOf(cwd, roots); id != 0 {
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
	if p.Name != nil {
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
	x.Changed(id)
	return nil
}
