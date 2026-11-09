package chat

import (
	"os"
	"path/filepath"
	"testing"
)

// The agents' own directories are never given out, dsh's among them.
func TestDeniedAgentHomes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, d := range []string{".claude", ".codex", ".pi", ".dsh", "work"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(home, d, "f"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, d := range []string{".claude", ".codex", ".pi", ".dsh"} {
		if !denied(filepath.Join(home, d, "f")) {
			t.Errorf("~/%s/f not denied", d)
		}
	}
	if denied(filepath.Join(home, "work", "f")) {
		t.Error("~/work/f denied")
	}
}
