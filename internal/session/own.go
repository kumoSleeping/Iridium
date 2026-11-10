package session

import (
	"context"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/store"
)

// Who opened a session, and where the list has it (docs/exec.md "远端工作区", the user,
// 2026-11-10): one a person opened is Own; one an agent started (ir new) hangs under its launcher
// on the page; a group at another machine is listed only while it holds an Own session not
// archived. What is not listed is not pushed either.

// ownOf: a person opened s (api.Session.Own), o what lies over it. One with a launcher only once
// taken out from under it (put by hand, Detach); one without, if Iridium opened it through a
// create (the page's "+", ir new in a shell, a Bot's, /clear: an opener noted). A subagent never.
func ownOf(s api.Session, o store.Overlay) bool {
	switch {
	case s.Subagent:
		return false
	case s.Launcher != "":
		return o.Manual
	}
	return s.Hosted && o.Opened
}

// hungUnder is the sessions hung under each launcher in list: started by it, not subagents, not
// taken out from under it.
func hungUnder(list []api.Session) map[string][]api.Session {
	out := map[string][]api.Session{}
	for _, s := range list {
		if s.Launcher != "" && !s.Subagent && !s.Own {
			out[s.Launcher] = append(out[s.Launcher], s)
		}
	}
	return out
}

// standing is where the sessions not archived stand in the list, as the page's tree.ts standing
// has it (the same rules, the same fixtures: testdata/standing.json). rows: listed on their own;
// hung: the dispatched ones (a launcher, not own) by the topmost listed one they came from; own:
// opened by a person as the page takes it (Own, or one whose launcher is not listed: archived,
// gone, a subagent, in a cycle, in a group not listed); groups: the groups listed.
type standing struct {
	rows   map[string]bool
	hung   map[string][]string
	own    map[string]bool
	groups map[int64]bool
}

// standOf is standing of list; roots: each group's folder, by ID (nil: unknown, a group at another
// machine by its sessions' machine), a session in none known in none.
func standOf(list []api.Session, roots map[int64]string) standing {
	var live []*api.Session
	byID := map[string]*api.Session{}
	for i := range list {
		if s := &list[i]; !s.Subagent && !s.Archived {
			live = append(live, s)
			byID[s.ID] = s
		}
	}
	wsOf := func(s *api.Session) int64 {
		if _, ok := roots[s.Workspace]; roots != nil && !ok {
			return 0
		}
		return s.Workspace
	}
	remote := func(s *api.Session) bool {
		if id := wsOf(s); id != 0 && roots != nil {
			return roots[id] != "" && !strings.HasPrefix(roots[id], "/")
		}
		return s.Machine != ""
	}
	st := standing{rows: map[string]bool{}, hung: map[string][]string{}, own: map[string]bool{}, groups: map[int64]bool{}}
	// Started by another (its ir new), not taken out from under it by hand, nor taken as own here.
	dispatched := func(s *api.Session) bool { return s.Launcher != "" && !s.Own && !st.own[s.ID] }
	// The topmost one a chain of launchers goes up to: the first not dispatched, or whose launcher
	// is not listed (archived, gone, a subagent); in a cycle, its least ID.
	top := map[string]string{}
	tops := func() {
		clear(top)
		for _, s := range live {
			var path []*api.Session
			seen := map[string]int{}
			cur := s
			var t string
			for {
				if known, ok := top[cur.ID]; ok {
					t = known
					break
				}
				if at, ok := seen[cur.ID]; ok {
					t = cur.ID
					for _, x := range path[at:] {
						t = min(t, x.ID)
					}
					break
				}
				seen[cur.ID] = len(path)
				path = append(path, cur)
				var up *api.Session
				if dispatched(cur) {
					up = byID[cur.Launcher]
				}
				if up == nil {
					t = cur.ID
					break
				}
				cur = up
			}
			for _, x := range path {
				top[x.ID] = t
			}
		}
	}
	tops()
	isTop := func(s *api.Session) bool { return top[s.ID] == s.ID }
	for _, s := range live {
		if isTop(s) && (s.Own || dispatched(s)) {
			st.own[s.ID] = true
		}
	}
	// A group at another machine listed while it holds an own one, or one of this machine's.
	count := func(s *api.Session) {
		if id := wsOf(s); id != 0 && (!remote(s) || s.Machine == "" || st.own[s.ID]) {
			st.groups[id] = true
		}
	}
	for _, s := range live {
		if isTop(s) {
			count(s)
		}
	}
	shown := func(s *api.Session) bool {
		if !remote(s) {
			return true
		}
		if id := wsOf(s); id != 0 {
			return st.groups[id]
		}
		return st.own[s.ID]
	}
	// Those whose launcher is not listed: one in its launcher's group lists that group, and so
	// hangs under it after all; the others, their launcher still not listed then, are as a person
	// opened them, their groups listed for them. Decided all at once, as the page does.
	var orphans, taken []*api.Session
	for _, s := range live {
		if up := byID[s.Launcher]; !isTop(s) && up != nil && isTop(up) && !shown(up) {
			orphans = append(orphans, s)
		}
	}
	for _, s := range orphans {
		if id := wsOf(s); id != 0 && id == wsOf(byID[s.Launcher]) {
			st.groups[id] = true
		}
	}
	for _, s := range orphans {
		if !shown(byID[s.Launcher]) {
			taken = append(taken, s)
		}
	}
	for _, s := range taken {
		st.own[s.ID] = true
	}
	for _, s := range taken {
		count(s)
	}
	if len(taken) > 0 {
		tops()
	}
	for _, s := range live {
		switch t := byID[top[s.ID]]; {
		case s == t:
			if shown(s) {
				st.rows[s.ID] = true
			}
		case shown(t):
			st.hung[t.ID] = append(st.hung[t.ID], s.ID)
		case shown(s):
			st.rows[s.ID] = true
		}
	}
	return st
}

// listed says whether the page lists session id of list (standOf; roots as there): one listed on
// its own or hung under another; an archived one at this machine, or own; a subagent as its
// parent. One not in list is.
func listed(list []api.Session, roots map[int64]string, id string) bool {
	byID := make(map[string]*api.Session, len(list))
	for i := range list {
		byID[list[i].ID] = &list[i]
	}
	var st *standing
	seen := map[string]bool{}
	for {
		s := byID[id]
		switch {
		case s == nil:
			return true
		case seen[id]:
			return false
		case s.Subagent:
			if s.Parent == "" {
				return s.Machine == ""
			}
			seen[id] = true
			id = s.Parent
			continue
		case s.Archived:
			return s.Machine == "" || s.Own
		}
		if st == nil {
			x := standOf(list, roots)
			st = &x
		}
		if st.rows[id] {
			return true
		}
		for _, h := range st.hung {
			if slices.Contains(h, id) {
				return true
			}
		}
		return false
	}
}

// Listed says whether the page lists session id: what is not is not pushed, nor its asks sent to
// a Bot. True when the list cannot be had.
func (m *Manager) Listed(id string) bool {
	l, err := m.List(m.ctx)
	if err != nil {
		return true
	}
	if h := m.byAgentID(id); h != nil {
		id = h.id
	}
	var roots map[int64]string
	if ws, err := m.o.Store.Workspaces(); err == nil {
		roots = make(map[int64]string, len(ws))
		for _, w := range ws {
			roots[w.ID] = w.Path
		}
	}
	return listed(l, roots, id)
}

// ArchiveTree archives session id (SetArchived) and, all the way down, the sessions hung under
// it (the user, 2026-11-10: they go to the archive with their launcher, and come back with it);
// their agents stop as an archived session's do. Those taken out from under it stay as they are.
func (m *Manager) ArchiveTree(ctx context.Context, id string, on bool) error {
	l, err := m.List(ctx)
	if err != nil {
		return err
	}
	if h := m.byAgentID(id); h != nil {
		id = h.id
	}
	if err := m.SetArchived(ctx, id, on); err != nil {
		return err
	}
	under := hungUnder(l)
	seen := map[string]bool{id: true}
	next := []string{id}
	for len(next) > 0 {
		at := next[0]
		next = next[1:]
		for _, c := range under[at] {
			if seen[c.ID] {
				continue
			}
			seen[c.ID] = true
			next = append(next, c.ID)
			if c.Archived == on {
				continue
			}
			if err := m.SetArchived(ctx, c.ID, on); err != nil {
				m.o.Logf("session %s: archived with %s (%v): %v", c.ID, id, on, err)
			}
		}
	}
	return nil
}

// Detach takes session id out from under its launcher (SessionPatch.Detach): it goes in its own
// folder's group (the outermost holding its folder, made if there is none, as discovery groups),
// put there by hand, so it is Own from now on and never hangs under it again.
func (m *Manager) Detach(ctx context.Context, id string) error {
	l, err := m.List(ctx)
	if err != nil {
		return err
	}
	if h := m.byAgentID(id); h != nil {
		id = h.id
	}
	var s *api.Session
	for i := range l {
		if l[i].ID == id {
			s = &l[i]
			break
		}
	}
	if s == nil {
		return ErrNotFound
	}
	ws := s.Workspace
	if g, ok := m.o.Discovery.(grouper); ok && s.Cwd != "" {
		if w := g.GroupFor(groupPath(s.Machine, s.Cwd), 0); w != 0 {
			ws = w
		}
	}
	if err := m.o.Store.Move(s.ID, ws); err != nil {
		return err
	}
	m.Refresh([]string{s.ID})
	return nil
}

// splitter is a Discovery that splits a group at its folder (discover.Index).
type splitter interface {
	SplitWorkspace(id int64) error
}

// SplitWorkspace splits group ws at its folder, as discovery does: the sessions in its subfolders
// go back to discovery, put there by hand no more. Those among them taken out from under their
// launcher (Detach: put by hand is what keeps them out, ownOf) go in their own folders' groups by
// hand again, so they never hang under it again.
func (m *Manager) SplitWorkspace(ctx context.Context, ws int64) error {
	d, ok := m.o.Discovery.(splitter)
	if !ok {
		return store.ErrNoWorkspace
	}
	l, err := m.List(ctx)
	if err != nil {
		return err
	}
	if err := d.SplitWorkspace(ws); err != nil {
		return err
	}
	for _, s := range l {
		if s.Workspace == ws && s.Own && s.Launcher != "" && !s.Subagent {
			if err := m.Detach(ctx, s.ID); err != nil {
				m.o.Logf("session %s: out of %d split: %v", s.ID, ws, err)
			}
		}
	}
	return nil
}

// relaunch hangs the hosted sessions launcher old started under nid, which goes on in its place
// (/clear), so they stay under it.
func (m *Manager) relaunch(old *Hosted, nid string) {
	from := map[string]bool{old.id: true}
	if a := old.discoveredAs(); a != "" {
		from[a] = true
	}
	m.mu.Lock()
	hosted := make([]*Hosted, 0, len(m.hosted))
	for _, h := range m.hosted {
		hosted = append(hosted, h)
	}
	m.mu.Unlock()
	for _, h := range hosted {
		if h == old || h.id == nid {
			continue
		}
		h.mu.Lock()
		if !from[h.meta.Launcher] {
			h.mu.Unlock()
			continue
		}
		h.meta.Launcher = nid
		// Written under its lock, as Started writes it: no older copy over a newer one.
		if err := writeJSON(filepath.Join(h.dir, fileMeta), h.meta); err != nil {
			h.logf("its launcher: %v", err)
		}
		h.mu.Unlock()
		h.publish()
	}
}
