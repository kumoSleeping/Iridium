package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLaunchHere(t *testing.T) {
	bin, state := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(bin, "tool"), []byte("#!/usr/bin/env node\n"), 0o755)
	env := []string{"PATH=" + bin}
	o := SpawnOptions{Session: "s1", SystemPromptFile: filepath.Join(state, "prompt.md")}
	if _, err := o.Command("tool", env); err == nil || !strings.Contains(err.Error(), "node not found") {
		t.Errorf("a node script without node: %v", err)
	}
	os.WriteFile(filepath.Join(bin, "node"), []byte("#!/bin/sh\n"), 0o755)
	if argv, err := o.Command("tool", env); err != nil || len(argv) != 1 || argv[0] != filepath.Join(bin, "tool") {
		t.Errorf("command %v %v", argv, err)
	}
	if p, err := o.PromptFile(); err != nil || p != o.SystemPromptFile {
		t.Errorf("prompt %q %v", p, err)
	}
	p, err := o.LaunchFile("ext.ts", []byte("x"))
	if b, _ := os.ReadFile(p); err != nil || p != filepath.Join(state, "ext.ts") || string(b) != "x" {
		t.Errorf("launch file %q %v %q", p, err, b)
	}
	if _, err := (SpawnOptions{}).LaunchFile("ext.ts", nil); err == nil {
		t.Error("a launch file with nowhere to go")
	}
}

func TestLaunchThere(t *testing.T) {
	state := t.TempDir()
	pf := filepath.Join(state, "prompt.md")
	os.WriteFile(pf, []byte("p"), 0o600)
	there := map[string]string{}
	o := SpawnOptions{Session: "s1", SystemPromptFile: pf, Remote: &Remote{
		Argv: []string{"/n/node", "/p/cli.js"}, Scratch: "/x",
		Join:      func(elem ...string) string { return strings.Join(elem, "/") },
		WriteFile: func(path string, data []byte) error { there[path] = string(data); return nil },
	}}
	if argv, err := o.Command("tool", []string{"PATH=/nowhere"}); err != nil || strings.Join(argv, " ") != "/n/node /p/cli.js" {
		t.Errorf("command %v %v", argv, err)
	}
	if p, err := o.PromptFile(); err != nil || p != "/x/s1/prompt.md" || there[p] != "p" {
		t.Errorf("prompt %q %v %v", p, err, there)
	}
	if p, err := o.LaunchFile("ext.ts", []byte("e")); err != nil || p != "/x/s1/ext.ts" || there[p] != "e" {
		t.Errorf("launch file %q %v", p, err)
	}
	o.Remote.Argv = nil
	if _, err := o.Command("tool", nil); err == nil {
		t.Error("no argv there")
	}
}
