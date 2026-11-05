package discover

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestFolders(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	for _, d := range []string{"b", "A", "c/d", ".hidden", ".claude/projects", ".ssh"} {
		os.MkdirAll(filepath.Join(home, d), 0o700)
	}
	os.WriteFile(filepath.Join(home, "file.txt"), nil, 0o600)
	os.MkdirAll(filepath.Join(root, "outside"), 0o700)
	os.Symlink(filepath.Join(home, "c"), filepath.Join(home, "link-c"))
	os.Symlink(filepath.Join(root, "outside"), filepath.Join(home, "link-out"))
	f := DefaultFolders(home)

	l, err := f.List("")
	if err != nil {
		t.Fatal(err)
	}
	if l.Path != home || l.Parent != "" || !slices.Equal(l.Folders, []string{"A", "b", "c", "link-c"}) {
		t.Errorf("home: %+v", l)
	}
	l, err = f.List("~/c")
	if err != nil || l.Path != filepath.Join(home, "c") || l.Parent != home || !slices.Equal(l.Folders, []string{"d"}) {
		t.Errorf("~/c: %+v %v", l, err)
	}
	// Hidden ones may be typed, but not the agents' own.
	if _, err := f.List(filepath.Join(home, ".hidden")); err != nil {
		t.Errorf("a hidden folder typed: %v", err)
	}
	for _, p := range []string{filepath.Join(home, ".claude", "projects"), filepath.Join(home, ".ssh"), filepath.Join(root, "outside"), filepath.Join(home, "link-out"), filepath.Join(home, "c", "..", ".."), "relative"} {
		if _, err := f.List(p); !errors.Is(err, ErrOutside) {
			t.Errorf("%s: %v", p, err)
		}
	}
	if _, err := f.List(filepath.Join(home, "file.txt")); err == nil {
		t.Errorf("a file listed")
	}
	if _, err := f.List(filepath.Join(home, "gone")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("gone: %v", err)
	}

	p, err := f.Make(home, "new one")
	if err != nil || p != filepath.Join(home, "new one") {
		t.Fatalf("make: %q %v", p, err)
	}
	if st, err := os.Stat(p); err != nil || !st.IsDir() {
		t.Errorf("made: %v", err)
	}
	if _, err := f.Make(home, "new one"); !errors.Is(err, os.ErrExist) {
		t.Errorf("made twice: %v", err)
	}
	for _, name := range []string{"", ".", "..", ".dot", "a/b", "  "} {
		if _, err := f.Make(home, name); err == nil {
			t.Errorf("made %q", name)
		}
	}
	if _, err := f.Make(filepath.Join(root, "outside"), "x"); !errors.Is(err, ErrOutside) {
		t.Errorf("made outside: %v", err)
	}
}
