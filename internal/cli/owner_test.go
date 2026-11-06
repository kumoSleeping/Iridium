package cli

import (
	"net/http"
	"strings"
	"testing"

	"github.com/kumoSleeping/iridium/internal/api"
)

// Claude is given by a person only: ir access <名字> --claude on refuses an agent (and so does the
// daemon, should an agent's ir skip the check); an agent's ir token --owner and ir invite --owner
// only ask, for a panel that has Claude (or 本机) to allow; --claude off is anyone's.
func TestOwnerOnlyFromAPerson(t *testing.T) {
	person, agent := daemonFor(t)
	if code, out, errs := runAs(agent, "access", "bot", "--claude", "on"); code != 2 || !strings.Contains(errs, "agent 不能直接给终端 Claude") || out != "" {
		t.Errorf("an agent's --claude on: %d %q %q", code, out, errs)
	}
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
