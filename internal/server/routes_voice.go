package server

import (
	"maps"
	"net/http"
	"strconv"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/voice"
)

// A folder group's voice (package voice, docs/voice.md): a guild's voice channel, one a group. The
// core's own, as every route here, so any app's login may be in a room, the page's as another's;
// who sees the group may join. Not a session's: a session ends, is archived, is cleared into
// another, while those who talk are about the folder's work, whichever session they look at.
func (s *Server) voiceRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/internal/guilds/{id}/voice", s.voiceConnect)
	mux.HandleFunc("GET /v1/internal/guilds/{id}/voice/members", func(w http.ResponseWriter, r *http.Request) {
		room, ok := s.voiceRoom(w, r)
		if !ok {
			return
		}
		reply(w, http.StatusOK, s.voice.Members(room))
	})
}

// voiceRoom is the room of the folder group the request names, which its caller must see. It has
// answered when false.
func (s *Server) voiceRoom(w http.ResponseWriter, r *http.Request) (string, bool) {
	id, ok := workspaceID(w, r)
	if !ok {
		return "", false
	}
	if !s.seesGuild(callerOf(r), id) {
		refuse(w, http.StatusUnprocessableEntity, "no_such_guild", "no such folder group")
		return "", false
	}
	return strconv.FormatInt(id, 10), true
}

// seesGuild reports whether folder group id is there and in the caller's scope.
func (s *Server) seesGuild(c caller, id int64) bool {
	if s.Discover == nil {
		return false
	}
	ws, err := s.Discover.Workspaces()
	if err != nil {
		return false
	}
	for _, w := range ws {
		if w.ID == id {
			return c.Host() || len(s.scopeWorkspaces(s.permsFor(c), []api.Workspace{w})) == 1
		}
	}
	return false
}

// The state "voice" carries who is in each group's voice (the rooms with someone in them), by the
// group's id, so a page shows it on the group's row to those not in it; a stream gets the rooms
// its caller sees (stateFilter).
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
	room, ok := s.voiceRoom(w, r)
	if !ok {
		return
	}
	who := voice.Who{App: c.App, Login: c.Login, Name: c.Name}
	if who.Name == "" {
		who.Name = s.Tag(c.ID())
	}
	s.voice.Serve(w, r, room, who)
}
