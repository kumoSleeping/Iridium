package server

import (
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/kumoSleeping/iridium/internal/agents"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/config"
	"github.com/kumoSleeping/iridium/internal/session"
)

// settingsRoutes are the settings page's (docs/api.md "设置"): the config file's fields it edits,
// saved to the file and in force at once.
func (s *Server) settingsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/settings", s.manage(func(w http.ResponseWriter, r *http.Request) { reply(w, http.StatusOK, s.settings()) }))
	mux.HandleFunc("PUT /api/settings", s.panelOnly(s.putSettings))
}

// settings are the live values.
func (s *Server) settings() api.Settings {
	t := s.m.Tuned()
	s.cmu.Lock()
	hidden, push, enter := slices.Clone(s.cfg.QuotaHidden), s.cfg.Push, s.cfg.EnterInTurn
	s.cmu.Unlock()
	if enter == "" {
		enter = "steer"
	}
	if s.Push != nil {
		push = s.Push.Enabled()
	}
	if hidden == nil {
		hidden = []string{}
	}
	modes := t.Modes
	if modes == nil {
		modes = map[string]string{}
	}
	return api.Settings{IdleReap: shortDuration(t.IdleReap), CrashResume: t.CrashResume, QuotaResume: t.QuotaResume, QuotaHidden: hidden, Modes: modes, Push: push, EnterInTurn: enter}
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	var p api.SettingsPatch
	if !readJSON(w, r, &p) {
		return
	}
	var reap time.Duration
	if p.IdleReap != nil {
		d, err := parseDuration(*p.IdleReap)
		if err != nil {
			fail(w, http.StatusBadRequest, "idleReap: "+err.Error())
			return
		}
		reap = d
	}
	if p.EnterInTurn != nil && *p.EnterInTurn != "steer" && *p.EnterInTurn != "queue" {
		fail(w, http.StatusBadRequest, `enterInTurn: "steer" or "queue"`)
		return
	}
	for a, mode := range p.Modes {
		info, ok := agents.Get(api.Agent(a))
		if !ok {
			fail(w, http.StatusBadRequest, "modes: no agent "+a)
			return
		}
		if mode != "" && !slices.Contains(info.Modes, mode) {
			fail(w, http.StatusBadRequest, fmt.Sprintf("modes: %s has no approval mode %q (%s)", info.Name, mode, strings.Join(info.Modes, ", ")))
			return
		}
	}
	err := s.SaveConfig(func(c *config.Config) {
		if p.IdleReap != nil {
			c.IdleReap = config.Duration(reap)
		}
		if p.CrashResume != nil {
			c.CrashResume = *p.CrashResume
		}
		if p.QuotaResume != nil {
			c.QuotaResume = *p.QuotaResume
		}
		if p.QuotaHidden != nil {
			c.QuotaHidden = cleanKeys(*p.QuotaHidden)
		}
		if len(p.Modes) > 0 {
			modes := maps.Clone(c.Modes)
			if modes == nil {
				modes = map[string]string{}
			}
			for a, mode := range p.Modes {
				if mode == "" {
					delete(modes, a)
				} else {
					modes[a] = mode
				}
			}
			c.Modes = modes
		}
		if p.EnterInTurn != nil {
			c.EnterInTurn = *p.EnterInTurn
		}
		if p.Push != nil && s.Push == nil {
			c.Push = *p.Push
		}
	})
	if err == nil && p.Push != nil && s.Push != nil {
		err = s.Push.SetEnabled(*p.Push) // saves the file's push through SaveConfig
	}
	if err != nil {
		s.error(w, err)
		return
	}
	s.logf("server: %s changed the settings", callerOf(r).Name)
	reply(w, http.StatusOK, s.settings())
}

// SaveConfig changes the config: in memory, the session manager's live values, and the file at
// ConfigPath as it is there now (what change leaves alone stays as the file has it). One change
// at a time.
func (s *Server) SaveConfig(change func(*config.Config)) error {
	s.cmu.Lock()
	defer s.cmu.Unlock()
	if s.ConfigPath != "" {
		c, err := config.Load(s.ConfigPath)
		if err != nil {
			return err
		}
		change(&c)
		if err := c.Save(s.ConfigPath); err != nil {
			return err
		}
	}
	change(&s.cfg)
	s.m.Tune(session.TunablesOf(s.cfg))
	return nil
}

// parseDuration takes "1h30m"; "" and "0" are 0, never.
func parseDuration(v string) (time.Duration, error) {
	v = strings.TrimSpace(v)
	if v == "" || v == "0" {
		return 0, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, err
	}
	if d < 0 {
		return 0, fmt.Errorf("%q is below 0", v)
	}
	return d, nil
}

// shortDuration writes d as the settings page offers it: "1h", "15m", "1h30m"; "0" for 0.
func shortDuration(d time.Duration) string {
	if d <= 0 {
		return "0"
	}
	v := d.String()
	if strings.HasSuffix(v, "m0s") {
		v = strings.TrimSuffix(v, "0s")
	}
	if strings.HasSuffix(v, "h0m") {
		v = strings.TrimSuffix(v, "0m")
	}
	return v
}

// cleanKeys drops empty and repeated keys, keeping the order.
func cleanKeys(keys []string) []string {
	out := []string{}
	for _, k := range keys {
		if k = strings.TrimSpace(k); k != "" && !slices.Contains(out, k) {
			out = append(out, k)
		}
	}
	return out
}
