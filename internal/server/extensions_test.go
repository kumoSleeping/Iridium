package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/access"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/chat"
	"github.com/kumoSleeping/iridium/internal/config"
	"github.com/kumoSleeping/iridium/internal/gate"
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

// fakeExtension is an iridium-qq that answers list, start, stop, pair, bind and remove as the real
// one does, its home a temp dir: each scan is one more bot (101 Bot1, 102 Bot2…).
const fakeExtension = `#!/bin/sh
root=$(dirname "$0")/home
cmd=$1; shift
bot=""; all=""
while [ $# -gt 0 ]; do
	case "$1" in
	--bot) bot=$2; shift ;;
	--all) all=1 ;;
	--taken) echo "$2" > "$root/taken" ;;
	esac
	shift
done
bots() { ls "$root/bots" 2>/dev/null; }
case "$cmd" in
list)
	sep=""; printf '['
	for id in $(bots); do
		d="$root/bots/$id"
		service=""; [ -f "$d/started" ] && service=active
		token=false; [ -f "$d/token" ] && token=true
		printf '%s{"version":"dev","home":"%s","bound":true,"appId":"%s","name":"%s","owners":1,"token":%s,"service":"%s","enabled":false}' "$sep" "$d" "$id" "$(cat "$d/name")" "$token" "$service"
		sep=","
	done
	echo ']' ;;
start)
	for id in ${bot:-$(bots)}; do
		[ -f "$root/bots/$id/token" ] || { echo "iridium-qq: no token" >&2; exit 1; }
		touch "$root/bots/$id/started"
	done ;;
stop) for id in ${bot:-$(bots)}; do rm -f "$root/bots/$id/started"; done ;;
restart) ;;
pair) ;;
bind)
	echo '{"state":"qr","url":"https://q.qq.com/x"}'
	sleep 0.3
	n=$(( $(bots | wc -l) + 1 )); id=$((100 + n))
	mkdir -p "$root/bots/$id"; echo "Bot$n" > "$root/bots/$id/name"
	echo "{\"state\":\"bound\",\"appId\":\"$id\",\"name\":\"Bot$n\",\"owner\":true}" ;;
remove)
	if [ -n "$all" ]; then rm -rf "$root"; else rm -rf "$root/bots/$bot"; fi ;;
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
	home := filepath.Join(dir, "home")
	os.MkdirAll(home, 0o700)
	x = api.Extension{}
	l.want(200, "socket", "POST", "/api/extensions/qq/install", nil, &x)
	if !x.Installed || x.Error != "" || len(x.Bots) != 0 {
		t.Fatalf("installed: %+v", x)
	}
	// An agent uses the extension's own command line.
	l.want(403, "agent", "POST", "/api/extensions/qq/bind", nil, nil)
	// A scan: a QR code, then the bot's TOKEN in its directory and its service started.
	scan := func(n int) []api.ExtensionStatus {
		t.Helper()
		x := api.Extension{}
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
		bots := list[0].Bots
		if list[0].Error != "" || len(bots) != n {
			t.Fatalf("bound: %+v", list[0])
		}
		for _, b := range bots {
			if !b.Bound || !b.Token || b.Service != "active" {
				t.Fatalf("bot: %+v", b)
			}
		}
		return bots
	}
	scan(1)
	if b, err := os.ReadFile(filepath.Join(home, "bots", "101", "token")); err != nil || !strings.HasPrefix(string(b), "ir_") {
		t.Fatalf("token: %q %v", b, err)
	}
	// Another scan: one more bot, one more terminal; the terminal names in use go to the
	// extension, for a new bot's to differ.
	scan(2)
	if b, _ := os.ReadFile(filepath.Join(home, "taken")); !strings.Contains(string(b), "Bot1") {
		t.Fatalf("taken with a bot: %q", b)
	}
	terminals := func() map[string]bool {
		var ts api.Terminals
		l.want(200, "socket", "GET", "/api/terminals", nil, &ts)
		out := map[string]bool{}
		for _, tm := range ts.Terminals {
			if tm.Kind == api.TerminalChannel {
				out[tm.Name] = access.HasClaude(tm.Rules)
			}
		}
		return out
	}
	if ts := terminals(); !ts["Bot1"] || !ts["Bot2"] {
		t.Fatalf("no Bot terminals with Claude: %+v", ts)
	}
	// One bot's service stopped: the other's runs on.
	x = api.Extension{}
	l.want(400, "socket", "POST", "/api/extensions/qq/stop?bot=-x", nil, nil)
	l.want(200, "socket", "POST", "/api/extensions/qq/stop?bot=101", nil, &x)
	if len(x.Bots) != 2 || x.Bots[0].Service != "" || x.Bots[1].Service != "active" || x.Error != "" {
		t.Fatalf("stopped: %+v", x)
	}
	// Renamed on the page, its old name taken by another Bot: the bot is still its own terminal
	// (by its TOKEN), and the other one is no bot of the extension's.
	renamed := "改名一" // the page's rename lands here
	if _, err := l.s.gate().UpdateProfile("Bot1", gate.Profile{Name: &renamed}); err != nil {
		t.Fatal(err)
	}
	l.want(200, "socket", "POST", "/api/terminals/tokens", api.NewInvite{Name: "Bot1"}, nil)
	x = api.Extension{}
	l.want(200, "socket", "GET", "/api/extensions", nil, &list)
	if b := list[0].Bots; b[0].Name != "改名一" || b[1].Name != "Bot2" {
		t.Fatalf("renamed: %+v", b)
	}
	l.want(200, "socket", "DELETE", "/api/terminals/Bot1", nil, nil)
	time.Sleep(300 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(home, "bots", "101")); err != nil {
		t.Fatalf("another's terminal took the bot away: %v", err)
	}
	// Removed in the list as any Bot: that bot goes (its directory cleared), the other stays.
	l.want(200, "socket", "DELETE", "/api/terminals/改名一", nil, nil)
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		if _, err := os.Stat(filepath.Join(home, "bots", "101")); os.IsNotExist(err) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("its service stayed")
		}
	}
	if _, err := os.Stat(filepath.Join(home, "bots", "102")); err != nil {
		t.Fatalf("the other bot went too: %v", err)
	}
	// Removing the extension: every bot's terminal and the binary go.
	x = api.Extension{}
	l.want(200, "socket", "DELETE", "/api/extensions/qq", nil, &x)
	if x.Installed || x.Error != "" {
		t.Fatalf("removed: %+v", x)
	}
	if ts := terminals(); len(ts) != 1 {
		t.Fatalf("its terminals stayed: %+v", ts)
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
	l.want(400, "socket", "PUT", "/api/terminals/"+botID+"/follows", api.SetFollows{Panels: []string{"local"}}, nil)
	l.want(404, "socket", "PUT", "/api/terminals/local/follows", api.SetFollows{}, nil)
	l.want(403, bot, "PUT", "/api/terminals/"+botID+"/follows", api.SetFollows{}, nil)
	l.want(200, "socket", "PUT", "/api/terminals/"+botID+"/follows", api.SetFollows{}, nil)

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

// A session's menu brings a bot's chat to it: the chat points there and is told so, as by /use;
// the terminal list marks the bot.
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
	l.want(200, "socket", "POST", "/api/terminals/tokens", api.NewInvite{Name: "QQ", Owner: true}, &tok)
	bot, botID := tok.Token, tok.Terminal.ID
	var b api.NewSessionReply
	l.want(200, "web", "POST", "/api/sessions", api.NewSession{Agent: api.AgentClaude, Cwd: l.work, Text: "first"}, &b)
	l.waitReply("web", b.ID, "ok: first")
	// No chat yet, not connected: not a bot in the list, none to bring.
	botOf := func() (x api.Terminal) {
		var ts api.Terminals
		l.want(200, "web", "GET", "/api/terminals", nil, &ts)
		if i := slices.IndexFunc(ts.Terminals, func(x api.Terminal) bool { return x.ID == botID }); i >= 0 {
			x = ts.Terminals[i]
		}
		return x
	}
	if x := botOf(); x.Bot {
		t.Fatal("a bot without a chat")
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
	if x := botOf(); !x.Bot || !x.Chatless {
		t.Fatalf("connected, no chat: %+v", x)
	}
	l.want(404, "web", "POST", "/api/sessions/"+b.ID+"/bot", api.BringBot{Bot: botID}, nil)

	var a api.ChatInputReply
	l.want(200, bot, "POST", "/api/chat/qq:1/input", api.ChatInput{Text: "/new claude"}, &a)
	if x := botOf(); !x.Bot || x.Chatless {
		t.Fatalf("not marked a bot with a chat: %+v", x)
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
	if x := botOf(); !x.Bot || x.Chatless {
		t.Fatalf("a bot with a chat, gone: %+v", x)
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
