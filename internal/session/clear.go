package session

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"github.com/kumoSleeping/iridium/internal/agent"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/store"
)

// IsClear says whether text is /clear or another of Claude's names for it (/reset, /new), what
// comes after it aside (Claude's is a name for the new conversation), which Iridium runs itself:
// sent to the agent, Claude would go on in a conversation of a new id (clearedInside).
func IsClear(text string) bool {
	f := strings.Fields(text)
	if len(f) == 0 {
		return false
	}
	switch strings.ToLower(f[0]) {
	case "/clear", "/reset", "/new":
		return true
	}
	return false
}

// Clear starts the session over (/clear, as Claude Code's): a new session of the same agent, in
// the same folder and workspace, with the same model, mode, effort and ultracode. The old one
// stays on record; idle, it goes to the archive and its agent stops; in a turn it is left to
// finish. A session that has said nothing yet is already clear: its own id comes back.
func (m *Manager) Clear(ctx context.Context, id, from string) (string, error) {
	h, err := m.take(ctx, id)
	if err != nil {
		return "", err
	}
	if p, err := m.Messages(h.id, -1, -1); err == nil && len(p.Blocks) == 0 && !h.busy() {
		return h.id, nil
	}
	h.mu.Lock()
	mt, effort, ultracode := h.meta, h.effort, h.ultracode
	h.mu.Unlock()
	req := api.NewSession{Agent: mt.Agent, Cwd: mt.Cwd, Model: mt.Model, Mode: mt.Mode, Caller: mt.Launcher}
	var name string // the one the old was given, if it is to go to the archive
	if over, err := m.o.Store.Overlays(); err == nil {
		if o := over[h.id]; !o.AutoName && !h.busy() && len(h.asksNow()) == 0 {
			name = o.Name
		}
		req.Workspace = over[h.id].Workspace
		if req.Workspace == 0 {
			req.Workspace = over[h.discoveredAs()].Workspace
		}
	}
	nid, err := m.create(ctx, req, from, name, func(nid string) error {
		if effort == "" && ultracode == nil {
			return nil
		}
		return m.o.Store.UpdateSettings(nid, func(s *store.Settings) { s.Effort, s.Ultracode = effort, ultracode })
	})
	if err != nil {
		return nid, err
	}
	if !h.busy() && len(h.asksNow()) == 0 {
		if err := m.SetArchived(ctx, h.id, true); err != nil {
			m.o.Logf("session %s: archiving it after /clear: %v", h.id, err)
		}
	}
	m.o.Logf("session %s: cleared, going on as %s", h.id, nid)
	return nid, nil
}

// clearedInside: session h's agent cleared itself (Claude's own /clear, which came to it past
// Iridium's): process proc now writes conversation nid, which Iridium does not host, so its words
// would show only there, read-only, and its asks here. Once the process is quiet (no turn, ask or
// task) it is stopped and h archived; nid is taken up in h's place, with h's settings, workspace
// and opener, and h's page follows it (Next). h, resumed, is as it was before the /clear.
func (m *Manager) clearedInside(h *Hosted, proc int, nid string) {
	h.logf("its agent cleared itself, going on as %s", nid)
	ctx := m.ctx
	wait := func(d time.Duration) bool {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(d):
			return true
		}
	}
	// Quiet at two looks in a row, not replaying: a daemon taking the process up replays its
	// output, the turns after the /clear too.
	for calm := 0; calm < 2; {
		h.mu.Lock()
		gone := !h.live || h.proc != proc
		quiet := !h.starting && h.turn == agent.Idle && len(h.asks) == 0 && len(h.tasks) == 0 && !h.replaying.Load()
		h.mu.Unlock()
		if gone {
			break
		}
		if quiet {
			calm++
		} else {
			calm = 0
		}
		if calm < 2 && !wait(m.o.ClearPoll) {
			return
		}
	}
	h.mu.Lock()
	if h.proc != proc {
		h.mu.Unlock()
		return // a process of its own since
	}
	mt := h.meta
	h.mu.Unlock()
	settings, _ := m.o.Store.Settings(h.id)
	var workspace int64
	var name string
	if over, err := m.o.Store.Overlays(); err == nil {
		if !over[h.id].AutoName {
			name = over[h.id].Name // the one it was given; a placeholder stays behind
		}
		if workspace = over[h.id].Workspace; workspace == 0 {
			workspace = over[h.discoveredAs()].Workspace
		}
	}
	opener := m.o.Store.Opener(h.id)
	if err := m.SetArchived(ctx, h.id, true); err != nil {
		h.logf("archiving it after its agent cleared itself: %v", err)
	}
	// Taken up once its process is gone (Claude's registry names nid till then) and discovery
	// found nid's transcript.
	var n *Hosted
	var err error
	for try := 0; try < 60; try++ {
		if !h.isLive() {
			if n, err = m.take(ctx, nid); err == nil {
				break
			}
		}
		if !wait(m.o.ClearPoll) {
			return
		}
	}
	if n == nil {
		h.logf("taking up %s, where its agent went on: %v", nid, err)
		return
	}
	n.mu.Lock()
	n.meta.Model, n.meta.Mode = mt.Model, mt.Mode
	n.effort, n.ultracode = h.effort, h.ultracode
	nmeta := n.meta
	n.mu.Unlock()
	if err := writeJSON(filepath.Join(n.dir, fileMeta), nmeta); err != nil {
		n.logf("saving its settings: %v", err)
	}
	if err := m.o.Store.SetSettings(nid, settings); err != nil {
		n.logf("saving its settings: %v", err)
	}
	if workspace != 0 {
		if err := m.o.Store.Move(nid, workspace); err != nil && !errors.Is(err, store.ErrNoWorkspace) {
			n.logf("moving it to workspace %d: %v", workspace, err)
		}
	}
	if name != "" {
		if err := m.o.Store.SetName(nid, name); err != nil {
			n.logf("its name: %v", err)
		}
	}
	if opener != "" {
		if err := m.o.Store.SetOpener(nid, opener); err != nil {
			n.logf("its opener: %v", err)
		}
	}
	h.mu.Lock()
	h.next = nid
	h.mu.Unlock()
	h.publish()
	n.publish()
	m.publishList()
	m.o.Logf("session %s: cleared by its agent, going on as %s", h.id, nid)
}

// asksNow are the asks open on h.
func (h *Hosted) asksNow() []api.Ask {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.asks
}

// RunsItself says whether text sent to session id is one its driver runs rather than writes as
// words (agent.Commander): nothing of it shows in the transcript.
func (m *Manager) RunsItself(id, text string) bool {
	h, err := m.get(id)
	if err != nil {
		return false
	}
	c, ok := h.drv.(agent.Commander)
	return ok && c.RunsItself(text)
}
