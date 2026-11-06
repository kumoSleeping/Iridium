package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/kumoSleeping/iridium/internal/access"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/config"
	"github.com/kumoSleeping/iridium/internal/gate"
)

// terminalRoutes are the interactive terminals' (docs/api.md "交互终端"): who is calling, the
// list (ir access), invites and tokens, names and avatars, permissions, revoking, the
// confirmations agents' ir asks panels for, and public.origin.
func (s *Server) terminalRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/me", s.me)
	mux.HandleFunc("PUT /api/me/profile", func(w http.ResponseWriter, r *http.Request) { s.gate().ServeProfile(w, r, "") })
	// Another terminal's: panels and 本机 only (the gate checks).
	profile := func(w http.ResponseWriter, r *http.Request) { s.gate().ServeProfile(w, r, r.PathValue("id")) }
	mux.HandleFunc("PUT /api/endpoints/{id}/profile", profile)
	mux.HandleFunc("PUT /api/terminals/{id}/profile", profile)
	// The device it runs on: its own, or (panels and 本机) another's.
	mux.HandleFunc("PUT /api/me/device", func(w http.ResponseWriter, r *http.Request) { s.setDevice(w, r, "") })
	mux.HandleFunc("PUT /api/terminals/{id}/device", func(w http.ResponseWriter, r *http.Request) { s.setDevice(w, r, r.PathValue("id")) })
	mux.HandleFunc("GET /api/terminals", s.manage(s.terminals))
	mux.HandleFunc("DELETE /api/terminals/{id}", s.manage(s.revoke))
	mux.HandleFunc("PUT /api/terminals/{id}/rules", s.manage(s.setRules))
	mux.HandleFunc("POST /api/terminals/invites", s.manage(s.invite))
	mux.HandleFunc("POST /api/terminals/tokens", s.manage(s.token))
	mux.HandleFunc("GET /api/confirms", s.panelOnly(func(w http.ResponseWriter, r *http.Request) { reply(w, http.StatusOK, s.asks.pending()) }))
	mux.HandleFunc("POST /api/confirms/{id}", s.panelOnly(s.answerConfirm))
	mux.HandleFunc("GET /api/confirms/{id}", s.manage(s.confirmState))
	mux.HandleFunc("GET /api/public", s.manage(s.publicState))
	mux.HandleFunc("PUT /api/public", s.panelOnly(s.setPublic))
}

// manage lets panels and 本机 (also an agent's ir) through; channels manage nothing.
func (s *Server) manage(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !callerOf(r).CanManage() {
			forbid(w, errors.New("only a web panel or 本机 manages terminals"))
			return
		}
		h(w, r)
	}
}

// panelOnly lets through what may approve, widen and see secrets: panels, and a person's ir.
func (s *Server) panelOnly(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !callerOf(r).panel() {
			forbid(w, errors.New("only a web panel may do this"))
			return
		}
		h(w, r)
	}
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	c := callerOf(r)
	info, ok := s.gate().Endpoint(c.EndpointID)
	if !ok {
		fail(w, http.StatusNotFound, "no such terminal")
		return
	}
	t := s.terminalOf(info)
	t.Local = c.Local
	if c.Local {
		t.Rules = access.Full()
	}
	reply(w, http.StatusOK, api.Me{Terminal: t, Panel: c.panel(), Public: s.gate().Public()})
}

func (s *Server) terminals(w http.ResponseWriter, r *http.Request) {
	a := s.gate().Access()
	out := api.Terminals{Terminals: []api.Terminal{}, Invites: []api.PendingInvite{}}
	local, _ := s.gate().Endpoint(gate.LocalEndpointID)
	t := s.terminalOf(local)
	t.Rules = access.Full()
	out.Terminals = append(out.Terminals, t)
	for _, e := range a.Endpoints {
		out.Terminals = append(out.Terminals, s.terminalOf(e))
	}
	for _, i := range a.Invites {
		out.Invites = append(out.Invites, api.PendingInvite{ID: i.ID, Name: i.Name, Setup: i.Setup, Owner: i.Owner, CreatedAt: ms(i.CreatedAt), ExpiresAt: ms(i.ExpiresAt)})
	}
	reply(w, http.StatusOK, out)
}

func (s *Server) revoke(w http.ResponseWriter, r *http.Request) {
	ref := r.PathValue("id")
	if ref == gate.LocalEndpointID || ref == gate.LocalName {
		fail(w, http.StatusConflict, "本机 is built in")
		return
	}
	s.gateDone(w, s.gate().Revoke(ref))
}

func (s *Server) setRules(w http.ResponseWriter, r *http.Request) {
	var req api.SetRules
	if !readJSON(w, r, &req) {
		return
	}
	id := r.PathValue("id")
	if id == gate.LocalEndpointID {
		fail(w, http.StatusConflict, access.ErrLocal.Error())
		return
	}
	info, ok := s.gate().Endpoint(id)
	if !ok {
		fail(w, http.StatusNotFound, "no such terminal")
		return
	}
	c := callerOf(r)
	next, err := widened(c, s.ownerSays(c), s.rulesOf(id, info.Kind, false), req)
	switch {
	case access.IsDenied(err):
		forbid(w, err)
		return
	case err != nil:
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	b, _ := json.Marshal(next)
	if err := s.st.SetTerminalRules(id, b); err != nil {
		s.error(w, err)
		return
	}
	s.logf("server: %s set the permissions of %s (%s)", c.Name, info.Name, id)
	s.publishList()
	reply(w, http.StatusOK, s.terminalOf(info))
}

// setDevice sets the device terminal ref (ID or name; "" the caller) runs on.
func (s *Server) setDevice(w http.ResponseWriter, r *http.Request, ref string) {
	var req api.SetDevice
	if !readJSON(w, r, &req) {
		return
	}
	if req.Device != api.DeviceComputer && req.Device != api.DevicePhone {
		fail(w, http.StatusBadRequest, `device is "computer" or "phone"`)
		return
	}
	c := callerOf(r)
	if ref == "" {
		ref = c.EndpointID
	}
	info, ok := s.gate().Find(ref)
	if !ok {
		fail(w, http.StatusNotFound, "no such terminal")
		return
	}
	if info.ID != c.EndpointID && !c.CanManage() {
		forbid(w, errors.New("only a web panel or 本机 sets another terminal's device"))
		return
	}
	if err := s.st.SetDevice(info.ID, string(req.Device)); err != nil {
		s.error(w, err)
		return
	}
	t := s.terminalOf(info)
	if info.ID == gate.LocalEndpointID {
		t.Rules = access.Full()
	}
	reply(w, http.StatusOK, t)
}

// publishList makes every stream's list again: what a terminal sees may have changed.
func (s *Server) publishList() {
	if l, err := s.m.List(context.Background()); err == nil {
		s.hub.Set("list", l)
	}
}

func (s *Server) invite(w http.ResponseWriter, r *http.Request) {
	var req api.NewInvite
	if !readJSON(w, r, &req) {
		return
	}
	c := callerOf(r)
	if req.Owner && c.panel() && s.ownerSays(c) == "" {
		forbid(w, errOwnerFlag)
		return
	}
	if !s.gate().Public() {
		fail(w, http.StatusConflict, "no public origin: a panel needs one for its passkey (ir public)")
		return
	}
	if _, err := s.gate().CheckName(req.Name); req.Name != "" && err != nil {
		s.gateDone(w, err)
		return
	}
	if !c.panel() {
		s.ask(w, c, "invite", req.Name, req.Owner)
		return
	}
	inv, err := s.makeInvite(req.Name, req.Owner)
	if err != nil {
		s.gateDone(w, err)
		return
	}
	reply(w, http.StatusOK, inv)
}

func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	var req api.NewInvite
	if !readJSON(w, r, &req) {
		return
	}
	if _, err := gate.NormalizeName(req.Name); err != nil {
		s.gateDone(w, err)
		return
	}
	c := callerOf(r)
	by := s.ownerSays(c)
	if req.Owner && c.panel() && by == "" {
		forbid(w, errOwnerFlag)
		return
	}
	if !c.panel() {
		s.ask(w, c, "token", req.Name, req.Owner)
		return
	}
	if !req.Owner {
		by = ""
	}
	tok, err := s.makeToken(req.Name, by)
	if err != nil {
		s.gateDone(w, err)
		return
	}
	reply(w, http.StatusOK, tok)
}

// ask turns an agent's request for a secret into a confirmation for the panels: 202 and no secret.
// owner (--owner): only a panel that has Claude, or 本机, may allow it (answerConfirm).
func (s *Server) ask(w http.ResponseWriter, c caller, kind, name string, owner bool) {
	cf := s.asks.add(kind, name, owner, c.Name, c.session)
	s.logf("server: %s asks a panel for a %s (%s, owner %v), confirmation %s", c.Name, kind, name, owner, cf.ID)
	reply(w, http.StatusAccepted, api.Pending{Confirm: cf})
}

// errOwnerFlag answers --owner from a panel that has no Claude itself.
var errOwnerFlag = errors.New("only a person at the machine or on a panel that has Claude says a terminal is the owner's own (--owner)")

// makeInvite makes a panel invite; owner: the panel that joins gets Claude (joined).
func (s *Server) makeInvite(name string, owner bool) (*api.Invite, error) {
	inv, err := s.gate().CreateInvite(name, owner)
	if err != nil {
		return nil, err
	}
	out := &api.Invite{ID: inv.ID, Name: inv.Name, URL: inv.URL, ExpiresAt: ms(inv.ExpiresAt)}
	// The same link as a QR code, for a phone to open it; without one the link alone still works.
	if out.QR, err = qrSVG(inv.URL); err != nil {
		s.logf("server: the invite's QR code: %v", err)
	}
	return out, nil
}

// makeToken gives channel name its token; ownerBy (ir token --owner, as ownerSays found it): it
// gets Claude too. Without it, a channel that has Claude keeps it.
func (s *Server) makeToken(name, ownerBy string) (*api.Token, error) {
	info, tok, err := s.gate().CreateToken(name)
	if err != nil {
		return nil, err
	}
	if ownerBy != "" {
		if err := s.giveClaude(info, ownerBy); err != nil {
			return nil, err
		}
	}
	return &api.Token{Terminal: s.terminalOf(info), Token: tok}, nil
}

// giveClaude turns Claude on for terminal info: it is the owner's own, as by says.
func (s *Server) giveClaude(info gate.EndpointInfo, by string) error {
	r := s.rulesOf(info.ID, info.Kind, false)
	if access.HasClaude(r) {
		return nil
	}
	b, err := json.Marshal(access.WithClaude(r, by, time.Now().UnixMilli()))
	if err != nil {
		return err
	}
	if err := s.st.SetTerminalRules(info.ID, b); err != nil {
		return err
	}
	s.logf("server: claude for %s (%s), the owner's own (%s)", info.Name, info.ID, by)
	return nil
}

// joined is the gate's word that a panel registered: the first one, or one invited with
// --owner, is the owner's own and gets Claude.
func (s *Server) joined(j gate.Joined) {
	by := ""
	switch {
	case j.First:
		by = access.ByFirst
	case j.Owner:
		by = access.ByLocal
	default:
		return
	}
	if err := s.giveClaude(j.Endpoint, by); err != nil {
		s.logf("server: claude for the new panel %s: %v", j.Endpoint.ID, err)
	}
}

func (s *Server) answerConfirm(w http.ResponseWriter, r *http.Request) {
	var a api.ConfirmAnswer
	if !readJSON(w, r, &a) {
		return
	}
	id := r.PathValue("id")
	c := callerOf(r)
	by := s.ownerSays(c)
	// An --owner ask is allowed only by one that may say so; it stays waiting for one that may.
	// Denying it is any panel's.
	if cf, ok := s.asks.peek(id); ok && cf.Owner && a.Allow && by == "" {
		forbid(w, errOwnerFlag)
		return
	}
	cf, ok := s.asks.take(id)
	if !ok {
		fail(w, http.StatusNotFound, "no such confirmation waiting")
		return
	}
	if !cf.Owner {
		by = ""
	}
	st := api.ConfirmState{ID: id, Kind: cf.Kind, Name: cf.Name, Owner: cf.Owner, State: "denied"}
	out := api.ConfirmReply{}
	if a.Allow {
		var err error
		switch cf.Kind {
		case "invite":
			out.Invite, err = s.makeInvite(cf.Name, cf.Owner)
			if err == nil {
				st.Name, st.ExpiresAt = out.Invite.Name, out.Invite.ExpiresAt
			}
		case "token":
			out.Token, err = s.makeToken(cf.Name, by)
			if err == nil {
				st.Name = out.Token.Terminal.Name
			}
		}
		st.State = "allowed"
		if err != nil {
			st.State, st.Error = "failed", err.Error()
		}
	}
	s.asks.finish(id, st)
	s.logf("server: %s answered confirmation %s: %s", c.Name, id, st.State)
	out.ConfirmState = st
	reply(w, http.StatusOK, out)
}

// confirmState is how a confirmation stands; ?wait=<seconds> (at most 60) waits for an answer.
func (s *Server) confirmState(w http.ResponseWriter, r *http.Request) {
	var d time.Duration
	if v := r.URL.Query().Get("wait"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			fail(w, http.StatusBadRequest, "bad wait")
			return
		}
		d = time.Duration(min(n, 60)) * time.Second
	}
	st, ok := s.asks.wait(r.Context(), r.PathValue("id"), d)
	if !ok {
		fail(w, http.StatusNotFound, "no such confirmation")
		return
	}
	reply(w, http.StatusOK, st)
}

func (s *Server) publicState(w http.ResponseWriter, r *http.Request) {
	st := api.PublicState{Origin: s.origin()}
	a := s.gate().Access()
	for _, e := range a.Endpoints {
		if e.Passkeys > 0 {
			st.Passkeys = true
		}
	}
	reply(w, http.StatusOK, st)
}

func (s *Server) origin() string {
	s.gmu.Lock()
	defer s.gmu.Unlock()
	return s.cfg.Public.Origin
}

// setPublic sets public.origin: a new gate takes over, every connection the old one let in is
// closed, and the config file says it for the next start.
func (s *Server) setPublic(w http.ResponseWriter, r *http.Request) {
	var req api.SetPublic
	if !readJSON(w, r, &req) {
		return
	}
	if err := s.SetPublicOrigin(req.Origin); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	s.logf("server: %s set public.origin to %q", callerOf(r).Name, req.Origin)
	s.publicState(w, r)
}

// SetPublicOrigin opens the gate for origin ("" is this machine only) in place of the one there.
func (s *Server) SetPublicOrigin(origin string) error {
	s.gmu.Lock()
	defer s.gmu.Unlock()
	g, err := s.newGate(origin)
	if err != nil {
		return err
	}
	if err := s.SaveConfig(func(c *config.Config) { c.Public.Origin = origin }); err != nil {
		g.Close()
		return err
	}
	old := s.g.Swap(g)
	old.CloseAll()
	old.Close()
	return nil
}

// gateDone answers a gate call's outcome.
func (s *Server) gateDone(w http.ResponseWriter, err error) {
	var ne *gate.NameError
	switch {
	case err == nil:
		reply(w, http.StatusOK, map[string]any{"ok": true})
	case errors.As(err, &ne):
		reply(w, http.StatusConflict, map[string]string{"error": err.Error(), "nameError": ne.Code})
	case errors.Is(err, gate.ErrNotFound):
		fail(w, http.StatusNotFound, err.Error())
	case errors.Is(err, gate.ErrLastPasskey), errors.Is(err, gate.ErrNotPublic), errors.Is(err, gate.ErrLocal):
		fail(w, http.StatusConflict, err.Error())
	default:
		s.error(w, err)
	}
}
