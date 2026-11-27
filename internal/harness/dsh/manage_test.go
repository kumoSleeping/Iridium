package dsh

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/harness"
)

func TestComputerStateAndManualPath(t *testing.T) {
	stateDir := t.TempDir()
	synced := map[string]bool{}
	ch := New(Options{
		StateDir: stateDir,
		GetSync:  func(m string) bool { return synced[m] },
		SetSync:  func(m string, on bool) { synced[m] = on },
	})
	defer ch.Close()

	// Remote computer: Sync is reported, PathNeeded is always false.
	st := ch.ComputerState("pc1")
	if st.Sync == nil || *st.Sync {
		t.Fatalf("remote default sync = %v, want false", st.Sync)
	}
	if st.PathNeeded {
		t.Fatal("remote computer should never report PathNeeded")
	}

	on := true
	st, err := ch.SetComputerConfig(context.Background(), "pc1", harness.ComputerConfig{Sync: &on})
	if err != nil {
		t.Fatalf("SetComputerConfig(sync): %v", err)
	}
	if st.Sync == nil || !*st.Sync || !synced["pc1"] {
		t.Fatalf("expected sync=true, got st=%+v map=%v", st, synced["pc1"])
	}

	// Path on remote is rejected.
	bad := "/tmp/nope"
	if _, err := ch.SetComputerConfig(context.Background(), "pc1", harness.ComputerConfig{Path: &bad}); err == nil {
		t.Fatal("expected error setting path on remote machine")
	}

	// Invalid path on host is rejected.
	if _, err := ch.SetComputerConfig(context.Background(), "", harness.ComputerConfig{Path: &bad}); err == nil {
		t.Fatal("expected error for invalid Desktop path")
	}

	// Valid fake Desktop install directory succeeds and persists.
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "resources", "runtime", "primary-runtime"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "resources", "app.asar"), []byte("asar"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "deepseek-harness"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	st, err = ch.SetComputerConfig(context.Background(), "", harness.ComputerConfig{Path: &root})
	if err != nil {
		t.Fatalf("SetComputerConfig(valid path): %v", err)
	}
	if LoadManualDesktopPath(stateDir) != root {
		t.Fatalf("LoadManualDesktopPath = %q, want %q", LoadManualDesktopPath(stateDir), root)
	}
	if !st.PathNeeded || st.Path != root {
		t.Fatalf("expected PathNeeded=true and Path=%q until Desktop connects, got %+v", root, st)
	}

	// Clearing path removes manual config.
	empty := ""
	st, err = ch.SetComputerConfig(context.Background(), "", harness.ComputerConfig{Path: &empty})
	if err != nil {
		t.Fatalf("SetComputerConfig(clear path): %v", err)
	}
	if LoadManualDesktopPath(stateDir) != "" || st.Path != "" {
		t.Fatalf("expected cleared manual path, got %q / %+v", LoadManualDesktopPath(stateDir), st)
	}

	// AdoptExternal delegates to Options.Adopt.
	var adopted api.ExternalSession
	ch2 := New(Options{
		Adopt: func(_ context.Context, machine string, s DesktopSession) error {
			adopted = api.ExternalSession{ID: s.ID, Machine: machine, Cwd: s.Cwd, Title: s.Title}
			return nil
		},
	})
	defer ch2.Close()
	if err := ch2.AdoptExternal(context.Background(), api.ExternalSession{ID: "s1", Agent: api.AgentDsh, Machine: "pc1", Cwd: "/p", Title: "hi"}); err != nil {
		t.Fatalf("AdoptExternal: %v", err)
	}
	if adopted.ID != "s1" || adopted.Machine != "pc1" || adopted.Title != "hi" {
		t.Fatalf("adopted = %+v", adopted)
	}
}
