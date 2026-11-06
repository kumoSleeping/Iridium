package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kumoSleeping/iridium/internal/access"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/gate"
)

// as runs handler h for caller c, as the routes would: c in the request's context.
func as(t *testing.T, c caller, h http.HandlerFunc, method, id string, body, out any) int {
	t.Helper()
	b, _ := json.Marshal(body)
	r := httptest.NewRequest(method, "http://ir/x", bytes.NewReader(b))
	r.SetPathValue("id", id)
	r = r.WithContext(context.WithValue(gate.NewContext(r.Context(), c.Identity), callerKey{}, c))
	w := httptest.NewRecorder()
	h(w, r)
	if out != nil {
		json.Unmarshal(w.Body.Bytes(), out)
	}
	return w.Code
}

// Who may say a terminal is the owner's own: a person at the machine, or a person on a passkey
// panel that has Claude. Not a panel without it, not the local page, never an agent, not even one
// in a session the owner's panel opened. An agent's --owner waits for one that may to allow it.
func TestOwnerFromPanels(t *testing.T) {
	l := start(t)
	panel := func(id string) gate.EndpointInfo { return gate.EndpointInfo{ID: id, Name: id, Kind: gate.KindPanel} }
	l.s.joined(gate.Joined{Endpoint: panel("owner0000000000a"), Owner: true})
	l.s.joined(gate.Joined{Endpoint: panel("guest0000000000a")})
	who := func(id string) gate.Identity { return gate.Identity{EndpointID: id, Name: id, Kind: gate.KindPanel} }
	owner, guest := caller{Identity: who("owner0000000000a")}, caller{Identity: who("guest0000000000a")}
	agentIn := caller{Identity: who("owner0000000000a"), socket: true, agent: true, session: "s"}
	page := caller{Identity: localIdentity}
	machine := caller{Identity: localIdentity, socket: true}
	for name, c := range map[string]struct {
		c    caller
		want string
	}{"owner": {owner, "panel"}, "guest": {guest, ""}, "agent": {agentIn, ""}, "page": {page, ""}, "machine": {machine, "local"}} {
		if got := l.s.ownerSays(c.c); got != c.want {
			t.Errorf("%s: ownerSays %q, want %q", name, got, c.want)
		}
	}

	// A person on the owner's panel: --owner straight away. On another panel: refused.
	var tok api.Token
	if code := as(t, owner, l.s.token, "POST", "", api.NewInvite{Name: "ownbot", Owner: true}, &tok); code != 200 || !access.HasClaude(tok.Terminal.Rules) || tok.Terminal.Rules.ClaudeOwner.By != "panel" {
		t.Fatalf("the owner's panel's --owner: %d %+v", code, tok.Terminal.Rules)
	}
	if code := as(t, guest, l.s.token, "POST", "", api.NewInvite{Name: "guestbot", Owner: true}, nil); code != 403 {
		t.Fatalf("a panel without Claude said --owner: %d", code)
	}
	if code := as(t, guest, l.s.token, "POST", "", api.NewInvite{Name: "plainbot"}, &tok); code != 200 || access.HasClaude(tok.Terminal.Rules) {
		t.Fatalf("a plain token from a panel: %d %+v", code, tok.Terminal.Rules)
	}

	// An agent's --owner: a confirmation that says so, no secret.
	var p api.Pending
	l.want(202, "agent", "POST", "/api/terminals/tokens", api.NewInvite{Name: "bot", Owner: true}, &p)
	if !p.Confirm.Owner {
		t.Fatalf("pending: %+v", p.Confirm)
	}
	l.want(202, "agent-in:s", "POST", "/api/terminals/tokens", api.NewInvite{Name: "bot2", Owner: true}, nil)
	// Neither a panel without Claude nor the local page allows it; it stays waiting.
	if code := as(t, guest, l.s.answerConfirm, "POST", p.Confirm.ID, api.ConfirmAnswer{Allow: true}, nil); code != 403 {
		t.Fatalf("a panel without Claude allowed --owner: %d", code)
	}
	l.want(403, "web", "POST", "/api/confirms/"+p.Confirm.ID, api.ConfirmAnswer{Allow: true}, nil)
	var st api.ConfirmState
	l.want(200, "agent", "GET", "/api/confirms/"+p.Confirm.ID, nil, &st)
	if st.State != "pending" || !st.Owner {
		t.Fatalf("after the refusals: %+v", st)
	}
	// The owner's panel allows it: the token has Claude, marked by a panel.
	var ans api.ConfirmReply
	if code := as(t, owner, l.s.answerConfirm, "POST", p.Confirm.ID, api.ConfirmAnswer{Allow: true}, &ans); code != 200 || ans.Token == nil {
		t.Fatalf("the owner's panel allowing: %d %+v", code, ans)
	}
	if r := ans.Token.Terminal.Rules; !access.HasClaude(r) || r.ClaudeOwner.By != "panel" {
		t.Fatalf("allowed --owner token: %+v", r)
	}
	l.want(200, "agent", "GET", "/api/confirms/"+p.Confirm.ID, nil, &st)
	if st.State != "allowed" || !st.Owner {
		t.Fatalf("the agent's view: %+v", st)
	}

	// Any panel may deny one.
	l.want(202, "agent", "POST", "/api/terminals/tokens", api.NewInvite{Name: "bot3", Owner: true}, &p)
	if code := as(t, guest, l.s.answerConfirm, "POST", p.Confirm.ID, api.ConfirmAnswer{Allow: false}, &ans); code != 200 || ans.State != "denied" {
		t.Fatalf("a panel denying: %d %+v", code, ans)
	}
	// 本机's ir allows one: marked local.
	l.want(202, "agent", "POST", "/api/terminals/tokens", api.NewInvite{Name: "bot4", Owner: true}, &p)
	ans = api.ConfirmReply{}
	l.want(200, "socket", "POST", "/api/confirms/"+p.Confirm.ID, api.ConfirmAnswer{Allow: true}, &ans)
	if ans.Token == nil || ans.Token.Terminal.Rules.ClaudeOwner == nil || ans.Token.Terminal.Rules.ClaudeOwner.By != "local" {
		t.Fatalf("本机 allowing: %+v", ans)
	}

	// --claude on: the owner's panel may, a panel without Claude may not.
	plain := l.terminal("socket", tok.Terminal.ID).Rules
	on := plain
	on.Agents = append(on.Agents, api.AgentClaude)
	if code := as(t, guest, l.s.setRules, "PUT", tok.Terminal.ID, api.SetRules{Rules: on}, nil); code != 403 {
		t.Fatalf("a panel without Claude turned claude on: %d", code)
	}
	var got api.Terminal
	if code := as(t, owner, l.s.setRules, "PUT", tok.Terminal.ID, api.SetRules{Rules: on}, &got); code != 200 || !access.HasClaude(got.Rules) || got.Rules.ClaudeOwner.By != "panel" {
		t.Fatalf("the owner's panel turning claude on: %d %+v", code, got.Rules)
	}
}
