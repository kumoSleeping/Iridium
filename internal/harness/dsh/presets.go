package dsh

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/kumoSleeping/iridium/internal/agent"
	agentdsh "github.com/kumoSleeping/iridium/internal/agent/dsh"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/harness"
	"github.com/kumoSleeping/iridium/internal/remote"
)

// Agent presets (dsh's): those the dsh where a session runs registers, not a list of Iridium's own
// (the user, 2026-11-08). Where DSH Desktop's dsh plugin is connected (at that machine, or on this
// computer), Desktop's own registry (agent.presets: a bundle's and the profile's own too); else the
// dsh package's files. Kept a short while.

// presetsFor is how long a machine's presets are kept.
const presetsFor = 30 * time.Second

type presetList struct {
	at   time.Time
	list []agent.Preset
	def  string
}

// Presets are the agent presets dsh has at machine ("": this one), and the default (one of them).
func (c *Channel) Presets(ctx context.Context, machine string) ([]agent.Preset, string, error) {
	c.presetMu.Lock()
	p, ok := c.presets[machine]
	c.presetMu.Unlock()
	if ok && time.Since(p.at) < presetsFor {
		return p.list, p.def, nil
	}
	list, def, err := c.readPresets(ctx, machine)
	if err != nil {
		return nil, "", err
	}
	if len(list) == 0 {
		return nil, "", fmt.Errorf("dsh registers no agent presets")
	}
	if !slices.ContainsFunc(list, func(p agent.Preset) bool { return p.ID == def }) {
		def = list[0].ID
		if slices.ContainsFunc(list, func(p agent.Preset) bool { return p.ID == agentdsh.DefaultPreset }) {
			def = agentdsh.DefaultPreset
		}
	}
	c.presetMu.Lock()
	if c.presets == nil {
		c.presets = map[string]presetList{}
	}
	c.presets[machine] = presetList{at: time.Now(), list: list, def: def}
	c.presetMu.Unlock()
	return list, def, nil
}

func (c *Channel) readPresets(ctx context.Context, machine string) ([]agent.Preset, string, error) {
	if mc := c.hostOf(machine); mc != nil {
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		r, err := hostPresets(ctx, mc)
		if err != nil {
			return nil, "", fmt.Errorf("dsh at %s: agent presets: %w", mc.ID(), err)
		}
		var out []agent.Preset
		for _, p := range r.Presets {
			if p.Broken == "" && p.ID != "" {
				out = append(out, agent.Preset{ID: p.ID, Name: p.Name, Description: p.Description, Plugins: p.Plugins})
			}
		}
		return out, r.Default, nil
	}
	if machine == "" {
		h := c.h()
		if h == nil {
			return nil, "", fmt.Errorf("dsh: not started")
		}
		prog, err := agent.LookPath(h.Program(api.AgentDsh), h.AgentEnv())
		if err != nil {
			return nil, "", fmt.Errorf("%w: dsh", harness.ErrNotInstalled)
		}
		pkg, err := agentdsh.FindPackage(prog)
		if err != nil {
			return nil, "", err
		}
		return pkg.Presets()
	}
	if c.o.Remote == nil {
		return nil, "", fmt.Errorf("no machine %q", machine)
	}
	mc := c.o.Remote.Find(machine)
	if mc == nil {
		return nil, "", fmt.Errorf("no machine %q", machine)
	}
	hi, ok := mc.Hello()
	if !ok {
		return nil, "", fmt.Errorf("%w: %s", harness.ErrOffline, machine)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ai, ok := mc.Agent(string(api.AgentDsh))
	if !ok || ai.Scope == "" {
		return nil, "", fmt.Errorf("%w: dsh (not at %s)", harness.ErrNotInstalled, machine)
	}
	pkg := agentdsh.Package{Scope: ai.Scope, Version: ai.Version, Read: func(parts ...string) ([]byte, error) {
		return mc.ReadFile(ctx, remote.Join(hi.Sep, append([]string{ai.Scope}, parts...)...), 16<<20)
	}}
	return pkg.Presets()
}
