package server

import (
	"encoding/json"
	"testing"

	"github.com/kumoSleeping/iridium/internal/access"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/gate"
)

// who is the app or login id as GET /api/apps lists it: its name and rules.
func (l *live) who(as, id string) api.App {
	l.t.Helper()
	var as_ api.Apps
	l.want(200, as, "GET", "/api/apps", nil, &as_)
	for _, x := range as_.Apps {
		if x.ID == id {
			return x
		}
	}
	for _, x := range as_.Logins {
		if x.ID == id {
			return api.App{ID: x.ID, Name: x.Name, Rules: x.Rules, UsedToday: x.UsedToday, Device: x.Device, Online: x.Online}
		}
	}
	l.t.Fatalf("no app or login %s in %+v", id, as_)
	return api.App{}
}

// Claude only for the owner's own, said by a person as an app or login is made: an app made with
// it runs Claude, one without may not. ir token and POST /api/terminals/tokens are gone: an app comes
// by pairing only (docs/next/decisions.md 9). An invite with Claude is a person's: an agent's only
// asks, the local page's is refused.
func TestClaudeOnlyFromTheMachine(t *testing.T) {
	l := start(t)
	for _, path := range []string{"/api/terminals/tokens", "/api/terminals", "/api/apps/tokens"} {
		if code := l.do("socket", "POST", path, api.NewInvite{Name: "mine"}, nil); code != 404 && code != 405 {
			t.Errorf("POST %s: %d", path, code)
		}
	}
	l.want(409, "agent", "POST", "/api/invites", api.NewInvite{Claude: true}, nil) // no public origin
	l.want(403, "web", "POST", "/api/invites", api.NewInvite{Claude: true}, nil)
	mine := l.newApp("mine", true)
	if !access.HasClaude(mine.App.Rules) || mine.App.Rules.ClaudeOwner.By != "local" {
		t.Fatalf("an app with Claude: %+v", mine.App.Rules)
	}
	l.want(200, mine.Token, "POST", "/api/sessions", api.NewSession{Agent: api.AgentClaude, Cwd: l.work}, nil)
	other := l.newApp("other", false)
	if access.HasClaude(other.App.Rules) {
		t.Fatalf("a plain app has claude: %+v", other.App.Rules)
	}
	l.want(403, other.Token, "POST", "/api/sessions", api.NewSession{Agent: api.AgentClaude, Cwd: l.work}, nil)
}

// A new login of ir_web gets Claude when the gate says it is the first, or its invite's perms give
// it; any other does not. An invite with Claude is listed as one.
func TestClaudeForJoinedPanels(t *testing.T) {
	l := start(t)
	panel := func(id string) gate.EndpointInfo { return gate.EndpointInfo{ID: id, Name: id, Kind: gate.KindPanel} }
	perms, _ := json.Marshal(access.WithClaude(access.Default(), access.ByLocal, 1))
	l.s.joined(gate.Joined{Endpoint: panel("first0000000000a"), First: true})
	l.s.joined(gate.Joined{Endpoint: panel("owner0000000000a"), Perms: perms})
	l.s.joined(gate.Joined{Endpoint: panel("guest0000000000a")})
	for id, by := range map[string]string{"first0000000000a": "first", "owner0000000000a": "local", "guest0000000000a": ""} {
		r := l.s.rulesOf(id, false)
		if access.HasClaude(r) != (by != "") || (by != "" && r.ClaudeOwner.By != by) {
			t.Errorf("%s: %+v", id, r)
		}
	}
	if b, ok, _ := l.s.st.TerminalRules("guest0000000000a"); ok {
		t.Errorf("rules written for a plain login: %s", b)
	}

	// With a public origin: ir invite --claude is stored with the invite and listed.
	l.want(200, "socket", "PUT", "/api/public", api.SetPublic{Origin: "https://ir.example.com"}, nil)
	l.want(200, "socket", "POST", "/api/invites", api.NewInvite{Name: "pad", Claude: true}, nil)
	l.want(200, "socket", "POST", "/api/invites", api.NewInvite{Name: "guest"}, nil)
	var as api.Apps
	l.want(200, "socket", "GET", "/api/apps", nil, &as)
	claude := map[string]bool{}
	for _, i := range as.Invites {
		claude[i.Name] = i.Claude
	}
	if !claude["pad"] || claude["guest"] {
		b, _ := json.Marshal(as.Invites)
		t.Fatalf("invites: %s", b)
	}
	// ir_web is listed (a public origin), after the host.
	if len(as.Apps) < 2 || !as.Apps[0].Host || !as.Apps[1].Web || as.Apps[1].ID != api.WebApp {
		t.Fatalf("apps: %+v", as.Apps)
	}
}
