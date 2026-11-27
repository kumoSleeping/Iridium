package testagent

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// DSHPlugin is the dsh plugin's checkout (github.com/otae-1204/dsh-remote-iridium; in this
// repository's plugins/ till 2026-11-27) that tests run as an executor against this side:
// IRIDIUM_DSH_PLUGIN. Not set, or no node, the test is skipped; CI checks the plugin out and sets it.
func DSHPlugin(t testing.TB) string {
	t.Helper()
	dir := os.Getenv("IRIDIUM_DSH_PLUGIN")
	if dir == "" {
		t.Skip("IRIDIUM_DSH_PLUGIN not set: the dsh plugin's checkout (github.com/otae-1204/dsh-remote-iridium)")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("no node")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(abs, "executor.js")); err != nil {
		t.Fatalf("IRIDIUM_DSH_PLUGIN=%s: not the dsh plugin: %v", dir, err)
	}
	return abs
}
