package session

import (
	"context"

	"github.com/kumoSleeping/iridium/internal/harness"
)

// Settings set where a session is held go to its channel.

// channelSet sets ch on session id through its channel (harness.Setter), which says whether it is
// its to set (one held elsewhere; an effort its own process has the say over while it runs here).
// done false: not such a session; the usual way goes.
func (m *Manager) channelSet(ctx context.Context, id string, ch harness.Change) (bool, error) {
	r, ok := m.ref(id)
	if !ok {
		return false, nil
	}
	s, ok := channelOf[harness.Setter](m, r.Agent)
	if !ok {
		return false, nil
	}
	return s.Set(ctx, r, ch)
}
