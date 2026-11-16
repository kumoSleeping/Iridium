package server

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/channel"
	"github.com/kumoSleeping/iridium/internal/ctx/assemble"
	"github.com/kumoSleeping/iridium/internal/store"
)

// A card (docs/channels.md "资料"): an agent changes its own and another's, with an emoji and a
// colour for images; the channel says so in one line; the members' list has it.
func TestProfileCards(t *testing.T) {
	t.Parallel()
	l, ch, second := two(t)
	var p api.Profile
	l.want(200, "agent-in:"+second, "PATCH", "/v1/internal/self", map[string]any{"nick": "tester", "intro": "runs the tests only", "avatar": "🧪", "background": "#2f4f6f"}, &p)
	if p.Name != "tester" || p.Intro != "runs the tests only" || !strings.Contains(p.Avatar, "/avatar?v=g") || !strings.Contains(p.Background, "/background?v=g") {
		t.Fatalf("its card: %+v", p)
	}
	for _, path := range []string{"/avatar", "/background"} {
		img, _, ok := l.s.face(store.ChannelProfileKey(second), path == "/background", "")
		if !ok || !strings.HasPrefix(string(img), "<svg") {
			t.Errorf("%s: %q", path, img)
		}
		l.want(200, "web", "GET", "/v1/internal/channels/"+second+path, nil, nil)
	}
	// Again at once: still one line in the channel, of all that changed.
	end := l.s.ch.End(ch)
	l.want(200, "agent-in:"+second, "PATCH", "/v1/internal/self", map[string]any{"intro": "runs the tests, changes no code"}, nil)
	last, _ := l.s.ch.Get(ch, l.s.ch.End(ch))
	if last.Kind != channel.KindNote || last.D.Event == nil || last.D.Event.Type != "profile" || len(last.D.Event.What) != 3 {
		t.Errorf("the channel's line: %+v %+v", last.ChannelMessage, last.D.Event)
	}
	if before, ok := l.s.ch.Get(ch, end); ok && !before.Gone && before.Seq != last.Seq && before.D.Event != nil && before.D.Event.Type == "profile" {
		t.Errorf("two lines for one card: #%d and #%d", before.Seq, last.Seq)
	}
	var ms api.ChannelMembers
	l.want(200, "web", "GET", "/v1/internal/channels/"+ch+"/members", nil, &ms)
	found := false
	for _, m := range ms.Members {
		if m.Session == second {
			found = m.Nick == "tester" && m.Intro == "runs the tests, changes no code" && m.Background != ""
		}
	}
	if !found {
		t.Errorf("the members: %+v", ms.Members)
	}
	// Another agent's, by its nick; the channel's own, as "here".
	l.want(200, "agent-in:"+second, "PATCH", "/v1/internal/members/claude", map[string]any{"intro": "leads"}, &p)
	if p.Session != ch || p.Intro != "leads" {
		t.Errorf("another's card: %+v", p)
	}
	l.want(400, "agent-in:"+second, "PATCH", "/v1/internal/self", map[string]any{"intro": strings.Repeat("长", api.IntroMax+1)}, nil)
	l.want(400, "agent-in:"+second, "PATCH", "/v1/internal/self", map[string]any{"avatar": "not an image"}, nil)
	var mc api.MemberCard
	l.want(200, "agent-in:"+second, "GET", "/v1/internal/members/claude", nil, &mc)
	if mc.Intro != "leads" || mc.Kind != api.CardKindAgent {
		t.Errorf("looked up: %+v", mc)
	}
	// A person's own: an intro; not another's to change.
	l.want(200, "web", "PATCH", "/v1/internal/self", map[string]any{"intro": "likes short answers"}, &p)
	if p.Intro != "likes short answers" || p.Kind != api.CardKindPerson {
		t.Errorf("a person's card: %+v", p)
	}
	l.want(403, "agent-in:"+second, "PATCH", "/v1/internal/members/"+p.ID, map[string]any{"intro": "x"}, nil)
	// Its system prompt says who it is.
	self := l.s.selfText(second)
	if self.Nick != "tester" || self.Intro != "runs the tests, changes no code" {
		t.Errorf("told of itself: %+v", self)
	}
	in, err := assemble.System(assemble.Where{Self: self})
	if err != nil || !strings.Contains(in.String(), "You go by tester here.") || !strings.Contains(in.String(), "ir api internal/remember") {
		t.Errorf("the system prompt: %v\n%s", err, in.String())
	}
}

// What changed of the cards since an agent was last told goes with its next message, not the
// standing words again (docs/channels.md "资料变了怎么告诉 agent").
func TestProfileChangesTold(t *testing.T) {
	t.Parallel()
	l, ch, second := two(t)
	l.wantSat(200, "web", "message.create", map[string]any{"channel_id": ch, "content": "@claude2 one"}, nil)
	given(t, l, second, 1)
	l.want(200, "web", "PATCH", "/v1/internal/members/claude", map[string]any{"intro": "leads the work"}, nil)
	l.want(200, "web", "PATCH", "/v1/internal/members/claude2", map[string]any{"nick": "tester"}, nil)
	// Its own change it is not told of.
	l.want(200, "agent-in:"+second, "PATCH", "/v1/internal/self", map[string]any{"intro": "tests"}, nil)
	l.wantSat(200, "web", "message.create", map[string]any{"channel_id": ch, "content": "@tester two"}, nil)
	rows := given(t, l, second, 2)
	var rec assemble.Record
	if err := json.Unmarshal([]byte(rows[0].Record), &rec); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, part := range rec.Parts {
		if strings.HasPrefix(part.Source, "prompts:channel.context.profile") {
			got = append(got, strings.TrimPrefix(part.Source, "prompts:channel.context.")+":"+part.Values["who"]+">"+part.Values["to"])
		}
	}
	slices.Sort(got)
	if strings.Join(got, " ") != "profile-intro:claude> profile-name:claude2>tester profiles:>" {
		t.Errorf("told of: %q", got)
	}
	if rec.Full {
		t.Errorf("the standing words given again for a card's change")
	}
}

// Memory and recall (docs/channels.md "记忆"): an agent keeps a note, finds it and the
// channel's words again; a role's memory is its sessions'.
func TestMemoryAndRoles(t *testing.T) {
	t.Parallel()
	l, ch, second := two(t)
	var kept struct{ ID int64 }
	l.want(200, "agent-in:"+ch, "POST", "/v1/internal/remember", api.Remember{Text: "kumo wants short answers (#1)"}, &kept)
	l.want(400, "agent-in:"+ch, "POST", "/v1/internal/remember", api.Remember{Text: strings.Repeat("x", api.MemoryMax+1)}, nil)
	l.want(400, "web", "POST", "/v1/internal/remember", api.Remember{Text: "not an agent"}, nil)
	var mem []api.Memory
	l.want(200, "agent-in:"+ch, "GET", "/v1/internal/memory", nil, &mem)
	if len(mem) != 1 || mem[0].ID != kept.ID || mem[0].Session != ch || mem[0].Channel != ch || mem[0].Seq == 0 {
		t.Fatalf("its memory: %+v", mem)
	}
	var hits []api.RecallHit
	l.want(200, "agent-in:"+ch, "POST", "/v1/internal/recall", api.Recall{Q: "short"}, &hits)
	if len(hits) == 0 || hits[0].Kind != "memory" {
		t.Errorf("recalled: %+v", hits)
	}
	l.want(200, "agent-in:"+ch, "POST", "/v1/internal/recall", api.Recall{Q: "first"}, &hits)
	if len(hits) == 0 || hits[0].Kind != "message" || hits[0].Channel != ch || !strings.Contains(hits[0].Get, "message.get") {
		t.Errorf("recalled of the channel: %+v", hits)
	}
	// The other agent's memory is its own.
	l.want(200, "agent-in:"+second, "GET", "/v1/internal/memory", nil, &mem)
	if len(mem) != 0 {
		t.Errorf("another's memory: %+v", mem)
	}
	// Kept as a role: its card and its memory go with it; a session started as it has both.
	l.want(200, "web", "PATCH", "/v1/internal/members/claude", map[string]any{"intro": "a fat fish", "avatar": "🐟"}, nil)
	l.want(403, "agent-in:"+ch, "POST", "/v1/internal/roles", api.NewRole{Session: ch}, nil)
	var role api.Role
	l.want(200, "web", "POST", "/v1/internal/roles", api.NewRole{Session: ch}, &role)
	if role.Name != "claude" || role.Agent != api.AgentClaude || role.Intro != "a fat fish" || role.Avatar == "" || role.Memories != 1 {
		t.Fatalf("the role: %+v", role)
	}
	var roles []api.Role
	l.want(200, "web", "GET", "/v1/internal/roles", nil, &roles)
	if len(roles) != 1 {
		t.Fatalf("the roles: %+v", roles)
	}
	third := l.create(200, "web", api.NewSession{Role: "claude", Cwd: l.work, Text: "hello"})
	if got := l.s.ch.Nick(third); got != "claude3" {
		t.Errorf("its nick: %q", got)
	}
	var p api.Self
	l.want(200, "agent-in:"+third, "GET", "/v1/internal/self", nil, &p)
	if p.Intro != "a fat fish" || p.Role != role.ID || len(p.Memory) != 1 || !strings.Contains(p.Avatar, "v=g") {
		t.Errorf("a session of the role: %+v", p)
	}
	// Changed in the session, the role's stays; a person forgets a memory.
	l.want(200, "web", "PATCH", "/v1/internal/members/claude3", map[string]any{"intro": "another fish"}, nil)
	l.want(200, "web", "GET", "/v1/internal/roles", nil, &roles)
	if roles[0].Intro != "a fat fish" {
		t.Errorf("the role after its session changed: %+v", roles[0])
	}
	l.want(200, "web", "DELETE", "/v1/internal/memory/"+itoa(kept.ID)+"?session="+third, nil, nil)
	l.want(200, "agent-in:"+ch, "GET", "/v1/internal/memory", nil, &mem)
	if len(mem) != 0 {
		t.Errorf("forgotten: %+v", mem)
	}
	l.want(200, "web", "DELETE", "/v1/internal/roles/"+role.ID, nil, nil)
	if l.s.st.RoleOf(third) != "" {
		t.Errorf("a session of a role gone")
	}
}


// given waits until session id's agent was given n things by channels and is idle again; the
// records of them, the latest first.
func given(t *testing.T, l *live, id string, n int) []store.Injection {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		rows, _ := l.s.st.Injections(id, 0)
		var st api.SessionState
		l.do("web", "GET", "/v1/internal/sessions/"+id, nil, &st)
		if len(rows) >= n && st.Status == api.StatusIdle && st.Last != nil && strings.HasPrefix(st.Last.Text, "ok: ") {
			return rows
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s was given %d things, not %d: %+v", id, len(rows), n, st.Session)
		}
	}
}

// A session's own channel has a card of its own (kumo, 2026-11-16): its face, intro and
// background changed there are not its agent's; one it has not been given is its agent's.
func TestChannelCardApart(t *testing.T) {
	t.Parallel()
	l, ch, _ := two(t)
	l.want(200, "web", "PATCH", "/v1/internal/members/claude", map[string]any{"avatar": "🤖", "intro": "the agent"}, nil)
	var p api.Profile
	l.want(200, "web", "GET", "/v1/internal/channels/"+ch+"/profile", nil, &p)
	agentFace, _, _ := l.s.face(store.ChannelProfileKey(ch), false, "")
	if !strings.Contains(p.Avatar, "of=channel") || p.Intro != "" || p.Session != "" {
		t.Fatalf("the channel's card before it has its own: %+v", p)
	}
	if img, _, ok := l.s.channelFace(ch, nil, false); !ok || string(img) != string(agentFace) {
		t.Errorf("its face before it has its own: %q, want the agent's", img)
	}
	l.want(200, "web", "PATCH", "/v1/internal/channels/"+ch, map[string]any{"avatar": "🏠", "intro": "the room"}, &p)
	if p.Intro != "the room" {
		t.Errorf("the channel's card: %+v", p)
	}
	if img, _, ok := l.s.channelFace(ch, nil, false); !ok || !strings.Contains(string(img), "🏠") {
		t.Errorf("the channel's face: %q", img)
	}
	if img, _, _ := l.s.face(store.ChannelProfileKey(ch), false, ""); string(img) != string(agentFace) {
		t.Errorf("the agent's face changed with the channel's: %q", img)
	}
	var mc api.MemberCard
	l.want(200, "web", "GET", "/v1/internal/members/claude", nil, &mc)
	if mc.Intro != "the agent" {
		t.Errorf("the agent's card: %+v", mc)
	}
	l.want(200, "web", "GET", "/v1/internal/channels/"+ch+"/avatar?of=channel", nil, nil)
}
