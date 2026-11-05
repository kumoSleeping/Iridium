package pi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kumoSleeping/iridium/internal/agent"
)

// A session's effort goes on the command line, unless the model names its own level.
func TestSpawnEffort(t *testing.T) {
	bin := t.TempDir()
	os.WriteFile(filepath.Join(bin, "pi"), []byte("#!/usr/bin/env node\n"), 0o755)
	os.WriteFile(filepath.Join(bin, "node"), []byte("#!/bin/sh\n"), 0o755)
	pf := filepath.Join(t.TempDir(), "system-prompt.md")
	for model, want := range map[string]string{"p/m": "--thinking high", "p/m:low": "--thinking low"} {
		spec, err := New(Options{}).Spawn(agent.SpawnOptions{Session: "s", Cwd: "/w", Env: []string{"PATH=" + bin}, Program: "pi", SystemPromptFile: pf, Model: model, Effort: "high"})
		if err != nil || !strings.Contains(strings.Join(spec.Argv, " "), want) {
			t.Errorf("%s: %v %v", model, spec.Argv, err)
		}
	}
}
