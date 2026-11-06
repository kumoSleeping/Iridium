package node

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakes are an ssh that runs the command here with HOME the test's, and a systemctl in that
// HOME's ~/.local/bin that only writes what it was asked (the real services are never touched).
func fakes(t *testing.T) (ssh, home string) {
	dir := t.TempDir()
	home = filepath.Join(dir, "home")
	os.MkdirAll(filepath.Join(home, ".local", "bin"), 0o755)
	ssh = filepath.Join(dir, "ssh")
	os.WriteFile(ssh, []byte(`#!/bin/sh
while [ $# -gt 0 ]; do
	case "$1" in -o|-i) shift 2 ;; *) break ;; esac
done
shift # the destination
case "$1" in "uname -sm") echo "$FAKE_UNAME"; exit 0 ;; esac
HOME=$FAKE_HOME exec sh -c "$1"
`), 0o755)
	os.WriteFile(filepath.Join(home, ".local", "bin", "systemctl"), []byte(`#!/bin/sh
echo "$*" >>"$HOME/systemctl.log"
case "$*" in *is-enabled*) [ -f "$HOME/enabled" ] ;; *) exit 0 ;; esac
`), 0o755)
	t.Setenv("FAKE_HOME", home)
	t.Setenv("FAKE_UNAME", map[string]string{"amd64": "Linux x86_64", "arm64": "Linux aarch64"}[runtime.GOARCH])
	return ssh, home
}

// The binary sent: version, and install (which marks the service enabled).
func binary(t *testing.T) string {
	exe := filepath.Join(t.TempDir(), "iridium")
	os.WriteFile(exe, []byte(`#!/bin/sh
case "$1" in
version) echo "iridium v9" ;;
install) echo "$*" >"$HOME/installed"; touch "$HOME/enabled" ;;
esac
`), 0o755)
	return exe
}

func read(path string) string { b, _ := os.ReadFile(path); return string(b) }

func TestPush(t *testing.T) {
	ssh, home := fakes(t)
	exe := binary(t)
	ctx := context.Background()
	o := PushOptions{Target: "ir@localhost", Key: "/k", Listen: "127.0.0.1:7811", Exe: exe, SSH: ssh, Out: io.Discard}
	// The first push installs on the port given.
	if err := Push(ctx, o); err != nil {
		t.Fatal(err)
	}
	if got := read(filepath.Join(home, ".local", "opt", "iridium", "iridium")); got != read(exe) {
		t.Fatalf("not in place: %q", got)
	}
	if got := read(filepath.Join(home, "installed")); strings.TrimSpace(got) != "install -now -listen 127.0.0.1:7811" {
		t.Fatalf("installed %q", got)
	}
	// The next one restarts.
	if err := Push(ctx, o); err != nil {
		t.Fatal(err)
	}
	if log := read(filepath.Join(home, "systemctl.log")); !strings.Contains(log, "--user restart iridium.service") {
		t.Fatalf("systemctl %q", log)
	}
	if _, err := os.Stat(filepath.Join(home, ".local", "opt", "iridium", ".iridium-push")); err == nil {
		t.Error("left the pushed file")
	}
	// Another platform: nothing sent.
	t.Setenv("FAKE_UNAME", "Darwin arm64")
	if err := Push(ctx, o); err == nil || !strings.Contains(err.Error(), "this binary is for") {
		t.Fatalf("other platform: %v", err)
	}
	if err := Push(ctx, PushOptions{Target: "-oProxyCommand=x", SSH: ssh}); err == nil {
		t.Error("an option as the destination")
	}
}

func TestNew(t *testing.T) {
	key := filepath.Join(t.TempDir(), "key")
	os.WriteFile(key+".pub", []byte("ssh-ed25519 AAAA test\n"), 0o600)
	var ran [][]string
	var pushed PushOptions
	o := NewOptions{
		User: "irtest", Listen: "127.0.0.1:7899", Key: key, Elevate: "sudo",
		Run: func(_ context.Context, name string, args ...string) error {
			ran = append(ran, append([]string{name}, args...))
			return nil
		},
		Push: func(_ context.Context, p PushOptions) error { pushed = p; return nil },
	}
	if err := New(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	// One sudo for all of the root part, the account and the key as its arguments.
	if len(ran) != 1 || strings.Join(ran[0][:3], " ") != "sudo sh -c" || ran[0][3] != rootPart || strings.Join(ran[0][4:], "|") != "sh|irtest|ssh-ed25519 AAAA test" {
		t.Fatalf("ran %q", ran)
	}
	if pushed.Target != "irtest@localhost" || pushed.Key != key || pushed.Listen != "127.0.0.1:7899" {
		t.Fatalf("pushed %+v", pushed)
	}
	// Without sudo: su with one quoted command, which a shell reads back as the same words.
	ran = nil
	o.Elevate = "su"
	if err := New(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	if len(ran) != 1 || ran[0][0] != "su" || ran[0][1] != "-c" || ran[0][3] != "root" {
		t.Fatalf("su %q", ran)
	}
	words, err := exec.Command("sh", "-c", "set -- "+ran[0][2]+`; printf '%s\036' "$@"`).Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Split(strings.TrimSuffix(string(words), "\x1e"), "\x1e"); len(got) != 6 || got[0] != "sh" || got[2] != rootPart || got[4] != "irtest" || got[5] != "ssh-ed25519 AAAA test" {
		t.Fatalf("su words %q", got)
	}
	// A failing root part stops it before anything is pushed.
	pushed = PushOptions{}
	o.Run = func(context.Context, string, ...string) error { return errors.New("no") }
	if err := New(context.Background(), o); err == nil || pushed.Target != "" {
		t.Fatalf("root part failed: %v %+v", err, pushed)
	}
	if err := New(context.Background(), NewOptions{User: "Bad Name"}); err == nil {
		t.Error("a bad name")
	}
}

// The root part itself, run as a shell script against stand-ins for the root commands.
func TestRootPart(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	home := filepath.Join(dir, "home")
	os.MkdirAll(bin, 0o755)
	for name, body := range map[string]string{
		"id":       `[ -f "$LOG.made" ]`,
		"useradd":  `touch "$LOG.made"; echo "useradd $*" >>"$LOG"`,
		"loginctl": `echo "loginctl $*" >>"$LOG"`,
		"getent":   `echo "irtest:x:1:1::$HOME_DIR:/bin/bash"`,
		"install":  `shift 7; mkdir -p "$1"`,
		"chown":    `true`,
	} {
		os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755)
	}
	log := filepath.Join(dir, "log")
	run := func() {
		c := exec.Command("sh", "-c", rootPart, "sh", "irtest", "ssh-ed25519 AAAA test")
		c.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "LOG="+log, "HOME_DIR="+home)
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
	}
	run()
	run() // again: not made twice, the key in once
	if got := read(log); got != "useradd -m -s /bin/bash irtest\nloginctl enable-linger irtest\nloginctl enable-linger irtest\n" {
		t.Fatalf("log %q", got)
	}
	if got := read(filepath.Join(home, ".ssh", "authorized_keys")); got != "ssh-ed25519 AAAA test\n" {
		t.Fatalf("authorized_keys %q", got)
	}
}
