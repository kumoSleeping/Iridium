package server

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	pngpkg "image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/channel"
	"github.com/kumoSleeping/iridium/internal/deliver"
	"github.com/kumoSleeping/iridium/internal/gate"
	"github.com/kumoSleeping/iridium/internal/satori"
	"github.com/kumoSleeping/iridium/internal/session"
)

// said are channel id's messages as as sees them: "<n> <who> <words>" each.
func (l *live) said(as, id string) []string {
	l.t.Helper()
	var page satori.BidiList[satori.Message]
	l.wantSat(200, as, "message.list", map[string]any{"channel_id": id, "limit": 200}, &page)
	var out []string
	for _, m := range page.Data {
		who := m.User.ID
		if m.User.Nick != "" {
			who = "@" + m.User.Nick
		}
		out = append(out, fmt.Sprintf("%s %s %s", m.ID, who, m.Content))
	}
	return out
}

// waitSaid waits until one of channel id's messages has every one of parts.
func (l *live) waitSaid(as, id string, parts ...string) []string {
	l.t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(30 * time.Millisecond) {
		all := l.said(as, id)
		for _, m := range all {
			ok := true
			for _, p := range parts {
				ok = ok && strings.Contains(m, p)
			}
			if ok {
				return all
			}
		}
		if time.Now().After(deadline) {
			l.t.Fatalf("no message with %q in %s:\n%s", parts, id, strings.Join(all, "\n"))
		}
	}
}

// A channel of two agents: the session's own keeps receiving what people say and speaks in it
// directly; the one added is a session of its own, given only what names it, with the words
// under their number and what it has not seen of the channel; what it says in its turn stays in
// its own channel, and it speaks in this one with ir. Agents naming each other pass the chain
// on, four times at most.
func TestChannelOfSeveral(t *testing.T) {
	t.Parallel()
	l := start(t)
	ch := l.create(200, "web", api.NewSession{Agent: api.AgentClaude, Cwd: l.work, Text: "first"})
	l.waitReply("web", ch, "ok: first")

	// One agent: the default members, the panel's logins and the host's (no Bot's).
	var ms api.ChannelMembers
	l.want(200, "web", "GET", "/v1/internal/channels/"+ch+"/members", nil, &ms)
	if ms.Multi || len(ms.Members) != 3 || ms.Members[0].Session != ch || fmt.Sprint(ms.Members[0].Recv) != "[*]" ||
		ms.Members[1].Member != "ir_web/*" || ms.Members[2].Member != "local/*" || !ms.Joined {
		t.Fatalf("default members: %+v", ms)
	}

	// A second agent joins: a session of its own, a nick of its own, only what names it.
	l.want(400, "web", "POST", "/v1/internal/channels/"+ch+"/members", api.NewMember{Agent: api.AgentClaude, Nick: "a b"}, nil)
	l.want(200, "web", "POST", "/v1/internal/channels/"+ch+"/members", api.NewMember{Agent: api.AgentClaude}, &ms)
	ms.Members = slices.DeleteFunc(ms.Members, func(m api.ChannelMember) bool { return m.Session == "" })
	if !ms.Multi || len(ms.Members) != 2 || ms.Members[0].Nick != "claude" || ms.Members[1].Nick != "claude2" || len(ms.Members[1].Recv) != 0 || ms.Members[1].Name == "" {
		t.Fatalf("members: %+v", ms)
	}
	second := ms.Members[1].Session
	// The channel says who joined, and what happened with it.
	var joined satori.BidiList[satori.Message]
	l.wantSat(200, "web", "message.list", map[string]any{"channel_id": ch, "limit": 1}, &joined)
	if raw, _ := json.Marshal(joined.Data[0].Data); joined.Data[0].Content != "@claude2 加入了频道" || !strings.Contains(string(raw), `"type":"join"`) || !strings.Contains(string(raw), `"nick":"claude2"`) {
		t.Errorf("the line of the joining: %+v %s", joined.Data[0], raw)
	}
	if second == "" || second == ch {
		t.Fatalf("the second agent's session: %+v", ms.Members[2])
	}
	// It has a channel of its own, with itself alone; it is listed as any session, marked with
	// the channel it is in and where it was started.
	var own api.ChannelMembers
	if l.want(200, "web", "GET", "/v1/internal/channels/"+second+"/members", nil, &own); own.Channel != second || own.Multi || len(own.Members) != 3 {
		t.Errorf("the second agent's own channel: %+v", own)
	}
	found := false
	for _, ss := range l.sessions("web") {
		switch ss.ID {
		case second:
			found = true
			if len(ss.In) != 1 || ss.In[0] != ch || ss.Spawned == nil || ss.Spawned.Channel != ch || ss.Launcher != "" {
				t.Errorf("the second agent as listed: %+v", ss)
			}
		case ch:
			if len(ss.Agents) != 2 || ss.Agents[0].Session != ch || ss.Agents[1].Nick != "claude2" || len(ss.In) != 0 {
				t.Errorf("the channel as listed: %+v", ss)
			}
		}
	}
	if !found {
		t.Errorf("the second agent is not listed")
	}
	// In it already: not twice, nor the channel's own agent.
	l.want(409, "web", "POST", "/v1/internal/channels/"+ch+"/members", api.NewMember{Session: second}, nil)
	l.want(409, "web", "POST", "/v1/internal/channels/"+ch+"/members", api.NewMember{Session: ch}, nil)

	// What a new agent of the kind may start with here: as the latest session of it said.
	var choices api.AgentChoices
	l.want(200, "web", "GET", "/v1/internal/agents/claude/choices?channel="+ch, nil, &choices)
	var first api.SessionState
	l.do("web", "GET", "/v1/internal/sessions/"+ch, nil, &first)
	if len(choices.Models) != len(first.Models) || choices.Models == nil || choices.Efforts == nil {
		t.Errorf("choices: %+v, the session's %+v", choices, first.Models)
	}
	l.want(200, "web", "GET", "/v1/internal/agents/pi/choices", nil, &choices)
	if len(choices.Models) != 0 {
		t.Errorf("choices of a kind that never ran: %+v", choices)
	}
	// What was kept is answered at once, with the presets kept beside, no session of it asked.
	_ = l.s.st.Keep(choicesKey(api.AgentDsh, ""), `{"models":[{"value":"flash","name":"Flash"}],"efforts":[]}`)
	_ = l.s.st.Keep("presets:dsh:", `{"presets":[{"id":"standard"},{"id":"ptc"}],"default":"standard"}`)
	l.want(200, "web", "GET", "/v1/internal/agents/dsh/choices", nil, &choices)
	if len(choices.Models) != 1 || choices.Models[0].Value != "flash" || choices.Presets == nil || len(choices.Presets.Presets) != 2 {
		t.Errorf("kept choices: %+v", choices)
	}
	// No model is called "default": the test agent's resolves to a model not listed, a row of
	// its own, marked, and what the session says it runs before the agent names it.
	for _, m := range first.Models {
		if m.Value == "default" {
			t.Errorf("a model called default: %+v", first.Models)
		}
	}
	if m, ok := api.DefaultModel(first.Models); !ok || m.Value != "fake-model" || first.Pick == "default" || first.Model == "default" || first.Model == "" {
		t.Errorf("the default as named: %+v, pick %q, model %q", m, first.Pick, first.Model)
	}
	// An agent that names no default: the model its kind last ran there, listed or not; the
	// level one given none ran at. Kept choices from before, still listing "default": merged.
	_ = l.s.st.Keep(session.RanKey(api.AgentDsh, ""), "pro")
	_ = l.s.st.Keep(session.RanEffortKey(api.AgentDsh, ""), "high")
	var ran api.AgentChoices
	l.want(200, "web", "GET", "/v1/internal/agents/dsh/choices", nil, &ran)
	if ran.Ran != "pro" || ran.Effort != "high" || len(ran.Models) != 2 || ran.Models[0].Value != "pro" {
		t.Errorf("the model last ran: %+v", ran)
	}
	_ = l.s.st.Keep(choicesKey(api.AgentDsh, ""), `{"models":[{"value":"default","name":"Default","resolved":"flash-1"},{"value":"flash","name":"Flash","resolved":"flash-1"}],"efforts":[]}`)
	var old api.AgentChoices
	l.want(200, "web", "GET", "/v1/internal/agents/dsh/choices", nil, &old)
	if old.Ran != "" || len(old.Models) != 1 || !old.Models[0].Default {
		t.Errorf("kept choices with a default entry: %+v", old)
	}

	// Nobody named: the channel's own agent alone answers; the second is given nothing.
	code, r := l.send("web", ch, api.Send{Text: "plain words"})
	if code != 200 || r.Seq == 0 {
		t.Fatalf("send: %d %+v", code, r)
	}
	l.waitSaid("web", ch, "@claude ", fmt.Sprintf("ok: #%d: plain words", r.Seq))
	if p := l.page("web", second); len(p.Blocks) != 0 {
		t.Errorf("the second agent was given: %+v", p.Blocks)
	}

	// Named: it gets the words under their number and what it has not seen, each message once,
	// and is told which channel this is and how it speaks there. What it says in its turn is
	// said in its own channel, not in this one.
	code, r = l.send("web", ch, api.Send{Text: "now @claude2 look"})
	if code != 200 {
		t.Fatalf("send naming: %d %+v", code, r)
	}
	l.waitSaid("web", second, "@claude2 ", fmt.Sprintf("ok: #%d: now @claude2 look", r.Seq))
	p := l.page("web", second)
	if len(p.Blocks) == 0 || p.Blocks[0].Kind != api.BlockKindUser {
		t.Fatalf("the second agent's record: %+v", p.Blocks)
	}
	got := p.Blocks[0].Text
	for _, want := range []string{fmt.Sprintf("#%d: now @claude2 look", r.Seq), "<channel-context>", "You are claude2, called in the channel", "(" + ch + ")", "said in the channel as you write it",
		"message.list channel_id=" + ch, "claude (", "]: first\n", "]: ok: first\n", "]: plain words\n", "</channel-context>"} {
		if !strings.Contains(got, want) {
			t.Errorf("given without %q:\n%s", want, got)
		}
	}
	if strings.Count(got, "plain words") != 2 { // the words, and the first agent's echo of them
		t.Errorf("given twice:\n%s", got)
	}
	// Its turn ended with nothing said in the channel with ir: its reply is said there for it.
	l.waitSaid("web", ch, " @claude2 ", fmt.Sprintf("ok: #%d: now @claude2 look", r.Seq))
	// What it was given is not a message of its own channel: only what it said is.
	for _, m := range l.said("web", second) {
		if !strings.Contains(m, " @claude2 ") {
			t.Errorf("in the second agent's own channel: %s", m)
		}
	}

	// What it was given is kept: where each part came from.
	// What it was given is kept: where each part came from, and its text as it got it.
	var ins []api.Injection
	l.want(200, "web", "GET", "/v1/internal/sessions/"+second+"/injections", nil, &ins)
	ins = turnsOf(ins)
	if n := len(ins); n == 0 || ins[0].Seq != r.Seq || !strings.Contains(string(ins[0].Record), `"prompts:channel.context.guest"`) || !strings.Contains(string(ins[0].Record), `"text":"`) {
		t.Errorf("injections: %+v", ins)
	}

	// How far each has read: the channel's own agent what it was given and said (not the
	// message that named the second, though it takes all that names nobody), the second what
	// it was given; a person as they say, never back, and not an agent by hand.
	var reads api.ChannelReads
	l.want(200, "web", "GET", "/v1/internal/channels/"+ch+"/reads", nil, &reads)
	if reads.End < r.Seq || len(reads.Readers) != 2 || reads.Readers[0].Member != "agent:"+ch || reads.Readers[0].Seq < 1 || reads.Readers[0].Seq >= r.Seq ||
		reads.Readers[1].Nick != "claude2" || reads.Readers[1].Seq < r.Seq || reads.Readers[1].Name == "" {
		t.Errorf("reads: %+v, the message named %d", reads, r.Seq)
	}
	l.want(400, "web", "POST", "/v1/internal/channels/"+ch+"/read", api.Read{}, nil)
	l.want(403, "agent-in:"+second, "POST", "/v1/internal/channels/"+ch+"/read", api.Read{Seq: 1}, nil)
	// The host has read it, and is no reader on the page: it is Iridium itself there (a person's
	// reads: TestChannelPeople).
	l.want(200, "web", "POST", "/v1/internal/channels/"+ch+"/read", api.Read{Seq: 3}, &reads)
	l.want(200, "web", "POST", "/v1/internal/channels/"+ch+"/read", api.Read{Seq: 1 << 40}, &reads)
	if len(reads.Readers) != 2 || slices.ContainsFunc(reads.Readers, func(r api.Reader) bool { return !strings.HasPrefix(r.Member, "agent:") }) {
		t.Errorf("the host among the readers: %+v", reads)
	}
	// The channel's own agent taking only what names it has read what it said, not what came
	// after: the message to the second was not given to it.
	at := api.Recv{}
	l.want(200, "web", "PATCH", "/v1/internal/channels/"+ch+"/members/agent:"+ch, api.MemberPatch{Recv: &at}, nil)
	l.want(200, "web", "GET", "/v1/internal/channels/"+ch+"/reads", nil, &reads)
	for _, rd := range reads.Readers {
		if rd.Member == "agent:"+ch && (rd.Seq < 1 || rd.Seq >= r.Seq) {
			t.Errorf("the own agent taking @ only read to %d, the message to the second %d", rd.Seq, r.Seq)
		}
	}
	every := api.Recv{"*"}
	l.want(200, "web", "PATCH", "/v1/internal/channels/"+ch+"/members/agent:"+ch, api.MemberPatch{Recv: &every}, nil)

	// It speaks in the channel with ir: said there under its nick, given to whom it names.
	var sent []satori.Message
	l.wantSat(200, "agent-in:"+second, "message.create", map[string]any{"channel_id": ch, "content": "seen, @claude go on"}, &sent)
	all := l.waitSaid("web", ch, "@claude ", "seen, @claude go on", "ok: #")
	if !slices.ContainsFunc(all, func(m string) bool { return strings.Contains(m, " @claude2 seen, @claude go on") }) {
		t.Errorf("what it sent is not said under its nick:\n%s", strings.Join(all, "\n"))
	}
	// What it sent says where it is from: the stretch of its own record since it was called.
	var page satori.BidiList[satori.Message]
	l.wantSat(200, "web", "message.list", map[string]any{"channel_id": ch, "limit": 200}, &page)
	pointed := false
	for _, m := range page.Data {
		if raw, _ := json.Marshal(m.Data); strings.Contains(m.Content, "seen, @claude go on") && m.User.Nick == "claude2" {
			var d satori.LogData
			_ = json.Unmarshal(raw, &d)
			pointed = d.Session == second && d.To > 0 && (d.From == nil || d.To > *d.From)
		}
	}
	if !pointed {
		t.Errorf("what it sent does not point at its record")
	}
	// The members as Satori has them: of the guild, the agents of its sessions and the app's
	// logins; of the channel alone (Iridium's own), its agents with when each joined.
	var gm satori.List[satori.GuildMember]
	l.wantSat(200, "web", "guild.member.list", map[string]any{"guild_id": "loose"}, &gm)
	inGuild := map[string]satori.GuildMember{}
	for _, m := range gm.Data {
		inGuild[m.User.ID] = m
	}
	if m := inGuild["agent:"+second]; m.Nick != "claude2" || m.JoinedAt == 0 || !m.User.IsBot || inGuild["agent:"+ch].Nick != "claude" {
		t.Errorf("the guild's members: %+v", gm.Data)
	}
	l.wantSat(200, "web", "guild.member.list", map[string]any{"_data": map[string]any{"channel_id": ch}}, &gm)
	if len(gm.Data) < 2 || gm.Data[0].User.ID != "agent:"+ch || gm.Data[1].User.ID != "agent:"+second || gm.Data[1].JoinedAt == 0 {
		t.Errorf("the channel's members: %+v", gm.Data)
	}
	var one satori.GuildMember
	if l.wantSat(200, "web", "guild.member.get", map[string]any{"guild_id": "loose", "user_id": "agent:" + second}, &one); one.Nick != "claude2" || one.User == nil {
		t.Errorf("a member: %+v", one)
	}
	l.wantSat(422, "web", "guild.member.get", map[string]any{"guild_id": "loose", "user_id": "agent:nobody"}, nil)
	l.wantSat(400, "web", "guild.member.list", map[string]any{}, nil)

	// Its record in outline: a turn a line, the channel's message by its words alone. Once its
	// turns have settled (what it said with ir woke the first, whose echo may call it back):
	// the outline's end is then the record's.
	// Settled: no turn after the outline's end, three times in a row (an echo may wake one more
	// after idle, its words said a moment after its turn: again).
	var outline api.Outline
	for k, quiet, last := 0, 0, int64(-1); quiet < 3 && k < 100; k++ {
		l.idle(ch)
		l.idle(second)
		l.wantSat(200, "agent-in:"+ch, "internal/block.outline", map[string]any{"channel_id": second}, &outline)
		var after api.Outline
		l.wantSat(200, "web", "internal/block.outline", map[string]any{"channel_id": second, "from": fmt.Sprint(outline.End)}, &after)
		if quiet = quiet + 1; len(after.Turns) > 0 || outline.End != last {
			quiet = 0
		}
		last = outline.End
		time.Sleep(150 * time.Millisecond)
	}
	if len(outline.Turns) < 1 || !strings.HasPrefix(outline.Turns[0].Asked, fmt.Sprintf("#%d: now @claude2 look", r.Seq)) || strings.Contains(outline.Turns[0].Asked, "channel-context") ||
		!strings.HasPrefix(outline.Turns[0].Said, "ok: #") || outline.Turns[0].Words != 1 || outline.Turns[0].At == "" || outline.End == 0 {
		t.Errorf("outline: %+v", outline)
	}
	l.wantSat(400, "web", "internal/block.outline", map[string]any{"channel_id": second, "from": "x"}, nil)
	if l.wantSat(200, "web", "internal/block.outline", map[string]any{"channel_id": second, "from": fmt.Sprint(outline.End)}, &outline); len(outline.Turns) != 0 {
		t.Errorf("outline after the end: %+v", outline)
	}

	// The fake agents echo what they get. The second keeps sending the first words that name
	// both: the first's echo calls the second back, and so on, until four agents in a row have
	// passed the chain on: then its ir is refused.
	stopped := false
	for i := range 8 {
		words := fmt.Sprintf("@claude tell @claude2 round%d", i)
		code := l.sat("agent-in:"+second, "message.create", map[string]any{"channel_id": ch, "content": words}, nil)
		if code == 409 {
			// Something it had not seen names it (the first's echo, on its way to it): read, again.
			code = l.sat("agent-in:"+second, "message.create", map[string]any{"channel_id": ch, "content": words}, nil)
		}
		if code == 400 {
			stopped = true
			break
		} else if code != 200 {
			t.Fatalf("round %d: %d", i, code)
		}
		l.waitSaid("web", second, fmt.Sprintf("round%d", i))
	}
	if !stopped {
		t.Errorf("the agents went on passing it")
	}
	// A person speaking begins anew.
	if code, _ := l.send("web", ch, api.Send{Text: "@claude2 again"}); code != 200 {
		t.Fatalf("a person after the chain stopped: %d", code)
	}
	l.waitSaid("web", second, "@claude2 again")
	if code := l.sat("agent-in:"+second, "message.create", map[string]any{"channel_id": ch, "content": "fine"}, nil); code != 200 && code != 409 {
		t.Errorf("the second agent after a person spoke: %d", code)
	}

	// An agent reads its own record and its channel.
	if code := l.sat("agent-in:"+ch, "internal/block.list", map[string]any{"channel_id": ch}, nil); code != 200 {
		t.Errorf("an agent reading its own record: %d", code)
	}
	if code := l.sat("agent-in:"+ch, "message.list", map[string]any{"channel_id": "self"}, nil); code != 200 {
		t.Errorf("an agent reading its channel: %d", code)
	}
	if code := l.sat("agent-in:"+second, "message.list", map[string]any{"channel_id": ch}, nil); code != 200 {
		t.Errorf("an agent reading a channel it is in: %d", code)
	}
	// An agent leaves: the channel one agent's again; its session stays as it is.
	ms = api.ChannelMembers{}
	l.want(200, "web", "DELETE", "/v1/internal/channels/"+ch+"/members/agent:"+second, nil, &ms)
	if ms.Multi || len(ms.Members) != 3 {
		t.Fatalf("after it left: %+v", ms)
	}
	var st api.SessionState
	if l.want(200, "web", "GET", "/v1/internal/sessions/"+second, nil, &st); st.Archived {
		t.Errorf("the session of the agent that left: %+v", st.Session)
	}
	l.want(422, "web", "DELETE", "/v1/internal/channels/"+ch+"/members/agent:"+ch, nil, nil)
	code, r = l.send("web", ch, api.Send{Text: "alone again"})
	if code != 200 {
		t.Fatalf("send alone: %d", code)
	}
	l.waitSaid("web", ch, "ok: alone again")

	// A session there is joins as it is, under the nick it had; it keeps its own channel.
	l.want(200, "web", "POST", "/v1/internal/channels/"+ch+"/members", api.NewMember{Session: second, Recv: api.Recv{"*"}}, &ms)
	if last := ms.Members[len(ms.Members)-1]; !ms.Multi || len(ms.Members) != 4 || last.Session != second || last.Nick != "claude2" || fmt.Sprint(last.Recv) != "[*]" {
		t.Fatalf("a session there is, joined: %+v", ms)
	}
	// Receiving everything: what a person says with no name wakes it too.
	code, r = l.send("web", ch, api.Send{Text: "to all of you"})
	if code != 200 {
		t.Fatalf("send to all: %d", code)
	}
	l.waitSaid("web", ch, "@claude ", fmt.Sprintf("ok: #%d: to all of you", r.Seq))
	l.waitSaid("web", second, fmt.Sprintf("ok: #%d: to all of you", r.Seq))
}

// A channel's people: everyone unless chosen. One who is not among them still reads it, and
// neither speaks in it nor changes it; anyone among them changes who is.
func TestChannelPeople(t *testing.T) {
	t.Parallel()
	l := start(t)
	ch := l.create(200, "web", api.NewSession{Agent: api.AgentClaude, Cwd: l.work, Text: "first"})
	l.waitReply("web", ch, "ok: first")
	bot := l.newApp("bot", true).Token
	// New: the panel's and the host's, no Bot's (the user, 2026-11-13); everyone, once chosen.
	if code, _ := l.send(bot, ch, api.Send{Text: "from the bot"}); code != 403 {
		t.Fatalf("a Bot speaks in a new channel: %d", code)
	}
	l.want(200, "web", "PUT", "/v1/internal/channels/"+ch+"/people", api.People{People: []string{"*"}}, nil)
	if code, _ := l.send(bot, ch, api.Send{Text: "from the bot"}); code != 200 {
		t.Fatalf("everyone speaks: %d", code)
	}
	l.waitSaid("web", ch, "ok: from the bot")
	// A person's place: where they said they read to, never back, never past the end.
	var reads api.ChannelReads
	person := func() api.Reader {
		for _, r := range reads.Readers {
			if !strings.HasPrefix(r.Member, "agent:") {
				return r
			}
		}
		return api.Reader{}
	}
	l.want(200, bot, "POST", "/v1/internal/channels/"+ch+"/read", api.Read{Seq: 2}, &reads)
	l.want(200, bot, "POST", "/v1/internal/channels/"+ch+"/read", api.Read{Seq: 1}, &reads)
	if p := person(); len(reads.Readers) != 2 || p.Seq != 2 || p.Member == "" || p.Name == "" || p.At == 0 {
		t.Errorf("reads with a person's: %+v", reads)
	}
	l.want(200, bot, "POST", "/v1/internal/channels/"+ch+"/read", api.Read{Seq: 1 << 40}, &reads)
	if person().Seq != reads.End {
		t.Errorf("read past the end: %+v", reads)
	}

	// The page chooses the people: itself. The bot reads, and that is all.
	var ms api.ChannelMembers
	l.want(200, "web", "GET", "/v1/internal/channels/"+ch+"/members", nil, &ms)
	me := ms.Me
	if me == "" || !ms.Joined {
		t.Fatalf("me: %+v", ms)
	}
	l.want(400, "web", "PUT", "/v1/internal/channels/"+ch+"/people", api.People{People: []string{"nonsense"}}, nil)
	l.want(200, "web", "PUT", "/v1/internal/channels/"+ch+"/people", api.People{People: []string{me}}, &ms)
	if !ms.Joined || len(ms.Members) != 2 || ms.Members[1].Member != me && ms.Members[0].Member != me {
		t.Fatalf("people chosen: %+v", ms)
	}
	if code, _ := l.send(bot, ch, api.Send{Text: "again"}); code != 403 {
		t.Errorf("one not among them speaks: %d", code)
	}
	if code := l.sat(bot, "message.list", map[string]any{"channel_id": ch}, nil); code != 200 {
		t.Errorf("one not among them reads: %d", code)
	}
	var theirs api.ChannelMembers
	if l.asLogin(bot, "main", "GET", "/v1/internal/channels/"+ch+"/members", nil, &theirs); theirs.Joined {
		t.Errorf("joined: %+v", theirs)
	}
	if code := l.asLogin(bot, "main", "PUT", "/v1/internal/channels/"+ch+"/people", api.People{}, nil); code != 403 {
		t.Errorf("one not among them changes them: %d", code)
	}
	if code := l.asLogin(bot, "main", "POST", "/v1/internal/sessions/"+ch+"/interrupt", nil, nil); code != 403 {
		t.Errorf("one not among them stops the agent: %d", code)
	}
	if code, _ := l.send("web", ch, api.Send{Text: "mine"}); code != 200 {
		t.Errorf("one of them speaks: %d", code)
	}
	l.waitSaid("web", ch, "ok: mine")
	// The host is always one. None chosen: everyone again.
	if code, _ := l.send("socket", ch, api.Send{Text: "host"}); code != 200 {
		t.Errorf("the host speaks: %d", code)
	}
	l.want(200, "web", "PUT", "/v1/internal/channels/"+ch+"/people", api.People{}, &ms)
	if code, _ := l.send(bot, ch, api.Send{Text: "back"}); code != 200 {
		t.Errorf("everyone again: %d", code)
	}
}

// A session made with its channel's members: the agents there from the start, each a session
// of its own, its first words said in the channel, reaching whom they name.
func TestChannelCreatedWhole(t *testing.T) {
	t.Parallel()
	l := start(t)
	ch := l.create(200, "web", api.NewSession{Agent: api.AgentClaude, Cwd: l.work, Text: "@helper begin",
		Members: &api.ChannelSetup{Nick: "lead", Agents: []api.NewMember{{Agent: api.AgentClaude, Nick: "helper"}, {Agent: api.AgentClaude}}}})
	var ms api.ChannelMembers
	l.want(200, "web", "GET", "/v1/internal/channels/"+ch+"/members", nil, &ms)
	ms.Members = slices.DeleteFunc(ms.Members, func(m api.ChannelMember) bool { return m.Session == "" })
	if !ms.Multi || len(ms.Members) != 3 || ms.Members[0].Nick != "lead" || ms.Members[1].Nick != "helper" || ms.Members[2].Nick != "claude" {
		t.Fatalf("members: %+v", ms)
	}
	// The one named answers, in its own channel and, said for it, here; the others were given nothing.
	l.waitSaid("web", ms.Members[1].Session, "@helper ", "ok: #1: @helper begin")
	for _, m := range l.said("web", ch) {
		if strings.Contains(m, "@lead ") || strings.Contains(m, "@claude ") {
			t.Errorf("another answered: %s", m)
		}
	}
	if p := l.page("web", ms.Members[2].Session); len(p.Blocks) != 0 {
		t.Errorf("one not named was given: %+v", p.Blocks)
	}
	l.create(400, "web", api.NewSession{Agent: api.AgentClaude, Cwd: l.work, Members: &api.ChannelSetup{Agents: []api.NewMember{{Agent: api.AgentClaude, Nick: "a b"}}}})
}

// A channel that is no session's, made as Satori has it (channel.create with no agent): listed
// in its folder, with no agent until one joins; what is said in it is its messages. An app
// names an agent by Satori's <at id>, as a person types its nick.
func TestChannelBare(t *testing.T) {
	t.Parallel()
	l := start(t)
	var made satori.Channel
	l.wantSat(200, "web", "channel.create", map[string]any{"data": map[string]any{"name": "room"}, "_data": map[string]any{"cwd": l.work}}, &made)
	ch := made.ID
	if ch == "" || made.Name != "room" || made.Type != satori.ChannelText || made.Data == nil || !made.Data.Bare || made.Data.Agent != "" {
		t.Fatalf("made: %+v %+v", made, made.Data)
	}
	listed := false
	for _, ss := range l.sessions("web") {
		if ss.ID == ch {
			listed = ss.Bare && ss.Cwd == l.work && !ss.Archived && ss.Title == "room"
		}
	}
	if !listed {
		t.Fatalf("not listed as it is")
	}
	var ms api.ChannelMembers
	l.want(200, "web", "GET", "/v1/internal/channels/"+ch+"/members", nil, &ms)
	if len(ms.Members) != 2 || ms.Members[0].Member != "ir_web/*" || ms.Members[1].Member != "local/*" || !ms.Joined {
		t.Fatalf("members: %+v", ms)
	}
	// Words with nobody to wake are said all the same; a slash is words too.
	code, r := l.send("web", ch, api.Send{Text: "anyone here"})
	if code != 200 || r.Seq != 1 {
		t.Fatalf("send: %d %+v", code, r)
	}
	if code, _ := l.send("web", ch, api.Send{Text: "/clear"}); code != 200 {
		t.Fatalf("send a slash: %d", code)
	}
	if all := l.said("web", ch); len(all) != 2 || !strings.Contains(all[0], "anyone here") {
		t.Fatalf("said: %v", all)
	}
	// An agent joins: a session of its own in the channel's folder; it is given what names it,
	// with what it has not seen, and answers with ir.
	l.want(200, "web", "POST", "/v1/internal/channels/"+ch+"/members", api.NewMember{Agent: api.AgentClaude}, &ms)
	if len(ms.Members) != 3 || ms.Members[2].Session == "" || ms.Members[2].Cwd != l.work {
		t.Fatalf("members with an agent: %+v", ms)
	}
	agent, nick := ms.Members[2].Session, ms.Members[2].Nick
	bot := l.newApp("bot", true).Token
	l.want(200, "web", "PUT", "/v1/internal/channels/"+ch+"/people", api.People{People: []string{"*"}}, nil)
	var sent []satori.Message
	l.wantSat(200, bot, "message.create", map[string]any{"channel_id": ch, "content": `<at id="agent:` + agent + `"/> look at this`}, &sent)
	l.waitSaid("web", agent, "@"+nick+" look at this")
	got := l.page("web", agent).Blocks[0].Text
	for _, want := range []string{"called in the channel \"room\"", "]: anyone here\n", "(" + ch + ")", "said in the channel as you write it"} {
		if !strings.Contains(got, want) {
			t.Errorf("given without %q:\n%s", want, got)
		}
	}
	l.wantSat(200, "agent-in:"+agent, "message.create", map[string]any{"channel_id": ch, "content": "looked"}, &sent)
	l.waitSaid(bot, ch, "@"+nick+" looked")
	// An agent's name for it does not cover the one it has; a channel with none takes it.
	l.wantSat(200, "agent-in:"+agent, "channel.update", map[string]any{"channel_id": ch, "data": map[string]any{"name": "mine"}}, nil)
	var unnamed, got2 satori.Channel
	l.wantSat(200, "agent-in:"+agent, "channel.create", map[string]any{"data": map[string]any{"name": "made by an agent"}, "_data": map[string]any{"cwd": l.work}}, &unnamed)
	if l.wantSat(200, "web", "channel.get", map[string]any{"channel_id": ch}, &got2); got2.Name != "room" || unnamed.Name != "made by an agent" {
		t.Errorf("named by an agent: %q, one it made %q", got2.Name, unnamed.Name)
	}
	// Renamed and archived as a session is.
	l.wantSat(200, "socket", "channel.update", map[string]any{"channel_id": ch, "data": map[string]any{"name": "hall"}}, nil)
	l.wantSat(200, "web", "channel.delete", map[string]any{"channel_id": ch}, nil)
	for _, ss := range l.sessions("web") {
		if ss.ID == ch && (!ss.Archived || ss.Title != "hall") {
			t.Errorf("after renaming and archiving: %+v", ss)
		}
	}
	// The agent's session is as it was.
	var st api.SessionState
	if l.want(200, "web", "GET", "/v1/internal/sessions/"+agent, nil, &st); st.Archived {
		t.Errorf("the agent's session: %+v", st.Session)
	}
}

// A bare channel opened by a person can make its remote group appear in the sidebar.
func TestRemoteBareChannelOwn(t *testing.T) {
	t.Parallel()
	l := start(t)
	machine := l.newApp("remote-pc", false).App.ID
	agent := l.create(200, "web", api.NewSession{Agent: api.AgentClaude, Cwd: l.work})
	body := createBody(api.NewSession{Machine: machine, Cwd: l.work})
	for _, c := range []struct {
		as  string
		own bool
	}{
		{"web", true},
		{"agent-in:" + agent, false},
	} {
		var made satori.Channel
		l.wantSat(200, c.as, "channel.create", body, &made)
		if made.Data == nil || made.Data.Own != c.own {
			t.Errorf("created as %s: %+v, want own %t", c.as, made.Data, c.own)
		}
	}
}

// An agent that speaks in a channel with ir is held to what it has seen of it: what it had not
// seen comes back with its send; too far behind, it is told to read first. Its receive set
// wakes it by who speaks; two subscribed to each other stop at MaxHops. The one that started an
// agent is told when it says nothing.
func TestChannelSendCheckAndRecv(t *testing.T) {
	t.Parallel()
	l := start(t)
	ch := l.create(200, "web", api.NewSession{Agent: api.AgentClaude, Cwd: l.work, Text: "first"})
	l.waitReply("web", ch, "ok: first")
	var ms api.ChannelMembers
	l.want(200, "web", "POST", "/v1/internal/channels/"+ch+"/members", api.NewMember{Agent: api.AgentClaude, Nick: "guest"}, &ms)
	guest := ms.Members[len(ms.Members)-1].Session
	say := func(as, to, words string) (int, api.SendReply, string) {
		t.Helper()
		var raw string
		code := l.sat(as, "message.create", map[string]any{"channel_id": to, "content": words}, &raw)
		var out []struct {
			Data api.SendReply `json:"_data"`
		}
		if json.Unmarshal([]byte(raw), &out) == nil && len(out) > 0 {
			return code, out[0].Data, raw
		}
		return code, api.SendReply{}, raw
	}

	// A little behind: sent, and what it had not seen comes back; then nothing more to see.
	code, r, raw := say("agent-in:"+guest, ch, "hello all")
	if code != 200 || !r.Said || !strings.Contains(r.Unseen, "Sent. Said in the channel since you last read it") || !strings.Contains(r.Unseen, "]: first\n") ||
		!strings.Contains(r.Unseen, "message.get channel_id="+ch) && strings.Contains(r.Unseen, "channel_id=self") {
		t.Fatalf("the first send: %d %+v %s", code, r, raw)
	}
	if code, r, _ = say("agent-in:"+guest, ch, "and again"); code != 200 || r.Unseen != "" {
		t.Fatalf("with nothing unseen: %d %+v", code, r)
	}
	// Far behind: told to read first, with what there is to read; then it goes.
	for i := range 7 {
		code, sr := l.send("web", ch, api.Send{Text: fmt.Sprintf("plain %d", i)})
		if code != 200 {
			t.Fatalf("send %d: %d", i, code)
		}
		l.waitSaid("web", ch, "@claude ", fmt.Sprintf("ok: #%d: plain %d", sr.Seq, i))
	}
	if code, _, raw = say("agent-in:"+guest, ch, "late"); code != 409 || !strings.Contains(raw, `"behind"`) || !strings.Contains(raw, "Not sent: 14 messages") || !strings.Contains(raw, "plain 6") {
		t.Fatalf("far behind: %d %s", code, raw)
	}
	if code, r, _ = say("agent-in:"+guest, ch, "late, having read"); code != 200 || r.Unseen != "" {
		t.Fatalf("after reading: %d %+v", code, r)
	}
	for _, m := range l.said("web", ch) {
		if strings.HasSuffix(m, " late") {
			t.Errorf("what was refused was said: %s", m)
		}
	}

	// A receive set: every person's words reach it though they name nobody; any member may
	// change any agent's set, an agent another's too, and the channel says so.
	all := api.Recv{"*"}
	l.want(200, "agent-in:"+guest, "PATCH", "/v1/internal/channels/"+ch+"/members/agent:"+guest, api.MemberPatch{Recv: &all}, &ms)
	of := func(ms api.ChannelMembers, id string) api.ChannelMember {
		for _, m := range ms.Members {
			if m.Session == id {
				return m
			}
		}
		return api.ChannelMember{}
	}
	if fmt.Sprint(of(ms, guest).Recv) != "[*]" {
		t.Fatalf("recv set by the agent: %+v", ms.Members)
	}
	_, sr := l.send("web", ch, api.Send{Text: "to everyone"})
	l.waitSaid("web", guest, fmt.Sprintf("ok: #%d: to everyone", sr.Seq))
	l.want(400, "web", "PATCH", "/v1/internal/channels/"+ch+"/members/agent:"+guest, map[string]any{"recv": []string{"agent:" + guest}}, nil)
	l.want(400, "web", "PATCH", "/v1/internal/channels/"+ch+"/members/agent:"+guest, map[string]any{"recv": []string{"nobody"}}, nil)
	var now api.ChannelMembers
	l.want(200, "agent-in:"+ch, "PATCH", "/v1/internal/channels/"+ch+"/members/agent:"+guest, map[string]any{"recv": "at"}, &now)
	if g := of(now, guest); g.Session == "" || len(g.Recv) != 0 {
		t.Fatalf("recv set to at: %+v", ms.Members)
	}
	l.waitSaid("web", ch, "iridium", "@guest 接收：仅 @我")
	_, sr = l.send("web", ch, api.Send{Text: "not for the guest"})
	l.waitSaid("web", ch, "@claude ", fmt.Sprintf("ok: #%d: not for the guest", sr.Seq))
	time.Sleep(300 * time.Millisecond)
	for _, m := range l.said("web", guest) {
		if strings.Contains(m, "not for the guest") {
			t.Errorf("given with an empty set: %s", m)
		}
	}

	// Subscribed to an agent: its turn's last words reach it. Two subscribed to each other pass
	// them on, not without end: MaxHops in a row, then the channel says so.
	l.want(200, "web", "PATCH", "/v1/internal/channels/"+ch+"/members/agent:"+guest, map[string]any{"recv": []string{"agent:" + ch}}, nil)
	_, sr = l.send("web", ch, api.Send{Text: "for the first"})
	l.waitSaid("web", guest, fmt.Sprintf("ok: #%d: for the first", sr.Seq), "@guest ok: #")
	l.want(200, "web", "PATCH", "/v1/internal/channels/"+ch+"/members/agent:"+ch, map[string]any{"recv": []string{"*", "agent:" + guest}}, nil)
	l.send("web", ch, api.Send{Text: "ping"})
	l.waitSaid("web", ch, "未转交", fmt.Sprint(deliver.MaxHops))
	l.want(200, "web", "PATCH", "/v1/internal/channels/"+ch+"/members/agent:"+ch, map[string]any{"recv": []string{"*"}}, nil)
	l.want(200, "web", "PATCH", "/v1/internal/channels/"+ch+"/members/agent:"+guest, map[string]any{"recv": []string{}}, nil)

	// An agent the channel's own agent started: the one that started it is told when the other
	// was called and said nothing.
	l.want(200, "agent-in:"+ch, "POST", "/v1/internal/channels/"+ch+"/members", api.NewMember{Agent: api.AgentClaude, Nick: "kid"}, &ms)
	kid := ms.Members[len(ms.Members)-1].Session
	found := false
	for _, ss := range l.sessions("web") {
		if ss.ID == kid {
			found = ss.Spawned != nil && ss.Spawned.By == ch && ss.Spawned.Channel == ch
		}
	}
	if !found {
		t.Errorf("the one it started is not marked so")
	}
	l.send("web", ch, api.Send{Text: "@kid begin (say nothing)"})
	l.waitSaid("web", ch, "iridium", "@kid 被叫起", "未在频道发话")
	l.waitSaid("web", ch, "@claude ", "kid, which you started, was called in the channel")
}

// A channel's face: given by one of its people, served to whoever sees it; a session's is its
// model's too, which a session of that model with none of its own has.
func TestChannelAvatar(t *testing.T) {
	t.Parallel()
	l := start(t)
	a := l.create(200, "web", api.NewSession{Agent: api.AgentClaude, Cwd: l.work, Text: "first"})
	l.waitReply("web", a, "ok: first")
	b := l.create(200, "web", api.NewSession{Agent: api.AgentClaude, Cwd: l.work, Text: "second"})
	l.waitReply("web", b, "ok: second")
	l.want(404, "web", "GET", "/v1/internal/channels/"+a+"/avatar", nil, nil)
	var png bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	_ = pngpkg.Encode(&png, img)
	put := func(as, id string, body []byte) int {
		return l.do(as, "PUT", "/v1/internal/channels/"+id+"/avatar", map[string]string{"avatar": base64.StdEncoding.EncodeToString(body)}, nil)
	}
	if code := put("web", a, []byte("not an image")); code != 422 {
		t.Errorf("not an image: %d", code)
	}
	// An agent may too (kumo, 2026-11-17).
	if code := put("agent-in:"+a, a, png.Bytes()); code != 200 {
		t.Errorf("an agent: %d", code)
	}
	if code := put("web", a, png.Bytes()); code != 200 {
		t.Fatalf("put: %d", code)
	}
	l.want(200, "web", "GET", "/v1/internal/channels/"+a+"/avatar", nil, nil)
	// The other session of the model has it too, until it is given its own; a's own taken away,
	// the model's stays.
	l.want(200, "web", "GET", "/v1/internal/channels/"+b+"/avatar", nil, nil)
	var u satori.User
	if l.wantSat(200, "web", "user.get", map[string]any{"user_id": "agent:" + b}, &u); !strings.HasPrefix(u.Avatar, "/v1/internal/channels/"+b+"/avatar?v=") {
		t.Errorf("the agent as a user: %+v", u)
	}
	l.want(200, "web", "DELETE", "/v1/internal/channels/"+a+"/avatar", nil, nil)
	l.want(200, "web", "GET", "/v1/internal/channels/"+a+"/avatar", nil, nil)
}

// Answering a message (<quote id/>) calls its agent as if named, and the agent is given a line
// of it; @全体Agent calls every agent. In a session of one agent the quote is quoted lines.
func TestChannelQuoteAndAll(t *testing.T) {
	t.Parallel()
	l := start(t)
	ch := l.create(200, "web", api.NewSession{Agent: api.AgentClaude, Cwd: l.work, Text: "first"})
	l.waitReply("web", ch, "ok: first")
	var ms api.ChannelMembers
	l.want(200, "web", "POST", "/v1/internal/channels/"+ch+"/members", api.NewMember{Agent: api.AgentClaude}, &ms)
	second := ms.Members[len(ms.Members)-1].Session
	// A turn of its own first: what it runs as (its name in the standing words) is known by then.
	if code, _ := l.send("web", second, api.Send{Text: "settle"}); code != 200 {
		t.Fatalf("send to the second: %d", code)
	}
	l.waitReply("web", second, "ok: settle")

	// The second speaks with ir; a person answers it without naming it.
	var sent []satori.Message
	l.wantSat(200, "agent-in:"+second, "message.create", map[string]any{"channel_id": ch, "content": "a thought of mine"}, &sent)
	if len(sent) != 1 {
		t.Fatalf("sent: %+v", sent)
	}
	l.wantSat(200, "web", "message.create", map[string]any{"channel_id": ch, "content": `<quote id="` + sent[0].ID + `"/>tell me more`}, nil)
	l.waitSaid("web", second, "@claude2 ", "tell me more")
	if p := l.page("web", second); !strings.Contains(p.Blocks[len(p.Blocks)-2].Text+p.Blocks[len(p.Blocks)-1].Text, "[answering #"+sent[0].ID+" claude2: a thought of mine] tell me more") {
		t.Errorf("the second was not given the quote: %+v", p.Blocks)
	}
	var page satori.BidiList[satori.Message]
	l.wantSat(200, "web", "message.list", map[string]any{"channel_id": ch, "limit": 3}, &page)
	quotes := 0
	for _, m := range page.Data {
		if strings.HasPrefix(m.Content, `<quote id="`+sent[0].ID+`"/>tell me more`) {
			quotes++
		}
	}
	if quotes != 1 {
		t.Errorf("the answer as listed: %+v", page.Data)
	}
	l.wantSat(400, "web", "message.create", map[string]any{"channel_id": ch, "content": `<quote id="99999"/>no such`}, nil)

	// @全体Agent: both, though the second takes only what names it.
	code, r := l.send("web", ch, api.Send{Text: "@全体Agent roll call"})
	if code != 200 {
		t.Fatalf("send to all: %d %+v", code, r)
	}
	l.waitSaid("web", second, "@claude2 ", fmt.Sprintf("#%d: @全体Agent roll call", r.Seq))
	l.waitSaid("web", ch, "@claude ", fmt.Sprintf("#%d: @全体Agent roll call", r.Seq))

	// Given again, the standing words are not: a line in their place (docs/channels.md "增量").
	var ins []api.Injection
	l.want(200, "web", "GET", "/v1/internal/sessions/"+second+"/injections", nil, &ins)
	ins = turnsOf(ins)
	if len(ins) < 2 || !strings.Contains(string(ins[0].Record), `"static":"`) || strings.Contains(string(ins[0].Record), `"full":true`) || !strings.Contains(string(ins[len(ins)-1].Record), `"full":true`) ||
		strings.Contains(string(ins[0].Record), "prompts:channel.context.guest\"") || !strings.Contains(string(ins[0].Record), "prompts:channel.context.again") {
		t.Errorf("injections: %+v", ins)
	}
	p := l.page("web", second)
	given := ""
	for _, b := range p.Blocks {
		if b.Kind == api.BlockKindUser && strings.HasPrefix(b.Text, fmt.Sprintf("#%d: ", r.Seq)) {
			given = b.Text
		}
	}
	// In its record, Satori's forward: the messages given, each with its author, the call last;
	// the first time with the channel's standing words.
	var rec satori.BidiList[satori.Message]
	l.wantSat(200, "web", "internal/block.list", map[string]any{"channel_id": second}, &rec)
	fwds := []string{}
	for _, m := range rec.Data {
		if strings.Contains(m.Content, "<message forward>") {
			fwds = append(fwds, m.Content)
		}
	}
	again := ""
	for _, f := range fwds {
		if strings.Contains(f, fmt.Sprintf(` seq="%d"`, r.Seq)) {
			again = f
		}
	}
	if len(fwds) < 2 || !strings.Contains(fwds[0], `<iridium:relay channel="`+ch+`"`) || !strings.Contains(fwds[0], " full>You are claude2") ||
		!strings.Contains(fwds[0], `<author id="local" name="Iridium"/>tell me more</message></message>`) ||
		again == "" || strings.Contains(again, " full") || !strings.HasSuffix(again, "@全体Agent roll call</message></message>") {
		t.Errorf("forwards:\n%s", strings.Join(fwds, "\n"))
	}
	if !strings.Contains(given, `In the channel "`) || strings.Contains(given, "You are claude2") {
		t.Errorf("given again with its standing words:\n%s\n%+v", given, p.Blocks)
	}
}

// Two people's channel: the same two, the same one; no agent; its people the two.
func TestDirect(t *testing.T) {
	t.Parallel()
	l := start(t)
	bot := l.newApp("bot", true).Token
	var a, b struct {
		Channel string `json:"channel"`
	}
	var e api.Error
	if code := l.asLogin(bot, "", "POST", "/v1/internal/dm", map[string]string{"user": "local"}, &e); code != 200 {
		t.Fatalf("dm: %d %+v", code, e)
	}
	l.asLogin(bot, "", "POST", "/v1/internal/dm", map[string]string{"user": "local"}, &a)
	l.asLogin(bot, "", "POST", "/v1/internal/dm", map[string]string{"user": "local"}, &b)
	if a.Channel == "" || a.Channel != b.Channel {
		t.Fatalf("dm: %q then %q", a.Channel, b.Channel)
	}
	var ms api.ChannelMembers
	l.want(200, "socket", "GET", "/v1/internal/channels/"+a.Channel+"/members", nil, &ms)
	if len(ms.Members) != 2 || !ms.Multi || ms.Members[0].Member == ms.Members[1].Member {
		t.Errorf("members: %+v", ms)
	}
	if code := l.asLogin(bot, "", "POST", "/v1/internal/dm", map[string]string{"user": "nobody"}, nil); code != 422 {
		t.Errorf("no such user: %d", code)
	}
	l.want(400, "socket", "POST", "/v1/internal/dm", map[string]string{"user": "local"}, nil)
}

// A session called before its record had anything: what it says there is from line 0, kept as
// such (not dropped as "not known"); said where it was not called, from nothing.
func TestSpokeFromZero(t *testing.T) {
	k := calls{}
	k.add("s1", called{Channel: "c1", Seq: 1, From: 0})
	from := k.spoke("s1", "c1", 30)
	if from == nil || *from != 0 {
		t.Fatalf("from %v", from)
	}
	raw, _ := json.Marshal(channel.Data{Session: "s1", From: from, To: 30})
	if !strings.Contains(string(raw), `"from":0`) {
		t.Errorf("from 0 not kept: %s", raw)
	}
	if from := k.spoke("s1", "c2", 40); from != nil {
		t.Errorf("not called there: from %v", from)
	}
}

// An agent's open calls outlive a restart: kept in the store, read back by a new server's.
func TestCallsKept(t *testing.T) {
	t.Parallel()
	l := start(t)
	any := func(called) bool { return true }
	l.s.calls.add("s1", called{Channel: "c1", Seq: 4, From: 10})
	again := calls{st: l.s.st}
	if from := again.spoke("s1", "c1", 20); from == nil || *from != 10 {
		t.Errorf("spoke after a restart: from %v", from)
	}
	fresh := calls{st: l.s.st}
	if got := fresh.take("s1", any); len(got) != 1 || !got[0].Spoke || got[0].From != 20 {
		t.Errorf("taken: %+v", got)
	}
	if got := (&calls{st: l.s.st}).take("s1", any); len(got) != 0 {
		t.Errorf("taken twice: %+v", got)
	}
}

// An agent invited by the page (greet) is asked to say hello as it joins: a turn of its own,
// its words said in the channel. Without greet (ir, an agent's invite) it says nothing.
func TestChannelInviteGreets(t *testing.T) {
	t.Parallel()
	l := start(t)
	ch := l.create(200, "web", api.NewSession{Agent: api.AgentClaude, Cwd: l.work, Text: "first"})
	l.waitReply("web", ch, "ok: first")
	var ms api.ChannelMembers
	l.want(200, "web", "POST", "/v1/internal/channels/"+ch+"/members", api.NewMember{Agent: api.AgentClaude, Greet: true}, &ms)
	l.waitSaid("web", ch, "@claude2", "邀请你加入了这个频道")
	l.want(200, "web", "POST", "/v1/internal/channels/"+ch+"/members", api.NewMember{Agent: api.AgentClaude}, &ms)
	time.Sleep(300 * time.Millisecond)
	for _, m := range l.said("web", ch) {
		if strings.Contains(m, "@claude3 ") && !strings.Contains(m, "加入了频道") {
			t.Errorf("one invited with no greet spoke: %s", m)
		}
	}
}

// Invited with text: once it is in, the one inviting says "@nick text" in the channel, which
// calls it; its answer is said there. No greeting then.
func TestChannelInviteWithWords(t *testing.T) {
	t.Parallel()
	l := start(t)
	ch := l.create(200, "web", api.NewSession{Agent: api.AgentClaude, Cwd: l.work, Text: "first"})
	l.waitReply("web", ch, "ok: first")
	var ms api.ChannelMembers
	l.want(200, "web", "POST", "/v1/internal/channels/"+ch+"/members", api.NewMember{Agent: api.AgentClaude, Nick: "tester", Text: "run the tests", Greet: true}, &ms)
	l.waitSaid("web", ch, "@tester run the tests")
	all := l.waitSaid("web", ch, "@tester", "ok: #")
	for _, m := range all {
		if strings.Contains(m, "邀请你加入了这个频道") {
			t.Errorf("greeted as well: %s", m)
		}
	}
}

// What an agent is given of earlier messages names their files, no paths (kumo, 2026-11-16).
func TestFileNames(t *testing.T) {
	t.Parallel()
	got := fileNames("look at these\n\n[附件] `/home/u/a.png`\n[文件] `/w/report.pdf`", []api.FileRef{{Name: "a.png", Path: "/home/u/a.png"}, {Name: "c.txt", Path: "/x/c.txt"}})
	if want := "look at these\n（附件：a.png、report.pdf、c.txt）"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got := fileNames("plain words", nil); got != "plain words" {
		t.Errorf("no files: %q", got)
	}
}

// turnsOf are the injections that are a channel's messages (not the system prompt, other lines).
func turnsOf(ins []api.Injection) []api.Injection {
	var out []api.Injection
	for _, in := range ins {
		if strings.Contains(string(in.Record), `"kind":"turn-prefix"`) {
			out = append(out, in)
		}
	}
	return out
}

// Everything an agent is given is kept whole: its system prompt, a message's line as written.
func TestInjectionsWhole(t *testing.T) {
	t.Parallel()
	l := start(t)
	id := l.create(200, "web", api.NewSession{Agent: api.AgentClaude, Cwd: l.work, Text: "first words"})
	l.waitReply("web", id, "ok: first words")
	var ins []api.Injection
	l.want(200, "web", "GET", "/v1/internal/sessions/"+id+"/injections", nil, &ins)
	var system, msg bool
	for _, in := range ins {
		r := string(in.Record)
		system = system || (strings.Contains(r, `"kind":"system"`) && strings.Contains(r, "hosted by Iridium"))
		msg = msg || (strings.Contains(r, `"kind":"message"`) && strings.Contains(r, "first words"))
	}
	if !system || !msg {
		t.Errorf("system %v, message %v: %+v", system, msg, ins)
	}
}

// A channel of no agent of its own is listed with its last words, as a session is: an agent's
// with its nick (kumo, 2026-11-16: the reply there was not shown under its name).
func TestBareChannelLast(t *testing.T) {
	t.Parallel()
	l := start(t)
	var made satori.Channel
	l.wantSat(200, "web", "channel.create", map[string]any{"data": map[string]any{"name": "room"}, "_data": map[string]any{"cwd": l.work}}, &made)
	room := made.ID
	l.want(200, "web", "POST", "/v1/internal/channels/"+room+"/members", api.NewMember{Agent: api.AgentClaude, Nick: "fish"}, nil)
	l.send("web", room, api.Send{Text: "@fish hello there"})
	l.waitSaid("web", room, "@fish ", "ok: #")
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		var row *api.Session
		for _, ss := range l.sessions("web") {
			if ss.ID == room {
				row = &ss
			}
		}
		if row != nil && row.Last != nil && row.Last.Role == "assistant" && row.Last.Nick == "fish" && strings.HasPrefix(row.Last.Text, "ok: #") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the room as listed: %+v", row)
		}
	}
}

// A session's channel with other agents in it is listed with the last words said there, the
// others' and the people's too, not its own agent's (kumo, 2026-11-16).
func TestChannelLast(t *testing.T) {
	t.Parallel()
	l := start(t)
	ch := l.create(200, "web", api.NewSession{Agent: api.AgentClaude, Cwd: l.work, Text: "first"})
	l.waitReply("web", ch, "ok: first")
	l.want(200, "web", "POST", "/v1/internal/channels/"+ch+"/members", api.NewMember{Agent: api.AgentClaude, Nick: "fish"}, nil)
	listed := func(what string, ok func(*api.LastSaid) bool) {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(50 * time.Millisecond) {
			var row *api.Session
			for _, ss := range l.sessions("web") {
				if ss.ID == ch {
					row = &ss
				}
			}
			if row != nil && row.Last != nil && ok(row.Last) {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: the channel as listed: %+v", what, row)
			}
		}
	}
	// Another agent's reply, which its own agent's record has not.
	l.send("web", ch, api.Send{Text: "@fish hello there"})
	l.waitSaid("web", ch, "@fish ", "ok: #")
	listed("fish's reply", func(last *api.LastSaid) bool {
		return last.Role == "assistant" && last.Nick == "fish" && strings.HasPrefix(last.Text, "ok: #")
	})
	// A person's words that wake nobody.
	l.want(200, "web", "PATCH", "/v1/internal/channels/"+ch+"/members/agent:"+ch, map[string]any{"recv": "at"}, nil)
	l.send("web", ch, api.Send{Text: "just saying"})
	listed("a person's words", func(last *api.LastSaid) bool { return last.Role == "user" && last.Text == "just saying" })
}

// A channel's log shows the files its agents' words give, as a page of their records does: a
// file line in what the agent said in its turn, kept as it is taken in; a file link (ir:<id>) in
// what it sent with ir, found as it is read.
func TestChannelSaidFiles(t *testing.T) {
	t.Parallel()
	l := start(t)
	ch := l.create(200, "web", api.NewSession{Agent: api.AgentClaude, Cwd: l.work, Text: "first"})
	l.waitReply("web", ch, "ok: first")
	var ms api.ChannelMembers
	l.want(200, "web", "POST", "/v1/internal/channels/"+ch+"/members", api.NewMember{Agent: api.AgentClaude, Nick: "guest"}, &ms)
	guest := ms.Members[len(ms.Members)-1].Session
	png := func(name string) string {
		var b bytes.Buffer
		pngpkg.Encode(&b, image.NewRGBA(image.Rect(0, 0, 2, 2)))
		p := filepath.Join(l.work, name)
		if err := os.WriteFile(p, b.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}

	// Said in its turn: "ok: #<n>: look\n[文件] `<path>`".
	p := png("said.png")
	l.send("web", ch, api.Send{Text: "look\n[文件] `" + p + "`"})
	l.waitSaid("web", ch, "@claude ", "ok: #", `<img src="internal:`, `title="said.png"`)

	// Sent with ir: its link.
	lk, err := l.s.files.Link(guest, png("sent.png"))
	if err != nil {
		t.Fatal(err)
	}
	l.wantSat(200, "agent-in:"+guest, "message.create", map[string]any{"channel_id": ch, "content": "here " + lk.Markdown}, nil)
	l.waitSaid("web", ch, "@guest here", `<img src="internal:`, "/"+lk.File.Ref+`"`, `title="sent.png"`)
}

// An agent that works in another folder than the channel's (the user, 2026-11-18): it is told
// where the channel is and where it works; one at home is told nothing of places until then.
func TestChannelAgentElsewhere(t *testing.T) {
	t.Parallel()
	l := start(t)
	ch := l.create(200, "web", api.NewSession{Agent: api.AgentClaude, Cwd: l.work, Text: "first"})
	l.waitReply("web", ch, "ok: first")
	other := filepath.Join(l.work, "elsewhere")
	if err := os.Mkdir(other, 0o755); err != nil {
		t.Fatal(err)
	}
	var ms api.ChannelMembers
	l.want(200, "web", "POST", "/v1/internal/channels/"+ch+"/members", api.NewMember{Agent: api.AgentClaude, Nick: "away", Cwd: other}, &ms)
	ms.Members = slices.DeleteFunc(ms.Members, func(m api.ChannelMember) bool { return m.Session == "" })
	if len(ms.Members) != 2 || ms.Members[1].Cwd != other {
		t.Fatalf("members: %+v", ms)
	}
	away := ms.Members[1].Session
	code, r := l.send("web", ch, api.Send{Text: "@away look"})
	if code != 200 {
		t.Fatalf("send: %d %+v", code, r)
	}
	l.waitSaid("web", away, "@away ", fmt.Sprintf("ok: #%d: @away look", r.Seq))
	p := l.page("web", away)
	if len(p.Blocks) == 0 {
		t.Fatal("nothing given")
	}
	for _, want := range []string{"The channel's folder is host:" + l.work + ";", "away (", "at host:" + other, "you work at host:" + other + ".", "(host:" + other + "/notes.md)"} {
		if !strings.Contains(p.Blocks[0].Text, want) {
			t.Errorf("given without %q:\n%s", want, p.Blocks[0].Text)
		}
	}
}

// An agent of the lineup that cannot be started (kumo, 2026-11-18): the channel is made all the
// same and says who is not in it; the start is not answered with an error, which left the page
// where it was to make another at each press.
func TestLineupMemberFails(t *testing.T) {
	t.Parallel()
	l := start(t)
	ch := l.create(200, "web", api.NewSession{Agent: api.AgentClaude, Cwd: l.work, Members: &api.ChannelSetup{Agents: []api.NewMember{{Agent: api.AgentClaude, Nick: "far", Machine: "no-such-computer", Cwd: "/"}}}})
	var log satori.BidiList[satori.Message]
	l.wantSat(200, "web", "message.list", map[string]any{"channel_id": ch, "limit": 5}, &log)
	said := false
	for _, m := range log.Data {
		said = said || strings.HasPrefix(m.Content, "far（no-such-computer） 未加入：")
	}
	if !said {
		t.Errorf("the channel does not say who is not in it: %+v", log.Data)
	}
}

// An agent whose app's scope has not the channel (one at another computer, kumo, 2026-11-18):
// it sees the channel it is in and the agents in it with it, and no more; what it may do to
// their sessions is still its scope's.
func TestMemberSeesItsChannel(t *testing.T) {
	t.Parallel()
	l := start(t)
	a, b := filepath.Join(l.work, "a"), filepath.Join(l.work, "b")
	for _, d := range []string{a, b} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	ch := l.create(200, "web", api.NewSession{Agent: api.AgentClaude, Cwd: b, Text: "first"})
	l.waitReply("web", ch, "ok: first")
	apart := l.create(200, "web", api.NewSession{Agent: api.AgentClaude, Cwd: b, Text: "apart"})
	var ms api.ChannelMembers
	l.want(200, "web", "POST", "/v1/internal/channels/"+ch+"/members", api.NewMember{Agent: api.AgentClaude, Nick: "away", Cwd: a}, &ms)
	l.want(200, "web", "POST", "/v1/internal/channels/"+ch+"/members", api.NewMember{Agent: api.AgentClaude, Nick: "near"}, &ms)
	by := map[string]string{}
	for _, m := range ms.Members {
		by[m.Nick] = m.Session
	}
	away, near := by["away"], by["near"]
	if away == "" || near == "" {
		t.Fatalf("members: %+v", ms)
	}
	app := l.newApp("far", true)
	l.want(200, "socket", "PUT", "/v1/internal/apps/far/perms", api.Perms{Agents: []string{"*"}, Scope: []string{"主机:" + a}}, nil)
	state := func(id string) api.Session {
		st, err := l.s.m.State(id)
		if err != nil {
			t.Fatal(err)
		}
		return st.Session
	}
	in := caller{Identity: gate.Identity{App: app.App.ID}, agent: true, session: away}
	for _, id := range []string{ch, near, away} {
		if !l.s.seenBy(in, state(id)) {
			t.Errorf("%s of its channel not seen", id)
		}
	}
	if l.s.seenBy(in, state(apart)) {
		t.Error("a session of no channel of its seen")
	}
	// The app itself, no agent's session with it: its scope alone.
	if l.s.seenBy(caller{Identity: gate.Identity{App: app.App.ID}}, state(ch)) {
		t.Error("the app sees the channel out of its scope")
	}
	if _, err := l.s.using(in, near); err == nil {
		t.Error("it may use a session out of its scope")
	}
}
