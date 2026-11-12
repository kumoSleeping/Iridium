package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/voice"
)

// A channel's voice: the page joins its session's room; who is in it is listed and carried as
// the state "voice", gone as it leaves. An agent's ir may not join; a session not there has none.
func TestVoice(t *testing.T) {
	l := start(t)
	id := l.create(200, "web", api.NewSession{Agent: api.AgentClaude, Cwd: l.work})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	url := strings.Replace(l.base, "http", "ws", 1) + "/v1/internal/sessions/"
	if _, resp, err := websocket.Dial(ctx, url+"nope/voice", nil); err == nil || resp == nil || resp.StatusCode < 400 {
		t.Fatalf("a session not there has a room: %v", err)
	}
	if code := l.do("agent", "GET", "/v1/internal/sessions/"+id+"/voice", nil, nil); code != 403 {
		t.Fatalf("an agent's ir in voice: %d", code)
	}
	ws, _, err := websocket.Dial(ctx, url+id+"/voice", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	_, b, err := ws.Read(ctx)
	var hello struct {
		Op      string
		Self    int
		Members []voice.Member
	}
	if err != nil || json.Unmarshal(b, &hello) != nil || hello.Op != "hello" || hello.Self != 1 || len(hello.Members) != 1 || hello.Members[0].Name == "" {
		t.Fatalf("hello %s %v", b, err)
	}
	var members []voice.Member
	l.want(200, "web", "GET", "/v1/internal/sessions/"+id+"/voice/members", nil, &members)
	if len(members) != 1 {
		t.Fatalf("members %+v", members)
	}
	carried := func() map[string][]voice.Member {
		var m map[string][]voice.Member
		json.Unmarshal(l.s.hub.Get("voice"), &m)
		return m
	}
	if m := carried(); len(m[id]) != 1 {
		t.Fatalf("carried %+v", m)
	}
	ws.Close(websocket.StatusNormalClosure, "")
	for i := 0; len(carried()) != 0; i++ {
		if i > 200 {
			t.Fatalf("the room stayed carried: %+v", carried())
		}
		time.Sleep(10 * time.Millisecond)
	}
}
