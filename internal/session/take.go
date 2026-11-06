package session

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/store"
	"github.com/kumoSleeping/iridium/internal/transcript"
)

// ErrElsewhere: another process runs the session, so it is read-only here.
var ErrElsewhere = errors.New("the session runs elsewhere")

// ErrSubagent: the session is a subagent's, which only its parent talks to: read-only here.
var ErrSubagent = errors.New("a subagent's session is read-only; talk to its parent")

// Finder is what a Discovery may do besides listing: give one session it found, with its
// transcript where it can be read; and say whether a session is free to resume, that is no
// process but Iridium's own uses it.
type Finder interface {
	Found(id string) (api.Session, *transcript.File, bool)
	Free(id string) error
}

func (m *Manager) finder() Finder {
	f, _ := m.o.Discovery.(Finder)
	return f
}

// found is session id as discovery found it, if Iridium does not host it.
func (m *Manager) found(id string) (api.Session, *transcript.File, bool) {
	if f := m.finder(); f != nil {
		return f.Found(id)
	}
	return api.Session{}, nil, false
}

// Known: Iridium hosts session id, or found it on disk.
func (m *Manager) Known(id string) bool {
	if _, err := m.get(id); err == nil {
		return true
	}
	_, _, ok := m.found(id)
	return ok
}

// take is the hosted session of id, taking up one found on disk if no process uses it: from then
// on Iridium hosts it, resuming the agent's own session (--resume). One Iridium hosts that runs
// elsewhere now is not started either.
func (m *Manager) take(ctx context.Context, id string) (*Hosted, error) {
	f := m.finder()
	h, err := m.get(id)
	if err != nil {
		// A hosted Codex session named by its thread.
		if a := m.byAgentID(id); a != nil {
			h, err = a, nil
		}
	}
	if err == nil {
		// Free by the id the agent's processes and locks know it by (Codex: the thread).
		if as := h.discoveredAs(); f != nil && !h.isLive() {
			if err := f.Free(as); err != nil {
				return nil, fmt.Errorf("%w: %v", ErrElsewhere, err)
			}
		}
		return h, nil
	}
	if f == nil {
		return nil, err
	}
	s, _, ok := f.Found(id)
	if !ok {
		return nil, ErrNotFound
	}
	if s.Subagent {
		return nil, ErrSubagent
	}
	if err := f.Free(id); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrElsewhere, err)
	}
	// Claude and pi resume a session by its id, Codex a thread: its id is the hosted one's too.
	switch s.Agent {
	case api.AgentClaude, api.AgentCodex, api.AgentPi:
	default:
		return nil, fmt.Errorf("resuming a %s session is not supported yet", s.Agent)
	}
	if st, err := os.Stat(s.Cwd); err != nil || !st.IsDir() || !filepath.IsAbs(s.Cwd) {
		return nil, fmt.Errorf("its folder is gone: %q", s.Cwd)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if h := m.hosted[id]; h != nil {
		return h, nil // taken meanwhile
	}
	mt := meta{ID: id, Agent: s.Agent, Cwd: s.Cwd, Created: time.Now().UnixMilli()}
	if s.Agent == api.AgentCodex {
		mt.AgentID = id // the thread to resume
	}
	h = m.newHosted(mt)
	if err := os.MkdirAll(h.dir, 0o700); err != nil {
		return nil, err
	}
	if err := writeJSON(filepath.Join(h.dir, fileMeta), mt); err != nil {
		return nil, err
	}
	h.said = cached{title: s.Title, last: s.Last}
	if s.UpdatedAt > 0 {
		h.lastActive = time.UnixMilli(s.UpdatedAt)
		h.shownAt = h.lastActive
	}
	m.hosted[id] = h
	m.o.Logf("session %s: taken up from disk", id)
	return h, nil
}

// elsewhere marks a hosted session whose agent Iridium is not running but another process is;
// as: the id discovery finds it under.
func (m *Manager) elsewhere(s api.Session, as string) api.Session {
	if s.Live {
		return s
	}
	if f, _, ok := m.found(as); ok && f.Status == api.StatusElsewhere {
		s.Status, s.Live, s.Needs = api.StatusElsewhere, true, f.Needs
	}
	return s
}

// foreignState is the carried state of a session found on disk that Iridium does not host:
// read-only, no asks, no tasks; End is how far its transcript goes.
func (m *Manager) foreignState(id string, over map[string]store.Overlay) (*api.SessionState, error) {
	s, f, ok := m.found(id)
	if !ok {
		return nil, ErrNotFound
	}
	if over == nil {
		over, _ = m.o.Store.Overlays()
	}
	o := over[id]
	s.Name, s.Pinned, s.Workspace, s.Color = o.NameOver(s.Title), o.Pinned, o.Workspace, api.Color(o.Color)
	s.Archived = !m.visibility().Shown(string(s.Agent), s.ID)
	m.parentOf(&s)
	st := &api.SessionState{Session: s, Asks: []api.Ask{}, Tasks: []api.Task{}}
	if f != nil {
		st.End, _ = f.Size()
	}
	return st, nil
}

// parentOf names a subagent's parent by Iridium's id: a hosted Codex session's thread is not.
func (m *Manager) parentOf(s *api.Session) {
	if !s.Subagent || s.Parent == "" {
		return
	}
	if h := m.byAgentID(s.Parent); h != nil {
		s.Parent, s.Launcher = h.id, h.id
	}
}

// readable is the session's transcript, hosted or found on disk; nil if there is none (yet) or
// it cannot be read.
func (m *Manager) readable(id string) (*transcript.File, error) {
	if h, err := m.get(id); err == nil {
		return h.transcript(), nil
	}
	_, f, ok := m.found(id)
	if !ok {
		return nil, ErrNotFound
	}
	return f, nil
}

// Refresh is discovery telling what changed: the list again, and the state of each session
// named, hosted or not.
func (m *Manager) Refresh(ids []string) {
	m.publishList()
	over, _ := m.o.Store.Overlays()
	for _, id := range ids {
		if h, err := m.get(id); err == nil {
			h.publish()
			continue
		}
		if st, err := m.foreignState(id, over); err == nil {
			m.o.Hub.Set("session:"+id, st)
		} else {
			m.o.Hub.Delete("session:" + id)
		}
	}
}

// intoCallers puts a session made for a caller (the session that ran ir new) in the caller's
// workspace, as its launcher's (plan: 新会话进调用者的工作区). A caller in none leaves it to
// discovery.
func (m *Manager) intoCallers(id, caller string) {
	over, err := m.o.Store.Overlays()
	if err != nil || over[caller].Workspace == 0 {
		return
	}
	if _, err := m.o.Store.AssignIfNew(id, over[caller].Workspace); err != nil {
		m.o.Logf("session %s: %v", id, err)
	}
}
