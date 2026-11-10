package cli

import (
	"strings"
	"testing"
)

// Claude is given by a person only, as an app or login is made: a pairing allowed as the owner's
// own, an invite with --claude. An agent's invite --claude only asks, for a login that has Claude
// (or the host) to allow. Nothing turns it on or off after: ir apps only lists.
func TestOwnerOnlyFromAPerson(t *testing.T) {
	person, _ := daemonFor(t)
	pairApp(t, person, "bot", true)
	pairApp(t, person, "plain", false)
	claudeOf := func(name string) string {
		t.Helper()
		_, out, _ := runAs(person, "apps")
		for _, line := range strings.Split(out, "\n") {
			if f := strings.Fields(line); len(f) > 3 && f[0] == name {
				return f[3]
			}
		}
		t.Fatalf("no %s in\n%s", name, out)
		return ""
	}
	if claudeOf("bot") != "开" || claudeOf("plain") != "—" || claudeOf("主机") != "开" {
		t.Fatal("the list's Claude column")
	}
	for _, args := range [][]string{{"apps", "bot", "--claude", "on"}, {"invite", "--owner"}, {"invite", "a", "b"}} {
		if code, _, _ := runAs(person, args...); code != 2 {
			t.Errorf("%v: %d, want usage", args, code)
		}
	}
}
