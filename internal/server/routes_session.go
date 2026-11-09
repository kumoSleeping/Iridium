package server

import (
	"context"
	"net/http"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
)

// sessionRoutes: a session's model, mode, effort, ultracode and other features, preset, its queue
// and its "always allow" rules (docs/api.md "会话设置", "待发队列").
func (s *Server) sessionRoutes(mux *http.ServeMux) {
	// What a terminal may use (access.go): Claude's sessions only the owner's own.
	mux.HandleFunc("PUT /api/sessions/{id}/model", s.use(s.setModel))
	mux.HandleFunc("PUT /api/sessions/{id}/mode", s.use(s.setMode))
	mux.HandleFunc("PUT /api/sessions/{id}/effort", s.use(s.setEffort))
	mux.HandleFunc("PUT /api/sessions/{id}/ultracode", s.use(s.setUltracode))
	mux.HandleFunc("PUT /api/sessions/{id}/preset", s.use(s.setPreset))
	mux.HandleFunc("GET /api/sessions/{id}/presets", s.presets)
	mux.HandleFunc("PUT /api/sessions/{id}/features/{feature}", s.use(s.setFeature))
	mux.HandleFunc("POST /api/sessions/{id}/continue", s.use(s.continueQueue))
	mux.HandleFunc("POST /api/sessions/{id}/queue/{input}/steer", s.use(s.steer))
	mux.HandleFunc("PUT /api/sessions/{id}/queue/{input}", s.use(s.editQueued))
	mux.HandleFunc("DELETE /api/sessions/{id}/queue/{input}", s.use(s.deleteQueued))
	mux.HandleFunc("GET /api/sessions/{id}/rules", s.rules)
	mux.HandleFunc("DELETE /api/sessions/{id}/rules", s.use(s.clearRules))
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

// setFeature switches one of the session's features (api.Feature): Claude's UltraCode as
// setUltracode does, or one a plugin adds where DSH Desktop holds the session.
func (s *Server) setFeature(w http.ResponseWriter, r *http.Request) {
	var req api.SetFeature
	if !readJSON(w, r, &req) {
		return
	}
	ctx, cancel := agentCtx(r)
	defer cancel()
	s.settle(w, s.m.SetFeature(ctx, r.PathValue("id"), r.PathValue("feature"), req))
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

func (s *Server) editQueued(w http.ResponseWriter, r *http.Request) {
	var req api.EditQueued
	if !readJSON(w, r, &req) {
		return
	}
	s.settle(w, s.m.EditQueued(r.PathValue("id"), r.PathValue("input"), req.Text))
}

func (s *Server) deleteQueued(w http.ResponseWriter, r *http.Request) {
	s.settle(w, s.m.DeleteQueued(r.PathValue("id"), r.PathValue("input")))
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
