package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/kumoSleeping/iridium/internal/agent"
	"github.com/kumoSleeping/iridium/internal/agent/dsh"
	"github.com/kumoSleeping/iridium/internal/agents"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/hold"
	"github.com/kumoSleeping/iridium/internal/platform"
	"github.com/kumoSleeping/iridium/internal/remote"
	"github.com/kumoSleeping/iridium/internal/remote/wire"
)

// dsh's models (the user, 2026-11-08: not a list of Iridium's own): those its sdk profile
// registers at the machine a session runs at, every provider's, and the default, as dsh itself
// says them: a probe run there (dsh.ProbeModule), one at a time a machine, kept a while.

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
func (m *Manager) Catalog(ctx context.Context, machine string) (agent.Catalog, error) {
	return m.catalog(ctx, machine, false)
}

// catalog is Catalog; force: probed now, not the one kept (after one running ends).
func (m *Manager) catalog(ctx context.Context, machine string, force bool) (agent.Catalog, error) {
	for {
		m.presetMu.Lock()
		if m.catalogs == nil {
			m.catalogs = map[string]*catalogEntry{}
		}
		e := m.catalogs[machine]
		if e != nil && e.busy != nil {
			busy := e.busy
			m.presetMu.Unlock()
			select {
			case <-busy:
				continue
			case <-ctx.Done():
				return agent.Catalog{}, ctx.Err()
			}
		}
		if e != nil && !force && (e.err == nil && time.Since(e.at) < catalogFor || e.err != nil && time.Since(e.at) < catalogFailFor) {
			m.presetMu.Unlock()
			return e.cat, e.err
		}
		busy := make(chan struct{})
		m.catalogs[machine] = &catalogEntry{busy: busy}
		if e != nil && e.err == nil {
			m.catalogs[machine].cat = e.cat
		}
		m.presetMu.Unlock()
		cat, err := m.probeCatalog(ctx, machine)
		if err != nil {
			m.o.Logf("dsh: its models at %q: %v", machine, err)
		}
		m.presetMu.Lock()
		m.catalogs[machine] = &catalogEntry{at: time.Now(), cat: cat, err: err}
		m.presetMu.Unlock()
		close(busy)
		return cat, err
	}
}

// probeCatalog runs dsh's probe at machine, as a holder of its own, and reads what it printed.
func (m *Manager) probeCatalog(ctx context.Context, machine string) (agent.Catalog, error) {
	ctx, cancel := context.WithTimeout(ctx, catalogWait)
	defer cancel()
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	unit := m.o.UnitPrefix + "probe-" + hex.EncodeToString(b)
	dir := m.HoldDir(unit)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return agent.Catalog{}, err
	}
	defer os.RemoveAll(dir)
	info, _ := agents.Get(api.AgentDsh)
	var launcher platform.Launcher
	var spec hold.Spec
	if machine == "" {
		env := agent.ForeignEnv(m.agentEnv())
		argv, err := agent.SpawnOptions{Program: m.o.Config.Programs[string(api.AgentDsh)]}.Command("dsh", env)
		if err != nil {
			return agent.Catalog{}, err
		}
		mod, patch := filepath.Join(dir, "probe.mjs"), filepath.Join(dir, "probe.yml")
		if err := os.WriteFile(mod, []byte(dsh.ProbeModule), 0o600); err != nil {
			return agent.Catalog{}, err
		}
		if err := os.WriteFile(patch, dsh.ProbePatch(dsh.FileURL(mod)), 0o600); err != nil {
			return agent.Catalog{}, err
		}
		home, _ := os.UserHomeDir()
		launcher, spec = m.o.Launcher, hold.Spec{Argv: append(argv, dsh.ProbeArgs(patch)...), Env: env, Cwd: home}
	} else {
		if m.o.Remote == nil {
			return agent.Catalog{}, fmt.Errorf("no machine %q", machine)
		}
		mc := m.o.Remote.Find(machine)
		if mc == nil {
			return agent.Catalog{}, fmt.Errorf("no machine %q", machine)
		}
		hi, ok := mc.Hello()
		if !ok {
			return agent.Catalog{}, ErrOffline
		}
		ai, ok := mc.Agent(string(api.AgentDsh))
		if !ok || len(ai.Argv) == 0 {
			return agent.Catalog{}, fmt.Errorf("%w: dsh (not at %s)", ErrNotInstalled, machine)
		}
		mod := remote.Join(hi.Sep, hi.Scratch, "catalog-probe", "probe.mjs")
		patch := remote.Join(hi.Sep, hi.Scratch, "catalog-probe", "probe.yml")
		if err := mc.WriteFile(ctx, mod, []byte(dsh.ProbeModule), true); err != nil {
			return agent.Catalog{}, err
		}
		if err := mc.WriteFile(ctx, patch, dsh.ProbePatch(dsh.FileURL(mod)), true); err != nil {
			return agent.Catalog{}, err
		}
		argv := append(append([]string{}, ai.Argv...), dsh.ProbeArgs(patch, m.desktopLLM(ctx, mc)...)...)
		launcher, spec = mc.Launcher(), hold.Spec{Argv: argv, Env: info.RemoteEnv(hi.Env, ai.Home), Cwd: hi.Home}
	}
	spec.StopGraceMS = 1000
	if err := hold.Prepare(dir, spec); err != nil {
		return agent.Catalog{}, err
	}
	u := platform.Unit{
		Name:        unit,
		Argv:        []string{m.o.Exe, "hold", dir},
		StopPost:    []string{m.o.Exe, "hold", "--stop-post", dir},
		StopTimeout: 10 * time.Second,
	}
	if err := launcher.Start(ctx, u); err != nil {
		return agent.Catalog{}, err
	}
	for {
		if e, _ := hold.ReadExit(dir); e != nil {
			break
		}
		select {
		case <-ctx.Done():
			stop, done := context.WithTimeout(context.Background(), 15*time.Second)
			_ = launcher.Stop(stop, unit)
			done()
			return agent.Catalog{}, errors.New("dsh: the probe of its models did not end")
		case <-time.After(200 * time.Millisecond):
		}
	}
	out, _ := os.ReadFile(filepath.Join(dir, hold.FileOutLog))
	cat, err := dsh.ParseProbe(out)
	if err != nil {
		if b, _ := os.ReadFile(filepath.Join(dir, hold.FileErrLog)); len(b) > 0 {
			err = fmt.Errorf("%w: %s", err, b[max(0, len(b)-512):])
		}
		return agent.Catalog{}, err
	}
	return cat, nil
}

// desktopLLM are the --patch files at mc that give a dsh session there DSH Desktop's models and
// providers (the user, 2026-11-08: synced with Desktop's): the dsh plugin's dsh.llm, written anew;
// none from an executor without it.
func (m *Manager) desktopLLM(ctx context.Context, mc *remote.Machine) []string {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	p, err := mc.DshLLM(ctx)
	if err != nil {
		if !wire.IsCode(err, wire.CodeUnsupported) {
			m.o.Logf("dsh: DSH Desktop's models at %s: %v", mc.ID(), err)
		}
		return nil
	}
	if p == "" {
		return nil
	}
	return []string{p}
}

// cachedCatalog is dsh's models at machine as last probed, stale or not; false if never had.
func (m *Manager) cachedCatalog(machine string) (agent.Catalog, bool) {
	m.presetMu.Lock()
	defer m.presetMu.Unlock()
	e := m.catalogs[machine]
	if e == nil || len(e.cat.Models) == 0 {
		return agent.Catalog{}, false
	}
	return e.cat, true
}

// warmCatalog probes dsh's models at machine in the background if they are not fresh, then shows
// them in its dsh sessions whose agent has not said its own (not started yet): the page offers a
// model before the first message (the user, 2026-11-09).
func (m *Manager) warmCatalog(machine string) {
	m.presetMu.Lock()
	e := m.catalogs[machine]
	fresh := e != nil && (e.busy != nil || e.err == nil && time.Since(e.at) < catalogFor || e.err != nil && time.Since(e.at) < catalogFailFor)
	m.presetMu.Unlock()
	if fresh {
		return
	}
	go func() {
		if _, err := m.Catalog(m.ctx, machine); err == nil {
			m.refreshMachine(machine)
		}
	}()
}

// refreshMachine shows its dsh sessions at machine again, hosted or not.
func (m *Manager) refreshMachine(machine string) {
	var ids []string
	if d := m.o.Discovery; d != nil {
		if l, err := d.Sessions(m.ctx); err == nil {
			for _, s := range l {
				if s.Agent == api.AgentDsh && s.Machine == machine {
					ids = append(ids, s.ID)
				}
			}
		}
	}
	m.mu.Lock()
	for id, h := range m.hosted {
		if h.meta.Agent == api.AgentDsh && h.meta.Machine == machine {
			ids = append(ids, id)
		}
	}
	m.mu.Unlock()
	if len(ids) > 0 {
		m.Refresh(ids)
	}
}

// SyncDsh reads again what the dsh at machine (an executor's ID; "" this one) offers, at once, not
// as kept (the user, 2026-11-09: a key in a group's menu, not three minutes' wait): its workspaces
// and sessions, its models, probed now with DSH Desktop's settings as they are, and its presets;
// its sessions there show them. A running agent keeps its own till it starts again.
func (m *Manager) SyncDsh(ctx context.Context, machine string) error {
	m.presetMu.Lock()
	delete(m.presets, machine)
	m.presetMu.Unlock()
	// Its workspaces and sessions too (the user, 2026-11-09), not at the next look.
	if sc, ok := m.o.Discovery.(scanner); ok {
		if machine == "" {
			sc.Scan()
		} else if err := sc.ScanMachine(ctx, machine); err != nil {
			return err
		}
	}
	if _, err := m.catalog(ctx, machine, true); err != nil {
		return err
	}
	m.refreshMachine(machine)
	return nil
}

// scanner is a Discovery that looks again now: here, or at a machine (its dsh's workspaces and
// sessions).
type scanner interface {
	Scan()
	ScanMachine(ctx context.Context, id string) error
}

// catalogState gives a dsh session its machine's models as last probed (over what its agent said
// at its start: a sync shows at once; one picked that its agent has not restarts it), and the
// default as its model while its agent has said none (not started yet).
func (m *Manager) catalogState(s *api.SessionState, machine string) {
	if s.Agent != api.AgentDsh {
		return
	}
	cat, ok := m.cachedCatalog(machine)
	if !ok {
		return
	}
	s.Models = nil
	for _, x := range cat.Models {
		s.Models = append(s.Models, api.Model{Value: x.Value, Name: x.Name, Description: x.Description, Efforts: slices.Clone(x.Efforts), NoEffort: x.NoEffort})
	}
	if s.Model == "" {
		s.Model = cat.Default
	}
}

// catalogHook is a launch's Catalog: dsh's at the session's machine.
func (m *Manager) catalogHook(machine string) func() (agent.Catalog, error) {
	return func() (agent.Catalog, error) { return m.Catalog(m.ctx, machine) }
}
