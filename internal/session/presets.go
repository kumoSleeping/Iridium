package session

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/kumoSleeping/iridium/internal/agent"
	"github.com/kumoSleeping/iridium/internal/agent/dsh"
	"github.com/kumoSleeping/iridium/internal/agents"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/remote"
	"github.com/kumoSleeping/iridium/internal/remote/wire"
)

// Agent presets (dsh's): those the dsh where a session runs registers, not a list of Iridium's own
// (the user, 2026-11-08). At another machine, DSH Desktop's own registry through the dsh plugin
// (dsh.presets: a bundle's and the profile's own too); an executor without it, and this machine,
// the dsh package's files. Kept a short while.

// presetsFor is how long a machine's presets are kept.
const presetsFor = 30 * time.Second

type presetList struct {
	at   time.Time
	list []agent.Preset
	def  string
}

// Presets are the agent presets agent a has at machine ("": this one), and the default (one of
// them); none for an agent without presets.
func (m *Manager) Presets(ctx context.Context, a api.Agent, machine string) ([]agent.Preset, string, error) {
	if info, ok := agents.Get(a); !ok || !info.Presets {
		return nil, "", nil
	}
	m.presetMu.Lock()
	c, ok := m.presets[machine]
	m.presetMu.Unlock()
	if ok && time.Since(c.at) < presetsFor {
		return c.list, c.def, nil
	}
	list, def, err := m.readPresets(ctx, machine)
	if err != nil {
		return nil, "", err
	}
	if len(list) == 0 {
		return nil, "", fmt.Errorf("dsh registers no agent presets")
	}
	if !slices.ContainsFunc(list, func(p agent.Preset) bool { return p.ID == def }) {
		def = list[0].ID
		if slices.ContainsFunc(list, func(p agent.Preset) bool { return p.ID == dsh.DefaultPreset }) {
			def = dsh.DefaultPreset
		}
	}
	m.presetMu.Lock()
	if m.presets == nil {
		m.presets = map[string]presetList{}
	}
	m.presets[machine] = presetList{at: time.Now(), list: list, def: def}
	m.presetMu.Unlock()
	return list, def, nil
}

func (m *Manager) readPresets(ctx context.Context, machine string) ([]agent.Preset, string, error) {
	if machine == "" {
		prog, err := agent.LookPath(m.program(api.AgentDsh), m.agentEnv())
		if err != nil {
			return nil, "", fmt.Errorf("%w: dsh", ErrNotInstalled)
		}
		pkg, err := dsh.FindPackage(prog)
		if err != nil {
			return nil, "", err
		}
		return pkg.Presets()
	}
	if m.o.Remote == nil {
		return nil, "", fmt.Errorf("no machine %q", machine)
	}
	mc := m.o.Remote.Find(machine)
	if mc == nil {
		return nil, "", fmt.Errorf("no machine %q", machine)
	}
	hi, ok := mc.Hello()
	if !ok {
		return nil, "", fmt.Errorf("%w: %s", ErrOffline, machine)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	r, err := mc.DshPresets(ctx)
	if err == nil {
		var out []agent.Preset
		for _, p := range r.Presets {
			if p.Broken == "" && p.ID != "" {
				out = append(out, agent.Preset{ID: p.ID, Name: p.Name, Description: p.Description, Plugins: p.Plugins})
			}
		}
		return out, r.Default, nil
	}
	if !wire.IsCode(err, wire.CodeUnsupported) {
		return nil, "", fmt.Errorf("dsh at %s: agent presets: %w", machine, err)
	}
	ai, ok := mc.Agent(string(api.AgentDsh))
	if !ok || ai.Scope == "" {
		return nil, "", fmt.Errorf("%w: dsh (not at %s)", ErrNotInstalled, machine)
	}
	pkg := dsh.Package{Scope: ai.Scope, Version: ai.Version, Read: func(parts ...string) ([]byte, error) {
		return mc.ReadFile(ctx, remote.Join(hi.Sep, append([]string{ai.Scope}, parts...)...), 16<<20)
	}}
	return pkg.Presets()
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
	if a == api.AgentDsh {
		m.warmCatalog(machine) // the page opened it: its models too
	}
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

// presetsHook is a remote launch's Presets: the machine's, as the page lists them.
func (m *Manager) presetsHook(ctx context.Context, a api.Agent, machine string) func() ([]agent.Preset, string, error) {
	return func() ([]agent.Preset, string, error) { return m.Presets(ctx, a, machine) }
}
