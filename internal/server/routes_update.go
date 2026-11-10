package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/update"
	"github.com/kumoSleeping/iridium/internal/version"
)

// updateRoutes are the update's (docs/api.md "更新"): the state, looking now, installing.
func (s *Server) updateRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/update", func(w http.ResponseWriter, r *http.Request) { reply(w, http.StatusOK, s.updateState()) })
	mux.HandleFunc("POST /api/update/check", s.admin(func(w http.ResponseWriter, r *http.Request) {
		if s.Updater == nil {
			fail(w, http.StatusConflict, "no updater")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		st, err := s.Updater.Check(ctx)
		if err != nil {
			fail(w, http.StatusBadGateway, err.Error())
			return
		}
		reply(w, http.StatusOK, st)
	}))
	mux.HandleFunc("POST /api/update", s.approver(func(w http.ResponseWriter, r *http.Request) {
		var req api.ApplyUpdate
		if r.ContentLength != 0 && !readJSON(w, r, &req) {
			return
		}
		if s.Updater == nil {
			fail(w, http.StatusConflict, "no updater")
			return
		}
		st, err := s.Updater.Apply(r.Context(), req.Force)
		switch {
		case errors.Is(err, update.ErrUpToDate):
			fail(w, http.StatusConflict, err.Error())
		case err != nil:
			fail(w, http.StatusBadGateway, err.Error())
		default:
			s.logf("server: %s installed %s; restarting", callerOf(r).Name, st.Latest)
			reply(w, http.StatusOK, st)
		}
	}))
}

func (s *Server) updateState() api.UpdateState {
	if s.Updater == nil {
		return api.UpdateState{Current: version.Version, Phase: "idle"}
	}
	return s.Updater.State()
}
