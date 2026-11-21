package server

import (
	"fmt"
	"strings"
	"testing"

	"github.com/kumoSleeping/iridium/internal/api"
)

// Reactions wake the agents that receive their author (docs/channels.md "表情"): a session alone
// in its channel gets the line alone; with another agent, one subscribed to the reacting agent
// gets the line and the channel's context, the reacting agent nothing. A message taken back is
// gone for everyone, and the agent it was given is told its number the next time it is called.
func TestReactionsAndRecall(t *testing.T) {
	t.Parallel()
	l := start(t)
	ch := l.create(200, "web", api.NewSession{Agent: api.AgentClaude, Cwd: l.work, Text: "first"})
	l.waitReply("web", ch, "ok: first")

	l.wantSat(200, "web", "reaction.create", map[string]any{"channel_id": ch, "message_id": "1", "emoji": "👍"}, nil)
	for _, m := range l.waitSaid("web", ch, "ok: #1 [", "reacted 👍 to: first]") {
		if strings.Contains(m, "reacted") && strings.Contains(m, "channel-context") {
			t.Errorf("alone, given a channel's context: %s", m)
		}
	}

	var ms api.ChannelMembers
	l.want(200, "web", "POST", "/v1/internal/channels/"+ch+"/members", api.NewMember{Agent: api.AgentClaude, Nick: "guest"}, &ms)
	guest := ms.Members[len(ms.Members)-1].Session
	l.want(200, "web", "PATCH", "/v1/internal/channels/"+ch+"/members/agent:"+guest, map[string]any{"recv": []string{"agent:" + ch}}, nil)
	l.wantSat(200, "agent-in:"+ch, "reaction.create", map[string]any{"channel_id": ch, "message_id": "1", "emoji": "🎉"}, nil)
	l.waitSaid("web", ch, "@guest ", "reacted 🎉 to: first]", "channel-context")

	_, sr := l.send("web", ch, api.Send{Text: "secret plan"})
	l.waitSaid("web", ch, "@claude ", fmt.Sprintf("ok: #%d: secret plan", sr.Seq))
	l.wantSat(200, "web", "message.delete", map[string]any{"channel_id": ch, "message_id": fmt.Sprint(sr.Seq)}, nil)
	all := l.waitSaid("web", ch, "iridium", "撤回了一条消息")
	for _, m := range all {
		if strings.HasPrefix(m, fmt.Sprintf("%d ", sr.Seq)) {
			t.Errorf("still there: %s", m)
		}
	}
	l.wantSat(422, "web", "reaction.create", map[string]any{"channel_id": ch, "message_id": fmt.Sprint(sr.Seq), "emoji": "👍"}, nil)
	_, next := l.send("web", ch, api.Send{Text: "and now"})
	l.waitSaid("web", ch, "@claude ", fmt.Sprintf("ok: #%d: and now", next.Seq), fmt.Sprintf("gone for everyone: #%d.", sr.Seq))
}
