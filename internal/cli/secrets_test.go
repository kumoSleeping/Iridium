package cli

import (
	"encoding/base64"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
)

// answer answers the one pending confirmation of kind as a panel would, with a.
func answer(t *testing.T, as adminEnv, kind api.NeedKind, a api.NeedAnswer) api.Need {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		var cs api.Needs
		as.call("GET", "/v1/internal/needs", nil, &cs)
		if len(cs.Pending) == 1 && cs.Pending[0].Kind == kind {
			if _, err := as.call("POST", "/v1/internal/needs/"+cs.Pending[0].ID, a, nil); err != nil {
				t.Fatal(err)
			}
			return cs.Pending[0]
		}
		if time.Now().After(deadline) {
			t.Fatalf("no %s pending: %+v", kind, cs)
		}
	}
}

func TestSecretCommands(t *testing.T) {
	person, agent := daemonFor(t)
	const val = "tok/en+value=42"

	// A person pipes the value in (no terminal on stdin); it is never printed.
	p := person
	p.stdin = strings.NewReader(val + "\n")
	code, out, errs := runAs(p, "secret", "set", "API_TOKEN", "--note", "the API")
	if code != 0 || !strings.Contains(out, "已存 API_TOKEN") || strings.Contains(out+errs, val) {
		t.Fatalf("set: %d %q %q", code, out, errs)
	}
	if code, _, _ := runAs(p, "secret", "set", "lower"); code != 2 {
		t.Fatalf("a bad name: %d", code)
	}

	// An agent: the panel types the value; the agent hears only that it is stored.
	got := agentRun(agent, "secret", "set", "NAS_PASSWORD", "--note", "mount")
	cf := answer(t, person, api.NeedSecretSet, api.NeedAnswer{Allow: true, Value: "nas-secret-pw"})
	r := <-got
	if r[0] != "0" || !strings.Contains(r[1], "已存 NAS_PASSWORD") || strings.Contains(r[1]+r[2], "nas-secret-pw") || cf.Note != "mount" {
		t.Fatalf("agent set: %q %+v", r, cf)
	}
	got = agentRun(agent, "secret", "set", "OTHER")
	answer(t, person, api.NeedSecretSet, api.NeedAnswer{Allow: false})
	if r := <-got; r[0] != "1" || !strings.Contains(r[1], "拒绝") {
		t.Fatalf("denied: %q", r)
	}

	code, out, _ = runAs(agent, "secret", "ls")
	if code != 0 || !strings.Contains(out, "API_TOKEN") || !strings.Contains(out, "the API") || strings.Contains(out, val) {
		t.Fatalf("ls: %d %q", code, out)
	}

	// ir run: the value in the arguments and the environment, a file for {{file:}}, the output
	// masked (as is, base64, URL encoded), the exit code the command's.
	enc := base64.StdEncoding.EncodeToString([]byte(val))
	script := `echo "arg=$1"; echo "env=$API_TOKEN"; printf '%s' "$1" | base64; echo; cat "$2"; echo; echo "file=$2"; echo "err=$1" >&2; exit 3`
	code, out, errs = runAs(agent, "run", "--", "sh", "-c", script, "sh", "{{API_TOKEN}}", "{{file:NAS_PASSWORD}}")
	if code != 3 {
		t.Fatalf("run: exit %d, %q %q", code, out, errs)
	}
	if strings.Contains(out+errs, val) || strings.Contains(out, enc) || strings.Contains(out, "nas-secret-pw") {
		t.Fatalf("not masked: %q %q", out, errs)
	}
	for _, want := range []string{"arg=[secret:API_TOKEN]", "env=[secret:API_TOKEN]", "[secret:NAS_PASSWORD]", "file=/"} {
		if !strings.Contains(out, want) {
			t.Fatalf("no %q in %q", want, out)
		}
	}
	if !strings.Contains(errs, "err=[secret:API_TOKEN]") {
		t.Fatalf("stderr: %q", errs)
	}
	// The file is gone once the command ends.
	file := out[strings.Index(out, "file=")+5:]
	file = file[:strings.Index(file, "\n")]
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("the file stays: %s %v", file, err)
	}
	if code, _, errs := runAs(agent, "run", "--secret", "NOPE", "--", "true"); code != 1 || !strings.Contains(errs, "NOPE") {
		t.Fatalf("no such secret: %d %q", code, errs)
	}
	if code, _, _ := runAs(agent, "run", "--", "/no/such/program"); code != 127 {
		t.Fatalf("no such program: %d", code)
	}

	// Confirmed every time: ir run waits for the panel, then runs; denied, it does not.
	if _, err := person.call("PUT", "/v1/internal/secrets/API_TOKEN", api.SetSecret{Always: new(true)}, nil); err != nil {
		t.Fatal(err)
	}
	got = agentRun(agent, "run", "--secret", "API_TOKEN", "--", "sh", "-c", `echo "v=$API_TOKEN"`)
	cf = answer(t, person, api.NeedSecretUse, api.NeedAnswer{Allow: true})
	if r := <-got; r[0] != "0" || !strings.Contains(r[1], "v=[secret:API_TOKEN]") || !strings.Contains(r[2], "等确认") {
		t.Fatalf("confirmed run: %q", r)
	}
	if cf.Command != `sh -c echo "v=$API_TOKEN"` || cf.Secrets[0] != "API_TOKEN" {
		t.Fatalf("confirmation: %+v", cf)
	}
	got = agentRun(agent, "run", "--secret", "API_TOKEN", "--", "sh", "-c", "echo ran")
	answer(t, person, api.NeedSecretUse, api.NeedAnswer{Allow: false})
	if r := <-got; r[0] != "1" || strings.Contains(r[1], "ran") || !strings.Contains(r[2], "拒绝") {
		t.Fatalf("denied run: %q", r)
	}

	if code, _, _ := runAs(agent, "secret", "rm", "API_TOKEN"); code != 1 {
		t.Fatal("an agent removed a credential")
	}
	if code, out, _ := runAs(person, "secret", "rm", "API_TOKEN"); code != 0 || !strings.Contains(out, "删了") {
		t.Fatalf("rm: %d %q", code, out)
	}
}
