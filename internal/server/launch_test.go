package server

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/satori"
	"github.com/kumoSleeping/iridium/internal/session"
)

// memberIDs are channel ch's agents' sessions.
func (l *live) memberIDs(ch string) []string {
	l.t.Helper()
	var ms api.ChannelMembers
	l.want(200, "web", "GET", "/v1/internal/channels/"+ch+"/members", nil, &ms)
	var out []string
	for _, m := range ms.Members {
		if m.Session != "" {
			out = append(out, m.Session)
		}
	}
	return out
}

// A session an agent starts from inside its own (ir new) is in that agent's channel (docs/
// channels.md "拉起"): its own, or the one whose call it answers; recv at, marked as started
// for it by it, its launcher kept.
func TestLaunchJoinsChannel(t *testing.T) {
	t.Parallel()
	l := start(t)
	parent := l.create(200, "web", api.NewSession{Agent: api.AgentClaude, Cwd: l.work, Text: "first"})
	l.waitReply("web", parent, "ok: first")

	child := l.create(200, "agent-in:"+parent, api.NewSession{Agent: api.AgentClaude, Cwd: l.work})
	if ids := l.memberIDs(parent); !slices.Equal(ids, []string{parent, child}) {
		t.Fatalf("the parent's channel: %v, want %s and %s", ids, parent, child)
	}
	var ms api.ChannelMembers
	l.want(200, "web", "GET", "/v1/internal/channels/"+parent+"/members", nil, &ms)
	if i := slices.IndexFunc(ms.Members, func(m api.ChannelMember) bool { return m.Session == child }); i < 0 || len(ms.Members[i].On) != 0 || ms.Members[i].Nick == "" {
		t.Errorf("the launched one as a member: %+v", ms.Members)
	}
	l.waitSaid("web", parent, "加入了频道")
	for _, ss := range l.sessions("web") {
		if ss.ID == child && (ss.Spawned == nil || ss.Spawned.By != parent || ss.Spawned.Channel != parent || ss.Launcher != parent || !slices.Contains(ss.In, parent)) {
			t.Errorf("the launched one as listed: %+v %+v", ss, ss.Spawned)
		}
	}

	// A person's ir, or the page: a session of its own, in no channel but its own.
	alone := l.create(200, "web", api.NewSession{Agent: api.AgentClaude, Cwd: l.work})
	if l.s.ch.In(alone) != nil {
		t.Errorf("one the page started is in %v", l.s.ch.In(alone))
	}

	// Called by a channel with no agent of its own: what it starts is in that channel.
	var made satori.Channel
	l.wantSat(200, "web", "channel.create", map[string]any{"data": map[string]any{"name": "room"}, "_data": map[string]any{"cwd": l.work}}, &made)
	room := made.ID
	l.want(200, "web", "POST", "/v1/internal/channels/"+room+"/members", api.NewMember{Session: parent}, nil)
	l.s.calls.add(parent, called{Channel: room, Seq: 1})
	grand := l.create(200, "agent-in:"+parent, api.NewSession{Agent: api.AgentClaude, Cwd: l.work})
	if !l.s.ch.Speaks(room, grand) || l.s.ch.Speaks(parent, grand) {
		t.Errorf("started while called by the room: in %v", l.s.ch.In(grand))
	}
	if sp := l.s.spawnedOf(grand); sp == nil || sp.By != parent || sp.Channel != room {
		t.Errorf("where it was started: %+v", sp)
	}
}

// One an agent starts in its channel with words and a name (channel.create data.name): called
// by the name, the words said to it in the channel, its answer said there (kumo, 2026-11-16:
// not claude2, not kept in its own session).
func TestLaunchSpeaksInChannel(t *testing.T) {
	t.Parallel()
	l := start(t)
	parent := l.create(200, "web", api.NewSession{Agent: api.AgentClaude, Cwd: l.work, Text: "first"})
	l.waitReply("web", parent, "ok: first")
	// As Satori's channel.create gives it (data.name as Name).
	c := caller{Identity: localIdentity, socket: true, agent: true, session: parent}
	id, err := l.s.createAs(context.Background(), c, api.NewSession{Agent: api.AgentClaude, Cwd: l.work, Text: "say hi", Name: "子会话 helper"})
	if err != nil {
		t.Fatal(err)
	}
	if nick := l.s.ch.Nick(id); nick != "子会话helper" {
		t.Errorf("its nick: %q", nick)
	}
	l.waitSaid("web", parent, "@子会话helper", "ok: #", "say hi")
}

// An agent's ask is told in every channel it is in: an app there gets it and may answer it
// though it has no right to the agent itself, and it closes everywhere; one not among the
// channel's people may not (docs/channels.md "提问").
func TestAskInEveryChannel(t *testing.T) {
	t.Parallel()
	l := start(t)
	var made satori.Channel
	l.wantSat(200, "web", "channel.create", map[string]any{"data": map[string]any{"name": "room"}, "_data": map[string]any{"cwd": l.work}}, &made)
	room := made.ID
	var ms api.ChannelMembers
	l.want(200, "web", "POST", "/v1/internal/channels/"+room+"/members", api.NewMember{Agent: api.AgentClaude}, &ms)
	agent := ms.Members[len(ms.Members)-1].Session
	l.want(200, "web", "PUT", "/v1/internal/channels/"+room+"/people", api.People{People: []string{"*"}}, nil)

	// An app with no right to Claude: it cannot use the agent's session.
	bot := l.newApp("bot")
	var me satori.Meta
	l.sat(bot.Token, "meta", nil, &me) // its login
	w := l.satoriDial(nil)
	w.send(satori.OpIdentify, satori.Identify{Token: bot.Token})
	var ready satori.Ready
	if op, err := w.read(&ready); op != satori.OpReady || err != nil {
		t.Fatalf("READY: %d %v", op, err)
	}

	if code, _ := l.send("web", agent, api.Send{Text: "please ASK"}); code != 200 {
		t.Fatalf("send: %d", code)
	}
	ask := w.event(func(ev satori.Event) bool {
		return ev.Type == "message-created" && ev.XData != nil && ev.XData.Kind == "ask" && ev.Channel != nil && ev.Channel.ID == room
	})
	card := ask.Message.ID
	if !strings.HasPrefix(card, "ask:"+agent+":") {
		t.Fatalf("the ask in the room: %+v", ask.Message)
	}
	// Noted in both channels, under the same card.
	if _, ok := l.s.ch.ByRef(room, card); !ok {
		t.Errorf("the ask is not noted in the room")
	}
	if _, ok := l.s.ch.ByRef(agent, card); !ok {
		t.Errorf("the ask is not noted in the agent's own channel")
	}
	var m satori.Message
	l.wantSat(200, bot.Token, "message.get", map[string]any{"channel_id": room, "message_id": card}, &m)
	if !strings.Contains(m.Content, "#allow") {
		t.Errorf("message.get of the ask in the room: %+v", m)
	}

	// Not among the room's people: refused.
	l.want(200, "web", "PUT", "/v1/internal/channels/"+room+"/people", api.People{People: []string{"ir_web/*"}}, nil)
	if code := l.sat(bot.Token, "internal/button", map[string]any{"id": card + "#allow"}, nil); code == 200 {
		t.Fatalf("answered by one not in the room")
	}
	l.want(200, "web", "PUT", "/v1/internal/channels/"+room+"/people", api.People{People: []string{"*"}}, nil)
	l.wantSat(200, bot.Token, "internal/button", map[string]any{"id": card + "#allow"}, nil)
	w.event(func(ev satori.Event) bool {
		return ev.Type == "message-updated" && ev.Message != nil && ev.Message.ID == card && ev.Channel != nil && ev.Channel.ID == room
	})
	l.waitReply("web", agent, "ok: please ASK")
	var st api.SessionState
	if l.want(200, "web", "GET", "/v1/internal/sessions/"+agent, nil, &st); len(st.Asks) != 0 {
		t.Errorf("still asking: %+v", st.Asks)
	}

	// Its errors are told where its asks are: in the room too.
	l.s.sat.Notice(session.Notice{Session: agent, Kind: session.NoticeError, Text: "API Error: 402 Insufficient Balance"})
	w.event(func(ev satori.Event) bool {
		return ev.Type == "message-created" && ev.XData != nil && ev.XData.Kind == "error" && ev.Channel != nil && ev.Channel.ID == room
	})
}

// A turn ended in an error is said short: the usual ones by name, else its first line.
func TestErrorWords(t *testing.T) {
	for in, want := range map[string]string{
		"API Error: 402 Insufficient Balance (request_id: x)": "余额不足",
		"401 Unauthorized":                  "认证失败",
		"Error: rate limit exceeded, retry": "请求太频繁",
		"the process crashed\nstack trace":  "the process crashed",
		"":                                  "",
	} {
		if got := errorWords(in); got != want {
			t.Errorf("errorWords(%q) = %q, want %q", in, got, want)
		}
	}
}

// A channel takes MaxAgents invited; those its agents start are not counted among them.
func TestLaunchedNotCounted(t *testing.T) {
	t.Parallel()
	l := start(t)
	ch := l.create(200, "web", api.NewSession{Agent: api.AgentClaude, Cwd: l.work, Text: "first"})
	l.waitReply("web", ch, "ok: first")
	for range MaxAgents - 1 {
		l.want(200, "web", "POST", "/v1/internal/channels/"+ch+"/members", api.NewMember{Agent: api.AgentClaude}, nil)
	}
	l.want(409, "web", "POST", "/v1/internal/channels/"+ch+"/members", api.NewMember{Agent: api.AgentClaude}, nil)
	child := l.create(200, "agent-in:"+ch, api.NewSession{Agent: api.AgentClaude, Cwd: l.work})
	l.want(200, "agent-in:"+ch, "POST", "/v1/internal/channels/"+ch+"/members", api.NewMember{Agent: api.AgentClaude}, nil)
	if ids := l.memberIDs(ch); len(ids) != MaxAgents+2 || !slices.Contains(ids, child) {
		t.Errorf("%d agents (%v), want %d", len(ids), ids, MaxAgents+2)
	}
	// Still no room for one more invited by a person.
	l.want(409, "web", "POST", "/v1/internal/channels/"+ch+"/members", api.NewMember{Agent: api.AgentClaude}, nil)
	// All told, MaxAll.
	full := l.s.full(ch, true)
	many := slices.Clone(l.memberIDs(ch))
	for i := len(many); i < MaxAll; i++ {
		many = append(many, ch) // the channel's own counts, always
	}
	if !full(many) || full(many[:MaxAll-1]) {
		t.Errorf("launched: full at %d of %d", len(many), MaxAll)
	}
}
