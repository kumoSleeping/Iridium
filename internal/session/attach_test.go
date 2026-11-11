package session

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kumoSleeping/iridium/internal/agent"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/config"
	"github.com/kumoSleeping/iridium/internal/harness"
	"github.com/kumoSleeping/iridium/internal/remote"
	"github.com/kumoSleeping/iridium/internal/remote/wire"
)

// An executor's plugin attaches to a session at its machine (session.attach): shown with the
// session, replaced, taken away; a press of a button in it goes back to it (session.button, the
// agent's own id), and what it attached goes when it does. One at another machine's session, or a
// place of no shape, is let be; an app's is the caller's to tell.
func TestAttach(t *testing.T) {
	t.Parallel()
	const id = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	hub := &remote.Hub{Logf: t.Logf}
	r := newRig(t, func(_ *config.Config, o *Options) {
		o.Remote = hub
		o.Discovery = &heldFinder{desktopFinder: desktopFinder{id}}
	})
	x := startHolder(t, hub, []string{wire.CapAttach}, wire.HeldLive{})
	attach := func(session, place string, content any) {
		t.Helper()
		b, _ := json.Marshal(content)
		if err := x.conn.Notify(bg, "session.attach", wire.AttachParams{Session: session, Place: place, Content: b}); err != nil {
			t.Fatal(err)
		}
	}
	attach(id, "effort", map[string]string{"zh": `<button id="x.on">开</button>`, "en": `<button id="x.on">On</button>`})
	st := r.waitState(id, "attached", func(s *api.SessionState) bool { return len(s.Attached) == 1 })
	if a := st.Attached[0]; a.From != "m:m1" || a.Place != "effort" || a.Content["en"] != `<button id="x.on">On</button>` {
		t.Fatalf("attached: %+v", a)
	}
	attach(id, "effort", `<button id="x.on" pressed>On</button>`)
	r.waitState(id, "replaced", func(s *api.SessionState) bool {
		return len(s.Attached) == 1 && s.Attached[0].Content[""] == `<button id="x.on" pressed>On</button>`
	})
	attach("eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee", "effort", "<b>x</b>") // not a session here
	attach(id, "Bad Place", "<b>x</b>")

	if _, err := r.m.Press(bg, id, FromMachine("m1"), "x.on", PressUser{ID: "local", Name: "本机"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "session.button", func() bool {
		ev := x.told()
		return len(ev) == 1 && strings.HasPrefix(ev[0], "session.button ") && strings.Contains(ev[0], `"session":"`+id+`","id":"x.on","user":{"id":"local","name":"本机"}`)
	})
	if app, err := r.m.Press(bg, id, FromApp("bot"), "y", PressUser{}); err != nil || app != "bot" {
		t.Errorf("an app's: %q %v", app, err)
	}
	if _, err := r.m.Press(bg, id, "z", "y", PressUser{}); !errors.Is(err, ErrBadRequest) {
		t.Errorf("from nowhere: %v", err)
	}

	// An app's beside it; limits.
	if err := r.m.Attach(id, FromApp("bot"), "menu", map[string]string{"": "<b>bot</b>"}); err != nil {
		t.Fatal(err)
	}
	if err := r.m.Attach(id, FromApp("bot"), "menu", map[string]string{"": strings.Repeat("x", maxAttachText+1)}); !errors.Is(err, ErrBadRequest) {
		t.Errorf("too long: %v", err)
	}
	for _, p := range []string{"a", "b", "c"} {
		if err := r.m.Attach(id, FromApp("bot"), p, map[string]string{"": "x"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.m.Attach(id, FromApp("bot"), "d", map[string]string{"": "x"}); !errors.Is(err, ErrBadRequest) {
		t.Errorf("a fifth: %v", err)
	}
	if st, _ = r.m.State(id); len(st.Attached) != 5 {
		t.Fatalf("attached: %+v", st.Attached)
	}

	// Empty takes it away; the executor gone, its own go.
	attach(id, "effort", nil)
	r.waitState(id, "taken away", func(s *api.SessionState) bool { return len(s.Attached) == 4 })
	attach(id, "effort", "<b>again</b>")
	r.waitState(id, "again", func(s *api.SessionState) bool { return len(s.Attached) == 5 })
	x.conn.Close()
	st = r.waitState(id, "gone with it", func(s *api.SessionState) bool { return len(s.Attached) == 4 })
	for _, a := range st.Attached {
		if a.From != "a:bot" {
			t.Errorf("left: %+v", a)
		}
	}
}

func TestAttachContent(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		`null`:               "map[]",
		`""`:                 "map[]",
		`"<b/>"`:             "map[:<b/>]",
		`{"zh":"a","en":""}`: "map[zh:a]",
		`{"zh":"","en":""}`:  "map[]",
	} {
		got, err := AttachContent(json.RawMessage(in))
		if err != nil {
			t.Fatal(err)
		}
		if g := strings.ReplaceAll(strings.TrimSpace(fmtMap(got)), " ", ""); g != strings.ReplaceAll(want, " ", "") {
			t.Errorf("%s: %s", in, g)
		}
	}
	if _, err := AttachContent(json.RawMessage(`[1]`)); !errors.Is(err, ErrBadRequest) {
		t.Errorf("an array: %v", err)
	}
}

func fmtMap(m map[string]string) string {
	if m == nil {
		return "map[]"
	}
	b := []string{}
	for k, v := range m {
		b = append(b, k+":"+v)
	}
	return "map[" + strings.Join(b, " ") + "]"
}

// DSH Desktop's plugin on this computer (its hello's machine is this one's): a dsh launched here
// takes its presets, its patches and its own dsh; a patch others may write is not taken.
func TestLocalHost(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	good, bad := filepath.Join(dir, "good.yml"), filepath.Join(dir, "bad.yml")
	os.WriteFile(good, []byte("[]\n"), 0o600)
	os.WriteFile(bad, []byte("[]\n"), 0o600)
	os.Chmod(bad, 0o666)
	hub := &remote.Hub{Logf: t.Logf, Self: "this-pc"}
	r := newRig(t, func(_ *config.Config, o *Options) { o.Remote = hub })
	startHolderAs(t, hub, []string{wire.CapPrepare}, wire.HeldLive{}, "this-pc", []string{good})
	if mc := hub.Machine("m1"); !mc.IsLocal() || len(mc.Caps()) == 0 {
		t.Fatalf("not local: %v", mc.Caps())
	}
	if _, ok := hub.Machine("m1").Agent("dsh"); ok {
		t.Error("its agents taken as a machine's")
	}
	launch := agent.SpawnOptions{Session: "x", Cwd: dir}
	ch := r.m.o.Channels[api.AgentDsh].(harness.Launcher)
	if err := ch.Prepare(bg, harness.Ref{ID: "x", As: "x", Agent: api.AgentDsh}, &launch); err != nil {
		t.Fatal(err)
	}
	h := launch.Host
	if h == nil || strings.Join(h.Argv, " ") != "/desktop/node /desktop/dsh/lib/bin.js" || h.Scope != "/desktop/scope" {
		t.Fatalf("host: %+v", h)
	}
	if l, def, err := h.Presets(); err != nil || def != "omd-ptc" || len(l) != 1 {
		t.Errorf("presets: %+v %q %v", l, def, err)
	}
	if p, in, err := h.Prepare("omd-ptc", "sys"); err != nil || !in || len(p) != 1 || p[0] != good {
		t.Errorf("prepare: %q %v %v", p, in, err)
	}
	x := startHolderAs(t, hub, []string{wire.CapPrepare}, wire.HeldLive{}, "this-pc", []string{bad})
	_ = x
	launch = agent.SpawnOptions{Session: "y", Cwd: dir}
	if err := ch.Prepare(bg, harness.Ref{ID: "y", As: "y", Agent: api.AgentDsh}, &launch); err != nil {
		t.Fatal(err)
	}
	if _, _, err := launch.Host.Prepare("omd-ptc", ""); err == nil || !strings.Contains(err.Error(), "not this user's own file") {
		t.Errorf("a patch others may write: %v", err)
	}
}
