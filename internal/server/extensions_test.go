package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/access"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/chat"
	"github.com/kumoSleeping/iridium/internal/config"
	"github.com/kumoSleeping/iridium/internal/store"
)

// ir token --into: the daemon writes the token into the file, and shows it to no one; an agent's
// waits for a panel as before, the file named on the confirmation.
func TestTokenInto(t *testing.T) {
	l := startWith(t, nil)
	dir := t.TempDir()
	into := filepath.Join(dir, "token")
	var tok api.Token
	l.want(200, "socket", "POST", "/api/terminals/tokens", api.NewInvite{Name: "qq", Owner: true, Into: into}, &tok)
	if tok.Token != "" || tok.Into != into || !access.HasClaude(tok.Terminal.Rules) {
		t.Fatalf("token into: %+v", tok)
	}
	b, err := os.ReadFile(into)
	fi, _ := os.Stat(into)
	if err != nil || !strings.HasPrefix(string(b), "ir_") || fi.Mode().Perm() != 0o600 {
		t.Fatalf("the file: %q %v %v", b, fi.Mode(), err)
	}
	// The token works.
	l.want(200, strings.TrimSpace(string(b)), "GET", "/api/me", nil, nil)

	// An agent's: pending, the file named; allowed, written, not shown to the panel.
	into2 := filepath.Join(dir, "token2")
	var p api.Pending
	l.want(202, "agent", "POST", "/api/terminals/tokens", api.NewInvite{Name: "qq2", Owner: true, Into: into2}, &p)
	if p.Confirm.Into != into2 {
		t.Fatalf("pending: %+v", p.Confirm)
	}
	var ans api.ConfirmReply
	l.want(200, "socket", "POST", "/api/confirms/"+p.Confirm.ID, api.ConfirmAnswer{Allow: true}, &ans)
	if ans.Token == nil || ans.Token.Token != "" || ans.Token.Into != into2 {
		t.Fatalf("allowed: %+v", ans.Token)
	}
	if b, err := os.ReadFile(into2); err != nil || !strings.HasPrefix(string(b), "ir_") {
		t.Fatalf("the agent's file: %q %v", b, err)
	}
	// Not into Iridium's own directories, nor somewhere relative.
	l.want(400, "socket", "POST", "/api/terminals/tokens", api.NewInvite{Name: "qq3", Into: filepath.Join(l.cfg.StatePath(), "x")}, nil)
	l.want(400, "socket", "POST", "/api/terminals/tokens", api.NewInvite{Name: "qq3", Into: "token"}, nil)
}

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
	l.want(200, "socket", "POST", "/api/terminals/tokens", api.NewInvite{Name: "bot", Owner: true}, &tok)
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

// fakeExtension is an iridium-qq that answers status, start, pair and bind as the real one does,
// its home a temp dir.
const fakeExtension = `#!/bin/sh
home=$(dirname "$0")/home
case "$1" in
status)
	bound=false; [ -f "$home/bound" ] && bound=true
	service=""; [ -f "$home/started" ] && service=active
	printf '{"version":"dev","home":"%s","bound":%s,"appId":"app","name":"QQ","owners":1,"token":false,"service":"%s","enabled":false}\n' "$home" "$bound" "$service" ;;
start) [ -f "$home/token" ] || { echo "iridium-qq: no token" >&2; exit 1; }; touch "$home/started" ;;
stop) rm -f "$home/started" ;;
pair) ;;
bind)
	echo '{"state":"qr","url":"https://q.qq.com/x"}'
	sleep 0.3
	touch "$home/bound"
	echo '{"state":"bound","appId":"app","owner":true}' ;;
remove) rm -rf "$home" ;;
version) echo dev ;;
esac
`

func TestExtensions(t *testing.T) {
	dir := t.TempDir()
	l := startWith(t, func(s *Server, cfg config.Config, work string) { s.ExtDir = dir })
	var list []api.Extension
	l.want(200, "socket", "GET", "/api/extensions", nil, &list)
	if len(list) != 1 || list[0].ID != "qq" || list[0].Installed {
		t.Fatalf("before: %+v", list)
	}
	// A development build installs the copy beside it (push-test's), else says where to put one.
	var x api.Extension
	l.want(200, "socket", "POST", "/api/extensions/qq/install", nil, &x)
	if x.Installed || !strings.Contains(x.Error, "development build") {
		t.Fatalf("install without a copy: %+v", x)
	}
	if err := os.WriteFile(filepath.Join(dir, "iridium-qq.dist"), []byte(fakeExtension), 0o755); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(dir, "home"), 0o700)
	x = api.Extension{}
	l.want(200, "socket", "POST", "/api/extensions/qq/install", nil, &x)
	if !x.Installed || x.Error != "" || x.Status == nil || x.Status.Bound {
		t.Fatalf("installed: %+v", x)
	}
	// An agent uses the extension's own command line.
	l.want(403, "agent", "POST", "/api/extensions/qq/bind", nil, nil)
	// The scan: a QR code, then the TOKEN in its home and the service started.
	x = api.Extension{}
	l.want(200, "socket", "POST", "/api/extensions/qq/bind", nil, &x)
	if x.Busy != "bind" || x.Bind == nil || x.Bind.URL != "https://q.qq.com/x" || !strings.HasPrefix(x.Bind.QR, "data:image/svg+xml") {
		t.Fatalf("binding: %+v", x)
	}
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		list = nil
		l.want(200, "socket", "GET", "/api/extensions", nil, &list)
		if list[0].Busy == "" || time.Now().After(deadline) {
			break
		}
	}
	if s := list[0].Status; list[0].Error != "" || s == nil || !s.Bound || s.Service != "active" {
		t.Fatalf("bound: %+v %+v", list[0], s)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "home", "token")); err != nil || !strings.HasPrefix(string(b), "ir_") {
		t.Fatalf("token: %q %v", b, err)
	}
	var ts api.Terminals
	l.want(200, "socket", "GET", "/api/terminals", nil, &ts)
	found := false
	for _, tm := range ts.Terminals {
		if tm.Name == "QQ" && tm.Kind == api.TerminalChannel && access.HasClaude(tm.Rules) {
			found = true
		}
	}
	if !found {
		t.Fatalf("no QQ terminal with Claude: %+v", ts.Terminals)
	}
	// Removed in the list as any Bot: its service goes with it (the fake's remove clears its home).
	l.want(200, "socket", "DELETE", "/api/terminals/QQ", nil, nil)
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		if _, err := os.Stat(filepath.Join(dir, "home")); os.IsNotExist(err) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("its service stayed")
		}
	}
	os.MkdirAll(filepath.Join(dir, "home"), 0o700)
	x = api.Extension{}
	l.want(200, "socket", "POST", "/api/extensions/qq/stop", nil, &x)
	if x.Status.Service != "" || x.Error != "" {
		t.Fatalf("stopped: %+v", x)
	}
	// Removing it: its terminal and binary go.
	x = api.Extension{}
	l.want(200, "socket", "DELETE", "/api/extensions/qq", nil, &x)
	if x.Installed || x.Error != "" {
		t.Fatalf("removed: %+v", x)
	}
	ts = api.Terminals{}
	l.want(200, "socket", "GET", "/api/terminals", nil, &ts)
	for _, tm := range ts.Terminals {
		if tm.Name == "QQ" {
			t.Fatalf("its terminal stayed: %+v", tm)
		}
	}
}

func TestIsRelease(t *testing.T) {
	for v, want := range map[string]bool{"v0.1.7": true, "v1.2.3-rc.1": true, "v0.1.8-dev.0+gdfe3a86": false, "v0.1.8-dev.0+gdfe3a86.dirty": false, "dev": false} {
		if isRelease(v) != want {
			t.Errorf("isRelease(%q) = %v", v, !want)
		}
	}
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
	l.want(200, "socket", "POST", "/api/terminals/tokens", api.NewInvite{Name: "QQ", Owner: true}, &tok)
	bot, botID := tok.Token, tok.Terminal.ID
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
	if cr.Reply == nil || !strings.HasPrefix(cr.Reply.Markdown, "现在：[CC]") || !strings.Contains(cr.Reply.Markdown, "# 近期消息预览\n\n[本机] first  \n[CC] ok: first") {
		t.Fatalf("entered: %+v", cr.Reply)
	}
	var h api.ChatInputReply
	if l.want(200, bot, "POST", "/api/chat/qq:1/input", api.ChatInput{Text: "/history 1"}, &h); h.Reply == nil || !strings.Contains(h.Reply.Markdown, "[本机] first") {
		t.Fatalf("history: %+v", h.Reply)
	}
	// Pointed at now: the session's reply.
	r = api.BotReply{}
	l.want(200, "socket", "POST", "/api/bot", api.BotLetter{Text: "again", Caller: b.ID}, &r)
	if ev := took(); r.Letter || len(ev) != 1 || ev[0].Type != "reply" || !strings.Contains(ev[0].Data, "again") {
		t.Fatalf("as the reply: %+v %+v", r, ev)
	}
	// A's ask, A pointed at by none now: to the chat, A named; answered there, the chat stays.
	events()
	l.want(200, "web", "POST", "/api/sessions/"+a.Session+"/send", api.Send{Text: "please ASK"}, nil)
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

	// The bot removed: no session is marked with it any more.
	list = nil
	if l.want(200, "web", "GET", "/api/sessions", nil, &list); !slices.ContainsFunc(list, func(x api.Session) bool { return len(x.Bots) > 0 }) {
		t.Fatalf("no bot marked: %+v", list)
	}
	l.want(200, "socket", "DELETE", "/api/terminals/"+botID, nil, nil)
	list = nil
	if l.want(200, "web", "GET", "/api/sessions", nil, &list); slices.ContainsFunc(list, func(x api.Session) bool { return len(x.Bots) > 0 }) {
		t.Fatalf("a removed bot still marked: %+v", list)
	}
}
