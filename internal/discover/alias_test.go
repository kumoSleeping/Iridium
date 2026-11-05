package discover

import (
	"path/filepath"
	"testing"

	"github.com/kumoSleeping/iridium/internal/api"
)

// A hosted Codex runs its thread under Iridium's id for the session (IRIDIUM_SESSION): with the
// alias it is Iridium's own, not elsewhere, and not its own launcher.
func TestHostedCodexAlias(t *testing.T) {
	r := newRig(t)
	rollout := filepath.Join(r.home, ".codex", "sessions", "2026", "01", "01", "rollout-2026-01-01T00-00-00-"+idCodex+".jsonl")
	const hosted = "99999999-9999-4999-8999-999999999999"
	server := func(start uint64) *fakeProc {
		return &fakeProc{Stat: Stat{PPid: 1, Start: start}, argv: []string{"/opt/codex/bin/codex", "app-server", "--listen", "stdio://"}, exe: "/opt/codex/bin/codex", files: []string{rollout},
			env: []string{"IRIDIUM_SESSION=" + hosted}}
	}
	r.procs.set(600, server(1))
	r.x.Scan()
	if c := r.sessions()[idCodex]; c.Status != api.StatusElsewhere || c.Launcher != hosted {
		t.Fatalf("without the alias: %+v", c)
	}
	r.x.SetAlias(func(id string) string {
		if id == hosted {
			return idCodex
		}
		return ""
	})
	r.procs.set(600, server(2))
	r.x.Scan()
	if c := r.sessions()[idCodex]; c.Status != api.StatusIdle || c.Live {
		t.Errorf("with the alias: %+v", c)
	}
	// The thread's lock aside (none here), Iridium's own process does not keep it busy.
	if err := r.x.Free(idCodex); err != nil {
		t.Errorf("free: %v", err)
	}
	// Discovery reads its transcript in Codex's format.
	if _, f, ok := r.x.Found(idCodex); !ok || f == nil {
		t.Errorf("found: %v %v", ok, f)
	} else if p, err := f.Latest(); err != nil || len(p.Blocks) == 0 {
		t.Errorf("blocks: %+v %v", p, err)
	}
}
