package session

import (
	"encoding/json"
	"errors"
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

// A dsh session DSH Desktop holds, through the real plugin (test/run.mjs: its fake dsh and OMD):
// without OMD there, no features, but its effort and model are set in Desktop (dsh's session
// controller); with OMD, its Ultra is a feature, switched on (at the level OMD picks: none sent),
// a sub-mode picked (Ultra's level kept), a level set while on (through OMD), off; switched in
// Desktop, it is shown again (held.changed); one Desktop does not hold is shown, with the plugin's
// reason, and not switched.
func TestHeldPlugin(t *testing.T) {
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
	if mc := hub.Machine("m1"); !mc.Has(wire.CapHeld) || !mc.Has(wire.CapHeldFeatures) {
		t.Fatalf("caps: %v", mc.Caps())
	}

	// No OMD there: the model and the level Desktop runs at, no features.
	st := r.waitState(id, "the holder's state", func(s *api.SessionState) bool { return s.EffortNow != "" })
	if st.EffortNow != "high" || st.Effort != "high" || st.Model != "p/m" || len(st.Features) != 0 {
		t.Fatalf("no OMD: effort %q/%q model %q features %+v", st.Effort, st.EffortNow, st.Model, st.Features)
	}
	if err := r.m.SetFeature(bg, id, "omd.ultra", api.SetFeature{On: true}); !errors.Is(err, ErrBadRequest) {
		t.Errorf("a feature not there: %v", err)
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
	if st, _ = r.m.State(id); st.Model != "p/n" {
		t.Errorf("after the model: model %q", st.Model)
	}

	// OMD there (its adapter registered as it comes): Ultra, off, at the model's level.
	os.Remove(filepath.Join(desktop, "models.json"))
	put("omd.json", `{"`+id+`":{"eligible":true,"mode":"off","submode":"free","teamAvailable":false,"selected":{"provider":"p","model":"m","reasoningEffort":"high"}}}`)
	st = r.waitState(id, "OMD's Ultra", func(s *api.SessionState) bool { return len(s.Features) > 0 && s.EffortNow == "high" })
	u := st.Features[0]
	var opts []string
	for _, o := range u.Options {
		opts = append(opts, o.ID+"="+o.Name)
	}
	if len(st.Features) != 1 || u.ID != "omd.ultra" || u.Name != "Ultra" || u.On || u.Active || !u.Available || u.Option != "free" ||
		u.Place != "effort" || u.Tone != "warm" || u.Hint != "OMD 的协作模式，在 DSH Desktop 里运行；子模式在下面选" ||
		strings.Join(opts, " ") != "workflow=Workflow free=自由" || st.Model != "p/m" {
		t.Fatalf("off: %+v model %q", st.Features, st.Model)
	}
	// Only a feature it has, at an option of its: nothing asked of OMD otherwise.
	if err := r.m.SetFeature(bg, id, "omd.other", api.SetFeature{On: true}); !errors.Is(err, ErrBadRequest) {
		t.Errorf("an unknown feature: %v", err)
	}
	if err := r.m.SetFeature(bg, id, "omd.ultra", api.SetFeature{On: true, Option: "team"}); !errors.Is(err, ErrBadRequest) {
		t.Errorf("team, not available: %v", err)
	}
	if got := lines("modes.jsonl"); len(got) != 0 {
		t.Fatalf("asked of OMD: %q", got)
	}

	ultra := func(s api.SessionState) api.Feature {
		for _, f := range s.Features {
			if f.ID == "omd.ultra" {
				return f
			}
		}
		return api.Feature{}
	}
	steps := []struct {
		what string
		set  func() error
		want func(api.Feature, string) bool
	}{
		{"on", func() error { return r.m.SetFeature(bg, id, "omd.ultra", api.SetFeature{On: true}) },
			func(u api.Feature, e string) bool { return u.On && u.Active && e == "xhigh" }},
		{"a sub-mode", func() error { return r.m.SetFeature(bg, id, "omd.ultra", api.SetFeature{On: true, Option: "workflow"}) },
			func(u api.Feature, e string) bool { return u.On && u.Option == "workflow" && e == "xhigh" }},
		{"a level while on", func() error { return r.m.SetEffort(bg, id, "max") },
			func(u api.Feature, e string) bool { return u.On && e == "max" }},
		{"off", func() error { return r.m.SetFeature(bg, id, "omd.ultra", api.SetFeature{On: false}) },
			func(u api.Feature, e string) bool { return !u.On && !u.Active && u.Option == "workflow" && e == "max" }},
		{"a level while off", func() error { return r.m.SetEffort(bg, id, "low") },
			func(u api.Feature, e string) bool { return !u.On && e == "low" }},
	}
	for _, s := range steps {
		if err := s.set(); err != nil {
			t.Errorf("%s: %v", s.what, err)
			continue
		}
		st, _ := r.m.State(id)
		if !s.want(ultra(*st), st.EffortNow) {
			t.Errorf("%s: %+v effortNow %q", s.what, ultra(*st), st.EffortNow)
		}
	}
	// What OMD was asked: on with no level (its own pick), the sub-mode at Ultra's, the level
	// through it, off at the track's; each on the session's model. The level while off went by
	// dsh's own selection.
	modes := lines("modes.jsonl")
	var asked []map[string]any
	for _, l := range modes {
		var x map[string]any
		json.Unmarshal([]byte(l), &x)
		asked = append(asked, x)
	}
	if len(asked) != 4 || asked[0]["mode"] != "ultra" || asked[0]["provider"] != "p" || asked[0]["model"] != "m" ||
		asked[1]["submode"] != "workflow" || asked[1]["reasoningEffort"] != "xhigh" ||
		asked[2]["mode"] != "ultra" || asked[2]["reasoningEffort"] != "max" ||
		asked[3]["mode"] != "off" || asked[3]["reasoningEffort"] != "max" {
		t.Errorf("asked of OMD: %q", modes)
	} else if _, has := asked[0]["reasoningEffort"]; has {
		t.Errorf("Ultra on asked at a level: %s", modes[0])
	}
	if sel := lines("selects.jsonl"); len(sel) != 3 || !strings.Contains(sel[2], `"reasoningEffort":"low"`) {
		t.Errorf("selected: %q", sel)
	}

	// Switched in Desktop: the plugin tells (held.changed), the session is shown again.
	put("omd.json", `{"`+id+`":{"eligible":true,"mode":"ultra","submode":"free","teamAvailable":true,"selected":{"provider":"p","model":"m","reasoningEffort":"xhigh"}}}`)
	os.Remove(filepath.Join(desktop, "models.json"))
	shown := func() api.SessionState {
		var s api.SessionState
		json.Unmarshal(r.hub.Get("session:"+id), &s)
		return s
	}
	waitFor(t, "Ultra switched in Desktop shown", func() bool {
		s := shown()
		return ultra(s).On && ultra(s).Option == "free" && len(ultra(s).Options) == 3 && s.EffortNow == "xhigh"
	})

	// Not held by Desktop: shown, not available, the plugin's reason; not switched. Still on and in
	// effect (OMD keeps the mode with the session): the tag warm, as before.
	f.setHeld(false)
	put("open.json", `[]`)
	st = r.waitState(id, "not held", func(s *api.SessionState) bool { return len(s.Features) == 1 && !s.Features[0].Available })
	if u := st.Features[0]; u.Reason != "会话不在 DSH Desktop 里打开着，或没用 OMD 的预设" || !u.On || !u.Active || st.EffortNow != "" {
		t.Errorf("not held: %+v effortNow %q", st.Features, st.EffortNow)
	}
	n := len(lines("modes.jsonl"))
	if err := r.m.SetFeature(bg, id, "omd.ultra", api.SetFeature{On: false}); !errors.Is(err, ErrBadRequest) || !strings.Contains(err.Error(), "会话不在 DSH Desktop 里打开着") {
		t.Errorf("set, not held: %v", err)
	}
	if len(lines("modes.jsonl")) != n {
		t.Error("asked of OMD, not held")
	}
}
