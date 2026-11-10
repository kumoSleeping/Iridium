package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/access"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/gate"
	"github.com/kumoSleeping/iridium/internal/satori"
)

// What an app or login may use: its scope hides the sessions out of it (not listed, not found),
// its agents keep it from using a session it may still see, and a login of an app gets what both
// allow. The host may use all.
func TestPerms(t *testing.T) {
	l := start(t)
	a, b := filepath.Join(l.work, "a"), filepath.Join(l.work, "b")
	for _, d := range []string{a, b} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	var inA, inB api.NewSessionReply
	l.want(200, "web", "POST", "/api/sessions", api.NewSession{Agent: api.AgentClaude, Cwd: a, Text: "one"}, &inA)
	l.want(200, "web", "POST", "/api/sessions", api.NewSession{Agent: api.AgentClaude, Cwd: b, Text: "two"}, &inB)

	app := l.newApp("bot", true)
	l.want(200, "socket", "PUT", "/api/apps/bot/perms", api.Perms{Agents: []string{"*"}, Scope: []string{"主机:" + a}}, nil)
	var list []api.Session
	l.want(200, app.Token, "GET", "/api/sessions", nil, &list)
	if len(list) != 1 || list[0].ID != inA.ID {
		t.Fatalf("listed out of scope: %+v", list)
	}
	l.want(200, app.Token, "GET", "/api/sessions/"+inA.ID, nil, nil)
	// Satori sees the same: its channels are the sessions in scope.
	if sb := (satoriBackend{l.s}); !sb.Sees(satori.Caller{App: app.App.ID}, inA.ID) || sb.Sees(satori.Caller{App: app.App.ID}, inB.ID) {
		t.Fatal("Satori's channels out of scope")
	}
	l.want(404, app.Token, "GET", "/api/sessions/"+inB.ID, nil, nil)
	l.want(404, app.Token, "GET", "/api/sessions/"+inB.ID+"/messages", nil, nil)
	l.want(404, app.Token, "POST", "/api/sessions/"+inB.ID+"/stop", nil, nil)
	// A new session only within its scope.
	l.want(403, app.Token, "POST", "/api/sessions", api.NewSession{Agent: api.AgentClaude, Cwd: b, Text: "x"}, nil)
	// The host sees both.
	l.want(200, "socket", "GET", "/api/sessions/"+inB.ID, nil, nil)

	// An agent it may not use: the session it sees, it may not use.
	l.want(200, "socket", "PUT", "/api/apps/bot/perms", api.Perms{Agents: []string{"codex"}, Scope: []string{"*"}}, nil)
	l.want(200, app.Token, "GET", "/api/sessions/"+inB.ID, nil, nil)
	var e api.Error
	if l.want(403, app.Token, "PATCH", "/api/sessions/"+inB.ID, map[string]any{"name": "x"}, &e); e.Code != "no_permission" {
		t.Fatalf("an agent it may not use: %+v", e)
	}
	l.want(403, app.Token, "POST", "/api/sessions", api.NewSession{Agent: api.AgentClaude, Cwd: b, Text: "x"}, nil)

	// A login of the app: what both allow.
	c := caller{Identity: gate.Identity{App: app.App.ID, Login: "u1", Name: "u1"}}
	if _, err := l.s.st.AddLogin(app.App.ID, "u1", "u1", 20, time.Now()); err != nil {
		t.Fatal(err)
	}
	narrow, _ := json.Marshal(api.Perms{Agents: []string{"*"}, Scope: []string{"主机:" + b}})
	if ok, err := l.s.st.SetLoginPermsOf(app.App.ID, "u1", narrow); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if p := l.s.permsFor(c); len(p.Agents) != 1 || p.Agents[0] != "codex" || len(p.Scope) != 1 || p.Scope[0] != "主机:"+b {
		t.Fatalf("login within app: %+v", p)
	}
	if _, err := l.s.using(c, inA.ID); err == nil {
		t.Fatal("a login used a session out of its scope")
	}
	if _, err := l.s.using(c, inB.ID); !access.IsDenied(err) {
		t.Fatalf("a login used an agent its app may not: %v", err)
	}
	// Set over HTTP by whoever approves, never by an app.
	l.want(200, "socket", "PUT", "/api/apps/bot/logins/u1/perms", api.Perms{Agents: []string{"*"}, Scope: []string{"*"}}, nil)
	l.want(403, app.Token, "PUT", "/api/apps/bot/logins/u1/perms", api.Perms{}, nil)
	l.want(422, "socket", "PUT", "/api/apps/bot/logins/nobody/perms", api.Perms{}, nil)
}

// Places: the app by name, the host by its names, a drive letter is no app.
func TestParsePlace(t *testing.T) {
	l := start(t)
	app := l.newApp("pc", false)
	for in, want := range map[string][2]string{
		"pc:/work":    {app.App.ID, "/work"},
		"主机:/srv":     {gate.HostID, "/srv"},
		"host:/srv":   {gate.HostID, "/srv"},
		"/home/x":     {gate.HostID, "/home/x"},
		`C:\Users\x`:  {gate.HostID, `C:\Users\x`},
		"nobody:/tmp": {"nobody", "/tmp"},
	} {
		if a, p := l.s.parsePlace(in); a != want[0] || p != want[1] {
			t.Errorf("%s: %s %s, want %v", in, a, p, want)
		}
	}
	if !l.s.scopeHas([]string{"pc:/work"}, app.App.ID, "/work/x") || l.s.scopeHas([]string{"pc:/work"}, app.App.ID, "/workshop") || l.s.scopeHas([]string{"pc:/work"}, "", "/work") {
		t.Fatal("scopeHas")
	}
}
