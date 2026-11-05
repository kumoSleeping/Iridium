package server

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
)

// pushRoutes: Web Push (docs/api.md "推送").
func (s *Server) pushRoutes(mux *http.ServeMux) {
	if s.Push == nil {
		return
	}
	mux.HandleFunc("GET /api/push", s.pushState)
	mux.HandleFunc("PUT /api/push", s.pushSwitch)
	mux.HandleFunc("PUT /api/push/workspaces/{ws}", s.pushWorkspace)
	mux.HandleFunc("POST /api/push/subscriptions", s.pushSubscribe)
	mux.HandleFunc("DELETE /api/push/subscriptions", s.pushUnsubscribe)
	mux.HandleFunc("POST /api/push/test", s.pushTest)
}

func (s *Server) pushState(w http.ResponseWriter, r *http.Request) {
	st, err := s.Push.State()
	if err != nil {
		s.fault(w, err)
		return
	}
	reply(w, http.StatusOK, st)
}

func (s *Server) pushSwitch(w http.ResponseWriter, r *http.Request) {
	var req api.PushSwitch
	if !readJSON(w, r, &req) {
		return
	}
	s.settle(w, s.Push.SetEnabled(req.Enabled))
}

func (s *Server) pushWorkspace(w http.ResponseWriter, r *http.Request) {
	ws, err := strconv.ParseInt(r.PathValue("ws"), 10, 64)
	if err != nil || ws < 0 {
		fail(w, http.StatusBadRequest, "bad workspace")
		return
	}
	var req api.PushEvents
	if !readJSON(w, r, &req) {
		return
	}
	s.settle(w, s.Push.SetWorkspace(ws, req))
}

func (s *Server) pushSubscribe(w http.ResponseWriter, r *http.Request) {
	c, ok := s.caller(w, r)
	if !ok {
		return
	}
	var sub api.PushSubscription
	if !readJSON(w, r, &sub) {
		return
	}
	s.settle(w, s.Push.Subscribe(c.ID, sub))
}

func (s *Server) pushUnsubscribe(w http.ResponseWriter, r *http.Request) {
	var req api.PushUnsubscribe
	if !readJSON(w, r, &req) {
		return
	}
	s.settle(w, s.Push.Unsubscribe(req.Endpoint))
}

// pushTest pushes a test message to the caller's subscriptions, the switch on or off.
func (s *Server) pushTest(w http.ResponseWriter, r *http.Request) {
	c, ok := s.caller(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	n := s.Push.Send(ctx, c.ID, api.PushMessage{Kind: "test", Title: "Iridium", Body: "推送通了"})
	reply(w, http.StatusOK, map[string]any{"sent": n})
}
