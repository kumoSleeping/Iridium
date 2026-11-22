package server

import (
	"cmp"
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/remote"
	"github.com/kumoSleeping/iridium/internal/remote/wire"
)

// Personas from the agents' hosts (docs/channels.md "角色" 从 dsh 人格导入, docs/exec.md
// agent.personas; otae, 2026-11-22): an executor that says wire.CapPersonas is asked for the
// personas its host has, which a page takes as a persona of Iridium's (a copy). All of it comes
// from the host: Iridium knows no host's personas, and with none that gives any it lists none.

// personasWait is how long one executor may take to answer; personasKeep, how long its answer
// is kept.
const (
	personasWait = 5 * time.Second
	personasKeep = 30 * time.Second
)

// personasSeen is an executor's last answer, by the connection it came on.
type personasSeen struct {
	hello *wire.Hello
	at    time.Time
	list  []api.HostPersona
}

// personasCache holds the answers, by machine.
type personasCache struct {
	mu   sync.Mutex
	seen map[string]personasSeen
}

func (s *Server) personasGet(w http.ResponseWriter, r *http.Request) {
	reply(w, http.StatusOK, s.hostPersonas(r.Context()))
}

// hostPersonas are the personas of every executor connected now that gives them, this
// computer's first, each in its host's order.
func (s *Server) hostPersonas(ctx context.Context) []api.HostPersona {
	out := []api.HostPersona{}
	if s.Remote == nil {
		return out
	}
	var ms []*remote.Machine
	for _, id := range s.Remote.Online() {
		if m := s.Remote.Machine(id); m != nil && m.Has(wire.CapPersonas) {
			ms = append(ms, m)
		}
	}
	lists := make([][]api.HostPersona, len(ms))
	var wg sync.WaitGroup
	for i, m := range ms {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lists[i] = s.personasOf(ctx, m)
		}()
	}
	wg.Wait()
	for _, local := range []bool{true, false} {
		for i, m := range ms {
			if m.IsLocal() == local {
				out = append(out, lists[i]...)
			}
		}
	}
	return out
}

// personasOf asks executor m (kept a while, per connection); none if it does not answer.
func (s *Server) personasOf(ctx context.Context, m *remote.Machine) []api.HostPersona {
	hi, _ := m.Hello()
	s.personas.mu.Lock()
	if v, ok := s.personas.seen[m.ID()]; ok && v.hello == hi && time.Since(v.at) < personasKeep {
		s.personas.mu.Unlock()
		return v.list
	}
	s.personas.mu.Unlock()
	machine, name := m.ID(), ""
	if m.IsLocal() {
		machine = ""
	} else if s.Remote.NameOf != nil {
		name = s.Remote.NameOf(machine)
	}
	name = cmp.Or(name, machine)
	var list []api.HostPersona
	for _, a := range m.AgentNames() {
		ctx, cancel := context.WithTimeout(ctx, personasWait)
		var rep wire.PersonasReply
		err := m.Call(ctx, "agent.personas", wire.PersonasParams{Agent: a}, &rep)
		cancel()
		if err != nil {
			s.logf("server: personas of %s at %s: %v", a, m.ID(), err)
			continue
		}
		for _, p := range rep.Personas {
			if p.ID == "" || p.Text == "" {
				continue
			}
			list = append(list, api.HostPersona{Machine: machine, MachineName: name, Agent: api.Agent(a), ID: p.ID, Name: cmp.Or(p.Name, p.ID),
				Description: p.Description, Text: p.Text, From: p.From})
		}
	}
	s.personas.mu.Lock()
	if s.personas.seen == nil {
		s.personas.seen = map[string]personasSeen{}
	}
	s.personas.seen[m.ID()] = personasSeen{hello: hi, at: time.Now(), list: list}
	s.personas.mu.Unlock()
	return list
}
