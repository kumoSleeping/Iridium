package agents

import (
	"fmt"
	"path"
	"slices"
	"strings"
	"testing"
	"time"

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

// v7 is a UUIDv7 made at t, as Codex makes its thread ids.
func v7(t time.Time, tail string) string {
	h := fmt.Sprintf("%012x", t.UnixMilli())
	return h[:8] + "-" + h[8:] + "-7" + tail[:3] + "-8" + tail[3:6] + "-" + tail[6:18]
}

// Codex's rollout at an executor: by the thread id's time, in the day folder of the date where
// Codex runs (one either side of the UTC date), or archived; an id with no time, the newest day
// folders, bounded.
func TestCodexFindRemote(t *testing.T) {
	info, _ := Get(api.AgentCodex)
	at := func(s string) time.Time { tm, _ := time.Parse(time.RFC3339, s); return tm }
	same := v7(at("2026-11-08T12:31:00Z"), "aaaaaaaaaaaaaaaaaa")
	east := v7(at("2026-11-08T23:30:00Z"), "bbbbbbbbbbbbbbbbbb") // UTC+8: the 9th there
	west := v7(at("2026-11-08T03:00:00Z"), "cccccccccccccccccc") // UTC-5: the 7th there
	archived := v7(at("2026-01-02T00:00:00Z"), "dddddddddddddddddd")
	missing := v7(at("2026-11-08T12:00:00Z"), "eeeeeeeeeeeeeeeeee")
	far := v7(at("2026-11-08T12:00:00Z"), "ffffffffffffffffff") // two days off: not looked for
	const old = "0193a3c2-7d1e-4a51-9b8f-0c4d5e6f7a8b"          // a UUIDv4: no time in it
	const oldest = "0193a3c2-7d1e-4a51-9b8f-0c4d5e6f7a8c"       // past the bound
	rollout := func(ts, id string) string { return "rollout-" + ts + "-" + id + ".jsonl" }
	files := map[string][]string{
		"/h/.codex/sessions/2026/11/08": {"notes.txt", rollout("2026-11-08T20-31-00", same) + ".bak", rollout("2026-11-08T20-31-00", same)},
		"/h/.codex/sessions/2026/11/09": {rollout("2026-11-09T07-30-00", east)},
		"/h/.codex/sessions/2026/11/07": {rollout("2026-11-07T22-00-00", west)},
		"/h/.codex/sessions/2026/11/10": {rollout("2026-11-10T12-00-00", far)},
		"/h/.codex/archived_sessions":   {rollout("2026-01-02T08-00-00", archived)},
		"/h/.codex/sessions/2025/03/02": {rollout("2025-03-02T10-00-00", old)},
		"/c/sessions/2026/11/08":        {rollout("2026-11-08T12-31-00", same)},
	}
	dirs := map[string][]string{"/h/.codex/sessions": {"2025", "2026", "tmp"}, "/h/.codex/sessions/2026": {"11"},
		"/h/.codex/sessions/2026/11": {"07", "08", "09", "10"}, "/h/.codex/sessions/2025": {"03", "02"}}
	// 2025/02 has 400 day folders (more than a month has, to make the count), the oldest thread
	// in its first: past the bound with the five newer ones.
	dirs["/h/.codex/sessions/2025/03"] = []string{"02"}
	for d := 400; d >= 1; d-- {
		dirs["/h/.codex/sessions/2025/02"] = append(dirs["/h/.codex/sessions/2025/02"], fmt.Sprintf("%03d", d))
	}
	files["/h/.codex/sessions/2025/02/001"] = []string{rollout("2025-02-01T10-00-00", oldest)}
	var lists int
	where := func(id string, env ...string) RemoteWhere {
		return RemoteWhere{ID: id, User: "/h", Env: env, Join: path.Join,
			List: func(dir string) ([]string, error) { lists++; return files[dir], nil },
			Dirs: func(dir string) ([]string, error) { return dirs[dir], nil }}
	}
	for _, c := range []struct {
		id    string
		env   []string
		want  string
		lists int // at most
	}{
		{same, nil, "/h/.codex/sessions/2026/11/08/" + rollout("2026-11-08T20-31-00", same), 1},
		{east, nil, "/h/.codex/sessions/2026/11/09/" + rollout("2026-11-09T07-30-00", east), 3},
		{west, nil, "/h/.codex/sessions/2026/11/07/" + rollout("2026-11-07T22-00-00", west), 2},
		{archived, nil, "/h/.codex/archived_sessions/" + rollout("2026-01-02T08-00-00", archived), 4},
		{missing, nil, "", 4},
		{far, nil, "", 4},
		{same, info.RemoteEnv(nil, "/c"), "/c/sessions/2026/11/08/" + rollout("2026-11-08T12-31-00", same), 1},
		{old, nil, "/h/.codex/sessions/2025/03/02/" + rollout("2025-03-02T10-00-00", old), 5},
		{oldest, nil, "", 401},
		{"", nil, "", 0},
		{"../x", nil, "", 0},
	} {
		lists = 0
		if got, err := info.FindRemote(where(c.id, c.env...)); err != nil || got != c.want || lists > c.lists {
			t.Errorf("%s %v: %q %v (%d lists), want %q", c.id, c.env, got, err, lists, c.want)
		}
	}
	// The bound: 400 day folders listed, the archive too, and the oldest not reached.
	lists = 0
	if got, _ := info.FindRemote(where(oldest)); got != "" || lists != 401 {
		t.Errorf("an id with no time: %q, %d lists, want 401", got, lists)
	}
	// Its home as the executor said it.
	w := where(same)
	w.Home = "/c"
	if got, _ := info.FindRemote(w); got != "/c/sessions/2026/11/08/"+rollout("2026-11-08T12-31-00", same) {
		t.Errorf("home said: %q", got)
	}
	if env := info.RemoteEnv([]string{"A=1"}, "/c"); agent.GetEnv(env, "CODEX_HOME") != "/c" {
		t.Errorf("env %v", env)
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

// Every agent's home is denied, where it is moved too; every agent names its sessions to the
// processes it starts, Iridium's own variable believed first.
func TestHomesAndSessionVars(t *testing.T) {
	env := map[string]string{"DSH_HOME": "/srv/dsh", "CODEX_HOME": "/srv/codex"}
	got := Homes("/home/u", func(k string) string { return env[k] })
	for _, want := range []string{"/home/u/.claude", "/home/u/.codex", "/home/u/.pi", "/home/u/.dsh", "/srv/dsh", "/srv/codex"} {
		if !slices.Contains(got, want) {
			t.Errorf("Homes lacks %s: %v", want, got)
		}
	}
	for _, i := range All() {
		if i.HomeDir == "" || len(i.SessionEnv) == 0 {
			t.Errorf("%s: no HomeDir or SessionEnv", i.Agent)
		}
	}
	want := []string{"IRIDIUM_SESSION", "CLAUDE_CODE_SESSION_ID", "CODEX_THREAD_ID", "CODEX_SESSION_ID", "DSH_SESSION_ID", "PI_SESSION_ID"}
	if vars := SessionVars(); !slices.Equal(vars, want) {
		t.Errorf("SessionVars = %v, want %v (docs/api.md)", vars, want)
	}
	if len(sessionVarOrder) != len(all) {
		t.Errorf("sessionVarOrder lacks an agent: %v", sessionVarOrder)
	}
}
