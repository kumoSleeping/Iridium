package session

import (
	"maps"
	"time"

	"github.com/kumoSleeping/iridium/internal/agents"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/config"
	"github.com/kumoSleeping/iridium/internal/harness"
)

// Tunables are the config's fields the settings page changes while the daemon runs (PUT
// /api/settings): read through Manager.tuned, never Options.Config, so a change takes at once.
type Tunables struct {
	IdleReap    time.Duration // 0: never
	CrashResume bool
	QuotaResume bool
	// Modes: the approval mode a new session of each agent starts in; none: the agent's own.
	Modes map[string]string
	// Lang: the user's language ("zh", "en"; "" not known), told agents from their next start.
	Lang string
}

// TunablesOf are cfg's.
func TunablesOf(cfg config.Config) Tunables {
	return Tunables{IdleReap: cfg.IdleReap.D(), CrashResume: cfg.CrashResume, QuotaResume: cfg.QuotaResume, Modes: maps.Clone(cfg.Modes), Lang: cfg.Lang}
}

// Tune puts t in place of the live values: idle reaping, crash and quota resume from the next
// time they are looked at, the default modes from the next start; a language, the channels at
// once (harness.Retuner: a holder's features' texts).
func (m *Manager) Tune(t Tunables) {
	t.Modes = maps.Clone(t.Modes)
	if old := m.tune.Swap(&t); old != nil && old.Lang != t.Lang {
		for _, c := range m.o.Channels {
			if r, ok := c.(harness.Retuner); ok {
				r.Retuned()
			}
		}
	}
}

// Tuned are the live values.
func (m *Manager) Tuned() Tunables {
	t := *m.tune.Load()
	t.Modes = maps.Clone(t.Modes)
	return t
}

func (m *Manager) tuned() *Tunables { return m.tune.Load() }

// defaultMode is the mode a new process of agent a starts in when its session has none: the
// config's for a, else the agent's own default (agents.Info.DefaultMode).
func (m *Manager) defaultMode(a api.Agent) string {
	if mode := m.tuned().Modes[string(a)]; mode != "" {
		return mode
	}
	if info, ok := agents.Get(a); ok {
		return info.DefaultMode
	}
	return ""
}
