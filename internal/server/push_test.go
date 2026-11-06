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
	"github.com/kumoSleeping/iridium/internal/push"
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
