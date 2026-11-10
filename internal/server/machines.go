package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/kumoSleeping/iridium/internal/access"
	"github.com/kumoSleeping/iridium/internal/agents"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/gate"
	"github.com/kumoSleeping/iridium/internal/remote/wire"
)

// The computers of the apps that run agents or command lines (docs/exec.md "握手"; draft 二「带
// Agent」: no machines apart from apps): what each executor last said of itself (kept in the store,
// so one away is listed too), whether it is connected now, which of its agents are turned off.
// GET /api/apps lists them with their apps; the pages fetch it again on the carried "terminals".

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
	s.terminalsChanged()
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
			s.terminalsChanged()
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
		m := api.Computer{OS: hi.OS, Arch: hi.Arch, Home: hi.Home, Sep: hi.Sep, Agents: []api.ExecAgent{}, Shell: hi.ShellOn()}
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
	s.terminalsChanged()
}

// computers is the computer of each app that has one, by its ID: as it last said, Claude only
// at one allowed as the owner's own, the agents turned off there marked, whether it is connected.
func (s *Server) computers() map[string]*api.Computer {
	out := map[string]*api.Computer{}
	if s.st == nil {
		return out
	}
	rows, err := s.st.Machines()
	if err != nil {
		s.logf("server: machines: %v", err)
		return out
	}
	for _, r := range rows {
		var m api.Computer
		if json.Unmarshal(r.Info, &m) != nil {
			continue
		}
		m.SeenAt = r.At
		claude := access.HasClaude(s.rulesOf(r.Terminal, false))
		m.Agents = slices.DeleteFunc(m.Agents, func(a api.ExecAgent) bool { return a.Agent == string(api.AgentClaude) && !claude })
		off, _ := s.st.AgentsOff(r.Terminal)
		for i := range m.Agents {
			m.Agents[i].Off = slices.Contains(off, m.Agents[i].Agent)
		}
		if m.Agents == nil {
			m.Agents = []api.ExecAgent{}
		}
		m.Online, m.Latency = false, 0
		if s.Remote != nil {
			if mc := s.Remote.Find(r.Terminal); mc != nil {
				_, m.Online = mc.Hello()
				if c, err := mc.Conn(); err == nil && m.Online {
					m.Latency = c.RTT().Milliseconds()
				}
			}
		}
		out[r.Terminal] = &m
	}
	return out
}

// computerOf is app id's computer; nil if it has none.
func (s *Server) computerOf(id string) *api.Computer { return s.computers()[id] }

// putAgents turns agents app {id} carries off or on (PUT /api/apps/{id}/agents): the ones listed
// off, the rest on. One turned off is not started there, nor offered for new sessions; its sessions
// running go on.
func (s *Server) putAgents(w http.ResponseWriter, r *http.Request) {
	info, ok := s.gate().Find(r.PathValue("id"))
	if !ok || info.Kind != gate.KindChannel || info.Host() {
		refuse(w, http.StatusUnprocessableEntity, "no_such_app", "no such app")
		return
	}
	var req api.AppAgents
	if !readJSON(w, r, &req) {
		return
	}
	off := cleanAgents(req.Off)
	if _, err := s.st.SetAgentsOff(info.ID, off); err != nil {
		s.error(w, err)
		return
	}
	s.logf("server: %s turned off %v at %s", callerOf(r).Name, off, info.Name)
	s.terminalsChanged()
	reply(w, http.StatusOK, api.AppAgents{Off: off})
}

// cleanAgents is a list of agents' names within bounds: short, no repeats, a few.
func cleanAgents(in []string) []string {
	out := []string{}
	for _, a := range in {
		a = truncRunes(strings.TrimSpace(a), 32)
		if a != "" && !slices.Contains(out, a) && len(out) < 16 {
			out = append(out, a)
		}
	}
	return out
}

// agentOff is a refusal if agent a is turned off at app id.
func (s *Server) agentOff(id string, a api.Agent) error {
	if off, _ := s.st.AgentsOff(id); slices.Contains(off, string(a)) {
		name := id
		if info, ok := s.gate().Endpoint(id); ok {
			name = info.Name
		}
		return &access.Denied{Reason: fmt.Sprintf("%s is turned off at %s", a, name)}
	}
	return nil
}

func (s *Server) machineRoutes(mux *http.ServeMux) {
	mux.HandleFunc("PUT /api/apps/{id}/agents", s.approver(s.putAgents))
	mux.HandleFunc("GET /api/machines/{id}/dsh/workspaces", s.dshWorkspaces)
	mux.HandleFunc("POST /api/machines/{id}/dsh/sync", s.dshSync)
}
