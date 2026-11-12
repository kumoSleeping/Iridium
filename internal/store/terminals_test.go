package store

import (
	"testing"
	"time"
)

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
	// The host's device: none, then set and set again.
	if d, err := s.Device("local"); d != "" || err != nil {
		t.Fatalf("no device: %q %v", d, err)
	}
	must(t, s.SetDevice("local", "phone"))
	must(t, s.SetDevice("local", "computer"))
	if d, _ := s.Devices(); len(d) != 1 || d["local"] != "computer" {
		t.Fatalf("devices %v", d)
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
	// keep: only the sessions it keeps.
	must(t, s.AddUsage(Usage{Terminal: "x", Session: "s2", Agent: "pi", Model: "m", Input: 7}))
	day := func(time.Time) string { return "d" }
	got, err := s.UsageBy(0, day, nil, func(id string) bool { return id == "s2" })
	if err != nil || len(got) != 1 || got[0].Input != 7 || got[0].Agent != "pi" {
		t.Fatalf("kept s2: %+v %v", got, err)
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
