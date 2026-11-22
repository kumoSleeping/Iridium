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
	// A persona: its own it is told of whole, another's never (otae, 2026-11-21).
	l.want(200, "web", "PATCH", "/v1/internal/members/claude", map[string]any{"persona": "another's ways"}, nil)
	l.want(200, "web", "PATCH", "/v1/internal/members/tester", map[string]any{"persona": "short answers\nno guesses"}, nil)
	// Its own change it is not told of.
	l.want(200, "agent-in:"+second, "PATCH", "/v1/internal/self", map[string]any{"intro": "tests"}, nil)
	l.wantSat(200, "web", "message.create", map[string]any{"channel_id": ch, "content": "@tester two"}, nil)
	rows := given(t, l, second, 2)
	var rec assemble.Record
	if err := json.Unmarshal([]byte(rows[0].Record), &rec); err != nil {
		t.Fatal(err)
	}
	var got []string
	personas := 0
	for _, part := range rec.Parts {
		for _, r := range part.Refs {
			if r.Kind == "profile" && r.ID == "persona" {
				personas++
			}
		}
		if strings.HasPrefix(part.Source, "prompts:channel.context.profile") {
			got = append(got, strings.TrimPrefix(part.Source, "prompts:channel.context.")+":"+part.Values["who"]+">"+part.Values["to"])
		}
	}
	slices.Sort(got)
	if strings.Join(got, " ") != "profile-intro:claude> profile-name:claude2>tester profile-persona:> profiles:>" || personas != 1 {
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
	// Its name taken: hex digits of its session's id after it, not a number (kumo, 2026-11-17),
	// and no name of Iridium's it is asked to change.
	if got, want := l.s.ch.Nick(third), channel.NickWithID("claude", third, func(string) bool { return false }); got != want || got == "claude" || channel.IsDefaultNick(got) {
		t.Errorf("its nick: %q, want %q", got, want)
	}
	var p api.Self
	l.want(200, "agent-in:"+third, "GET", "/v1/internal/self", nil, &p)
	if p.Intro != "a fat fish" || p.Role != role.ID || len(p.Memory) != 1 || !strings.Contains(p.Avatar, "v=g") {
		t.Errorf("a session of the role: %+v", p)
	}
	// Changed in the session, the role's stays; a person forgets a memory.
	l.want(200, "web", "PATCH", "/v1/internal/members/"+l.s.ch.Nick(third), map[string]any{"intro": "another fish"}, nil)
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

// A role keeps no effort and no mode, and its model only as a default; one is made from its
// name and agent alone; 更新角色 takes the session's card, not what it runs; a persona goes into
// the system prompt after the intro (otae, 2026-11-21).
func TestRolePersona(t *testing.T) {
	t.Parallel()
	l, ch, _ := two(t)
	var role api.Role
	l.want(400, "web", "POST", "/v1/internal/roles", api.NewRole{Role: api.Role{Name: "reviewer"}}, nil)
	l.want(400, "web", "POST", "/v1/internal/roles", api.NewRole{Role: api.Role{Name: "reviewer", Agent: api.AgentClaude, Persona: strings.Repeat("长", api.PersonaMax+1)}}, nil)
	l.want(200, "web", "POST", "/v1/internal/roles", api.NewRole{Role: api.Role{Name: "reviewer", Agent: api.AgentClaude, Intro: "reviews", Persona: "  only sure findings\nverdict first  "}}, &role)
	if role.Persona != "only sure findings\nverdict first" || role.Intro != "reviews" || role.Model != "" {
		t.Fatalf("a role made from nothing: %+v", role)
	}
	// Started as it: its card, the effort given for this once, else none of the role's.
	one := l.create(200, "web", api.NewSession{Role: "reviewer", Cwd: l.work, Effort: "high"})
	two := l.create(200, "web", api.NewSession{Role: "reviewer", Cwd: l.work})
	if st, _ := l.s.m.State(one); st.Effort != "high" {
		t.Errorf("the effort given: %q", st.Effort)
	}
	if st, _ := l.s.m.State(two); st.Effort != "" {
		t.Errorf("an effort of the role's: %q", st.Effort)
	}
	self := l.s.selfText(two)
	in, err := assemble.System(assemble.Where{Self: self})
	text := in.String()
	if err != nil || !strings.Contains(text, "reviews\nonly sure findings\nverdict first\n") {
		t.Errorf("the system prompt: %v\n%s", err, text)
	}
	// 更新角色: the card comes back, the model does not; the role's name stays.
	l.want(200, "web", "PATCH", "/v1/internal/members/"+l.s.ch.Nick(two), map[string]any{"persona": "be kind", "intro": "reviews gently"}, nil)
	l.want(200, "web", "PATCH", "/v1/internal/roles/"+role.ID, map[string]any{"model": "sonnet"}, nil)
	l.want(200, "web", "PATCH", "/v1/internal/roles/"+role.ID, api.RolePatch{Session: two}, &role)
	if role.Persona != "be kind" || role.Intro != "reviews gently" || role.Model != "sonnet" || role.Name != "reviewer" {
		t.Errorf("updated from its session: %+v", role)
	}
	// Kept from a session: no effort comes with it (the field is gone), its persona does.
	l.want(200, "web", "PATCH", "/v1/internal/members/claude", map[string]any{"persona": "leads"}, nil)
	l.want(200, "web", "POST", "/v1/internal/roles", api.NewRole{Session: ch}, &role)
	if role.Persona != "leads" {
		t.Errorf("kept from a session: %+v", role)
	}
}

// A role kept from a session drawn with its model's face, none of its own, keeps that face; a
// role with none of its own has its model's (kumo, 2026-11-17: 大肥鱼派's was gone).
func TestRoleKeepsModelFace(t *testing.T) {
	t.Parallel()
	l, ch, second := two(t)
	png := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="
	l.want(200, "web", "PATCH", "/v1/internal/members/claude", map[string]any{"avatar": png}, nil)
	// Its model is known once its agent has started.
	var model string
	for deadline := time.Now().Add(10 * time.Second); model == ""; time.Sleep(20 * time.Millisecond) {
		var st api.SessionState
		l.want(200, "web", "GET", "/v1/internal/sessions/"+second, nil, &st)
		if model = modelAvatarKey(&st); model == "" && time.Now().After(deadline) {
			t.Fatal("no model for the second session")
		}
	}
	if _, _, ok := l.s.face(store.ChannelProfileKey(second), false, ""); ok {
		t.Fatal("the second session has a face of its own")
	}
	want, _, ok, _ := l.s.st.Avatar(model)
	if !ok {
		t.Fatal("no face of the model")
	}
	var role api.Role
	l.want(200, "web", "POST", "/v1/internal/roles", api.NewRole{Session: second}, &role)
	if role.Avatar == "" {
		t.Fatalf("the role kept no face: %+v", role)
	}
	if img, _, ok, _ := l.s.st.Avatar(store.RoleProfileKey(role.ID)); !ok || string(img) != string(want) {
		t.Errorf("the role's own face is not its session's model's")
	}
	l.want(200, "web", "GET", "/v1/internal/roles/"+role.ID+"/avatar", nil, nil)
	// One made as given, no face: its model's.
	var given api.Role
	l.want(200, "web", "POST", "/v1/internal/roles", api.NewRole{Name: "fish", Agent: api.AgentClaude, Model: strings.TrimPrefix(model, "m:claude:")}, &given)
	if given.Avatar == "" {
		t.Errorf("a role with no face of its own, not its model's: %+v", given)
	}
	l.want(200, "web", "GET", "/v1/internal/roles/"+given.ID+"/avatar", nil, nil)
	_ = ch
	// One started now: its face is its model's once its agent has said the model, and the pages
	// are told to ask for the faces again then (faceFollows), no turn of it needed.
	_, v, _ := l.s.avatarOf(second, func() *api.SessionState { st, _ := l.s.m.State(second); return st }())
	if v == "" {
		t.Fatal("no face for the second session")
	}
	seq := l.s.avatarsSeq.Load()
	l.want(200, "web", "POST", "/v1/internal/channels/"+ch+"/members", map[string]any{"agent": "claude"}, nil)
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		followed := false
		l.s.faceMu.Lock()
		for id, seen := range l.s.faceSeen {
			followed = followed || (id != second && id != ch && seen == v)
		}
		l.s.faceMu.Unlock()
		if followed && l.s.avatarsSeq.Load() > seq {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("a session started was not followed to its model's face: %v", l.s.faceSeen)
		}
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
// background changed there are not its agent's; one it has not been given is its agent's, or
// with several agents in it none: the page draws theirs (the user, 2026-11-16).
func TestChannelCardApart(t *testing.T) {
	t.Parallel()
	l, ch, second := two(t)
	l.want(200, "web", "PATCH", "/v1/internal/members/claude", map[string]any{"avatar": "🤖", "intro": "the agent"}, nil)
	var p api.Profile
	l.want(200, "web", "GET", "/v1/internal/channels/"+ch+"/profile", nil, &p)
	if p.Avatar != "" {
		t.Errorf("the card of a channel of two agents, none of its own: %q", p.Avatar)
	}
	if _, _, ok := l.s.channelFace(ch, nil, false); ok {
		t.Error("a face of a channel of two agents, none of its own")
	}
	l.want(200, "web", "DELETE", "/v1/internal/channels/"+ch+"/members/agent:"+second, nil, nil)
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

// An agent changes a face with the face's own route too (kumo, 2026-11-17: it was refused there).
func TestAgentPutsFace(t *testing.T) {
	t.Parallel()
	l, ch, second := two(t)
	png := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="
	l.want(200, "agent-in:"+second, "PUT", "/v1/internal/channels/"+ch+"/avatar", map[string]any{"avatar": png}, nil)
	if _, _, ok := l.s.face(store.ChannelProfileKey(ch), false, ""); !ok {
		t.Fatal("no face after an agent's PUT")
	}
	l.want(200, "agent-in:"+second, "DELETE", "/v1/internal/channels/"+ch+"/avatar", nil, nil)
}
