package server

import (
	"cmp"
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/gate"
)

// notice is POST /api/apps/{id}/logins/{login}/notice, ir send 应用/登录 (draft 二「消息」): text
// shown to one login, waking no agent. A login of ir_web gets it as a Web Push to its browsers;
// another app's login has no way to get it yet (the Satori events come later): 422
// not_deliverable, as when nothing would show it. A person, or an agent's ir; not an app's bare
// TOKEN (an app sends into sessions, not to other logins).
func (s *Server) notice(w http.ResponseWriter, r *http.Request) {
	c := callerOf(r)
	if !c.socket && !c.panel() && c.session == "" {
		forbid(w, errors.New("an app sends into sessions, not to logins"))
		return
	}
	var req api.Notice
	if !readJSON(w, r, &req) {
		return
	}
	req.Text = strings.TrimSpace(req.Text)
	if req.Text == "" {
		fail(w, http.StatusBadRequest, "an empty notice")
		return
	}
	appRef, loginRef := r.PathValue("id"), r.PathValue("login")
	out := api.NoticeReply{App: appRef, Login: loginRef}
	if appRef != gate.WebApp {
		app, ok := s.gate().Find(appRef)
		if !ok || app.Kind != gate.KindChannel || app.Host() {
			refuse(w, http.StatusUnprocessableEntity, "no_such_app", "no such app: "+appRef)
			return
		}
		logins, err := s.st.Logins()
		if err != nil {
			s.error(w, err)
			return
		}
		for _, l := range logins {
			if l.App == app.ID && (l.ID == loginRef || l.Name == loginRef) {
				refuse(w, http.StatusUnprocessableEntity, "not_deliverable", "not deliverable yet: "+app.Name+"'s logins get no notices until it speaks Satori")
				return
			}
		}
		refuse(w, http.StatusUnprocessableEntity, "no_such_login", "no such login: "+appRef+"/"+loginRef)
		return
	}
	login, ok := s.gate().Find(gate.WebApp + "/" + loginRef)
	if !ok || login.Kind != gate.KindPanel {
		refuse(w, http.StatusUnprocessableEntity, "no_such_login", "no such login: "+appRef+"/"+loginRef)
		return
	}
	out.Login, out.Name = login.ID, login.Name
	if s.Push == nil || !s.Push.Enabled() {
		refuse(w, http.StatusUnprocessableEntity, "not_deliverable", "not deliverable: Web Push is off")
		return
	}
	from := cmp.Or(c.session, req.Caller)
	msg := api.PushMessage{Kind: "notice", Title: "Iridium", Body: req.Text, Session: from}
	if from != "" {
		if st, err := s.m.State(from); err == nil {
			// The login it goes to must see the session it comes from (its scope).
			if !s.sees(s.permsFor(caller{Identity: login.Identity()}), st.Session) {
				refuse(w, http.StatusUnprocessableEntity, "not_deliverable", "not deliverable: "+login.Name+" may not see the session it comes from")
				return
			}
			msg.Title = cmp.Or(st.Name, st.Title, string(st.Agent))
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if out.Sent = s.Push.Send(ctx, login.ID, msg); out.Sent == 0 {
		refuse(w, http.StatusUnprocessableEntity, "not_deliverable", "not deliverable: "+login.Name+" has no browser with push on")
		return
	}
	reply(w, http.StatusOK, out)
}
