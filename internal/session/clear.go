package session

import (
	"context"
	"strings"

	"github.com/kumoSleeping/iridium/internal/agent"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/store"
)

// IsClear says whether text is /clear (or /new with nothing after it), which Iridium runs itself:
// sent to the agent, Claude would go on in a conversation of a new id, out of the session's sight.
func IsClear(text string) bool {
	t := strings.ToLower(strings.TrimSpace(text))
	return t == "/clear" || t == "/new"
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
	req := api.NewSession{Agent: mt.Agent, Cwd: mt.Cwd, Model: mt.Model, Mode: mt.Mode}
	if over, err := m.o.Store.Overlays(); err == nil {
		req.Workspace = over[h.id].Workspace
		if req.Workspace == 0 {
			req.Workspace = over[h.discoveredAs()].Workspace
		}
	}
	nid, err := m.create(ctx, req, from, func(nid string) error {
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
