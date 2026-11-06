package install

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kumoSleeping/iridium/internal/config"
)

func TestInstall(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))
	cfgPath := filepath.Join(root, "config", "iridium", "config.json")
	var calls [][]string
	var out bytes.Buffer
	o := Options{
		Exe: "/home/u/.local/opt/iridium/iridium", ConfigPath: cfgPath, UnitDir: filepath.Join(root, "config", "systemd", "user"),
		Unit: "iridium-test-daemon", Path: "/home/u/.local/bin:/usr/bin", Env: []string{"HOME=/tmp/a home"}, Linger: filepath.Join(root, "linger", "u"),
		Systemctl: func(args ...string) error { calls = append(calls, args); return nil }, Out: &out,
	}
	if err := Install(o); err != nil {
		t.Fatal(err)
	}
	unit, _ := os.ReadFile(filepath.Join(o.UnitDir, "iridium-test-daemon.service"))
	for _, want := range []string{"ExecStart=/home/u/.local/opt/iridium/iridium serve -config " + cfgPath, "Environment=PATH=/home/u/.local/bin:/usr/bin", `Environment="HOME=/tmp/a home"`, "Restart=on-failure", "WantedBy=default.target"} {
		if !strings.Contains(string(unit), want) {
			t.Errorf("unit has no %q:\n%s", want, unit)
		}
	}
	cfg, err := config.Load(cfgPath)
	if err != nil || cfg.Listen != "127.0.0.1:7810" {
		t.Fatalf("config: %+v %v", cfg, err)
	}
	if _, err := os.Stat(filepath.Join(root, "state", "iridium")); err != nil {
		t.Errorf("state dir: %v", err)
	}
	if fmt.Sprint(calls) != "[[daemon-reload]]" || !strings.Contains(out.String(), "systemctl --user enable --now iridium-test-daemon.service") || !strings.Contains(out.String(), "enable-linger u") {
		t.Errorf("calls %v, out:\n%s", calls, out.String())
	}
	// Again, with the config there: kept as it is.
	os.WriteFile(cfgPath, []byte(`{"listen":"127.0.0.1:7811"}`), 0o600)
	os.MkdirAll(filepath.Dir(o.Linger), 0o700)
	os.WriteFile(o.Linger, nil, 0o600)
	calls, o.Now = nil, true
	out.Reset()
	if err := Install(o); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(cfgPath); string(b) != `{"listen":"127.0.0.1:7811"}` {
		t.Errorf("config rewritten: %s", b)
	}
	if fmt.Sprint(calls) != "[[daemon-reload] [enable --now iridium-test-daemon.service]]" || strings.Contains(out.String(), "linger") {
		t.Errorf("calls %v, out:\n%s", calls, out.String())
	}
	if q := quote("/a b/%x"); q != `"/a b/%%x"` {
		t.Errorf("quote: %s", q)
	}
}

// A binary run from where it was unpacked is copied into the prefix and ir links to it; run from
// there, it stays.
func TestPlace(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "dl", "iridium")
	os.MkdirAll(filepath.Dir(exe), 0o755)
	os.WriteFile(exe, []byte("bin"), 0o755)
	prefix, bin := filepath.Join(dir, "opt"), filepath.Join(dir, "bin")
	got, err := Place(exe, prefix, bin)
	if err != nil || got != filepath.Join(prefix, "iridium") {
		t.Fatalf("place: %q %v", got, err)
	}
	if b, _ := os.ReadFile(got); string(b) != "bin" {
		t.Fatalf("copied %q", b)
	}
	if to, _ := os.Readlink(filepath.Join(bin, "ir")); to != got {
		t.Fatalf("ir -> %q", to)
	}
	if again, err := Place(got, prefix, bin); err != nil || again != got {
		t.Fatalf("again: %q %v", again, err)
	}
}
