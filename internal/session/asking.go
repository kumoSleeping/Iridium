package session

import (
	"slices"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/store"
)

// Asking is a hosted session with asks open: the session, its asks (each with its card), and who
// began its turn (the app or login of ir_web), if known.
type Asking struct {
	Session api.Session
	Asks    []api.Ask
	From    string
}

// Asking is every hosted session with asks open (the needs queue, k_glossary "需要"), in no order.
func (m *Manager) Asking() []Asking {
	m.mu.Lock()
	hosted := make([]*Hosted, 0, len(m.hosted))
	for _, h := range m.hosted {
		hosted = append(hosted, h)
	}
	m.mu.Unlock()
	var out []Asking
	for _, h := range hosted {
		h.mu.Lock()
		if len(h.asks) > 0 {
			a := Asking{Session: h.summaryLocked(store.Overlay{}, false), Asks: slices.Clone(h.asks), From: h.turnFrom}
			for i := range a.Asks {
				a.Asks[i].Card = CardOf(h.id, a.Asks[i])
			}
			out = append(out, a)
		}
		h.mu.Unlock()
	}
	if len(out) == 0 {
		return nil
	}
	// Its name and placement, as the list has them.
	if over, err := m.o.Store.Overlays(); err == nil {
		for i := range out {
			o := over[out[i].Session.ID]
			out[i].Session.Name, out[i].Session.Workspace, out[i].Session.Pinned = o.Name, o.Workspace, o.Pinned
		}
	}
	return out
}
