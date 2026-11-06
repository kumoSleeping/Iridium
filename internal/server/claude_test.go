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

// Claude only for the owner's own terminals, said by a person (plan "交互终端"): ir token --owner
// gives it, a plain token does not; the local page's --owner is refused, an agent's only asks
// (TestOwnerFromPanels has panels that have Claude). The local page only takes it away; a
// person's ir gives it back.
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
	if !access.HasClaude(mine.Terminal.Rules) || mine.Terminal.Rules.ClaudeOwner.By != "local" || !mine.Terminal.Rules.MustApprove {
		t.Fatalf("ir token --owner: %+v", mine.Terminal.Rules)
	}
	l.want(200, mine.Token, "POST", "/api/sessions", api.NewSession{Agent: api.AgentClaude, Cwd: l.work}, nil)
	l.want(200, "socket", "POST", "/api/terminals/tokens", api.NewInvite{Name: "other"}, &other)
	if access.HasClaude(other.Terminal.Rules) {
		t.Fatalf("a plain token has claude: %+v", other.Terminal.Rules)
	}
	l.want(403, other.Token, "POST", "/api/sessions", api.NewSession{Agent: api.AgentClaude, Cwd: l.work}, nil)
	// A new token without --owner leaves Claude as it was.
	l.want(200, "socket", "POST", "/api/terminals/tokens", api.NewInvite{Name: "mine"}, &mine)
	if !access.HasClaude(mine.Terminal.Rules) {
		t.Fatalf("a new token took claude away: %+v", mine.Terminal.Rules)
	}

	// The web may not turn it on, for another or (a channel) for itself; an agent neither.
	on := other.Terminal.Rules
	on.Agents = append(on.Agents, api.AgentClaude)
	on.ClaudeOwner = &api.OwnerConfirm{By: "local"}
	for _, as := range []string{"web", "agent", other.Token} {
		if code := l.rules(as, other.Terminal.ID, on); code != 403 {
			t.Errorf("%.5s turned claude on: %d", as, code)
		}
	}
	if access.HasClaude(l.terminal("socket", other.Terminal.ID).Rules) {
		t.Fatal("claude is on")
	}

	// The web (and an agent's ir) may turn it off; another change keeps it as it was.
	keep := mine.Terminal.Rules
	keep.ClaudeOwner, keep.Answer = nil, false
	if code := l.rules("web", mine.Terminal.ID, keep); code != 200 {
		t.Fatalf("the web narrowed: %d", code)
	}
	if r := l.terminal("socket", mine.Terminal.ID).Rules; !access.HasClaude(r) || r.ClaudeOwner.By != "local" || r.Answer {
		t.Fatalf("after narrowing: %+v", r)
	}
	off := access.WithoutClaude(keep)
	if code := l.rules("web", mine.Terminal.ID, off); code != 200 {
		t.Fatalf("the web turned claude off: %d", code)
	}
	if access.HasClaude(l.terminal("socket", mine.Terminal.ID).Rules) {
		t.Fatal("claude still on")
	}
	l.want(403, mine.Token, "POST", "/api/sessions", api.NewSession{Agent: api.AgentClaude, Cwd: l.work}, nil)
	if code := l.rules("web", mine.Terminal.ID, keep); code != 403 {
		t.Fatalf("the web turned it back on: %d", code)
	}
	// Back on: only a person's ir (ir access mine --claude on).
	if code := l.rules("socket", mine.Terminal.ID, keep); code != 200 {
		t.Fatalf("ir access --claude on: %d", code)
	}
	if r := l.terminal("socket", mine.Terminal.ID).Rules; !access.HasClaude(r) || r.ClaudeOwner.By != "local" {
		t.Fatalf("back on: %+v", r)
	}
	on2 := l.terminal("socket", mine.Terminal.ID).Rules
	if code := l.rules("agent", mine.Terminal.ID, access.WithoutClaude(on2)); code != 200 {
		t.Fatalf("an agent turned claude off: %d", code)
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
		r := l.s.rulesOf(id, gate.KindPanel, false)
		if access.HasClaude(r) != (by != "") || (by != "" && r.ClaudeOwner.By != by) {
			t.Errorf("%s: %+v", id, r)
		}
		if by != "" && (!r.AllDirs || !r.SeeOthers || r.MustApprove) {
			t.Errorf("%s lost the panel's defaults: %+v", id, r)
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
