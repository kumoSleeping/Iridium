package store

import (
	"path/filepath"
	"testing"
)

func TestWorkspaces(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ws, err := s.CreateWorkspace("w", "/w")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateWorkspace("w2", "/w"); err != ErrWorkspacePath {
		t.Errorf("same folder: %v", err)
	}
	if err := s.Move("a", ws); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.AssignIfNew("a", 0); ok {
		t.Errorf("assigned again")
	}
	if ok, _ := s.AssignIfNew("b", 0); !ok {
		t.Errorf("b not assigned")
	}
	if n, _ := s.Adopt(ws, []string{"a", "b", "c"}); n != 2 {
		t.Errorf("adopted %d, want b and c", n)
	}
	if err := s.DeleteWorkspace(ws); err != nil {
		t.Fatal(err)
	}
	a, _ := s.Assignments()
	if len(a) != 3 || a["a"].Workspace != 0 || !a["a"].Manual || a["b"].Workspace != 0 || a["b"].Manual {
		t.Errorf("after delete: %+v", a)
	}
	o, _ := s.Overlays()
	if o["a"].Workspace != 0 {
		t.Errorf("overlay: %+v", o["a"])
	}
	if err := s.DeleteWorkspace(ws); err != ErrNoWorkspace {
		t.Errorf("delete twice: %v", err)
	}
	if err := s.RenameWorkspace(ws, "x"); err != ErrNoWorkspace {
		t.Errorf("rename gone: %v", err)
	}
}

func TestWorkspaceCreator(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a, _ := s.CreateWorkspace("a", "/a")
	b, _ := s.CreateWorkspace("b", "/b")
	if err := s.SetWorkspaceCreator(a, "panel-1"); err != nil {
		t.Fatal(err)
	}
	ws, err := s.Workspaces()
	if err != nil || len(ws) != 2 {
		t.Fatalf("%v %+v", err, ws)
	}
	got := map[int64]string{ws[0].ID: ws[0].Creator, ws[1].ID: ws[1].Creator}
	if got[a] != "panel-1" || got[b] != "" {
		t.Errorf("creators %+v", got)
	}
	if n, err := s.ClaimWorkspaces("panel-2"); err != nil || n != 1 {
		t.Fatalf("claimed %d: %v", n, err)
	}
	ws, _ = s.Workspaces()
	for _, w := range ws {
		if want := map[int64]string{a: "panel-1", b: "panel-2"}[w.ID]; w.Creator != want {
			t.Errorf("%d: creator %q, want %q", w.ID, w.Creator, want)
		}
	}
}
