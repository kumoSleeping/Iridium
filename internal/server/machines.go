package server

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"

	"github.com/kumoSleeping/iridium/internal/access"
	"github.com/kumoSleeping/iridium/internal/agents"
	"github.com/kumoSleeping/iridium/internal/api"
)

// The executors (docs/exec.md) as the page lists them: each one that connected, what it last said
// of itself (kept in the store, so one away is listed too), whether it is connected now.

// WatchMachines follows the executors' coming and going: what they say is kept, the pages hear.
// Called once, after Remote is set and before serving.
func (s *Server) WatchMachines() {
	if s.Remote == nil {
		return
	}
	prev := s.Remote.Changed
	s.Remote.Changed = func(id string) {
		if prev != nil {
			prev(id)
		}
		s.machineSeen(id)
	}
	s.publishMachines()
}

// machineSeen keeps what executor id said, if it is connected, and tells the pages.
func (s *Server) machineSeen(id string) {
	if hi, ok := s.Remote.Machine(id).Hello(); ok && hi != nil {
		m := api.Machine{OS: hi.OS, Arch: hi.Arch, Home: hi.Home, Sep: hi.Sep, Agents: []api.ExecAgent{}}
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
}
