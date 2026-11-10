package session

import (
	"context"
	"path/filepath"

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

// listed says whether the page lists session id of list (the page's tree.ts, as far as it is
// the server's business): one at this machine; one Own; one with a launcher (hung under it while
// that is listed and not archived, else on its own, as Own); one in a group holding such a
// session not archived; a subagent as its parent. One not in list is.
func listed(list []api.Session, id string) bool {
	byID := make(map[string]*api.Session, len(list))
	for i := range list {
		byID[list[i].ID] = &list[i]
	}
	shown := func(id string) bool { l := byID[id]; return l != nil && !l.Archived }
	// Own on the page: Own, or one whose launcher is not shown.
	own := func(s *api.Session) bool {
		return s.Own || s.Launcher != "" && !s.Subagent && !shown(s.Launcher)
	}
	groups := map[int64]bool{} // the groups listed for an Own session in them
	for i := range list {
		if s := &list[i]; s.Workspace != 0 && !s.Archived && own(s) {
			groups[s.Workspace] = true
		}
	}
	seen := map[string]bool{}
	var is func(id string) bool
	is = func(id string) bool {
		s := byID[id]
		switch {
		case s == nil:
			return true
		case seen[id]:
			return false
		}
		seen[id] = true
		switch {
		case s.Machine == "", s.Own, s.Launcher != "" && !s.Subagent, groups[s.Workspace] && s.Workspace != 0:
			return true
		case s.Subagent && s.Parent != "":
			return is(s.Parent)
		}
		return false
	}
	return is(id)
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
	return listed(l, id)
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
		mt := h.meta
		h.mu.Unlock()
		if err := writeJSON(filepath.Join(h.dir, fileMeta), mt); err != nil {
			h.logf("its launcher: %v", err)
		}
		h.publish()
	}
}
