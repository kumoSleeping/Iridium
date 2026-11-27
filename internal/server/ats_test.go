package server

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/gate"
	"github.com/kumoSleeping/iridium/internal/satori"
)

func TestNamedIn(t *testing.T) {
	t.Parallel()
	names := []string{"Ann", "Ann Lee", "全体成员", "bo.b"}
	for text, want := range map[string]string{
		"hi @ann":               "ann",
		"hi @Ann Lee, look":     "ann lee",
		"@Anna":                 "",
		"mail a@ann":            "",
		"`@ann`":                "",
		"> @ann\nwell":          "",
		"```\n@ann\n```\n@全体成员": "全体成员",
		"@bo.b and @ann":        "ann,bo.b",
	} {
		got := namedIn(text, names)
		var keys []string
		for _, n := range []string{"ann", "ann lee", "bo.b", "全体成员"} {
			if got[n] {
				keys = append(keys, n)
			}
		}
		if s := strings.Join(keys, ","); s != want {
			t.Errorf("%q names %q, want %q", text, s, want)
		}
	}
}

// A login a message names has a mark on the channel till it reads that far: by name, by all the
// members, by an answer to its message, from a person or an agent; never for its own words.
func TestChannelAts(t *testing.T) {
	t.Parallel()
	l := start(t)
	bot := l.newApp("bot")
	if ok, err := l.s.st.AddApprovedLogin(bot.App.ID, "ann", "Ann", time.Now()); !ok || err != nil {
		t.Fatal(ok, err)
	}
	host := l.s.Tag(gate.HostID)
	ch := l.create(200, "web", api.NewSession{Agent: api.AgentClaude, Cwd: l.work, Text: "first"})
	l.waitReply("web", ch, "ok: first")
	l.want(200, "web", "PUT", "/v1/internal/channels/"+ch+"/people", api.People{People: []string{"*"}}, nil)
	every := onFrom("*")
	l.want(200, "web", "PATCH", "/v1/internal/channels/"+ch+"/members/agent:"+ch, api.MemberPatch{On: &every}, nil)
	ats := func(as string) api.ChannelAt {
		t.Helper()
		var out api.ChannelAts
		code := 0
		if as == "ann" {
			code = l.asLogin(bot.Token, "ann", "GET", "/v1/internal/ats", nil, &out)
		} else {
			code = l.do(as, "GET", "/v1/internal/ats", nil, &out)
		}
		if code != 200 {
			t.Fatalf("ats of %s: %d", as, code)
		}
		return out[ch]
	}
	read := func(as string, seq int64) {
		t.Helper()
		code := 0
		if as == "ann" {
			code = l.asLogin(bot.Token, "ann", "POST", "/v1/internal/channels/"+ch+"/read", api.Read{Seq: seq}, nil)
		} else {
			code = l.do(as, "POST", "/v1/internal/channels/"+ch+"/read", api.Read{Seq: seq}, nil)
		}
		if code != 200 {
			t.Fatalf("read as %s: %d", as, code)
		}
	}
	// settle reads the channel whole as each of them, till no mark of something said a moment
	// ago (the agent says the words back) comes after.
	settle := func(as ...string) {
		t.Helper()
		for quiet := 0; quiet < 5; time.Sleep(50 * time.Millisecond) {
			quiet++
			for _, a := range as {
				if ats(a).Seq != 0 {
					read(a, 1<<40)
					quiet = 0
				}
			}
		}
	}
	say := func(content string) int64 {
		t.Helper()
		var ms []satori.Message
		if code, e := l.satoriCall(bot, "ann", "message.create", map[string]any{"channel_id": ch, "content": content}, &ms); code != 200 || len(ms) == 0 {
			t.Fatalf("ann says %q: %d %+v", content, code, e)
		}
		n, _ := strconv.ParseInt(ms[len(ms)-1].ID, 10, 64)
		return n
	}

	// By name: hers, not the author's.
	code, r := l.send("web", ch, api.Send{Text: "ping @ann, here"})
	if code != 200 || r.Seq == 0 {
		t.Fatalf("send: %d %+v", code, r)
	}
	l.waitReply("web", ch, "ok: ping @ann, here")
	// The agent's own words name her too, once they are told as its reply: a moment after its
	// turn's end is seen. Waited for, so that it is not after she has read (below).
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if m, ok := l.s.ch.Get(ch, r.Seq+1); ok && m.D.Final {
			break
		}
	}
	time.Sleep(100 * time.Millisecond)
	if a := ats("ann"); a.Seq != r.Seq || a.Everyone {
		t.Errorf("named: %+v, want #%d", a, r.Seq)
	}
	if a := ats("web"); a.Seq != 0 {
		t.Errorf("the author has a mark: %+v", a)
	}
	if a := ats("agent-in:" + ch); a.Seq != 0 {
		t.Errorf("an agent has a mark: %+v", a)
	}
	if b := string(l.s.hub.Get("ats")); !strings.Contains(b, bot.App.ID+"/ann") {
		t.Errorf("the state: %s", b)
	}
	// Read short of it: still there; read that far: gone.
	read("ann", r.Seq-1)
	if a := ats("ann"); a.Seq != r.Seq {
		t.Errorf("read short of it: %+v", a)
	}
	read("ann", 1<<40)
	if a := ats("ann"); a.Seq != 0 {
		t.Errorf("read: %+v", a)
	}

	// All the members: everyone but who said it; the agent's own words name as a person's do.
	all := say("@全体成员 look")
	l.waitReply("web", ch, "ok: @全体成员 look")
	if a := ats("web"); a.Seq != all || !a.Everyone {
		t.Errorf("all the members: %+v, want #%d", a, all)
	}
	// The agent's words reach the channel a moment after its turn's end is seen.
	for deadline := time.Now().Add(5 * time.Second); ats("ann").Seq == 0 && time.Now().Before(deadline); {
		time.Sleep(20 * time.Millisecond)
	}
	if a := ats("ann"); a.Seq <= all || !a.Everyone {
		t.Errorf("her own words named her, or the agent's did not: %+v (hers #%d)", a, all)
	}
	// Named after that: the first one's place, no longer everyone's alone.
	say("and @" + host + " above all")
	if a := ats("web"); a.Seq != all || a.Everyone {
		t.Errorf("named after all were: %+v", a)
	}
	l.waitReply("web", ch, "ok: and @"+host+" above all")
	settle("web", "ann")

	// An answer to her message does not name her (kumo, 2026-11-21: a quote is no @); <at id>
	// does.
	var sent []satori.Message
	l.wantSat(200, "web", "message.create", map[string]any{"channel_id": ch, "content": fmt.Sprintf(`<quote id="%d"/>about that`, all)}, &sent)
	if len(sent) == 0 {
		t.Fatal("nothing sent")
	}
	if a := ats("ann"); a.Seq != 0 {
		t.Errorf("answered: %+v, want none", a)
	}
	l.waitSaid("web", ch, "ok: ", "about that")
	settle("ann")
	code, r = l.send("web", ch, api.Send{Text: `see <at id="ann"/>`})
	if code != 200 {
		t.Fatalf("send: %d", code)
	}
	if a := ats("ann"); a.Seq != r.Seq || a.Everyone {
		t.Errorf("<at id>: %+v, want #%d", a, r.Seq)
	}
}

// In a channel of several agents (what is said goes through the channel's rules), the same.
func TestChannelAtsAmongSeveral(t *testing.T) {
	t.Parallel()
	l := start(t)
	bot := l.newApp("bot")
	if ok, err := l.s.st.AddApprovedLogin(bot.App.ID, "ann", "Ann", time.Now()); !ok || err != nil {
		t.Fatal(ok, err)
	}
	ch := l.create(200, "web", api.NewSession{Agent: api.AgentClaude, Cwd: l.work, Text: "first"})
	l.waitReply("web", ch, "ok: first")
	var ms api.ChannelMembers
	l.want(200, "web", "POST", "/v1/internal/channels/"+ch+"/members", api.NewMember{Agent: api.AgentClaude}, &ms)
	second := ms.Members[len(ms.Members)-1].Session
	l.want(200, "web", "PUT", "/v1/internal/channels/"+ch+"/people", api.People{People: []string{"*"}}, nil)
	mark := func() api.ChannelAt {
		var out api.ChannelAts
		l.asLogin(bot.Token, "ann", "GET", "/v1/internal/ats", nil, &out)
		return out[ch]
	}
	// A person's, to all the members.
	code, r := l.send("web", ch, api.Send{Text: "@全体成员 meeting"})
	if code != 200 {
		t.Fatalf("send: %d", code)
	}
	if a := mark(); a.Seq != r.Seq || !a.Everyone {
		t.Errorf("all the members: %+v, want #%d", a, r.Seq)
	}
	// Both agents were called, and say it back.
	l.waitSaid("web", ch, "@claude ", fmt.Sprintf("ok: #%d: @全体成员 meeting", r.Seq))
	l.waitSaid("web", ch, "@claude2 ", fmt.Sprintf("ok: #%d: @全体成员 meeting", r.Seq))
	for quiet := 0; quiet < 5; time.Sleep(50 * time.Millisecond) {
		var s1, s2 api.SessionState
		l.want(200, "web", "GET", "/v1/internal/sessions/"+ch, nil, &s1)
		l.want(200, "web", "GET", "/v1/internal/sessions/"+second, nil, &s2)
		if mark().Seq != 0 {
			l.asLogin(bot.Token, "ann", "POST", "/v1/internal/channels/"+ch+"/read", api.Read{Seq: 1 << 40}, nil)
			quiet = 0
			continue
		}
		if s1.Status != api.StatusIdle || s2.Status != api.StatusIdle {
			quiet = 0
			continue
		}
		quiet++
	}
	// An agent's ir names her.
	var sent []satori.Message
	l.wantSat(200, "agent-in:"+second, "message.create", map[string]any{"channel_id": ch, "content": "done, @Ann"}, &sent)
	if len(sent) != 1 {
		t.Fatalf("sent: %+v", sent)
	}
	if a := mark(); strconv.FormatInt(a.Seq, 10) != sent[0].ID || a.Everyone {
		t.Errorf("named by an agent: %+v, want #%s", a, sent[0].ID)
	}
}

// Another session's agent, not of the channel, writing to it with ir: the words are that agent's
// (not the login its turn acts for, kumo 2026-11-21), and they reach the channel's own agent as a
// letter does.
func TestChannelLetterFromAnotherSession(t *testing.T) {
	t.Parallel()
	l := start(t)
	ch := l.create(200, "web", api.NewSession{Agent: api.AgentClaude, Cwd: l.work, Text: "first"})
	l.waitReply("web", ch, "ok: first")
	l.want(200, "web", "POST", "/v1/internal/channels/"+ch+"/members", api.NewMember{Agent: api.AgentClaude}, nil)
	other := l.create(200, "web", api.NewSession{Agent: api.AgentClaude, Cwd: l.work, Text: "elsewhere"})
	l.waitReply("web", other, "ok: elsewhere")
	var sent []satori.Message
	l.wantSat(200, "agent-in:"+other, "message.create", map[string]any{"channel_id": ch, "content": "a letter"}, &sent)
	if len(sent) != 1 {
		t.Fatalf("sent: %+v", sent)
	}
	var got satori.Message
	l.wantSat(200, "agent-in:"+other, "message.get", map[string]any{"channel_id": ch, "message_id": sent[0].ID}, &got)
	if got.User == nil || got.User.ID != "agent:"+other {
		t.Fatalf("in the log as: %+v", got.User)
	}
	l.waitSaid("web", ch, "@claude ", fmt.Sprintf("ok: #%s: a letter", sent[0].ID))
}
