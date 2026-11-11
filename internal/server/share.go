package server

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/gate"
)

// Share (draft 二「消息」 分享; POST /v1/internal/share, ir share): a session, a reply, a place or a file
// shared with one login, with who shared it and a note. The login must be able to use it: a
// session's agent and scope (using's checks), the scope of a reply's session, of a place, of a
// file. A login of ir_web finds it among its needs (a share need, till it puts it away or a day
// passes) and gets a Web Push; another app's login gets a Satori event (internal, _type ir.share).

func (s *Server) shareRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/internal/share", s.share)
	mux.HandleFunc("GET /v1/internal/share/targets", s.shareTargets)
}

// errNotUsable: the login it would go to may not use what is shared.
var errNotUsable = errors.New("may not use what is shared")

// shareThing fills in req's thing as caller c shares it (c must see it), and the check a target
// must pass.
func (s *Server) shareThing(c caller, req api.ShareRequest) (api.Share, *api.Session, func(caller) bool, error) {
	sh := api.Share{What: req.What, Session: req.Session, At: req.At, Place: req.Place, Ref: req.Ref, Path: req.Path, Note: strings.TrimSpace(req.Note)}
	if req.What == api.ShareOfPlace {
		if req.Place == "" {
			return sh, nil, nil, badShare("a place to share")
		}
		app, p := s.parsePlace(req.Place)
		if !c.Host() && !s.scopeHas(s.permsFor(c).Scope, app, p) {
			return sh, nil, nil, errNoSession
		}
		if app != gate.HostID {
			sh.Machine = app
		}
		sh.Path, sh.Title = p, path.Base(strings.ReplaceAll(p, `\`, "/"))
		return sh, nil, func(t caller) bool { return t.Host() || s.scopeHas(s.permsFor(t).Scope, app, p) }, nil
	}
	if req.Session == "" {
		return sh, nil, nil, badShare("a session")
	}
	st, err := s.seeing(c, req.Session)
	if err != nil {
		return sh, nil, nil, err
	}
	ss := st.Session
	sh.Session, sh.Machine, sh.Title = ss.ID, ss.Machine, cmp.Or(ss.Name, ss.Title, string(ss.Agent))
	scope := func(t caller) bool { return t.Host() || s.sees(s.permsFor(t), ss) }
	switch req.What {
	case api.ShareOfSession:
		return sh, &ss, func(t caller) bool { return s.mayUse(t, ss) }, nil
	case api.ShareOfReply:
		if req.At == "" {
			return sh, nil, nil, badShare("the reply's at")
		}
		text, err := s.m.Full(ss.ID, req.At)
		if err != nil {
			return sh, nil, nil, err
		}
		if line, _, _ := strings.Cut(strings.TrimSpace(text), "\n"); line != "" {
			if r := []rune(line); len(r) > 80 {
				line = string(r[:80]) + "…"
			}
			sh.Title = line
		}
		return sh, &ss, scope, nil
	case api.ShareOfFile:
		switch {
		case req.Ref != "":
			p, err := s.files.Lookup(ss.ID, req.Ref)
			if err != nil {
				return sh, nil, nil, err
			}
			sh.Path = p
		case req.Path != "" && ss.Machine == "":
			f, err := s.files.Attach(ss.ID, req.Path)
			if err != nil {
				return sh, nil, nil, err
			}
			sh.Ref, sh.Path = f.Ref, f.Path
		case req.Path == "":
			return sh, nil, nil, badShare("the file's ref or path")
		}
		sh.Title = path.Base(strings.ReplaceAll(sh.Path, `\`, "/"))
		file := sh.Path
		return sh, &ss, func(t caller) bool {
			return scope(t) && (t.Host() || s.scopeHas(s.permsFor(t).Scope, ss.Machine, file))
		}, nil
	}
	return sh, nil, nil, badShare("what: session, reply, place or file")
}

type badShareErr string

func (e badShareErr) Error() string { return "share: " + string(e) }

func badShare(what string) error { return badShareErr("give " + what) }

// target is a login as the share's To names it ("应用/登录"): who it is as a caller, its app's
// and its own ID and name.
type target struct {
	c                caller
	app, login, name string
	web              bool
}

func (s *Server) findTarget(to string) (target, string, bool) {
	appRef, loginRef, _ := strings.Cut(to, "/")
	if appRef == "" || loginRef == "" {
		return target{}, "no_such_login", false
	}
	if appRef == gate.WebApp {
		l, ok := s.gate().Find(gate.WebApp + "/" + loginRef)
		if !ok || l.Kind != gate.KindPanel {
			return target{}, "no_such_login", false
		}
		return target{c: caller{Identity: l.Identity()}, app: gate.WebApp, login: l.ID, name: l.Name, web: true}, "", true
	}
	app, ok := s.gate().Find(appRef)
	if !ok || app.Kind != gate.KindChannel || app.Host() {
		return target{}, "no_such_app", false
	}
	for _, l := range (satoriBackend{s}).Logins(app.ID) {
		if l.ID == loginRef || l.Name == loginRef {
			return target{c: caller{Identity: gate.Identity{App: app.ID, Login: l.ID, Name: l.Name}}, app: app.ID, login: l.ID, name: l.Name}, "", true
		}
	}
	return target{}, "no_such_login", false
}

// self: t is the caller.
func (t target) self(c caller) bool { return c.App == t.app && c.Login == t.login }

func (s *Server) shareFail(w http.ResponseWriter, err error) {
	var bad badShareErr
	if errors.As(err, &bad) {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	s.fault(w, err)
}

func (s *Server) share(w http.ResponseWriter, r *http.Request) {
	c := callerOf(r)
	var req api.ShareRequest
	if !readJSON(w, r, &req) {
		return
	}
	t, code, ok := s.findTarget(req.To)
	if !ok {
		refuse(w, http.StatusUnprocessableEntity, code, "no such login: "+req.To)
		return
	}
	if t.self(c) {
		fail(w, http.StatusBadRequest, "share: that is the caller itself")
		return
	}
	sh, ss, check, err := s.shareThing(c, req)
	if err != nil {
		s.shareFail(w, err)
		return
	}
	if !check(t.c) {
		refuse(w, http.StatusUnprocessableEntity, "not_usable", t.name+" "+errNotUsable.Error())
		return
	}
	sh.From, sh.FromName, sh.SharedAt = c.ID(), cmp.Or(c.Name, s.Tag(c.ID())), time.Now().UnixMilli()
	if c.Login != "" && !c.Web() && !c.Host() {
		sh.From = c.App + "/" + c.Login
	}
	sh.FromSession = c.session
	if sh.FromSession == "" && c.socket {
		sh.FromSession = req.Caller
	}
	out := api.ShareReply{App: t.app, Login: t.login, Name: t.name}
	if !t.web {
		s.sat.Share(t.app, t.login, ss, sh)
		reply(w, http.StatusOK, out)
		return
	}
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	s.more.addShare(api.Need{
		ID: "share:" + hex.EncodeToString(b), Kind: api.NeedShare, Name: sh.Title, Asker: sh.FromName,
		CreatedAt: sh.SharedAt, ExpiresAt: sh.SharedAt + ShareTTL.Milliseconds(), Share: &sh, To: t.login,
	})
	s.asks.publish()
	if s.Push != nil && s.Push.Enabled() {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		body := sh.Title
		if sh.Note != "" {
			body = sh.Title + "：" + sh.Note
		}
		out.Pushed = s.Push.Send(ctx, t.login, api.PushMessage{Kind: "share", Session: sh.Session, Title: sh.FromName + " 分享", Body: body})
	}
	reply(w, http.StatusOK, out)
}

// shareTargets is GET /v1/internal/share/targets?what=&session=&at=&place=&ref=&path=: the apps whose
// logins may use it, each with those logins (not the caller).
func (s *Server) shareTargets(w http.ResponseWriter, r *http.Request) {
	c := callerOf(r)
	q := r.URL.Query()
	req := api.ShareRequest{What: api.ShareWhat(q.Get("what")), Session: q.Get("session"), At: q.Get("at"), Place: q.Get("place"), Ref: q.Get("ref"), Path: q.Get("path")}
	if req.What == api.ShareOfFile && req.Ref == "" {
		req.What = api.ShareOfSession // a path is attached only as it is shared
	}
	_, _, check, err := s.shareThing(c, req)
	if err != nil {
		s.shareFail(w, err)
		return
	}
	out := api.ShareTargets{Apps: []api.ShareApp{}}
	add := func(id, name string, ls []api.ShareLogin) {
		if len(ls) > 0 {
			out.Apps = append(out.Apps, api.ShareApp{ID: id, Name: name, Logins: ls})
		}
	}
	var web []api.ShareLogin
	for _, e := range s.gate().Access().Endpoints {
		if e.Kind == gate.KindPanel {
			t := target{c: caller{Identity: e.Identity()}, app: gate.WebApp, login: e.ID}
			if !t.self(c) && check(t.c) {
				web = append(web, api.ShareLogin{ID: e.ID, Name: e.Name, Avatar: e.Avatar})
			}
		}
	}
	add(gate.WebApp, gate.WebApp, web)
	for _, e := range s.gate().Access().Endpoints {
		if e.Kind != gate.KindChannel || e.Host() {
			continue
		}
		var ls []api.ShareLogin
		for _, l := range (satoriBackend{s}).Logins(e.ID) {
			t := target{c: caller{Identity: gate.Identity{App: e.ID, Login: l.ID, Name: l.Name}}, app: e.ID, login: l.ID}
			if !t.self(c) && check(t.c) {
				ls = append(ls, api.ShareLogin{ID: l.ID, Name: l.Name})
			}
		}
		add(e.ID, e.Name, ls)
	}
	reply(w, http.StatusOK, out)
}
