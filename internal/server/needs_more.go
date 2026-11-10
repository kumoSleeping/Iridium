package server

import (
	"cmp"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/kumoSleeping/iridium/internal/access"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/session"
)

// The needs queue beyond the applications (k_glossary "需要"; draft 二「消息」): every session's
// open asks (questions, approvals, plans) for the logins that may use the session, and what was
// shared with a login of ir_web (share.go). The carried state "needs" holds them all; each stream
// and GET /api/needs gets what its caller may see (needsFor): the applications for a person on
// ir_web or at the host, as before; an ask for whoever may use its session; a share for its login.

// ShareTTL is how long a share waits in its login's needs.
const ShareTTL = 24 * time.Hour

// moreNeeds are the needs kept beside the applications: shares and when each ask was first seen.
type moreNeeds struct {
	mu      sync.Mutex
	shares  []api.Need
	askSeen map[string]int64 // an ask's card: when it was first seen (ms)
}

// wireNeeds carries the sessions' asks and the shares in "needs", published again as asks open
// and close.
func (s *Server) wireNeeds() {
	s.more = &moreNeeds{askSeen: map[string]int64{}}
	s.asks.more = func() []api.Need { return append(s.askNeeds(), s.more.live()...) }
	s.m.Watch(func(n session.Notice) {
		switch n.Kind {
		case session.NoticeAsk, session.NoticeAskDone, session.NoticeStatus, session.NoticeTurnEnd, session.NoticeError:
			s.asks.poke()
		}
	})
	s.asks.poke()
}

// askNeeds are the sessions' open asks as needs, each by its card's ID.
func (s *Server) askNeeds() []api.Need {
	if s.m == nil {
		return nil
	}
	open := s.m.Asking()
	now := time.Now().UnixMilli()
	s.more.mu.Lock()
	defer s.more.mu.Unlock()
	seen := make(map[string]int64, len(open))
	var out []api.Need
	for _, a := range open {
		for _, ask := range a.Asks {
			c := ask.Card
			at := cmp.Or(s.more.askSeen[c.ID], now)
			seen[c.ID] = at
			out = append(out, api.Need{
				ID: c.ID, Kind: api.NeedAsk, Name: cmp.Or(a.Session.Name, a.Session.Title), Asker: string(a.Session.Agent),
				Session: a.Session.ID, CreatedAt: at, Card: c, Origin: a.From,
			})
		}
	}
	s.more.askSeen = seen
	return out
}

// live is the shares not expired.
func (m *moreNeeds) live() []api.Need {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UnixMilli()
	out := m.shares[:0:0]
	keep := m.shares[:0]
	for _, n := range m.shares {
		if now < n.ExpiresAt {
			keep = append(keep, n)
			out = append(out, n)
		}
	}
	m.shares = keep
	return out
}

func (m *moreNeeds) addShare(n api.Need) {
	m.mu.Lock()
	m.shares = append(m.shares, n)
	m.mu.Unlock()
}

// dropShare puts away share id of login to; false if it has none such.
func (m *moreNeeds) dropShare(id, to string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, n := range m.shares {
		if n.ID == id && n.To == to {
			m.shares = append(m.shares[:i], m.shares[i+1:]...)
			return true
		}
	}
	return false
}

// mayUse reports whether c may use session st (answer it, send to it): using's checks.
func (s *Server) mayUse(c caller, st api.Session) bool {
	if c.Host() {
		return true
	}
	if !s.sees(s.permsFor(c), st) || access.Agent(s.rulesFor(c), st.Agent) != nil {
		return false
	}
	return s.checkPerms(c, st.Agent, st.Machine, st.Cwd) == nil
}

// needsFor is what of l caller c sees: applications for a person on ir_web or at the host, asks of
// the sessions it may use, what was shared with it.
func (s *Server) needsFor(c caller, l []api.Need) []api.Need {
	out := make([]api.Need, 0, len(l))
	for _, n := range l {
		switch n.Kind {
		case api.NeedAsk:
			if c.Host() {
				out = append(out, n)
				continue
			}
			if st, err := s.m.State(n.Session); err == nil && s.mayUse(c, st.Session) {
				out = append(out, n)
			}
		case api.NeedShare:
			if c.Web() && n.To == c.Login {
				out = append(out, n)
			}
		default:
			if c.panel() {
				out = append(out, n)
			}
		}
	}
	return out
}

// streamFilter is an event stream's hub.Filter: "needs" as the caller sees it (needsFor), the
// rest as stateFilter has it.
func (s *Server) streamFilter(c caller) func(key string, v []byte) []byte {
	f := s.stateFilter(c)
	return func(key string, v []byte) []byte {
		if key == "needs" {
			var n api.Needs
			if json.Unmarshal(v, &n) != nil {
				return v
			}
			n.Pending = s.needsFor(c, n.Pending)
			b, _ := json.Marshal(n)
			return b
		}
		if f == nil {
			return v
		}
		return f(key, v)
	}
}

// listNeeds is GET /api/needs: what waits that the caller sees. Not an agent's ir.
func (s *Server) listNeeds(w http.ResponseWriter, r *http.Request) {
	c := callerOf(r)
	if c.agent {
		forbid(w, errors.New("an agent's ir sees no needs"))
		return
	}
	n := s.asks.everything()
	n.Pending = s.needsFor(c, n.Pending)
	reply(w, http.StatusOK, n)
}

// dropNeed is DELETE /api/needs/{id}: a share put away by its login.
func (s *Server) dropNeed(w http.ResponseWriter, r *http.Request) {
	c := callerOf(r)
	if !c.Web() || !s.more.dropShare(r.PathValue("id"), c.Login) {
		fail(w, http.StatusNotFound, "no such share")
		return
	}
	s.asks.publish()
	reply(w, http.StatusOK, map[string]any{"ok": true})
}
