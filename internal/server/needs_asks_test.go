package server

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/gate"
)

// The needs queue carries every session's open asks, for whoever may use the session (its agent,
// its scope), answered at the session's card.
func TestNeedsAsks(t *testing.T) {
	t.Parallel()
	l := start(t)
	now := time.Now()
	bot := l.newApp("bot", true)
	for _, id := range []string{"u1", "u2"} {
		l.s.st.AddLogin(bot.App.ID, id, id, 20, now)
		if ok, err := l.s.st.ApproveLogin(bot.App.ID, id, now); !ok || err != nil {
			t.Fatal(ok, err)
		}
	}
	codexOnly, _ := json.Marshal(api.Perms{Agents: []string{"codex"}, Scope: []string{"*"}})
	l.s.st.SetLoginPermsOf(bot.App.ID, "u1", codexOnly)
	u1 := caller{Identity: gate.Identity{App: bot.App.ID, Login: "u1"}}
	u2 := caller{Identity: gate.Identity{App: bot.App.ID, Login: "u2"}}

	cr := l.create(200, "socket", api.NewSession{Agent: api.AgentClaude, Cwd: l.work, Text: "please ASK"})
	var ask api.Need
	for end := time.Now().Add(10 * time.Second); ask.ID == "" && time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		var ns api.Needs
		l.want(200, "socket", "GET", "/v1/internal/needs", nil, &ns)
		for _, n := range ns.Pending {
			if n.Kind == api.NeedAsk {
				ask = n
			}
		}
	}
	if ask.Session != cr || ask.Card == nil || ask.Card.ID != ask.ID || ask.Card.Kind != "permission" {
		t.Fatalf("the ask: %+v", ask)
	}
	all := l.s.asks.everything().Pending
	if len(l.s.needsFor(u1, all)) != 0 || len(l.s.needsFor(u2, all)) != 1 {
		t.Fatalf("an ask for who may use its session only: u1 %+v, u2 %+v", l.s.needsFor(u1, all), l.s.needsFor(u2, all))
	}
	// The stream's "needs" as its caller sees it.
	if f := l.s.streamFilter(u1); string(f("needs", mustJSON(l.s.asks.everything()))) != `{"pending":[]}` {
		t.Fatal("u1's stream has the ask")
	}
	if code := l.answer("socket", api.CardAnswer{Card: ask.ID, Option: "allow"}); code != 200 {
		t.Fatalf("answer: %d", code)
	}
	for end := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		var ns api.Needs
		l.want(200, "socket", "GET", "/v1/internal/needs", nil, &ns)
		if len(ns.Pending) == 0 {
			break
		}
		if time.Now().After(end) {
			t.Fatalf("answered, still there: %+v", ns.Pending)
		}
	}

}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
