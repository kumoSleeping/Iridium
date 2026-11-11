package dsh

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/kumoSleeping/iridium/internal/agent"
	agentdsh "github.com/kumoSleeping/iridium/internal/agent/dsh"
	"github.com/kumoSleeping/iridium/internal/agents"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/harness"
	"github.com/kumoSleeping/iridium/internal/remote"
	"github.com/kumoSleeping/iridium/internal/remote/wire"
)

// dsh's models (the user, 2026-11-08: not a list of Iridium's own): those its sdk profile
// registers at the machine a session runs at, every provider's, and the default, as dsh itself
// says them: a probe run there (agentdsh.ProbeModule), one at a time a machine, kept a while.

const (
	catalogFor     = 3 * time.Minute  // a catalog is kept
	catalogFailFor = time.Minute      // a probe that failed is not run again before
	catalogWait    = 45 * time.Second // the most a probe runs
)

type catalogEntry struct {
	at   time.Time
	cat  agent.Catalog
	err  error
	busy chan struct{} // closed when the probe running ends
}

// Catalog is the models dsh offers at machine ("": this one).
func (c *Channel) Catalog(ctx context.Context, machine string) (agent.Catalog, error) {
	return c.catalog(ctx, machine, false)
}

// catalog is Catalog; force: probed now, not the one kept (after one running ends).
func (c *Channel) catalog(ctx context.Context, machine string, force bool) (agent.Catalog, error) {
	for {
		c.presetMu.Lock()
		if c.catalogs == nil {
			c.catalogs = map[string]*catalogEntry{}
		}
		e := c.catalogs[machine]
		if e != nil && e.busy != nil {
			busy := e.busy
			c.presetMu.Unlock()
			select {
			case <-busy:
				continue
			case <-ctx.Done():
				return agent.Catalog{}, ctx.Err()
			}
		}
		if e != nil && !force && (e.err == nil && time.Since(e.at) < catalogFor || e.err != nil && time.Since(e.at) < catalogFailFor) {
			c.presetMu.Unlock()
			return e.cat, e.err
		}
		busy := make(chan struct{})
		c.catalogs[machine] = &catalogEntry{busy: busy}
		if e != nil && e.err == nil {
			c.catalogs[machine].cat = e.cat
		}
		c.presetMu.Unlock()
		cat, err := c.probeCatalog(ctx, machine)
		if err != nil {
			c.o.Logf("dsh: its models at %q: %v", machine, err)
		}
		c.presetMu.Lock()
		c.catalogs[machine] = &catalogEntry{at: time.Now(), cat: cat, err: err}
		c.presetMu.Unlock()
		close(busy)
		return cat, err
	}
}

// probeCatalog runs dsh's probe at machine, as a unit of its own, and reads what it printed.
func (c *Channel) probeCatalog(ctx context.Context, machine string) (agent.Catalog, error) {
	h := c.h()
	if h == nil {
		return agent.Catalog{}, fmt.Errorf("dsh: not started")
	}
	ctx, cancel := context.WithTimeout(ctx, catalogWait)
	defer cancel()
	var run harness.Run
	if machine == "" {
		env := agent.ForeignEnv(h.AgentEnv())
		var argv []string
		if mc := c.hostOf(""); mc != nil {
			if ai, ok := mc.HostAgent(string(api.AgentDsh)); ok && len(ai.Argv) > 0 {
				argv = append([]string{}, ai.Argv...) // Desktop's own dsh, as its launches
				env = agentdsh.HostEnv(argv, env)
			}
		}
		if argv == nil {
			var err error
			if argv, err = (agent.SpawnOptions{Program: h.Program(api.AgentDsh)}).Command("dsh", env); err != nil {
				return agent.Catalog{}, err
			}
		}
		dir, err := os.MkdirTemp("", "iridium-dsh-probe-")
		if err != nil {
			return agent.Catalog{}, err
		}
		defer os.RemoveAll(dir)
		mod, patch := filepath.Join(dir, "probe.mjs"), filepath.Join(dir, "probe.yml")
		if err := os.WriteFile(mod, []byte(agentdsh.ProbeModule), 0o600); err != nil {
			return agent.Catalog{}, err
		}
		if err := os.WriteFile(patch, agentdsh.ProbePatch(agentdsh.FileURL(mod)), 0o600); err != nil {
			return agent.Catalog{}, err
		}
		home, _ := os.UserHomeDir()
		run = harness.Run{Argv: append(argv, agentdsh.ProbeArgs(patch, c.hostModels(ctx, "")...)...), Env: env, Cwd: home}
	} else {
		mc := c.machine(machine)
		if mc == nil {
			if c.o.Remote == nil || c.o.Remote.Find(machine) == nil {
				return agent.Catalog{}, fmt.Errorf("no machine %q", machine)
			}
			return agent.Catalog{}, harness.ErrOffline
		}
		hi, _ := mc.Hello()
		ai, ok := mc.Agent(string(api.AgentDsh))
		if !ok || len(ai.Argv) == 0 {
			return agent.Catalog{}, fmt.Errorf("%w: dsh (not at %s)", harness.ErrNotInstalled, machine)
		}
		mod := remote.Join(hi.Sep, hi.Scratch, "catalog-probe", "probe.mjs")
		patch := remote.Join(hi.Sep, hi.Scratch, "catalog-probe", "probe.yml")
		if err := mc.WriteFile(ctx, mod, []byte(agentdsh.ProbeModule), true); err != nil {
			return agent.Catalog{}, err
		}
		if err := mc.WriteFile(ctx, patch, agentdsh.ProbePatch(agentdsh.FileURL(mod)), true); err != nil {
			return agent.Catalog{}, err
		}
		info, _ := agents.Get(api.AgentDsh)
		argv := append(append([]string{}, ai.Argv...), agentdsh.ProbeArgs(patch, c.hostModels(ctx, machine)...)...)
		run = harness.Run{Argv: argv, Env: info.RemoteEnv(hi.Env, ai.Home), Cwd: hi.Home}
	}
	out, errOut, err := h.RunOnce(ctx, machine, run)
	if err != nil {
		if ctx.Err() != nil {
			return agent.Catalog{}, fmt.Errorf("dsh: the probe of its models did not end")
		}
		return agent.Catalog{}, err
	}
	cat, err := agentdsh.ParseProbe(out)
	if err != nil {
		if len(errOut) > 0 {
			err = fmt.Errorf("%w: %s", err, errOut[max(0, len(errOut)-512):])
		}
		return agent.Catalog{}, err
	}
	return cat, nil
}

// hostModels are the --patch files that give a dsh process at machine DSH Desktop's models and
// providers (the user, 2026-11-08: synced with Desktop's), where its dsh plugin is connected
// (agent.prepare without a session); none if it is not.
func (c *Channel) hostModels(ctx context.Context, machine string) []string {
	mc := c.hostOf(machine)
	if mc == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, prepareWait)
	defer cancel()
	r, err := hostPrepare(ctx, mc, wire.PrepareParams{})
	if err != nil {
		c.o.Logf("dsh: DSH Desktop's models (%s): %v", mc.ID(), err)
		return nil
	}
	if machine == "" {
		for _, p := range r.Patches {
			if err := ownFile(p); err != nil {
				c.o.Logf("dsh: DSH Desktop's models: %v", err)
				return nil
			}
		}
	}
	return r.Patches
}

// Cached is dsh's models at machine as last probed, stale or not; false if never had.
func (c *Channel) Cached(machine string) (agent.Catalog, bool) {
	c.presetMu.Lock()
	defer c.presetMu.Unlock()
	e := c.catalogs[machine]
	if e == nil || len(e.cat.Models) == 0 {
		return agent.Catalog{}, false
	}
	return e.cat, true
}

// Warm probes dsh's models at machine in the background if they are not fresh, then shows them
// in its dsh sessions whose agent has not said its own (not started yet): the page offers a model
// before the first message (the user, 2026-11-09).
func (c *Channel) Warm(machine string) {
	c.presetMu.Lock()
	e := c.catalogs[machine]
	fresh := e != nil && (e.busy != nil || e.err == nil && time.Since(e.at) < catalogFor || e.err != nil && time.Since(e.at) < catalogFailFor)
	c.presetMu.Unlock()
	if fresh || c.h() == nil {
		return
	}
	go func() {
		if _, err := c.Catalog(c.ctx, machine); err == nil {
			c.refreshMachine(machine)
		}
	}()
}

// refreshMachine shows its dsh sessions at machine again, hosted or not.
func (c *Channel) refreshMachine(machine string) {
	h := c.h()
	if h == nil {
		return
	}
	if ids := h.Sessions(api.AgentDsh, machine); len(ids) > 0 {
		h.Refresh(ids)
	}
}

// Sync reads again what the dsh at machine (an executor's ID; "" this one) offers, at once, not
// as kept (the user, 2026-11-09: a key in a group's menu, not three minutes' wait): its workspaces
// and sessions, its models, probed now with DSH Desktop's settings as they are, and its presets;
// its sessions there show them. A running agent keeps its own till it starts again.
func (c *Channel) Sync(ctx context.Context, machine string) error {
	h := c.h()
	if h == nil {
		return fmt.Errorf("dsh: not started")
	}
	c.presetMu.Lock()
	delete(c.presets, machine)
	c.presetMu.Unlock()
	// Its workspaces and sessions too (the user, 2026-11-09), not at the next look.
	if err := h.Rescan(ctx, machine); err != nil {
		return err
	}
	if _, err := c.catalog(ctx, machine, true); err != nil {
		return err
	}
	c.refreshMachine(machine)
	return nil
}

// catalogState gives a dsh session its machine's models as last probed (over what its agent said
// at its start: a sync shows at once; one picked that its agent has not restarts it), and the
// default as its model while its agent has said none (not started yet).
func (c *Channel) catalogState(machine string, s *api.SessionState) {
	cat, ok := c.Cached(machine)
	if !ok {
		return
	}
	s.Models = nil
	for _, x := range cat.Models {
		s.Models = append(s.Models, api.Model{Value: x.Value, Name: x.Name, Description: x.Description, Efforts: slices.Clone(x.Efforts), NoEffort: x.NoEffort, EffortNames: maps.Clone(x.EffortNames), Group: x.Group})
	}
	if s.Model == "" {
		s.Model = cat.Default
	}
}

// ProbedAt is when dsh's models at machine were last probed; zero if never.
func (c *Channel) ProbedAt(machine string) time.Time {
	c.presetMu.Lock()
	defer c.presetMu.Unlock()
	if e := c.catalogs[machine]; e != nil {
		return e.at
	}
	return time.Time{}
}
