package pathsafe

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestGuard(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	work := filepath.Join(root, "work")
	secret := filepath.Join(root, "secret")
	state := filepath.Join(root, "state")
	other := filepath.Join(root, "other")
	for _, d := range []string{work, secret, filepath.Join(state, "uploads"), other} {
		os.MkdirAll(d, 0o700)
	}
	os.WriteFile(filepath.Join(secret, "key"), []byte("k"), 0o600)
	os.WriteFile(filepath.Join(work, "a.txt"), []byte("a"), 0o600)
	os.WriteFile(filepath.Join(other, "b.txt"), []byte("b"), 0o600)
	// Links out of the allowed root, into a denied directory, and a dangling one into it.
	os.Symlink(filepath.Join(secret, "key"), filepath.Join(work, "key-link"))
	os.Symlink(secret, filepath.Join(work, "secret-dir"))
	os.Symlink(filepath.Join(secret, "new"), filepath.Join(work, "dangling"))
	os.Symlink(other, filepath.Join(work, "other-dir"))
	// A denied directory reached through a link to it: the deny list resolves too.
	os.Symlink(secret, filepath.Join(root, "secret-alias"))

	g := New([]string{work}, []string{secret, state}, []string{filepath.Join(state, "uploads")})
	for _, c := range []struct {
		path string
		want error
	}{
		{filepath.Join(work, "a.txt"), nil},
		{filepath.Join(work, "not-yet", "c.txt"), nil}, // need not exist
		{filepath.Join(work, "key-link"), ErrDenied},
		{filepath.Join(work, "secret-dir", "key"), ErrDenied},
		{filepath.Join(work, "dangling"), ErrDenied},
		{filepath.Join(work, "other-dir", "b.txt"), ErrOutside},
		{filepath.Join(work, "..", "other", "b.txt"), ErrOutside},
		{filepath.Join(secret, "key"), ErrDenied},
		{filepath.Join(state, "iridium.db"), ErrDenied},
		{filepath.Join(state, "uploads", "x"), ErrOutside}, // excepted from the deny, still outside work
		{"relative/path", ErrRelative},
	} {
		_, err := g.Check(c.path)
		if !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", c.path, err, c.want)
		}
	}

	// Anywhere but the denied directories.
	any := New(nil, []string{filepath.Join(root, "secret-alias"), state}, []string{filepath.Join(state, "uploads")})
	if r, err := any.Check(filepath.Join(work, "other-dir", "b.txt")); err != nil || r != filepath.Join(other, "b.txt") {
		t.Errorf("anywhere: %q %v", r, err)
	}
	if _, err := any.Check(filepath.Join(secret, "key")); !errors.Is(err, ErrDenied) {
		t.Errorf("denied through the alias: %v", err)
	}
	if _, err := any.Check(filepath.Join(state, "uploads", "f")); err != nil {
		t.Errorf("exception: %v", err)
	}
}

func TestDenyList(t *testing.T) {
	home, _ := os.UserHomeDir()
	d := Deny("/s", "/c")
	g := New(nil, d, nil)
	for _, p := range []string{"/s/x", "/c/config.json", home + "/.ssh/id_ed25519", home + "/.claude/x", home + "/.codex", home + "/.pi/agent", home + "/.dsh/k", "/proc/self/environ"} {
		if _, err := g.Check(p); !errors.Is(err, ErrDenied) {
			t.Errorf("%s: %v", p, err)
		}
	}
	if _, err := g.Check(home + "/.sshx"); err != nil {
		t.Errorf("a sibling with the same prefix: %v", err)
	}
}
