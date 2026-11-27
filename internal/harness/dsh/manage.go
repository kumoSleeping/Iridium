package dsh

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/harness"
	"github.com/kumoSleeping/iridium/internal/remote"
)

// ComputerState reports dsh's per-computer configuration state: the "sync new sessions" toggle
// and, on the host computer alone, whether a manual Desktop install path fallback is needed.
func (c *Channel) ComputerState(machine string) harness.ComputerState {
	var st harness.ComputerState
	if c.o.GetSync != nil {
		on := c.o.GetSync(machine)
		st.Sync = &on
	}
	if machine == "" {
		if mc := c.inprocHost(""); mc != nil {
			if ai, ok := mc.HostAgent(string(api.AgentDsh)); ok && ai.Version != "" {
				st.Version = ai.Version
			}
			return st
		}
		if c.o.StateDir != "" {
			inst, ok := DiscoverDesktop(DesktopDiscoverOptions{StateDir: c.o.StateDir})
			if !ok || inst.Source == "manual" {
				st.PathNeeded = true
				st.Path = LoadManualDesktopPath(c.o.StateDir)
			}
		}
	}
	return st
}

// SetComputerConfig updates dsh's per-computer "sync new sessions" toggle (pushing it to the
// connected Desktop plugin when online for bidirectional sync) and/or the host's manual Desktop path.
func (c *Channel) SetComputerConfig(ctx context.Context, machine string, cfg harness.ComputerConfig) (harness.ComputerState, error) {
	if cfg.Path != nil {
		if machine != "" {
			return harness.ComputerState{}, fmt.Errorf("%w: desktop path is host-only", harness.ErrBadRequest)
		}
		p := strings.TrimSpace(*cfg.Path)
		if p == "" {
			if err := SaveManualDesktopPath(c.o.StateDir, ""); err != nil {
				return harness.ComputerState{}, err
			}
			if inst, ok := LoadDesktopInstall(c.o.StateDir); ok && inst.Source == "manual" {
				_ = os.Remove(filepath.Join(c.o.StateDir, desktopInstallFile))
			}
		} else {
			inst, ok := ResolveDesktopRoot(p)
			if !ok {
				return harness.ComputerState{}, fmt.Errorf("%w: no DSH Desktop install at %s", harness.ErrBadRequest, p)
			}
			inst.Source = "manual"
			if err := SaveManualDesktopPath(c.o.StateDir, p); err != nil {
				return harness.ComputerState{}, err
			}
			if err := SaveDesktopInstall(c.o.StateDir, inst); err != nil {
				return harness.ComputerState{}, err
			}
		}
	}
	if cfg.Sync != nil {
		if c.o.SetSync != nil {
			c.o.SetSync(machine, *cfg.Sync)
		}
		if mc := c.inprocHost(machine); mc != nil {
			pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			if _, err := desktopConfigSet(pctx, mc, desktopConfigSetParams{SyncNewSessions: cfg.Sync}); err != nil {
				c.o.Logf("dsh: desktop.config.set at %q: %v", machine, err)
			}
			cancel()
		}
	}
	return c.ComputerState(machine), nil
}

// SearchExternal queries local and online remote DSH Desktop hosts on demand for sessions matching query.
func (c *Channel) SearchExternal(ctx context.Context, query string, limit int) ([]api.ExternalSession, error) {
	if c.o.Remote == nil {
		return nil, nil
	}
	query = strings.TrimSpace(query)
	if limit <= 0 {
		limit = 30
	}
	if limit > 100 {
		limit = 100
	}
	type target struct {
		machine string // "" = host
		mc      *remote.Machine
	}
	var targets []target
	if mc := c.inprocHost(""); mc != nil {
		targets = append(targets, target{machine: "", mc: mc})
	}
	for _, id := range c.o.Remote.Online() {
		mc := c.o.Remote.Machine(id)
		if mc == nil || mc.IsLocal() || !mc.Has(CapInproc) {
			continue
		}
		targets = append(targets, target{machine: id, mc: mc})
	}
	if len(targets) == 0 {
		return nil, nil
	}
	var (
		mu   sync.Mutex
		wg   sync.WaitGroup
		out  []api.ExternalSession
		seen = map[string]bool{}
	)
	for _, t := range targets {
		wg.Add(1)
		go func(t target) {
			defer wg.Done()
			qctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			rep, err := desktopSessions(qctx, t.mc, DesktopSessionsParams{Query: query, Limit: limit})
			if err != nil {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			for _, s := range rep.Sessions {
				if s.ID == "" || s.Sub {
					continue
				}
				key := t.machine + "\x00" + s.ID
				if seen[key] {
					continue
				}
				seen[key] = true
				updated := s.UpdatedAt
				if updated == 0 {
					updated = s.Last
				}
				out = append(out, api.ExternalSession{
					ID:        s.ID,
					Agent:     api.AgentDsh,
					Machine:   t.machine,
					Cwd:       s.Cwd,
					Title:     s.Title,
					UpdatedAt: updated,
					Archived:  s.Archived,
				})
			}
		}(t)
	}
	wg.Wait()
	slices.SortFunc(out, func(a, b api.ExternalSession) int {
		if d := cmp.Compare(b.UpdatedAt, a.UpdatedAt); d != 0 {
			return d
		}
		return cmp.Compare(a.ID, b.ID)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// AdoptExternal brings a session picked from SearchExternal into Iridium's session list.
func (c *Channel) AdoptExternal(ctx context.Context, s api.ExternalSession) error {
	if c.o.Adopt == nil {
		return fmt.Errorf("%w: adopt not configured", harness.ErrBadRequest)
	}
	if s.ID == "" {
		return fmt.Errorf("%w: session id required", harness.ErrBadRequest)
	}
	return c.o.Adopt(ctx, s.Machine, DesktopSession{
		ID:        s.ID,
		Cwd:       s.Cwd,
		Title:     s.Title,
		Last:      s.UpdatedAt,
		UpdatedAt: s.UpdatedAt,
		Archived:  s.Archived,
	})
}
