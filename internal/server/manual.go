package server

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/kumoSleeping/iridium/internal/access"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/channel"
	"github.com/kumoSleeping/iridium/internal/gate"
	"github.com/kumoSleeping/iridium/internal/session"
	"github.com/kumoSleeping/iridium/internal/store"
)

// Manual approval asked for (session/manual.go; kumo, 2026-11-25): Claude whose auto mode denied a
// step it needs asks a person to switch it to manual approval for a while (POST
// internal/sessions/<id>/manual, ir api's --wait waiting for the answer). Its ask is carried as
// any ask is, and calls the person as an @ does (askCalls); only a person answers it
// (satoriBackend.Answer).

// askManual opens the ask: only the session's own agent asks it for itself.
func (s *Server) askManual(w http.ResponseWriter, r *http.Request) {
	c, id := callerOf(r), r.PathValue("id")
	if c.session != id {
		forbid(w, errors.New("only the session's own agent asks for manual approval (internal/sessions/<id>/manual)"))
		return
	}
	var req api.ManualAsk
	if !readJSON(w, r, &req) {
		return
	}
	st, err := s.m.AskManual(r.Context(), id, req.Reason, req.Steps)
	if err != nil {
		s.error(w, err)
		return
	}
	reply(w, http.StatusOK, st)
}

// manualState is how the latest ask stands; ?wait=<seconds> (at most 60) waits for its answer.
func (s *Server) manualState(w http.ResponseWriter, r *http.Request) {
	var d time.Duration
	if v := r.URL.Query().Get("wait"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			refuse(w, http.StatusUnprocessableEntity, "bad_wait", "bad wait")
			return
		}
		d = time.Duration(min(n, 60)) * time.Second
	}
	st, err := s.m.ManualState(r.Context(), r.PathValue("id"), d)
	if err != nil {
		s.error(w, err)
		return
	}
	reply(w, http.StatusOK, st)
}

// errManualByAgent answers an agent answering an ask for manual approval.
var errManualByAgent = &access.Denied{Reason: "only a person answers an ask for manual approval"}

// mayAnswerAsk: c may answer session id's ask request, as mayAnswer says; one for manual mode
// only a person.
func (s *Server) mayAnswerAsk(c caller, id, request string) error {
	if c.byAgent() && s.m.AskKind(id, request) == session.AskManual {
		return errManualByAgent
	}
	return s.mayAnswer(c, id)
}

// askCalls: ask a of session's agent was made message seq of channel ch. One for manual mode calls
// the person as an @ does (ats.go): the one who last spoke there before it, else the host.
func (s *Server) askCalls(ch string, seq int64, _ string, a api.Ask) {
	if a.Kind != session.AskManual {
		return
	}
	who := gate.HostID
	if ms, err := s.ch.List(ch, seq, -1, 50); err == nil {
		for i := len(ms) - 1; i >= 0; i-- {
			m := ms[i]
			if _, agent := store.SessionOfMember(m.Author); m.Kind == channel.KindUser && !agent && m.Author != "" {
				who = m.Author
				break
			}
		}
	}
	s.names(ch, seq, fmt.Sprintf(`<at id="%s"/>`, who), "")
}
