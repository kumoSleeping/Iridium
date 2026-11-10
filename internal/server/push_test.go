package server

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/config"
	"github.com/kumoSleeping/iridium/internal/push"
	"github.com/kumoSleeping/iridium/internal/store"
)

// allowHTTP lets the tests' push service be plain http.
var allowHTTP = true

// pushRig is a server with Web Push on the TCP address, no public origin: every request is the
// host's.
type pushRig struct {
	t    *testing.T
	base string
	work string
}

func newPushRig(t *testing.T) *pushRig {
	s, cfg, work := setup(t)
	st, err := store.Open(filepath.Join(cfg.StateDir, "iridium.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	s.Push, err = push.Open(push.Options{StateDir: cfg.StateDir, Store: st, Manager: s.m, Logf: t.Logf, AllowHTTP: allowHTTP})
	if err != nil {
		t.Fatal(err)
	}
	tcp, unix, err := s.Listen()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Serve(ctx, tcp, unix); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return &pushRig{t: t, base: "http://" + tcp.Addr().String(), work: work}
}

func (p *pushRig) do(method, path string, body, out any) int {
	p.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = strings.NewReader(string(b))
	}
	req, _ := http.NewRequest(method, p.base+path, r)
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

// With the switch on, an ask pushes "needs you" to the subscription; a turn's end does not, by
// default.
func TestPushOnAsk(t *testing.T) {
	p := newPushRig(t)
	ua, _ := ecdh.P256().GenerateKey(rand.Reader)
	auth := make([]byte, 16)
	rand.Read(auth)
	got := make(chan api.PushMessage, 10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		plain, err := push.Decrypt(ua, auth, body)
		if err != nil {
			t.Error(err)
		}
		var m api.PushMessage
		json.Unmarshal(plain, &m)
		got <- m
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	sub := api.PushSubscription{Endpoint: srv.URL}
	sub.Keys.P256dh = base64.RawURLEncoding.EncodeToString(ua.PublicKey().Bytes())
	sub.Keys.Auth = base64.RawURLEncoding.EncodeToString(auth)
	if code := p.do("POST", "/api/web/push/subscriptions", sub, nil); code != 200 {
		t.Fatalf("subscribe: %d", code)
	}
	var sent map[string]int
	if p.do("POST", "/api/web/push/test", nil, &sent); sent["sent"] != 1 || (<-got).Kind != "test" {
		t.Errorf("test push: %v", sent)
	}
	var created api.NewSessionReply
	p.do("POST", "/api/sessions", api.NewSession{Cwd: p.work, Text: "hi"}, &created)
	// A turn's end with the switch off: nothing (a push meanwhile would be the first got below).
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		var s api.SessionState
		if p.do("GET", "/api/sessions/"+created.ID, nil, &s); s.Status == api.StatusIdle && s.Last != nil && s.Last.Text == "ok: hi" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no turn's end")
		}
	}
	p.do("PUT", "/api/web/push", api.PushSwitch{Enabled: true}, nil)
	p.do("POST", "/api/sessions/"+created.ID+"/send", api.Send{Text: "please ASK"}, nil)
	select {
	case m := <-got:
		if m.Kind != "needs_you" || m.Session != created.ID || !strings.Contains(m.Body, "ls") {
			t.Errorf("push: %+v", m)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no push")
	}
	select {
	case m := <-got:
		t.Errorf("another push: %+v", m)
	case <-time.After(300 * time.Millisecond):
	}
}

// Push is the panels' and 本机's: a TOKEN and an agent's ir get 403 on every route, so neither
// switches it nor subscribes an address of its own. A revoked terminal's subscriptions go with it.
func TestPushPanelsOnly(t *testing.T) {
	l := startWith(t, func(s *Server, cfg config.Config, work string) {
		var err error
		s.Push, err = push.Open(push.Options{StateDir: cfg.StateDir, Store: s.st, Manager: s.m, Logf: t.Logf, AllowHTTP: allowHTTP})
		if err != nil {
			t.Fatal(err)
		}
	})
	var bot api.Token
	bot = l.newApp("bot", false)
	ua, _ := ecdh.P256().GenerateKey(rand.Reader)
	auth := make([]byte, 16)
	rand.Read(auth)
	keyed := func(endpoint string) api.PushSubscription {
		sub := api.PushSubscription{Endpoint: endpoint}
		sub.Keys.P256dh = base64.RawURLEncoding.EncodeToString(ua.PublicKey().Bytes())
		sub.Keys.Auth = base64.RawURLEncoding.EncodeToString(auth)
		return sub
	}
	// Never pushed to: the test push below runs with the subscription dropped again.
	sub := keyed("https://push.example.com/bot")
	routes := []struct {
		method, path string
		body         any
	}{
		{"GET", "/api/web/push", nil},
		{"PUT", "/api/web/push", api.PushSwitch{Enabled: true}},
		{"PUT", "/api/web/push/defaults", api.PushEvents{NeedsYou: true}},
		{"PUT", "/api/web/push/rules", api.PushRule{Scope: api.PushScopeWorkspace, Target: "0", Mode: api.PushModeOff}},
		{"POST", "/api/web/push/subscriptions", sub},
		{"DELETE", "/api/web/push/subscriptions", api.PushUnsubscribe{Endpoint: sub.Endpoint}},
		{"POST", "/api/web/push/test", nil},
	}
	for _, as := range []string{bot.Token, "agent"} {
		for _, r := range routes {
			l.want(403, as, r.method, r.path, r.body, nil)
		}
	}
	if l.s.Push.Enabled() {
		t.Fatal("switched on by a TOKEN or an agent")
	}
	if subs, _ := l.s.st.PushSubscriptions(); len(subs) != 0 {
		t.Fatalf("subscribed: %+v", subs)
	}
	for _, as := range []string{"web", "socket"} {
		for _, r := range routes {
			l.want(200, as, r.method, r.path, r.body, nil)
		}
	}

	// Revoked: the terminal's subscriptions dropped, the others' kept.
	mine := keyed("https://push.example.com/mine")
	l.want(200, "web", "POST", "/api/web/push/subscriptions", mine, nil)
	if err := l.s.st.AddPushSubscription(store.PushSubscription{Terminal: bot.App.ID, Endpoint: sub.Endpoint, Keys: `{}`}); err != nil {
		t.Fatal(err)
	}
	l.want(200, "socket", "DELETE", "/api/apps/"+bot.App.ID, nil, nil)
	subs, _ := l.s.st.PushSubscriptions()
	if len(subs) != 1 || subs[0].Endpoint != mine.Endpoint {
		t.Fatalf("after the revoke: %+v", subs)
	}
}

// The queue over HTTP, and the push settings.
func TestQueueAndPushRoutes(t *testing.T) {
	p := newPushRig(t)
	var created api.NewSessionReply
	p.do("POST", "/api/sessions", api.NewSession{Cwd: p.work, Text: "one WAIT"}, &created)
	id := created.ID
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		var s api.SessionState
		p.do("GET", "/api/sessions/"+id, nil, &s)
		if s.Status == api.StatusRunning {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no turn")
		}
	}
	var sent api.SendReply
	if p.do("POST", "/api/sessions/"+id+"/send", api.Send{Text: "two"}, &sent); !sent.Queued {
		t.Fatalf("not queued: %+v", sent)
	}
	if code := p.do("PUT", "/api/sessions/"+id+"/queue/"+sent.Input, api.EditQueued{Text: "two!"}, nil); code != 200 {
		t.Errorf("edit: %d", code)
	}
	if code := p.do("DELETE", "/api/sessions/"+id+"/queue/nope", nil, nil); code != 422 {
		t.Errorf("delete nothing: %d", code)
	}
	if code := p.do("POST", "/api/sessions/"+id+"/queue/"+sent.Input+"/steer", nil, nil); code != 200 {
		t.Errorf("steer: %d", code)
	}
	if code := p.do("PUT", "/api/sessions/"+id+"/mode", api.SetMode{Mode: "nope"}, nil); code != 400 {
		t.Errorf("bad mode: %d", code)
	}
	if code := p.do("POST", "/api/sessions/"+id+"/continue", nil, nil); code != 200 {
		t.Errorf("continue: %d", code)
	}
	var rules []api.AllowRule
	if code := p.do("GET", "/api/sessions/"+id+"/rules", nil, &rules); code != 200 || rules == nil {
		t.Errorf("rules: %d %v", code, rules)
	}

	var ps api.PushState
	if p.do("GET", "/api/web/push", nil, &ps); ps.Enabled || len(ps.PublicKey) < 80 || len(ps.Rules) != 0 || !ps.Defaults.NeedsYou || !ps.Defaults.Error || !ps.Defaults.TurnEnd {
		t.Errorf("push: %+v", ps)
	}
	ps = api.PushState{}
	if code := p.do("PUT", "/api/web/push/defaults", api.PushEvents{NeedsYou: true, TurnEnd: true}, &ps); code != 200 || !ps.Defaults.TurnEnd || ps.Defaults.Error {
		t.Errorf("defaults: %d %+v", code, ps)
	}
	if code := p.do("PUT", "/api/web/push/rules", api.PushRule{Scope: api.PushScopeWorkspace, Target: "0", Mode: api.PushModeOff}, nil); code != 200 {
		t.Errorf("workspace rule: %d", code)
	}
	ps = api.PushState{}
	p.do("PUT", "/api/web/push/rules", api.PushRule{Scope: api.PushScopeSession, Target: id, Mode: api.PushModeOn, Events: &api.PushEvents{Error: true}}, &ps)
	if len(ps.Rules) != 2 {
		t.Fatalf("rules: %+v", ps.Rules)
	}
	if e, on := push.Resolve(ps, 0, p.work, id); !on || !e.Error || e.NeedsYou {
		t.Errorf("the session over its workspace: %v %+v", on, e)
	}
	if _, on := push.Resolve(ps, 0, p.work, "other"); on {
		t.Error("another session of the workspace off")
	}
	p.do("PUT", "/api/web/push/rules", api.PushRule{Scope: api.PushScopeWorkspace, Target: "0"}, nil)
	if p.do("GET", "/api/web/push", nil, &ps); len(ps.Rules) != 1 || ps.Rules[0].Scope != api.PushScopeSession {
		t.Errorf("inherit drops the rule: %+v", ps.Rules)
	}
	for _, bad := range []api.PushRule{{Scope: "node", Target: "x", Mode: api.PushModeOn}, {Scope: api.PushScopeWorkspace, Target: "-1", Mode: api.PushModeOn}, {Scope: api.PushScopeFolder, Target: "rel/dir", Mode: api.PushModeOff}, {Scope: api.PushScopeSession, Mode: api.PushModeOn}, {Scope: api.PushScopeSession, Target: id, Mode: "maybe"}} {
		if code := p.do("PUT", "/api/web/push/rules", bad, nil); code != 400 {
			t.Errorf("%+v: %d", bad, code)
		}
	}
	sub := api.PushSubscription{Endpoint: "http://insecure.example/x"}
	if code := p.do("POST", "/api/web/push/subscriptions", sub, nil); code != 400 {
		t.Errorf("an http endpoint: %d", code)
	}
}

// ir send 应用/登录: a login of ir_web gets the notice as a Web Push from the session; another
// app's login, a login there is not, the switch off, no browser: 422 with the reason. An app's
// bare TOKEN may not send one.
func TestNotice(t *testing.T) {
	l := startWith(t, func(s *Server, cfg config.Config, work string) {
		var err error
		s.Push, err = push.Open(push.Options{StateDir: cfg.StateDir, Store: s.st, Manager: s.m, Logf: t.Logf, AllowHTTP: allowHTTP})
		if err != nil {
			t.Fatal(err)
		}
	})
	now := time.Now()
	padID := l.addPanel("pad")
	bot := l.newApp("bot", false)
	if ok, err := l.s.st.AddLogin(bot.App.ID, "u1", "张三", 20, now); !ok || err != nil {
		t.Fatal(ok, err)
	}
	var e api.Error
	code := func(as, to string, body api.Notice) string {
		t.Helper()
		e = api.Error{}
		app, login, _ := strings.Cut(to, "/")
		l.do(as, "POST", "/api/apps/"+app+"/logins/"+login+"/notice", body, &e)
		return e.Code
	}
	hi := api.Notice{Text: "跑完了"}
	if c := code("socket", "ir_web/pad", hi); c != "not_deliverable" || !strings.Contains(e.Message, "Web Push is off") {
		t.Errorf("push off: %+v", e)
	}
	l.want(200, "socket", "PUT", "/api/web/push", api.PushSwitch{Enabled: true}, nil)
	if c := code("socket", "ir_web/pad", hi); c != "not_deliverable" || !strings.Contains(e.Message, "no browser") {
		t.Errorf("no browser: %+v", e)
	}
	for to, want := range map[string]string{"bot/张三": "not_deliverable", "bot/u1": "not_deliverable", "bot/nobody": "no_such_login", "ir_web/nobody": "no_such_login", "nope/x": "no_such_app"} {
		if c := code("socket", to, hi); c != want {
			t.Errorf("%s: %+v, want %s", to, e, want)
		}
	}
	if c := code(bot.Token, "ir_web/pad", hi); c != "no_permission" {
		t.Errorf("an app's TOKEN: %+v", e)
	}
	l.want(400, "socket", "POST", "/api/apps/ir_web/logins/pad/notice", api.Notice{Text: "  "}, nil)

	// A browser of pad's: the notice comes from the session ir runs in.
	ua, _ := ecdh.P256().GenerateKey(rand.Reader)
	auth := make([]byte, 16)
	rand.Read(auth)
	got := make(chan api.PushMessage, 10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		plain, err := push.Decrypt(ua, auth, body)
		if err != nil {
			t.Error(err)
		}
		var m api.PushMessage
		json.Unmarshal(plain, &m)
		got <- m
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	keys, _ := json.Marshal(map[string]string{"p256dh": base64.RawURLEncoding.EncodeToString(ua.PublicKey().Bytes()), "auth": base64.RawURLEncoding.EncodeToString(auth)})
	if err := l.s.st.AddPushSubscription(store.PushSubscription{Terminal: padID, Endpoint: srv.URL, Keys: string(keys)}); err != nil {
		t.Fatal(err)
	}
	var mine api.NewSessionReply
	l.want(200, "socket", "POST", "/api/sessions", api.NewSession{Agent: api.AgentCodex, Cwd: l.work, Text: "hi"}, &mine)
	var r api.NoticeReply
	l.want(200, "agent-in:"+mine.ID, "POST", "/api/apps/ir_web/logins/pad/notice", hi, &r)
	if r.Sent != 1 || r.Login != padID || r.Name != "pad" {
		t.Errorf("reply: %+v", r)
	}
	select {
	case m := <-got:
		if m.Kind != "notice" || m.Body != "跑完了" || m.Session != mine.ID {
			t.Errorf("push: %+v", m)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no push")
	}
}

// An ask is answered at its session's own path: a card of another session is a bad request.
func TestAnswerAtSession(t *testing.T) {
	l := start(t)
	var a api.NewSessionReply
	l.want(200, "socket", "POST", "/api/sessions", api.NewSession{Agent: api.AgentCodex, Cwd: l.work, Text: "hi"}, &a)
	l.want(400, "socket", "POST", "/api/sessions/"+a.ID+"/answer", api.CardAnswer{Card: "ask:other:1", Option: "allow"}, nil)
	l.want(400, "socket", "POST", "/api/sessions/"+a.ID+"/answer", api.CardAnswer{Card: "pick:1", Option: "x"}, nil)
	l.want(410, "socket", "POST", "/api/sessions/"+a.ID+"/answer", api.CardAnswer{Card: "ask:" + a.ID + ":none", Option: "allow"}, nil)
}
