package push

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/store"
)

func TestEncryptRoundTrip(t *testing.T) {
	ua, _ := ecdh.P256().GenerateKey(rand.Reader)
	auth := make([]byte, 16)
	rand.Read(auth)
	body, err := Encrypt(ua.PublicKey().Bytes(), auth, []byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := Decrypt(ua, auth, body)
	if err != nil || string(plain) != "hello" {
		t.Fatalf("%q %v", plain, err)
	}
	auth[0] ^= 1
	if _, err := Decrypt(ua, auth, body); err == nil {
		t.Error("decrypted with the wrong auth")
	}
}

// A push service that decrypts what it is sent, as the browser would: the keys stay the same
// across opens, the switch is off by default, a gone subscription is dropped.
func TestSend(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "iridium.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	saved := false
	s, err := Open(Options{StateDir: dir, Store: st, AllowHTTP: true, Save: func(on bool) error { saved = on; return nil }, Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(filepath.Join(dir, FileKeys)); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("keys: %v %v", fi, err)
	}
	again, _ := Open(Options{StateDir: dir, Store: st})
	if again.PublicKey() != s.PublicKey() {
		t.Error("new keys on the second open")
	}
	if s.Enabled() {
		t.Error("on by default")
	}
	if err := s.SetEnabled(true); err != nil || !saved || !s.Enabled() {
		t.Error("switch")
	}

	ua, _ := ecdh.P256().GenerateKey(rand.Reader)
	auth := make([]byte, 16)
	rand.Read(auth)
	var mu sync.Mutex
	var got []api.PushMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/gone" {
			w.WriteHeader(http.StatusGone)
			return
		}
		if err := VerifyVAPID(r.Header.Get("Authorization"), s.pub); err != nil || r.Header.Get("Content-Encoding") != "aes128gcm" {
			t.Errorf("headers: %v %v", r.Header, err)
		}
		body, _ := io.ReadAll(r.Body)
		plain, err := Decrypt(ua, auth, body)
		if err != nil {
			t.Error(err)
		}
		var m api.PushMessage
		json.Unmarshal(plain, &m)
		mu.Lock()
		got = append(got, m)
		mu.Unlock()
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	sub := api.PushSubscription{Endpoint: srv.URL + "/ok"}
	sub.Keys.P256dh = b64.EncodeToString(ua.PublicKey().Bytes())
	sub.Keys.Auth = b64.EncodeToString(auth)
	if err := s.Subscribe("panel-1", sub); err != nil {
		t.Fatal(err)
	}
	sub.Endpoint = srv.URL + "/gone"
	s.Subscribe("panel-1", sub)
	bad := sub
	bad.Endpoint = "ftp://x"
	if err := s.Subscribe("panel-1", bad); err == nil {
		t.Error("took a bad endpoint")
	}
	if n := s.Send(context.Background(), "", api.PushMessage{Kind: "test", Title: "t", Body: "b"}); n != 1 {
		t.Errorf("sent to %d", n)
	}
	if len(got) != 1 || got[0].Body != "b" {
		t.Errorf("got %+v", got)
	}
	if subs, _ := st.PushSubscriptions(); len(subs) != 1 {
		t.Errorf("the gone subscription stayed: %+v", subs)
	}
	ps, _ := s.State()
	if len(ps.Rules) != 0 || ps.Defaults != (api.PushEvents{NeedsYou: true, Error: true}) {
		t.Errorf("defaults: %+v", ps)
	}
}

// The nearest rule of the session, its folder and its workspace says on or off; the events are
// the nearest rule's on with its own, else the defaults.
func TestResolve(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "iridium.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s, err := Open(Options{StateDir: t.TempDir(), Store: st, Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	turn := &api.PushEvents{TurnEnd: true}
	for _, r := range []api.PushRule{
		{Scope: api.PushScopeWorkspace, Target: "1", Mode: api.PushModeOff},
		{Scope: api.PushScopeFolder, Target: "/w1/loud", Mode: api.PushModeOn},
		{Scope: api.PushScopeSession, Target: "quiet", Mode: api.PushModeOff},
		{Scope: api.PushScopeWorkspace, Target: "2", Mode: api.PushModeOn, Events: turn},
		{Scope: api.PushScopeFolder, Target: "/w2/hush", Mode: api.PushModeOff},
		{Scope: api.PushScopeSession, Target: "back", Mode: api.PushModeOn},
		{Scope: api.PushScopeSession, Target: "own", Mode: api.PushModeOn, Events: &api.PushEvents{Compact: true}},
	} {
		if err := s.SetRule(r); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetDefaults(api.PushEvents{NeedsYou: true}); err != nil {
		t.Fatal(err)
	}
	ps, err := s.State()
	if err != nil {
		t.Fatal(err)
	}
	needs := api.PushEvents{NeedsYou: true}
	for _, c := range []struct {
		ws           int64
		cwd, session string
		on           bool
		want         api.PushEvents
	}{
		{0, "/elsewhere", "a", true, needs},               // no rule: the defaults
		{1, "/w1", "a", false, api.PushEvents{}},          // its workspace off
		{1, "/w1/loud", "a", true, needs},                 // its folder on, in a workspace off
		{1, "/w1/loud", "quiet", false, api.PushEvents{}}, // the session off, in a folder on
		{2, "/w2", "a", true, *turn},                      // the workspace's own events
		{2, "/w2/hush", "a", false, api.PushEvents{}},
		{2, "/w2/hush", "back", true, *turn}, // on again: the events from above
		{2, "/w2/hush", "own", true, api.PushEvents{Compact: true}},
		{2, "/w2/hush/deeper", "a", true, *turn}, // a folder is its own path only
	} {
		got, on := Resolve(ps, c.ws, c.cwd, c.session)
		if on != c.on || got != c.want {
			t.Errorf("%d %s %s: %v %+v, want %v %+v", c.ws, c.cwd, c.session, on, got, c.on, c.want)
		}
	}
	// Inherit drops the rule; off keeps no events.
	s.SetRule(api.PushRule{Scope: api.PushScopeWorkspace, Target: "1"})
	s.SetRule(api.PushRule{Scope: api.PushScopeWorkspace, Target: "2", Mode: api.PushModeOff, Events: turn})
	ps, _ = s.State()
	if _, on := Resolve(ps, 1, "/w1", "a"); !on {
		t.Error("workspace 1 still off")
	}
	for _, r := range ps.Rules {
		if r.Target == "2" && (r.Mode != api.PushModeOff || r.Events != nil) {
			t.Errorf("off: %+v", r)
		}
	}
}
