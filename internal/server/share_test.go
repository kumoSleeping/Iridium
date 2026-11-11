package server

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/gate"
	"github.com/kumoSleeping/iridium/internal/satori"
)

// The needs queue carries every session's open asks, for whoever may use the session (its agent,
// its scope), answered at the session's card; a share goes to one login, checked that it may use
// what is shared: a login of ir_web finds it in its needs, another app's gets an ir.share event.
func TestNeedsAsksAndShare(t *testing.T) {
	l := start(t)
	now := time.Now()
	padID := l.addPanel("pad")
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
	pad := caller{Identity: gate.Identity{App: gate.WebApp, Login: padID}}

	var cr api.NewSessionReply
	l.want(200, "socket", "POST", "/v1/internal/sessions", api.NewSession{Agent: api.AgentClaude, Cwd: l.work, Text: "please ASK"}, &cr)
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
	if ask.Session != cr.ID || ask.Card == nil || ask.Card.ID != ask.ID || ask.Card.Kind != "permission" {
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
	l.want(200, "socket", "POST", "/v1/internal/sessions/"+cr.ID+"/answer", api.CardAnswer{Card: ask.ID, Option: "allow"}, nil)
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

	// Shares: checked against the login it goes to.
	var e api.Error
	if l.do("socket", "POST", "/v1/internal/share", api.ShareRequest{What: api.ShareOfSession, Session: cr.ID, To: "bot/u1"}, &e); e.Code != "not_usable" {
		t.Errorf("a Claude session to a codex-only login: %+v", e)
	}
	if l.do("socket", "POST", "/v1/internal/share", api.ShareRequest{What: api.ShareOfSession, Session: cr.ID, To: "bot/nobody"}, &e); e.Code != "no_such_login" {
		t.Errorf("no such login: %+v", e)
	}
	l.want(400, "socket", "POST", "/v1/internal/share", api.ShareRequest{What: "song", Session: cr.ID, To: "bot/u2"}, nil)
	var tg api.ShareTargets
	l.want(200, "socket", "GET", "/v1/internal/share/targets?what=session&session="+cr.ID, nil, &tg)
	for _, a := range tg.Apps {
		for _, x := range a.Logins {
			if x.ID == "u1" {
				t.Errorf("targets list u1: %+v", tg)
			}
		}
	}

	if l.do("socket", "POST", "/v1/internal/share", api.ShareRequest{What: api.ShareOfSession, Session: cr.ID, To: "ir_web/pad"}, &e); e.Code != "not_usable" {
		t.Errorf("a Claude session to a login without Claude: %+v", e)
	}
	var cx api.NewSessionReply
	l.want(200, "socket", "POST", "/v1/internal/sessions", api.NewSession{Agent: api.AgentCodex, Cwd: l.work, Text: "hi"}, &cx)
	var r api.ShareReply
	l.want(200, "socket", "POST", "/v1/internal/share", api.ShareRequest{What: api.ShareOfSession, Session: cx.ID, To: "ir_web/pad", Note: "看看"}, &r)
	if r.Login != padID {
		t.Errorf("reply: %+v", r)
	}
	mine := l.s.needsFor(pad, l.s.asks.everything().Pending)
	if len(mine) != 1 || mine[0].Kind != api.NeedShare || mine[0].Share.Note != "看看" || mine[0].Share.Session != cx.ID || mine[0].Share.From != gate.HostID {
		t.Fatalf("pad's share: %+v", mine)
	}
	if len(l.s.needsFor(caller{Identity: gate.HostIdentity}, l.s.asks.everything().Pending)) != 0 {
		t.Error("the host sees pad's share")
	}
	if !l.s.more.dropShare(mine[0].ID, padID) || len(l.s.needsFor(pad, l.s.asks.everything().Pending)) != 0 {
		t.Error("put away")
	}

	// Another app's login: an ir.share event, to it alone.
	w := l.satoriDial(nil)
	w.send(satori.OpIdentify, satori.Identify{Token: bot.Token})
	var ready satori.Ready
	w.read(&ready)
	l.want(200, "socket", "POST", "/v1/internal/share", api.ShareRequest{What: api.ShareOfPlace, Place: "主机:" + l.work, To: "bot/u1", Note: "这里"}, nil)
	ev := w.event(func(ev satori.Event) bool { return ev.Type == "internal" })
	if ev.XType != "ir.share" || ev.Login == nil || ev.Login.User.ID != "u1" || ev.XData.Share == nil || ev.XData.Share.Path != l.work || ev.XData.Share.Note != "这里" {
		t.Fatalf("the event: %+v", ev)
	}
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
