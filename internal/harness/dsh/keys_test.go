package dsh

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/remote"
	"github.com/kumoSleeping/iridium/internal/remote/wire"
)

func ultraKey(preset string) api.Attached {
	return api.Attached{
		Place:   "effort",
		Content: map[string]string{"zh": `<button id="omd.ultra">Ultra</button>`, "en": `<button id="omd.ultra">Ultra</button>`},
		Presets: []string{preset},
	}
}

func waitKeys(t *testing.T, ch *Channel, machine string, want func([]api.Attached) bool) []api.Attached {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		got := ch.StartKeys(machine)
		if want(got) {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("StartKeys(%q) = %+v", machine, got)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestStartKeys(t *testing.T) {
	hub := &remote.Hub{}
	ch := New(Options{Remote: hub, Logf: t.Logf})
	defer ch.Close()
	none := func(string, json.RawMessage) (any, error) {
		return nil, &wire.Error{Code: wire.CodeUnsupported, Message: "no"}
	}
	local := dialFakeInproc(t, hub, true, remote.LocalExecID, none)
	far := dialFakeInproc(t, hub, false, "far", none)
	ctx := context.Background()

	if err := local.Notify(ctx, eventDesktopConfig, DesktopConfigEvent{Keys: []api.Attached{ultraKey("coding")}}); err != nil {
		t.Fatal(err)
	}
	if err := far.Notify(ctx, eventDesktopConfig, DesktopConfigEvent{Keys: []api.Attached{ultraKey("omd")}}); err != nil {
		t.Fatal(err)
	}
	got := waitKeys(t, ch, "", func(k []api.Attached) bool { return len(k) == 1 })
	if got[0].Place != "effort" || got[0].Presets[0] != "coding" || got[0].Content["en"] == "" {
		t.Fatalf("this computer's keys: %+v", got)
	}
	// A copy: changing it changes nothing kept.
	got[0].Presets[0] = "x"
	got[0].Content["en"] = ""
	if again := ch.StartKeys(""); again[0].Presets[0] != "coding" || again[0].Content["en"] == "" {
		t.Fatalf("StartKeys not a copy: %+v", again)
	}
	got = waitKeys(t, ch, "far", func(k []api.Attached) bool { return len(k) == 1 })
	if got[0].Presets[0] != "omd" {
		t.Fatalf("far's keys: %+v", got)
	}
	if k := ch.StartKeys(remote.LocalExecID); k != nil {
		t.Fatalf("this computer's keys by its executor id: %+v", k)
	}

	// A config without keys drops them (OMD unloaded).
	if err := local.Notify(ctx, eventDesktopConfig, DesktopConfigEvent{DesktopPath: "/x"}); err != nil {
		t.Fatal(err)
	}
	waitKeys(t, ch, "", func(k []api.Attached) bool { return k == nil })
	if k := ch.StartKeys("far"); len(k) != 1 {
		t.Fatalf("far's keys dropped with this computer's: %+v", k)
	}

	// The executor gone drops them.
	far.Close()
	waitKeys(t, ch, "far", func(k []api.Attached) bool { return k == nil })
}

func TestDefaultPresetFromCache(t *testing.T) {
	ch := New(Options{})
	defer ch.Close()
	if d := ch.DefaultPreset(""); d != "" {
		t.Fatalf("nothing kept: %q", d)
	}
	ch.presetMu.Lock()
	ch.presets = map[string]presetList{"": {at: time.Now(), def: "coding"}, "far": {at: time.Now().Add(-time.Hour), def: "omd"}}
	ch.presetMu.Unlock()
	if d := ch.DefaultPreset(""); d != "coding" {
		t.Fatalf("DefaultPreset(\"\") = %q", d)
	}
	if d := ch.DefaultPreset("far"); d != "omd" {
		t.Fatalf("DefaultPreset(far) = %q (kept, though old)", d)
	}
	if d := ch.DefaultPreset("gone"); d != "" {
		t.Fatalf("DefaultPreset(gone) = %q", d)
	}
}
