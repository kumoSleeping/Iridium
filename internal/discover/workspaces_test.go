package discover

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/store"
)

// A session at another machine that discovery put in a group of another machine's (its executor
// paired again: a new terminal ID) goes to the group of its folder at its machine now; one you
// moved there stays.
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
	add("kept", `C:\Code\Starmod`)
	if _, err := st.AssignAuto("kept", coord); err != nil {
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
	if a["kept"].Workspace != coord {
		t.Errorf("kept: %+v, want left in %d, the group of a machine still there", a["kept"], coord)
	}
	if a["moved"].Workspace != old || !a["moved"].Manual {
		t.Errorf("moved: %+v, want left in %d", a["moved"], old)
	}
	// A drive's root there holds only its own, as / here does.
	if got := path[a["deep"].Workspace]; got != "new:C:/x" {
		t.Errorf("deep: in %q, want new:C:/x", got)
	}
}

// A dsh session in none of DSH Desktop's workspaces (its 未分组) stays in none; one put in a group
// goes back once quiet; one filed under a workspace there is grouped by its folder.
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
	add("busy", `C:\Users\o\Documents\DSH\2026-11-02\新聊天`, time.Now())
	add("new", `C:\Users\o\Documents\DSH\2026-11-03\新聊天`, quiet)
	add("filed", `C:\Code\Starmod`, quiet)
	for _, id := range []string{"old", "busy"} {
		if _, err := st.AssignAuto(id, chats); err != nil {
			t.Fatal(err)
		}
	}
	x.filed["m"] = map[string]bool{"filed": true}
	x.classify()
	a, _ := st.Assignments()
	if got := a["old"]; got.Workspace != 0 || got.Manual {
		t.Errorf("old: %+v, want back in none", got)
	}
	if a["busy"].Workspace != chats {
		t.Errorf("busy: %+v, want left in %d until quiet", a["busy"], chats)
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

// A session its caller starts at another machine goes in the caller's group (a coordinator and
// the sessions it starts stay together); at the same machine, only a group holding it does.
func TestGroupForAcrossMachines(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "iridium.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	x := New(Options{Store: st, Home: "/home/u", Logf: t.Logf})
	remoteGroup, _ := st.CreateWorkspace("Starmod", "m:C:/Code/Starmod")
	localGroup, _ := st.CreateWorkspace("ir", "/home/u/ir")
	if got := x.GroupFor("/home/u", remoteGroup); got != remoteGroup {
		t.Errorf("here, from a session at m's group: %d, want %d", got, remoteGroup)
	}
	if got := x.GroupFor("m:D:/x", localGroup); got != localGroup {
		t.Errorf("at m, from a session here: %d, want %d", got, localGroup)
	}
	if got := x.GroupFor("/srv/other", localGroup); got == localGroup || got == 0 {
		t.Errorf("here, elsewhere than its caller's folder: %d", got)
	}
}
