package remote

import (
	"os"
	"runtime"
	"strings"
	"testing"
)

func TestLocalExecAuthLifecycle(t *testing.T) {
	dir := t.TempDir()
	ws := LocalExecWSURL("127.0.0.1:7810")
	if ws != "ws://127.0.0.1:7810/v1/internal/exec" {
		t.Fatalf("unexpected ws url: %q", ws)
	}

	auth1, err := WriteLocalExec(dir, ws)
	if err != nil {
		t.Fatalf("WriteLocalExec: %v", err)
	}
	if !strings.HasPrefix(auth1.Token(), LocalExecTokenPrefix) {
		t.Fatalf("token missing prefix: %q", auth1.Token())
	}
	if !auth1.Match(auth1.Token()) {
		t.Fatal("expected token match")
	}
	if auth1.Match("irl_wrong") || auth1.Match("ir_wrong") {
		t.Fatal("unexpected match for bad token")
	}

	got, err := ReadLocalExec(dir)
	if err != nil {
		t.Fatalf("ReadLocalExec: %v", err)
	}
	if got.WS != ws || got.Token != auth1.Token() || got.PID != os.Getpid() {
		t.Fatalf("unexpected payload: %+v", got)
	}
	if runtime.GOOS != "windows" {
		st, err := os.Stat(auth1.Path())
		if err != nil {
			t.Fatalf("stat: %v", err)
		}
		if perm := st.Mode().Perm(); perm != 0o600 {
			t.Fatalf("expected 0600, got %#o", perm)
		}
	}

	// A newer instance replaces the file; closing the older auth must not remove the newer file.
	auth2, err := WriteLocalExec(dir, LocalExecWSURL("127.0.0.1:7811"))
	if err != nil {
		t.Fatalf("WriteLocalExec 2: %v", err)
	}
	if err := auth1.Close(); err != nil {
		t.Fatalf("auth1.Close: %v", err)
	}
	got2, err := ReadLocalExec(dir)
	if err != nil {
		t.Fatalf("expected newer file to remain: %v", err)
	}
	if got2.Token != auth2.Token() {
		t.Fatalf("expected auth2 token, got %q", got2.Token)
	}

	// Closing auth2 removes the file.
	if err := auth2.Close(); err != nil {
		t.Fatalf("auth2.Close: %v", err)
	}
	if _, err := os.Stat(auth2.Path()); !os.IsNotExist(err) {
		t.Fatalf("expected file removed, err=%v", err)
	}
}

func TestLocalExecWSURL(t *testing.T) {
	cases := map[string]string{
		"127.0.0.1:7810": "ws://127.0.0.1:7810/v1/internal/exec",
		"0.0.0.0:7810":   "ws://127.0.0.1:7810/v1/internal/exec",
		":7810":          "ws://127.0.0.1:7810/v1/internal/exec",
		"[::1]:7810":     "ws://[::1]:7810/v1/internal/exec",
		"[::]:7810":      "ws://[::1]:7810/v1/internal/exec",
	}
	for in, want := range cases {
		if got := LocalExecWSURL(in); got != want {
			t.Errorf("LocalExecWSURL(%q) = %q, want %q", in, got, want)
		}
	}
}
