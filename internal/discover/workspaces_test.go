package discover

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/store"
)

// A session at another machine that discovery put in a group of another machine's (its executor
// paired again: a new terminal ID; or its caller's across machines, as once was) goes to the group
// of its folder at its machine now; one you moved there stays.
func TestRegroupRemote(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "iridium.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	x := New(Options{Store: st, Home: "/home/u", Logf: t.Logf})
	old, err := st.CreateWorkspace("Starmod", "old:C:/Code/Starmod")
	if err != nil {
		t.Fatal(err)
	}
	add := func(id, cwd string) {
		x.entries[id] = &entry{onDisk: onDisk{id: id, agent: api.AgentDsh}, glimpse: glimpse{cwd: cwd}, mtime: time.Now(), machine: "new"}
		if _, err := st.Show(string(api.AgentDsh), id); err != nil {
			t.Fatal(err)
		}
	}
	coord, err := st.CreateWorkspace("coord", "k:/srv/coord")
	if err != nil {
		t.Fatal(err)
	}
	st.SetMachine("k", []byte(`{}`)) // a machine still there
	local, err := st.CreateWorkspace("ir", "/home/u/ir")
	if err != nil {
		t.Fatal(err)
	}
	add("across", `C:\Code\Starmod`)
	if _, err := st.AssignAuto("across", coord); err != nil {
		t.Fatal(err)
	}
	add("here", `C:\Code\Starmod`)
	if _, err := st.AssignAuto("here", local); err != nil {
		t.Fatal(err)
	}
	add("auto", `C:\Code\Starmod`)
	add("moved", `C:\Code\Starmod`)
	add("drive", `C:\`)
	add("deep", `C:\x`)
	if _, err := st.AssignAuto("auto", old); err != nil {
		t.Fatal(err)
	}
	if err := st.Move("moved", old); err != nil {
		t.Fatal(err)
	}
	x.classify()
	a, _ := st.Assignments()
	ws, _ := st.Workspaces()
	path := map[int64]string{}
	for _, w := range ws {
		path[w.ID] = w.Path
	}
	if got := path[a["auto"].Workspace]; got != "new:C:/Code/Starmod" {
		t.Errorf("auto: in %q, want new:C:/Code/Starmod", got)
	}
	for _, id := range []string{"across", "here"} {
		if got := path[a[id].Workspace]; got != "new:C:/Code/Starmod" || a[id].Manual {
			t.Errorf("%s: in %q, want new:C:/Code/Starmod, as discovery put it", id, got)
		}
	}
	if a["moved"].Workspace != old || !a["moved"].Manual {
		t.Errorf("moved: %+v, want left in %d", a["moved"], old)
	}
	// A drive's root there holds only its own, as / here does.
	if got := path[a["deep"].Workspace]; got != "new:C:/x" {
		t.Errorf("deep: in %q, want new:C:/x", got)
	}
}

// A dsh session in none of DSH Desktop's workspaces (its 未分组) stays in none, not grouped; one
// filed under a workspace there is grouped by its folder. One put in a group before stays.
func TestLooseRemote(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "iridium.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	x := New(Options{Store: st, Home: "/home/u", Logf: t.Logf})
	chats, err := st.CreateWorkspace("新聊天", "m:C:/Users/o/Documents/DSH/2026-11-02/新聊天")
	if err != nil {
		t.Fatal(err)
	}
	add := func(id, cwd string, at time.Time) {
		x.entries[id] = &entry{onDisk: onDisk{id: id, agent: api.AgentDsh}, glimpse: glimpse{cwd: cwd}, mtime: at, machine: "m"}
		if _, err := st.Show(string(api.AgentDsh), id); err != nil {
			t.Fatal(err)
		}
	}
	quiet := time.Now().Add(-time.Hour)
	add("old", `C:\Users\o\Documents\DSH\2026-11-02\新聊天`, quiet)
	add("new", `C:\Users\o\Documents\DSH\2026-11-03\新聊天`, quiet)
	add("filed", `C:\Code\Starmod`, quiet)
	if _, err := st.AssignAuto("old", chats); err != nil {
		t.Fatal(err)
	}
	x.filed["m"] = map[string]bool{"filed": true}
	x.classify()
	a, _ := st.Assignments()
	if a["old"].Workspace != chats {
		t.Errorf("old: %+v, want left in %d", a["old"], chats)
	}
	if got, ok := a["new"]; ok && got.Workspace != 0 {
		t.Errorf("new: %+v, want in none", got)
	}
	if a["filed"].Workspace == 0 {
		t.Errorf("filed: %+v, want a group of its folder", a["filed"])
	}
	x.mu.Lock()
	loose := x.loose(x.entries["new"])
	x.mu.Unlock()
	if !loose {
		t.Error("new: its row not loose")
	}
}

// A session its caller starts goes in the caller's group only if that holds its folder: one at
// another machine is grouped by its own folder there (the page hangs it under its launcher).
func TestGroupForAcrossMachines(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "iridium.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	x := New(Options{Store: st, Home: "/home/u", Logf: t.Logf})
	remoteGroup, _ := st.CreateWorkspace("Starmod", "m:C:/Code/Starmod")
	localGroup, _ := st.CreateWorkspace("ir", "/home/u/ir")
	if got := x.GroupFor("/home/u/ir/web", remoteGroup); got != localGroup {
		t.Errorf("here, from a session at m's group: %d, want %d", got, localGroup)
	}
	if got := x.GroupFor("m:C:/Code/Starmod/x", localGroup); got != remoteGroup {
		t.Errorf("at m, from a session here: %d, want %d", got, remoteGroup)
	}
	if got := x.GroupFor("m:D:/x", localGroup); got == localGroup || got == remoteGroup || got == 0 {
		t.Errorf("at m, elsewhere: %d", got)
	}
	if got := x.GroupFor("/srv/other", localGroup); got == localGroup || got == 0 {
		t.Errorf("here, elsewhere than its caller's folder: %d", got)
	}
}

// Groups of subfolders made before their parent's (an executor's sessions all seen at once, the
// oldest in a subfolder) go into it, as folders there: what discovery put in them moves, a group
// left empty is dropped; one with a session you moved there stays with it. A home folder's group
// holds only its own, at a machine not connected too.
func TestNest(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "iridium.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	x := New(Options{Store: st, Home: "/home/u", Logf: t.Logf})
	group := func(path string) int64 {
		id, err := st.CreateWorkspace(filepath.Base(path), path)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	add := func(id, cwd string, ws int64) {
		x.entries[id] = &entry{onDisk: onDisk{id: id, agent: api.AgentDsh}, glimpse: glimpse{cwd: cwd}, mtime: time.Now(), machine: "m"}
		if _, err := st.Show(string(api.AgentDsh), id); err != nil {
			t.Fatal(err)
		}
		if ws != 0 {
			if _, err := st.AssignAuto(id, ws); err != nil {
				t.Fatal(err)
			}
		}
	}
	uc := group("m:C:/Code/Ds_Benchmark/UC")
	hy4 := group("m:C:/Code/Ds_Benchmark/hy4")
	deep := group("m:C:/Code/Ds_Benchmark/hy4/x")
	kept := group("m:C:/Code/Ds_Benchmark/kept")
	home := group("m:C:/Users/o")
	docs := group("m:C:/Users/o/Documents/a")
	bench := group("m:C:/Code/Ds_Benchmark")
	filed := map[string]bool{}
	for _, s := range []struct {
		id, cwd string
		ws      int64
	}{
		{"uc", `C:\Code\Ds_Benchmark\UC`, uc},
		{"hy4", `C:\Code\Ds_Benchmark\hy4`, hy4},
		{"deep", `C:\Code\Ds_Benchmark\hy4\x`, deep},
		{"auto", `C:\Code\Ds_Benchmark\kept`, kept},
		{"moved", `C:\Code\Ds_Benchmark\kept`, 0},
		{"home", `C:\Users\o`, home},
		{"docs", `C:\Users\o\Documents\a`, docs},
		{"bench", `C:\Code\Ds_Benchmark`, bench},
		{"fresh", `C:\Code\Ds_Benchmark\UC\y`, 0},
	} {
		add(s.id, s.cwd, s.ws)
		filed[s.id] = true
	}
	x.filed["m"] = filed
	if err := st.Move("moved", kept); err != nil {
		t.Fatal(err)
	}
	x.classify()
	a, _ := st.Assignments()
	for _, id := range []string{"uc", "hy4", "deep", "auto", "bench", "fresh"} {
		if a[id].Workspace != bench || a[id].Manual {
			t.Errorf("%s: %+v, want in Ds_Benchmark's group %d", id, a[id], bench)
		}
	}
	if a["moved"].Workspace != kept || !a["moved"].Manual {
		t.Errorf("moved: %+v, want left in %d", a["moved"], kept)
	}
	want := map[string]int64{"home": home, "docs": docs}
	for id, ws := range want {
		if a[id].Workspace != ws {
			t.Errorf("%s: %+v, want left in %d", id, a[id], ws)
		}
	}
	ws, _ := st.Workspaces()
	left := map[int64]bool{}
	for _, w := range ws {
		left[w.ID] = true
	}
	for _, id := range []int64{uc, hy4, deep} {
		if left[id] {
			t.Errorf("group %d left, want dropped", id)
		}
	}
	for _, id := range []int64{kept, home, docs, bench} {
		if !left[id] {
			t.Errorf("group %d dropped, want left", id)
		}
	}
}

// A group split at its folder keeps only the sessions right in it; those in its subfolders are
// grouped again by their folders, a subfolder's in its parent's, and the split group swallows them
// no more. One of this machine's you put in a group there stays where you put it.
func TestSplitAndFollow(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "iridium.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	x := New(Options{Store: st, Home: "/home/u", Logf: t.Logf})
	filed := map[string]bool{}
	add := func(id, cwd, machine string, agent api.Agent) {
		x.entries[id] = &entry{onDisk: onDisk{id: id, agent: agent}, glimpse: glimpse{cwd: cwd}, mtime: time.Now(), machine: machine}
		if _, err := st.Show(string(agent), id); err != nil {
			t.Fatal(err)
		}
		filed[id] = true
	}
	otc, _ := st.CreateWorkspace("otc", "m:C:/Code/otc")
	add("root", `C:\Code`, "m", api.AgentDsh)
	add("otc", `C:\Code\otc`, "m", api.AgentDsh)
	add("bench", `C:\Code\Ds_Benchmark`, "m", api.AgentDsh)
	add("hy4", `C:\Code\Ds_Benchmark\hy4`, "m", api.AgentDsh)
	add("coord", "/home/u", "", api.AgentClaude)
	x.filed["m"] = filed
	if err := st.Move("coord", otc); err != nil {
		t.Fatal(err)
	}
	x.classify()
	a, _ := st.Assignments()
	code := a["root"].Workspace
	for _, id := range []string{"otc", "bench", "hy4"} {
		if a[id].Workspace != code || a[id].Manual {
			t.Errorf("%s: %+v, want in Code's group %d", id, a[id], code)
		}
	}
	if a["coord"].Workspace != otc || !a["coord"].Manual {
		t.Errorf("coord: %+v, want left in %d", a["coord"], otc)
	}

	if err := x.SplitWorkspace(code); err != nil {
		t.Fatal(err)
	}
	x.classify()
	a, _ = st.Assignments()
	ws, _ := st.Workspaces()
	path := map[int64]string{}
	for _, w := range ws {
		path[w.ID] = w.Path
	}
	for id, want := range map[string]string{"root": "m:C:/Code", "otc": "m:C:/Code/otc", "bench": "m:C:/Code/Ds_Benchmark", "hy4": "m:C:/Code/Ds_Benchmark"} {
		if got := path[a[id].Workspace]; got != want {
			t.Errorf("%s in %q, want %q", id, got, want)
		}
	}
}
