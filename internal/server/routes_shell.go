package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/remote"
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

// machineShell is an agent's ir sh. Asked with Accept: application/x-ndjson, at an executor that
// speaks run, the command line's output streams back as ShellEvent lines as it comes, and its end
// last; else it runs as a holder and its ShellStarted is the answer, read with GET /api/shell/{run}
// (an older executor: the dsh plugin's).
func (s *Server) machineShell(w http.ResponseWriter, r *http.Request) {
	var req api.ShellRun
	if !readJSON(w, r, &req) {
		return
	}
	name := r.PathValue("id")
	id := name
	if s.Remote != nil {
		if mc := s.Remote.Find(name); mc != nil {
			id = mc.ID()
		}
	}
	stream := strings.Contains(r.Header.Get("Accept"), ndjson)
	ctx, cancel := agentCtx(r)
	defer cancel()
	st, run, err := s.m.MachineShell(ctx, id, req.Line, req.Cwd, stream)
	if errors.Is(err, session.ErrOffline) {
		// Said so an agent tells the user, not tries again and again.
		if info, ok := s.gate().Endpoint(id); ok {
			if m := s.computerOf(id); m != nil && m.SeenAt > 0 {
				fail(w, http.StatusConflict, fmt.Sprintf("%s is not connected (it last connected %s ago)", info.Name, roughly(time.Since(time.UnixMilli(m.SeenAt)))))
				return
			}
			if s.computerOf(id) != nil {
				fail(w, http.StatusConflict, fmt.Sprintf("%s is not connected", info.Name))
				return
			}
		}
		refuse(w, http.StatusUnprocessableEntity, "no_such_machine", fmt.Sprintf("no machine %s (ir apps lists them)", name))
		return
	}
	if err != nil {
		s.shellError(w, err)
		return
	}
	if run == nil {
		reply(w, http.StatusOK, st)
		return
	}
	s.streamRun(w, r, run)
}

const ndjson = "application/x-ndjson"

// streamRun writes run's output as ShellEvent lines as it comes, its end last; the caller gone
// (ir sh's --timeout, or killed), the run is stopped.
func (s *Server) streamRun(w http.ResponseWriter, r *http.Request, run *remote.Run) {
	rc := http.NewResponseController(w)
	w.Header().Set("Content-Type", ndjson)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	enc := json.NewEncoder(w)
	send := func(ev api.ShellEvent) bool {
		if enc.Encode(ev) != nil {
			return false
		}
		_ = rc.Flush()
		return true
	}
	_ = rc.Flush()
	stop := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := run.Stop(ctx); err != nil && !errors.Is(err, wire.ErrOffline) {
			s.logf("shell: stopping %s: %v", run.ID(), err)
		}
	}
	for {
		ev, err := run.Next(r.Context())
		if err != nil {
			if r.Context().Err() != nil {
				stop()
			}
			return
		}
		var out api.ShellEvent
		switch {
		case ev.Gone:
			out = api.ShellEvent{Done: true, Error: "the machine's connection ended: the command line was stopped"}
		case ev.Exit != nil:
			out = api.ShellEvent{Done: true, Exit: ev.Exit.Code, Error: ev.Exit.Error}
			if ev.Exit.Signal != nil {
				out.Signal = *ev.Exit.Signal
			}
		case ev.Err:
			out.Err = ev.Data
		default:
			out.Out = ev.Data
		}
		if !send(out) {
			stop()
			return
		}
		if out.Done {
			return
		}
	}
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
		refuse(w, http.StatusForbidden, "no_shell", err.Error())
	case errors.Is(err, session.ErrOffline):
		fail(w, http.StatusConflict, err.Error())
	case errors.As(err, new(*wire.Error)):
		fail(w, http.StatusBadRequest, err.Error())
	default:
		s.error(w, err)
	}
}

// roughly is d as a person says it: 40s, 12m, 3h, 2d.
func roughly(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}
