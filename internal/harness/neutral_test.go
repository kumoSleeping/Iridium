package harness_test

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// The core does not name dsh: what is dsh's alone is in its channel (internal/harness/dsh). Each
// pattern below found in a core package's code fails, but in the files still to move
// (pending: docs/exec.md "通道"), each with what keeps it there.
func TestCoreNamesNoHarness(t *testing.T) {
	_, here, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(here), "..")
	core := []string{"session", "server", "cli", "chat", "remote"}
	banned := regexp.MustCompile(`api\.AgentDsh|internal/agent/dsh"|internal/dshhome"|CapHeld|DSH Desktop|"held\.|"dsh\.`)
	pending := map[string]string{
		"server/folders_remote.go": "the /dsh/ routes, till the channel mounts them",
		"cli/sessions.go":          "SendReply.Desktop's wait, till the reply names the holder",
		"remote/fs.go":             "DshOpen, till remote discovery of dsh moves to the channel",
		"remote/wire/proto.go":     "the dsh plugin's wire types, till remote discovery of dsh moves",
	}
	for _, pkg := range core {
		err := filepath.WalkDir(filepath.Join(root, pkg), func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			rel, _ := filepath.Rel(root, path)
			if _, ok := pending[filepath.ToSlash(rel)]; ok {
				return nil
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for i, line := range strings.Split(string(b), "\n") {
				if m := banned.FindString(line); m != "" {
					t.Errorf("%s:%d names %q: dsh's own code goes in internal/harness/dsh", rel, i+1, m)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
