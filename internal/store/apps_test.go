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
	"github.com/kumoSleeping/iridium/internal/webapp"
)

func utc(st gate.State) string {
	for i := range st.Endpoints {
		e := &st.Endpoints[i]
		e.CreatedAt, e.LastSeenAt, e.ApprovedAt = e.CreatedAt.UTC(), e.LastSeenAt.UTC(), e.ApprovedAt.UTC()
	}
	b, _ := json.Marshal(st)
	return string(b)
}

func webUTC(st webapp.State) string {
	for i := range st.Creds {
		c := &st.Creds[i]
		for j := range c.Passkeys {
			c.Passkeys[j].CreatedAt, c.Passkeys[j].LastUsedAt = c.Passkeys[j].CreatedAt.UTC(), c.Passkeys[j].LastUsedAt.UTC()
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

// The core's state (apps, logins of ir_web) and ir_web's (their passkeys, invites, web sessions)
// go into the database and come back the same; an app or login deleted takes its permissions,
// device and passkeys along.
func TestGateStore(t *testing.T) {
	s, path := open(t)
	g, web := s.Gate(), s.Web()
	if st, err := g.Load(); err != nil || len(st.Endpoints) != 0 {
		t.Fatalf("empty: %+v %v", st, err)
	}
	if st, err := web.Load(); err != nil || len(st.Creds)+len(st.Invites)+len(st.Sessions) != 0 {
		t.Fatalf("empty: %+v %v", st, err)
	}
	t0 := time.Date(2026, 11, 5, 12, 0, 0, 123456789, time.UTC)
	panel := gate.Endpoint{ID: "aaaaaaaaaaaaaaaa", Name: "mac", Kind: gate.KindPanel,
		Avatar: []byte{0x89, 'P', 'N', 'G'}, AvatarVersion: "v1", CreatedAt: t0, LastSeenAt: t0.Add(time.Minute), ApprovedAt: t0}
	cred := webapp.Cred{Login: panel.ID, UserHandle: []byte{1, 2, 3},
		Passkeys: []webapp.Passkey{{Credential: webauthn.Credential{ID: []byte("cred"), PublicKey: []byte("pk")}, CreatedAt: t0, LastUsedAt: t0}}}
	bot := gate.Endpoint{ID: "bbbbbbbbbbbbbbbb", Name: "bot", Kind: gate.KindChannel, Type: "computer", TokenHash: "abc", CreatedAt: t0.Add(time.Second)}
	other := gate.Endpoint{ID: "cccccccccccccccc", Name: "mac", Kind: gate.KindChannel, CreatedAt: t0.Add(2 * time.Second)}
	setup := webapp.Invite{ID: "setup", Hash: "h0", Setup: true, CreatedAt: t0}
	inv := webapp.Invite{ID: "invite01", Hash: "h1", Name: "pad", Perms: []byte(`{"claudeOwner":{"by":"local","at":1}}`), CreatedAt: t0, ExpiresAt: t0.Add(10 * time.Minute)}
	login := webapp.Session{Hash: "s1", Login: panel.ID, CreatedAt: t0, LastSeenAt: t0, ExpiresAt: t0.Add(7 * 24 * time.Hour)}
	if err := web.Apply(webapp.Change{Creds: []webapp.Cred{cred}}); err == nil {
		t.Error("passkeys for a login not there")
	}
	must(t, g.Apply(gate.Change{Endpoints: []gate.Endpoint{panel, bot, other}}))
	must(t, web.Apply(webapp.Change{Creds: []webapp.Cred{cred}, Invites: []webapp.Invite{setup, inv}, Sessions: []webapp.Session{login}}))
	// Ordered by creation, then ID.
	want := gate.State{Endpoints: []gate.Endpoint{panel, bot, other}}
	got, err := g.Load()
	if err != nil || utc(got) != utc(want) {
		t.Fatalf("round trip:\n got %s\nwant %s\n%v", utc(got), utc(want), err)
	}
	wantWeb := webapp.State{Creds: []webapp.Cred{cred}, Invites: []webapp.Invite{inv, setup}, Sessions: []webapp.Session{login}}
	gotWeb, err := web.Load()
	if err != nil || webUTC(gotWeb) != webUTC(wantWeb) {
		t.Fatalf("round trip:\n got %s\nwant %s\n%v", webUTC(gotWeb), webUTC(wantWeb), err)
	}
	if !gotWeb.Invites[1].ExpiresAt.IsZero() || !got.Endpoints[1].LastSeenAt.IsZero() {
		t.Errorf("zero times: %+v %+v", gotWeb.Invites[1], got.Endpoints[1])
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
	// A gate update keeps the device and the passkeys.
	panel.Name = "Mac"
	must(t, g.Apply(gate.Change{Endpoints: []gate.Endpoint{panel}}))
	if d, _ := s.Device(panel.ID); d != "laptop" {
		t.Errorf("device after a rename: %q", d)
	}
	if st, _ := web.Load(); len(st.Creds) != 1 || len(st.Creds[0].Passkeys) != 1 {
		t.Errorf("passkeys after a rename: %+v", st.Creds)
	}

	// An update, a deletion with its rules, all in one change; then from a new connection.
	must(t, s.SetTerminalRules(bot.ID, []byte(`{"agents":["pi"]}`)))
	bot.Name, bot.TokenHash = "bot2", "def"
	must(t, g.Apply(gate.Change{DeleteEndpoints: []string{bot.ID}, Endpoints: []gate.Endpoint{other}}))
	must(t, web.Apply(webapp.Change{DeleteInvites: []string{inv.ID}, DeleteSessions: []string{"s1"}}))
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
	gotWeb, _ = s2.Web().Load()
	if len(got.Endpoints) != 3 || len(gotWeb.Invites) != 1 || len(gotWeb.Sessions) != 0 || got.Endpoints[2].TokenHash != "" {
		t.Fatalf("after: %s %s", utc(got), webUTC(gotWeb))
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
	if mac.Kind != gate.KindPanel || mac.Name != "kumo mac" || mac.AvatarVersion != "av1" ||
		!mac.ApprovedAt.Equal(f.T0) || !mac.LastSeenAt.Equal(f.T0.Add(time.Hour)) {
		t.Errorf("mac: %+v", mac)
	}
	wst, err := s.Web().Load()
	if err != nil {
		t.Fatal(err)
	}
	creds := map[string]webapp.Cred{}
	for _, c := range wst.Creds {
		creds[c.Login] = c
	}
	if c := creds[f.Mac]; len(c.Passkeys) != 1 || string(c.UserHandle) != "\x01\x02" {
		t.Errorf("mac's passkeys: %+v", c)
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
	inv := map[string]webapp.Invite{}
	for _, i := range wst.Invites {
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
	if len(wst.Sessions) != 1 || wst.Sessions[0].Login != f.Mac {
		t.Errorf("web sessions: %+v", wst.Sessions)
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

	// Through ir_web and the gate: the TOKENs and the browser's cookie are let in as before, the
	// browser as ir_web speaking for its login.
	const origin = "https://ir.example.com"
	now := func() time.Time { return f.T0.Add(2 * time.Hour) }
	g, err := gate.New(gate.Config{Store: s.Gate(), Now: now})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	tok, err := webapp.AppToken(g, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	web, err := webapp.New(webapp.Config{PublicOrigin: origin, Core: g, Token: tok, Store: s.Web(), Now: now})
	if err != nil {
		t.Fatal(err)
	}
	defer web.Close()
	var seen gate.Identity
	core := g.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen, _ = gate.FromContext(r.Context())
		if seen.App == gate.WebApp { // as the server's withLogin
			info, _ := g.Seen(r.Header.Get(webapp.HeaderUser))
			seen.Login, seen.Name = info.ID, info.Name
		}
	}))
	h := web.Handler(core, http.NotFoundHandler())
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
	if id, code := do(func(r *http.Request) { r.AddCookie(&http.Cookie{Name: webapp.CookieName, Value: f.Cookie}) }); code != 200 || !id.Web() || id.Login != f.Mac || id.Name != "kumo mac" {
		t.Errorf("mac's browser: %d %+v", code, id)
	}
	if _, code := do(func(r *http.Request) { r.Header.Set("Authorization", "Bearer ir_"+token("x")) }); code != http.StatusUnauthorized {
		t.Errorf("a TOKEN never given: %d", code)
	}
}
