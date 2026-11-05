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

// classify puts each session shown for the first time in a workspace, once: its launcher's;
// else the one holding a session of its folder or of the nearest folder above it, or whose own
// folder that is; else none. Hidden ones (the archive) wait for their first activity. Oldest
// first, so a launcher seen in the same look is put somewhere before the sessions it started.
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
		at                time.Time
	}
	var news []fresh
	cwds := map[string]string{}
	x.mu.Lock()
	for id, e := range x.entries {
		cwds[id] = e.cwd
		if _, ok := assigned[id]; !ok && vis.Shown(string(e.agent), id) {
			news = append(news, fresh{id, e.cwd, x.launcherOf(e), e.mtime})
		}
	}
	x.mu.Unlock()
	slices.SortFunc(news, func(a, b fresh) int { return a.at.Compare(b.at) })
	for _, n := range news {
		ws := int64(0)
		if a, ok := assigned[n.launcher]; ok && n.launcher != "" {
			ws = a.Workspace
		}
		if ws == 0 {
			ws = nearest(n.cwd, cwds, assigned, roots)
		}
		ok, err := x.o.Store.AssignIfNew(n.id, ws)
		if err != nil {
			x.o.Logf("discover: %v", err)
			return
		}
		if ok {
			assigned[n.id] = store.Assignment{Workspace: ws}
			if ws != 0 {
				x.Changed(n.id)
			}
		}
	}
}

// nearest is the workspace of the session whose folder is cwd, or the nearest folder above it,
// a workspace's own folder (roots) counting as one of its sessions'; 0 if none is in one.
func nearest(cwd string, cwds map[string]string, assigned map[string]store.Assignment, roots map[int64]string) int64 {
	if cwd == "" {
		return 0
	}
	best, depth := int64(0), -1
	for id, c := range cwds {
		a := assigned[id]
		if a.Workspace == 0 || c == "" || !within(cwd, c) {
			continue
		}
		if len(c) > depth {
			best, depth = a.Workspace, len(c)
		}
	}
	for ws, root := range roots {
		if root != "" && within(cwd, root) && len(root) > depth {
			best, depth = ws, len(root)
		}
	}
	return best
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

// CreateWorkspace makes a workspace of folder path (absolute, checked by the caller); name ""
// is the folder's name. It takes no session already classified: those shown from now on in its
// folder go in it by the first-sight rule (classify), as the user decided (2026-11-05).
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

// DeleteWorkspace drops a workspace; its sessions go to none (未归类).
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

// Patch changes what lies over session id: its name, pin, workspace (archiving is the session
// manager's: it is the shown marker's, and stops a hosted agent). A move sticks.
func (x *Index) Patch(id string, p api.SessionPatch) error {
	st := x.o.Store
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
	x.Changed(id)
	return nil
}
