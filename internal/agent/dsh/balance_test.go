package dsh

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAPIKey(t *testing.T) {
	home := t.TempDir()
	env := []string{"DSH_HOME=" + home, "DEEPSEEK_API_KEY=from-env"}
	if k := APIKey(env); k != "from-env" {
		t.Fatalf("env: %q", k)
	}
	if err := os.WriteFile(filepath.Join(home, ".env"), []byte("export DEEPSEEK_API_KEY='from-dotenv'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if k := APIKey(env[:1]); k != "from-dotenv" {
		t.Fatalf(".env: %q", k)
	}
	stored := "version: 1\nrecords:\n  a/b:\n    kind: x\n    payload:\n      DEEPSEEK_API_KEY: wrong\nrefs:\n  OTHER: o\n  DEEPSEEK_API_KEY: \"from-file\"\n"
	if err := os.WriteFile(filepath.Join(home, ".credentials.yaml"), []byte(stored), 0o600); err != nil {
		t.Fatal(err)
	}
	if k := APIKey(env); k != "from-file" {
		t.Fatalf("stored file first: %q", k)
	}
}
