package session

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/config"
	"github.com/kumoSleeping/iridium/internal/discover"
	"github.com/kumoSleeping/iridium/internal/store"
)

// groupRig is a rig whose sessions are grouped as discovery groups them (GroupFor), never scanned.
func groupRig(t *testing.T) *rig {
	return newRig(t, func(c *config.Config, o *Options) {
		o.Discovery = discover.New(discover.Options{ClaudeDir: c.ClaudeHome, CodexHome: t.TempDir(), PiSessions: t.TempDir(), DshHome: t.TempDir(), Store: o.Store, Procs: &procs{}, UID: os.Getuid(), Logf: t.Logf})
	})
}

// row is session id as the list has it.
func (r *rig) row(id string) api.Session {
	r.t.Helper()
	l, err := r.m.List(bg)
	if err != nil {
		r.t.Fatal(err)
	}
	for _, s := range l {
		if s.ID == id {
			return s
		}
	}
	r.t.Fatalf("%s not listed", id)
	return api.Session{}
}

// A session a person opened is own: one Iridium opened through a create (an opener noted); one an
// agent started (ir new, its launcher noted) only once taken out from under it, by its own
// folder's group put by hand. An agent's ir new --workspace does not take it out.
func TestOwn(t *testing.T) {
	t.Parallel()
	r := groupRig(t)
	opened, _ := r.m.Create(bg, api.NewSession{Cwd: r.cwd}, "网页")
	taken, _ := r.m.Create(bg, api.NewSession{Cwd: r.cwd}, "网页") // as a take-up: no opener
	if err := r.store.SetOpener(opened, "local"); err != nil {
		t.Fatal(err)
	}
	if !r.row(opened).Own || r.row(taken).Own {
		t.Errorf("own: opened %v, no opener %v", r.row(opened).Own, r.row(taken).Own)
	}
	sub := filepath.Join(r.cwd, "sub")
	os.MkdirAll(sub, 0o700)
	ws, err := r.store.CreateWorkspace("elsewhere", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	child, _ := r.m.Create(bg, api.NewSession{Cwd: sub, Caller: opened}, "ir:"+opened)
	placed, _ := r.m.Create(bg, api.NewSession{Cwd: sub, Caller: opened, Workspace: ws}, "ir:"+opened)
	for _, id := range []string{child, placed} {
		r.store.SetOpener(id, "local") // the HTTP create notes one for an agent's ir new too
		if s := r.row(id); s.Own || s.Launcher != opened {
			t.Errorf("%s: own %v, launcher %q", id, s.Own, s.Launcher)
		}
	}
	if s := r.row(placed); s.Workspace != ws {
		t.Errorf("--workspace: in %d, want %d", s.Workspace, ws)
	}
	if err := r.m.Detach(bg, placed); err != nil {
		t.Fatal(err)
	}
	s := r.row(placed)
	w, _ := r.store.Workspace(s.Workspace)
	over, _ := r.store.Overlays()
	if !s.Own || w.Path != r.cwd || !over[placed].Manual {
		t.Errorf("detached: own %v, in %q (manual %v), want %q", s.Own, w.Path, over[placed].Manual, r.cwd)
	}
	if r.row(child).Own {
		t.Error("its sibling taken out too")
	}
}

// splitAll is discovery splitting a group as it does the sessions it found: those of out (in its
// subfolders) given back to it, put there by hand no more.
type splitAll struct {
	*discover.Index
	st  *store.Store
	out []string
}

func (d *splitAll) SplitWorkspace(id int64) error { return d.st.SplitWorkspace(id, d.out) }

// A split of the group one taken out from under its launcher is in gives back to discovery what
// is in its subfolders, put there by hand no more; that one goes in its folder's group by hand
// again, still taken out.
func TestSplitKeepsTakenOut(t *testing.T) {
	t.Parallel()
	var d *splitAll
	r := newRig(t, func(c *config.Config, o *Options) {
		d = &splitAll{Index: discover.New(discover.Options{ClaudeDir: c.ClaudeHome, CodexHome: t.TempDir(), PiSessions: t.TempDir(), DshHome: t.TempDir(), Store: o.Store, Procs: &procs{}, UID: os.Getuid(), Logf: t.Logf}), st: o.Store}
		o.Discovery = d
	})
	top, _ := r.m.Create(bg, api.NewSession{Cwd: r.cwd}, "网页")
	sub := filepath.Join(r.cwd, "bench")
	os.MkdirAll(sub, 0o700)
	out, _ := r.m.Create(bg, api.NewSession{Cwd: sub, Caller: top}, "ir:"+top)
	hung, _ := r.m.Create(bg, api.NewSession{Cwd: sub, Caller: top}, "ir:"+top)
	if err := r.m.Detach(bg, out); err != nil {
		t.Fatal(err)
	}
	ws := r.row(out).Workspace
	if w, _ := r.store.Workspace(ws); w.Path != r.cwd || !r.row(out).Own {
		t.Fatalf("detached: in %q, own %v", w.Path, r.row(out).Own)
	}
	d.out = []string{out, hung}
	if err := r.m.SplitWorkspace(bg, ws); err != nil {
		t.Fatal(err)
	}
	s := r.row(out)
	w, _ := r.store.Workspace(s.Workspace)
	if over, _ := r.store.Overlays(); !s.Own || w.Path != sub || !over[out].Manual {
		t.Errorf("split: own %v, in %q (manual %v), want %q", s.Own, w.Path, over[out].Manual, sub)
	}
	if r.row(hung).Own {
		t.Error("the one hung taken out by the split")
	}
}

// Archiving a session archives what hangs under it, all the way down, and brings it back with it;
// one taken out from under it stays as it is.
func TestArchiveTree(t *testing.T) {
	t.Parallel()
	r := groupRig(t)
	top, _ := r.m.Create(bg, api.NewSession{Cwd: r.cwd}, "网页")
	child, _ := r.m.Create(bg, api.NewSession{Cwd: r.cwd, Caller: top}, "ir:"+top)
	grand, _ := r.m.Create(bg, api.NewSession{Cwd: r.cwd, Caller: child}, "ir:"+child)
	out, _ := r.m.Create(bg, api.NewSession{Cwd: r.cwd, Caller: top}, "ir:"+top)
	if err := r.m.Detach(bg, out); err != nil {
		t.Fatal(err)
	}
	if err := r.m.ArchiveTree(bg, top, true); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{top, child, grand} {
		r.waitState(id, "archived, stopped", func(s *api.SessionState) bool { return s.Archived && !s.Live })
	}
	if r.row(out).Archived {
		t.Error("the one taken out archived too")
	}
	if err := r.m.ArchiveTree(bg, top, false); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{top, child, grand} {
		if r.row(id).Archived {
			t.Errorf("%s still archived", id)
		}
	}
	// The child alone: its own goes with it, its launcher stays.
	if err := r.m.ArchiveTree(bg, child, true); err != nil {
		t.Fatal(err)
	}
	if !r.row(grand).Archived || r.row(top).Archived {
		t.Errorf("the child archived: grand %v, top %v", r.row(grand).Archived, r.row(top).Archived)
	}
}

// /clear of a session hung under its launcher goes on in one hung under it too (not put by hand);
// of one taken out from under it, in one taken out. What the cleared one started hangs under the
// new one.
func TestClearKeepsLauncher(t *testing.T) {
	t.Parallel()
	r := groupRig(t)
	top, _ := r.m.Create(bg, api.NewSession{Cwd: r.cwd}, "网页")
	hung, _ := r.m.Create(bg, api.NewSession{Cwd: r.cwd, Caller: top, Text: "hi"}, "ir:"+top)
	out, _ := r.m.Create(bg, api.NewSession{Cwd: r.cwd, Caller: top, Text: "hi"}, "ir:"+top)
	under, _ := r.m.Create(bg, api.NewSession{Cwd: r.cwd, Caller: hung}, "ir:"+hung)
	if err := r.m.Detach(bg, out); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{hung, out} {
		r.waitState(id, "the reply", func(s *api.SessionState) bool { return said(s, "ok: hi") && s.Status == api.StatusIdle })
	}
	nh, err := r.m.Clear(bg, hung, "网页")
	if err != nil || nh == hung {
		t.Fatalf("cleared to %q: %v", nh, err)
	}
	if s := r.row(nh); s.Launcher != top || s.Own {
		t.Errorf("the hung one cleared: launcher %q own %v", s.Launcher, s.Own)
	}
	if s := r.row(under); s.Launcher != nh {
		t.Errorf("what it started: launcher %q, want %q", s.Launcher, nh)
	}
	if h, err := r.m.get(under); err != nil {
		t.Fatal(err)
	} else if b, err := os.ReadFile(filepath.Join(h.dir, fileMeta)); err != nil || !strings.Contains(string(b), nh) {
		t.Errorf("its launcher not saved: %s %v", b, err)
	}
	no, err := r.m.Clear(bg, out, "网页")
	if err != nil || no == out {
		t.Fatalf("cleared to %q: %v", no, err)
	}
	if s := r.row(no); s.Launcher != top || !s.Own {
		t.Errorf("the one taken out cleared: launcher %q own %v", s.Launcher, s.Own)
	}
}

// What the page lists, so what is pushed: this machine's; an own session and the group at
// another machine it is in; one hung under its launcher; one whose launcher is gone or archived;
// a subagent as its parent. Not one in a group at another machine no one opened.
func TestListed(t *testing.T) {
	l := []api.Session{
		{ID: "here", Workspace: 1},
		{ID: "desk", Machine: "m", Workspace: 2},                // DSH Desktop's, in a group no one opened
		{ID: "agent", Machine: "m", Workspace: 2, Hosted: true}, // an agent's ir send took it up
		{ID: "sub", Machine: "m", Workspace: 2, Subagent: true, Parent: "agent", Launcher: "agent"},
		{ID: "mine", Machine: "m", Workspace: 3, Own: true, Hosted: true},
		{ID: "beside", Machine: "m", Workspace: 3},
		{ID: "hung", Machine: "m", Workspace: 4, Launcher: "here"},
		{ID: "orphan", Machine: "m", Workspace: 5, Launcher: "gone"},
		{ID: "near", Machine: "m", Workspace: 5},
		{ID: "old", Machine: "m", Workspace: 6, Own: true, Archived: true},
		{ID: "by", Machine: "m", Workspace: 6},
	}
	want := map[string]bool{"here": true, "desk": false, "agent": false, "sub": false, "mine": true, "beside": true,
		"hung": true, "orphan": true, "near": true, "old": true, "by": false, "unknown": true}
	for id, w := range want {
		if got := listed(l, nil, id); got != w {
			t.Errorf("%s: listed %v, want %v", id, got, w)
		}
	}
}

// The page's fixtures (web/test/logic.test.ts reads the same): what stands where, so what is
// listed, is the page's.
func TestStanding(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "standing.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name       string
		Workspaces []api.Workspace
		Sessions   []api.Session
		Rows       []string
		Hung       map[string][]string
		Groups     []int64
	}
	if err := json.Unmarshal(b, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		roots := map[int64]string{}
		for _, w := range c.Workspaces {
			roots[w.ID] = w.Path
		}
		st := standOf(c.Sessions, roots)
		var rows []string
		for _, s := range c.Sessions {
			if st.rows[s.ID] {
				rows = append(rows, s.ID)
			}
		}
		groups := slices.Sorted(maps.Keys(st.groups))
		if !slices.Equal(rows, c.Rows) || !maps.EqualFunc(st.hung, c.Hung, slices.Equal) || !slices.Equal(groups, c.Groups) {
			t.Errorf("%s: rows %v hung %v groups %v, want %v %v %v", c.Name, rows, st.hung, groups, c.Rows, c.Hung, c.Groups)
		}
		hung := map[string]bool{}
		for _, h := range st.hung {
			for _, id := range h {
				hung[id] = true
			}
		}
		for _, s := range c.Sessions {
			if got, want := listed(c.Sessions, roots, s.ID), s.Archived && (s.Machine == "" || s.Own) || st.rows[s.ID] || hung[s.ID] || s.Subagent && listed(c.Sessions, roots, s.Parent); got != want {
				t.Errorf("%s: %s listed %v, want %v", c.Name, s.ID, got, want)
			}
		}
	}
}
