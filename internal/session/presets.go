package session

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/kumoSleeping/iridium/internal/agent"
	"github.com/kumoSleeping/iridium/internal/agents"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/harness"
)

// Agent presets: those the agent registers where a session runs (agents.Info.Presets), as its
// channel says them (harness.Presets), not a list of Iridium's own (the user, 2026-11-08).

// Presets are the agent presets agent a has at machine ("": this one), and the default (one of
// them); none for an agent without presets.
func (m *Manager) Presets(ctx context.Context, a api.Agent, machine string) ([]agent.Preset, string, error) {
	if info, ok := agents.Get(a); !ok || !info.Presets {
		return nil, "", nil
	}
	p, ok := channelOf[harness.Presets](m, a)
	if !ok {
		return nil, "", fmt.Errorf("%w: no presets of %s here", ErrNotInstalled, a)
	}
	return p.Presets(ctx, machine)
}

// checkPreset: preset ("" the default) is one agent a has at machine; the default with it.
func (m *Manager) checkPreset(ctx context.Context, a api.Agent, machine, preset string) (string, error) {
	info, _ := agents.Get(a)
	if !info.Presets {
		return "", fmt.Errorf("%w: %s has no presets", ErrBadRequest, info.Name)
	}
	list, def, err := m.Presets(ctx, a, machine)
	if err != nil {
		return "", err
	}
	if preset != "" && !slices.ContainsFunc(list, func(p agent.Preset) bool { return p.ID == preset }) {
		ids := make([]string, len(list))
		for i, p := range list {
			ids[i] = p.ID
		}
		return "", fmt.Errorf("%w: no preset %q (%s)", ErrBadRequest, preset, strings.Join(ids, ", "))
	}
	return def, nil
}

// SessionPresets are the agent presets a session may run with, for the page: those its agent has
// where it runs, and the default.
func (m *Manager) SessionPresets(ctx context.Context, id string) (api.Presets, error) {
	a, machine := api.Agent(""), ""
	if h, err := m.get(id); err == nil {
		a, machine = h.meta.Agent, h.meta.Machine
	} else if s, _, ok := m.found(id); ok {
		a, machine = s.Agent, s.Machine
	} else {
		return api.Presets{}, err
	}
	m.warmModels(a, machine) // the page opened it: its models too
	return m.PagePresets(ctx, a, machine)
}

// PagePresets are the agent presets agent a has at machine, and the default, as the page has
// them.
func (m *Manager) PagePresets(ctx context.Context, a api.Agent, machine string) (api.Presets, error) {
	list, def, err := m.Presets(ctx, a, machine)
	if err != nil {
		return api.Presets{}, err
	}
	out := api.Presets{Presets: make([]api.Preset, len(list)), Default: def}
	for i, p := range list {
		out.Presets[i] = api.Preset{ID: p.ID, Name: p.Name, Description: p.Description}
	}
	return out, nil
}

// warmModels has agent a's channel ask again in the background for the models it offers at
// machine, if what it keeps is old (harness.Models): the page offers a model before the first
// message (the user, 2026-11-09).
func (m *Manager) warmModels(a api.Agent, machine string) {
	if mo, ok := channelOf[harness.Models](m, a); ok {
		mo.Warm(machine)
	}
}

// Catalog is the models agent a offers at machine ("": this one), as its channel says them.
func (m *Manager) Catalog(ctx context.Context, a api.Agent, machine string) (agent.Catalog, error) {
	mo, ok := channelOf[harness.Models](m, a)
	if !ok {
		return agent.Catalog{}, fmt.Errorf("%w: %s says no models", ErrBadRequest, a)
	}
	return mo.Catalog(ctx, machine)
}

// Sync reads again at once what agent a at machine (an executor's ID; "" this one) offers: its
// presets, models, workspaces and sessions (harness.Syncer).
func (m *Manager) Sync(ctx context.Context, a api.Agent, machine string) error {
	s, ok := channelOf[harness.Syncer](m, a)
	if !ok {
		return fmt.Errorf("%w: %s has nothing to sync", ErrBadRequest, a)
	}
	return s.Sync(ctx, machine)
}

// AgentComputerState returns per-computer state reported by agent a's channel (harness.ComputerStater).
func (m *Manager) AgentComputerState(a api.Agent, machine string) (harness.ComputerState, bool) {
	cs, ok := channelOf[harness.ComputerStater](m, a)
	if !ok {
		return harness.ComputerState{}, false
	}
	return cs.ComputerState(machine), true
}

// SetAgentComputerConfig updates per-computer configuration via agent a's channel (harness.ComputerStater).
func (m *Manager) SetAgentComputerConfig(ctx context.Context, a api.Agent, machine string, cfg harness.ComputerConfig) (harness.ComputerState, error) {
	cs, ok := channelOf[harness.ComputerStater](m, a)
	if !ok {
		return harness.ComputerState{}, fmt.Errorf("%w: %s has no computer settings", ErrBadRequest, a)
	}
	return cs.SetComputerConfig(ctx, machine, cfg)
}

// SearchExternal queries all channels that support on-demand external session search.
func (m *Manager) SearchExternal(ctx context.Context, query string, limit int) ([]api.ExternalSession, error) {
	var all []api.ExternalSession
	for _, ch := range m.o.Channels {
		ss, ok := ch.(harness.SessionSearcher)
		if !ok {
			continue
		}
		got, err := ss.SearchExternal(ctx, query, limit)
		if err != nil {
			continue
		}
		all = append(all, got...)
	}
	slices.SortFunc(all, func(a, b api.ExternalSession) int {
		if d := cmp.Compare(b.UpdatedAt, a.UpdatedAt); d != 0 {
			return d
		}
		return cmp.Compare(a.ID, b.ID)
	})
	if limit > 0 && len(all) > limit {
		all = all[:limit]
	}
	return all, nil
}

// AdoptExternal adopts an unsynced external session via its agent's channel.
func (m *Manager) AdoptExternal(ctx context.Context, s api.ExternalSession) error {
	ss, ok := channelOf[harness.SessionSearcher](m, s.Agent)
	if !ok {
		return fmt.Errorf("%w: %s cannot adopt external sessions", ErrBadRequest, s.Agent)
	}
	return ss.AdoptExternal(ctx, s)
}
