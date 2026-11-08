package session

import (
	"errors"
	"testing"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/store"
)

// A new session has no name till its first message titles it (the page shows 新会话); its
// agent's name goes over the title, and a /clear does not pass it on.
func TestNewSessionName(t *testing.T) {
	t.Parallel()
	r := newRig(t, nil)
	a, _ := r.m.Create(bg, api.NewSession{Cwd: r.cwd}, "网页")
	if st, err := r.m.State(a); err != nil || st.Session.Name != "" || st.Session.Title != "" {
		t.Fatalf("new: name %q title %q: %v", st.Session.Name, st.Session.Title, err)
	}
	c, _ := r.m.Create(bg, api.NewSession{Cwd: r.cwd, Text: "hi"}, "网页")
	r.waitState(c, "the reply", func(s *api.SessionState) bool { return said(s, "ok: hi") && s.Status == api.StatusIdle })
	if st, err := r.m.State(c); err != nil || st.Session.Name != "" || st.Session.Title != "hi" {
		t.Fatalf("c, said hi: name %q title %q: %v", st.Session.Name, st.Session.Title, err)
	}
	if err := r.store.SetAgentName(c, "问候"); err != nil {
		t.Fatal(err)
	}
	if st, err := r.m.State(c); err != nil || st.Session.Name != "问候" {
		t.Fatalf("named by its agent: %q: %v", st.Session.Name, err)
	}
	n, err := r.m.Clear(bg, c, "网页")
	if err != nil {
		t.Fatal(err)
	}
	if over, _ := r.store.Overlays(); over[n].Name != "" {
		t.Errorf("cleared: %+v", over[n])
	}
}

// A name the user gave stays over the title and the agent's, and a /clear passes it on.
func TestGivenNameStays(t *testing.T) {
	t.Parallel()
	r := newRig(t, nil)
	c, _ := r.m.Create(bg, api.NewSession{Cwd: r.cwd, Text: "hi"}, "网页")
	if err := r.store.SetAgentName(c, "agent's"); err != nil {
		t.Fatal(err)
	}
	if err := r.store.SetName(c, "mine"); err != nil {
		t.Fatal(err)
	}
	if err := r.store.SetAgentName(c, "agent's again"); !errors.Is(err, store.ErrUserNamed) {
		t.Errorf("the agent over the user's: %v", err)
	}
	r.waitState(c, "the reply", func(s *api.SessionState) bool { return said(s, "ok: hi") && s.Status == api.StatusIdle })
	if st, err := r.m.State(c); err != nil || st.Session.Name != "mine" {
		t.Fatalf("named mine, said hi: %q: %v", st.Session.Name, err)
	}
	n, err := r.m.Clear(bg, c, "网页")
	if err != nil {
		t.Fatal(err)
	}
	if over, _ := r.store.Overlays(); over[n].Name != "mine" || over[n].AgentNamed {
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
