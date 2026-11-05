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
	"github.com/kumoSleeping/iridium/internal/discover"
	"github.com/kumoSleeping/iridium/internal/store"
)

func TestWorkspacesAndFolders(t *testing.T) {
	s, cfg, work := setup(t)
	st, err := store.Open(filepath.Join(cfg.StateDir, "iridium.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	root := filepath.Dir(work)
	empty := t.TempDir()
	d := discover.New(discover.Options{ClaudeDir: cfg.ClaudeHome, CodexHome: empty, PiSessions: empty, DshHome: empty, Store: st, Hub: s.hub, Procs: discover.ProcFS{Root: empty}, Logf: t.Logf, Settle: 10 * time.Millisecond})
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

	// Workspaces: make, rename, list, delete.
	var ws api.Workspace
	if code := call(t, c, "POST", base+"/api/workspaces", api.NewWorkspace{Path: made.Path}, &ws); code != 200 || ws.Name != "proj" || ws.Path != made.Path {
		t.Fatalf("new workspace: %d %+v", code, ws)
	}
	if code := call(t, c, "POST", base+"/api/workspaces", api.NewWorkspace{Path: made.Path}, nil); code != http.StatusConflict {
		t.Errorf("a second of the folder: %d", code)
	}
	if code := call(t, c, "POST", base+"/api/workspaces", api.NewWorkspace{Path: filepath.Join(work, "gone")}, nil); code != http.StatusNotFound {
		t.Errorf("a folder that is not there: %d", code)
	}
	if code := call(t, c, "PATCH", base+"/api/workspaces/"+strconv.FormatInt(ws.ID, 10), api.WorkspacePatch{Name: "Project"}, nil); code != 200 {
		t.Errorf("rename: %d", code)
	}
	var list []api.Workspace
	call(t, c, "GET", base+"/api/workspaces", nil, &list)
	if len(list) != 1 || list[0].Name != "Project" {
		t.Errorf("workspaces: %+v", list)
	}

	// A session moved into it, pinned and named; then the workspace deleted.
	var created api.NewSessionReply
	if code := call(t, c, "POST", base+"/api/sessions", api.NewSession{Agent: api.AgentClaude, Cwd: work}, &created); code != 200 {
		t.Fatalf("new session: %d", code)
	}
	yes, name := true, "named"
	if code := call(t, c, "PATCH", base+"/api/sessions/"+created.ID, api.SessionPatch{Workspace: &ws.ID, Pinned: &yes, Name: &name}, nil); code != 200 {
		t.Fatalf("patch: %d", code)
	}
	var sessions []api.Session
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		call(t, c, "GET", base+"/api/sessions", nil, &sessions)
		if len(sessions) == 1 && sessions[0].Workspace == ws.ID && sessions[0].Pinned && sessions[0].Name == "named" {
			break
		}
	}
	if len(sessions) != 1 || sessions[0].Workspace != ws.ID || !sessions[0].Pinned || sessions[0].Name != "named" {
		t.Fatalf("sessions: %+v", sessions)
	}
	if code := call(t, c, "PATCH", base+"/api/sessions/00000000-0000-4000-8000-000000000000", api.SessionPatch{Pinned: &yes}, nil); code != http.StatusNotFound {
		t.Errorf("patch of no session: %d", code)
	}
	if code := call(t, c, "DELETE", base+"/api/workspaces/"+strconv.FormatInt(ws.ID, 10), nil, nil); code != 200 {
		t.Errorf("delete: %d", code)
	}
	if code := call(t, c, "DELETE", base+"/api/workspaces/"+strconv.FormatInt(ws.ID, 10), nil, nil); code != http.StatusNotFound {
		t.Errorf("delete twice: %d", code)
	}
	sessions = nil
	call(t, c, "GET", base+"/api/sessions", nil, &sessions)
	if len(sessions) != 1 || sessions[0].Workspace != 0 {
		t.Errorf("after delete: %+v", sessions)
	}
}
