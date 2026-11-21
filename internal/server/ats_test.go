package server

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/gate"
	"github.com/kumoSleeping/iridium/internal/push"
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
	bot := l.newApp("bot", true)
	if ok, err := l.s.st.AddApprovedLogin(bot.App.ID, "ann", "Ann", time.Now()); !ok || err != nil {
		t.Fatal(ok, err)
	}
	host := l.s.Tag(gate.HostID)
	ch := l.create(200, "web", api.NewSession{Agent: api.AgentClaude, Cwd: l.work, Text: "first"})
	l.waitReply("web", ch, "ok: first")
	l.want(200, "web", "PUT", "/v1/internal/channels/"+ch+"/people", api.People{People: []string{"*"}}, nil)
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
	bot := l.newApp("bot", true)
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
		if quiet++; mark().Seq != 0 {
			l.asLogin(bot.Token, "ann", "POST", "/v1/internal/channels/"+ch+"/read", api.Read{Seq: 1 << 40}, nil)
			quiet = 0
		}
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

// The login a message names is pushed it (kind "at"); its own words push it nothing.
func TestChannelAtPush(t *testing.T) {
	t.Parallel()
	p := newPushRig(t)
	ua, _ := ecdh.P256().GenerateKey(rand.Reader)
	auth := make([]byte, 16)
	rand.Read(auth)
	got := make(chan api.PushMessage, 10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var m api.PushMessage
		if plain, err := push.Decrypt(ua, auth, body); err == nil {
			json.Unmarshal(plain, &m)
		}
		got <- m
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	sub := api.PushSubscription{Endpoint: srv.URL}
	sub.Keys.P256dh = base64.RawURLEncoding.EncodeToString(ua.PublicKey().Bytes())
	sub.Keys.Auth = base64.RawURLEncoding.EncodeToString(auth)
	if code := p.do("POST", "/v1/internal/web/push/subscriptions", sub, nil); code != 200 {
		t.Fatalf("subscribe: %d", code)
	}
	p.do("PUT", "/v1/internal/web/push", api.PushSwitch{Enabled: true}, nil)
	// The session's rule: nothing pushed. One named is pushed all the same.
	created, _ := createAt(p.t, http.DefaultClient, p.base, api.NewSession{Agent: api.AgentClaude, Cwd: p.work, Text: "hi"})
	p.do("PUT", "/v1/internal/web/push/rules", api.PushRule{Scope: api.PushScopeSession, Target: created, Mode: api.PushModeOff}, nil)
	// The agent says the words back: its message names the host, the host's own does not.
	sendAt(p.t, http.DefaultClient, p.base, created, api.Send{Text: "tell @Iridium"})
	for {
		select {
		case m := <-got:
			if m.Kind != "at" {
				continue
			}
			if m.Session != created || !strings.Contains(m.Body, "ok: tell @Iridium") {
				t.Errorf("push: %+v", m)
			}
			return
		case <-time.After(10 * time.Second):
			t.Fatal("no push")
		}
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
