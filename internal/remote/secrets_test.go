package remote_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kumoSleeping/iridium/internal/hold"
	"github.com/kumoSleeping/iridium/internal/platform"
	"github.com/kumoSleeping/iridium/internal/remote/executor"
	"github.com/kumoSleeping/iridium/internal/remote/wire"
)

// A secret in the executor's environment never reaches this side: the hello gives a placeholder,
// which the executor makes the value again when it starts a process. The variables pointing an ir
// at its own Iridium are left out.
func TestSecretsStayThere(t *testing.T) { kinds(t, testSecretsStayThere) }

func testSecretsStayThere(t *testing.T, kind string) {
	const value = "fixture-not-a-real-key"
	t.Setenv("FAKE_API_KEY", value)
	t.Setenv("IRIDIUM_SOCKET", "/there/iridium.sock")
	t.Setenv("IRIDIUM_URL", "http://127.0.0.1:1")
	r := newRig(t, kind)
	hi, _ := r.machine().Hello()
	if !slices.Contains(hi.Env, "FAKE_API_KEY="+executor.Secret) {
		t.Fatal("the hello does not give FAKE_API_KEY as the placeholder")
	}
	for _, kv := range hi.Env {
		if strings.Contains(kv, value) || strings.HasPrefix(kv, "IRIDIUM_SOCKET=") || strings.HasPrefix(kv, "IRIDIUM_URL=") {
			t.Fatalf("the hello gives %s", strings.SplitN(kv, "=", 2)[0])
		}
	}

	// A spawn based on it, as a driver makes one: this side's own variables go as they are.
	dir := filepath.Join(r.root, "u9")
	env := append(slices.Clone(hi.Env), "IRIDIUM_SESSION=s9", "OTHER_TOKEN="+executor.Secret)
	spec := hold.Spec{Argv: []string{"/bin/sh", "-c", `echo "k=$FAKE_API_KEY s=$IRIDIUM_SESSION o=${OTHER_TOKEN-unset} i=$IRIDIUM_SOCKET"`}, Env: env, Cwd: r.state}
	if err := hold.Prepare(dir, spec); err != nil {
		t.Fatal(err)
	}
	if err := r.machine().Launcher().Start(context.Background(), platform.Unit{Name: "u9"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "output", func() bool { return strings.Contains(readOut(dir), "\n") })
	if got, want := readOut(dir), "k="+value+" s=s9 o=unset i=\n"; got != want {
		t.Fatalf("out.log %q, want %q", got, want)
	}
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if !e.Type().IsRegular() {
			continue // the in pipe
		}
		if b, _ := os.ReadFile(filepath.Join(dir, e.Name())); e.Name() != hold.FileOutLog && strings.Contains(string(b), value) {
			t.Fatalf("%s here has the secret", e.Name())
		}
	}
}

// Logins and keys at the executor are never served; the transcripts beside them are.
func TestDeniedFiles(t *testing.T) { kinds(t, testDeniedFiles) }

func testDeniedFiles(t *testing.T, kind string) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, k := range []string{"DSH_HOME", "CLAUDE_CONFIG_DIR", "CODEX_HOME"} {
		t.Setenv(k, "")
	}
	put := func(rel string) string {
		p := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	denied := []string{
		".dsh/.credentials.yaml", ".dsh/.env", ".dsh/profiles/web/cordis.yml", ".claude/.credentials.json",
		".codex/auth.json", ".pi/agent/auth.json", ".aws/credentials", ".config/gh/hosts.yml", ".gnupg/k", ".netrc", ".ssh/id_ed25519",
	}
	allowed := []string{".dsh/storages/workspace.json", ".claude/projects/p/s.jsonl", ".codex/sessions/s.jsonl"}
	for _, rel := range append(slices.Clone(denied), allowed...) {
		put(rel)
	}
	// A link to a login is denied as the login is.
	if err := os.Symlink(filepath.Join(home, ".claude", ".credentials.json"), filepath.Join(home, "link.json")); err != nil {
		t.Fatal(err)
	}
	r := newRig(t, kind)
	ctx := context.Background()
	for _, rel := range append(denied, "link.json") {
		if _, err := r.machine().ReadFile(ctx, filepath.Join(home, rel), 1<<10); !wire.IsCode(err, wire.CodeDenied) {
			t.Errorf("%s: %v, want denied", rel, err)
		}
	}
	if err := r.machine().WriteFile(ctx, filepath.Join(home, ".dsh/profiles/web/cordis.yml"), []byte("y"), true); !wire.IsCode(err, wire.CodeDenied) {
		t.Errorf("wrote the profile: %v", err)
	}
	for _, rel := range allowed {
		if b, err := r.machine().ReadFile(ctx, filepath.Join(home, rel), 1<<10); err != nil || string(b) != "x" {
			t.Errorf("%s: %q, %v", rel, b, err)
		}
	}
}
