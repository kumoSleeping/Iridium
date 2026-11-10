package server

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/kumoSleeping/iridium/internal/access"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/config"
	"github.com/kumoSleeping/iridium/internal/gate"
	"github.com/kumoSleeping/iridium/internal/secrets"
)

// appRoutes are the apps' and their logins' (docs/api.md "应用"): who is calling, the list (ir
// apps), invites of ir_web, names and avatars, devices, removing, the needs (what waits for a
// person), the catalogue and public.origin. An app comes by pairing only (pairing.go).
func (s *Server) appRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/me", s.me)
	mux.HandleFunc("PUT /api/me/profile", func(w http.ResponseWriter, r *http.Request) { s.gate().ServeProfile(w, r, "") })
	// Another's: ir_web's logins and the host only (the gate checks).
	mux.HandleFunc("PUT /api/apps/{id}/profile", func(w http.ResponseWriter, r *http.Request) { s.gate().ServeProfile(w, r, r.PathValue("id")) })
	// The device it runs on: its own, or (ir_web's logins and the host) another's.
	mux.HandleFunc("PUT /api/me/device", func(w http.ResponseWriter, r *http.Request) { s.setDevice(w, r, "") })
	mux.HandleFunc("PUT /api/apps/{id}/device", func(w http.ResponseWriter, r *http.Request) { s.setDevice(w, r, r.PathValue("id")) })
	mux.HandleFunc("GET /api/apps", s.manage(s.apps))
	mux.HandleFunc("DELETE /api/apps/{id}", s.approver(s.revoke))
	mux.HandleFunc("DELETE /api/apps/{id}/logins/{login}", s.approver(s.removeLogin))
	// ir send 应用/登录: a notice shown to one login (notice.go).
	mux.HandleFunc("POST /api/apps/{id}/logins/{login}/notice", s.notice)
	// What an app may use (agents, scope): whoever approves sets it (S2 enforces it).
	mux.HandleFunc("PUT /api/apps/{id}/perms", s.approver(s.putAppPerms))
	mux.HandleFunc("PUT /api/apps/{id}/logins/{login}/perms", s.approver(s.putLoginPerms))
	mux.HandleFunc("POST /api/invites", s.manage(s.invite))
	// An app asks for other permissions: an app_change need.
	mux.HandleFunc("PUT /api/me/perms", s.askChange)
	mux.HandleFunc("GET /api/needs", s.listNeeds) // needs_more.go
	mux.HandleFunc("DELETE /api/needs/{id}", s.dropNeed)
	mux.HandleFunc("POST /api/needs/{id}", s.approver(s.answerNeed))
	// How one went, never a secret: for whoever manages, and any ir on the socket (an agent's in a
	// session an app opened waits for its ir run's too).
	mux.HandleFunc("GET /api/needs/{id}", func(w http.ResponseWriter, r *http.Request) {
		if c := callerOf(r); !c.CanManage() && !c.socket {
			forbid(w, errManageApps)
			return
		}
		s.needState(w, r)
	})
	mux.HandleFunc("GET /api/catalogue", func(w http.ResponseWriter, r *http.Request) { reply(w, http.StatusOK, catalogue) })
	mux.HandleFunc("GET /api/usage", s.usage) // routes_usage.go
	mux.HandleFunc("GET /api/public", s.manage(s.publicState))
	// Opening or closing ir_web: a person's ir at the host only (decision 3), never a login of it.
	mux.HandleFunc("PUT /api/public", func(w http.ResponseWriter, r *http.Request) {
		if !callerOf(r).machine() {
			forbid(w, errors.New("only the host's ir sets public.origin"))
			return
		}
		s.setPublic(w, r)
	})
}

// errManageApps answers an app that would manage the others.
var errManageApps = errors.New("only ir_web's logins and the host manage apps")

// manage lets ir_web's logins and the host (also an agent's ir, to read or to ask a person)
// through; apps manage nothing.
func (s *Server) manage(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !callerOf(r).CanManage() {
			forbid(w, errManageApps)
			return
		}
		h(w, r)
	}
}

// admin lets a person through who manages: on a login of ir_web or at the host, never an agent's
// ir (decision 15).
func (s *Server) admin(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if c := callerOf(r); !c.CanManage() || c.agent {
			forbid(w, errManageApps)
			return
		}
		h(w, r)
	}
}

// approver lets through who may approve needs (mayApprove).
func (s *Server) approver(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.mayApprove(callerOf(r)) {
			forbid(w, errApprove)
			return
		}
		h(w, r)
	}
}

// errApprove answers one that may not approve.
var errApprove = errors.New("only the host, or a login of ir_web that may use every agent everywhere, approves")

// mayApprove: the caller may answer needs and open pairing (decision 15): a person at the host, or
// on a login of ir_web that may use all agents (Claude too) with the whole scope. Never an agent.
func (s *Server) mayApprove(c caller) bool {
	switch {
	case c.agent || c.session != "":
		return false
	case c.Host():
		return true
	case !c.Web():
		return false
	}
	if !access.HasClaude(s.rulesFor(c)) {
		return false
	}
	if p := s.loginPerms(c.ID()); p != nil {
		return slices.Contains(p.Agents, api.PermsAll) && slices.Contains(p.Scope, api.PermsAll)
	}
	return true
}

// panelOnly lets through what may approve, widen and see secrets: a person on a login of ir_web,
// or at the host.
func (s *Server) panelOnly(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !callerOf(r).panel() {
			forbid(w, errors.New("only a person on ir_web or at the host may do this"))
			return
		}
		h(w, r)
	}
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	c := callerOf(r)
	info, ok := s.gate().Endpoint(c.ID())
	if !ok {
		fail(w, http.StatusNotFound, "no such app or login")
		return
	}
	reply(w, http.StatusOK, s.meOf(c, info))
}

// apps lists the host, ir_web (while there is a public origin, or a login of it), the apps, the
// logins of ir_web and their invites.
func (s *Server) apps(w http.ResponseWriter, r *http.Request) {
	a := s.gate().Access()
	out := api.Apps{Apps: []api.App{}, Logins: []api.Login{}, Invites: []api.PendingInvite{}}
	host, _ := s.gate().Endpoint(gate.HostID)
	out.Apps = append(out.Apps, s.appOf(host))
	web := api.App{ID: gate.WebApp, Name: gate.WebApp, Web: true, Rules: access.Default(), Device: api.DeviceComputer}
	var apps []api.App
	for _, e := range a.Endpoints {
		if e.Kind == gate.KindPanel {
			l := s.loginOf(e)
			out.Logins = append(out.Logins, l)
			web.Logins++
			web.Online += l.Online
			web.UsedToday += l.UsedToday
			web.LastSeenAt = max(web.LastSeenAt, l.LastSeenAt)
			continue
		}
		app := s.appOf(e)
		app.Perms = s.appPerms(e.ID)
		apps = append(apps, app)
	}
	if s.gate().Public() || web.Logins > 0 {
		out.Apps = append(out.Apps, web)
	}
	out.Apps = append(out.Apps, apps...)
	others, err := s.st.Logins()
	if err != nil {
		s.error(w, err)
		return
	}
	for _, l := range others {
		out.Logins = append(out.Logins, api.Login{App: l.App, ID: l.ID, Name: l.Name, CreatedAt: ms(l.CreatedAt),
			LastSeenAt: ms(l.LastActive), ApprovedAt: ms(l.ApprovedAt), Rules: s.rulesOf(l.App, false), Perms: s.loginPermsOf(l.App, l.ID)})
	}
	for _, i := range a.Invites {
		out.Invites = append(out.Invites, api.PendingInvite{ID: i.ID, Name: i.Name, Setup: i.Setup, Claude: permsClaude(i.Perms),
			CreatedAt: ms(i.CreatedAt), ExpiresAt: ms(i.ExpiresAt)})
	}
	reply(w, http.StatusOK, out)
}

// revoke removes an app, a login ("应用/登录", or for ir_web its ID) or an unused invite. The
// host and ir_web are built in.
func (s *Server) revoke(w http.ResponseWriter, r *http.Request) {
	ref := r.PathValue("id")
	if gate.IsHostRef(ref) || ref == gate.WebApp {
		refuse(w, http.StatusUnprocessableEntity, "built_in", ref+" is built in")
		return
	}
	if app, login, ok := strings.Cut(ref, "/"); ok && app != gate.WebApp {
		s.dropLogin(w, app, login)
		return
	}
	s.gateDone(w, s.gate().Revoke(ref))
}

// putAppPerms sets what app {id} may use: any within bounds (the approver gives as they see fit,
// not only less than it once asked for). Not the host's or ir_web's.
func (s *Server) putAppPerms(w http.ResponseWriter, r *http.Request) {
	info, ok := s.gate().Find(r.PathValue("id"))
	if !ok || info.Kind != gate.KindChannel || info.Host() {
		refuse(w, http.StatusUnprocessableEntity, "no_such_app", "no such app")
		return
	}
	var p api.Perms
	if !readJSON(w, r, &p) {
		return
	}
	p = cleanPerms(&p)
	if err := s.setAppPerms(info.ID, p); err != nil {
		s.error(w, err)
		return
	}
	s.logf("server: %s set %s's permissions", callerOf(r).Name, info.Name)
	s.terminalsChanged()
	reply(w, http.StatusOK, p)
}

// putLoginPerms sets what login {login} of app {id} (ir_web's too) may use, within its app's.
func (s *Server) putLoginPerms(w http.ResponseWriter, r *http.Request) {
	app := r.PathValue("id")
	if app != gate.WebApp {
		info, ok := s.gate().Find(app)
		if !ok || info.Kind != gate.KindChannel || info.Host() {
			refuse(w, http.StatusUnprocessableEntity, "no_such_app", "no such app")
			return
		}
		app = info.ID
	}
	var p api.Perms
	if !readJSON(w, r, &p) {
		return
	}
	p = cleanPerms(&p)
	b, _ := json.Marshal(p)
	ok, err := s.st.SetLoginPermsOf(app, r.PathValue("login"), b)
	switch {
	case err != nil:
		s.error(w, err)
		return
	case !ok:
		refuse(w, http.StatusUnprocessableEntity, "no_such_login", "no such login")
		return
	}
	s.logf("server: %s set %s/%s's permissions", callerOf(r).Name, app, r.PathValue("login"))
	s.terminalsChanged()
	reply(w, http.StatusOK, p)
}

// removeLogin removes login {login} of app {id}: for another app's, it asks again as it next speaks.
func (s *Server) removeLogin(w http.ResponseWriter, r *http.Request) {
	app, login := r.PathValue("id"), r.PathValue("login")
	if app == gate.WebApp {
		s.gateDone(w, s.gate().Revoke(gate.WebApp+"/"+login))
		return
	}
	s.dropLogin(w, app, login)
}

func (s *Server) dropLogin(w http.ResponseWriter, appRef, login string) {
	info, ok := s.gate().Find(appRef)
	if !ok || info.Kind != gate.KindChannel || info.Host() {
		refuse(w, http.StatusUnprocessableEntity, "no_such_app", "no such app: "+appRef)
		return
	}
	gone, err := s.st.DeleteLogin(info.ID, login)
	if err != nil {
		s.error(w, err)
		return
	}
	if !gone {
		refuse(w, http.StatusUnprocessableEntity, "no_such_login", "no such login: "+appRef+"/"+login)
		return
	}
	s.logf("server: login %s of %s removed", login, info.Name)
	s.terminalsChanged()
	reply(w, http.StatusOK, map[string]any{"ok": true})
}

// setDevice sets the device app or login ref (ID or name; "" the caller) runs on.
func (s *Server) setDevice(w http.ResponseWriter, r *http.Request, ref string) {
	var req api.SetDevice
	if !readJSON(w, r, &req) {
		return
	}
	if !req.Device.Valid() {
		fail(w, http.StatusBadRequest, `device is "computer", "laptop", "phone" or "none"`)
		return
	}
	c := callerOf(r)
	if ref == "" {
		ref = c.ID()
	}
	info, ok := s.gate().Find(ref)
	if !ok {
		fail(w, http.StatusNotFound, "no such app or login")
		return
	}
	if info.ID != c.ID() && !c.CanManage() {
		forbid(w, errors.New("only ir_web's logins and the host set another's device"))
		return
	}
	if err := s.st.SetDevice(info.ID, string(req.Device)); err != nil {
		s.error(w, err)
		return
	}
	reply(w, http.StatusOK, api.SetDevice{Device: s.deviceOf(info.ID)})
}

// publishList makes every stream's list again: what an app sees may have changed.
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
	if req.Claude && c.panel() && s.ownerSays(c) == "" {
		forbid(w, errOwnerFlag)
		return
	}
	if !s.gate().Public() {
		refuse(w, http.StatusForbidden, "not_public", "no public origin: ir_web needs one for its passkeys (ir public)")
		return
	}
	if _, err := s.gate().CheckLoginName(req.Name); req.Name != "" && err != nil {
		s.gateDone(w, err)
		return
	}
	if !c.panel() {
		s.ask(w, c, api.NeedInvite, req.Name, req.Claude)
		return
	}
	// A login slot is made by whoever approves (a narrowed login may not invite a wider one).
	if !s.mayApprove(c) {
		forbid(w, errApprove)
		return
	}
	by := ""
	if req.Claude {
		by = s.ownerSays(c)
	}
	inv, err := s.makeInvite(req.Name, by, req.Perms)
	if err != nil {
		s.gateDone(w, err)
		return
	}
	reply(w, http.StatusOK, inv)
}

// ask turns an agent's request for a secret into a need for a person: 202 and no secret. owner
// (Claude): only a login of ir_web that has Claude, or the host, may allow it (answerNeed).
func (s *Server) ask(w http.ResponseWriter, c caller, kind api.NeedKind, name string, owner bool) {
	cf := s.asks.add(kind, name, owner, c.Name, c.session)
	s.logf("server: %s asks a person for a %s (%s, owner %v), need %s", c.Name, kind, name, owner, cf.ID)
	reply(w, http.StatusAccepted, api.Pending{Need: cf})
}

// errOwnerSecret answers allowing a secret-use from a login that has no Claude itself.
var errOwnerSecret = errors.New("only a person at the host or on a login of ir_web that has Claude lets a session use the credentials")

// errOwnerFlag answers giving Claude from a login that has no Claude itself.
var errOwnerFlag = errors.New("only a person at the host or on a login of ir_web that has Claude gives Claude")

// makeInvite makes a slot for a login of ir_web; claudeBy (ownerSays's, "" none): the login that
// takes it gets Claude, as that one said (joined).
func (s *Server) makeInvite(name, claudeBy string, given *api.Perms) (*api.Invite, error) {
	// The invite keeps the login's rules (Claude) and, beside them, what it may use (_perms).
	slot := struct {
		api.Rules
		Perms *api.Perms `json:"_perms,omitempty"`
	}{Rules: access.Default()}
	if claudeBy != "" {
		slot.Rules = access.WithClaude(access.Default(), claudeBy, time.Now().UnixMilli())
	}
	if given != nil {
		p := cleanPerms(given)
		slot.Perms = &p
	}
	var perms []byte
	if claudeBy != "" || given != nil {
		var err error
		if perms, err = json.Marshal(slot); err != nil {
			return nil, err
		}
	}
	inv, err := s.gate().CreateInvite(name, perms)
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

// permsClaude: an invite's perms give Claude.
func permsClaude(perms []byte) bool {
	var r api.Rules
	return len(perms) > 0 && json.Unmarshal(perms, &r) == nil && access.HasClaude(r)
}

// giveClaude turns Claude on for app or login info: it is the owner's own, as by says.
func (s *Server) giveClaude(info gate.EndpointInfo, by string) error {
	r := s.rulesOf(info.ID, false)
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

// joined is the gate's word that a login of ir_web registered: it takes its invite's perms; the
// first one is the owner's own and gets Claude.
func (s *Server) joined(j gate.Joined) {
	if j.First {
		if err := s.giveClaude(j.Endpoint, access.ByFirst); err != nil {
			s.logf("server: claude for the new login %s: %v", j.Endpoint.ID, err)
		}
		return
	}
	if len(j.Perms) == 0 {
		return
	}
	if err := s.st.SetTerminalRules(j.Endpoint.ID, j.Perms); err != nil {
		s.logf("server: the perms of the new login %s: %v", j.Endpoint.ID, err)
	}
	var slot struct {
		Perms *api.Perms `json:"_perms"`
	}
	if json.Unmarshal(j.Perms, &slot) == nil && slot.Perms != nil {
		b, _ := json.Marshal(slot.Perms)
		if _, err := s.st.SetLoginPermsOf(gate.WebApp, j.Endpoint.ID, b); err != nil {
			s.logf("server: what the new login %s may use: %v", j.Endpoint.ID, err)
		}
	}
}

func (s *Server) answerNeed(w http.ResponseWriter, r *http.Request) {
	var a api.NeedAnswer
	if !readJSON(w, r, &a) {
		return
	}
	id := r.PathValue("id")
	c := callerOf(r)
	by := s.ownerSays(c)
	// What a person may not give, or an answer that cannot stand, leaves the need waiting for one
	// that may. Denying it is anyone's who answers.
	cf, ok := s.asks.peek(id)
	if ok && a.Allow {
		switch {
		case cf.Kind == api.NeedAppJoin && a.Owner && by == "":
			forbid(w, errOwnerFlag)
			return
		case cf.Owner && by == "" && cf.Kind == api.NeedSecretUse:
			forbid(w, errOwnerSecret)
			return
		case cf.Owner && by == "":
			forbid(w, errOwnerFlag)
			return
		case cf.Kind == api.NeedSecretSet && (a.Value == "" || len(a.Value) > secrets.MaxValue):
			refuse(w, http.StatusUnprocessableEntity, "bad_value", "type the value (64 KiB at most)")
			return
		case (cf.Kind == api.NeedAppJoin || cf.Kind == api.NeedAppUpdate) && !s.checkPairAnswer(w, cf, a):
			return
		case cf.Kind == api.NeedAppChange && !s.checkGiven(w, cf, a):
			return
		}
	}
	cf, ok = s.asks.take(id)
	if !ok {
		refuse(w, http.StatusUnprocessableEntity, "no_such_need", "no such need waiting")
		return
	}
	owner := by
	if !cf.Owner {
		by = ""
	}
	st := api.NeedState{ID: id, Kind: cf.Kind, Name: cf.Name, Owner: cf.Owner, State: "denied"}
	out := api.NeedReply{}
	var err error
	switch {
	case !a.Allow && cf.Kind == api.NeedLoginJoin:
		// Denied, it is gone: it asks again as it next speaks.
		_, err = s.st.DeleteLogin(cf.App.ID, cf.Login)
	case !a.Allow:
	case cf.Kind == api.NeedInvite:
		out.Invite, err = s.makeInvite(cf.Name, by, nil)
		if err == nil {
			st.Name, st.ExpiresAt = out.Invite.Name, out.Invite.ExpiresAt
		}
	case cf.Kind == api.NeedSecretSet:
		err = s.storeAsked(cf, a.Value, c)
	case cf.Kind == api.NeedSecretUse:
		s.allowSecretUse(cf, a.ForSession)
	case cf.Kind == api.NeedAppJoin, cf.Kind == api.NeedAppUpdate:
		// The TOKEN goes to the asker alone, by its request; the person never sees it.
		var tok *api.Token
		if tok, err = s.pairAllowed(cf, a, owner); err == nil {
			s.pairToken(id, tok.Token)
			st.Name = tok.App.Name
		}
	case cf.Kind == api.NeedAppChange:
		err = s.setAppPerms(cf.App.ID, cleanPerms(cmp.Or(a.Perms, cf.Wants)))
		s.terminalsChanged()
	case cf.Kind == api.NeedLoginJoin:
		var there bool
		if there, err = s.st.ApproveLogin(cf.App.ID, cf.Login, time.Now()); err == nil && !there {
			err = errors.New("the login is gone")
		}
		s.terminalsChanged()
	}
	if a.Allow {
		st.State = "allowed"
	}
	if err != nil {
		st.State, st.Error = "failed", err.Error()
	}
	s.asks.finish(id, st)
	s.logf("server: %s answered need %s (%s): %s", c.Name, id, cf.Kind, st.State)
	out.NeedState = st
	reply(w, http.StatusOK, out)
}

// needState is how a need stands; ?wait=<seconds> (at most 60) waits for an answer.
func (s *Server) needState(w http.ResponseWriter, r *http.Request) {
	var d time.Duration
	if v := r.URL.Query().Get("wait"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			refuse(w, http.StatusUnprocessableEntity, "bad_wait", "bad wait")
			return
		}
		d = time.Duration(min(n, 60)) * time.Second
	}
	st, ok := s.asks.wait(r.Context(), r.PathValue("id"), d)
	if !ok {
		refuse(w, http.StatusUnprocessableEntity, "no_such_need", "no such need")
		return
	}
	reply(w, http.StatusOK, st)
}

// askChange is an app asking for other permissions (PUT /api/me/perms): an app_change need, 202.
// Only an app's own TOKEN asks; what it has stays till a person allows it.
func (s *Server) askChange(w http.ResponseWriter, r *http.Request) {
	var p api.Perms
	if !readJSON(w, r, &p) {
		return
	}
	c := callerOf(r)
	if c.Host() || c.Web() || c.session != "" || c.socket {
		refuse(w, http.StatusForbidden, "no_permission", "only an app asks for its permissions")
		return
	}
	info, ok := s.gate().Endpoint(c.App)
	if !ok {
		refuse(w, http.StatusUnprocessableEntity, "no_such_app", "no such app")
		return
	}
	wants := cleanPerms(&p)
	app := s.appOf(info)
	has := cleanPerms(s.appPerms(info.ID))
	if old, ok := s.asks.find(func(n api.Need) bool { return n.Kind == api.NeedAppChange && n.App != nil && n.App.ID == info.ID }); ok {
		s.asks.drop(old.ID)
	}
	nd := s.asks.addNeed(api.Need{Kind: api.NeedAppChange, Name: info.Name, Asker: info.Name, App: &app, Wants: &wants, Has: &has})
	s.logf("server: %s asks for other permissions, need %s", info.Name, nd.ID)
	reply(w, http.StatusAccepted, api.Pending{Need: nd})
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
		reply(w, http.StatusUnprocessableEntity, api.Error{Code: "bad_name", Message: err.Error(), NameError: ne.Code})
	case errors.Is(err, gate.ErrNotFound):
		refuse(w, http.StatusUnprocessableEntity, "not_found", err.Error())
	case errors.Is(err, gate.ErrLastPasskey):
		refuse(w, http.StatusConflict, "last_passkey", err.Error())
	case errors.Is(err, gate.ErrNotPublic):
		refuse(w, http.StatusForbidden, "not_public", err.Error())
	case errors.Is(err, gate.ErrLocal):
		refuse(w, http.StatusUnprocessableEntity, "built_in", err.Error())
	default:
		s.error(w, err)
	}
}
