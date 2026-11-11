package dsh

import (
	"context"
	"time"

	"github.com/kumoSleeping/iridium/internal/agent"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/harness"
	"github.com/kumoSleeping/iridium/internal/remote"
	"github.com/kumoSleeping/iridium/internal/remote/wire"
)

// prepareWait is how long agent.prepare may take: the plugin reads its profile and writes a few
// files.
const prepareWait = 20 * time.Second

// hostOf is the executor that composes a dsh launch at machine ("": this computer) from its host's
// settings (wire.CapPrepare: the dsh plugin in DSH Desktop); nil if none is connected. On this
// computer, one that said it is on it (remote.Hub.Local).
func (c *Channel) hostOf(machine string) *remote.Machine {
	if c.o.Remote == nil {
		return nil
	}
	if machine == "" {
		return c.o.Remote.Local(wire.CapPrepare)
	}
	if mc := c.machine(machine); mc != nil && mc.Has(wire.CapPrepare) {
		return mc
	}
	return nil
}

// Prepare gives a dsh launch what dsh takes at its start: its machine's models (the driver's
// catalog), and where DSH Desktop's dsh plugin is connected (at that machine, or on this computer)
// what Desktop composes: its presets, its models, a preset of its bundles, its own part in the
// process (agent.presets, agent.prepare). On this computer the dsh run is Desktop's own.
func (c *Channel) Prepare(ctx context.Context, r harness.Ref, o *agent.SpawnOptions) error {
	if !ours(r) {
		return nil
	}
	machine := r.Machine
	o.Catalog = func() (agent.Catalog, error) { return c.Catalog(c.ctx, machine) }
	mc := c.hostOf(machine)
	if mc == nil {
		return nil
	}
	h := &agent.Host{
		Presets: c.spawnPresets(ctx, machine),
		Prepare: func(preset, system string) ([]string, bool, error) {
			ctx, cancel := context.WithTimeout(ctx, prepareWait)
			defer cancel()
			rep, err := hostPrepare(ctx, mc, wire.PrepareParams{Session: o.Session, Cwd: o.Cwd, Preset: preset, System: system})
			if err != nil {
				return nil, false, err
			}
			if machine == "" {
				for _, p := range rep.Patches {
					if err := ownFile(p); err != nil {
						return nil, false, err
					}
				}
			}
			return rep.Patches, rep.Preset, nil
		},
	}
	if machine == "" {
		ai, ok := mc.HostAgent(string(api.AgentDsh))
		if !ok || len(ai.Argv) == 0 {
			return nil // it runs no dsh of its own: the one here, as it ships
		}
		h.Argv, h.Package, h.Scope, h.Version = ai.Argv, ai.Package, ai.Scope, ai.Version
	}
	o.Host = h
	return nil
}

// ProvidedHere: DSH Desktop's plugin on this computer runs a dsh of its own, which a launch here
// takes.
func (c *Channel) ProvidedHere() bool {
	mc := c.hostOf("")
	if mc == nil {
		return false
	}
	ai, ok := mc.HostAgent(string(api.AgentDsh))
	return ok && len(ai.Argv) > 0
}
