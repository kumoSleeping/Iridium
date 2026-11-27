package dsh

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/kumoSleeping/iridium/internal/agent"
)

func TestSpawnInprocLocalAndRemote(t *testing.T) {
	dir := t.TempDir()
	sysPath := filepath.Join(dir, "system-prompt.md")
	if err := os.WriteFile(sysPath, []byte("hello system prompt\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// 1. Local new session with preset, system prompt, persona.
	drv := New(Options{})
	spec, err := drv.Spawn(agent.SpawnOptions{
		Session:          "sess-1",
		Cwd:              dir,
		SystemPromptFile: sysPath,
		Preset:           "coding",
		Persona:          true,
		Host:             &agent.Host{Inproc: true, Argv: []string{InprocArgv0}, Version: "0.2.2-rc.5"},
	})
	if err != nil {
		t.Fatalf("Spawn new inproc: %v", err)
	}
	if len(spec.Argv) == 0 || spec.Argv[0] != InprocArgv0 {
		t.Fatalf("expected argv[0]=%s, got %v", InprocArgv0, spec.Argv)
	}
	for _, bad := range []string{"--profile", "sdk", "--patch"} {
		if slices.Contains(spec.Argv, bad) {
			t.Fatalf("inproc argv must not contain %q: %v", bad, spec.Argv)
		}
	}
	if !hasArgPair(spec.Argv, "--preset", "coding") {
		t.Fatalf("missing --preset coding in %v", spec.Argv)
	}
	if !hasArgPair(spec.Argv, "--persona", "own") {
		t.Fatalf("missing --persona own in %v", spec.Argv)
	}
	if slices.Contains(spec.Argv, "--keep-model") {
		t.Fatalf("new session must not pass --keep-model: %v", spec.Argv)
	}
	wantSys := filepath.Join(dir, SystemFile)
	if !hasArgPair(spec.Argv, "--system", wantSys) {
		t.Fatalf("missing --system %s in %v", wantSys, spec.Argv)
	}

	// 2. Resume without explicit model passes --keep-model and omits --preset.
	drv2 := New(Options{})
	spec2, err := drv2.Spawn(agent.SpawnOptions{
		Session:          "sess-2",
		Resume:           true,
		Cwd:              dir,
		SystemPromptFile: sysPath,
		Preset:           "coding",
		Host:             &agent.Host{Inproc: true, Argv: []string{InprocArgv0}},
	})
	if err != nil {
		t.Fatalf("Spawn resume inproc: %v", err)
	}
	if slices.Contains(spec2.Argv, "--preset") {
		t.Fatalf("resume must not pass --preset: %v", spec2.Argv)
	}
	if !slices.Contains(spec2.Argv, "--keep-model") {
		t.Fatalf("resume without explicit model must pass --keep-model: %v", spec2.Argv)
	}

	// 3. Remote inproc triggered purely by Remote.Argv[0] == "@dsh-desktop", explicit model omits --keep-model.
	var wrotePath string
	drv3 := New(Options{})
	spec3, err := drv3.Spawn(agent.SpawnOptions{
		Session:          "sess-3",
		Resume:           true,
		Cwd:              `C:\work`,
		Model:            "deepseek-official/deepseek-chat",
		SystemPromptFile: sysPath,
		Remote: &agent.Remote{
			Argv:    []string{InprocArgv0},
			Version: "0.2.2-rc.5",
			Scratch: `C:\scratch`,
			Join:    func(elem ...string) string { return filepath.Join(elem...) },
			WriteFile: func(p string, data []byte) error {
				wrotePath = p
				return nil
			},
		},
	})
	if err != nil {
		t.Fatalf("Spawn remote inproc: %v", err)
	}
	if slices.Contains(spec3.Argv, "--keep-model") {
		t.Fatalf("explicit model must not pass --keep-model: %v", spec3.Argv)
	}
	if wrotePath == "" || !hasArgPair(spec3.Argv, "--system", wrotePath) {
		t.Fatalf("expected remote system file written and passed, wrotePath=%q argv=%v", wrotePath, spec3.Argv)
	}
}

func hasArgPair(argv []string, k, v string) bool {
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == k && argv[i+1] == v {
			return true
		}
	}
	return false
}

func TestSpawnInprocAttachFirstLaunchOnly(t *testing.T) {
	dir := t.TempDir()
	host := &agent.Host{Inproc: true, Argv: []string{InprocArgv0}}
	spec, err := New(Options{}).Spawn(agent.SpawnOptions{Session: "s1", Cwd: dir, Attach: "omd.ultra.team", Host: host})
	if err != nil {
		t.Fatal(err)
	}
	if !hasArgPair(spec.Argv, "--attach", "omd.ultra.team") {
		t.Fatalf("first launch: missing --attach omd.ultra.team in %v", spec.Argv)
	}
	spec, err = New(Options{}).Spawn(agent.SpawnOptions{Session: "s1", Cwd: dir, Resume: true, Attach: "omd.ultra.team", Host: host})
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(spec.Argv, "--attach") {
		t.Fatalf("resume must not pass --attach: %v", spec.Argv)
	}
	spec, err = New(Options{}).Spawn(agent.SpawnOptions{Session: "s2", Cwd: dir, Host: host})
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(spec.Argv, "--attach") {
		t.Fatalf("no key: must not pass --attach: %v", spec.Argv)
	}
}
