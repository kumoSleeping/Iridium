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
