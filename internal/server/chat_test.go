package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/chat"
	"github.com/kumoSleeping/iridium/internal/config"
	"github.com/kumoSleeping/iridium/internal/store"
)

// A plugin's attachments: an upload into the chat's session (opened for it), then named in the
// input, written into the message as the web's are.
func TestChatAttachments(t *testing.T) {
	l := startWith(t, func(s *Server, cfg config.Config, work string) {
		var err error
		s.Chat, err = chat.New(chat.Options{Manager: s.m, Store: s.st, StateDir: cfg.StateDir, ChatDir: work, Logf: t.Logf, Guard: s.ChatGuard(), Message: s.Files().Message})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(s.Chat.Close)
	})
	var tok api.Token
	tok = l.newApp("bot", true)
	bot := tok.Token
	var u api.Upload
	// No session chosen: no upload, and a message gets the sessions to pick from, not sent.
	l.want(409, bot, "POST", "/api/chat/qq:1/uploads", api.NewUpload{Name: "a.txt", Size: 3}, nil)
	var none api.ChatInputReply
	l.want(200, bot, "POST", "/api/chat/qq:1/input", api.ChatInput{Text: "hi"}, &none)
	if none.Session != "" || none.Card == nil {
		t.Fatalf("no session: %+v", none)
	}
	l.want(200, bot, "POST", "/api/chat/qq:1/input", api.ChatInput{Text: "/new claude"}, nil)
	l.want(200, bot, "POST", "/api/chat/qq:1/uploads", api.NewUpload{Name: "a.txt", Size: 3}, &u)
	if u.Session == "" {
		t.Fatalf("upload: %+v", u)
	}
	req, _ := http.NewRequest("PUT", l.base+"/api/uploads/"+u.ID+"?offset=0", strings.NewReader("abc"))
	req.Header.Set("Authorization", "Bearer "+bot)
	res, err := l.web.Do(req)
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("chunk: %v %v", res, err)
	}
	res.Body.Close()
	sum := sha256.Sum256([]byte("abc"))
	l.want(200, bot, "POST", "/api/uploads/"+u.ID+"/complete", api.CompleteUpload{Sha256: hex.EncodeToString(sum[:])}, &u)
	if u.File == nil {
		t.Fatalf("complete: %+v", u)
	}
	var in api.ChatInputReply
	l.want(200, bot, "POST", "/api/chat/qq:1/input", api.ChatInput{Text: "see", Attachments: []string{u.File.Ref}}, &in)
	if in.Session != u.Session {
		t.Fatalf("another session: %s, the upload's %s", in.Session, u.Session)
	}
	var page api.MessagePage
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		l.want(200, bot, "GET", "/api/sessions/"+in.Session+"/messages", nil, &page)
		if len(page.Blocks) > 0 || time.Now().After(deadline) {
			break
		}
	}
	if len(page.Blocks) == 0 || !strings.Contains(page.Blocks[0].Text, "[附件] `") || !strings.Contains(page.Blocks[0].Text, "a.txt`") {
		t.Fatalf("the message: %+v", page.Blocks)
	}
	// Attachments alone are a message too; a ref of no upload is refused.
	l.want(200, bot, "POST", "/api/chat/qq:1/input", api.ChatInput{Attachments: []string{u.File.Ref}}, nil)
	l.want(400, bot, "POST", "/api/chat/qq:1/input", api.ChatInput{Text: "x", Attachments: []string{"nope"}}, nil)
}

// ir bot, and the asks of a session no chat points at: they reach the user through the bot's
// chat; a letter is 进入会话 (with the last rounds) or 关闭.
func TestBotLetter(t *testing.T) {
	var srv *Server
	l := startWith(t, func(s *Server, cfg config.Config, work string) {
		srv = s
		var err error
		s.Chat, err = chat.New(chat.Options{Manager: s.m, Store: s.st, StateDir: cfg.StateDir, ChatDir: work, Logf: t.Logf, Guard: s.ChatGuard(), Terminal: s.HasTerminal})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(s.Chat.Close)
	})
	var tok api.Token
	tok = l.newApp("QQ", true)
	bot, botID := tok.Token, tok.App.ID
	// None has a chat yet: no bot to reach.
	var b api.NewSessionReply
	l.want(200, "web", "POST", "/api/sessions", api.NewSession{Agent: api.AgentClaude, Cwd: l.work, Text: "first"}, &b)
	l.waitReply("web", b.ID, "ok: first")
	l.want(404, "socket", "POST", "/api/bot", api.BotLetter{Text: "x", Caller: b.ID}, nil)

	var a api.ChatInputReply
	l.want(200, bot, "POST", "/api/chat/qq:1/input", api.ChatInput{Text: "/new claude"}, &a)
	var last int64
	events := func() []store.Outgoing {
		t.Helper()
		out, err := srv.st.OutboxAfter(botID, last)
		if err != nil {
			t.Fatal(err)
		}
		if len(out) > 0 {
			last = out[len(out)-1].Seq
		}
		return out
	}
	events()
	// What a letter or a reply makes, without the status lamps' events.
	took := func() []store.Outgoing {
		t.Helper()
		return slices.DeleteFunc(events(), func(x store.Outgoing) bool { return x.Type == "status" })
	}

	// B, pointed at by none: a letter.
	var r api.BotReply
	l.want(200, "socket", "POST", "/api/bot", api.BotLetter{Text: "跑完了", Caller: b.ID}, &r)
	if r.Bot != botID || r.Name != "QQ" || r.Chat != "qq:1" || !r.Letter {
		t.Fatalf("letter: %+v", r)
	}
	ev := took()
	var e api.ChatEvent
	if len(ev) != 1 || ev[0].Type != "card" || json.Unmarshal([]byte(ev[0].Data), &e) != nil || e.Card == nil ||
		e.Card.Summary[0] != "跑完了" || e.Card.Options[0].ID != b.ID || e.Card.Options[0].Text != "进入会话" || e.Card.Options[1].Text != "关闭" {
		t.Fatalf("the letter's card: %+v", ev)
	}
	// 关闭: nothing moves.
	var cr api.CardReply
	l.want(200, bot, "POST", "/api/answer", api.CardAnswer{Card: e.Card.ID, Option: "-"}, &cr)
	var ptr api.ChatPointer
	if l.want(200, bot, "GET", "/api/chat/qq:1", nil, &ptr); ptr.Session != a.Session {
		t.Fatalf("closed, yet moved: %+v", ptr)
	}
	// Again, and 进入会话: the chat points at B, its last rounds in the reply.
	l.want(200, "socket", "POST", "/api/bot", api.BotLetter{Text: "跑完了", Caller: b.ID}, nil)
	json.Unmarshal([]byte(took()[0].Data), &e)
	cr = api.CardReply{}
	l.want(200, bot, "POST", "/api/answer", api.CardAnswer{Card: e.Card.ID, Option: b.ID}, &cr)
	if cr.Reply == nil || !strings.HasPrefix(cr.Reply.Markdown, "现在：[CC]") || !strings.Contains(cr.Reply.Markdown, "# 近期消息预览\n\n[主机] first  \n[CC] ok: first") {
		t.Fatalf("entered: %+v", cr.Reply)
	}
	var h api.ChatInputReply
	if l.want(200, bot, "POST", "/api/chat/qq:1/input", api.ChatInput{Text: "/history 1"}, &h); h.Reply == nil || !strings.Contains(h.Reply.Markdown, "[主机] first") {
		t.Fatalf("history: %+v", h.Reply)
	}
	// Pointed at now: the session's reply.
	r = api.BotReply{}
	l.want(200, "socket", "POST", "/api/bot", api.BotLetter{Text: "again", Caller: b.ID}, &r)
	if ev := took(); r.Letter || len(ev) != 1 || ev[0].Type != "reply" || !strings.Contains(ev[0].Data, "again") {
		t.Fatalf("as the reply: %+v %+v", r, ev)
	}
	// A's ask, A pointed at by none now, its turn begun by 本机: no Bot follows 本机, so none gets it.
	events()
	l.want(200, "web", "POST", "/api/sessions/"+a.Session+"/send", api.Send{Text: "please ASK"}, nil)
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		var st api.SessionState
		if l.do("web", "GET", "/api/sessions/"+a.Session, nil, &st); len(st.Asks) > 0 {
			l.want(200, "web", "POST", "/api/answer", api.CardAnswer{Card: st.Asks[0].Card.ID, Option: "allow"}, nil)
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no ask in A")
		}
	}
	l.waitReply("web", a.Session, "ok: please ASK")
	if slices.ContainsFunc(events(), func(x store.Outgoing) bool { return x.Type == "card" }) {
		t.Fatal("an ask no Bot follows went to the bot")
	}
	// The bot sends to A itself (not through its chat): begun at the bot, so its chat gets the ask,
	// A named; answered there, the chat stays.
	l.want(200, bot, "POST", "/api/sessions/"+a.Session+"/send", api.Send{Text: "please ASK"}, nil)
	var card *api.Card
	for deadline := time.Now().Add(10 * time.Second); card == nil; time.Sleep(20 * time.Millisecond) {
		for _, x := range events() {
			if x.Type == "card" {
				json.Unmarshal([]byte(x.Data), &e)
				card = e.Card
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("no ask")
		}
	}
	if !strings.HasPrefix(card.Title, "[CC] ") || e.Session != a.Session || len(card.Summary) < 2 {
		t.Fatalf("the ask: %+v", card)
	}
	l.want(200, bot, "POST", "/api/answer", api.CardAnswer{Card: card.ID, Option: "allow"}, nil)
	if l.want(200, bot, "GET", "/api/chat/qq:1", nil, &ptr); ptr.Session != b.ID {
		t.Fatalf("the ask moved the chat: %+v", ptr)
	}
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if slices.ContainsFunc(events(), func(x store.Outgoing) bool { return x.Type == "card_done" }) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no card_done")
		}
	}
	// Only ir on the socket, in a session.
	l.want(400, bot, "POST", "/api/bot", api.BotLetter{Text: "x", Caller: b.ID}, nil)
	l.want(400, "socket", "POST", "/api/bot", api.BotLetter{Text: "x"}, nil)
	l.want(404, "socket", "POST", "/api/bot", api.BotLetter{Text: "x", Caller: b.ID, To: "nobody"}, nil)
	// 会话 offers to leave the one it is in; /leave: in none, the list marks none with the bot.
	var use api.ChatInputReply
	if l.want(200, bot, "POST", "/api/chat/qq:1/input", api.ChatInput{Text: "/use"}, &use); use.Card == nil || use.Card.Options[len(use.Card.Options)-1].Text != "离开当前会话" {
		t.Fatalf("no leaving: %+v", use.Card)
	}
	var left api.CardReply
	l.want(200, bot, "POST", "/api/answer", api.CardAnswer{Card: use.Card.ID, Option: use.Card.Options[len(use.Card.Options)-1].ID}, &left)
	var list []api.Session
	if l.want(200, "web", "GET", "/api/sessions", nil, &list); left.Reply == nil || left.Reply.Text != "已离开会话。" || slices.ContainsFunc(list, func(x api.Session) bool { return len(x.Bots) > 0 }) {
		t.Fatalf("left: %+v %+v", left.Reply, list)
	}
	var again api.ChatInputReply
	if l.want(200, bot, "POST", "/api/chat/qq:1/input", api.ChatInput{Text: "/leave"}, &again); again.Reply == nil || again.Reply.Text != "当前不在任何会话。" {
		t.Fatalf("again: %+v", again.Reply)
	}
	l.want(200, bot, "POST", "/api/chat/qq:1/input", api.ChatInput{Text: "/use " + b.ID}, nil)

	// What it follows: web panels only, set by a panel or a person's ir.
	l.want(400, "socket", "PUT", "/api/apps/"+botID+"/follows", api.SetFollows{Panels: []string{"local"}}, nil)
	l.want(404, "socket", "PUT", "/api/apps/local/follows", api.SetFollows{}, nil)
	l.want(403, bot, "PUT", "/api/apps/"+botID+"/follows", api.SetFollows{}, nil)
	l.want(200, "socket", "PUT", "/api/apps/"+botID+"/follows", api.SetFollows{}, nil)

	// The bot removed: no session is marked with it any more.
	list = nil
	if l.want(200, "web", "GET", "/api/sessions", nil, &list); !slices.ContainsFunc(list, func(x api.Session) bool { return len(x.Bots) > 0 }) {
		t.Fatalf("no bot marked: %+v", list)
	}
	l.want(200, "socket", "DELETE", "/api/apps/"+botID, nil, nil)
	list = nil
	if l.want(200, "web", "GET", "/api/sessions", nil, &list); slices.ContainsFunc(list, func(x api.Session) bool { return len(x.Bots) > 0 }) {
		t.Fatalf("a removed bot still marked: %+v", list)
	}
}

// A session's menu brings a bot's chat to it: the chat points there and is told so, as by /use;
// the pages are told when a bot's chats change.
func TestBringBot(t *testing.T) {
	var srv *Server
	var changed atomic.Int32
	l := startWith(t, func(s *Server, cfg config.Config, work string) {
		srv = s
		var err error
		s.Chat, err = chat.New(chat.Options{Manager: s.m, Store: s.st, StateDir: cfg.StateDir, ChatDir: work, Logf: t.Logf, Guard: s.ChatGuard(), Terminal: s.HasTerminal,
			BotsChanged: func() { changed.Add(1); s.BotsChanged() }})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(s.Chat.Close)
	})
	var tok api.Token
	tok = l.newApp("QQ", true)
	bot, botID := tok.Token, tok.App.ID
	var b api.NewSessionReply
	l.want(200, "web", "POST", "/api/sessions", api.NewSession{Agent: api.AgentClaude, Cwd: l.work, Text: "first"}, &b)
	l.waitReply("web", b.ID, "ok: first")
	// No chat yet, not connected: no chat in the list, none to bring.
	chatOf := func() api.AppChat {
		var as api.Apps
		l.want(200, "web", "GET", "/api/apps", nil, &as)
		if i := slices.IndexFunc(as.Apps, func(x api.App) bool { return x.ID == botID }); i >= 0 {
			return as.Apps[i].Chat
		}
		t.Fatal("not listed")
		return ""
	}
	if c := chatOf(); c != "" {
		t.Fatalf("a chat without one: %q", c)
	}
	l.want(404, "web", "POST", "/api/sessions/"+b.ID+"/bot", api.BringBot{Bot: botID}, nil)

	// Its event stream open, still no chat: a bot with none to bring, and the pages are told.
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "GET", l.base+"/api/events", nil)
	req.Header.Set("Authorization", "Bearer "+bot)
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("events: %v %v", resp, err)
	}
	waitTill(t, func() bool { return changed.Load() == 1 })
	if c := chatOf(); c != api.AppChatIdle {
		t.Fatalf("connected, no chat: %q", c)
	}
	l.want(404, "web", "POST", "/api/sessions/"+b.ID+"/bot", api.BringBot{Bot: botID}, nil)

	var a api.ChatInputReply
	l.want(200, bot, "POST", "/api/chat/qq:1/input", api.ChatInput{Text: "/new claude"}, &a)
	if c := chatOf(); c != api.AppChatOn {
		t.Fatalf("a chat, not listed: %q", c)
	}
	if n := changed.Load(); n != 2 {
		t.Fatalf("told %d times, want 2", n)
	}
	l.want(200, bot, "POST", "/api/chat/qq:1/input", api.ChatInput{Text: "again"}, nil)
	// Its turn's reply comes in its time: wait for it, so it is not taken for what bringing says.
	waitTill(t, func() bool {
		out, _ := srv.st.OutboxAfter(botID, 0)
		return slices.ContainsFunc(out, func(x store.Outgoing) bool { return strings.Contains(x.Data, "ok: again") })
	})
	if n := changed.Load(); n != 2 {
		t.Fatalf("told again for a chat it had: %d", n)
	}
	cancel()
	resp.Body.Close()
	waitTill(t, func() bool { return changed.Load() == 3 })
	if c := chatOf(); c != api.AppChatOn {
		t.Fatalf("a chat, gone: %q", c)
	}
	last, _ := srv.st.OutboxLast(botID)
	var r api.BotReply
	l.want(200, "web", "POST", "/api/sessions/"+b.ID+"/bot", api.BringBot{Bot: botID}, &r)
	if r.Bot != botID || r.Name != "QQ" || r.Chat != "qq:1" {
		t.Fatalf("brought: %+v", r)
	}
	var ptr api.ChatPointer
	if l.want(200, bot, "GET", "/api/chat/qq:1", nil, &ptr); ptr.Session != b.ID {
		t.Fatalf("not moved: %+v", ptr)
	}
	out, _ := srv.st.OutboxAfter(botID, last)
	out = slices.DeleteFunc(out, func(x store.Outgoing) bool { return x.Type == "status" })
	var e api.ChatEvent
	if len(out) != 1 || out[0].Type != "reply" || json.Unmarshal([]byte(out[0].Data), &e) != nil || e.Reply == nil ||
		!strings.HasPrefix(e.Reply.Markdown, "现在：[CC]") || !strings.Contains(e.Reply.Markdown, "[CC] ok: first") {
		t.Fatalf("told: %+v", out)
	}
	var list []api.Session
	l.want(200, "web", "GET", "/api/sessions", nil, &list)
	if i := slices.IndexFunc(list, func(x api.Session) bool { return x.ID == b.ID }); i < 0 || !slices.Contains(list[i].Bots, botID) {
		t.Fatalf("not marked: %+v", list)
	}
	// Not a bot; not from a bot.
	l.want(404, "web", "POST", "/api/sessions/"+b.ID+"/bot", api.BringBot{Bot: "nobody"}, nil)
	l.want(403, bot, "POST", "/api/sessions/"+b.ID+"/bot", api.BringBot{Bot: botID}, nil)
}

// waitTill waits up to 5s for ok.
func waitTill(t *testing.T, ok func() bool) {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); !ok(); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatal("timed out")
		}
	}
}
