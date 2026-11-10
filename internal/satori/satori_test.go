package satori

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/session"
)

var errGone = errors.New("gone")

// fake is a Backend: apps by TOKEN (and by the X-App header, as the gate would), sessions with a
// transcript of one block a line (offsets 10 apart), pages of 3.
type fake struct {
	mu       sync.Mutex
	tokens   map[string]string // TOKEN: app
	logins   map[string][]LoginInfo
	sessions map[string]*api.SessionState
	blocks   map[string][]api.Block
	sent     []string
	answers  []api.CardAnswer
	hidden   map[string]bool // sessions no login sees
}

func newFake() *fake {
	return &fake{
		tokens:   map[string]string{"ir_good": "app1", "ir_other": "app2"},
		logins:   map[string][]LoginInfo{"app1": {{ID: "u1", Name: "Alice"}, {ID: "u2", Name: "Bob"}}},
		sessions: map[string]*api.SessionState{},
		blocks:   map[string][]api.Block{},
		hidden:   map[string]bool{},
	}
}

func (f *fake) addSession(id string, n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessions[id] = &api.SessionState{Session: api.Session{ID: id, Agent: api.AgentClaude, Cwd: "/w/" + id, UpdatedAt: int64(len(f.sessions))}}
	for i := range n {
		kind := api.BlockKindUser
		if i%2 == 1 {
			kind = api.BlockKindAssistant
		}
		f.blocks[id] = append(f.blocks[id], api.Block{At: fmt.Sprintf("%d.0", i*10), Kind: kind, Text: fmt.Sprintf("m%d", i), Time: time.Now().UnixMilli()})
	}
}

func (f *fake) Who(r *http.Request) (Caller, bool) {
	app := r.Header.Get("X-App")
	if app == "" {
		return Caller{}, false
	}
	c := Caller{App: app, AppName: app + "-name"}
	if l := r.Header.Get(HeaderUser); l != "" {
		for _, x := range f.Logins(app) {
			if x.ID == l {
				c.Login = l
			}
		}
	}
	return c, true
}

func (f *fake) AppByToken(_ *http.Request, tok string) (Caller, bool) {
	app, ok := f.tokens[tok]
	return Caller{App: app}, ok
}

func (f *fake) Logins(app string) []LoginInfo {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]LoginInfo(nil), f.logins[app]...)
}

func (f *fake) Track(r *http.Request, app string, closeFn func()) func() { return func() {} }

func (f *fake) User(id string) (User, bool) {
	if id == "app2" {
		return User{ID: id, Name: "other"}, true
	}
	return User{}, false
}

func (f *fake) Sessions(context.Context) ([]api.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []api.Session
	for _, s := range f.sessions {
		out = append(out, s.Session)
	}
	return out, nil
}

func (f *fake) Session(id string) (*api.SessionState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.sessions[id]
	if !ok {
		return nil, session.ErrNotFound
	}
	c := *s
	return &c, nil
}

func (f *fake) Workspaces() ([]api.Workspace, error) {
	return []api.Workspace{{ID: 7, Name: "proj", Path: "/w"}}, nil
}

func (f *fake) Sees(c Caller, id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return !f.hidden[id]
}

func (f *fake) Messages(id string, before, after int64) (api.MessagePage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	bs := f.blocks[id]
	size := int64(len(bs) * 10)
	if after >= 0 {
		lo := min(int(after+9)/10, len(bs))
		hi := min(lo+3, len(bs))
		end := size
		if hi < len(bs) {
			end = int64(hi * 10)
		}
		return api.MessagePage{Blocks: append([]api.Block{}, bs[lo:hi]...), Start: after, End: end, More: lo > 0}, nil
	}
	hi := len(bs)
	if before >= 0 {
		hi = min(int(before+9)/10, len(bs))
	}
	lo := max(hi-3, 0)
	return api.MessagePage{Blocks: append([]api.Block{}, bs[lo:hi]...), Start: int64(lo * 10), End: int64(hi * 10), More: lo > 0}, nil
}

func (f *fake) Full(id, at string) (string, error) { return "", errGone }

func (f *fake) Watch(ctx context.Context, _ *http.Request, _ Caller, keys []string, emit func(string, []byte) error) error {
	for _, k := range keys {
		if err := emit("state", []byte(`{"key":"`+k+`","value":1}`)); err != nil {
			return err
		}
	}
	<-ctx.Done()
	return nil
}

func (f *fake) Send(_ context.Context, c Caller, id, text string, refs []string, _ SendOpts) (SendResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, c.Login+": "+text+" "+strings.Join(refs, ","))
	return SendResult{Input: "in1", Session: id}, nil
}

func (f *fake) Answer(_ context.Context, c Caller, a api.CardAnswer) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answers = append(f.answers, a)
	return nil
}

func (f *fake) Upload(c Caller, id, name string, size int64, body io.Reader) (string, error) {
	b, _ := io.ReadAll(body)
	if int64(len(b)) != size {
		return "", fmt.Errorf("size %d, got %d", size, len(b))
	}
	return "ref" + name, nil
}

func (f *fake) File(c Caller, id, ref string) (string, error) { return "", errGone }

func (f *fake) ServeFile(w http.ResponseWriter, r *http.Request, path string) {}

func (f *fake) Mask(t string) string { return strings.ReplaceAll(t, "SECRET", "******") }

func (f *fake) Fault(err error) (int, string) {
	if errors.Is(err, session.ErrNotFound) {
		return 422, "no_such_session"
	}
	return 500, "internal"
}

type rig struct {
	t   *testing.T
	f   *fake
	s   *Service
	srv *httptest.Server
}

func newRig(t *testing.T) *rig {
	f := newFake()
	s := New(f, t.Logf)
	srv := httptest.NewServer(s)
	t.Cleanup(func() { srv.Close(); s.Close() })
	return &rig{t: t, f: f, s: s, srv: srv}
}

// call posts body to /v1/path as app (platform) speaking as login: status, reply.
func (r *rig) call(app, platform, login, path string, body any, out any) (int, errorBody) {
	r.t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", r.srv.URL+"/v1/"+path, bytes.NewReader(b))
	if app != "" {
		req.Header.Set("X-App", app)
	}
	req.Header.Set(HeaderPlatform, platform)
	if login != "" {
		req.Header.Set(HeaderUser, login)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		r.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var e errorBody
	json.Unmarshal(raw, &e)
	if out != nil && resp.StatusCode == 200 {
		if err := json.Unmarshal(raw, out); err != nil {
			r.t.Fatalf("%s: %v: %s", path, err, raw)
		}
	}
	return resp.StatusCode, e
}

func (r *rig) as(login, path string, body, out any) (int, errorBody) {
	return r.call("app1", "app1", login, path, body, out)
}

// Errors: 404 only for an API there is not; what is not there is 422; who speaks, 403.
func TestErrors(t *testing.T) {
	r := newRig(t)
	r.f.addSession("s1", 2)
	for _, c := range []struct {
		app, platform, login, path string
		body                       any
		status                     int
		code                       string
	}{
		{"app1", "app1", "u1", "nope.get", nil, 404, "no_such_api"},
		{"app1", "app1", "u1", "message.delete", nil, 404, "no_such_api"},
		{"", "app1", "u1", "channel.get", nil, 403, "no_login"},
		{"app1", "app2", "u1", "channel.get", nil, 403, "no_such_login"},
		{"app1", "app1", "", "channel.get", nil, 403, "no_such_login"},
		{"app1", "app1", "stranger", "channel.get", nil, 403, "no_such_login"},
		{"app1", "app1", "u1", "channel.get", map[string]string{"channel_id": "nope"}, 422, "no_such_session"},
		{"app1", "app1", "u1", "channel.get", map[string]string{}, 400, "bad_request"},
		{"app1", "app1", "u1", "message.get", map[string]string{"channel_id": "s1", "message_id": "99.0"}, 422, "no_such_message"},
		{"app1", "app1", "u1", "message.list", map[string]string{"channel_id": "s1", "direction": "after"}, 400, "bad_request"},
		{"app1", "app1", "u1", "guild.get", map[string]string{"guild_id": "x"}, 422, "no_such_guild"},
		{"app1", "app1", "u1", "user.get", map[string]string{"user_id": "x"}, 422, "no_such_user"},
		{"app1", "app1", "u1", "message.create", map[string]string{"channel_id": "s1", "content": " "}, 400, "bad_request"},
	} {
		if status, e := r.call(c.app, c.platform, c.login, c.path, c.body, nil); status != c.status || e.Code != c.code {
			t.Errorf("%s as %s/%s on %s: %d %+v, want %d %s", c.path, c.app, c.login, c.platform, status, e, c.status, c.code)
		}
	}
	resp, _ := http.Get(r.srv.URL + "/v1/channel.get")
	if resp.StatusCode != 405 {
		t.Errorf("GET: %d", resp.StatusCode)
	}
	resp.Body.Close()
	r.f.hidden["s1"] = true
	if status, e := r.as("u1", "channel.get", map[string]string{"channel_id": "s1"}, nil); status != 422 || e.Code != "no_such_session" {
		t.Errorf("a session not seen: %d %+v", status, e)
	}
}

func TestResources(t *testing.T) {
	r := newRig(t)
	r.f.addSession("s1", 7)
	r.f.addSession("s2", 0)
	r.f.sessions["s2"].Workspace = 7
	r.f.sessions["s2"].Name = "named"

	var lg Login
	if r.as("u2", "login.get", nil, &lg); lg.Platform != "app1" || lg.User == nil || lg.User.ID != "u2" || lg.SN != 2 || *lg.Status != StatusOnline {
		t.Errorf("login.get: %+v", lg)
	}
	var meta Meta
	r.call("app1", "", "", "meta", nil, &meta)
	if len(meta.Logins) != 2 || meta.ProxyURLs == nil {
		t.Errorf("meta: %+v", meta)
	}
	var gl List[Guild]
	if r.as("u1", "guild.list", nil, &gl); len(gl.Data) != 2 || gl.Data[0].ID != "7" || gl.Data[1].ID != Loose {
		t.Errorf("guilds: %+v", gl)
	}
	var cl List[Channel]
	if r.as("u1", "channel.list", map[string]string{"guild_id": "7"}, &cl); len(cl.Data) != 1 || cl.Data[0].ID != "s2" || cl.Data[0].Name != "named" {
		t.Errorf("channels of 7: %+v", cl)
	}
	if r.as("u1", "channel.list", map[string]string{"guild_id": Loose}, &cl); len(cl.Data) != 1 || cl.Data[0].Name != "s1" {
		t.Errorf("loose channels: %+v", cl)
	}
	var u User
	if r.as("u1", "user.get", map[string]string{"user_id": "claude"}, &u); !u.IsBot || u.Name == "" {
		t.Errorf("an agent: %+v", u)
	}
	if r.as("u1", "user.get", map[string]string{"user_id": "u2"}, &u); u.Name != "Bob" {
		t.Errorf("a login: %+v", u)
	}

	// The transcript's messages, paged back from the latest; a block's place is its id.
	var page BidiList[Message]
	r.as("u1", "message.list", map[string]string{"channel_id": "s1"}, &page)
	if len(page.Data) != 3 || page.Data[0].ID != "40.0" || page.Prev != "40" || page.Next != "40" {
		t.Fatalf("latest: %+v", page)
	}
	if page.Data[1].User == nil || page.Data[1].User.ID != "claude" || !page.Data[1].User.IsBot {
		t.Errorf("the agent's: %+v", page.Data[1])
	}
	prev := page.Prev
	page = BidiList[Message]{}
	r.as("u1", "message.list", map[string]any{"channel_id": "s1", "next": prev, "order": "desc"}, &page)
	if len(page.Data) != 3 || page.Data[0].ID != "30.0" || page.Data[2].ID != "10.0" || page.Prev != "10" {
		t.Errorf("before 40, desc: %+v", page)
	}
	page = BidiList[Message]{}
	r.as("u1", "message.list", map[string]any{"channel_id": "s1", "next": "10", "direction": "after", "limit": 2}, &page)
	if len(page.Data) != 2 || page.Data[0].ID != "10.0" || page.Next != "30" || page.Prev != "30" {
		t.Errorf("after 10, 2: %+v", page)
	}
	page = BidiList[Message]{}
	r.as("u1", "message.list", map[string]any{"channel_id": "s1", "next": "30", "direction": "around"}, &page)
	if len(page.Data) != 6 || page.Prev != "" || page.Next != "60" {
		t.Errorf("around 30: %+v", page)
	}
	var m Message
	if r.as("u1", "message.get", map[string]string{"channel_id": "s1", "message_id": "30.0"}, &m); m.Content != "m3" || m.Channel.ID != "s1" {
		t.Errorf("message.get: %+v", m)
	}

	// Sent as the login; a file of this login's in this session is an attachment.
	var sent []Message
	r.as("u1", "message.create", map[string]string{"channel_id": "s1", "content": `hi &amp; <img src="internal:app1/u1/files/s1/r9"/><img src="internal:app1/u2/files/s1/r8"/>`}, &sent)
	if len(sent) != 1 || sent[0].ID != "in1" || len(r.f.sent) != 1 || r.f.sent[0] != "u1: hi & internal:app1/u2/files/s1/r8 r9" {
		t.Errorf("message.create: %+v %q", sent, r.f.sent)
	}
}

func TestUpload(t *testing.T) {
	r := newRig(t)
	r.f.addSession("s1", 0)
	var body bytes.Buffer
	body.WriteString("--b\r\nContent-Disposition: form-data; name=\"channel_id\"\r\n\r\ns1\r\n" +
		"--b\r\nContent-Disposition: form-data; name=\"pic\"; filename=\"a.png\"\r\nContent-Type: image/png\r\n\r\nPNGDATA\r\n--b--\r\n")
	req, _ := http.NewRequest("POST", r.srv.URL+"/v1/upload.create", &body)
	req.Header.Set("Content-Type", "multipart/form-data; boundary=b")
	req.Header.Set("X-App", "app1")
	req.Header.Set(HeaderPlatform, "app1")
	req.Header.Set(HeaderUser, "u1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]string
	json.NewDecoder(resp.Body).Decode(&out)
	if resp.StatusCode != 200 || out["pic"] != "internal:app1/u1/files/s1/refa.png" {
		t.Fatalf("upload.create: %d %v", resp.StatusCode, out)
	}
	// The proxy: only internal: URLs of the app's own logins.
	for path, want := range map[string]int{
		"/v1/proxy/https://example.com/x":            403,
		"/v1/proxy/internal:app2/u1/files/s1/x":      404,
		"/v1/proxy/internal:app1/nobody/files/s1/x":  404,
		"/v1/proxy/internal:app1":                    400,
		"/v1/proxy/internal:app1/u1/files/s1/refnot": 404,
	} {
		req, _ := http.NewRequest("GET", r.srv.URL+path, nil)
		req.Header.Set("X-App", "app1")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("%s: %d, want %d", path, resp.StatusCode, want)
		}
	}
}

// ws is a Satori connection in a test.
type ws struct {
	t *testing.T
	c *websocket.Conn
}

func (r *rig) dial(header http.Header) *ws {
	r.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(r.srv.URL, "http")+"/v1/events", &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		r.t.Fatal(err)
	}
	r.t.Cleanup(func() { c.CloseNow() })
	return &ws{t: r.t, c: c}
}

func (w *ws) send(op int, body any) {
	w.t.Helper()
	b, _ := json.Marshal(map[string]any{"op": op, "body": body})
	if err := w.c.Write(context.Background(), websocket.MessageText, b); err != nil {
		w.t.Fatal(err)
	}
}

// next reads the next signal, its body into out.
func (w *ws) next(out any) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, b, err := w.c.Read(ctx)
	if err != nil {
		return -1, err
	}
	var sig Signal
	json.Unmarshal(b, &sig)
	if out != nil {
		json.Unmarshal(sig.Body, out)
	}
	return sig.Op, nil
}

func (w *ws) want(op int, out any) {
	w.t.Helper()
	got, err := w.next(out)
	if err != nil || got != op {
		w.t.Fatalf("op %d (%v), want %d", got, err, op)
	}
}

// IDENTIFY with the TOKEN, READY with the logins; PING, PONG; a bad TOKEN is closed 4004.
func TestHandshake(t *testing.T) {
	r := newRig(t)
	w := r.dial(nil)
	w.send(OpPing, nil)
	w.want(OpPong, nil)
	w.send(OpIdentify, Identify{Token: "ir_good"})
	var ready Ready
	w.want(OpReady, &ready)
	if len(ready.Logins) != 2 || ready.Logins[0].Platform != "app1" || ready.Logins[1].User.ID != "u2" || ready.Logins[1].SN != 2 || ready.ProxyURLs == nil {
		t.Fatalf("READY: %+v", ready)
	}
	w.send(OpPing, nil)
	w.want(OpPong, nil)

	for _, id := range []Identify{{Token: "ir_bad"}, {}} {
		w := r.dial(nil)
		w.send(OpIdentify, id)
		if _, err := w.next(nil); websocket.CloseStatus(err) != 4004 {
			t.Errorf("IDENTIFY %+v: %v", id, err)
		}
	}
	// Come in with the TOKEN already (the gate's): IDENTIFY need not repeat it, nor name another.
	w = r.dial(http.Header{"X-App": {"app1"}})
	w.send(OpIdentify, nil)
	w.want(OpReady, &ready)
	w = r.dial(http.Header{"X-App": {"app1"}})
	w.send(OpIdentify, Identify{Token: "ir_other"})
	if _, err := w.next(nil); websocket.CloseStatus(err) != 4004 {
		t.Errorf("another app's TOKEN: %v", err)
	}
	// IDENTIFY first.
	w = r.dial(nil)
	w.send(OpEvent, nil)
	if _, err := w.next(nil); websocket.CloseStatus(err) != 4000 {
		t.Errorf("no IDENTIFY: %v", err)
	}
}

// What the agent says comes as message-created to each login, progress and reply; resumed by sn,
// what was missed comes again.
func TestEventsAndResume(t *testing.T) {
	r := newRig(t)
	r.f.addSession("s1", 2)
	w := r.dial(nil)
	w.send(OpIdentify, Identify{Token: "ir_good"})
	w.want(OpReady, nil)

	r.s.Notice(session.Notice{Session: "s1", Kind: session.NoticeTurnStart, From: "app1"})
	r.s.Notice(session.Notice{Session: "s1", Kind: session.NoticeMessage, Text: "m1"}) // the block 10.0
	r.s.Notice(session.Notice{Session: "s1", Kind: session.NoticeTool, Tool: "Bash"})
	r.s.Notice(session.Notice{Session: "s1", Kind: session.NoticeTurnEnd, Text: "done SECRET"})
	var evs []Event
	for range 4 {
		var ev Event
		w.want(OpEvent, &ev)
		evs = append(evs, ev)
	}
	p, rep := evs[0], evs[2]
	if p.Type != "message-created" || p.XData.Kind != "progress" || p.Message.ID != "10.0" || p.Login.User.ID != "u1" || evs[1].Login.User.ID != "u2" ||
		p.Channel.ID != "s1" || p.Guild.ID != Loose || p.User.ID != "claude" || p.XData.Origin != "app1" || p.SN != evs[1].SN {
		t.Fatalf("progress: %+v %+v %+v", p, p.Message, p.XData)
	}
	if rep.XData.Kind != "reply" || rep.Message.Content != "done ******" || !strings.HasPrefix(rep.Message.ID, "live:") || rep.SN <= p.SN {
		t.Fatalf("reply: %+v %+v", rep, rep.Message)
	}

	// Gone, then back with the last sn it had: what came since, and nothing again.
	w.c.CloseNow()
	r.s.Notice(session.Notice{Session: "s1", Kind: session.NoticeTurnEnd, Text: "later"})
	time.Sleep(100 * time.Millisecond)
	w = r.dial(nil)
	w.send(OpIdentify, Identify{Token: "ir_good", SN: rep.SN})
	w.want(OpReady, nil)
	var ev Event
	w.want(OpEvent, &ev)
	if ev.Message == nil || ev.Message.Content != "later" || ev.SN <= rep.SN {
		t.Fatalf("resumed: %+v", ev)
	}
	// message.get finds what was said live, kept with its events.
	var m Message
	if status, e := r.as("u1", "message.get", map[string]string{"channel_id": "s1", "message_id": rep.Message.ID}, &m); status != 200 || m.Content != "done ******" {
		t.Errorf("message.get of a live one: %d %+v %+v", status, e, m)
	}
	// Without sn: only what comes from now on.
	w2 := r.dial(nil)
	w2.send(OpIdentify, Identify{Token: "ir_good"})
	w2.want(OpReady, nil)
	r.f.addSession("s2", 0)
	r.f.mu.Lock()
	r.f.hidden["s1"] = true // a session the logins do not see: nothing of it
	r.f.mu.Unlock()
	r.s.Notice(session.Notice{Session: "s1", Kind: session.NoticeError, Text: "unseen"})
	r.s.Notice(session.Notice{Session: "s2", Kind: session.NoticeError, Text: "boom"})
	w2.want(OpEvent, &ev)
	if ev.XData.Kind != "error" || ev.Message.Content != "boom" {
		t.Errorf("error: %+v", ev)
	}
}

// An ask: a message with its buttons; pressed, answered; closed, its message updated.
func TestAskButtons(t *testing.T) {
	r := newRig(t)
	r.f.addSession("s1", 0)
	ask := api.Ask{RequestID: "r1", Kind: "permission", Tool: "Bash", Input: map[string]any{"command": "ls"}}
	r.f.sessions["s1"].Asks = []api.Ask{ask}
	w := r.dial(nil)
	w.send(OpIdentify, Identify{Token: "ir_good"})
	w.want(OpReady, nil)
	r.s.Notice(session.Notice{Session: "s1", Kind: session.NoticeAsk, Ask: &ask})
	var ev Event
	w.want(OpEvent, &ev)
	w.want(OpEvent, nil)
	card := "ask:s1:r1"
	if ev.Message.ID != card || ev.XData.Kind != "ask" || !strings.Contains(ev.Message.Content, `<button type="action" id="ask:s1:r1#allow" theme="primary">允许</button>`) ||
		!strings.Contains(ev.Message.Content, `<button type="input" id="ask:s1:r1#deny" text="ask:s1:r1#deny ">`) {
		t.Fatalf("the ask: %+v", ev.Message)
	}
	var m Message
	if r.as("u1", "message.get", map[string]string{"channel_id": "s1", "message_id": card}, &m); m.ID != card {
		t.Errorf("message.get of the ask: %+v", m)
	}

	if status, e := r.call("app1", "app1", "u2", "internal/button", map[string]string{"id": card + "#nope"}, nil); status != 422 || e.Code != "no_such_button" {
		t.Errorf("no such button: %d %+v", status, e)
	}
	if status, e := r.call("app1", "app1", "u2", "internal/button", map[string]string{"id": card + "#allow"}, nil); status != 200 {
		t.Fatalf("press: %d %+v", status, e)
	}
	if len(r.f.answers) != 1 || r.f.answers[0].Card != card || r.f.answers[0].Option != "allow" {
		t.Fatalf("answered: %+v", r.f.answers)
	}
	w.want(OpEvent, &ev)
	if ev.Type != "interaction/button" || ev.Button.ID != card+"#allow" || ev.User.ID != "u2" {
		t.Fatalf("interaction/button: %+v", ev)
	}
	w.want(OpEvent, nil)
	r.s.Notice(session.Notice{Session: "s1", Kind: session.NoticeAskDone, RequestID: "r1"})
	w.want(OpEvent, &ev)
	if ev.Type != "message-updated" || ev.Message.ID != card || strings.Contains(ev.Message.Content, "<button") || !strings.Contains(ev.Message.Content, "已结束") {
		t.Fatalf("message-updated: %+v", ev.Message)
	}

	// An input button's text begins a message: the answer, in words.
	sent := len(r.f.sent)
	var out []Message
	if status, e := r.as("u1", "message.create", map[string]string{"channel_id": "s1", "content": card + "#deny not that"}, &out); status != 200 || len(out) != 0 {
		t.Fatalf("typed: %d %+v", status, e)
	}
	if a := r.f.answers[len(r.f.answers)-1]; a.Option != "deny" || a.Message != "not that" || len(r.f.sent) != sent {
		t.Errorf("typed answer: %+v", a)
	}

	// Two questions: the presses add up till submit.
	q := api.Ask{RequestID: "r2", Kind: "question", Input: map[string]any{"questions": []any{
		map[string]any{"question": "A?", "options": []any{map[string]any{"label": "a1"}, map[string]any{"label": "a2"}}},
		map[string]any{"question": "B?", "multiSelect": true, "options": []any{map[string]any{"label": "b1"}, map[string]any{"label": "b2"}}},
	}}}
	r.f.sessions["s1"].Asks = []api.Ask{q}
	card = "ask:s1:r2"
	for _, b := range []string{"#0.1", "#1.0", "#1.1", "#1.0"} {
		if status, e := r.as("u1", "internal/button", map[string]string{"id": card + b}, nil); status != 200 {
			t.Fatalf("press %s: %d %+v", b, status, e)
		}
	}
	n := len(r.f.answers)
	r.as("u1", "internal/button", map[string]string{"id": card + "#submit"}, nil)
	if len(r.f.answers) != n+1 || strings.Join(r.f.answers[n].Choices, ",") != "0.1,1.1" {
		t.Errorf("submitted: %+v", r.f.answers[n:])
	}
}
