package deliver

import (
	"strings"
	"testing"
)

func TestMentions(t *testing.T) {
	nicks := []string{"claude", "claude2", "审稿"}
	for text, want := range map[string]string{
		"@claude look":                          "claude",
		"hi @Claude2, and @claude":              "claude2 claude",
		"@审稿 看一下":                               "审稿",
		"请@审稿看一下":                               "", // inside words: "审稿看一下" is not the nick
		"请 @审稿，看一下 @claude。":                    "审稿 claude",
		"mail me@claude.dev or a@claude":        "",
		"`@claude` and\n```\n@claude2\n```\nno": "",
		"> @claude said\nfine":                  "",
		"@claudette @nobody":                    "",
		"@claude @claude":                       "claude",
		"see https://x.dev/@claude":             "",
	} {
		if got := strings.Join(Mentions(text, nicks), " "); got != want {
			t.Errorf("%q: %q, want %q", text, got, want)
		}
	}
	for n, ok := range map[string]bool{"claude2": true, "审稿": true, "a b": false, "": false, "a@b": false, "x-y_z": true} {
		if ValidNick(n) != ok {
			t.Errorf("ValidNick(%q) != %v", n, ok)
		}
	}
}

func TestRoute(t *testing.T) {
	agents := []Agent{{"s1", "claude", []string{"*"}}, {"s2", "codex", nil}, {"s3", "pi", []string{"web/kumo", "agent:s1"}}}
	wake := func(d Decision) string { return strings.Join(d.Wake, " ") }
	person := From{Member: "web/kumo"}
	// A person: whoever is named; nobody named, those whose set has them.
	if d := Route("hello", person, agents); wake(d) != "s1 s3" {
		t.Errorf("a person, nobody named: %+v", d)
	}
	if d := Route("hello", From{Member: "qq/123"}, agents); wake(d) != "s1" {
		t.Errorf("another person: %+v", d)
	}
	// Named: only whom it names, not those whose set has the sender too.
	if d := Route("@codex hello", person, agents); wake(d) != "s2" || len(d.Named) != 1 {
		t.Errorf("a person naming one: %+v", d)
	}
	// An agent: whom it names, or those subscribed to it ("*" is people alone); never itself.
	if d := Route("done", From{Member: "agent:s1", Session: "s1"}, agents); wake(d) != "s3" {
		t.Errorf("an agent speaking: %+v", d)
	}
	if d := Route("done", From{Member: "agent:s2", Session: "s2"}, agents); wake(d) != "" {
		t.Errorf("an agent nobody follows: %+v", d)
	}
	if d := Route("done", From{Member: "agent:s1", Session: "s1", Quiet: true}, agents); wake(d) != "" {
		t.Errorf("an agent on its way: %+v", d)
	}
	if d := Route("@codex and @claude", From{Member: "agent:s1", Session: "s1"}, agents); wake(d) != "s2" {
		t.Errorf("an agent naming: %+v", d)
	}
	// @全体成员 and @全体Agent: every agent but the one speaking, as if each were named.
	if d := Route("@全体成员 look", person, agents); wake(d) != "s1 s2 s3" || len(d.Named) != 3 {
		t.Errorf("a person to all: %+v", d)
	}
	if d := Route("@全体agent look", From{Member: "agent:s1", Session: "s1"}, agents); wake(d) != "s2 s3" {
		t.Errorf("an agent to all agents: %+v", d)
	}
	if d := Route("`@全体成员`", person, agents); wake(d) != "s1 s3" {
		t.Errorf("in code: %+v", d)
	}
	if ValidNick("全体成员") || ValidNick("全体AGENT") {
		t.Error("a reserved name taken as a nick")
	}
	for set, want := range map[string]bool{"*": true, "web/*": true, "web/kumo": true, "qq/*": false, "agent:s1": false} {
		if Receives([]string{set}, "web/kumo") != want {
			t.Errorf("Receives(%q, web/kumo) != %v", set, want)
		}
	}
	if Receives([]string{"*", "web/*"}, "agent:s1") || !Receives([]string{"agent:s1"}, "agent:s1") {
		t.Error("an agent's words: only an entry of its own")
	}
}

// A reaction goes to whom a message that names nobody would: those whose receive set has its
// author; an agent's only to those subscribed to it, never to itself.
func TestReacts(t *testing.T) {
	agents := []Agent{{Session: "a", Nick: "a", Recv: []string{"*"}}, {Session: "b", Nick: "b", Recv: []string{}}, {Session: "c", Nick: "c", Recv: []string{"agent:a", "ir_web/kumo"}}}
	for _, tc := range []struct {
		from From
		want string
	}{
		{From{Member: "ir_web/kumo"}, "a c"},
		{From{Member: "ir_web/bob"}, "a"},
		{From{Member: "agent:a", Session: "a"}, "c"},
		{From{Member: "agent:c", Session: "c"}, ""},
	} {
		if got := strings.Join(Reacts(tc.from, agents), " "); got != tc.want {
			t.Errorf("%+v: %q, want %q", tc.from, got, tc.want)
		}
	}
}
