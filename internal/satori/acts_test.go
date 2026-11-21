package satori

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/channel"
)

// actsFake keeps the channels' rules as the server does: u1 is one of the channel's people, u2
// reads only.
type actsFake struct {
	*fake
	mu       sync.Mutex
	recalled []int64
	reacted  []string
}

func (f *actsFake) Actor(c Caller, ch string) (User, error) {
	name := c.Login
	for _, l := range f.Logins(c.App) {
		if l.ID == c.Login {
			name = l.Name
		}
	}
	if c.Login == "u2" {
		return User{ID: c.Login, Name: name}, refuse(403, "no_permission", "reads only")
	}
	return User{ID: c.Login, Name: name}, nil
}

func (f *actsFake) Recalled(_ Caller, m channel.Message) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recalled = append(f.recalled, m.Seq)
}

func (f *actsFake) Reacted(_ Caller, who User, ch string, seq int64, emoji string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reacted = append(f.reacted, who.ID+" "+emoji)
}

func newActsRig(t *testing.T) (*rig, *actsFake) {
	f := &actsFake{fake: newFake()}
	s := New(f, t.Logf)
	logFor(t, s, f.fake)
	srv := httptest.NewServer(s)
	t.Cleanup(func() { srv.Close(); s.Close() })
	return &rig{t: t, f: f.fake, s: s, srv: srv}, f
}

// eventOf reads events till one of type typ (for login u1).
func (w *ws) eventOf(typ string) Event {
	w.t.Helper()
	for range 20 {
		var ev Event
		w.want(OpEvent, &ev)
		if ev.Type == typ && ev.Login != nil && ev.Login.User.ID == "u1" {
			return ev
		}
	}
	w.t.Fatalf("no %s", typ)
	return Event{}
}

// A message taken back (message.delete by its number): gone from the log and the record for
// everyone, a line of Iridium's own in its place, message-deleted told with what it was; one
// who only reads may not; an ask or Iridium's line is not taken back.
func TestRecall(t *testing.T) {
	r, f := newActsRig(t)
	r.f.addSession("s1", 0)
	ss := r.f.sessions["s1"].Session
	one := r.s.Input(ss, "in-1", "oops", User{ID: "u1", Name: "Alice"}, "app1")
	two := r.s.Input(ss, "in-2", "fine", User{ID: "u1", Name: "Alice"}, "app1")
	if one != 1 || two != 2 {
		t.Fatalf("numbers %d %d", one, two)
	}
	// The agent's record has them as it was given them.
	r.f.addBlock("s1", api.Block{Kind: api.BlockKindUser, ID: "in-1", Text: "oops"})
	r.f.addBlock("s1", api.Block{Kind: api.BlockKindUser, ID: "in-2", Text: "fine"})
	w := r.dial(nil)
	w.send(OpIdentify, Identify{Token: "ir_good"})
	w.want(OpReady, nil)

	if code, e := r.as("u2", "message.delete", map[string]any{"channel_id": "s1", "message_id": "1"}, nil); code != 403 {
		t.Errorf("one who reads: %d %+v", code, e)
	}
	if code, e := r.as("u1", "message.delete", map[string]any{"channel_id": "s1", "message_id": "1"}, nil); code != 200 {
		t.Fatalf("recall: %d %+v", code, e)
	}
	del := w.eventOf("message-deleted")
	raw, _ := json.Marshal(del)
	if del.Message.ID != "1" || del.User.ID != "u1" || del.Operator.ID != "u1" || !strings.Contains(string(raw), `"input":"in-1"`) {
		t.Errorf("message-deleted: %s", raw)
	}
	note := w.eventOf("message-created")
	if note.XData.Kind != "note" || note.Message.Content != "Alice 撤回了一条消息" {
		t.Errorf("the note: %+v %+v", note.Message, note.XData)
	}
	var page BidiList[json.RawMessage]
	r.as("u1", "message.list", map[string]any{"channel_id": "s1"}, &page)
	all := ""
	for _, m := range page.Data {
		all += string(m) + "\n"
	}
	if strings.Contains(all, "oops") || !strings.Contains(all, `"event":{"type":"recall","member":"u1","seq":1,"by":"Alice"}`) || strings.Count(all, `"content":`) != 2 {
		t.Errorf("the log: %s", all)
	}
	var blocks BidiList[Message]
	r.as("u1", "internal/block.list", map[string]any{"channel_id": "s1"}, &blocks)
	if len(blocks.Data) != 1 || !strings.Contains(blocks.Data[0].Content, "fine") {
		t.Errorf("the record: %+v", blocks.Data)
	}
	if code, _ := r.as("u1", "message.delete", map[string]any{"channel_id": "s1", "message_id": "1"}, nil); code != 422 {
		t.Errorf("again: %d", code)
	}
	if code, _ := r.as("u1", "message.delete", map[string]any{"channel_id": "s1", "message_id": "3"}, nil); code != 422 {
		t.Errorf("the note: %d", code)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.recalled) != 1 || f.recalled[0] != 1 {
		t.Errorf("recalled: %v", f.recalled)
	}
}

// Reactions (reaction.*): Satori's events, each message's in _data.reactions, who put one on; one
// emoji each; who reads only may not.
func TestReactions(t *testing.T) {
	r, f := newActsRig(t)
	r.f.addSession("s1", 0)
	ss := r.f.sessions["s1"].Session
	r.s.Input(ss, "in-1", "ship it", User{ID: "u1", Name: "Alice"}, "app1")
	w := r.dial(nil)
	w.send(OpIdentify, Identify{Token: "ir_good"})
	w.want(OpReady, nil)

	react := func(path, login string, body map[string]any) int {
		body["channel_id"] = "s1"
		code, _ := r.as(login, path, body, nil)
		return code
	}
	for _, c := range []struct {
		body map[string]any
		code int
	}{
		{map[string]any{"message_id": "1", "emoji_id": "👍"}, 200},
		{map[string]any{"message_id": "1", "emoji": "🎉"}, 200},
		{map[string]any{"message_id": "1", "emoji": "👍🏽"}, 200},
		{map[string]any{"message_id": "1", "emoji": "hi"}, 400},
		{map[string]any{"message_id": "1", "emoji": "👍👍"}, 400},
		{map[string]any{"message_id": "9", "emoji": "👍"}, 422},
	} {
		if got := react("reaction.create", "u1", c.body); got != c.code {
			t.Errorf("%v: %d, want %d", c.body, got, c.code)
		}
	}
	if got := react("reaction.create", "u2", map[string]any{"message_id": "1", "emoji": "👍"}); got != 403 {
		t.Errorf("one who reads: %d", got)
	}
	added := w.eventOf("reaction-added")
	if added.Emoji == nil || added.Emoji.ID != "👍" || added.Message.ID != "1" || added.User.ID != "u1" || added.Channel.ID != "s1" {
		t.Errorf("reaction-added: %+v %+v", added, added.Emoji)
	}
	var m struct {
		Data struct {
			Reactions []ReactionSum `json:"reactions"`
		} `json:"_data"`
	}
	r.as("u1", "message.get", map[string]any{"channel_id": "s1", "message_id": "1"}, &m)
	if rs := m.Data.Reactions; len(rs) != 3 || rs[0].Emoji != "👍" || rs[0].Count != 1 || !rs[0].Me || rs[0].Users[0].Name != "Alice" {
		t.Errorf("reactions: %+v", rs)
	}
	var who List[User]
	r.as("u1", "reaction.list", map[string]any{"channel_id": "s1", "message_id": "1", "emoji_id": "👍"}, &who)
	if len(who.Data) != 1 || who.Data[0].ID != "u1" {
		t.Errorf("who: %+v", who)
	}
	if got := react("reaction.delete", "u1", map[string]any{"message_id": "1", "emoji": "👍"}); got != 200 {
		t.Errorf("delete: %d", got)
	}
	if removed := w.eventOf("reaction-removed"); removed.Emoji.ID != "👍" || removed.User.ID != "u1" {
		t.Errorf("reaction-removed: %+v", removed)
	}
	if got := react("reaction.clear", "u1", map[string]any{"message_id": "1"}); got != 200 {
		t.Errorf("clear: %d", got)
	}
	r.as("u1", "message.get", map[string]any{"channel_id": "s1", "message_id": "1"}, &m)
	m.Data.Reactions = nil
	r.as("u1", "message.get", map[string]any{"channel_id": "s1", "message_id": "1"}, &m)
	if len(m.Data.Reactions) != 0 {
		t.Errorf("cleared: %+v", m.Data.Reactions)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if strings.Join(f.reacted, ",") != "u1 👍,u1 🎉,u1 👍🏽" {
		t.Errorf("given: %q", f.reacted)
	}
}

// One emoji: a pictograph with what may go with it, a flag, a keycap; not words, nor two.
func TestEmoji(t *testing.T) {
	for e, ok := range map[string]bool{"👍": true, "❤️": true, "👍🏽": true, "👨‍👩‍👧": true, "🇨🇳": true, "1️⃣": true, "✅": true, " 🎉 ": true,
		"": false, "a": false, "1": false, "👍👍": false, "👍 ok": false, "‍👍": false, "🇨": false, "️": false} {
		if _, err := channel.Emoji(e); (err == nil) != ok {
			t.Errorf("%q: %v", e, err)
		}
	}
}
