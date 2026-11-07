package cli

import (
	"net/http"
	"strings"
	"testing"

	"github.com/kumoSleeping/iridium/internal/api"
)

// Claude is given by a person only, as a terminal is made: an agent's ir token --owner and ir
// invite --owner only ask, for a panel that has Claude (or 本机) to allow. Nothing turns it on or
// off after: ir access only lists.
func TestOwnerOnlyFromAPerson(t *testing.T) {
	person, agent := daemonFor(t)
	var p api.Pending
	if code, err := agent.call("POST", "/api/terminals/tokens", api.NewInvite{Name: "bot", Owner: true}, &p); code != http.StatusAccepted || !p.Confirm.Owner {
		t.Errorf("an agent's --owner did not ask: %d %v %+v", code, err, p)
	}

	code, out, errs := runAs(person, "token", "bot", "--owner")
	if code != 0 || !strings.Contains(out, "带 Claude") || strings.Contains(out, "不带") {
		t.Fatalf("token --owner: %d %q %q", code, out, errs)
	}
	if code, out, _ := runAs(person, "token", "plain"); code != 0 || !strings.Contains(out, "不带 Claude") {
		t.Fatalf("plain token: %d %q", code, out)
	}
	claudeOf := func(name string) string {
		t.Helper()
		_, out, _ := runAs(person, "access")
		for _, line := range strings.Split(out, "\n") {
			if f := strings.Fields(line); len(f) > 3 && f[0] == name {
				return f[3]
			}
		}
		t.Fatalf("no %s in\n%s", name, out)
		return ""
	}
	if claudeOf("bot") != "开" || claudeOf("plain") != "—" || claudeOf("本机") != "开" {
		t.Fatal("the list's Claude column")
	}
	// Not after: --owner for one there is without Claude is refused, and it stays without.
	if code, _, errs := runAs(person, "token", "plain", "--owner"); code != 1 || !strings.Contains(errs, "claude is set as a terminal is made") {
		t.Errorf("--owner after: %d %q", code, errs)
	}
	if claudeOf("plain") != "—" {
		t.Fatal("claude given after")
	}
	for _, args := range [][]string{{"access", "bot"}, {"access", "bot", "--claude", "on"}, {"token", "--owner"}, {"invite", "a", "b"}} {
		if code, _, _ := runAs(person, args...); code != 2 {
			t.Errorf("%v: %d, want usage", args, code)
		}
	}
}
