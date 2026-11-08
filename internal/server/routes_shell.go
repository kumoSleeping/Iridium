package server

import (
	"errors"
	"io/fs"
	"net/http"
	"strconv"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/remote/wire"
	"github.com/kumoSleeping/iridium/internal/session"
)

// shellRoutes: command lines run at a session's machine and folder, without its agent (docs/api.md
// "命令"). Only panels and 本机: a Bot asks agents, it does not run commands.
func (s *Server) shellRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/sessions/{id}/shell", s.manages(s.use(s.shellRun)))
	mux.HandleFunc("GET /api/shell/{run}", s.manages(s.shellRead))
	mux.HandleFunc("DELETE /api/shell/{run}", s.manages(s.shellStop))
	// An agent's ir sh: at a machine by ID or name, where its owner lets agents.
	mux.HandleFunc("POST /api/machines/{id}/shell", s.manages(s.machineShell))
}

func (s *Server) machineShell(w http.ResponseWriter, r *http.Request) {
	var req api.ShellRun
	if !readJSON(w, r, &req) {
		return
	}
	id := r.PathValue("id")
	if s.Remote != nil {
		if mc := s.Remote.Find(id); mc != nil {
			id = mc.ID()
		}
	}
	ctx, cancel := agentCtx(r)
	defer cancel()
	st, err := s.m.MachineShell(ctx, id, req.Line, req.Cwd)
	if err != nil {
		s.shellError(w, err)
		return
	}
	reply(w, http.StatusOK, st)
}

func (s *Server) shellRun(w http.ResponseWriter, r *http.Request) {
	var req api.ShellRun
	if !readJSON(w, r, &req) {
		return
	}
	ctx, cancel := agentCtx(r)
	defer cancel()
	st, err := s.m.Shell(ctx, r.PathValue("id"), req.Line, req.Cwd)
	if err != nil {
		s.shellError(w, err)
		return
	}
	reply(w, http.StatusOK, st)
}

func (s *Server) shellRead(w http.ResponseWriter, r *http.Request) {
	from, _ := strconv.ParseInt(r.URL.Query().Get("from"), 10, 64)
	out, err := s.m.ShellRead(r.PathValue("run"), max(0, from))
	if err != nil {
		s.shellError(w, err)
		return
	}
	reply(w, http.StatusOK, out)
}

func (s *Server) shellStop(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := agentCtx(r)
	defer cancel()
	s.settle(w, s.m.ShellStop(ctx, r.PathValue("run")))
}

func (s *Server) shellError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, session.ErrNoRun), errors.Is(err, fs.ErrNotExist), wire.IsCode(err, wire.CodeNotFound):
		fail(w, http.StatusNotFound, err.Error())
	case errors.Is(err, session.ErrNoShell):
		fail(w, http.StatusForbidden, err.Error())
	case errors.Is(err, session.ErrOffline):
		fail(w, http.StatusConflict, err.Error())
	case errors.As(err, new(*wire.Error)):
		fail(w, http.StatusBadRequest, err.Error())
	default:
		s.error(w, err)
	}
}
