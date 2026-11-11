package executor

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// An agent started with its session's TOKEN gets ir: a script in its holder's bin/, first on its
// PATH, with the address and the TOKEN written in (its commands may not get them in their
// environment), running the binary by the name ir; one without a TOKEN, nothing.
func TestWithIR(t *testing.T) {
	state := t.TempDir()
	// Stands in for the binary: says how it was named and what it got.
	fake := filepath.Join(state, "iridium")
	os.WriteFile(fake, []byte("#!/bin/sh\necho \"$(basename \"$0\") $IRIDIUM_URL $IRIDIUM_TOKEN $*\"\n"), 0o755)
	e := &Executor{o: Options{URL: "wss://ir.example.com/v1/internal/exec", State: state, IR: fake, Logf: t.Logf}}
	dir := filepath.Join(state, "holds", "h1")
	if got := e.withIR([]string{"PATH=/usr/bin"}, dir); strings.Join(got, " ") != "PATH=/usr/bin" {
		t.Errorf("without a TOKEN: %v", got)
	}
	env := e.withIR([]string{"PATH=/usr/bin", "IRIDIUM_TOKEN=irs_x"}, dir)
	bin := filepath.Join(dir, "bin")
	want := map[string]bool{"PATH=" + bin + ":/usr/bin": true, "IRIDIUM_TOKEN=irs_x": true, "IRIDIUM_URL=https://ir.example.com": true}
	if len(env) != len(want) {
		t.Errorf("env: %v", env)
	}
	for _, kv := range env {
		if !want[kv] {
			t.Errorf("env has %q", kv)
		}
	}
	// Run as an agent's command: only a PATH.
	cmd := exec.Command(filepath.Join(bin, "ir"), "ls", "--here")
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	out, err := cmd.Output()
	if err != nil || strings.TrimSpace(string(out)) != "ir https://ir.example.com irs_x ls --here" {
		t.Errorf("ir: %q %v", out, err)
	}
	// One the Iridium set keeps its own.
	env = e.withIR([]string{"IRIDIUM_TOKEN=irs_x", "IRIDIUM_URL=https://other"}, dir)
	if !strings.Contains(strings.Join(env, " "), "IRIDIUM_URL=https://other") {
		t.Errorf("its own IRIDIUM_URL: %v", env)
	}
}
