package server

import (
	"maps"
	"net/http"

	"github.com/kumoSleeping/iridium/internal/voice"
)

// A channel's voice (package voice, docs/voice.md): the core's own, as every route here, so any
// app's login may be in a room, the page's as another's. sessionGuard has checked that the caller
// sees the session.
func (s *Server) voiceRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/internal/sessions/{id}/voice", s.voiceConnect)
	mux.HandleFunc("GET /v1/internal/sessions/{id}/voice/members", func(w http.ResponseWriter, r *http.Request) {
		st, err := s.seeing(callerOf(r), r.PathValue("id"))
		if err != nil {
			s.error(w, err)
			return
		}
		reply(w, http.StatusOK, s.voice.Members(st.ID))
	})
}

// The state "voice" carries who is in each channel's voice (the rooms with someone in them), so a
// page shows it on the channel's row to those not in it; a stream gets the rooms its caller sees
// (stateFilter).
func (s *Server) carryVoice(room string, members []voice.Member) {
	s.voiceMu.Lock()
	defer s.voiceMu.Unlock()
	if s.voiceNow == nil {
		s.voiceNow = map[string][]voice.Member{}
	}
	if len(members) == 0 {
		delete(s.voiceNow, room)
	} else {
		s.voiceNow[room] = members
	}
	s.hub.Set("voice", maps.Clone(s.voiceNow))
}

// voiceConnect takes a member's WebSocket: a login (of the page or another app), or the host's
// page. Not an agent's ir: it has no ears.
func (s *Server) voiceConnect(w http.ResponseWriter, r *http.Request) {
	c := callerOf(r)
	if c.socket || c.agent || c.session != "" || (c.Login == "" && !c.Host()) {
		refuse(w, http.StatusForbidden, "no_login", "voice takes a login")
		return
	}
	st, err := s.seeing(c, r.PathValue("id"))
	if err != nil {
		s.error(w, err)
		return
	}
	who := voice.Who{App: c.App, Login: c.Login, Name: c.Name}
	if who.Name == "" {
		who.Name = s.Tag(c.ID())
	}
	s.voice.Serve(w, r, st.ID, who)
}
