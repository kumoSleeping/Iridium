package server

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/discover"
)

// A new workspace takes the hosted sessions of its folder that are in none and were never moved
// by hand, as it does the discovered ones: also those with no transcript yet, which discovery
// does not list.
func TestWorkspaceTakesHosted(t *testing.T) {
	s, cfg, work := setup(t)
	empty := t.TempDir()
	d := discover.New(discover.Options{ClaudeDir: cfg.ClaudeHome, CodexHome: empty, PiSessions: empty, DshHome: empty, Store: s.st, Hub: s.hub, Procs: discover.ProcFS{Root: empty}, Logf: t.Logf, Settle: 10 * time.Millisecond})
	if err := d.Start(); err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	s.Discover, s.Folders = d, discover.DefaultFolders(filepath.Dir(work))
	srv := httptest.NewServer(s.Handler(false))
	defer srv.Close()
	c, base := srv.Client(), srv.URL

	app := filepath.Join(work, "app")
	sub := filepath.Join(app, "sub")
	other := filepath.Join(work, "other")
	for _, p := range []string{sub, other} {
		os.MkdirAll(p, 0o700)
	}
	open := func(cwd string) string {
		var r api.NewSessionReply
		if code := call(t, c, "POST", base+"/api/sessions", api.NewSession{Agent: api.AgentClaude, Cwd: cwd}, &r); code != 200 {
			t.Fatalf("new session: %d", code)
		}
		return r.ID
	}
	inApp, below, moved, outside := open(app), open(sub), open(app), open(other)
	none := int64(0)
	if code := call(t, c, "PATCH", base+"/api/sessions/"+moved, api.SessionPatch{Workspace: &none}, nil); code != 200 {
		t.Fatalf("move: %d", code)
	}

	var ws api.Workspace
	if code := call(t, c, "POST", base+"/api/workspaces", api.NewWorkspace{Path: app}, &ws); code != 200 {
		t.Fatalf("new workspace: %d", code)
	}
	want := map[string]int64{inApp: ws.ID, below: ws.ID, moved: 0, outside: 0}
	var got map[string]int64
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		var list []api.Session
		call(t, c, "GET", base+"/api/sessions", nil, &list)
		got = map[string]int64{}
		for _, x := range list {
			got[x.ID] = x.Workspace
		}
		ok := len(got) == len(want)
		for id, w := range want {
			ok = ok && got[id] == w
		}
		if ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("workspaces: got %v, want %v", got, want)
		}
	}
}
