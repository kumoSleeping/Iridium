package server

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/chat"
	"github.com/kumoSleeping/iridium/internal/push"
	"github.com/kumoSleeping/iridium/internal/store"
)

// fakeGate names the caller by a header, as the gate will by its TOKEN.
var fakeGate = IdentityFunc(func(r *http.Request) (chat.Caller, bool) {
	switch r.Header.Get("X-Test-Terminal") {
	case "qq":
		return chat.Caller{ID: "ep-qq", Name: "QQ"}, true
	case "tg":
		return chat.Caller{ID: "ep-tg", Name: "TG"}, true
	case "":
		return chat.Caller{ID: "panel", Name: "网页"}, true
	}
	return chat.Caller{}, false
})

type pluginRig struct {
	t    *testing.T
	base string
	work string
	cfgs string
}

func newPluginRig(t *testing.T) *pluginRig {
	s, cfg, work := setup(t)
	st, err := store.Open(filepath.Join(cfg.StateDir, "iridium.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	s.Chat, err = chat.New(chat.Options{Manager: s.m, Store: st, StateDir: cfg.StateDir, ChatDir: work, Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Chat.Close)
	s.Push, err = push.Open(push.Options{StateDir: cfg.StateDir, Store: st, Manager: s.m, Logf: t.Logf, AllowHTTP: allowHTTP})
	if err != nil {
		t.Fatal(err)
	}
	s.Who = fakeGate
	tcp, unix, err := s.Listen()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Serve(ctx, tcp, unix); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return &pluginRig{t: t, base: "http://" + tcp.Addr().String(), work: work, cfgs: cfg.StateDir}
}

func (p *pluginRig) do(who, method, path string, body, out any) int {
	p.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = strings.NewReader(string(b))
	}
	req, _ := http.NewRequest(method, p.base+path, r)
	if who != "" {
		req.Header.Set("X-Test-Terminal", who)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		p.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if out != nil {
		json.Unmarshal(b, out)
	}
	if resp.StatusCode != 200 {
		p.t.Logf("%s %s: %d %s", method, path, resp.StatusCode, b)
	}
	return resp.StatusCode
}

type sse struct{ id, event, data string }

// stream reads who's plugin events from after last ("": from now on).
func (p *pluginRig) stream(who, last string) (<-chan sse, func()) {
	req, _ := http.NewRequest("GET", p.base+"/api/events", nil)
	req.Header.Set("X-Test-Terminal", who)
	if last != "" {
		req.Header.Set("Last-Event-ID", last)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		p.t.Fatalf("events: %v %v", resp, err)
	}
	ch := make(chan sse, 100)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		var e sse
		for sc.Scan() {
			l := sc.Text()
			switch {
			case strings.HasPrefix(l, "id: "):
				e.id = l[4:]
			case strings.HasPrefix(l, "event: "):
				e.event = l[7:]
			case strings.HasPrefix(l, "data: "):
				e.data = l[6:]
			case l == "" && e.event != "":
				ch <- e
				e = sse{}
			}
		}
	}()
	return ch, func() { resp.Body.Close() }
}

// next is the next event of type typ, skipping the rest.
func (p *pluginRig) next(ch <-chan sse, typ string) (sse, api.ChatEvent) {
	p.t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case e := <-ch:
			if e.event == typ {
				var ce api.ChatEvent
				json.Unmarshal([]byte(e.data), &ce)
				return e, ce
			}
		case <-deadline:
			p.t.Fatalf("no %s event", typ)
		}
	}
}

func TestPluginChat(t *testing.T) {
	p := newPluginRig(t)
	const key = "/api/chat/qq:private:1"
	var cmds []api.ChatCommand
	if p.do("qq", "GET", key+"/commands", nil, &cmds); len(cmds) != 9 || cmds[0].Name != "new" || !cmds[0].Iridium {
		t.Fatalf("commands: %+v", cmds)
	}
	if code := p.do("nobody", "GET", key+"/commands", nil, nil); code != http.StatusUnauthorized {
		t.Errorf("no terminal: %d", code)
	}
	events, closeEvents := p.stream("qq", "")
	defer closeEvents()

	// No pointer yet: a session opens in the chat directory, and the reply comes on the stream.
	var in api.ChatInputReply
	if code := p.do("qq", "POST", key+"/input", api.ChatInput{Text: "hello", Sender: "张三"}, &in); code != 200 || in.Session == "" {
		t.Fatalf("input: %d %+v", code, in)
	}
	first, r := p.next(events, "reply")
	if r.Chat != "qq:private:1" || r.Session != in.Session || r.Reply == nil || r.Reply.Markdown != "ok: hello" {
		t.Fatalf("reply: %+v", r)
	}
	var page api.MessagePage
	p.do("", "GET", "/api/sessions/"+in.Session+"/messages", nil, &page)
	if page.Blocks[0].From != "QQ·张三" || page.Blocks[0].Text != "hello" {
		t.Errorf("source: %+v", page.Blocks[0])
	}
	var ptr api.ChatPointer
	if p.do("qq", "GET", key, nil, &ptr); ptr.Session != in.Session || ptr.Cwd != p.work {
		t.Errorf("pointer: %+v", ptr)
	}
	// Another plugin's same key is another chat.
	if ptr = (api.ChatPointer{}); p.do("tg", "GET", key, nil, &ptr) != 200 || ptr.Session != "" {
		t.Errorf("tg's pointer: %+v", ptr)
	}

	// Iridium's commands answer at once; others pass through to the agent as typed.
	var st api.ChatInputReply
	p.do("qq", "POST", key+"/input", api.ChatInput{Text: "/status"}, &st)
	if st.Reply == nil || !strings.Contains(st.Reply.Text, in.Session[:8]) || strings.Contains(st.Reply.Text, "**") {
		t.Errorf("status: %+v", st.Reply)
	}
	p.do("qq", "POST", key+"/input", api.ChatInput{Text: "/compact"}, nil)
	if _, r := p.next(events, "reply"); r.Reply.Markdown != "ok: /compact" {
		t.Errorf("passed through: %+v", r.Reply)
	}

	// /use gives a card; picking from it moves the pointer.
	var use api.ChatInputReply
	p.do("qq", "POST", key+"/input", api.ChatInput{Text: "/use"}, &use)
	if use.Card == nil || use.Card.Kind != "pick" || len(use.Card.Options) != 1 {
		t.Fatalf("use: %+v", use)
	}
	var picked api.CardReply
	if code := p.do("qq", "POST", "/api/answer", api.CardAnswer{Card: use.Card.ID, Option: in.Session}, &picked); code != 200 || picked.Reply == nil || !strings.HasPrefix(picked.Reply.Text, "现在") {
		t.Errorf("picked: %d %+v", code, picked)
	}
	if code := p.do("tg", "POST", "/api/answer", api.CardAnswer{Card: use.Card.ID, Option: in.Session}, nil); code != http.StatusGone {
		t.Errorf("another plugin's card: %d", code)
	}

	// An ask comes as a card; answered, it is done, and the turn goes on.
	p.do("qq", "POST", key+"/input", api.ChatInput{Text: "please ASK"}, nil)
	_, c := p.next(events, "card")
	if c.Card == nil || c.Card.Kind != "permission" || c.Card.Summary[0] != "ls" {
		t.Fatalf("card: %+v", c.Card)
	}
	if code := p.do("qq", "POST", "/api/answer", api.CardAnswer{Card: c.Card.ID, Option: "allow"}, nil); code != 200 {
		t.Fatalf("answer: %d", code)
	}
	if _, d := p.next(events, "card_done"); d.CardID != c.Card.ID {
		t.Errorf("card_done: %+v", d)
	}
	p.next(events, "reply")

	// A file the reply names comes with a ref, good for this plugin only.
	file := filepath.Join(p.work, "notes.txt")
	os.WriteFile(file, []byte("the notes"), 0o600)
	p.do("qq", "POST", key+"/input", api.ChatInput{Text: "see `" + file + "`"}, nil)
	_, r = p.next(events, "reply")
	if len(r.Reply.Files) != 1 || r.Reply.Files[0].Name != "notes.txt" || r.Reply.Files[0].Size != 9 || r.Reply.Text != "ok: see "+file {
		t.Fatalf("files: %+v", r.Reply)
	}
	req, _ := http.NewRequest("GET", p.base+key+"/files/"+r.Reply.Files[0].Ref, nil)
	req.Header.Set("X-Test-Terminal", "qq")
	req.Header.Set("Range", "bytes=4-")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent || string(b) != "notes" {
		t.Errorf("file: %d %q", resp.StatusCode, b)
	}
	if code := p.do("tg", "GET", key+"/files/"+r.Reply.Files[0].Ref, nil, nil); code != 404 {
		t.Errorf("another plugin's ref: %d", code)
	}

	// Reconnecting with Last-Event-ID gives what came after it again.
	closeEvents()
	again, closeAgain := p.stream("qq", first.id)
	defer closeAgain()
	if _, r := p.next(again, "reply"); r.Reply.Markdown != "ok: /compact" {
		t.Errorf("after reconnect: %+v", r.Reply)
	}
}

// The queue over HTTP, and the push settings.
func TestQueueAndPushRoutes(t *testing.T) {
	p := newPluginRig(t)
	var created api.NewSessionReply
	p.do("", "POST", "/api/sessions", api.NewSession{Cwd: p.work, Text: "one WAIT"}, &created)
	id := created.ID
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		var s api.SessionState
		p.do("", "GET", "/api/sessions/"+id, nil, &s)
		if s.Status == api.StatusRunning {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no turn")
		}
	}
	var sent api.SendReply
	if p.do("", "POST", "/api/sessions/"+id+"/send", api.Send{Text: "two"}, &sent); !sent.Queued {
		t.Fatalf("not queued: %+v", sent)
	}
	if code := p.do("", "PUT", "/api/sessions/"+id+"/queue/"+sent.Input, api.EditQueued{Text: "two!"}, nil); code != 200 {
		t.Errorf("edit: %d", code)
	}
	if code := p.do("", "DELETE", "/api/sessions/"+id+"/queue/nope", nil, nil); code != 404 {
		t.Errorf("delete nothing: %d", code)
	}
	if code := p.do("", "POST", "/api/sessions/"+id+"/queue/"+sent.Input+"/steer", nil, nil); code != 200 {
		t.Errorf("steer: %d", code)
	}
	if code := p.do("", "PUT", "/api/sessions/"+id+"/mode", api.SetMode{Mode: "nope"}, nil); code != 400 {
		t.Errorf("bad mode: %d", code)
	}
	if code := p.do("", "POST", "/api/sessions/"+id+"/continue", nil, nil); code != 200 {
		t.Errorf("continue: %d", code)
	}
	var rules []api.AllowRule
	if code := p.do("", "GET", "/api/sessions/"+id+"/rules", nil, &rules); code != 200 || rules == nil {
		t.Errorf("rules: %d %v", code, rules)
	}

	var ps api.PushState
	if p.do("", "GET", "/api/push", nil, &ps); ps.Enabled || len(ps.PublicKey) < 80 || len(ps.Workspaces) != 1 {
		t.Errorf("push: %+v", ps)
	}
	p.do("", "PUT", "/api/push/workspaces/0", api.PushEvents{NeedsYou: true, TurnEnd: true}, nil)
	if p.do("", "GET", "/api/push", nil, &ps); !ps.Workspaces[0].TurnEnd || ps.Workspaces[0].Error {
		t.Errorf("push after: %+v", ps)
	}
	sub := api.PushSubscription{Endpoint: "http://insecure.example/x"}
	if code := p.do("", "POST", "/api/push/subscriptions", sub, nil); code != 400 {
		t.Errorf("an http endpoint: %d", code)
	}
}
