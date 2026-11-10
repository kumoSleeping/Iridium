package server

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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

// With the switch on, an ask pushes "needs you" to the subscription; a turn's end does not, by
// default.
func TestPushOnAsk(t *testing.T) {
	p := newPluginRig(t)
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
	if code := p.do("", "POST", "/api/push/subscriptions", sub, nil); code != 200 {
		t.Fatalf("subscribe: %d", code)
	}
	var sent map[string]int
	if p.do("", "POST", "/api/push/test", nil, &sent); sent["sent"] != 1 || (<-got).Kind != "test" {
		t.Errorf("test push: %v", sent)
	}
	var created api.NewSessionReply
	p.do("", "POST", "/api/sessions", api.NewSession{Cwd: p.work, Text: "hi"}, &created)
	// A turn's end with the switch off: nothing (a push meanwhile would be the first got below).
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		var s api.SessionState
		if p.do("", "GET", "/api/sessions/"+created.ID, nil, &s); s.Status == api.StatusIdle && s.Last != nil && s.Last.Text == "ok: hi" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no turn's end")
		}
	}
	p.do("", "PUT", "/api/push", api.PushSwitch{Enabled: true}, nil)
	p.do("", "POST", "/api/sessions/"+created.ID+"/send", api.Send{Text: "please ASK"}, nil)
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
		{"GET", "/api/push", nil},
		{"PUT", "/api/push", api.PushSwitch{Enabled: true}},
		{"PUT", "/api/push/defaults", api.PushEvents{NeedsYou: true}},
		{"PUT", "/api/push/rules", api.PushRule{Scope: api.PushScopeWorkspace, Target: "0", Mode: api.PushModeOff}},
		{"POST", "/api/push/subscriptions", sub},
		{"DELETE", "/api/push/subscriptions", api.PushUnsubscribe{Endpoint: sub.Endpoint}},
		{"POST", "/api/push/test", nil},
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
	l.want(200, "web", "POST", "/api/push/subscriptions", mine, nil)
	if err := l.s.st.AddPushSubscription(store.PushSubscription{Terminal: bot.App.ID, Endpoint: sub.Endpoint, Keys: `{}`}); err != nil {
		t.Fatal(err)
	}
	l.want(200, "socket", "DELETE", "/api/apps/"+bot.App.ID, nil, nil)
	subs, _ := l.s.st.PushSubscriptions()
	if len(subs) != 1 || subs[0].Endpoint != mine.Endpoint {
		t.Fatalf("after the revoke: %+v", subs)
	}
}
