package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/access"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/gate"
	"github.com/kumoSleeping/iridium/internal/webapp"
)

// asLogin sends a request with an app's TOKEN as its login (Satori-User-ID): the status, and the
// body into out.
func (l *live) asLogin(token, login, method, path string, body, out any) int {
	l.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, l.base+path, r)
	req.Header.Set("Authorization", "Bearer "+token)
	if login != "" {
		req.Header.Set(HeaderLogin, login)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		l.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if out != nil {
		json.Unmarshal(raw, out)
	}
	return resp.StatusCode
}

// A login an app speaks as the first time waits: 403 login_pending, and one login_join need
// however often it speaks. Allowed, it goes through as itself; denied or removed, it asks again
// as it next speaks. An app has 20 waiting at most.
func TestLoginJoin(t *testing.T) {
	t.Parallel()
	l := start(t)
	app := l.newApp("qq", false)
	var e api.Error
	if code := l.asLogin(app.Token, "u1", "GET", "/v1/internal/me", nil, &e); code != 403 || e.Code != "login_pending" || e.Message == "" {
		t.Fatalf("a new login: %d %+v", code, e)
	}
	l.asLogin(app.Token, "u1", "GET", "/v1/internal/agents", nil, nil)
	nd := l.pending()
	if nd.Kind != api.NeedLoginJoin || nd.Login != "u1" || nd.App == nil || nd.App.ID != app.App.ID || nd.Asker != "qq" {
		t.Fatalf("the need: %+v", nd)
	}
	var as api.Apps
	l.want(200, "socket", "GET", "/v1/internal/apps", nil, &as)
	if i := slices.IndexFunc(as.Logins, func(x api.Login) bool { return x.App == app.App.ID && x.ID == "u1" }); i < 0 || as.Logins[i].ApprovedAt != 0 {
		t.Fatalf("listed: %+v", as.Logins)
	}
	// The app itself, with no login, goes on as it did.
	if code := l.asLogin(app.Token, "", "GET", "/v1/internal/me", nil, nil); code != 200 {
		t.Fatalf("the app: %d", code)
	}
	l.want(200, "socket", "POST", "/v1/internal/needs/"+nd.ID, api.NeedAnswer{Allow: true}, nil)
	var me api.Me
	if code := l.asLogin(app.Token, "u1", "GET", "/v1/internal/me", nil, &me); code != 200 || me.Login != "u1" || me.App != app.App.ID || me.ID != app.App.ID {
		t.Fatalf("approved: %d %+v", code, me)
	}

	// Denied: gone; it asks again.
	l.asLogin(app.Token, "u2", "GET", "/v1/internal/me", nil, nil)
	l.want(200, "socket", "POST", "/v1/internal/needs/"+l.pending().ID, api.NeedAnswer{}, nil)
	if l.want(200, "socket", "GET", "/v1/internal/apps", nil, &as); slices.ContainsFunc(as.Logins, func(x api.Login) bool { return x.ID == "u2" }) {
		t.Fatalf("a denied login listed: %+v", as.Logins)
	}
	if code := l.asLogin(app.Token, "u2", "GET", "/v1/internal/me", nil, &e); code != 403 || e.Code != "login_pending" {
		t.Fatalf("denied, again: %d %+v", code, e)
	}
	if l.pending().Login != "u2" {
		t.Fatal("no need again")
	}

	// Removed (ir apps rm qq/u1, or its own route): it asks again.
	l.want(200, "socket", "DELETE", "/v1/internal/apps/"+url.PathEscape("qq/u1"), nil, nil)
	if code := l.asLogin(app.Token, "u1", "GET", "/v1/internal/me", nil, &e); code != 403 || e.Code != "login_pending" {
		t.Fatalf("removed: %d %+v", code, e)
	}
	l.want(200, "socket", "DELETE", "/v1/internal/apps/qq/logins/u1", nil, nil)
	l.want(422, "socket", "DELETE", "/v1/internal/apps/qq/logins/u1", nil, nil)
	l.want(403, "agent", "DELETE", "/v1/internal/apps/qq/logins/u2", nil, nil)

	// Bounds: its ID, how many wait; a TOKEN not valid is 401 whatever it names.
	if code := l.asLogin(app.Token, "a/b", "GET", "/v1/internal/me", nil, &e); code != 422 || e.Code != "bad_login" {
		t.Fatalf("a bad login: %d %+v", code, e)
	}
	if code := l.asLogin("ir_nope", "u1", "GET", "/v1/internal/me", nil, &e); code != 401 || e.Code != "unauthenticated" {
		t.Fatalf("a bad TOKEN: %d %+v", code, e)
	}
	other := l.newApp("busy", false)
	for i := range 20 {
		if code := l.asLogin(other.Token, fmt.Sprint("p", i), "GET", "/v1/internal/me", nil, &e); code != 403 || e.Code != "login_pending" {
			t.Fatalf("login %d: %d %+v", i, code, e)
		}
	}
	if code := l.asLogin(other.Token, "p20", "GET", "/v1/internal/me", nil, &e); code != 403 || e.Code != "too_many_pending" {
		t.Fatalf("the 21st: %d %+v", code, e)
	}
	if code := l.asLogin(other.Token, "p3", "GET", "/v1/internal/me", nil, &e); code != 403 || e.Code != "login_pending" {
		t.Fatalf("one waiting, again: %d %+v", code, e)
	}
}

// Who approves (decision 15): a person at the host, or on a login of ir_web that may use every
// agent (Claude too) everywhere. Never an agent's ir, an app, a login without Claude or one kept to
// some places.
func TestApprovers(t *testing.T) {
	t.Parallel()
	l := start(t)
	panel := func(id string) gate.EndpointInfo { return gate.EndpointInfo{ID: id, Name: id, Kind: gate.KindPanel} }
	claude, _ := json.Marshal(access.WithClaude(access.Default(), access.ByLocal, 1))
	l.s.joined(webapp.Joined{Endpoint: panel("owner0000000000a"), Perms: claude})
	l.s.joined(webapp.Joined{Endpoint: panel("kept00000000000a"), Perms: claude})
	l.s.joined(webapp.Joined{Endpoint: panel("guest0000000000a")})
	kept, _ := json.Marshal(api.Perms{Agents: []string{"*"}, Scope: []string{"pc:/work"}})
	if _, err := l.s.st.AddLogin(gate.WebApp, "kept00000000000a", "kept", 20, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := l.s.st.SetLoginPerms("kept00000000000a", kept); err != nil {
		t.Fatal(err)
	}
	web := func(id string) caller { return caller{Identity: gate.Identity{App: gate.WebApp, Login: id, Name: id}} }
	for name, c := range map[string]struct {
		c    caller
		want bool
	}{
		"host's ir":    {caller{Identity: localIdentity, socket: true}, true},
		"host's page":  {caller{Identity: localIdentity}, true},
		"agent":        {caller{Identity: localIdentity, socket: true, agent: true}, false},
		"agent in web": {caller{Identity: web("owner0000000000a").Identity, socket: true, agent: true, session: "s"}, false},
		"owner":        {web("owner0000000000a"), true},
		"kept":         {web("kept00000000000a"), false},
		"guest":        {web("guest0000000000a"), false},
		"app":          {caller{Identity: gate.Identity{App: "app0000000000000", Name: "bot"}}, false},
	} {
		if got := l.s.mayApprove(c.c); got != c.want {
			t.Errorf("%s: %v, want %v", name, got, c.want)
		}
	}
	// Over HTTP: an agent and an app are refused with a code; the host's ir lists them.
	app := l.newApp("bot", false)
	var e api.Error
	if l.want(403, "agent", "POST", "/v1/internal/needs/x", api.NeedAnswer{}, &e); e.Code != "no_permission" {
		t.Fatalf("an agent: %+v", e)
	}
	// An app sees only the asks of sessions it may use (none here), never the applications.
	var ns api.Needs
	if l.want(200, app.Token, "GET", "/v1/internal/needs", nil, &ns); len(ns.Pending) != 0 {
		t.Fatalf("an app's needs: %+v", ns)
	}
	l.want(403, "agent", "GET", "/v1/internal/needs", nil, nil)
	l.want(403, app.Token, "DELETE", "/v1/internal/apps/bot", nil, nil)
	l.want(403, "agent", "DELETE", "/v1/internal/apps/bot", nil, nil)
	// What an app may use: set by whoever approves, cleaned; never by an agent or an app.
	l.want(403, "agent", "PUT", "/v1/internal/apps/bot/perms", api.Perms{}, nil)
	l.want(403, app.Token, "PUT", "/v1/internal/apps/bot/perms", api.Perms{}, nil)
	var p api.Perms
	l.want(200, "socket", "PUT", "/v1/internal/apps/bot/perms", api.Perms{Agents: []string{"codex", "codex", " "}, Scope: []string{"pc:/w"}}, &p)
	if got := l.s.appPerms(app.App.ID); got == nil || len(got.Agents) != 1 || got.Agents[0] != "codex" || got.Scope[0] != "pc:/w" || len(p.Agents) != 1 {
		t.Fatalf("perms set: %+v %+v", got, p)
	}
	l.want(422, "socket", "PUT", "/v1/internal/apps/主机/perms", api.Perms{}, nil)
	if l.want(422, "socket", "POST", "/v1/internal/needs/x", api.NeedAnswer{}, &e); e.Code != "no_such_need" {
		t.Fatalf("no such need: %+v", e)
	}
}
