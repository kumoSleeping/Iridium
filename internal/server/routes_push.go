package server

import (
	"context"
	"net/http"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
)

// pushRoutes: Web Push (docs/api.md "推送"), under /v1/internal/web/push (decision 32): the web's alone,
// the logins of ir_web and the host. A TOKEN, or an agent's ir, neither switches it nor
// subscribes an address of its own.
func (s *Server) pushRoutes(mux *http.ServeMux) {
	if s.Push == nil {
		return
	}
	mux.HandleFunc("GET /v1/internal/web/push", s.panelOnly(s.pushState))
	mux.HandleFunc("PUT /v1/internal/web/push", s.panelOnly(s.pushSwitch))
	mux.HandleFunc("PUT /v1/internal/web/push/defaults", s.panelOnly(s.pushDefaults))
	mux.HandleFunc("PUT /v1/internal/web/push/rules", s.panelOnly(s.pushRule))
	mux.HandleFunc("POST /v1/internal/web/push/subscriptions", s.panelOnly(s.pushSubscribe))
	mux.HandleFunc("DELETE /v1/internal/web/push/subscriptions", s.panelOnly(s.pushUnsubscribe))
	mux.HandleFunc("POST /v1/internal/web/push/test", s.panelOnly(s.pushTest))
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

// pushDefaults sets the events pushed where no rule says otherwise; it answers with the state.
func (s *Server) pushDefaults(w http.ResponseWriter, r *http.Request) {
	var req api.PushEvents
	if !readJSON(w, r, &req) {
		return
	}
	if err := s.Push.SetDefaults(req); err != nil {
		s.fault(w, err)
		return
	}
	s.pushState(w, r)
}

// pushRule sets a workspace's, a folder's or a session's rule, or drops it (mode ""); it answers
// with the state.
func (s *Server) pushRule(w http.ResponseWriter, r *http.Request) {
	var req api.PushRule
	if !readJSON(w, r, &req) {
		return
	}
	if err := s.Push.SetRule(req); err != nil {
		s.fault(w, err)
		return
	}
	s.pushState(w, r)
}

func (s *Server) pushSubscribe(w http.ResponseWriter, r *http.Request) {
	c := callerOf(r)
	var sub api.PushSubscription
	if !readJSON(w, r, &sub) {
		return
	}
	s.settle(w, s.Push.Subscribe(c.ID(), sub))
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
	c := callerOf(r)
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	n := s.Push.Send(ctx, c.ID(), api.PushMessage{Kind: "test", Title: "Iridium", Body: "推送通了"})
	reply(w, http.StatusOK, map[string]any{"sent": n})
}
