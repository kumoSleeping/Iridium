package server

import (
	"strings"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/store"
)

func waitSessionStatus(t *testing.T, l *live, id string, want api.Status) {
	t.Helper()
	for d := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		var st api.SessionState
		l.want(200, "web", "GET", "/v1/internal/sessions/"+id, nil, &st)
		if st.Status == want {
			return
		}
		if time.Now().After(d) {
			t.Fatalf("session %s status=%s, want %s", id, st.Status, want)
		}
	}
}

// A running agent in a multi-agent channel overhears unmentioned human messages as marked
// <channel-overhear> excerpts (attachments by filename only), while idle agents are never
// woken and already-woken agents do not receive a duplicate overhear.
func TestChannelRunningOverhear(t *testing.T) {
	t.Parallel()
	l := start(t)

	ch := l.create(200, "web", api.NewSession{Agent: api.AgentClaude, Cwd: l.work, Text: "hello"})
	l.waitReply("web", ch, "ok: hello")

	var ms api.ChannelMembers
	l.want(200, "web", "POST", "/v1/internal/channels/"+ch+"/members", api.NewMember{Agent: api.AgentClaude, Nick: "worker"}, &ms)
	l.want(200, "web", "POST", "/v1/internal/channels/"+ch+"/members", api.NewMember{Agent: api.AgentClaude, Nick: "sleeper"}, &ms)
	var worker, sleeper string
	for _, m := range ms.Members {
		switch m.Nick {
		case "worker":
			worker = m.Session
		case "sleeper":
			sleeper = m.Session
		}
	}
	if worker == "" || sleeper == "" {
		t.Fatalf("missing sessions: %+v", ms.Members)
	}

	// Start worker on a HOLD turn so it stays in agent.Running until a steered message arrives.
	if code, r := l.send("web", ch, api.Send{Text: "@worker HOLD"}); code != 200 || r.Seq == 0 {
		t.Fatalf("start worker: %d %+v", code, r)
	}
	waitSessionStatus(t, l, worker, api.StatusRunning)

	// Human sends an unmentioned message with an attachment path: worker overhears it as
	// <channel-overhear> (filename only); idle sleeper stays idle and gets nothing; direct agent
	// gets normal delivery, not a duplicate overhear.
	code, r := l.send("web", ch, api.Send{Text: "also fix hover\n[附件] `/tmp/secret/shot.png`"})
	if code != 200 || r.Seq == 0 {
		t.Fatalf("send unmentioned: %d %+v", code, r)
	}

	injs, err := l.s.st.Injections(worker, 10)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, in := range injs {
		if in.Seq == r.Seq && in.Channel == ch && strings.Contains(in.Record, "channel.overhear") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("worker did not record overhear injection for seq %d: %+v", r.Seq, injs)
	}

	// Since nothing unseen lay between worker's start and r.Seq, worker's Seen advanced to r.Seq.
	var reads api.ChannelReads
	l.want(200, "web", "GET", "/v1/internal/channels/"+ch+"/reads", nil, &reads)
	var workerSeen int64
	for _, rd := range reads.Readers {
		if rd.Member == store.AgentMember(worker) {
			workerSeen = rd.Seq
		}
	}
	if workerSeen < r.Seq {
		t.Errorf("worker seen=%d, want >= %d", workerSeen, r.Seq)
	}

	// Idle sleeper must NOT be woken and must get no injection for r.Seq.
	var sleeperSt api.SessionState
	l.want(200, "web", "GET", "/v1/internal/sessions/"+sleeper, nil, &sleeperSt)
	if sleeperSt.Status == api.StatusRunning {
		t.Fatalf("idle sleeper was unexpectedly woken: %+v", sleeperSt)
	}
	sinjs, err := l.s.st.Injections(sleeper, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range sinjs {
		if in.Seq == r.Seq {
			t.Fatalf("idle sleeper unexpectedly got injection for seq %d: %+v", r.Seq, in)
		}
	}

	// Direct agent (already woken by the unmentioned message) must NOT get a duplicate overhear.
	directInjs, err := l.s.st.Injections(ch, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range directInjs {
		if in.Seq == r.Seq && strings.Contains(in.Record, "channel.overhear") {
			t.Fatalf("woken direct agent also got overhear injection: %+v", in)
		}
	}

	// Verify the exact overheard text worker actually received (fake Claude echoes steered text
	// back; Satori XML-escapes < and > in message.list): filename only, no raw path.
	msgs := l.waitSaid("web", ch, "@worker", "channel-overhear", "also fix hover", "shot.png")
	for _, m := range msgs {
		if strings.Contains(m, "@worker") && strings.Contains(m, "channel-overhear") && strings.Contains(m, "/tmp/secret/shot.png") {
			t.Errorf("overhear leaked raw attachment path: %s", m)
		}
	}
}

// Human messages explicitly @ing another agent must NOT be overheard by a running agent.
func TestChannelOverhearSkipsMentioned(t *testing.T) {
	t.Parallel()
	l := start(t)

	ch := l.create(200, "web", api.NewSession{Agent: api.AgentClaude, Cwd: l.work, Text: "hello"})
	l.waitReply("web", ch, "ok: hello")

	var ms api.ChannelMembers
	l.want(200, "web", "POST", "/v1/internal/channels/"+ch+"/members", api.NewMember{Agent: api.AgentClaude, Nick: "worker"}, &ms)
	l.want(200, "web", "POST", "/v1/internal/channels/"+ch+"/members", api.NewMember{Agent: api.AgentClaude, Nick: "other"}, &ms)
	var worker string
	for _, m := range ms.Members {
		if m.Nick == "worker" {
			worker = m.Session
		}
	}
	if worker == "" {
		t.Fatalf("missing worker: %+v", ms.Members)
	}

	if code, r := l.send("web", ch, api.Send{Text: "@worker HOLD"}); code != 200 || r.Seq == 0 {
		t.Fatalf("start worker: %d %+v", code, r)
	}
	waitSessionStatus(t, l, worker, api.StatusRunning)

	code, r := l.send("web", ch, api.Send{Text: "@other do something else"})
	if code != 200 || r.Seq == 0 {
		t.Fatalf("send @other: %d %+v", code, r)
	}
	injs, err := l.s.st.Injections(worker, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range injs {
		if in.Seq == r.Seq {
			t.Fatalf("worker overheard message addressing @other: %+v", in)
		}
	}
}
