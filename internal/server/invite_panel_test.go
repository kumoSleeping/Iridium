package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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

// Who makes an invite: one that approves (a person at the host, a login of ir_web that may use
// every agent everywhere); not a login kept to some places. An agent's asks, and waits for one
// that approves to allow it.
func TestInvitesFromPanels(t *testing.T) {
	t.Parallel()
	l := start(t)
	panel := func(id string) gate.EndpointInfo { return gate.EndpointInfo{ID: id, Name: id, Kind: gate.KindPanel} }
	l.s.joined(webapp.Joined{Endpoint: panel("owner0000000000a")})
	l.s.joined(webapp.Joined{Endpoint: panel("kept00000000000a")})
	kept, _ := json.Marshal(api.Perms{Agents: []string{"*"}, Scope: []string{"pc:/work"}})
	if _, err := l.s.st.AddLogin(gate.WebApp, "kept00000000000a", "kept", 20, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := l.s.st.SetLoginPerms("kept00000000000a", kept); err != nil {
		t.Fatal(err)
	}
	who := func(id string) gate.Identity { return gate.Identity{App: gate.WebApp, Login: id, Name: id} }
	full, narrow := caller{Identity: who("owner0000000000a")}, caller{Identity: who("kept00000000000a")}
	l.want(200, "socket", "PUT", "/v1/internal/public", api.SetPublic{Origin: "https://ir.example.com"}, nil)
	invited := func(name string) bool {
		t.Helper()
		var as api.Apps
		l.want(200, "socket", "GET", "/v1/internal/apps", nil, &as)
		for _, i := range as.Invites {
			if i.Name == name {
				return true
			}
		}
		return false
	}

	if code := as(t, full, l.s.invite, "POST", "", api.NewInvite{Name: "own"}, nil); code != 200 || !invited("own") {
		t.Fatalf("an invite from a login that approves: %d", code)
	}
	// A login kept to some places makes no slot: it would invite a wider one.
	if code := as(t, narrow, l.s.invite, "POST", "", api.NewInvite{Name: "plain"}, nil); code != 403 {
		t.Fatalf("an invite from a login that may not approve: %d", code)
	}

	// An agent's: a confirmation, no secret. The kept login may not answer it; it stays waiting.
	var p api.Pending
	l.want(202, "agent", "POST", "/v1/internal/invites", api.NewInvite{Name: "pad"}, &p)
	if code := as(t, narrow, l.s.approver(l.s.answerNeed), "POST", p.Need.ID, api.NeedAnswer{Allow: true}, nil); code != 403 {
		t.Fatalf("a login that may not approve allowed: %d", code)
	}
	var st api.NeedState
	l.want(200, "agent", "GET", "/v1/internal/needs/"+p.Need.ID, nil, &st)
	if st.State != "pending" {
		t.Fatalf("after the refusal: %+v", st)
	}
	var ans api.NeedReply
	if code := as(t, full, l.s.approver(l.s.answerNeed), "POST", p.Need.ID, api.NeedAnswer{Allow: true}, &ans); code != 200 || ans.Invite == nil || !invited("pad") {
		t.Fatalf("a login allowing: %d %+v", code, ans)
	}
	l.want(200, "agent", "GET", "/v1/internal/needs/"+p.Need.ID, nil, &st)
	if st.State != "allowed" {
		t.Fatalf("the agent's view: %+v", st)
	}

	// A login that may not approve may not deny either (decision 15); one that may, does.
	l.want(202, "agent", "POST", "/v1/internal/invites", api.NewInvite{Name: "pad3"}, &p)
	if code := as(t, narrow, l.s.approver(l.s.answerNeed), "POST", p.Need.ID, api.NeedAnswer{Allow: false}, &ans); code != 403 {
		t.Fatalf("a login that may not approve denying: %d %+v", code, ans)
	}
	if code := as(t, full, l.s.approver(l.s.answerNeed), "POST", p.Need.ID, api.NeedAnswer{Allow: false}, &ans); code != 200 || ans.State != "denied" {
		t.Fatalf("a login denying: %d %+v", code, ans)
	}
	// The host's ir allows one.
	l.want(202, "agent", "POST", "/v1/internal/invites", api.NewInvite{Name: "pad4"}, &p)
	ans = api.NeedReply{}
	l.want(200, "socket", "POST", "/v1/internal/needs/"+p.Need.ID, api.NeedAnswer{Allow: true}, &ans)
	if ans.Invite == nil || !invited("pad4") {
		t.Fatalf("the host allowing: %+v", ans)
	}
}
