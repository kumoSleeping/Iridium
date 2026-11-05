package store

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/kumoSleeping/iridium/internal/gate"
)

func utc(st gate.State) string {
	for i := range st.Endpoints {
		e := &st.Endpoints[i]
		e.CreatedAt, e.LastSeenAt = e.CreatedAt.UTC(), e.LastSeenAt.UTC()
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

// The gate's state goes into the database and comes back the same, and a terminal deleted takes
// its permissions along.
func TestGateStore(t *testing.T) {
	s, path := open(t)
	g := s.Gate()
	if st, err := g.Load(); err != nil || len(st.Endpoints)+len(st.Invites)+len(st.Sessions) != 0 {
		t.Fatalf("empty: %+v %v", st, err)
	}
	t0 := time.Date(2026, 11, 5, 12, 0, 0, 123456789, time.UTC)
	panel := gate.Endpoint{ID: "aaaaaaaaaaaaaaaa", Name: "mac", Kind: gate.KindPanel, UserHandle: []byte{1, 2, 3},
		Passkeys: []gate.Passkey{{Credential: webauthn.Credential{ID: []byte("cred"), PublicKey: []byte("pk")}, CreatedAt: t0, LastUsedAt: t0}},
		Avatar:   []byte{0x89, 'P', 'N', 'G'}, AvatarVersion: "v1", CreatedAt: t0, LastSeenAt: t0.Add(time.Minute)}
	bot := gate.Endpoint{ID: "bbbbbbbbbbbbbbbb", Name: "bot", Kind: gate.KindChannel, TokenHash: "abc", CreatedAt: t0.Add(time.Second)}
	other := gate.Endpoint{ID: "cccccccccccccccc", Name: "other", Kind: gate.KindChannel, CreatedAt: t0.Add(2 * time.Second)}
	setup := gate.Invite{ID: "setup", Hash: "h0", Setup: true, CreatedAt: t0}
	inv := gate.Invite{ID: "invite01", Hash: "h1", Name: "pad", CreatedAt: t0, ExpiresAt: t0.Add(10 * time.Minute)}
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

	// An update, a deletion with its rules, all in one change; then from a new connection.
	must(t, s.SetTerminalRules(bot.ID, []byte(`{"agents":["pi"]}`)))
	bot.Name, bot.TokenHash = "bot2", "def"
	must(t, g.Apply(gate.Change{DeleteEndpoints: []string{bot.ID}, DeleteInvites: []string{inv.ID}, DeleteSessions: []string{"s1"}, Endpoints: []gate.Endpoint{other}}))
	if _, ok, _ := s.TerminalRules(bot.ID); ok {
		t.Error("rules outlived their terminal")
	}
	// Two channels without a token are fine; two with the same are not.
	other2 := gate.Endpoint{ID: "dddddddddddddddd", Name: "o2", Kind: gate.KindChannel, CreatedAt: t0}
	must(t, g.Apply(gate.Change{Endpoints: []gate.Endpoint{other2}}))
	a, b := other, other2
	a.TokenHash, b.TokenHash = "same", "same"
	if err := g.Apply(gate.Change{Endpoints: []gate.Endpoint{a, b}}); err == nil {
		t.Error("two terminals with one token")
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

func TestRulesOpenersTokens(t *testing.T) {
	s, _ := open(t)
	if _, ok, err := s.TerminalRules("x"); ok || err != nil {
		t.Fatalf("no rules: %v %v", ok, err)
	}
	must(t, s.SetTerminalRules("x", []byte(`{"a":1}`)))
	must(t, s.SetTerminalRules("x", []byte(`{"a":2}`)))
	if r, ok, err := s.TerminalRules("x"); !ok || err != nil || string(r) != `{"a":2}` {
		t.Fatalf("rules %s %v %v", r, ok, err)
	}
	must(t, s.SetOpener("s1", "x"))
	must(t, s.SetOpener("s1", "y")) // the first stays
	if o, _ := s.Openers(); o["s1"] != "x" || s.Opener("s1") != "x" || s.Opener("s2") != "" {
		t.Fatalf("openers %v", o)
	}
	must(t, s.AddUsage(Usage{Terminal: "x", Session: "s1", Agent: "claude", Model: "m", Input: 1, Output: 2, CacheRead: 3, CacheWrite: 4}))
	must(t, s.AddUsage(Usage{Terminal: "x", Session: "s1", Agent: "claude", Model: "m", Input: 100, At: 1}))
	must(t, s.AddUsage(Usage{Terminal: "y", Session: "s1", Agent: "claude", Model: "m", Input: 1000}))
	if n, err := s.TokensSince("x", 2); err != nil || n != 10 {
		t.Fatalf("tokens %d %v", n, err)
	}
	if n, _ := s.TokensSince("z", 0); n != 0 {
		t.Fatalf("none: %d", n)
	}
}

func TestSourceByText(t *testing.T) {
	s, _ := open(t)
	must(t, s.AddInput(Input{ID: "1", Session: "a", Terminal: "pppppppppppppppp", Tag: "mac", Text: "hello", State: "sent", Created: 1}))
	must(t, s.AddInput(Input{ID: "2", Session: "a", Terminal: "local", Tag: "本机", Text: "hello", State: "sent", Created: 2}))
	must(t, s.AddInput(Input{ID: "3", Session: "a", Terminal: "local", Tag: "本机", Text: "/compact", State: "sent", Created: 3}))
	must(t, s.AddInput(Input{ID: "4", Session: "b", Terminal: "local", Tag: "本机", Text: "queued", State: "queued", Created: 4}))
	for _, c := range []struct{ session, text, terminal, tag string }{
		{"a", "[mac] hello", "pppppppppppppppp", "mac"},
		{"a", "[本机] hello", "local", "本机"},
		{"a", "/compact", "local", "本机"},
		{"a", "hello", "", ""},       // typed in a terminal: no tag
		{"b", "[本机] queued", "", ""}, // not sent yet
		{"b", "[mac] hello", "", ""}, // another session's
	} {
		id, tag, ok := s.SourceByText(c.session, c.text)
		if id != c.terminal || tag != c.tag || ok != (c.terminal != "") {
			t.Errorf("%s %q: %q %q %v", c.session, c.text, id, tag, ok)
		}
	}
}
