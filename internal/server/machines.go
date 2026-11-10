package server

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/kumoSleeping/iridium/internal/access"
	"github.com/kumoSleeping/iridium/internal/agents"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/remote/wire"
)

// The executors (docs/exec.md) as the page lists them: each one that connected, what it last said
// of itself (kept in the store, so one away is listed too), whether it is connected now.

// WatchMachines follows the executors' coming and going: what they say is kept, the pages hear.
// Called once, after Remote is set and before serving.
func (s *Server) WatchMachines() {
	if s.Remote == nil {
		return
	}
	// Found by its app's name now, not the one it connected with (an app renamed since).
	s.Remote.NameOf = func(id string) string {
		if info, ok := s.gate().Endpoint(id); ok {
			return info.Name
		}
		return ""
	}
	prev := s.Remote.Changed
	s.Remote.Changed = func(id string) {
		if prev != nil {
			prev(id)
		}
		s.machineSeen(id)
	}
	s.publishMachines()
	go s.watchLatency()
}

// SlowRTT: a round trip above it is a slow connection (the page's yellow lamp).
const SlowRTT = 300 * time.Millisecond

// watchLatency tells the pages again when a machine's round trip has moved enough to show: its
// first one on a connection, crossing SlowRTT, or by a quarter and LatencyStep at once. The
// heartbeat measures it every wire.PingEvery; the page shows it by the group's lamp.
func (s *Server) watchLatency() {
	type seen struct {
		c   *wire.Conn
		rtt time.Duration
	}
	last := map[string]seen{}
	for range time.Tick(10 * time.Second) {
		changed := false
		online := s.Remote.Online()
		for id := range last {
			if !slices.Contains(online, id) {
				delete(last, id)
			}
		}
		for _, id := range online {
			mc := s.Remote.Machine(id)
			if mc == nil {
				continue
			}
			c, err := mc.Conn()
			if err != nil || c.RTT() == 0 {
				continue
			}
			if was, now := last[id], c.RTT(); was.c != c || latencyMoved(was.rtt, now) {
				last[id], changed = seen{c, now}, true
			}
		}
		if changed {
			s.publishMachines()
		}
	}
}

// LatencyStep: a round trip changed by less is not told again.
const LatencyStep = 20 * time.Millisecond

func latencyMoved(was, now time.Duration) bool {
	if (was > SlowRTT) != (now > SlowRTT) {
		return true
	}
	d := now - was
	if d < 0 {
		d = -d
	}
	return d >= LatencyStep && d*4 >= was
}

// machineSeen keeps what executor id said, if it is connected, and tells the pages. One gone
// with its app (Drop) is not found: nothing to keep.
func (s *Server) machineSeen(id string) {
	var hi *wire.Hello
	ok := false
	if mc := s.Remote.Find(id); mc != nil {
		hi, ok = mc.Hello()
	}
	if ok && hi != nil {
		m := api.Machine{OS: hi.OS, Arch: hi.Arch, Home: hi.Home, Sep: hi.Sep, Agents: []api.ExecAgent{}, Shell: hi.ShellOn()}
		for _, a := range hi.Agents {
			// Only what Iridium runs at an executor.
			if info, ok := agents.Get(api.Agent(a.Agent)); ok && info.FindRemote != nil {
				m.Agents = append(m.Agents, api.ExecAgent{Agent: a.Agent, Version: a.Version})
			}
		}
		if b, err := json.Marshal(m); err == nil {
			if err := s.st.SetMachine(id, b); err != nil {
				s.logf("server: keeping machine %s: %v", id, err)
			}
		}
	}
	s.publishMachines()
}

// machines lists the executors whose Bot is still there, by name.
func (s *Server) machines() []api.Machine {
	out := []api.Machine{}
	rows, err := s.st.Machines()
	if err != nil {
		s.logf("server: machines: %v", err)
		return out
	}
	for _, r := range rows {
		info, ok := s.gate().Endpoint(r.Terminal)
		if !ok {
			continue
		}
		var m api.Machine
		if json.Unmarshal(r.Info, &m) != nil {
			continue
		}
		m.ID, m.Name, m.Avatar, m.SeenAt = r.Terminal, info.Name, info.Avatar, r.At
		// Claude only at a machine allowed as the owner's own.
		claude := access.HasClaude(s.rulesOf(r.Terminal, false))
		m.Agents = slices.DeleteFunc(m.Agents, func(a api.ExecAgent) bool { return a.Agent == string(api.AgentClaude) && !claude })
		if m.Agents == nil {
			m.Agents = []api.ExecAgent{}
		}
		if s.Remote != nil {
			if mc := s.Remote.Find(r.Terminal); mc != nil {
				_, m.Online = mc.Hello()
				if c, err := mc.Conn(); err == nil && m.Online {
					m.Latency = c.RTT().Milliseconds()
				}
			}
		}
		out = append(out, m)
	}
	slices.SortFunc(out, func(a, b api.Machine) int { return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) })
	return out
}

func (s *Server) publishMachines() { s.hub.Set("machines", s.machines()) }

func (s *Server) machineRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/machines", func(w http.ResponseWriter, r *http.Request) { reply(w, http.StatusOK, s.machines()) })
	mux.HandleFunc("GET /api/machines/{id}/dsh/workspaces", s.dshWorkspaces)
	mux.HandleFunc("POST /api/machines/{id}/dsh/sync", s.dshSync)
}
