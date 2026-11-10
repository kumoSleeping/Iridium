package testagent

import (
	"os"
	"path/filepath"
	"testing"
)

// Programs writes the fake agents into bin and returns config.Programs for them. A test rig starts
// every agent through these, never a real one: tests that started Codex sessions that way ran the
// user's own Codex, which wrote its rollouts into ~/.codex and opened threads on their account
// (2026-11-11: 95 of them, from three tests).
//
// Claude and Codex are the test binary itself, linked under their names (Main runs the fakes). pi
// and dsh are a stub that takes what is written and never answers, enough for a session to be
// made and routed to.
func Programs(t *testing.T, bin string) map[string]string {
	t.Helper()
	stub := filepath.Join(bin, "stub")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\nexec cat > /dev/null\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	p := map[string]string{}
	for _, name := range []string{"claude", "codex"} {
		link := filepath.Join(bin, name)
		if err := os.Symlink(os.Args[0], link); err != nil {
			t.Fatal(err)
		}
		p[name] = link
	}
	for _, name := range []string{"pi", "dsh"} {
		link := filepath.Join(bin, name)
		if err := os.Symlink(stub, link); err != nil {
			t.Fatal(err)
		}
		p[name] = link
	}
	return p
}
