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
	t.Parallel()
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
	if code := p.do("POST", "/v1/internal/web/push/subscriptions", sub, nil); code != 200 {
		t.Fatalf("subscribe: %d", code)
	}
	var sent map[string]int
	if p.do("POST", "/v1/internal/web/push/test", nil, &sent); sent["sent"] != 1 || (<-got).Kind != "test" {
		t.Errorf("test push: %v", sent)
	}
	created, _ := createAt(p.t, http.DefaultClient, p.base, api.NewSession{Cwd: p.work, Text: "hi"})
	// A turn's end with the switch off: nothing (a push meanwhile would be the first got below).
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		var s api.SessionState
		if p.do("GET", "/v1/internal/sessions/"+created, nil, &s); s.Status == api.StatusIdle && s.Last != nil && s.Last.Text == "ok: hi" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no turn's end")
		}
	}
	p.do("PUT", "/v1/internal/web/push", api.PushSwitch{Enabled: true}, nil)
	sendAt(p.t, http.DefaultClient, p.base, created, api.Send{Text: "please ASK"})
	select {
	case m := <-got:
		if m.Kind != "needs_you" || m.Session != created || !strings.Contains(m.Body, "ls") {
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
	t.Parallel()
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
		{"GET", "/v1/internal/web/push", nil},
		{"PUT", "/v1/internal/web/push", api.PushSwitch{Enabled: true}},
		{"PUT", "/v1/internal/web/push/defaults", api.PushEvents{NeedsYou: true}},
		{"PUT", "/v1/internal/web/push/rules", api.PushRule{Scope: api.PushScopeWorkspace, Target: "0", Mode: api.PushModeOff}},
		{"POST", "/v1/internal/web/push/subscriptions", sub},
		{"DELETE", "/v1/internal/web/push/subscriptions", api.PushUnsubscribe{Endpoint: sub.Endpoint}},
		{"POST", "/v1/internal/web/push/test", nil},
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
	l.want(200, "web", "POST", "/v1/internal/web/push/subscriptions", mine, nil)
	if err := l.s.st.AddPushSubscription(store.PushSubscription{Terminal: bot.App.ID, Endpoint: sub.Endpoint, Keys: `{}`}); err != nil {
		t.Fatal(err)
	}
	l.want(200, "socket", "DELETE", "/v1/internal/apps/"+bot.App.ID, nil, nil)
	subs, _ := l.s.st.PushSubscriptions()
	if len(subs) != 1 || subs[0].Endpoint != mine.Endpoint {
		t.Fatalf("after the revoke: %+v", subs)
	}
}

// The queue over HTTP, and the push settings.
func TestQueueAndPushRoutes(t *testing.T) {
	t.Parallel()
	p := newPushRig(t)
	created, _ := createAt(p.t, http.DefaultClient, p.base, api.NewSession{Cwd: p.work, Text: "one WAIT"})
	id, sentInput := created, ""
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		var s api.SessionState
		p.do("GET", "/v1/internal/sessions/"+id, nil, &s)
		if s.Status == api.StatusRunning {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no turn")
		}
	}
	// A message waiting in the queue: changed (message.update), not one that is not there
	// (message.delete).
	if _, sent := sendAt(p.t, http.DefaultClient, p.base, id, api.Send{Text: "two"}); !sent.Queued {
		t.Fatalf("not queued: %+v", sent)
	} else {
		var edited string
		if code := satAt(p.t, http.DefaultClient, p.base, "message.update", map[string]any{"channel_id": id, "message_id": sent.Input, "content": "two!"}, &edited); code != 200 {
			t.Errorf("edit: %d %s", code, edited)
		}
		var s api.SessionState
		if p.do("GET", "/v1/internal/sessions/"+id, nil, &s); len(s.Queue) != 1 || s.Queue[0].Text != "two!" {
			t.Errorf("edited: %+v", s.Queue)
		}
		if code := satAt(p.t, http.DefaultClient, p.base, "message.delete", map[string]any{"channel_id": id, "message_id": "nope"}, nil); code != 422 {
			t.Errorf("delete nothing: %d", code)
		}
		sentInput = sent.Input
	}
	if code := p.do("POST", "/v1/internal/sessions/"+id+"/queue/"+sentInput+"/steer", nil, nil); code != 200 {
		t.Errorf("steer: %d", code)
	}
	if code := p.do("PUT", "/v1/internal/sessions/"+id+"/mode", api.SetMode{Mode: "nope"}, nil); code != 400 {
		t.Errorf("bad mode: %d", code)
	}
	if code := p.do("POST", "/v1/internal/sessions/"+id+"/continue", nil, nil); code != 200 {
		t.Errorf("continue: %d", code)
	}
	var rules []api.AllowRule
	if code := p.do("GET", "/v1/internal/sessions/"+id+"/rules", nil, &rules); code != 200 || rules == nil {
		t.Errorf("rules: %d %v", code, rules)
	}

	var ps api.PushState
	if p.do("GET", "/v1/internal/web/push", nil, &ps); ps.Enabled || len(ps.PublicKey) < 80 || len(ps.Rules) != 0 || !ps.Defaults.NeedsYou || !ps.Defaults.Error || !ps.Defaults.TurnEnd {
		t.Errorf("push: %+v", ps)
	}
	ps = api.PushState{}
	if code := p.do("PUT", "/v1/internal/web/push/defaults", api.PushEvents{NeedsYou: true, TurnEnd: true}, &ps); code != 200 || !ps.Defaults.TurnEnd || ps.Defaults.Error {
		t.Errorf("defaults: %d %+v", code, ps)
	}
	if code := p.do("PUT", "/v1/internal/web/push/rules", api.PushRule{Scope: api.PushScopeWorkspace, Target: "0", Mode: api.PushModeOff}, nil); code != 200 {
		t.Errorf("workspace rule: %d", code)
	}
	ps = api.PushState{}
	p.do("PUT", "/v1/internal/web/push/rules", api.PushRule{Scope: api.PushScopeSession, Target: id, Mode: api.PushModeOn, Events: &api.PushEvents{Error: true}}, &ps)
	if len(ps.Rules) != 2 {
		t.Fatalf("rules: %+v", ps.Rules)
	}
	if e, on := push.Resolve(ps, 0, p.work, id); !on || !e.Error || e.NeedsYou {
		t.Errorf("the session over its workspace: %v %+v", on, e)
	}
	if _, on := push.Resolve(ps, 0, p.work, "other"); on {
		t.Error("another session of the workspace off")
	}
	p.do("PUT", "/v1/internal/web/push/rules", api.PushRule{Scope: api.PushScopeWorkspace, Target: "0"}, nil)
	if p.do("GET", "/v1/internal/web/push", nil, &ps); len(ps.Rules) != 1 || ps.Rules[0].Scope != api.PushScopeSession {
		t.Errorf("inherit drops the rule: %+v", ps.Rules)
	}
	for _, bad := range []api.PushRule{{Scope: "node", Target: "x", Mode: api.PushModeOn}, {Scope: api.PushScopeWorkspace, Target: "-1", Mode: api.PushModeOn}, {Scope: api.PushScopeFolder, Target: "rel/dir", Mode: api.PushModeOff}, {Scope: api.PushScopeSession, Mode: api.PushModeOn}, {Scope: api.PushScopeSession, Target: id, Mode: "maybe"}} {
		if code := p.do("PUT", "/v1/internal/web/push/rules", bad, nil); code != 400 {
			t.Errorf("%+v: %d", bad, code)
		}
	}
	sub := api.PushSubscription{Endpoint: "http://insecure.example/x"}
	if code := p.do("POST", "/v1/internal/web/push/subscriptions", sub, nil); code != 400 {
		t.Errorf("an http endpoint: %d", code)
	}
}

// An ask is answered by its card (internal/button): a card of no session, not an ask's, or an ask
// no longer open is not there.
func TestAnswerCard(t *testing.T) {
	t.Parallel()
	l := start(t)
	a := l.create(200, "socket", api.NewSession{Agent: api.AgentCodex, Cwd: l.work, Text: "hi"})
	for _, card := range []string{"ask:other:1", "pick:1", "ask:" + a + ":none"} {
		if code := l.answer("socket", api.CardAnswer{Card: card, Option: "allow"}); code != 422 {
			t.Errorf("%s: %d", card, code)
		}
	}
}
