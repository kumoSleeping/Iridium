package store

import (
	"path/filepath"
	"slices"
	"testing"
)

func TestOrder(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var ids []int64
	for _, p := range []string{"/a", "/b", "/c"} {
		id, err := s.CreateWorkspace(p, p)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	order := func() []int64 {
		ws, err := s.Workspaces()
		if err != nil {
			t.Fatal(err)
		}
		var out []int64
		for _, w := range ws {
			out = append(out, w.ID)
		}
		return out
	}
	// c dragged above a: b, left out, after them; one made after, last.
	if err := s.SetWorkspaceOrder([]int64{ids[2], ids[0]}); err != nil {
		t.Fatal(err)
	}
	d, err := s.CreateWorkspace("/d", "/d")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := order(), []int64{ids[2], ids[0], ids[1], d}; !slices.Equal(got, want) {
		t.Errorf("order %v, want %v", got, want)
	}

	if err := s.SetOrder("x", 1234.5); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPinned("x", true); err != nil {
		t.Fatal(err)
	}
	o, err := s.Overlays()
	if err != nil {
		t.Fatal(err)
	}
	if o["x"].Order != 1234.5 || !o["x"].Pinned {
		t.Errorf("overlay %+v", o["x"])
	}
	if err := s.SetOrder("x", 0); err != nil {
		t.Fatal(err)
	}
	if o, _ := s.Overlays(); o["x"].Order != 0 {
		t.Errorf("order not taken off: %+v", o["x"])
	}

	for p, at := range map[string]float64{"/a/x": 5, "m:/b/y": 7, "/a/z": 9} {
		if err := s.SetFolderOrder(p, at); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetFolderOrder("/a/z", 0); err != nil {
		t.Fatal(err)
	}
	if f, err := s.FolderOrder(); err != nil || len(f) != 2 || f["/a/x"] != 5 || f["m:/b/y"] != 7 {
		t.Errorf("folder order %v %v", f, err)
	}
	for _, p := range []string{"/a/x", "m:/b/y", "/a/x"} {
		if err := s.SetFolderPinned(p, true); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetFolderPinned("/a/x", false); err != nil {
		t.Fatal(err)
	}
	if p, err := s.FolderPins(); err != nil || !slices.Equal(p, []string{"m:/b/y"}) {
		t.Errorf("folder pins %v %v", p, err)
	}
}
