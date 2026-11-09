package dsh

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/harness"
)

// homeHost is a Host whose agents start with DSH_HOME at home.
type homeHost struct {
	harness.Host
	home string
}

func (h homeHost) AgentEnv() []string { return []string{"DSH_HOME=" + h.home} }

// attach keeps data in home's attachments, as dsh does, and is its handle.
func attach(t *testing.T, home string, data []byte) string {
	t.Helper()
	sum := sha256.Sum256(data)
	hx := hex.EncodeToString(sum[:])
	dir := filepath.Join(home, "attachments", "v1", "objects", hx[:2])
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, hx), data, 0o600); err != nil {
		t.Fatal(err)
	}
	return "sha256:" + hx
}

func TestImageFromAttachments(t *testing.T) {
	home := t.TempDir()
	c := New(Options{})
	c.Bind(homeHost{home: home})
	ctx := context.Background()
	here := harness.Ref{ID: "s", Agent: api.AgentDsh}
	pic := []byte("\x89PNG\r\n\x1a\n a picture")
	handle := attach(t, home, pic)

	data, done, err := c.Image(ctx, here, handle)
	if err != nil || !done || !bytes.Equal(data, pic) {
		t.Fatalf("Image: %q %v %v", data, done, err)
	}
	// Kept: read again though the file went.
	if err := os.RemoveAll(filepath.Join(home, "attachments")); err != nil {
		t.Fatal(err)
	}
	if data, _, err := c.Image(ctx, here, handle); err != nil || !bytes.Equal(data, pic) {
		t.Fatalf("kept: %q %v", data, err)
	}
	// Gone, and one whose content is not its sum.
	other := []byte("another")
	gone := attach(t, home, other)
	if err := os.Remove(filepath.Join(home, "attachments", "v1", "objects", gone[7:9], gone[7:])); err != nil {
		t.Fatal(err)
	}
	if _, done, err := c.Image(ctx, here, gone); !done || !errors.Is(err, harness.ErrGone) {
		t.Errorf("gone: %v %v", done, err)
	}
	bad := attach(t, home, other)
	os.WriteFile(filepath.Join(home, "attachments", "v1", "objects", bad[7:9], bad[7:]), []byte("changed"), 0o600)
	if _, done, err := c.Image(ctx, here, bad); !done || !errors.Is(err, harness.ErrGone) {
		t.Errorf("not its sum: %v %v", done, err)
	}
	// Not dsh's, not a handle, a machine not connected.
	for _, tc := range []struct {
		r      harness.Ref
		handle string
	}{
		{harness.Ref{Agent: api.AgentClaude}, handle},
		{here, "sha256:../../etc/passwd"},
		{here, "/etc/passwd"},
	} {
		if _, done, err := c.Image(ctx, tc.r, tc.handle); done || err != nil {
			t.Errorf("%v %q: done %v %v", tc.r.Agent, tc.handle, done, err)
		}
	}
	if _, done, err := c.Image(ctx, harness.Ref{Agent: api.AgentDsh, Machine: "m1"}, gone); !done || !errors.Is(err, harness.ErrOffline) {
		t.Errorf("at a machine not connected: %v %v", done, err)
	}
}

func TestKeptPicturesBounded(t *testing.T) {
	c := New(Options{})
	big := make([]byte, keptBytes/3+1)
	for i := range 5 {
		c.keep(string(rune('a'+i)), big)
	}
	if c.imageBytes > keptBytes || len(c.images) != 2 {
		t.Fatalf("kept %d bytes in %d", c.imageBytes, len(c.images))
	}
	if _, ok := c.kept("e"); !ok {
		t.Error("the newest went")
	}
	if _, ok := c.kept("a"); ok {
		t.Error("the oldest stayed")
	}
}
