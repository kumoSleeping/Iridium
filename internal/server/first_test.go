package server

import (
	"encoding/json"
	"testing"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/satori"
)

// A session's first screen in one answer: what its own APIs say, each part as they give it; the
// channel's side only once it has another agent.
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
	var rec satori.BidiList[satori.Message]
	l.wantSat(200, "web", "internal/block.list", map[string]any{"channel_id": ch}, &rec)
	if one.Session.ID != ch || one.Page == nil || len(one.Page.Data) == 0 || len(one.Page.Data) != len(rec.Data) || one.Page.XData == nil || one.Page.XData.End != rec.XData.End ||
		one.Members == nil || one.Members.Channel != ch || !one.Members.Joined {
		t.Fatalf("one agent: %+v", one)
	}
	if one.Log != nil || one.Reads != nil || len(one.Sessions) != 0 || len(one.Work) != 0 {
		t.Errorf("one agent, the channel's side: %+v", one)
	}
	l.want(422, "web", "GET", "/v1/internal/sessions/none/first", nil, nil)

	// Another agent, called and answering with ir: the log, the reads, its state, its work.
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
	if two.Log == nil || len(two.Log.Data) == 0 || len(two.Log.Data) != len(log.Data) || two.Reads == nil || two.Reads.Channel != ch || two.Sessions[second].ID != second || two.Page == nil {
		t.Fatalf("two agents: %+v", two)
	}
	// A page that has the record asks for the rest alone.
	var rest first
	l.want(200, "web", "GET", "/v1/internal/sessions/"+ch+"/first?record=0&since=0", nil, &rest)
	if rest.Page != nil || rest.Log == nil || len(rest.Log.Data) != len(log.Data) || rest.Session.ID != ch {
		t.Errorf("without the record: %+v", rest)
	}
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
