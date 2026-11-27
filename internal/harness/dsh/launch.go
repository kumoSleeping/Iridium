package dsh

import (
	"context"
	"errors"

	"github.com/kumoSleeping/iridium/internal/agent"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/harness"
	"github.com/kumoSleeping/iridium/internal/remote"
)

// inprocHost is a connected executor at machine ("": this computer) speaking CapInproc: sessions
// run inside DSH Desktop's own process via @dsh-desktop holds (no child process, no --patch).
func (c *Channel) inprocHost(machine string) *remote.Machine {
	if c.o.Remote == nil {
		return nil
	}
	if machine == "" {
		return c.o.Remote.Local(CapInproc)
	}
	if mc := c.machine(machine); mc != nil && mc.Has(CapInproc) {
		return mc
	}
	return nil
}

// hostOf is the connected DSH Desktop host at machine ("": this computer).
func (c *Channel) hostOf(machine string) *remote.Machine {
	return c.inprocHost(machine)
}

// LocalExecutor implements harness.LocalHost: when a local DSH Desktop speaking CapInproc is
// connected (or the headless dsh-desktop-host fallback starts), local dsh sessions run through
// that local executor instead of spawning a local OS child directly.
func (c *Channel) LocalExecutor(ctx context.Context, r harness.Ref) (string, bool) {
	if !ours(r) || r.Machine != "" {
		return "", false
	}
	if mc := c.inprocHost(""); mc != nil {
		return mc.ID(), true
	}
	if c.o.EnsureHeadless != nil {
		return c.o.EnsureHeadless(ctx)
	}
	if c.headless != nil {
		if err := c.headless.Ensure(ctx); err == nil {
			if mc := c.inprocHost(""); mc != nil {
				return mc.ID(), true
			}
		} else if !errors.Is(err, ErrNoDesktop) && !errors.Is(err, ErrDesktopRuns) {
			c.o.Logf("dsh: headless fallback: %v", err)
		}
	}
	return "", false
}

// Prepare gives a dsh launch its machine's models catalog and, when DSH Desktop speaking CapInproc
// is connected at that machine (or locally), routes the session in-process via @dsh-desktop.
func (c *Channel) Prepare(ctx context.Context, r harness.Ref, o *agent.SpawnOptions) error {
	if !ours(r) {
		return nil
	}
	machine := r.Machine
	o.Catalog = func() (agent.Catalog, error) { return c.Catalog(c.ctx, machine) }
	if mc := c.inprocHost(machine); mc != nil {
		version := ""
		if machine == "" {
			if ai, ok := mc.HostAgent(string(api.AgentDsh)); ok {
				version = ai.Version
			}
		} else if ai, ok := mc.Agent(string(api.AgentDsh)); ok {
			version = ai.Version
		}
		o.Host = &agent.Host{
			Inproc:  true,
			Argv:    []string{inprocArgv0},
			Version: version,
			Presets: c.spawnPresets(ctx, machine),
		}
	}
	return nil
}

// ProvidedHere reports whether DSH Desktop on this computer (connected or discoverable) provides dsh.
func (c *Channel) ProvidedHere() bool {
	if c.inprocHost("") != nil {
		return true
	}
	if c.o.StateDir != "" {
		if _, ok := DiscoverDesktop(DesktopDiscoverOptions{
			StateDir:   c.o.StateDir,
			ManualPath: LoadManualDesktopPath(c.o.StateDir),
		}); ok {
			return true
		}
	}
	return false
}
