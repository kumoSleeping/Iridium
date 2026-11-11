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
	"github.com/kumoSleeping/iridium/internal/remote"
	"github.com/kumoSleeping/iridium/internal/remote/wire"
)

// A dsh session DSH Desktop holds, through the real plugin (test/run.mjs: its fake dsh): its
// effort and model are set in Desktop (dsh's session controller); what a plugin there attaches to
// it is shown with it, a button pressed goes back to that plugin (session.button), and what it
// takes away goes; the executor gone, all it attached goes, and comes again with it.
func TestHeldPlugin(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("no node")
	}
	const id = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	hub := &remote.Hub{Logf: t.Logf}
	f := &heldFinder{desktopFinder: desktopFinder{id}, held: true}
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
	put("models.json", `{"`+id+`":{"provider":"p","model":"m","reasoningEffort":"high"}}`)
	put("efforts.json", `{"p/m":["low","high","xhigh","max"],"p/n":["high"]}`)
	agents, _ := json.Marshal([]wire.AgentInfo{{Agent: "dsh", Argv: []string{"dsh"}, Version: "0.2.2-rc.1", Home: filepath.Join(there, ".dsh")}})
	cmd := exec.Command("node", "../../plugins/dsh-remote-iridium/test/run.mjs", "ws"+strings.TrimPrefix(srv.URL, "http"), "tok", filepath.Join(there, "executor"))
	cmd.Env = append(os.Environ(), "IRIDIUM_TEST_AGENTS="+string(agents), "IRIDIUM_TEST_DSH="+desktop)
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Signal(syscall.SIGTERM); _ = cmd.Wait() })
	waitFor(t, "the executor", func() bool { _, ok := hub.Machine("m1").Hello(); return ok })
	if mc := hub.Machine("m1"); !mc.Has(wire.CapHeld) || !mc.Has(wire.CapAttach) || !mc.Has(wire.CapPrepare) {
		t.Fatalf("caps: %v", mc.Caps())
	}

	st := r.waitState(id, "the holder's state", func(s *api.SessionState) bool { return s.EffortNow != "" })
	if st.EffortNow != "high" || st.Effort != "high" || st.Model != "p/m" || len(st.Attached) != 0 {
		t.Fatalf("effort %q/%q model %q attached %+v", st.Effort, st.EffortNow, st.Model, st.Attached)
	}
	// Set in Desktop, by dsh's session controller: the level on the model; a model without that
	// level, none.
	if err := r.m.SetEffort(bg, id, "low"); err != nil {
		t.Fatal(err)
	}
	if err := r.m.SetModel(bg, id, "p/n"); err != nil {
		t.Fatal(err)
	}
	sel := lines("selects.jsonl")
	if len(sel) != 2 || sel[0] != `{"sessionId":"`+id+`","provider":"p","model":"m","reasoningEffort":"low"}` ||
		sel[1] != `{"sessionId":"`+id+`","provider":"p","model":"n"}` {
		t.Fatalf("selected: %q", sel)
	}
	if _, err := r.m.get(id); err == nil {
		t.Error("taken up here")
	}

	// A plugin there attaches a key to it (the service iridium): shown, in both languages.
	put("attach.json", `{"`+id+`":{"effort":{"zh":"<button id=\"x.k\" pressed>键</button>","en":"<button id=\"x.k\" pressed>Key</button>"}}}`)
	st = r.waitState(id, "attached", func(s *api.SessionState) bool { return len(s.Attached) == 1 })
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
