package node

import (
	"context"
	"errors"
	"io"
	"os"
	"os/user"
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
	var ran []string
	made := false
	var pushed PushOptions
	o := NewOptions{
		User: "irtest", Listen: "127.0.0.1:7899", Key: key,
		Run: func(_ context.Context, _ io.Reader, name string, args ...string) error {
			ran = append(ran, name+" "+strings.Join(args, " "))
			if len(args) > 0 && args[0] == "useradd" {
				made = true
			}
			return nil
		},
		Lookup: func(name string) (*user.User, error) {
			if !made {
				return nil, user.UnknownUserError(name)
			}
			return &user.User{Username: name, HomeDir: "/home/" + name}, nil
		},
		Push: func(_ context.Context, p PushOptions) error { pushed = p; return nil },
	}
	if err := New(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	if len(ran) != 3 || ran[0] != "sudo useradd -m -s /bin/bash irtest" || ran[1] != "sudo loginctl enable-linger irtest" ||
		!strings.HasPrefix(ran[2], "sudo sh -c") || !strings.HasSuffix(ran[2], "sh /home/irtest irtest ssh-ed25519 AAAA test") {
		t.Fatalf("ran %q", ran)
	}
	if pushed.Target != "irtest@localhost" || pushed.Key != key || pushed.Listen != "127.0.0.1:7899" {
		t.Fatalf("pushed %+v", pushed)
	}
	// An account that is there is not made again.
	ran = nil
	if err := New(context.Background(), o); err != nil || len(ran) != 2 {
		t.Fatalf("again: %q %v", ran, err)
	}
	// A failing sudo stops it before anything is pushed.
	pushed = PushOptions{}
	o.Run = func(context.Context, io.Reader, string, ...string) error { return errors.New("no") }
	if err := New(context.Background(), o); err == nil || pushed.Target != "" {
		t.Fatalf("sudo failed: %v %+v", err, pushed)
	}
	if err := New(context.Background(), NewOptions{User: "Bad Name"}); err == nil {
		t.Error("a bad name")
	}
}
