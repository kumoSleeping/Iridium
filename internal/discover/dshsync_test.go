package discover

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
)

// DSH Desktop's sessions here (the ones its registry files) are shown by activity only with
// "sync new sessions" on, and by activity after it was set; a terminal's dsh sessions as ever.
func TestDshDesktopSync(t *testing.T) {
	r := newRig(t)
	r.x.o.State = t.TempDir()
	dsh := filepath.Join(r.home, ".dsh")
	session := func(id string, lines ...string) {
		t.Helper()
		p := filepath.Join(dsh, "sessions", "--x-work--", id, "session.v4.jsonl.zstd")
		os.MkdirAll(filepath.Dir(p), 0o700)
		if err := os.WriteFile(p, []byte(zlines(t, `{"type":"session","cwd":"/work"}`, lines...)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	registry := func(ids ...string) {
		t.Helper()
		list := ""
		for i, id := range ids {
			if i > 0 {
				list += ","
			}
			list += quote(id)
		}
		p := filepath.Join(dsh, "storages", "workspace.json")
		os.MkdirAll(filepath.Dir(p), 0o700)
		if err := os.WriteFile(p, []byte(`{"unit":{"name":"workspace","version":2},"global":{"workspaceIds":["w0"]},"tables":{"workspaces":{"w0":{"path":"/work","title":"work","sessionIds":[`+list+`]}}}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		r.x.syncMu.Lock()
		r.x.reg.checked = time.Time{} // read again now
		r.x.syncMu.Unlock()
	}
	// Off, as it comes: Desktop's session stays out however it grows, a terminal's comes in.
	registry("desk")
	session("desk")
	session("term")
	r.x.Scan()
	session("desk", `{"type":"event","seq":1}`)
	session("term", `{"type":"event","seq":1}`)
	r.x.Scan()
	if len(r.sessions()) < 2 || r.shown(api.AgentDsh, "desk") || !r.shown(api.AgentDsh, "term") {
		t.Fatalf("off: desk shown %v, term shown %v", r.shown(api.AgentDsh, "desk"), r.shown(api.AgentDsh, "term"))
	}
	// On: what Desktop did before does not come in, what it does after does. Kept over a restart.
	r.x.SetSync("", true)
	time.Sleep(20 * time.Millisecond)
	if on, at := r.x.localSync(); !on || at == 0 {
		t.Fatal("not on")
	}
	r.x.sync = nil
	if !r.x.Sync("") {
		t.Fatal("not kept")
	}
	registry("desk", "desk2")
	session("desk2")
	old := time.Now().Add(-time.Hour)
	os.Chtimes(filepath.Join(dsh, "sessions", "--x-work--", "desk2", "session.v4.jsonl.zstd"), old, old)
	r.x.Scan()
	session("desk", `{"type":"event","seq":1}`, `{"type":"event","seq":2}`)
	r.x.Scan()
	if !r.shown(api.AgentDsh, "desk") || r.shown(api.AgentDsh, "desk2") {
		t.Errorf("on: desk shown %v, desk2 (not touched since) shown %v", r.shown(api.AgentDsh, "desk"), r.shown(api.AgentDsh, "desk2"))
	}
	// Adopting an unsynced local Desktop session shows it immediately, even when sync is turned off.
	r.x.SetSync("", false)
	if err := r.x.Adopt(context.Background(), "", DesktopSession{ID: "desk2"}); err != nil {
		t.Fatalf("local Adopt: %v", err)
	}
	if !r.shown(api.AgentDsh, "desk2") {
		t.Fatal("desk2 should be shown after local Adopt")
	}
}
