// Package plugins holds the dsh plugins (JavaScript, published to npm); this test runs their own
// tests (node --test, test/*.test.mjs) with the Go ones, so CI and preflight cover them.
package plugins

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestPlugins(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("no node")
	}
	files, _ := filepath.Glob("*/test/*.test.mjs")
	if len(files) == 0 {
		t.Fatal("no plugin tests")
	}
	for _, f := range files {
		t.Run(f, func(t *testing.T) {
			t.Parallel()
			cmd := exec.Command("node", "--test", f)
			cmd.Env = append(os.Environ(), "NODE_NO_WARNINGS=1")
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Errorf("%v\n%s", err, out)
			}
		})
	}
}
