package server

import (
	"encoding/json"
	"testing"

	"github.com/kumoSleeping/iridium/internal/api"
)

// who is the app or login id as GET /v1/internal/apps lists it.
func (l *live) who(as, id string) api.App {
	l.t.Helper()
	var as_ api.Apps
	l.want(200, as, "GET", "/v1/internal/apps", nil, &as_)
	for _, x := range as_.Apps {
		if x.ID == id {
			return x
		}
	}
	for _, x := range as_.Logins {
		if x.ID == id {
			return api.App{ID: x.ID, Name: x.Name, UsedToday: x.UsedToday, Device: x.Device, Online: x.Online}
		}
	}
	l.t.Fatalf("no app or login %s in %+v", id, as_)
	return api.App{}
}

// Claude is an agent like the others: any app runs it. ir token and POST
// /v1/internal/terminals/tokens are gone: an app comes by pairing only (docs/next/decisions.md 9).
func TestClaudeLikeAnyAgent(t *testing.T) {
	t.Parallel()
	l := start(t)
	for _, path := range []string{"/v1/internal/terminals/tokens", "/v1/internal/terminals", "/v1/internal/apps/tokens"} {
		if code := l.do("socket", "POST", path, api.NewInvite{Name: "mine"}, nil); code != 404 && code != 405 {
			t.Errorf("POST %s: %d", path, code)
		}
	}
	l.want(403, "agent", "POST", "/v1/internal/invites", api.NewInvite{}, nil) // no public origin
	app := l.newApp("mine")
	l.create(200, app.Token, api.NewSession{Agent: api.AgentClaude, Cwd: l.work})
}

// Invites are listed; ir_web with them, after the host, once there is a public origin.
func TestInvitesListed(t *testing.T) {
	t.Parallel()
	l := start(t)
	l.want(200, "socket", "PUT", "/v1/internal/public", api.SetPublic{Origin: "https://ir.example.com"}, nil)
	l.want(200, "socket", "POST", "/v1/internal/invites", api.NewInvite{Name: "pad"}, nil)
	l.want(200, "socket", "POST", "/v1/internal/invites", api.NewInvite{Name: "guest"}, nil)
	var as api.Apps
	l.want(200, "socket", "GET", "/v1/internal/apps", nil, &as)
	named := 0
	for _, i := range as.Invites {
		if i.Name == "pad" || i.Name == "guest" {
			named++
		}
	}
	if named != 2 {
		b, _ := json.Marshal(as.Invites)
		t.Fatalf("invites: %s", b)
	}
	if len(as.Apps) < 2 || !as.Apps[0].Host || !as.Apps[1].Web || as.Apps[1].ID != api.WebApp {
		t.Fatalf("apps: %+v", as.Apps)
	}
}
