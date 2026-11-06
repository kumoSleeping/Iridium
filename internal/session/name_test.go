package session

import (
	"path/filepath"
	"testing"

	"github.com/kumoSleeping/iridium/internal/api"
)

// A new session is named after its folder, numbered past the listed sessions' names, till its
// first message titles it; an archived one's name is free again.
func TestNewSessionName(t *testing.T) {
	t.Parallel()
	r := newRig(t, nil)
	base := filepath.Base(r.cwd)
	name := func(id string) string {
		over, _ := r.store.Overlays()
		return over[id].Name
	}
	a, _ := r.m.Create(bg, api.NewSession{Cwd: r.cwd}, "网页")
	b, _ := r.m.Create(bg, api.NewSession{Cwd: r.cwd}, "网页")
	c, _ := r.m.Create(bg, api.NewSession{Cwd: r.cwd, Text: "hi"}, "网页")
	if name(a) != base || name(b) != base+" 2" || name(c) != base+" 3" {
		t.Fatalf("names %q %q %q", name(a), name(b), name(c))
	}
	if err := r.m.SetArchived(bg, b, true); err != nil {
		t.Fatal(err)
	}
	if d, _ := r.m.Create(bg, api.NewSession{Cwd: r.cwd}, "网页"); name(d) != base+" 2" {
		t.Errorf("after archiving %q: %q", base+" 2", name(d))
	}
	r.waitState(c, "the reply", func(s *api.SessionState) bool { return said(s, "ok: hi") && s.Status == api.StatusIdle })
	// c's first message is its name now: its placeholder is free again, and a /clear does not
	// pass a placeholder on.
	if st, err := r.m.State(c); err != nil || st.Session.Name != "" || st.Session.Title != "hi" {
		t.Fatalf("c, said hi: name %q title %q: %v", st.Session.Name, st.Session.Title, err)
	}
	if e, _ := r.m.Create(bg, api.NewSession{Cwd: r.cwd}, "网页"); name(e) != base+" 3" {
		t.Errorf("past a titled placeholder: %q", name(e))
	}
	if n, err := r.m.Clear(bg, c, "网页"); err != nil || name(n) != base+" 4" {
		t.Errorf("cleared to %q named %q: %v", n, name(n), err)
	}
}

// A name the user gave stays over the title, and a /clear passes it on.
func TestGivenNameStays(t *testing.T) {
	t.Parallel()
	r := newRig(t, nil)
	c, _ := r.m.Create(bg, api.NewSession{Cwd: r.cwd, Text: "hi"}, "网页")
	if err := r.store.SetName(c, "mine"); err != nil {
		t.Fatal(err)
	}
	r.waitState(c, "the reply", func(s *api.SessionState) bool { return said(s, "ok: hi") && s.Status == api.StatusIdle })
	if st, err := r.m.State(c); err != nil || st.Session.Name != "mine" {
		t.Fatalf("named mine, said hi: %q: %v", st.Session.Name, err)
	}
	n, err := r.m.Clear(bg, c, "网页")
	if err != nil {
		t.Fatal(err)
	}
	if over, _ := r.store.Overlays(); over[n].Name != "mine" || over[n].AutoName {
		t.Errorf("cleared: %+v", over[n])
	}
}

// A session opened from inside another names it its launcher, and keeps it past a /clear.
func TestLauncher(t *testing.T) {
	t.Parallel()
	r := newRig(t, nil)
	a, _ := r.m.Create(bg, api.NewSession{Cwd: r.cwd}, "网页")
	b, _ := r.m.Create(bg, api.NewSession{Cwd: r.cwd, Caller: a, Text: "hi"}, "网页")
	if st, err := r.m.State(b); err != nil || st.Session.Launcher != a {
		t.Fatalf("launcher %q, want %q: %v", st.Session.Launcher, a, err)
	}
	r.waitState(b, "the reply", func(s *api.SessionState) bool { return said(s, "ok: hi") && s.Status == api.StatusIdle })
	n, err := r.m.Clear(bg, b, "网页")
	if err != nil {
		t.Fatal(err)
	}
	if st, err := r.m.State(n); err != nil || st.Session.Launcher != a {
		t.Errorf("cleared: launcher %q: %v", st.Session.Launcher, err)
	}
}
