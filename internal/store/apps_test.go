package store

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/kumoSleeping/iridium/internal/gate"
)

func utc(st gate.State) string {
	for i := range st.Endpoints {
		e := &st.Endpoints[i]
		e.CreatedAt, e.LastSeenAt, e.ApprovedAt = e.CreatedAt.UTC(), e.LastSeenAt.UTC(), e.ApprovedAt.UTC()
		for j := range e.Passkeys {
			e.Passkeys[j].CreatedAt, e.Passkeys[j].LastUsedAt = e.Passkeys[j].CreatedAt.UTC(), e.Passkeys[j].LastUsedAt.UTC()
		}
	}
	for i := range st.Invites {
		st.Invites[i].CreatedAt, st.Invites[i].ExpiresAt = st.Invites[i].CreatedAt.UTC(), st.Invites[i].ExpiresAt.UTC()
	}
	for i := range st.Sessions {
		l := &st.Sessions[i]
		l.CreatedAt, l.LastSeenAt, l.ExpiresAt = l.CreatedAt.UTC(), l.LastSeenAt.UTC(), l.ExpiresAt.UTC()
	}
	b, _ := json.Marshal(st)
	return string(b)
}

// The gate's state goes into the database (apps, logins of ir_web, invites, web sessions) and
// comes back the same; an app or login deleted takes its permissions and device along.
func TestGateStore(t *testing.T) {
	s, path := open(t)
	g := s.Gate()
	if st, err := g.Load(); err != nil || len(st.Endpoints)+len(st.Invites)+len(st.Sessions) != 0 {
		t.Fatalf("empty: %+v %v", st, err)
	}
	t0 := time.Date(2026, 11, 5, 12, 0, 0, 123456789, time.UTC)
	panel := gate.Endpoint{ID: "aaaaaaaaaaaaaaaa", Name: "mac", Kind: gate.KindPanel, UserHandle: []byte{1, 2, 3},
		Passkeys: []gate.Passkey{{Credential: webauthn.Credential{ID: []byte("cred"), PublicKey: []byte("pk")}, CreatedAt: t0, LastUsedAt: t0}},
		Avatar:   []byte{0x89, 'P', 'N', 'G'}, AvatarVersion: "v1", CreatedAt: t0, LastSeenAt: t0.Add(time.Minute), ApprovedAt: t0}
	bot := gate.Endpoint{ID: "bbbbbbbbbbbbbbbb", Name: "bot", Kind: gate.KindChannel, Type: "computer", TokenHash: "abc", CreatedAt: t0.Add(time.Second)}
	other := gate.Endpoint{ID: "cccccccccccccccc", Name: "mac", Kind: gate.KindChannel, CreatedAt: t0.Add(2 * time.Second)}
	setup := gate.Invite{ID: "setup", Hash: "h0", Setup: true, CreatedAt: t0}
	inv := gate.Invite{ID: "invite01", Hash: "h1", Name: "pad", Perms: []byte(`{"claudeOwner":{"by":"local","at":1}}`), CreatedAt: t0, ExpiresAt: t0.Add(10 * time.Minute)}
	login := gate.Session{Hash: "s1", EndpointID: panel.ID, CreatedAt: t0, LastSeenAt: t0, ExpiresAt: t0.Add(7 * 24 * time.Hour)}
	must(t, g.Apply(gate.Change{Endpoints: []gate.Endpoint{panel, bot, other}, Invites: []gate.Invite{setup, inv}, Sessions: []gate.Session{login}}))
	// Ordered by creation, then ID.
	want := gate.State{Endpoints: []gate.Endpoint{panel, bot, other}, Invites: []gate.Invite{inv, setup}, Sessions: []gate.Session{login}}
	got, err := g.Load()
	if err != nil || utc(got) != utc(want) {
		t.Fatalf("round trip:\n got %s\nwant %s\n%v", utc(got), utc(want), err)
	}
	if !got.Invites[1].ExpiresAt.IsZero() || !got.Endpoints[1].LastSeenAt.IsZero() {
		t.Errorf("zero times: %+v %+v", got.Invites[1], got.Endpoints[1])
	}
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM logins WHERE app = 'ir_web' AND id = ? AND approved_at IS NOT NULL`, panel.ID).Scan(&n)
	if n != 1 {
		t.Error("the browser is no approved login of ir_web")
	}

	// Devices: an app's and a login's in their rows, the host's on its own; none for no one.
	must(t, s.SetDevice(bot.ID, "phone"))
	must(t, s.SetDevice(panel.ID, "laptop"))
	must(t, s.SetDevice(gate.HostID, "none"))
	if err := s.SetDevice("nobody", "phone"); err != ErrNoOne {
		t.Errorf("a device for no one: %v", err)
	}
	if d, _ := s.Devices(); len(d) != 3 || d[bot.ID] != "phone" || d[panel.ID] != "laptop" || d[gate.HostID] != "none" {
		t.Fatalf("devices %v", d)
	}
	// A gate update keeps the device.
	panel.Name = "Mac"
	must(t, g.Apply(gate.Change{Endpoints: []gate.Endpoint{panel}}))
	if d, _ := s.Device(panel.ID); d != "laptop" {
		t.Errorf("device after a rename: %q", d)
	}

	// An update, a deletion with its rules, all in one change; then from a new connection.
	must(t, s.SetTerminalRules(bot.ID, []byte(`{"agents":["pi"]}`)))
	bot.Name, bot.TokenHash = "bot2", "def"
	must(t, g.Apply(gate.Change{DeleteEndpoints: []string{bot.ID}, DeleteInvites: []string{inv.ID}, DeleteSessions: []string{"s1"}, Endpoints: []gate.Endpoint{other}}))
	if _, ok, _ := s.TerminalRules(bot.ID); ok {
		t.Error("rules outlived their app")
	}
	if d, _ := s.Device(bot.ID); d != "" {
		t.Errorf("the device outlived its app: %q", d)
	}
	// Two apps without a token are fine; two with the same are not.
	other2 := gate.Endpoint{ID: "dddddddddddddddd", Name: "o2", Kind: gate.KindChannel, CreatedAt: t0}
	must(t, g.Apply(gate.Change{Endpoints: []gate.Endpoint{other2}}))
	a, b := other, other2
	a.TokenHash, b.TokenHash = "same", "same"
	if err := g.Apply(gate.Change{Endpoints: []gate.Endpoint{a, b}}); err == nil {
		t.Error("two apps with one token")
	}
	s.Close()
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	got, _ = s2.Gate().Load()
	if len(got.Endpoints) != 3 || len(got.Invites) != 1 || len(got.Sessions) != 0 || got.Endpoints[2].TokenHash != "" {
		t.Fatalf("after: %s", utc(got))
	}
}

// Migration 26 on a 0.1 database: the panels are ir_web's logins and still log in, the channels
// are apps and their TOKENs still work, usage, permissions, devices and invites carry over, and
// push subscriptions are left only to the host and ir_web's logins.
func TestMigrateToApps(t *testing.T) {
	f := makeFixture(t)
	s, err := Open(f.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := os.Stat(f.Path + ".v25.bak"); err != nil {
		t.Errorf("no copy of the 0.1 database: %v", err)
	}
	st, err := s.Gate().Load()
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]gate.Endpoint{}
	for _, e := range st.Endpoints {
		byID[e.ID] = e
	}
	if len(byID) != 4 {
		t.Fatalf("endpoints: %s", utc(st))
	}
	mac, qq, box := byID[f.Mac], byID[f.QQ], byID[f.Box]
	if mac.Kind != gate.KindPanel || mac.Name != "kumo mac" || len(mac.Passkeys) != 1 || mac.AvatarVersion != "av1" ||
		!mac.ApprovedAt.Equal(f.T0) || !mac.LastSeenAt.Equal(f.T0.Add(time.Hour)) || string(mac.UserHandle) != "\x01\x02" {
		t.Errorf("mac: %+v", mac)
	}
	if byID[f.Pad].Kind != gate.KindPanel || byID[f.Pad].ApprovedAt.IsZero() {
		t.Errorf("pad: %+v", byID[f.Pad])
	}
	if qq.Kind != gate.KindChannel || qq.Type != "" || qq.TokenHash != sha(f.QQToken) {
		t.Errorf("qq: %+v", qq)
	}
	if box.Kind != gate.KindChannel || box.Type != "computer" || box.AvatarVersion != "av2" {
		t.Errorf("box: %+v", box)
	}
	// Invites: the --owner one carries Claude as its perms; the setup link is still one.
	inv := map[string]gate.Invite{}
	for _, i := range st.Invites {
		inv[i.ID] = i
	}
	var perms struct {
		ClaudeOwner struct {
			By string `json:"by"`
			At int64  `json:"at"`
		} `json:"claudeOwner"`
	}
	if err := json.Unmarshal(inv[f.Owner].Perms, &perms); err != nil || perms.ClaudeOwner.By != "local" || perms.ClaudeOwner.At != f.T0.UnixMilli() {
		t.Errorf("owner invite's perms %s: %v", inv[f.Owner].Perms, err)
	}
	if inv[f.Plain].Perms != nil || !inv["setup"].Setup || inv[f.Owner].Name != "phone" {
		t.Errorf("invites: %+v", inv)
	}
	if len(st.Sessions) != 1 || st.Sessions[0].EndpointID != f.Mac {
		t.Errorf("web sessions: %+v", st.Sessions)
	}
	// Permissions, usage, openers, devices by the same IDs.
	if r, ok := rulesOf(t, s, f.Mac); !ok || r.ClaudeOwner == nil || r.ClaudeOwner.By != "first" {
		t.Errorf("mac's rules: %+v", r)
	}
	if r, ok := rulesOf(t, s, f.QQ); !ok || r.ClaudeOwner == nil {
		t.Errorf("qq's rules: %+v", r)
	}
	if n, _ := s.TokensSince(f.Mac, 0); n != 100 {
		t.Errorf("mac's usage: %d", n)
	}
	if n, _ := s.TokensSince(f.QQ, 0); n != 10 {
		t.Errorf("qq's usage: %d", n)
	}
	if s.Opener("s-qq") != f.QQ || s.Opener("s-mac") != f.Mac {
		t.Error("openers")
	}
	if d, _ := s.Devices(); len(d) != 3 || d[f.Mac] != "phone" || d[f.Box] != "laptop" || d[gate.HostID] != "none" {
		t.Errorf("devices: %v", d)
	}
	// Push: the host's and mac's stay; the app's and the gone one's go.
	subs, _ := s.PushSubscriptions()
	var who []string
	for _, p := range subs {
		who = append(who, p.Terminal)
	}
	if len(who) != 2 || who[0] != gate.HostID || who[1] != f.Mac {
		t.Errorf("push subscriptions: %v", who)
	}
	var n int
	if s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name IN ('terminals', 'devices_old')`).Scan(&n); n != 0 {
		t.Error("terminals table left")
	}
	// The chat layer's tables are gone (28); the outbox stays, emptied.
	if s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name IN ('chat_pointers', 'chat_seen', 'bot_follows')`).Scan(&n); n != 0 {
		t.Error("chat tables left")
	}
	if s.db.QueryRow(`SELECT COUNT(*) FROM outbox`).Scan(&n); n != 0 {
		t.Errorf("outbox: %d rows", n)
	}

	// Through the gate: the TOKENs and the browser's cookie are let in as before.
	const origin = "https://ir.example.com"
	g, err := gate.New(gate.Config{PublicOrigin: origin, Store: s.Gate(), Now: func() time.Time { return f.T0.Add(2 * time.Hour) }})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	var seen gate.Identity
	h := g.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { seen, _ = gate.FromContext(r.Context()) }))
	do := func(set func(*http.Request)) (gate.Identity, int) {
		seen = gate.Identity{}
		r := httptest.NewRequest("GET", origin+"/api/me", nil)
		set(r)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return seen, w.Code
	}
	if id, code := do(func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+f.QQToken) }); code != 200 || id.App != f.QQ || id.Login != "" || id.Name != "QQ" {
		t.Errorf("QQ's TOKEN: %d %+v", code, id)
	}
	if id, code := do(func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+f.BoxTok) }); code != 200 || id.App != f.Box {
		t.Errorf("box's TOKEN: %d %+v", code, id)
	}
	if id, code := do(func(r *http.Request) { r.AddCookie(&http.Cookie{Name: gate.CookieName, Value: f.Cookie}) }); code != 200 || !id.Web() || id.Login != f.Mac || id.Name != "kumo mac" {
		t.Errorf("mac's browser: %d %+v", code, id)
	}
	if _, code := do(func(r *http.Request) { r.Header.Set("Authorization", "Bearer ir_"+token("x")) }); code != http.StatusUnauthorized {
		t.Errorf("a TOKEN never given: %d", code)
	}
}
