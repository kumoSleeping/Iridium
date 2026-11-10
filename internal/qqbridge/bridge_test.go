package qqbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/qq"
)

const owner = "OWNER"

// fakeQQ is QQ's API and gateway: it records what is sent, refuses what it is told to, and pushes
// the events a test gives it.
type fakeQQ struct {
	t      *testing.T
	srv    *httptest.Server
	events chan string // dispatch frames
	mu     sync.Mutex
	sent   []qq.Message
	acks   []string
	// noMarkdown refuses Markdown; passiveLeft is how many passive replies QQ still takes;
	// activeLeft, active messages.
	noMarkdown  bool
	passiveLeft int
	activeLeft  int
	menus       []string // the menu calls, "PUT /v2/menu", "PUT /v2/panels/P1"…
}

func newFakeQQ(t *testing.T) *fakeQQ {
	f := &fakeQQ{t: t, events: make(chan string, 16), passiveLeft: 100, activeLeft: 100}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /app/getAppAccessToken", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"access_token":"tok","expires_in":"7200"}`)
	})
	mux.HandleFunc("GET /users/@me", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"id":"1","username":"bot","avatar":"http://%s/avatar.png"}`, r.Host)
	})
	menu := func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.menus = append(f.menus, r.Method+" "+r.URL.Path+" "+string(b))
		f.mu.Unlock()
		io.WriteString(w, `{}`)
	}
	mux.HandleFunc("PUT /v2/menu", menu)
	mux.HandleFunc("PUT /v2/panels/{id}", menu)
	mux.HandleFunc("POST /v2/panels", menu)
	mux.HandleFunc("GET /v2/panels", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"records":[{"panel_id":"OTHER","panel":{"remark":"mine"}},{"panel_id":"RH","panel":{"remark":"rhodium"}}]}`)
	})
	mux.HandleFunc("GET /avatar.png", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "AVATAR") })
	mux.HandleFunc("PUT /interactions/{id}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.acks = append(f.acks, r.PathValue("id"))
		f.mu.Unlock()
		io.WriteString(w, `{}`)
	})
	mux.HandleFunc("POST /v2/users/{id}/files", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"file_info":"FI"}`)
	})
	mux.HandleFunc("POST /v2/users/{id}/messages", func(w http.ResponseWriter, r *http.Request) {
		var m qq.Message
		_ = json.NewDecoder(r.Body).Decode(&m)
		f.mu.Lock()
		defer f.mu.Unlock()
		refuse := func(code int) {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, `{"code":%d,"message":"no"}`, code)
		}
		switch {
		case m.Type == qq.MsgMarkdown && f.noMarkdown:
			refuse(304003)
			return
		case m.MsgID != "" && f.passiveLeft <= 0:
			refuse(40034024)
			return
		case m.MsgID == "" && f.activeLeft <= 0:
			refuse(22009)
			return
		}
		if m.MsgID != "" {
			f.passiveLeft--
		} else {
			f.activeLeft--
		}
		f.sent = append(f.sent, m)
		fmt.Fprintf(w, `{"id":"m%d"}`, len(f.sent))
	})
	mux.HandleFunc("GET /ws", func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		ctx := r.Context()
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"op":10,"d":{"heartbeat_interval":30000}}`))
		if _, _, err := c.Read(ctx); err != nil { // identify
			return
		}
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"op":0,"s":1,"t":"READY","d":{"session_id":"S"}}`))
		go func() {
			for {
				if _, b, err := c.Read(ctx); err != nil {
					return
				} else if strings.Contains(string(b), `"op":1`) {
					_ = c.Write(ctx, websocket.MessageText, []byte(`{"op":11}`))
				}
			}
		}()
		seq := 1
		for {
			select {
			case <-ctx.Done():
				return
			case e := <-f.events:
				seq++
				_ = c.Write(ctx, websocket.MessageText, []byte(strings.Replace(e, "SEQ", fmt.Sprint(seq), 1)))
			}
		}
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeQQ) message(id, from, text string) {
	d, _ := json.Marshal(map[string]any{"id": id, "author": map[string]string{"user_openid": from}, "content": text})
	f.events <- fmt.Sprintf(`{"op":0,"s":SEQ,"t":"C2C_MESSAGE_CREATE","d":%s}`, d)
}

func (f *fakeQQ) pressButton(id, from, data string) {
	d, _ := json.Marshal(map[string]any{"id": id, "scene": "c2c", "user_openid": from, "data": map[string]any{"resolved": map[string]string{"button_data": data}}})
	f.events <- fmt.Sprintf(`{"op":0,"s":SEQ,"t":"INTERACTION_CREATE","d":%s}`, d)
}

func (f *fakeQQ) messages() []qq.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]qq.Message(nil), f.sent...)
}

// fakeIridium is Iridium's plugin API.
type fakeIridium struct {
	srv     *httptest.Server
	events  chan string
	mu      sync.Mutex
	inputs  []ChatInput
	answers []api.CardAnswer
	avatars int
	n       int
}

func newFakeIridium(t *testing.T) *fakeIridium {
	f := &fakeIridium{events: make(chan string, 16)}
	mux := http.NewServeMux()
	auth := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer TOKEN" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			h(w, r)
		}
	}
	mux.HandleFunc("POST /api/chat/{key}/input", auth(func(w http.ResponseWriter, r *http.Request) {
		var in ChatInput
		_ = json.NewDecoder(r.Body).Decode(&in)
		f.mu.Lock()
		f.inputs = append(f.inputs, in)
		f.mu.Unlock()
		out := ChatInputReply{Session: "s1"}
		if in.Text == "/ls" {
			out.Reply = &ChatReply{Markdown: "**sessions**", Text: "sessions"}
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	mux.HandleFunc("POST /api/answer", auth(func(w http.ResponseWriter, r *http.Request) {
		var a api.CardAnswer
		_ = json.NewDecoder(r.Body).Decode(&a)
		f.mu.Lock()
		f.answers = append(f.answers, a)
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(CardReply{OK: true})
	}))
	mux.HandleFunc("POST /api/chat/{key}/uploads", auth(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(api.Upload{ID: "u1"})
	}))
	mux.HandleFunc("PUT /api/uploads/{id}", auth(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(api.Upload{ID: "u1"})
	}))
	mux.HandleFunc("POST /api/uploads/{id}/complete", auth(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(api.Upload{ID: "u1", Done: true, File: &api.FileRef{Ref: "R1"}})
	}))
	mux.HandleFunc("GET /api/chat/{key}/commands", auth(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]ChatCommand{{Name: "new", Description: "开新会话", Iridium: true}, {Name: "review", Description: "agent's"}, {Name: "use", Description: "换会话", Iridium: true}})
	}))
	mux.HandleFunc("GET /api/chat/{key}/files/{ref}", auth(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "PNGDATA")
	}))
	mux.HandleFunc("PUT /api/me/profile", auth(func(w http.ResponseWriter, r *http.Request) {
		if file, _, err := r.FormFile("avatar"); err == nil {
			b, _ := io.ReadAll(file)
			if string(b) == "AVATAR" {
				f.mu.Lock()
				f.avatars++
				f.mu.Unlock()
			}
		}
		io.WriteString(w, `{}`)
	}))
	mux.HandleFunc("GET /api/events", auth(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		for {
			select {
			case <-r.Context().Done():
				return
			case e := <-f.events:
				io.WriteString(w, e)
				w.(http.Flusher).Flush()
			}
		}
	}))
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeIridium) send(kind string, e ChatEvent) {
	f.mu.Lock()
	f.n++
	n := f.n
	f.mu.Unlock()
	b, _ := json.Marshal(e)
	f.events <- fmt.Sprintf("id: %d\nevent: %s\ndata: %s\n\n", n, kind, b)
}

func (f *fakeIridium) got() []ChatInput {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]ChatInput(nil), f.inputs...)
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func start(t *testing.T) (*Bridge, *fakeQQ, *fakeIridium) {
	t.Helper()
	q, ir := newFakeQQ(t), newFakeIridium(t)
	// Not t.TempDir: a send still in flight as the bridge stops may write its outbox here a
	// moment after; removed (retried) once the bridge is stopped (this cleanup runs last).
	home, err := os.MkdirTemp("", "qqbridge-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for i := 0; i < 50 && os.RemoveAll(home) != nil; i++ {
			time.Sleep(20 * time.Millisecond)
		}
	})
	if err := WriteFile(home, TokenFile, []byte("TOKEN\n")); err != nil {
		t.Fatal(err)
	}
	b := &Bridge{
		Home:       home,
		Config:     Config{AppID: "app", Secret: "s", Owners: []string{owner}, Iridium: ir.srv.URL, Name: "QQ"},
		QQ:         &qq.Client{AppID: "app", Secret: "s", API: q.srv.URL, TokenURL: q.srv.URL, MenuAPI: q.srv.URL},
		IR:         &Iridium{URL: ir.srv.URL, Token: func() string { return Token(home) }},
		GatewayURL: "ws" + strings.TrimPrefix(q.srv.URL, "http") + "/ws",
		Logf:       t.Logf,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { b.Run(ctx); close(done) }()
	// Stopped before its home goes: it writes its status there till it ends.
	t.Cleanup(func() { cancel(); <-done })
	waitFor(t, "connected", func() bool {
		var s Status
		ok, _ := Read(home, StatusFile, &s)
		return ok && s.QQ && s.Iridium
	})
	return b, q, ir
}

func TestMessageToIridiumAndReplyBack(t *testing.T) {
	_, q, ir := start(t)
	q.message("M1", owner, " 你好 ")
	waitFor(t, "input", func() bool { return len(ir.got()) == 1 })
	if in := ir.got()[0]; in.Text != "你好" {
		t.Fatalf("input %+v", in)
	}
	ir.send("reply", ChatEvent{Chat: "c2c:" + owner, Reply: &ChatReply{Markdown: "**好**", Text: "好", Files: []ChatFile{{Name: "a.png", Type: "image/png", Size: 7, Ref: "F"}}}})
	waitFor(t, "reply and file", func() bool { return len(q.messages()) == 2 })
	m := q.messages()
	if m[0].Type != qq.MsgMarkdown || m[0].Markdown.Content != "**好**" || m[0].MsgID != "M1" || m[0].MsgSeq != 1 {
		t.Fatalf("reply %+v", m[0])
	}
	if m[1].Type != qq.MsgMedia || m[1].Media.FileInfo != "FI" || m[1].MsgSeq != 2 {
		t.Fatalf("file %+v", m[1])
	}
	// A command's own answer comes at once.
	q.message("M2", owner, "/ls")
	waitFor(t, "command reply", func() bool { return len(q.messages()) == 3 })
	if m := q.messages()[2]; m.Markdown == nil || m.Markdown.Content != "**sessions**" || m.MsgID != "M2" {
		t.Fatalf("command reply %+v", m)
	}
}

func TestStrangersAndPairing(t *testing.T) {
	b, q, ir := start(t)
	q.message("X1", "STRANGER", "hi")
	q.message("M1", owner, "after")
	waitFor(t, "owner's input", func() bool { return len(ir.got()) == 1 })
	if ir.got()[0].Text != "after" {
		t.Fatalf("a stranger's message went on: %+v", ir.got())
	}
	if err := Write(b.Home, PairFile, Pair{Code: "123456", Expires: time.Now().Add(time.Minute).UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	q.message("X2", "STRANGER", "123456")
	waitFor(t, "paired", func() bool { return len(q.messages()) == 1 })
	c, _, _ := LoadConfig(b.Home)
	if !c.IsOwner("STRANGER") {
		t.Fatalf("not an owner: %+v", c.Owners)
	}
	q.message("X3", "STRANGER", "now")
	waitFor(t, "new owner's input", func() bool { return len(ir.got()) == 2 })
}

func TestMarkdownRefusedGoesAsText(t *testing.T) {
	b, q, ir := start(t)
	q.mu.Lock()
	q.noMarkdown = true
	q.mu.Unlock()
	q.message("M1", owner, "x")
	waitFor(t, "input", func() bool { return len(ir.got()) == 1 })
	ir.send("reply", ChatEvent{Chat: "c2c:" + owner, Reply: &ChatReply{Markdown: "**a**", Text: "a"}})
	waitFor(t, "plain", func() bool { return len(q.messages()) == 1 })
	if m := q.messages()[0]; m.Type != qq.MsgText || m.Content != "a" || m.MsgID != "M1" {
		t.Fatalf("plain %+v", m)
	}
	if !waitState(b, func(s State) bool { return s.Markdown == "no" }) {
		t.Fatal("markdown not marked refused")
	}
	ir.send("reply", ChatEvent{Chat: "c2c:" + owner, Reply: &ChatReply{Markdown: "**b**", Text: "b"}})
	waitFor(t, "second plain", func() bool { return len(q.messages()) == 2 })
	if m := q.messages()[1]; m.Type != qq.MsgText || m.Content != "b" {
		t.Fatalf("second %+v", m)
	}
}

func waitState(b *Bridge, ok func(State) bool) bool {
	for i := 0; i < 200; i++ {
		b.mu.Lock()
		s := b.state
		b.mu.Unlock()
		if ok(s) {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func TestQuota(t *testing.T) {
	_, q, ir := start(t)
	q.message("M1", owner, "x")
	waitFor(t, "input", func() bool { return len(ir.got()) == 1 })
	// Four passive replies, then active ones.
	for i := 0; i < 5; i++ {
		ir.send("reply", ChatEvent{Chat: "c2c:" + owner, Reply: &ChatReply{Markdown: fmt.Sprint(i), Text: fmt.Sprint(i)}})
	}
	waitFor(t, "five", func() bool { return len(q.messages()) == 5 })
	m := q.messages()
	for i := 0; i < 4; i++ {
		if m[i].MsgID != "M1" || m[i].MsgSeq != i+1 {
			t.Fatalf("passive %d: %+v", i, m[i])
		}
	}
	if m[4].MsgID != "" {
		t.Fatalf("fifth not active: %+v", m[4])
	}
	// No quota at all: it waits for the next message, and goes as its reply.
	q.mu.Lock()
	q.activeLeft = 0
	q.mu.Unlock()
	ir.send("reply", ChatEvent{Chat: "c2c:" + owner, Reply: &ChatReply{Markdown: "later", Text: "later"}})
	time.Sleep(200 * time.Millisecond)
	if len(q.messages()) != 5 {
		t.Fatalf("sent without quota: %+v", q.messages())
	}
	q.message("M2", owner, "y")
	waitFor(t, "flushed", func() bool { return len(q.messages()) == 6 })
	if m := q.messages()[5]; m.MsgID != "M2" || m.Markdown == nil || m.Markdown.Content != "later" {
		t.Fatalf("flushed %+v", m)
	}
}

func TestCards(t *testing.T) {
	_, q, ir := start(t)
	q.message("M1", owner, "x")
	waitFor(t, "input", func() bool { return len(ir.got()) == 1 })
	card := api.Card{ID: "ask:s1:r1", Kind: "permission", Title: "运行 ls？", Options: []api.CardOption{{ID: "allow", Text: "允许", Primary: true}, {ID: "deny", Text: "拒绝"}}}
	ir.send("card", ChatEvent{Chat: "c2c:" + owner, Card: &card})
	waitFor(t, "card", func() bool { return len(q.messages()) == 1 })
	m := q.messages()[0]
	if m.Keyboard == nil || len(m.Keyboard.Content.Rows) != 2 || !strings.Contains(m.Markdown.Content, "└── 拒绝") || strings.Contains(m.Markdown.Content, "2.") {
		t.Fatalf("card %+v", m)
	}
	// Its name answers it.
	q.message("M2", owner, "拒绝")
	waitFor(t, "answer", func() bool { ir.mu.Lock(); defer ir.mu.Unlock(); return len(ir.answers) == 1 })
	if a := ir.answers[0]; a.Card != card.ID || a.Option != "deny" {
		t.Fatalf("answer %+v", a)
	}
	if len(ir.got()) != 1 {
		t.Fatalf("the name went on as a message: %+v", ir.got())
	}
	// A button answers the next one.
	ir.send("card", ChatEvent{Chat: "c2c:" + owner, Card: &card})
	waitFor(t, "card again", func() bool { return len(q.messages()) == 2 })
	data := q.messages()[1].Keyboard.Content.Rows[0].Buttons[0].Action.Data
	q.pressButton("I1", owner, data)
	waitFor(t, "pressed", func() bool { ir.mu.Lock(); defer ir.mu.Unlock(); return len(ir.answers) == 2 })
	if a := ir.answers[1]; a.Option != "allow" {
		t.Fatalf("press %+v", a)
	}
	q.mu.Lock()
	acks := len(q.acks)
	q.mu.Unlock()
	if acks != 1 {
		t.Fatalf("press not acknowledged: %d", acks)
	}
}

func TestTokenRefused(t *testing.T) {
	b, q, _ := start(t)
	if err := WriteFile(b.Home, TokenFile, []byte("WRONG")); err != nil {
		t.Fatal(err)
	}
	q.message("M1", owner, "x")
	waitFor(t, "told", func() bool { return len(q.messages()) == 1 })
	if m := q.messages()[0]; !strings.Contains(m.Content, "TOKEN") {
		t.Fatalf("told %+v", m)
	}
}

func TestChunks(t *testing.T) {
	s := strings.Repeat("a", 1500) + "\n" + strings.Repeat("b", 1500)
	c := Chunks(s, 2000)
	if len(c) != 2 || c[0] != strings.Repeat("a", 1500) || c[1] != strings.Repeat("b", 1500) {
		t.Fatalf("%d chunks", len(c))
	}
	if c := Chunks(strings.Repeat("字", 4500), 2000); len(c) != 3 {
		t.Fatalf("%d chunks", len(c))
	}
}

func TestHomeFiles(t *testing.T) {
	home := t.TempDir()
	if err := Write(home, ConfigFile, Config{AppID: "a", Secret: "s"}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(home, ConfigFile))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("config %v %v", fi.Mode(), err)
	}
	c, ok, err := LoadConfig(home)
	if !ok || err != nil || c.Name != "QQ" {
		t.Fatalf("%+v %v %v", c, ok, err)
	}
	if TakePair(home, "1") {
		t.Fatal("no pair waiting")
	}
}

// The bot's avatar goes on its terminal once, and again for a new TOKEN (a terminal made again).
func TestAvatar(t *testing.T) {
	b, _, ir := start(t)
	n := func() int { ir.mu.Lock(); defer ir.mu.Unlock(); return ir.avatars }
	waitFor(t, "avatar", func() bool { return n() == 1 })
	b.avatar(context.Background())
	if n() != 1 {
		t.Fatalf("sent again: %d", n())
	}
	var s Status
	if ok, _ := Read(b.Home, StatusFile, &s); !ok || s.Bot != "bot" {
		t.Fatalf("status %+v", s)
	}
	if err := WriteFile(b.Home, TokenFile, []byte("TOKEN \n")); err != nil { // same token, other bytes
		t.Fatal(err)
	}
	b.avatar(context.Background())
	if n() != 1 {
		t.Fatalf("sent for the same token: %d", n())
	}
}

// The menus: the row with the sessions first, the / panel of Iridium's commands over Rhodium's,
// pushed once.
func TestMenus(t *testing.T) {
	b, q, _ := start(t)
	got := func() []string { q.mu.Lock(); defer q.mu.Unlock(); return append([]string(nil), q.menus...) }
	waitFor(t, "menus", func() bool { return len(got()) == 2 })
	m := got()
	if !strings.HasPrefix(m[0], "PUT /v2/menu ") || !strings.Contains(m[0], `"name":"会话","send_message":"/use"`) {
		t.Fatalf("row %s", m[0])
	}
	if !strings.HasPrefix(m[1], "PUT /v2/panels/RH ") || !strings.Contains(m[1], `"remark":"iridium"`) || !strings.Contains(m[1], `"name":"new"`) || strings.Contains(m[1], "review") {
		t.Fatalf("panel %s", m[1])
	}
	b.menus(context.Background())
	if len(got()) != 2 {
		t.Fatalf("pushed again: %v", got())
	}
}

// A card as a tree; an option by its name, its tag or a folder's "/" left out.
func TestCardTree(t *testing.T) {
	c := api.Card{Title: "Iridium", Summary: []string{"~/kumo/Iridium"}, Options: []api.CardOption{
		{ID: "s1", Text: "[CC] 打招呼", Description: "9 小时前", Primary: true}, {ID: "g:w1/web", Text: "web/"}, {ID: "n:w1", Text: "[NEW] 新会话"}}}
	md, text := cardText(c)
	if want := "Iridium\n\n~/kumo/Iridium\n\n├── [CC] 打招呼 9 小时前\n├── web/\n└── [NEW] 新会话"; text != want {
		t.Errorf("text:\n%s", text)
	}
	if !strings.Contains(md, "├── **[CC] 打招呼** 9 小时前  \n├── web/  \n└── [NEW] 新会话") {
		t.Errorf("markdown:\n%s", md)
	}
	for name, id := range map[string]string{"打招呼": "s1", "[cc] 打招呼": "s1", "web": "g:w1/web", "新会话": "n:w1"} {
		if o, ok := pickByName(c.Options, name); !ok || o.ID != id {
			t.Errorf("%q: %+v %v", name, o, ok)
		}
	}
	if _, ok := pickByName(c.Options, "你好"); ok {
		t.Error("a message taken for an option")
	}
}
