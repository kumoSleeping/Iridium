package server

import (
	"sync"

	"github.com/kumoSleeping/iridium/internal/gate"
)

// The login a turn is for (decision 18, draft 二「权限」): set as a person's message (or new
// session) starts it; a message an agent sends with ir from session S, or a session it starts,
// is for S's own turn's login, down the chain to the first person. An agent's ir acts as that
// login (within its app's), not as whoever first opened the session. Kept in memory: after a
// restart, until the next turn, a session's opener stands in.
type origins struct {
	mu sync.Mutex
	m  map[string]gate.Identity
}

// noteOrigin records whose turn session id now runs: c's, or for an agent's ir (via: the session
// it runs in, c.session or the request's caller) that session's turn's.
func (s *Server) noteOrigin(c caller, id, via string) {
	o := c.Identity
	if via == "" {
		via = c.session
	}
	if via != "" && (c.agent || c.socket || c.session != "") {
		if x, ok := s.originOf(via); ok {
			o = x
		}
	}
	s.turns.mu.Lock()
	if s.turns.m == nil {
		s.turns.m = map[string]gate.Identity{}
	}
	s.turns.m[id] = o
	s.turns.mu.Unlock()
}

// originOf is whose turn session id runs: as noted, else its opener (the host if none).
func (s *Server) originOf(id string) (gate.Identity, bool) {
	s.turns.mu.Lock()
	o, ok := s.turns.m[id]
	s.turns.mu.Unlock()
	if ok {
		return o, true
	}
	if s.st == nil {
		return gate.Identity{}, false
	}
	op := s.st.Opener(id)
	if op == "" || op == gate.HostID {
		return gate.HostIdentity, op != ""
	}
	if info, ok := s.gate().Endpoint(op); ok {
		return info.Identity(), true
	}
	return gate.Identity{}, false
}
