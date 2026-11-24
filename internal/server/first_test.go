package server

import (
	"encoding/json"
	"testing"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/satori"
)

// A channel's first screen in one answer: its state, members, reads, newest log, other agents'
// states and the work under all agents' words; ?record=1 returns the session's raw transcript.
func TestFirstScreen(t *testing.T) {
	t.Parallel()
	l := start(t)
	ch := l.create(200, "web", api.NewSession{Agent: api.AgentClaude, Cwd: l.work, Text: "first"})
	l.waitReply("web", ch, "ok: first")

	type first struct {
		Session  api.SessionState                 `json:"session"`
		Page     *satori.BidiList[satori.Message] `json:"page"`
		Members  *api.ChannelMembers              `json:"members"`
		Reads    *api.ChannelReads                `json:"reads"`
		Log      *satori.BidiList[satori.Message] `json:"log"`
		Sessions map[string]api.SessionState      `json:"sessions"`
		Work     []struct {
			Session string            `json:"session"`
			Data    []json.RawMessage `json:"data"`
		} `json:"work"`
	}
	var one first
	l.want(200, "web", "GET", "/v1/internal/sessions/"+ch+"/first", nil, &one)
	var log1 satori.BidiList[satori.Message]
	l.wantSat(200, "web", "message.list", map[string]any{"channel_id": ch}, &log1)
	if one.Session.ID != ch || one.Page != nil ||
		one.Members == nil || one.Members.Channel != ch || !one.Members.Joined ||
		one.Reads == nil || one.Reads.Channel != ch ||
		one.Log == nil || len(one.Log.Data) == 0 || len(one.Log.Data) != len(log1.Data) {
		t.Fatalf("one agent channel: %+v", one)
	}
	if len(one.Sessions) != 0 {
		t.Errorf("one agent, extra sessions: %+v", one.Sessions)
	}
	// ?record=1 returns the raw transcript page (TranscriptView).
	var recFirst first
	l.want(200, "web", "GET", "/v1/internal/sessions/"+ch+"/first?record=1", nil, &recFirst)
	var rec satori.BidiList[satori.Message]
	l.wantSat(200, "web", "internal/block.list", map[string]any{"channel_id": ch}, &rec)
	if recFirst.Session.ID != ch || recFirst.Page == nil || len(recFirst.Page.Data) != len(rec.Data) || recFirst.Log != nil {
		t.Fatalf("record view: %+v", recFirst)
	}
	l.want(422, "web", "GET", "/v1/internal/sessions/none/first", nil, nil)

	// Another agent, called and answering: the log, the reads, its state, its work.
	var ms api.ChannelMembers
	l.want(200, "web", "POST", "/v1/internal/channels/"+ch+"/members", api.NewMember{Agent: api.AgentClaude}, &ms)
	second := ""
	for _, m := range ms.Members {
		if m.Session != "" && m.Session != ch {
			second = m.Session
		}
	}
	if second == "" {
		t.Fatalf("members: %+v", ms)
	}
	var two first
	l.want(200, "web", "GET", "/v1/internal/sessions/"+ch+"/first", nil, &two)
	var log satori.BidiList[satori.Message]
	l.wantSat(200, "web", "message.list", map[string]any{"channel_id": ch}, &log)
	if two.Log == nil || len(two.Log.Data) == 0 || len(two.Log.Data) != len(log.Data) || two.Reads == nil || two.Reads.Channel != ch || two.Sessions[second].ID != second || two.Page != nil {
		t.Fatalf("two agents: %+v", two)
	}

	// Bare channel with one agent: /first brings its log & agent state, and /clear replaces the
	// sole agent in place while keeping the channel and its history.
	bare := l.create(200, "web", api.NewSession{
		Cwd: l.work,
		Members: &api.ChannelSetup{
			Agents: []api.NewMember{{Agent: api.AgentClaude, Nick: "solo", On: onFrom("*")}},
		},
	})
	var bareMs api.ChannelMembers
	l.want(200, "web", "GET", "/v1/internal/channels/"+bare+"/members", nil, &bareMs)
	var solo string
	for _, m := range bareMs.Members {
		if m.Session != "" {
			solo = m.Session
		}
	}
	if solo == "" || solo == bare {
		t.Fatalf("bare channel agent: %+v", bareMs)
	}
	if code, _ := l.send("web", bare, api.Send{Text: "hello bare"}); code != 200 {
		t.Fatalf("send to bare: %d", code)
	}
	l.waitSaid("web", bare, "@solo", "hello bare")
	l.idle(solo)
	var bareFirst first
	l.want(200, "web", "GET", "/v1/internal/sessions/"+bare+"/first", nil, &bareFirst)
	if bareFirst.Session.ID != bare || bareFirst.Log == nil || len(bareFirst.Log.Data) < 2 || bareFirst.Sessions[solo].ID != solo {
		t.Fatalf("bare first: %+v", bareFirst)
	}
	code, cr := l.send("web", bare, api.Send{Text: "/clear"})
	if code != 200 || cr.Session != bare {
		t.Fatalf("clear bare solo: %d %+v", code, cr)
	}
	l.want(200, "web", "GET", "/v1/internal/channels/"+bare+"/members", nil, &bareMs)
	var nextSolo string
	for _, m := range bareMs.Members {
		if m.Session != "" {
			nextSolo = m.Session
		}
	}
	if nextSolo == "" || nextSolo == solo || nextSolo == bare {
		t.Fatalf("bare agent not replaced in place: old=%s new=%s members=%+v", solo, nextSolo, bareMs)
	}
	// History stays in the channel and the replacement agent answers under the same nick.
	var bareLog satori.BidiList[satori.Message]
	l.wantSat(200, "web", "message.list", map[string]any{"channel_id": bare}, &bareLog)
	if len(bareLog.Data) < 2 {
		t.Fatalf("bare history lost: %d", len(bareLog.Data))
	}
	if code, _ := l.send("web", bare, api.Send{Text: "after clear"}); code != 200 {
		t.Fatalf("send after clear: %d", code)
	}
	l.waitSaid("web", bare, "@solo", "after clear")
}

func TestWorkAt(t *testing.T) {
	bs := []api.Block{{At: "1.0", Kind: "user"}, {At: "2.0", Kind: "thinking"}, {At: "3.0", Kind: "tool"}, {At: "4.0", Kind: "assistant", Text: "said"}, {At: "5.0", Kind: "tool"}, {At: "6.0", Kind: "assistant", Text: "last"}}
	if got := workAt(bs, "6.0", nil, 0); len(got) != 1 || got[0] != 4 {
		t.Errorf("before a block: %v", got)
	}
	if got := workAt(bs, "4.0", nil, 0); len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Errorf("before a block, to the input: %v", got)
	}
	if got := workAt(bs, "", new(int64(1)), 5); len(got) != 3 || got[0] != 1 || got[2] != 3 {
		t.Errorf("lines from..to: %v", got)
	}
	// A session called first, its record empty then: its stretch begins at line 0.
	if got := workAt(bs, "", new(int64(0)), 5); len(got) != 3 || got[0] != 1 || got[2] != 3 {
		t.Errorf("lines 0..to: %v", got)
	}
	if got := workAt(bs, "", new(int64(2)), 0); got != nil {
		t.Errorf("no place: %v", got)
	}
}
