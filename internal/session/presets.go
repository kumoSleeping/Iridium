package session

import (
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
