package server

import (
	"context"
	"net/http"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/session"
)

// sessionRoutes: a session's model, mode, effort, ultracode, preset, what an app attaches to its
// page, its queue and its "always allow" rules (docs/api.md "会话设置", "待发队列").
func (s *Server) sessionRoutes(mux *http.ServeMux) {
	// What a terminal may use (access.go): Claude's sessions only the owner's own.
	mux.HandleFunc("PUT /v1/internal/sessions/{id}/model", s.use(s.setModel))
	mux.HandleFunc("PUT /v1/internal/sessions/{id}/mode", s.use(s.setMode))
	mux.HandleFunc("PUT /v1/internal/sessions/{id}/effort", s.use(s.setEffort))
	mux.HandleFunc("PUT /v1/internal/sessions/{id}/ultracode", s.use(s.setUltracode))
	mux.HandleFunc("PUT /v1/internal/sessions/{id}/preset", s.use(s.setPreset))
	mux.HandleFunc("GET /v1/internal/sessions/{id}/presets", s.presets)
	mux.HandleFunc("PUT /v1/internal/sessions/{id}/attach/{place}", s.use(s.attach))
	mux.HandleFunc("DELETE /v1/internal/sessions/{id}/attach/{place}", s.use(s.attach))
	mux.HandleFunc("POST /v1/internal/sessions/{id}/continue", s.use(s.continueQueue))
	mux.HandleFunc("POST /v1/internal/sessions/{id}/queue/{input}/steer", s.use(s.steer))
	mux.HandleFunc("GET /v1/internal/sessions/{id}/rules", s.rules)
	mux.HandleFunc("DELETE /v1/internal/sessions/{id}/rules", s.use(s.clearRules))
	s.shellRoutes(mux)
}

func (s *Server) settle(w http.ResponseWriter, err error) {
	if err != nil {
		s.fault(w, err)
		return
	}
	reply(w, http.StatusOK, map[string]any{"ok": true})
}

func agentCtx(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), 30*time.Second)
}

func (s *Server) setModel(w http.ResponseWriter, r *http.Request) {
	var req api.SetModel
	if !readJSON(w, r, &req) {
		return
	}
	ctx, cancel := agentCtx(r)
	defer cancel()
	s.settle(w, s.m.SetModel(ctx, r.PathValue("id"), req.Model))
}

func (s *Server) setMode(w http.ResponseWriter, r *http.Request) {
	var req api.SetMode
	if !readJSON(w, r, &req) {
		return
	}
	ctx, cancel := agentCtx(r)
	defer cancel()
	s.settle(w, s.m.SetMode(ctx, r.PathValue("id"), req.Mode))
}

func (s *Server) setPreset(w http.ResponseWriter, r *http.Request) {
	var req api.SetPreset
	if !readJSON(w, r, &req) {
		return
	}
	ctx, cancel := agentCtx(r)
	defer cancel()
	s.settle(w, s.m.SetPreset(ctx, r.PathValue("id"), req.Preset))
}

// presets are the agent presets the session may run with: those its agent registers where it runs.
func (s *Server) presets(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := agentCtx(r)
	defer cancel()
	l, err := s.m.SessionPresets(ctx, r.PathValue("id"))
	if err != nil {
		s.fault(w, err)
		return
	}
	reply(w, http.StatusOK, l)
}

func (s *Server) setEffort(w http.ResponseWriter, r *http.Request) {
	var req api.SetEffort
	if !readJSON(w, r, &req) {
		return
	}
	ctx, cancel := agentCtx(r)
	defer cancel()
	s.settle(w, s.m.SetEffort(ctx, r.PathValue("id"), req.Effort))
}

func (s *Server) setUltracode(w http.ResponseWriter, r *http.Request) {
	var req api.SetUltracode
	if !readJSON(w, r, &req) {
		return
	}
	ctx, cancel := agentCtx(r)
	defer cancel()
	s.settle(w, s.m.SetUltracode(ctx, r.PathValue("id"), req.Ultracode))
}

// attach puts an app's Satori elements at a place on the session's page (DELETE, or empty content:
// takes them away); a button pressed in them comes to the app as interaction/button. Apps only:
// not a person's page, the host, nor an agent's ir (an executor's plugin attaches through its
// connection).
func (s *Server) attach(w http.ResponseWriter, r *http.Request) {
	c := callerOf(r)
	if c.App == "" || c.Web() || c.Host() || c.socket || c.agent || c.session != "" {
		refuse(w, http.StatusForbidden, "no_permission", "apps attach to a session's page")
		return
	}
	var content map[string]string
	if r.Method == http.MethodPut {
		var req api.Attach
		if !readJSON(w, r, &req) {
			return
		}
		var err error
		if content, err = session.AttachContent(req.Content); err != nil {
			s.fault(w, err)
			return
		}
	}
	id := r.PathValue("id")
	if ref, ok := s.m.SessionID(id); ok {
		id = ref
	}
	s.settle(w, s.m.Attach(id, session.FromApp(c.App), r.PathValue("place"), content))
}

func (s *Server) continueQueue(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := agentCtx(r)
	defer cancel()
	s.settle(w, s.m.Continue(ctx, r.PathValue("id")))
}

func (s *Server) steer(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := agentCtx(r)
	defer cancel()
	s.settle(w, s.m.Steer(ctx, r.PathValue("id"), r.PathValue("input")))
}

func (s *Server) rules(w http.ResponseWriter, r *http.Request) {
	l, err := s.m.Rules(r.PathValue("id"))
	if err != nil {
		s.fault(w, err)
		return
	}
	reply(w, http.StatusOK, l)
}

func (s *Server) clearRules(w http.ResponseWriter, r *http.Request) {
	s.settle(w, s.m.ClearRules(r.PathValue("id")))
}
