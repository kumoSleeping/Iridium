package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/config"
	"github.com/kumoSleeping/iridium/internal/discover"
)

// A command never steers into a running turn: it waits for the turn's end.
func TestCommandWaitsForTurn(t *testing.T) {
	t.Parallel()
	r := newRig(t, nil)
	id, _ := r.m.Create(bg, api.NewSession{Cwd: r.cwd, Text: "one WAIT"}, "网页")
	r.waitState(id, "the turn", func(s *api.SessionState) bool { return s.Status == api.StatusRunning })
	if _, queued, err := r.m.SendWith(bg, id, " /compact ", "网页", true); err != nil || !queued {
		t.Fatalf("a command steered in: queued %v, %v", queued, err)
	}
	s, _ := r.m.State(id)
	if len(s.Queue) != 1 || s.Queue[0].Text != "/compact" {
		t.Fatalf("queue: %+v", s.Queue)
	}
	// Its own turn: the fake compacts, then is idle again.
	r.waitState(id, "the command's turn", func(s *api.SessionState) bool { return s.Status == api.StatusCompacting && len(s.Queue) == 0 })
	r.waitState(id, "the compaction's end", func(s *api.SessionState) bool { return s.Status == api.StatusIdle })
}

// /clear goes on in a new session in the same place with the same settings; the old one, idle,
// is archived. One that said nothing yet is clear already.
func TestClear(t *testing.T) {
	t.Parallel()
	r := newRig(t, nil)
	ws, err := r.store.CreateWorkspace("w", r.cwd)
	if err != nil {
		t.Fatal(err)
	}
	fresh, _ := r.m.Create(bg, api.NewSession{Cwd: r.cwd, Workspace: ws}, "网页")
	if id, err := r.m.Clear(bg, fresh, "网页"); err != nil || id != fresh {
		t.Fatalf("a fresh session cleared to %q: %v", id, err)
	}

	old, _ := r.m.Create(bg, api.NewSession{Cwd: r.cwd, Workspace: ws, Text: "hi", Mode: "plan"}, "网页")
	r.waitState(old, "the reply", func(s *api.SessionState) bool { return said(s, "ok: hi") && s.Status == api.StatusIdle })
	if err := r.m.SetEffort(bg, old, "high"); err != nil {
		t.Fatal(err)
	}
	id, err := r.m.Clear(bg, old, "网页")
	if err != nil || id == "" || id == old {
		t.Fatalf("cleared to %q: %v", id, err)
	}
	s := r.waitState(id, "the new session", func(s *api.SessionState) bool { return s.Live && s.Status == api.StatusIdle })
	if s.Cwd != r.cwd || s.Agent != api.AgentClaude || s.Effort != "high" || s.Mode != "plan" || s.Archived {
		t.Errorf("new session: cwd %q agent %q effort %q mode %q archived %v", s.Cwd, s.Agent, s.Effort, s.Mode, s.Archived)
	}
	if over, _ := r.store.Overlays(); over[id].Workspace != ws {
		t.Errorf("workspace %d, not %d", over[id].Workspace, ws)
	}
	if len(r.messages(id)) != 0 {
		t.Errorf("the new session has words: %+v", r.messages(id))
	}
	r.waitState(old, "the old one archived", func(s *api.SessionState) bool { return s.Archived && !s.Live })
	// Its words are still on record.
	if b := r.messages(old); len(b) != 2 {
		t.Errorf("old blocks: %+v", b)
	}
}

// A /clear that reached Claude (its own, not Iridium's) goes on in a conversation of a new id in
// the same process: Iridium stops it, archives the session, and takes the new conversation up in
// its place, with its settings and launcher; the page follows (Next). The old one, resumed, is as
// it was.
func TestClearedInside(t *testing.T) {
	t.Parallel()
	r, _ := clearRig(t, 20*time.Millisecond)
	ws, err := r.store.CreateWorkspace("w", r.cwd)
	if err != nil {
		t.Fatal(err)
	}
	top, _ := r.m.Create(bg, api.NewSession{Cwd: r.cwd, Workspace: ws}, "网页")
	old, _ := r.m.Create(bg, api.NewSession{Cwd: r.cwd, Workspace: ws, Text: "hi", Mode: "plan", Caller: top}, "网页")
	r.waitState(old, "the reply", func(s *api.SessionState) bool { return said(s, "ok: hi") && s.Status == api.StatusIdle })
	if err := r.m.SetEffort(bg, old, "high"); err != nil {
		t.Fatal(err)
	}
	// Past IsClear, as a /clear from a terminal or a source Iridium does not look at.
	if _, err := r.m.Send(bg, old, "/clear", "网页"); err != nil {
		t.Fatal(err)
	}
	s := r.waitState(old, "the old one archived, its page sent on", func(s *api.SessionState) bool { return s.Next != "" && s.Archived && !s.Live })
	nid := s.Next
	if nid == old {
		t.Fatalf("went on as itself")
	}
	n, err := r.m.State(nid)
	if err != nil || !n.Hosted || n.Archived || n.Cwd != r.cwd || n.Effort != "high" || n.Pick != "" {
		t.Fatalf("the new session: %+v %v", n, err)
	}
	// Hung under its launcher, as the old one.
	if n.Launcher != top || n.Own {
		t.Errorf("launcher %q own %v", n.Launcher, n.Own)
	}
	if over, _ := r.store.Overlays(); over[nid].Workspace != ws {
		t.Errorf("workspace %d, not %d", over[nid].Workspace, ws)
	}
	// It goes on there, resumed by the id Claude gave it, in plan mode as before.
	if _, err := r.m.Send(bg, nid, "next", "网页"); err != nil {
		t.Fatal(err)
	}
	n = r.waitState(nid, "its reply", func(s *api.SessionState) bool { return said(s, "ok: next") && s.Status == api.StatusIdle })
	if n.Mode != "plan" {
		t.Errorf("mode %q", n.Mode)
	}
	if b := r.messages(old); len(b) != 2 {
		t.Errorf("old blocks: %+v", b)
	}
	// The old one, resumed, is its own again: no more Next.
	if _, err := r.m.Send(bg, old, "back", "网页"); err != nil {
		t.Fatal(err)
	}
	s = r.waitState(old, "its reply", func(s *api.SessionState) bool { return said(s, "ok: back") && s.Status == api.StatusIdle })
	if s.Next != "" {
		t.Errorf("still sent on to %q", s.Next)
	}
	if b := r.messages(old); len(b) != 4 {
		t.Errorf("old blocks after resuming: %+v", b)
	}
}

// clearRig is a rig with discovery, scanning often, and clearedInside looking every poll.
func clearRig(t *testing.T, poll time.Duration) (*rig, *discover.Index) {
	var d *discover.Index
	r := newRig(t, func(c *config.Config, o *Options) {
		d = discover.New(discover.Options{ClaudeDir: c.ClaudeHome, CodexHome: t.TempDir(), PiSessions: t.TempDir(), DshHome: t.TempDir(), Store: o.Store, Procs: &procs{}, UID: os.Getuid(), Logf: t.Logf})
		o.Discovery = d
		o.ClearPoll = poll
	})
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-time.After(20 * time.Millisecond):
				d.Scan()
			}
		}
	}()
	return r, d
}

// A daemon that comes back to a process that cleared itself before it went (its output replayed)
// sets the split right as one that saw it would.
func TestClearedInsideBeforeRestart(t *testing.T) {
	t.Parallel()
	r, d := clearRig(t, time.Hour) // this daemon never gets to it
	old, _ := r.m.Create(bg, api.NewSession{Cwd: r.cwd, Text: "hi"}, "网页")
	r.waitState(old, "the reply", func(s *api.SessionState) bool { return said(s, "ok: hi") && s.Status == api.StatusIdle })
	if _, err := r.m.Send(bg, old, "/clear", "网页"); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(r.cfg.ClaudeHome, "projects", strings.ReplaceAll(r.cwd, "/", "-"))
	waitFor(t, "the new conversation", func() bool { m, _ := filepath.Glob(filepath.Join(dir, "*.jsonl")); return len(m) == 2 })
	r.waitState(old, "the /clear's turn over", func(s *api.SessionState) bool { return s.Status == api.StatusIdle && s.Live })
	r.m.Close()
	o := r.options()
	o.Discovery, o.ClearPoll = d, 20*time.Millisecond
	r.start(o)
	s := r.waitState(old, "the old one archived, its page sent on", func(s *api.SessionState) bool { return s.Next != "" && s.Archived && !s.Live })
	if _, err := r.m.Send(bg, s.Next, "next", "网页"); err != nil {
		t.Fatal(err)
	}
	r.waitState(s.Next, "its reply", func(s *api.SessionState) bool { return said(s, "ok: next") && s.Status == api.StatusIdle })
}

// Claude clears on /clear, /reset and /new, a name after them or not: Iridium runs each.
func TestIsClear(t *testing.T) {
	t.Parallel()
	for text, want := range map[string]bool{
		"/clear": true, " /CLEAR ": true, "/reset": true, "/new": true, "/clear my notes": true, "/new\nmore": true,
		"/clearer": false, "/compact": false, "clear": false, "": false, "please /clear": false,
	} {
		if got := IsClear(text); got != want {
			t.Errorf("IsClear(%q) = %v", text, got)
		}
	}
}
