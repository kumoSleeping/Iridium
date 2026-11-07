package server

import (
	"encoding/json"
	"testing"

	"github.com/kumoSleeping/iridium/internal/access"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/gate"
)

func (l *live) terminal(as, id string) api.Terminal {
	l.t.Helper()
	var ts api.Terminals
	l.want(200, as, "GET", "/api/terminals", nil, &ts)
	for _, x := range ts.Terminals {
		if x.ID == id {
			return x
		}
	}
	l.t.Fatalf("no terminal %s in %+v", id, ts.Terminals)
	return api.Terminal{}
}

// Claude only for the owner's own terminals, said by a person as the terminal is made (plan
// "交互终端"): ir token --owner gives it, a plain token does not; the local page's --owner is
// refused, an agent's only asks (TestOwnerFromPanels has panels that have Claude). Nothing turns it
// on or off after.
func TestClaudeOnlyFromTheMachine(t *testing.T) {
	l := start(t)

	// --owner: from a person's ir; an agent's waits for a confirmation, the local page's is refused.
	var p api.Pending
	l.want(202, "agent", "POST", "/api/terminals/tokens", api.NewInvite{Name: "mine", Owner: true}, &p)
	if !p.Confirm.Owner {
		t.Fatalf("an agent's --owner ask: %+v", p.Confirm)
	}
	l.want(403, "web", "POST", "/api/terminals/tokens", api.NewInvite{Name: "mine", Owner: true}, nil)
	l.want(409, "agent", "POST", "/api/terminals/invites", api.NewInvite{Owner: true}, nil) // no public origin
	l.want(403, "web", "POST", "/api/terminals/invites", api.NewInvite{Owner: true}, nil)
	var mine, other api.Token
	l.want(200, "socket", "POST", "/api/terminals/tokens", api.NewInvite{Name: "mine", Owner: true}, &mine)
	if !access.HasClaude(mine.Terminal.Rules) || mine.Terminal.Rules.ClaudeOwner.By != "local" {
		t.Fatalf("ir token --owner: %+v", mine.Terminal.Rules)
	}
	l.want(200, mine.Token, "POST", "/api/sessions", api.NewSession{Agent: api.AgentClaude, Cwd: l.work}, nil)
	l.want(200, "socket", "POST", "/api/terminals/tokens", api.NewInvite{Name: "other"}, &other)
	if access.HasClaude(other.Terminal.Rules) {
		t.Fatalf("a plain token has claude: %+v", other.Terminal.Rules)
	}
	l.want(403, other.Token, "POST", "/api/sessions", api.NewSession{Agent: api.AgentClaude, Cwd: l.work}, nil)
	// A new token without --owner leaves Claude as it was; one with it does not give it after.
	l.want(200, "socket", "POST", "/api/terminals/tokens", api.NewInvite{Name: "mine"}, &mine)
	if !access.HasClaude(mine.Terminal.Rules) {
		t.Fatalf("a new token took claude away: %+v", mine.Terminal.Rules)
	}
	l.want(409, "socket", "POST", "/api/terminals/tokens", api.NewInvite{Name: "other", Owner: true}, nil)
	if access.HasClaude(l.terminal("socket", other.Terminal.ID).Rules) {
		t.Fatal("claude given after")
	}
}

// A new panel gets Claude when the gate says it is the first, or came by an --owner invite;
// any other panel does not. An --owner invite is listed as one.
func TestClaudeForJoinedPanels(t *testing.T) {
	l := start(t)
	panel := func(id string) gate.EndpointInfo { return gate.EndpointInfo{ID: id, Name: id, Kind: gate.KindPanel} }
	l.s.joined(gate.Joined{Endpoint: panel("first0000000000a"), First: true})
	l.s.joined(gate.Joined{Endpoint: panel("owner0000000000a"), Owner: true})
	l.s.joined(gate.Joined{Endpoint: panel("guest0000000000a")})
	for id, by := range map[string]string{"first0000000000a": "first", "owner0000000000a": "local", "guest0000000000a": ""} {
		r := l.s.rulesOf(id, false)
		if access.HasClaude(r) != (by != "") || (by != "" && r.ClaudeOwner.By != by) {
			t.Errorf("%s: %+v", id, r)
		}
	}
	if b, ok, _ := l.s.st.TerminalRules("guest0000000000a"); ok {
		t.Errorf("rules written for a plain panel: %s", b)
	}

	// With a public origin: ir invite --owner is stored with the invite and listed.
	l.want(200, "socket", "PUT", "/api/public", api.SetPublic{Origin: "https://ir.example.com"}, nil)
	l.want(200, "socket", "POST", "/api/terminals/invites", api.NewInvite{Name: "pad", Owner: true}, nil)
	l.want(200, "socket", "POST", "/api/terminals/invites", api.NewInvite{Name: "guest"}, nil)
	var ts api.Terminals
	l.want(200, "socket", "GET", "/api/terminals", nil, &ts)
	owners := map[string]bool{}
	for _, i := range ts.Invites {
		owners[i.Name] = i.Owner
	}
	if !owners["pad"] || owners["guest"] {
		b, _ := json.Marshal(ts.Invites)
		t.Fatalf("invites: %s", b)
	}
}
