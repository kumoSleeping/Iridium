package server

import (
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/channel"
	"github.com/kumoSleeping/iridium/internal/session"
)

// Claude asks for manual approval itself (POST internal/sessions/<its id>/manual): only its own
// agent asks; the ask is the channel's message, and calls the person who spoke there as an @
// does; an agent cannot answer it, a person can; approved, manual mode till the turn ends.
func TestManualAsk(t *testing.T) {
	t.Parallel()
	l := start(t)
	id := l.create(200, "web", api.NewSession{Agent: api.AgentClaude, Cwd: l.work, Text: "go HOLD", Mode: "auto"})
	var st api.SessionState
	for end := time.Now().Add(10 * time.Second); st.Status != api.StatusRunning; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatalf("no turn: %+v", st)
		}
		l.want(200, "web", "GET", "/v1/internal/sessions/"+id, nil, &st)
	}

	// A person does not ask it, nor an agent for another session.
	l.want(403, "web", "POST", "/v1/internal/sessions/"+id+"/manual", api.ManualAsk{Reason: "x"}, nil)
	l.want(403, "agent", "POST", "/v1/internal/sessions/"+id+"/manual", api.ManualAsk{Reason: "x"}, nil)
	l.want(400, "agent-in:"+id, "POST", "/v1/internal/sessions/"+id+"/manual", api.ManualAsk{Reason: "x", Minutes: 99}, nil)
	var ms api.ManualState
	l.want(200, "agent-in:"+id, "POST", "/v1/internal/sessions/"+id+"/manual", api.ManualAsk{Reason: "npx is needed"}, &ms)
	if ms.State != "pending" || ms.Minutes != 10 || ms.RequestID == "" {
		t.Fatalf("asked: %+v", ms)
	}
	card := session.AskCardID(id, ms.RequestID)

	// The channel's message of it, and the host called by it.
	var asked channel.Message
	for end := time.Now().Add(10 * time.Second); asked.Seq == 0; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatal("no message of the ask")
		}
		asked, _ = l.s.ch.ByRef(id, card)
	}
	if asked.Kind != channel.KindAsk {
		t.Fatalf("the ask's message: %+v", asked)
	}
	var ats api.ChannelAts
	for end := time.Now().Add(5 * time.Second); ats[id].Seq != asked.Seq; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatalf("not called: %+v, want #%d", ats, asked.Seq)
		}
		l.want(200, "web", "GET", "/v1/internal/ats", nil, &ats)
	}

	// An agent cannot answer it, its own nor another's.
	if code := l.answer("agent-in:"+id, api.CardAnswer{Card: card, Option: session.OptionAllow}); code != 403 {
		t.Fatalf("its own agent answered: %d", code)
	}
	if code := l.answer("agent", api.CardAnswer{Card: card, Option: session.OptionAllow}); code != 403 {
		t.Fatalf("an agent answered: %d", code)
	}
	l.want(200, "agent-in:"+id, "GET", "/v1/internal/sessions/"+id+"/manual", nil, &ms)
	if ms.State != "pending" {
		t.Fatalf("after the agents: %+v", ms)
	}
	if code := l.answer("web", api.CardAnswer{Card: card, Option: session.OptionAllow}); code != 200 {
		t.Fatalf("the person: %d", code)
	}
	l.want(200, "agent-in:"+id, "GET", "/v1/internal/sessions/"+id+"/manual?wait=10", nil, &ms)
	if ms.State != "approved" {
		t.Fatalf("approved: %+v", ms)
	}
	l.want(200, "web", "GET", "/v1/internal/sessions/"+id, nil, &st)
	if st.Mode != "manual" || len(st.Asks) != 0 {
		t.Fatalf("approved: %s %+v", st.Mode, st.Asks)
	}
	// The turn's end: auto again.
	l.send("web", id, api.Send{Text: "go on"})
	for end := time.Now().Add(10 * time.Second); st.Mode != "auto"; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatalf("not back: %s %s", st.Status, st.Mode)
		}
		l.want(200, "web", "GET", "/v1/internal/sessions/"+id, nil, &st)
	}
}
