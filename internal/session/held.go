package session

import (
	"context"
	"fmt"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/harness"
)

// Features: extra switches on a session (api.Feature), Claude's UltraCode (its driver's) or ones
// a session's channel says it has (a plugin's where another process holds it, harness.Setter). Settings set where a
// session is held go to its channel too.

const (
	// ultracodeFeature is Claude's UltraCode as a feature (hosted.state, SetFeature).
	ultracodeFeature = "claude.ultracode"
	featurePlace     = "effort" // the one place the page knows
)

// SetFeature switches feature fid of session id: Claude's UltraCode as SetUltracode does (no
// option); another, through the session's channel (where it is held: a plugin's there). One the
// session does not have is a bad request.
func (m *Manager) SetFeature(ctx context.Context, id, fid string, req api.SetFeature) error {
	if fid == ultracodeFeature {
		return m.SetUltracode(ctx, id, req.On)
	}
	r, ok := m.ref(id)
	if !ok {
		return ErrNotFound
	}
	if s, ok := channelOf[harness.Setter](m, r.Agent); ok {
		if done, err := s.Set(ctx, r, harness.Change{Feature: &harness.FeatureChange{ID: fid, On: req.On, Option: req.Option}}); done {
			return err
		}
	}
	return fmt.Errorf("%w: no feature %q", ErrBadRequest, fid)
}

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
