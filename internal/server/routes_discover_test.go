package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/config"
	"github.com/kumoSleeping/iridium/internal/discover"
	"github.com/kumoSleeping/iridium/internal/session"
)

func TestWorkspacesAndFolders(t *testing.T) {
	// The session manager's discovery is this index: a session made goes in its group at once.
	var d *discover.Index
	s, _, work := setupWith(t, func(o *session.Options) {
		empty := t.TempDir()
		d = discover.New(discover.Options{ClaudeDir: o.Config.ClaudeHome, CodexHome: empty, PiSessions: empty, DshHome: empty, Store: o.Store, Hub: o.Hub, Procs: discover.ProcFS{Root: empty}, Logf: t.Logf, Settle: 10 * time.Millisecond})
		o.Discovery = d
	})
	root := filepath.Dir(work)
	d.SetOnChange(s.m.Refresh)
	if err := d.Start(); err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	s.Discover, s.Folders = d, discover.DefaultFolders(root)
	srv := httptest.NewServer(s.Handler(false))
	defer srv.Close()
	c, base := srv.Client(), srv.URL

	// The picker: folders only, sorted, no hidden ones; a new folder.
	os.MkdirAll(filepath.Join(work, "Zed"), 0o700)
	os.MkdirAll(filepath.Join(work, "alpha"), 0o700)
	os.MkdirAll(filepath.Join(work, ".git"), 0o700)
	os.WriteFile(filepath.Join(work, "README"), nil, 0o600)
	var fl api.Folders
	if code := call(t, c, "GET", base+"/api/folders?path="+work, nil, &fl); code != 200 || !slices.Equal(fl.Folders, []string{"alpha", "Zed"}) || fl.Parent != root {
		t.Fatalf("folders: %d %+v", code, fl)
	}
	var made api.FolderReply
	if code := call(t, c, "POST", base+"/api/folders", api.NewFolder{Path: work, Name: "proj"}, &made); code != 200 || made.Path != filepath.Join(work, "proj") {
		t.Fatalf("new folder: %d %+v", code, made)
	}
	if code := call(t, c, "POST", base+"/api/folders", api.NewFolder{Path: work, Name: "proj"}, nil); code != http.StatusConflict {
		t.Errorf("new folder twice: %d", code)
	}
	if code := call(t, c, "GET", base+"/api/folders?path=/", nil, nil); code != http.StatusForbidden {
		t.Errorf("outside the home: %d", code)
	}

	// Groups: none made by hand (the user, 2026-11-08); a session in a folder makes its group,
	// which is renamed and listed.
	if code := call(t, c, "POST", base+"/api/workspaces", map[string]string{"path": made.Path}, nil); code != http.StatusMethodNotAllowed {
		t.Errorf("a workspace made by hand: %d", code)
	}
	var created api.NewSessionReply
	if code := call(t, c, "POST", base+"/api/sessions", api.NewSession{Agent: api.AgentClaude, Cwd: made.Path, Text: "hi"}, &created); code != 200 {
		t.Fatalf("new session: %d", code)
	}
	var list []api.Workspace
	var sessions []api.Session
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		call(t, c, "GET", base+"/api/workspaces", nil, &list)
		call(t, c, "GET", base+"/api/sessions", nil, &sessions)
		if len(list) == 1 && len(sessions) == 1 && sessions[0].Workspace == list[0].ID {
			break
		}
	}
	if len(list) != 1 || list[0].Name != "proj" || list[0].Path != made.Path || len(sessions) != 1 || sessions[0].Workspace != list[0].ID {
		t.Fatalf("the session's group: %+v %+v", list, sessions)
	}
	if code := call(t, c, "PATCH", base+"/api/workspaces/"+strconv.FormatInt(list[0].ID, 10), api.WorkspacePatch{Name: "Project"}, nil); code != 200 {
		t.Errorf("rename: %d", code)
	}
	call(t, c, "GET", base+"/api/workspaces", nil, &list)
	if len(list) != 1 || list[0].Name != "Project" {
		t.Errorf("workspaces: %+v", list)
	}
	yes, name := true, "named"
	if code := call(t, c, "PATCH", base+"/api/sessions/"+created.ID, api.SessionPatch{Pinned: &yes, Name: &name}, nil); code != 200 {
		t.Fatalf("patch: %d", code)
	}
	if code := call(t, c, "PATCH", base+"/api/sessions/00000000-0000-4000-8000-000000000000", api.SessionPatch{Pinned: &yes}, nil); code != http.StatusNotFound {
		t.Errorf("patch of no session: %d", code)
	}
}

// A session on disk from before Iridium watched is the archive: listed archived. Restored, it
// is shown; archived again, it stays so till its transcript grows.
func TestArchiveFound(t *testing.T) {
	var d *discover.Index
	var cfg config.Config
	s, cfg, work := setupWith(t, func(o *session.Options) {
		empty := t.TempDir()
		d = discover.New(discover.Options{ClaudeDir: o.Config.ClaudeHome, CodexHome: empty, PiSessions: empty, DshHome: empty, Store: o.Store, Hub: o.Hub, Procs: discover.ProcFS{Root: empty}, Logf: t.Logf, Settle: 10 * time.Millisecond})
		o.Discovery = d
	})
	id := "77777777-7777-4777-8777-777777777777"
	dir := filepath.Join(cfg.ClaudeHome, "projects", "-old")
	os.MkdirAll(dir, 0o700)
	path := filepath.Join(dir, id+".jsonl")
	os.WriteFile(path, []byte(`{"type":"user","message":{"role":"user","content":"long ago"},"cwd":"`+work+`"}`+"\n"), 0o600)
	past := time.Now().Add(-time.Hour)
	os.Chtimes(path, past, past)
	d.SetOnChange(s.m.Refresh)
	if err := d.Start(); err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	s.Discover = d
	srv := httptest.NewServer(s.Handler(false))
	defer srv.Close()
	c, base := srv.Client(), srv.URL
	archived := func() bool {
		t.Helper()
		var l []api.Session
		call(t, c, "GET", base+"/api/sessions", nil, &l)
		for _, x := range l {
			if x.ID == id {
				return x.Archived
			}
		}
		t.Fatalf("%s not listed: %+v", id, l)
		return false
	}
	if !archived() {
		t.Fatal("an old session is shown")
	}
	no, yes := false, true
	if code := call(t, c, "PATCH", base+"/api/sessions/"+id, api.SessionPatch{Archived: &no}, nil); code != 200 || archived() {
		t.Fatalf("restore: %d", code)
	}
	if code := call(t, c, "PATCH", base+"/api/sessions/"+id, api.SessionPatch{Archived: &yes}, nil); code != 200 || !archived() {
		t.Fatalf("archive: %d", code)
	}
	d.Scan()
	if !archived() {
		t.Fatal("back with no growth")
	}
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"again"}]}}` + "\n")
	f.Close()
	d.Scan()
	if archived() {
		t.Fatal("it grew: shown again")
	}
}
