package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/satori"
)

// satoriCall posts body to /v1/path with an app's TOKEN as login (Satori's headers, the platform
// its ID): the status, the reply into out, the error.
func (l *live) satoriCall(tok api.Token, login, path string, body, out any) (int, api.Error) {
	l.t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", l.base+"/v1/"+path, bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+tok.Token)
	req.Header.Set(satori.HeaderPlatform, tok.App.ID)
	if login != "" {
		req.Header.Set(satori.HeaderUser, login)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		l.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var e api.Error
	json.Unmarshal(raw, &e)
	if out != nil && resp.StatusCode == 200 {
		json.Unmarshal(raw, out)
	}
	return resp.StatusCode, e
}

type satoriWS struct {
	t *testing.T
	c *websocket.Conn
}

func (l *live) satoriDial(header http.Header) *satoriWS {
	l.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(l.base, "http")+"/v1/events", &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		l.t.Fatal(err)
	}
	l.t.Cleanup(func() { c.CloseNow() })
	return &satoriWS{t: l.t, c: c}
}

func (w *satoriWS) send(op int, body any) {
	b, _ := json.Marshal(map[string]any{"op": op, "body": body})
	if err := w.c.Write(context.Background(), websocket.MessageText, b); err != nil {
		w.t.Fatal(err)
	}
}

func (w *satoriWS) read(out any) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, b, err := w.c.Read(ctx)
	if err != nil {
		return -1, err
	}
	var sig satori.Signal
	json.Unmarshal(b, &sig)
	if out != nil {
		json.Unmarshal(sig.Body, out)
	}
	return sig.Op, nil
}

// event waits for the next event that ok takes.
func (w *satoriWS) event(ok func(satori.Event) bool) satori.Event {
	w.t.Helper()
	for {
		var ev satori.Event
		op, err := w.read(&ev)
		if err != nil {
			w.t.Fatalf("no event: %v", err)
		}
		if op == satori.OpEvent && ok(ev) {
			return ev
		}
	}
}

// Satori with ir against a real daemon: the login by header (a new one waits), the events over the
// WebSocket by the app's TOKEN, a message into a session and its reply, an ask answered by its
// button, an upload, and the app removed closing its stream.
func TestSatori(t *testing.T) {
	l := start(t)
	app := l.newApp("bridge", true)

	// A login the first time: login_pending, as anywhere; allowed, it speaks.
	if code, e := l.satoriCall(app, "alice", "login.get", nil, nil); code != 403 || e.Code != "login_pending" {
		t.Fatalf("a new login: %d %+v", code, e)
	}
	l.want(200, "socket", "POST", "/api/needs/"+l.pending().ID, api.NeedAnswer{Allow: true}, nil)
	var lg satori.Login
	if code, e := l.satoriCall(app, "alice", "login.get", nil, &lg); code != 200 || lg.Platform != app.App.ID || lg.User.ID != "alice" {
		t.Fatalf("login.get: %d %+v %+v", code, e, lg)
	}
	// The platform is the app's ID; no login, no say; the host and a bad TOKEN are no apps.
	other := app
	other.App.ID = "someone-else"
	if code, e := l.satoriCall(other, "alice", "login.get", nil, nil); code != 403 || e.Code != "no_such_login" {
		t.Errorf("another platform: %d %+v", code, e)
	}
	if code, e := l.satoriCall(app, "", "login.get", nil, nil); code != 403 || e.Code != "no_such_login" {
		t.Errorf("no login: %d %+v", code, e)
	}
	var raw string
	if code := l.do("web", "POST", "/v1/login.get", nil, &raw); code != 403 || !strings.Contains(raw, "app_only") {
		t.Errorf("the host: %d %s", code, raw)
	}
	if code, _ := l.satoriCall(api.Token{App: app.App, Token: "ir_nope"}, "alice", "login.get", nil, nil); code != 401 {
		t.Errorf("a bad TOKEN: %d", code)
	}
	if code, e := l.satoriCall(app, "alice", "nothing.here", nil, nil); code != 404 || e.Code != "no_such_api" {
		t.Errorf("no API: %d %+v", code, e)
	}

	var created api.NewSessionReply
	l.want(200, "socket", "POST", "/api/sessions", api.NewSession{Agent: "claude", Cwd: l.work}, &created)
	id := created.ID
	if code, e := l.satoriCall(app, "alice", "channel.get", map[string]string{"channel_id": "nope"}, nil); code != 422 || e.Code != "no_such_session" {
		t.Errorf("no session: %d %+v", code, e)
	}
	var ch satori.Channel
	if code, _ := l.satoriCall(app, "alice", "channel.get", map[string]string{"channel_id": id}, &ch); code != 200 || ch.ID != id {
		t.Errorf("channel.get: %d %+v", code, ch)
	}

	// The stream: no header, the TOKEN in IDENTIFY.
	w := l.satoriDial(nil)
	w.send(satori.OpIdentify, satori.Identify{Token: app.Token})
	var ready satori.Ready
	if op, err := w.read(&ready); op != satori.OpReady || err != nil || len(ready.Logins) != 1 || ready.Logins[0].User.ID != "alice" {
		t.Fatalf("READY: %d %v %+v", op, err, ready)
	}
	w.send(satori.OpPing, nil)
	if op, _ := w.read(nil); op != satori.OpPong {
		t.Fatalf("PONG: %d", op)
	}

	var sent []satori.Message
	if code, e := l.satoriCall(app, "alice", "message.create", map[string]string{"channel_id": id, "content": "hello &amp; bye"}, &sent); code != 200 || len(sent) != 1 || sent[0].ID == "" {
		t.Fatalf("message.create: %d %+v %+v", code, e, sent)
	}
	reply := w.event(func(ev satori.Event) bool {
		return ev.Type == "message-created" && ev.XData != nil && ev.XData.Kind == "reply"
	})
	if reply.Message.Content != "ok: hello &amp; bye" || reply.Channel.ID != id || reply.Login.User.ID != "alice" || reply.User.ID != "claude" || reply.XData.Origin != app.App.ID {
		t.Fatalf("the reply: %+v %+v", reply, reply.Message)
	}
	if strings.HasPrefix(reply.Message.ID, "live:") {
		t.Errorf("the reply's id is not its block's: %s", reply.Message.ID)
	}
	var m satori.Message
	if code, e := l.satoriCall(app, "alice", "message.get", map[string]string{"channel_id": id, "message_id": reply.Message.ID}, &m); code != 200 || m.Content != reply.Message.Content {
		t.Errorf("message.get: %d %+v %+v", code, e, m)
	}
	var page satori.BidiList[satori.Message]
	l.satoriCall(app, "alice", "message.list", map[string]string{"channel_id": id}, &page)
	if n := len(page.Data); n < 2 || page.Data[n-1].ID != reply.Message.ID || page.Data[n-2].User == nil || !strings.Contains(page.Data[n-2].User.Name, "alice") {
		t.Errorf("message.list: %+v", page.Data)
	}

	// An ask: its buttons; the one pressed answers it, and the turn goes on.
	l.satoriCall(app, "alice", "message.create", map[string]string{"channel_id": id, "content": "please ASK"}, nil)
	ask := w.event(func(ev satori.Event) bool {
		return ev.XData != nil && ev.XData.Kind == "ask" && ev.Type == "message-created"
	})
	if !strings.Contains(ask.Message.Content, `#allow"`) {
		t.Fatalf("the ask: %+v", ask.Message)
	}
	if code, e := l.satoriCall(app, "alice", "internal/button", map[string]string{"id": ask.Message.ID + "#allow"}, nil); code != 200 {
		t.Fatalf("press: %d %+v", code, e)
	}
	w.event(func(ev satori.Event) bool {
		return ev.Type == "interaction/button" && ev.Button.ID == ask.Message.ID+"#allow"
	})
	w.event(func(ev satori.Event) bool { return ev.Type == "message-updated" && ev.Message.ID == ask.Message.ID })
	w.event(func(ev satori.Event) bool {
		return ev.XData != nil && ev.XData.Kind == "reply" && ev.Message.Content == "ok: please ASK"
	})

	// Two questions, one of many choices: the presses add up, submit answers.
	l.satoriCall(app, "alice", "message.create", map[string]string{"channel_id": id, "content": "QUESTION"}, nil)
	q := w.event(func(ev satori.Event) bool {
		return ev.XData != nil && ev.XData.Kind == "ask" && ev.Type == "message-created"
	})
	for _, b := range []string{"#0.1", "#1.0", "#1.2", "#submit"} {
		if code, e := l.satoriCall(app, "alice", "internal/button", map[string]string{"id": q.Message.ID + b}, nil); code != 200 {
			t.Fatalf("press %s: %d %+v\n%s", b, code, e, q.Message.Content)
		}
	}
	got := w.event(func(ev satori.Event) bool { return ev.XData != nil && ev.XData.Kind == "reply" })
	if !strings.Contains(got.Message.Content, "answers") || !strings.Contains(got.Message.Content, "Blue") {
		t.Errorf("answered: %q\n%s", got.Message.Content, q.Message.Content)
	}

	// A file: uploaded for the session, sent with a message, fetched through the proxy.
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	mw.WriteField("channel_id", id)
	fw, _ := mw.CreateFormFile("doc", "note.txt")
	fw.Write([]byte("the note"))
	mw.Close()
	req, _ := http.NewRequest("POST", l.base+"/v1/upload.create", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+app.Token)
	req.Header.Set(satori.HeaderPlatform, app.App.ID)
	req.Header.Set(satori.HeaderUser, "alice")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var up map[string]string
	json.NewDecoder(resp.Body).Decode(&up)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.HasPrefix(up["doc"], "internal:"+app.App.ID+"/alice/files/"+id+"/") {
		t.Fatalf("upload.create: %d %v", resp.StatusCode, up)
	}
	req, _ = http.NewRequest("GET", l.base+"/v1/proxy/"+up["doc"], nil)
	req.Header.Set("Authorization", "Bearer "+app.Token)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(b) != "the note" {
		t.Errorf("proxy: %d %q", resp.StatusCode, b)
	}
	if code, e := l.satoriCall(app, "alice", "message.create", map[string]string{"channel_id": id, "content": `read <file src="` + up["doc"] + `"/>`}, nil); code != 200 {
		t.Fatalf("with the file: %d %+v", code, e)
	}
	got = w.event(func(ev satori.Event) bool { return ev.XData != nil && ev.XData.Kind == "reply" })
	if !strings.Contains(got.Message.Content, "note.txt") {
		t.Errorf("the reply to the file: %q", got.Message.Content)
	}

	// Removed: its stream closes.
	l.want(200, "socket", "DELETE", "/api/apps/bridge", nil, nil)
	for {
		if _, err := w.read(nil); err != nil {
			break
		}
	}
}
