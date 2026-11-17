package server

// The passkeys of a login of ir_web, one per device (docs/web.md "Passkey"): listed, added by a
// one-time link, set to a device, removed. A person on that login, or one who manages, may.

import (
	"errors"
	"net/http"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/gate"
	"github.com/kumoSleeping/iridium/internal/webapp"
)

func (s *Server) passkeyRoutes(mux *http.ServeMux) {
	const base = "/v1/internal/apps/ir_web/logins/{login}/passkeys"
	mux.HandleFunc("GET "+base, s.passkeyLogin(s.listPasskeys))
	mux.HandleFunc("POST "+base, s.passkeyLogin(s.addPasskey))
	mux.HandleFunc("PUT "+base+"/{passkey}/device", s.passkeyLogin(s.setPasskeyDevice))
	mux.HandleFunc("DELETE "+base+"/{passkey}", s.passkeyLogin(s.removePasskey))
	mux.HandleFunc("DELETE "+base+"/links/{link}", s.passkeyLogin(s.dropPasskeyLink))
}

var errPasskeys = errors.New("only a person on that login, or one who manages, handles its passkeys")

// passkeyLogin lets through a person (never an agent) on the login {login} names, or one who
// manages, with that login of ir_web found: h gets its ID.
func (s *Server) passkeyLogin(h func(w http.ResponseWriter, r *http.Request, login gate.EndpointInfo)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c := callerOf(r)
		info, ok := s.gate().Find(r.PathValue("login"))
		if !ok || info.Kind != gate.KindPanel {
			refuse(w, http.StatusUnprocessableEntity, "no_such_login", "no such login of ir_web")
			return
		}
		if c.byAgent() || !(c.CanManage() || (c.Web() && c.ID() == info.ID)) {
			forbid(w, errPasskeys)
			return
		}
		h(w, r, info)
	}
}

func (s *Server) listPasskeys(w http.ResponseWriter, r *http.Request, login gate.EndpointInfo) {
	out := api.Passkeys{Passkeys: []api.Passkey{}, Links: []api.PasskeyLink{}}
	for _, p := range s.web().Passkeys(login.ID, callerOf(r).web) {
		out.Passkeys = append(out.Passkeys, api.Passkey{ID: p.ID, Name: p.Name, Device: api.Device(p.Device), Auto: p.Auto,
			Current: p.Current, CreatedAt: ms(p.CreatedAt), LastUsedAt: ms(p.LastUsedAt)})
	}
	for _, l := range s.web().PasskeyLinks(login.ID) {
		out.Links = append(out.Links, api.PasskeyLink{ID: l.ID, CreatedAt: ms(l.CreatedAt), ExpiresAt: ms(l.ExpiresAt)})
	}
	reply(w, http.StatusOK, out)
}

// addPasskey makes the one-time link that adds a passkey to the login, shown once (and as a QR
// code): opened on another device, that device is the login too.
func (s *Server) addPasskey(w http.ResponseWriter, r *http.Request, login gate.EndpointInfo) {
	link, err := s.web().AddPasskeyLink(login.ID)
	if err != nil {
		s.gateDone(w, err)
		return
	}
	out := api.Invite{ID: link.ID, Name: login.Name, URL: link.URL, ExpiresAt: ms(link.ExpiresAt)}
	if out.QR, err = qrSVG(link.URL); err != nil {
		s.logf("server: the passkey link's QR code: %v", err)
	}
	s.terminalsChanged()
	reply(w, http.StatusOK, out)
}

func (s *Server) setPasskeyDevice(w http.ResponseWriter, r *http.Request, login gate.EndpointInfo) {
	var req api.PasskeyDevice
	if !readJSON(w, r, &req) {
		return
	}
	if req.Device != "" && !req.Device.Valid() {
		fail(w, http.StatusBadRequest, `device is "computer", "laptop", "phone", "none" or "" (by the browser)`)
		return
	}
	if !s.passkeyDone(w, s.web().SetPasskeyDevice(login.ID, r.PathValue("passkey"), string(req.Device))) {
		return
	}
	s.terminalsChanged()
	reply(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) removePasskey(w http.ResponseWriter, r *http.Request, login gate.EndpointInfo) {
	if !s.passkeyDone(w, s.web().RemovePasskey(login.ID, r.PathValue("passkey"))) {
		return
	}
	s.logf("server: a passkey of %s removed", login.Name)
	s.terminalsChanged()
	reply(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) dropPasskeyLink(w http.ResponseWriter, r *http.Request, login gate.EndpointInfo) {
	id := r.PathValue("link")
	for _, l := range s.web().PasskeyLinks(login.ID) {
		if l.ID == id {
			s.gateDone(w, s.web().RevokeInvite(id))
			return
		}
	}
	refuse(w, http.StatusUnprocessableEntity, "not_found", "no such link")
}

// passkeyDone answers err of a change to a passkey, if there is one: false then.
func (s *Server) passkeyDone(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return true
	case errors.Is(err, webapp.ErrNoPasskey):
		refuse(w, http.StatusUnprocessableEntity, "no_such_passkey", err.Error())
	case errors.Is(err, webapp.ErrLastOfLogin):
		refuse(w, http.StatusConflict, "last_passkey", err.Error())
	case errors.Is(err, webapp.ErrBadDevice):
		fail(w, http.StatusBadRequest, err.Error())
	default:
		s.error(w, err)
	}
	return false
}
