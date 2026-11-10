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
	"github.com/kumoSleeping/iridium/internal/webapp"
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

// Who may give Claude: a person at the host, or a person on a login of ir_web that has Claude.
// Not a login without it, not the local page, never an agent, not even one in a session the
// owner's login opened. An agent's ask waits for one that may to allow it.
func TestOwnerFromPanels(t *testing.T) {
	l := start(t)
	panel := func(id string) gate.EndpointInfo { return gate.EndpointInfo{ID: id, Name: id, Kind: gate.KindPanel} }
	perms, _ := json.Marshal(access.WithClaude(access.Default(), access.ByLocal, 1))
	l.s.joined(webapp.Joined{Endpoint: panel("owner0000000000a"), Perms: perms})
	l.s.joined(webapp.Joined{Endpoint: panel("guest0000000000a")})
	who := func(id string) gate.Identity { return gate.Identity{App: gate.WebApp, Login: id, Name: id} }
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
	l.want(200, "socket", "PUT", "/api/public", api.SetPublic{Origin: "https://ir.example.com"}, nil)
	claudeOf := func(name string) *api.OwnerConfirm {
		t.Helper()
		var as api.Apps
		l.want(200, "socket", "GET", "/api/apps", nil, &as)
		for _, i := range as.Invites {
			if i.Name == name {
				if !i.Claude {
					return nil
				}
				inv := l.s.web().Invites()
				for _, x := range inv {
					if x.Name == name {
						var r api.Rules
						json.Unmarshal(x.Perms, &r)
						return r.ClaudeOwner
					}
				}
			}
		}
		return nil
	}

	// A person on the owner's login: Claude straight away. On another login: refused.
	if code := as(t, owner, l.s.invite, "POST", "", api.NewInvite{Name: "own", Claude: true}, nil); code != 200 {
		t.Fatalf("the owner's login's invite with Claude: %d", code)
	}
	if c := claudeOf("own"); c == nil || c.By != "panel" {
		t.Fatalf("the invite's Claude: %+v", c)
	}
	if code := as(t, guest, l.s.invite, "POST", "", api.NewInvite{Name: "guest2", Claude: true}, nil); code != 403 {
		t.Fatalf("a login without Claude gave Claude: %d", code)
	}
	// A login that may not approve (no Claude) makes no slot at all: it would invite a wider one.
	if code := as(t, guest, l.s.invite, "POST", "", api.NewInvite{Name: "plain"}, nil); code != 403 {
		t.Fatalf("a plain invite from a login that may not approve: %d", code)
	}
	if code := as(t, owner, l.s.invite, "POST", "", api.NewInvite{Name: "plain"}, nil); code != 200 || claudeOf("plain") != nil {
		t.Fatalf("a plain invite from the owner's login: %d", code)
	}

	// An agent's: a confirmation that says so, no secret.
	var p api.Pending
	l.want(202, "agent", "POST", "/api/invites", api.NewInvite{Name: "pad", Claude: true}, &p)
	if !p.Need.Owner {
		t.Fatalf("pending: %+v", p.Need)
	}
	l.want(202, "agent-in:s", "POST", "/api/invites", api.NewInvite{Name: "pad2", Claude: true}, nil)
	// Neither a login without Claude nor a page without a login allows it; it stays waiting.
	if code := as(t, guest, l.s.approver(l.s.answerNeed), "POST", p.Need.ID, api.NeedAnswer{Allow: true}, nil); code != 403 {
		t.Fatalf("a login without Claude allowed Claude: %d", code)
	}
	if code := as(t, page, l.s.approver(l.s.answerNeed), "POST", p.Need.ID, api.NeedAnswer{Allow: true}, nil); code != 403 {
		t.Fatalf("the page without a login allowed: %d", code)
	}
	var st api.NeedState
	l.want(200, "agent", "GET", "/api/needs/"+p.Need.ID, nil, &st)
	if st.State != "pending" || !st.Owner {
		t.Fatalf("after the refusals: %+v", st)
	}
	// The owner's login allows it: the invite has Claude, marked by a login.
	var ans api.NeedReply
	if code := as(t, owner, l.s.approver(l.s.answerNeed), "POST", p.Need.ID, api.NeedAnswer{Allow: true}, &ans); code != 200 || ans.Invite == nil {
		t.Fatalf("the owner's login allowing: %d %+v", code, ans)
	}
	if c := claudeOf("pad"); c == nil || c.By != "panel" {
		t.Fatalf("allowed invite's Claude: %+v", c)
	}
	l.want(200, "agent", "GET", "/api/needs/"+p.Need.ID, nil, &st)
	if st.State != "allowed" || !st.Owner {
		t.Fatalf("the agent's view: %+v", st)
	}

	// A login that may not approve may not deny either (decision 15); one that may, does.
	l.want(202, "agent", "POST", "/api/invites", api.NewInvite{Name: "pad3", Claude: true}, &p)
	if code := as(t, guest, l.s.approver(l.s.answerNeed), "POST", p.Need.ID, api.NeedAnswer{Allow: false}, &ans); code != 403 {
		t.Fatalf("a login without Claude denying: %d %+v", code, ans)
	}
	if code := as(t, owner, l.s.approver(l.s.answerNeed), "POST", p.Need.ID, api.NeedAnswer{Allow: false}, &ans); code != 200 || ans.State != "denied" {
		t.Fatalf("a login denying: %d %+v", code, ans)
	}
	// The host's ir allows one: marked local.
	l.want(202, "agent", "POST", "/api/invites", api.NewInvite{Name: "pad4", Claude: true}, &p)
	ans = api.NeedReply{}
	l.want(200, "socket", "POST", "/api/needs/"+p.Need.ID, api.NeedAnswer{Allow: true}, &ans)
	if c := claudeOf("pad4"); ans.Invite == nil || c == nil || c.By != "local" {
		t.Fatalf("the host allowing: %+v %+v", ans, c)
	}
}
