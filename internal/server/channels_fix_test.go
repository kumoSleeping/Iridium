package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/channel"
	"github.com/kumoSleeping/iridium/internal/gate"
	"github.com/kumoSleeping/iridium/internal/satori"
)

// An agent woken in a channel acts for whoever woke it: its ir has that login's rights, not
// those of whoever its last turn was for; the channel's own agent, not woken, is as it was.
func TestChannelWakeOrigin(t *testing.T) {
	t.Parallel()
	l := start(t)
	ch := l.create(200, "web", api.NewSession{Agent: api.AgentClaude, Cwd: l.work, Text: "first"})
	l.waitReply("web", ch, "ok: first")
	var ms api.ChannelMembers
	l.want(200, "web", "POST", "/v1/internal/channels/"+ch+"/members", api.NewMember{Agent: api.AgentClaude}, &ms)
	second := ms.Members[len(ms.Members)-1].Session
	if o, ok := l.s.originOf(second); !ok || !o.Host() {
		t.Fatalf("the second agent's turn before anyone woke it: %+v %v", o, ok)
	}
	bot := l.newApp("bot", true).Token
	l.want(200, "web", "PUT", "/v1/internal/channels/"+ch+"/people", api.People{People: []string{"*"}}, nil)
	code, r := l.send(bot, ch, api.Send{Text: "@claude2 from the bot"})
	if code != 200 {
		t.Fatalf("the bot names the second agent: %d %+v", code, r)
	}
	l.waitSaid("web", second, "from the bot")
	if o, ok := l.s.originOf(second); !ok || o.Host() || o.App == "" {
		t.Errorf("the woken agent's turn is for %+v, not the bot", o)
	}
	if o, ok := l.s.originOf(ch); !ok || !o.Host() {
		t.Errorf("the channel's own agent, not woken, is now for %+v", o)
	}
}

// two is a channel of two agents: its own, and a second one in it by a subscription.
func two(t *testing.T) (l *live, ch, second string) {
	t.Helper()
	l = start(t)
	ch = l.create(200, "web", api.NewSession{Agent: api.AgentClaude, Cwd: l.work, Text: "first"})
	l.waitReply("web", ch, "ok: first")
	var ms api.ChannelMembers
	l.want(200, "web", "POST", "/v1/internal/channels/"+ch+"/members", api.NewMember{Agent: api.AgentClaude}, &ms)
	return l, ch, ms.Members[len(ms.Members)-1].Session
}

// running waits until session id's agent is in a turn.
func (l *live) running(id string) {
	l.t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		var st api.SessionState
		if l.do("web", "GET", "/v1/internal/sessions/"+id, nil, &st); st.Status == api.StatusRunning {
			return
		}
		if time.Now().After(deadline) {
			l.t.Fatalf("no turn in %s", id)
		}
	}
}

// noCalls waits until session id's agent has no call open.
func (l *live) noCalls(id string) {
	l.t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		l.s.calls.mu.Lock()
		n := len(l.s.calls.load(id))
		l.s.calls.mu.Unlock()
		if n == 0 {
			return
		}
		if time.Now().After(deadline) {
			l.t.Fatalf("%s has %d calls open still", id, n)
		}
	}
}

// A call waits for the turn that takes its message: a turn of the agent's own that ends while
// the message waits in the queue is not its answer, and is not said in the channel.
func TestChannelCallWaitsForItsTurn(t *testing.T) {
	t.Parallel()
	l, ch, second := two(t)
	if code, _ := l.send("web", second, api.Send{Text: "its own work WAIT"}); code != 200 {
		t.Fatalf("send to the second: %d", code)
	}
	l.running(second)
	code, r := l.send("web", ch, api.Send{Text: "@claude2 when you can"})
	if code != 200 || !r.Queued {
		t.Fatalf("a message for a busy agent: %d %+v", code, r)
	}
	all := l.waitSaid("web", ch, " @claude2 ", fmt.Sprintf("ok: #%d: @claude2 when you can", r.Seq))
	for _, m := range all {
		if strings.Contains(m, "its own work") {
			t.Errorf("another turn's reply said in the channel: %s", m)
		}
	}
	l.noCalls(second)
}

// A turn cut short (the agent stopped) closes its calls: the channel is told the agent said
// nothing, and what it says in a later turn of its own is not said there.
func TestChannelCallClosedWhenCutShort(t *testing.T) {
	t.Parallel()
	l, ch, second := two(t)
	if code, _ := l.send("web", second, api.Send{Text: "its own work HOLD"}); code != 200 {
		t.Fatalf("send to the second: %d", code)
	}
	l.running(second)
	// Open while the turn runs, as a message that joined it leaves it.
	l.s.calls.add(second, called{Channel: ch, Seq: 1, Input: "0190a7c0-0000-7000-8000-000000000001"})
	l.want(200, "web", "POST", "/v1/internal/sessions/"+second+"/stop", nil, nil)
	l.waitSaid("web", ch, "@claude2 被叫起（#1）")
	l.noCalls(second)
	if code, _ := l.send("web", second, api.Send{Text: "unrelated"}); code != 200 {
		t.Fatalf("send to the second after the stop: %d", code)
	}
	l.waitReply("web", second, "ok: unrelated")
	time.Sleep(200 * time.Millisecond)
	for _, m := range l.said("web", ch) {
		if strings.Contains(m, "unrelated") {
			t.Errorf("a later turn's reply said in the channel: %s", m)
		}
	}
}

// Calls kept from before a restart whose turn ended unheard are closed by the next turn's
// start; one whose message still waits in the queue stays open.
func TestCallsOldClosed(t *testing.T) {
	t.Parallel()
	l, ch, second := two(t)
	l.s.calls.add(second, called{Channel: ch, Seq: 1, Input: "0190a7c0-0000-7000-8000-000000000002"})
	// As a new process reads them.
	l.s.calls.of = nil
	l.s.calls.open()
	if code, _ := l.send("web", second, api.Send{Text: "a new turn"}); code != 200 {
		t.Fatalf("send: %d", code)
	}
	l.waitReply("web", second, "ok: a new turn")
	l.waitSaid("web", ch, "@claude2 被叫起（#1）")
	for _, m := range l.said("web", ch) {
		if strings.Contains(m, "a new turn") {
			t.Errorf("a later turn's reply said in the channel: %s", m)
		}
	}
}

// seenOf is how far session id's agent has been given channel ch.
func (l *live) seenOf(ch, id string) int64 {
	l.t.Helper()
	var ms api.ChannelMembers
	l.want(200, "web", "GET", "/v1/internal/channels/"+ch+"/members", nil, &ms)
	for _, m := range ms.Members {
		if m.Session == id {
			return m.Seen
		}
	}
	l.t.Fatalf("%s is not in %s", id, ch)
	return 0
}

// A reply said for an agent stays said when it cannot be passed on: after too many agents in a
// row the channel has it and a line that it was not passed on, not a line that the agent said
// nothing.
func TestChannelRelayStopped(t *testing.T) {
	t.Parallel()
	l, ch, second := two(t)
	// One line of agents: a person calls the second alone, into a turn of its own that runs,
	// naming someone nobody is called yet; the channel's own agent takes that name meanwhile.
	// The fake agents echo: the second's reply names the first, whose echo names the second...
	at, helper := "at", "helper"
	l.want(200, "web", "PATCH", "/v1/internal/channels/"+ch+"/members/agent:"+ch, api.MemberPatch{Recv: &at}, nil)
	if code, _ := l.send("web", second, api.Send{Text: "its own work WAIT"}); code != 200 {
		t.Fatalf("send to the second: %d", code)
	}
	l.running(second)
	code, r := l.send("web", ch, api.Send{Text: "@claude2 ask @helper back", Steer: true})
	if code != 200 || r.Queued {
		t.Fatalf("send: %d %+v", code, r)
	}
	l.want(200, "web", "PATCH", "/v1/internal/channels/"+ch+"/members/agent:"+ch, api.MemberPatch{Nick: &helper}, nil)
	all := l.waitSaid("web", ch, "iridium", "未转交 #")
	time.Sleep(300 * time.Millisecond)
	all = l.said("web", ch)
	var stoppedAt string
	for _, m := range all {
		if strings.Contains(m, "被叫起") {
			t.Errorf("a reply that was said is told as none: %s", m)
		}
		if _, rest, ok := strings.Cut(m, "未转交 #"); ok {
			stoppedAt, _, _ = strings.Cut(rest, "：")
		}
	}
	// The message not passed on is there, said for the second agent (the fifth in the row).
	if stoppedAt == "" {
		t.Fatalf("no line of what was not passed on:\n%s", strings.Join(all, "\n"))
	}
	found := false
	for _, m := range all {
		found = found || strings.HasPrefix(m, stoppedAt+" @claude2 ok: ")
	}
	if !found {
		t.Errorf("the reply not passed on (#%s) is not in the channel:\n%s", stoppedAt, strings.Join(all, "\n"))
	}
}

// The count of agents passing a message on is a channel's, begun anew by a person speaking in
// it: nothing of one channel's run counts in another, nor outlives its run.
func TestChainsByChannel(t *testing.T) {
	var c chains
	lk := c.person("ch1", "me")
	c.pass("ch1", "a", link{epoch: lk.epoch, hop: 3, kin: 2})
	if got := c.of("ch1", "a"); got.hop != 3 || got.kin != 2 || got.origin != "me" {
		t.Errorf("as passed: %+v", got)
	}
	c.pass("ch1", "a", link{epoch: lk.epoch, hop: 1, kin: 5})
	if got := c.of("ch1", "a"); got.hop != 3 || got.kin != 5 {
		t.Errorf("a shorter line shortened the count: %+v", got)
	}
	if got := c.of("ch2", "a"); got.hop != 0 || got.kin != 0 {
		t.Errorf("one channel's count in another: %+v", got)
	}
	next := c.person("ch1", "you")
	if got := c.of("ch1", "a"); got.hop != 0 || got.kin != 0 || got.origin != "you" || got.epoch != next.epoch {
		t.Errorf("after a person spoke: %+v", got)
	}
	// What was on its way as the person spoke belongs to the run that is over.
	c.pass("ch1", "a", link{epoch: lk.epoch, hop: 4})
	if got := c.of("ch1", "a"); got.hop != 0 {
		t.Errorf("a pass of the run before counted: %+v", got)
	}
}

// An agent's place in a channel moves as far as what it was given, no further: what its ir was
// refused with is not lost, and what is said while its turn runs is not marked read by the
// reply said for it.
func TestChannelSeenOnlyWhatWasGiven(t *testing.T) {
	t.Parallel()
	l, ch, second := two(t)
	code, r := l.send("web", ch, api.Send{Text: "plain words"})
	if code != 200 {
		t.Fatalf("send: %d", code)
	}
	l.waitSaid("web", ch, "@claude ", fmt.Sprintf("ok: #%d: plain words", r.Seq))
	// Refused (no such message to answer): what it has not seen is not given, so not read.
	l.wantSat(400, "agent-in:"+second, "message.create", map[string]any{"channel_id": ch, "content": `<quote id="99999"/>no such`}, nil)
	if seen := l.seenOf(ch, second); seen != 0 {
		t.Errorf("a refused send moved its place to %d", seen)
	}
	var raw string
	l.wantSat(200, "agent-in:"+second, "message.create", map[string]any{"channel_id": ch, "content": "now"}, &raw)
	if !strings.Contains(raw, "plain words") {
		t.Errorf("what it had not seen did not come back with the send: %s", raw)
	}

	// Its turn runs; it is called into it; someone speaks meanwhile (to nobody: the channel's
	// own agent takes only what names it); its reply is said for it.
	at := "at"
	l.want(200, "web", "PATCH", "/v1/internal/channels/"+ch+"/members/agent:"+ch, api.MemberPatch{Recv: &at}, nil)
	if code, _ := l.send("web", second, api.Send{Text: "its own work WAIT"}); code != 200 {
		t.Fatalf("send to the second: %d", code)
	}
	l.running(second)
	code, call := l.send("web", ch, api.Send{Text: "@claude2 join in", Steer: true})
	if code != 200 || call.Queued {
		t.Fatalf("a message joining its turn: %d %+v", code, call)
	}
	code, between := l.send("web", ch, api.Send{Text: "said meanwhile"})
	if code != 200 {
		t.Fatalf("send meanwhile: %d", code)
	}
	l.waitSaid("web", ch, " @claude2 ", fmt.Sprintf("ok: #%d: @claude2 join in", call.Seq))
	l.noCalls(second)
	if seen := l.seenOf(ch, second); seen >= between.Seq {
		t.Errorf("read to %d by the reply said for it; said meanwhile is %d, never given", seen, between.Seq)
	}
	code, next := l.send("web", ch, api.Send{Text: "@claude2 once more"})
	if code != 200 {
		t.Fatalf("send: %d", code)
	}
	l.waitSaid("web", second, fmt.Sprintf("ok: #%d: @claude2 once more", next.Seq), "]: said meanwhile\n")
}

// What an agent asked to be given counts only messages that are said: one refused takes
// nothing from it.
func TestChannelFollowNotSpentOnRefusal(t *testing.T) {
	t.Parallel()
	l, ch, second := two(t)
	l.want(200, "web", "PUT", "/v1/internal/channels/"+ch+"/people", api.People{People: []string{"*"}}, nil)
	l.want(200, "agent-in:"+second, "POST", "/v1/internal/channels/"+ch+"/follow", api.Follow{User: "*"}, nil)
	// A Bot with no right to Claude names it: refused.
	bot := l.newApp("bot", false).Token
	if code, _ := l.send(bot, ch, api.Send{Text: "@claude for you"}); code != 403 {
		t.Fatalf("a Bot without Claude names it: %d", code)
	}
	code, r := l.send("web", ch, api.Send{Text: "the next one said"})
	if code != 200 {
		t.Fatalf("send: %d", code)
	}
	l.waitSaid("web", second, fmt.Sprintf("ok: #%d: the next one said", r.Seq))
}

// A session cleared goes on as a new one: that one is in the channels the old was in, under
// its nick, and has the members of the old one's own channel; the old one, in the archive, is
// in none and is given nothing.
func TestChannelClearKeepsMembership(t *testing.T) {
	t.Parallel()
	l, ch, second := two(t)
	// A turn of its own, so that there is something to clear.
	if code, _ := l.send("web", second, api.Send{Text: "work"}); code != 200 {
		t.Fatalf("send to the second: %d", code)
	}
	l.waitReply("web", second, "ok: work")
	// Words of its in the channel before the clear.
	code, br := l.send("web", ch, api.Send{Text: "@claude2 before"})
	if code != 200 {
		t.Fatalf("send: %d", code)
	}
	before := fmt.Sprintf("ok: #%d: @claude2 before", br.Seq)
	l.waitSaid("web", ch, " @claude2 ", before)
	l.idle(second)
	code, r := l.send("web", second, api.Send{Text: "/clear"})
	if code != 200 || r.Session == "" || r.Session == second {
		t.Fatalf("clear: %d %+v", code, r)
	}
	next := r.Session
	// Its words from before are the member's as it is now: the new session's, under its nick.
	var page satori.BidiList[satori.Message]
	l.wantSat(200, "web", "message.list", map[string]any{"channel_id": ch, "limit": 200}, &page)
	found := false
	for _, m := range page.Data {
		if strings.Contains(m.Content, before) {
			found = true
			if m.User == nil || m.User.ID != "agent:"+next || m.User.Nick != "claude2" {
				t.Errorf("its words from before the clear by %+v, not the new session %s", m.User, next)
			}
		}
	}
	if !found {
		t.Errorf("its words from before the clear are gone")
	}
	var ms api.ChannelMembers
	l.want(200, "web", "GET", "/v1/internal/channels/"+ch+"/members", nil, &ms)
	in := map[string]string{}
	for _, m := range ms.Members {
		if m.Session != "" {
			in[m.Session] = m.Nick
		}
	}
	if in[next] != "claude2" || len(in) != 2 {
		t.Fatalf("the channel's agents after the clear: %+v (the new session %s, the old %s)", in, next, second)
	}
	if got := l.s.ch.In(second); len(got) != 0 {
		t.Errorf("the old session is still in %v", got)
	}
	if sp := l.s.spawnedOf(next); sp == nil || sp.Channel != ch {
		t.Errorf("where the new one was started: %+v", sp)
	}
	// Called by the same name, the new one answers.
	code, sr := l.send("web", ch, api.Send{Text: "@claude2 still there"})
	if code != 200 {
		t.Fatalf("send: %d", code)
	}
	l.waitSaid("web", ch, " @claude2 ", fmt.Sprintf("ok: #%d: @claude2 still there", sr.Seq))
	if p := l.page("web", second); len(p.Blocks) > 4 {
		t.Errorf("the old session was given more: %+v", p.Blocks)
	}

	// The channel's own session cleared: its members are the new one's.
	l.idle(ch)
	code, r = l.send("web", ch, api.Send{Text: "/clear"})
	if code != 200 || r.Session == "" || r.Session == ch {
		t.Fatalf("clear of the channel's own: %d %+v", code, r)
	}
	l.want(200, "web", "GET", "/v1/internal/channels/"+r.Session+"/members", nil, &ms)
	in = map[string]string{}
	for _, m := range ms.Members {
		if m.Session != "" {
			in[m.Session] = m.Nick + "/" + m.Send
		}
	}
	if in[r.Session] != "claude/direct" || in[next] != "claude2/ir" || len(in) != 2 {
		t.Errorf("the new channel's agents: %+v", in)
	}
}

// idle waits until session id's agent is in no turn.
func (l *live) idle(id string) {
	l.t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		var st api.SessionState
		if l.do("web", "GET", "/v1/internal/sessions/"+id, nil, &st); st.Status == api.StatusIdle && len(st.Queue) == 0 {
			return
		}
		if time.Now().After(deadline) {
			l.t.Fatalf("%s is not idle: %+v", id, st.Session)
		}
	}
}

// An agent whose session is in the archive is given nothing and holds no place among the
// channel's agents; shown again, it is as it was.
func TestChannelArchivedAgentPassedOver(t *testing.T) {
	t.Parallel()
	l, ch, second := two(t)
	l.wantSat(200, "web", "channel.delete", map[string]any{"channel_id": second}, nil)
	if list, _ := l.s.agentsOf(ch); len(list) != 1 || list[0].Session != ch {
		t.Fatalf("the agents messages go to: %+v", list)
	}
	code, r := l.send("web", ch, api.Send{Text: "@claude2 are you there"})
	if code != 200 {
		t.Fatalf("send: %d", code)
	}
	time.Sleep(300 * time.Millisecond)
	if p := l.page("web", second); len(p.Blocks) != 0 {
		t.Errorf("an archived agent was given: %+v", p.Blocks)
	}
	if !l.s.counts(ch, ch) || l.s.counts(ch, second) {
		t.Errorf("which count: the own %v, the archived %v", l.s.counts(ch, ch), l.s.counts(ch, second))
	}
	_ = r
}

// Two joining at once do not both find the last place; one that does not join keeps its nick
// and is not marked as started for the channel.
func TestChannelJoinFullAndRolledBack(t *testing.T) {
	t.Parallel()
	l := start(t)
	ch := l.create(200, "web", api.NewSession{Agent: api.AgentClaude, Cwd: l.work, Text: "first"})
	l.waitReply("web", ch, "ok: first")
	other := l.create(200, "web", api.NewSession{Agent: api.AgentClaude, Cwd: l.work, Text: "other"})
	l.waitReply("web", other, "ok: other")
	// Full but for one place.
	for i := range MaxAgents - 2 {
		id := fmt.Sprintf("filler-%d", i)
		if _, err := l.s.ch.Join(ch, id, "", "", func(string) string { return "filler" }, atMost(MaxAgents)); err != nil {
			t.Fatal(err)
		}
	}
	errs := make(chan error, 2)
	for _, id := range []string{"racer-a", "racer-b"} {
		go func() {
			_, err := l.s.ch.Join(ch, id, "", "", func(string) string { return "racer" }, atMost(MaxAgents))
			errs <- err
		}()
	}
	a, b := <-errs, <-errs
	if (a == nil) == (b == nil) {
		t.Fatalf("two joining for the last place: %v, %v", a, b)
	}
	if n := len(l.s.ch.Agents(ch)); n != MaxAgents {
		t.Errorf("%d agents in a channel of at most %d", n, MaxAgents)
	}
	if _, err := l.s.ch.Join(ch, "late", "", "latecomer", func(string) string { return "late" }, atMost(MaxAgents)); !errors.Is(err, channel.ErrFull) || l.s.ch.Nick("late") != "" {
		t.Errorf("joining a full channel: %v, its nick %q", err, l.s.ch.Nick("late"))
	}
	// A session there is that does not join (what it would receive is no such thing): the nick
	// asked for it is not set.
	l.want(400, "web", "POST", "/v1/internal/channels/"+other+"/members", api.NewMember{Session: ch, Nick: "newname", Recv: "bogus"}, nil)
	other = ch
	if nick := l.s.ch.Nick(other); nick == "newname" {
		t.Errorf("the nick of one that did not join: %q", nick)
	}
}

// Among several agents only a command of the channel's own agent goes to it alone: words that
// merely begin with a slash are said in the channel and reach whom they name.
func TestChannelSlashWords(t *testing.T) {
	t.Parallel()
	l, ch, second := two(t)
	code, r := l.send("web", ch, api.Send{Text: "/home/x is broken @claude2"})
	if code != 200 || !r.Said || r.Seq == 0 {
		t.Fatalf("words beginning with a slash: %d %+v", code, r)
	}
	l.waitSaid("web", second, fmt.Sprintf("ok: #%d: /home/x is broken @claude2", r.Seq))
	// A command is the own agent's still.
	if code, r = l.send("web", ch, api.Send{Text: "/compact"}); code != 200 || r.Said {
		t.Fatalf("a command: %d %+v", code, r)
	}
	for text, want := range map[string]bool{"/clear": true, "/compact now": true, "/home/x": false, "/ what": false, "/nosuchcommand": false, "plain": false} {
		if got := l.s.m.IsCommand(ch, text); got != want {
			t.Errorf("IsCommand(%q) = %v", text, got)
		}
	}
}

// A message of a channel of several agents that waits in the queue of the agent it woke is
// changed and taken back there, though the agent has it under an input of its own.
func TestChannelQueuedEdited(t *testing.T) {
	t.Parallel()
	l, ch, second := two(t)
	queue := func() []api.Queued {
		var st api.SessionState
		l.do("web", "GET", "/v1/internal/sessions/"+second, nil, &st)
		return st.Queue
	}
	if code, _ := l.send("web", second, api.Send{Text: "its own work HOLD"}); code != 200 {
		t.Fatalf("send to the second: %d", code)
	}
	l.running(second)
	code, r := l.send("web", ch, api.Send{Text: "@claude2 the first words"})
	if code != 200 || !r.Queued {
		t.Fatalf("a message for a busy agent: %d %+v", code, r)
	}
	before := l.seenOf(ch, second)
	l.wantSat(200, "web", "message.update", map[string]any{"channel_id": ch, "message_id": fmt.Sprint(r.Seq), "content": "@claude2 other words"}, nil)
	if q := queue(); len(q) != 1 || !strings.HasPrefix(q[0].Text, fmt.Sprintf("#%d: @claude2 other words\n\n<channel-context>", r.Seq)) {
		t.Fatalf("the queue after the change: %+v", q)
	}
	if all := l.said("web", ch); !strings.HasSuffix(all[len(all)-1], "@claude2 other words") {
		t.Errorf("the channel after the change: %s", all[len(all)-1])
	}
	// By the input it was sent as, too; taken back, the agent was never given it.
	l.wantSat(200, "web", "message.delete", map[string]any{"channel_id": ch, "message_id": r.Input}, nil)
	if q := queue(); len(q) != 0 {
		t.Fatalf("the queue after it was taken back: %+v", q)
	}
	l.noCalls(second)
	if seen := l.seenOf(ch, second); seen >= before || seen >= r.Seq {
		t.Errorf("its place after the message was taken back: %d (%d with it)", seen, before)
	}
	l.wantSat(422, "web", "message.delete", map[string]any{"channel_id": ch, "message_id": fmt.Sprint(r.Seq)}, nil)
	l.want(200, "web", "POST", "/v1/internal/sessions/"+second+"/interrupt", nil, nil)
}

// Two people's channel is seen by exactly the two, whatever folders anyone may see; the same
// two asking at once get the same one.
func TestDirectSeenByItsTwo(t *testing.T) {
	t.Parallel()
	l := start(t)
	bot := l.newApp("bot", true)
	// The Bot sees one folder only: not the home folder the channel is listed in.
	scope, _ := json.Marshal(api.Perms{Agents: []string{"*"}, Scope: []string{"local:" + l.work}})
	if err := l.s.st.SetAppPerms(bot.App.ID, scope); err != nil {
		t.Fatal(err)
	}
	ids := make(chan string, 8)
	for range 8 {
		go func() {
			var out struct {
				Channel string `json:"channel"`
			}
			l.asLogin(bot.Token, "", "POST", "/v1/internal/dm", map[string]string{"user": "local"}, &out)
			ids <- out.Channel
		}()
	}
	dm := <-ids
	for range 7 {
		if id := <-ids; id != dm || id == "" {
			t.Fatalf("the same two at once: %q and %q", dm, id)
		}
	}
	listed := func(as string) bool {
		var page satori.List[satori.Channel]
		l.wantSat(200, as, "channel.list", map[string]any{"guild_id": "loose"}, &page)
		for _, ss := range l.sessions(as) {
			if ss.ID == dm {
				return true
			}
		}
		return false
	}
	// Its two: the Bot, out of whose scope its folder is, and the host.
	var ms api.ChannelMembers
	if code := l.asLogin(bot.Token, "", "GET", "/v1/internal/channels/"+dm+"/members", nil, &ms); code != 200 || !ms.Joined || len(ms.Members) != 2 {
		t.Errorf("one of the two, out of its scope: %d %+v", code, ms)
	}
	if code := l.sat(bot.Token, "message.create", map[string]any{"channel_id": dm, "content": "hello"}, nil); code != 200 {
		t.Errorf("one of the two speaks: %d", code)
	}
	if !listed(bot.Token) || !listed("socket") {
		t.Errorf("listed to its two: the Bot %v, the host %v", listed(bot.Token), listed("socket"))
	}
	// Anyone else, though it sees every folder: not there.
	other := l.newApp("other", true).Token
	if code := l.asLogin(other, "", "GET", "/v1/internal/channels/"+dm+"/members", nil, nil); code != 422 {
		t.Errorf("one who is not of the two reads its members: %d", code)
	}
	if code := l.sat(other, "message.list", map[string]any{"channel_id": dm}, nil); code == 200 {
		t.Errorf("one who is not of the two reads it: %d", code)
	}
	if listed(other) {
		t.Errorf("listed to one who is not of the two")
	}
	if l.s.seenBy(caller{Identity: localIdentity}, api.Session{ID: "no-dm"}) != true || l.s.seenBy(caller{Identity: gate.Identity{App: "x", Login: "y"}}, api.Session{ID: dm}) {
		t.Errorf("seenBy")
	}
}

// What a channel gave an agent is found by the input it was given as, not by reading the
// channel out of the words: a channel whose name has a quote in it is forwarded as any.
func TestChannelRelayByInput(t *testing.T) {
	t.Parallel()
	l := start(t)
	var made satori.Channel
	l.wantSat(200, "web", "channel.create", map[string]any{"data": map[string]any{"name": `the "odd" one`}, "_data": map[string]any{"cwd": l.work}}, &made)
	ch := made.ID
	var ms api.ChannelMembers
	l.want(200, "web", "POST", "/v1/internal/channels/"+ch+"/members", api.NewMember{Agent: api.AgentClaude}, &ms)
	agent, nick := ms.Members[len(ms.Members)-1].Session, ms.Members[len(ms.Members)-1].Nick
	code, r := l.send("web", ch, api.Send{Text: "@" + nick + " look"})
	if code != 200 {
		t.Fatalf("send: %d", code)
	}
	l.waitSaid("web", agent, fmt.Sprintf("ok: #%d: @%s look", r.Seq, nick))
	var rec satori.BidiList[satori.Message]
	l.wantSat(200, "web", "internal/block.list", map[string]any{"channel_id": agent}, &rec)
	found := false
	for _, m := range rec.Data {
		found = found || strings.Contains(m.Content, `<iridium:relay channel="`+ch+`"`) && strings.Contains(m.Content, "<message forward>")
	}
	if !found {
		t.Errorf("not forwarded as the channel's: %+v", rec.Data)
	}
}

// What an agent says with ir, and what is said for it, is an agent's words in the channel
// (kind "said": told as progress, or reply when said for it at its turn's end), and is given
// to whom it names once.
func TestChannelAgentWordsAreSaid(t *testing.T) {
	t.Parallel()
	l, ch, second := two(t)
	var sent []satori.Message
	l.wantSat(200, "agent-in:"+second, "message.create", map[string]any{"channel_id": ch, "content": "over to @claude"}, &sent)
	l.waitSaid("web", ch, "@claude ", "ok: #"+sent[0].ID+": over to @claude")
	l.idle(ch)
	n := 0
	for _, b := range l.page("web", ch).Blocks {
		if b.Kind == api.BlockKindUser && strings.HasPrefix(b.Text, "#"+sent[0].ID+": ") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("what it sent was given to the one it names %d times", n)
	}
	code, r := l.send("web", ch, api.Send{Text: "@claude2 answer (no names)"})
	if code != 200 {
		t.Fatalf("send: %d", code)
	}
	l.waitSaid("web", ch, " @claude2 ", fmt.Sprintf("ok: #%d: @claude2 answer", r.Seq))
	kinds := map[string]string{}
	all, err := l.s.ch.Between(ch, 0, 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range all {
		if m.Author == "agent:"+second {
			k := m.Kind
			if m.D.Final {
				k += " final"
			}
			if m.D.Sent {
				k += " sent"
			}
			kinds[strings.SplitN(m.Text, ":", 2)[0]] = k
		}
	}
	if kinds["over to @claude"] != "said sent" || kinds["ok"] != "said final sent" {
		t.Errorf("the kinds of what the second agent said: %+v", kinds)
	}
	var page satori.BidiList[satori.Message]
	l.wantSat(200, "web", "message.list", map[string]any{"channel_id": ch, "limit": 200}, &page)
	for _, m := range page.Data {
		raw, _ := json.Marshal(m.Data)
		switch {
		case m.ID == sent[0].ID && !strings.Contains(string(raw), `"kind":"progress"`):
			t.Errorf("what it sent with ir as listed: %s", raw)
		case m.User.Nick == "claude2" && strings.HasPrefix(m.Content, "ok: ") && !strings.Contains(string(raw), `"kind":"reply"`):
			t.Errorf("what was said for it as listed: %s", raw)
		}
	}
}

// Who may change a channel's members: a person among its people, never an agent, however its
// request names itself. A nick, which is the session's everywhere, by whoever may use that
// session; a person is not taken out one by one.
func TestChannelMemberRights(t *testing.T) {
	t.Parallel()
	l, ch, second := two(t)
	l.want(200, "web", "PUT", "/v1/internal/channels/"+ch+"/people", api.People{People: []string{"*"}}, nil)
	// An agent, with or without the agent flag: neither the people, nor a member out. A nick it
	// may change: a card is any member's to change (docs/channels.md "资料").
	nick := "renamed"
	for _, as := range []string{"agent-in:" + second, "ir-in:" + second} {
		l.want(403, as, "PUT", "/v1/internal/channels/"+ch+"/people", api.People{People: []string{"*"}}, nil)
		l.want(403, as, "DELETE", "/v1/internal/channels/"+ch+"/members/agent:"+second, nil, nil)
		l.want(200, as, "PATCH", "/v1/internal/channels/"+ch+"/members/agent:"+second, api.MemberPatch{Nick: &nick}, nil)
	}
	// A Bot with no right to Claude is one of the people, and may not rename a Claude session.
	bot := l.newApp("bot", false).Token
	other := "bots"
	if code := l.asLogin(bot, "", "PATCH", "/v1/internal/channels/"+ch+"/members/agent:"+second, api.MemberPatch{Nick: &other}, nil); code != 403 {
		t.Errorf("a nick set by one with no right to the session: %d", code)
	}
	if got := l.s.ch.Nick(second); got != "renamed" {
		t.Errorf("its nick: %q", got)
	}
	l.want(200, "web", "PATCH", "/v1/internal/channels/"+ch+"/members/agent:"+second, api.MemberPatch{Nick: &nick}, nil)
	// People are not removed one by one.
	l.want(400, "web", "DELETE", "/v1/internal/channels/"+ch+"/members/*", nil, nil)
	if !l.s.ch.Has(ch, "anyone", "at all") {
		t.Errorf("everyone was taken out")
	}
}

// A session that is its agent's alone is read to its end by that agent, whatever the agent
// was set to receive: everything said there is sent to it.
func TestReadsOfOneAgent(t *testing.T) {
	t.Parallel()
	l := start(t)
	ch := l.create(200, "web", api.NewSession{Agent: api.AgentClaude, Cwd: l.work, Text: "first"})
	l.waitReply("web", ch, "ok: first")
	at := "at"
	l.want(200, "web", "PATCH", "/v1/internal/channels/"+ch+"/members/agent:"+ch, api.MemberPatch{Recv: &at}, nil)
	if code, _ := l.send("web", ch, api.Send{Text: "more"}); code != 200 {
		t.Fatalf("send: %d", code)
	}
	l.waitReply("web", ch, "ok: more")
	var reads api.ChannelReads
	l.want(200, "web", "GET", "/v1/internal/channels/"+ch+"/reads", nil, &reads)
	if len(reads.Readers) != 1 || reads.Readers[0].Seq != reads.End || reads.End < 4 {
		t.Errorf("reads: %+v", reads)
	}
}
