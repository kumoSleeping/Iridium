//go:build !windows

package session

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/config"
	hdsh "github.com/kumoSleeping/iridium/internal/harness/dsh"
	"github.com/kumoSleeping/iridium/internal/remote"
	"github.com/kumoSleeping/iridium/internal/remote/wire"
)

// The real dsh plugin (test/run.mjs) speaking dsh.inproc, desktop.* and session.attach: models
// come from desktop.models (and refresh on SIGUSR2 / desktop.models.changed), workspaces and
// search come from desktop.workspaces / desktop.sessions, and attached buttons round-trip through
// session.attach / session.button and survive reconnect (SIGUSR1).
func TestInprocPlugin(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("no node")
	}
	const id = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	hub := &remote.Hub{Logf: t.Logf}
	f := &heldFinder{desktopFinder: desktopFinder{id: id}}
	r := newRig(t, func(_ *config.Config, o *Options) { o.Remote = hub; o.Discovery = f })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { hub.Serve(w, req, "m1", "pc") }))
	t.Cleanup(srv.Close)
	there := t.TempDir()
	desktop := filepath.Join(there, "desktop")
	os.MkdirAll(desktop, 0o700)
	put := func(name, s string) {
		if err := os.WriteFile(filepath.Join(desktop, name), []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	lines := func(name string) []string {
		b, _ := os.ReadFile(filepath.Join(desktop, name))
		if s := strings.TrimSpace(string(b)); s != "" {
			return strings.Split(s, "\n")
		}
		return nil
	}
	put("open.json", `["`+id+`"]`)
	put("sessions.json", `[{"id":"`+id+`","cwd":"/work/ir","title":"Rewrite","last":1700000000000}]`)
	put("models.json", `{"default":"p/m","models":[{"value":"p/m","name":"Model M","efforts":["low","high"]},{"value":"p/n","name":"Model N"}]}`)
	put("workspaces.json", `{"workspaces":[{"id":"ws1","title":"IR","path":"/work/ir","sessions":["`+id+`"]}]}`)
	agents, _ := json.Marshal([]wire.AgentInfo{{Agent: "dsh", Argv: []string{"@dsh-desktop"}, Version: "0.2.2-rc.5", Home: filepath.Join(there, ".dsh")}})
	cmd := exec.Command("node", "../../plugins/dsh-remote-iridium/test/run.mjs", "ws"+strings.TrimPrefix(srv.URL, "http"), "tok", filepath.Join(there, "executor"))
	cmd.Env = append(os.Environ(), "IRIDIUM_TEST_AGENTS="+string(agents), "IRIDIUM_TEST_DSH="+desktop, `IRIDIUM_TEST_INPROC=["/bin/true"]`)
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Signal(syscall.SIGTERM); _ = cmd.Wait() })
	waitFor(t, "the executor", func() bool { _, ok := hub.Machine("m1").Hello(); return ok })
	if mc := hub.Machine("m1"); !mc.Has(hdsh.CapInproc) || !mc.Has(wire.CapAttach) {
		t.Fatalf("caps: %v", mc.Caps())
	}

	ch := r.m.o.Channels[api.AgentDsh].(*hdsh.Channel)
	cat, err := ch.Catalog(bg, "m1")
	if err != nil || cat.Default != "p/m" || len(cat.Models) != 2 {
		t.Fatalf("desktop.models: cat=%+v err=%v", cat, err)
	}
	ws, err := ch.Workspaces(bg, "m1")
	if err != nil || len(ws) != 1 || ws[0].Path != "/work/ir" {
		t.Fatalf("desktop.workspaces: ws=%+v err=%v", ws, err)
	}
	found, err := ch.SearchSessions(bg, "m1", "Rew", 10)
	if err != nil || len(found) != 1 || found[0].ID != id {
		t.Fatalf("desktop.sessions: found=%+v err=%v", found, err)
	}

	// A plugin there attaches a key to it (the service iridium): shown, in both languages.
	put("attach.json", `{"`+id+`":{"effort":{"zh":"<button id=\"x.k\" pressed>键</button>","en":"<button id=\"x.k\" pressed>Key</button>"}}}`)
	st := r.waitState(id, "attached", func(s *api.SessionState) bool { return len(s.Attached) == 1 })
	if a := st.Attached[0]; a.From != "m:m1" || a.Place != "effort" || a.Content["zh"] != `<button id="x.k" pressed>键</button>` {
		t.Fatalf("attached: %+v", a)
	}
	// Pressed: back to the plugin there, by its own id.
	if _, err := r.m.Press(bg, id, "m:m1", "x.k", PressUser{ID: "local", Name: "本机"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the press there", func() bool {
		b := lines("buttons.jsonl")
		return len(b) == 1 && strings.Contains(b[0], `"session":"`+id+`"`) && strings.Contains(b[0], `"id":"x.k"`)
	})
	// The executor gone, what it attached goes; back, it attaches it again.
	_ = cmd.Process.Signal(syscall.SIGUSR1)
	r.waitState(id, "gone with it", func(s *api.SessionState) bool { return len(s.Attached) == 0 })
	r.waitState(id, "again", func(s *api.SessionState) bool { return len(s.Attached) == 1 })
	// Taken away there.
	put("attach.json", `{}`)
	r.waitState(id, "taken away", func(s *api.SessionState) bool { return len(s.Attached) == 0 })
}
