package cli

import (
	"net/http"
	"strings"
	"testing"

	"github.com/kumoSleeping/iridium/internal/api"
)

// Claude is given at the machine by a person only: ir token --owner, ir invite --owner and
// ir access <名字> --claude on refuse an agent (and so does the daemon, should an agent's ir skip
// the check); --claude off is anyone's.
func TestOwnerOnlyFromAPerson(t *testing.T) {
	person, agent := daemonFor(t)
	for _, args := range [][]string{{"token", "bot", "--owner"}, {"invite", "--owner"}, {"invite", "pad", "--owner"}, {"access", "bot", "--claude", "on"}} {
		if code, out, errs := runAs(agent, args...); code != 2 || !strings.Contains(errs, "agent 不能给终端 Claude") || out != "" {
			t.Errorf("an agent's %v: %d %q %q", args, code, out, errs)
		}
	}
	if code, err := agent.call("POST", "/api/terminals/tokens", api.NewInvite{Name: "bot", Owner: true}, nil); code != http.StatusForbidden {
		t.Errorf("the daemon let an agent's --owner through: %d %v", code, err)
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

	// Off: an agent may. On again: a person only.
	if code, out, errs := runAs(agent, "access", "BOT", "--claude", "off"); code != 0 || !strings.Contains(out, "关了 bot") {
		t.Fatalf("off: %d %q %q", code, out, errs)
	}
	if claudeOf("bot") != "—" {
		t.Fatal("still on")
	}
	if code, out, errs := runAs(person, "access", "bot", "--claude=on"); code != 0 || !strings.Contains(out, "开了 Claude") {
		t.Fatalf("on: %d %q %q", code, out, errs)
	}
	if claudeOf("bot") != "开" {
		t.Fatal("not on")
	}
	if code, out, _ := runAs(person, "access", "bot", "--claude", "on"); code != 0 || !strings.Contains(out, "本来就开着") {
		t.Fatalf("on twice: %d %q", code, out)
	}
	for _, args := range [][]string{{"access", "bot"}, {"access", "bot", "--claude", "yes"}, {"token", "--owner"}, {"invite", "a", "b"}} {
		if code, _, _ := runAs(person, args...); code != 2 {
			t.Errorf("%v: %d, want usage", args, code)
		}
	}
	if code, _, errs := runAs(person, "access", "nobody", "--claude", "on"); code != 1 || !strings.Contains(errs, "nobody") {
		t.Errorf("nobody: %d %q", code, errs)
	}
}
