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

// classify puts each session never seen before in a workspace, once: its launcher's; else the
// one holding a session of its folder or of the nearest folder above it; else none. Oldest
// first, so a launcher seen in the same look is put somewhere before the sessions it started.
func (x *Index) classify() {
	assigned, err := x.o.Store.Assignments()
	if err != nil {
		x.o.Logf("discover: %v", err)
		return
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
		if _, ok := assigned[id]; !ok {
			news = append(news, fresh{id, e.cwd, x.launchers[id], e.mtime})
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
			ws = nearest(n.cwd, cwds, assigned)
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

// nearest is the workspace of the session whose folder is cwd, or the nearest folder above it;
// 0 if none is in one.
func nearest(cwd string, cwds map[string]string, assigned map[string]store.Assignment) int64 {
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
// is the folder's name. The sessions of that folder and below that are in none and were never
// moved by the user go in it.
func (x *Index) CreateWorkspace(path, name string) (api.Workspace, error) {
	path = filepath.Clean(path)
	if name = oneLine(name); name == "" {
		name = filepath.Base(path)
	}
	id, err := x.o.Store.CreateWorkspace(name, path)
	if err != nil {
		return api.Workspace{}, err
	}
	var ids []string
	x.mu.Lock()
	for sid, e := range x.entries {
		if e.cwd != "" && within(e.cwd, path) {
			ids = append(ids, sid)
		}
	}
	x.mu.Unlock()
	if _, err := x.o.Store.Adopt(id, ids); err != nil {
		return api.Workspace{}, err
	}
	x.Changed(ids...)
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

// Patch changes what lies over session id: its name, pin, archive, workspace. A move sticks.
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
	if p.Archived != nil {
		if err := st.SetArchived(id, *p.Archived); err != nil {
			return err
		}
	}
	x.Changed(id)
	return nil
}
