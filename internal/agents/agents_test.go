package agents

import (
	"strings"
	"testing"

	"github.com/kumoSleeping/iridium/internal/agent"
	"github.com/kumoSleeping/iridium/internal/api"
)

// pi's transcript at a Windows executor: under its home there by default, or where its variables
// say.
func TestPiFindRemote(t *testing.T) {
	info, _ := Get(api.AgentPi)
	const id = "0193a3c2-7d1e-7a51-9b8f-0c4d5e6f7a8b"
	files := map[string][]string{
		`C:\Users\k\.pi\agent\sessions\--C--w--`: {"2026-11-08T10-00-00-000Z_other.jsonl", "2026-11-08T10-00-00-000Z_" + id + ".jsonl"},
		`D:\pi\sessions\--C--w--`:                {"x_" + id + ".jsonl"},
		`E:\only`:                                {"y_" + id + ".jsonl"},
	}
	where := func(env ...string) RemoteWhere {
		return RemoteWhere{ID: id, Cwd: `C:\w`, User: `C:\Users\k`, Env: env,
			Join: func(elem ...string) string { return strings.Join(elem, `\`) },
			List: func(dir string) ([]string, error) { return files[dir], nil }}
	}
	for _, c := range []struct {
		env  []string
		want string
	}{
		{nil, `C:\Users\k\.pi\agent\sessions\--C--w--\2026-11-08T10-00-00-000Z_` + id + ".jsonl"},
		{info.RemoteEnv(nil, `D:\pi`), `D:\pi\sessions\--C--w--\x_` + id + ".jsonl"},
		{[]string{`PI_CODING_AGENT_SESSION_DIR=E:\only`}, `E:\only\y_` + id + ".jsonl"},
	} {
		if got, err := info.FindRemote(where(c.env...)); err != nil || got != c.want {
			t.Errorf("env %v: %q %v, want %q", c.env, got, err, c.want)
		}
	}
	w := where()
	w.ID = "../x"
	if got, _ := info.FindRemote(w); got != "" {
		t.Errorf("a bad id found %q", got)
	}
}

func TestRemoteRows(t *testing.T) {
	dsh, _ := Get(api.AgentDsh)
	for v, ok := range map[string]bool{"0.2.2-rc.1": true, "0.2.2": true, "0.3": true, "1.0.0": true, "0.2.1": false, "0.1.0": false, "": false} {
		if dsh.RemoteVersionOK(v) != ok {
			t.Errorf("dsh %q: want %v", v, ok)
		}
	}
	if pi, _ := Get(api.AgentPi); !pi.RemoteVersionOK("") {
		t.Error("pi has no least version")
	}
	if env := dsh.RemoteEnv([]string{"A=1"}, "/h"); agent.GetEnv(env, "DSH_HOME") != "/h" || agent.GetEnv(env, "A") != "1" {
		t.Errorf("env %v", env)
	}
	if claude, _ := Get(api.AgentClaude); len(claude.RemoteEnv([]string{"A=1"}, "/h")) != 1 {
		t.Error("Claude's home set: its login may be keyed by its config dir")
	}
	// Every agent that runs at an executor says where its transcript is there.
	for _, i := range All() {
		if i.MinRemote != "" && i.FindRemote == nil {
			t.Errorf("%s: a least version but no FindRemote", i.Name)
		}
	}
}
