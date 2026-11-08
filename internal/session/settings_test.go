package session

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/config"
)

// dsh's preset is picked before the first message: a started session keeps its tools (dsh adds a
// new set only as deferred tools, which the API turns down when all are).
func TestPresetBeforeTheFirstMessage(t *testing.T) {
	// The presets of a dsh package (the fixture's), not of whatever dsh this machine has.
	bin, err := filepath.Abs("../agent/dsh/testdata/pkg/lib/bin.js")
	if err != nil {
		t.Fatal(err)
	}
	r := newRig(t, func(c *config.Config, _ *Options) { c.Programs["dsh"] = bin })
	const id = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	h := r.m.newHosted(meta{ID: id, Agent: api.AgentDsh, Cwd: r.cwd, Created: time.Now().UnixMilli()})
	r.m.mu.Lock()
	r.m.hosted[id] = h
	r.m.mu.Unlock()
	if err := r.m.SetPreset(bg, id, "ptc"); err != nil {
		t.Fatalf("blank session: %v", err)
	}
	if st, _ := r.store.Settings(id); st.Preset != "ptc" {
		t.Errorf("stored: %+v", st)
	}
	h.mu.Lock()
	h.said.last = &api.LastSaid{Role: "user", Text: "hi"}
	h.mu.Unlock()
	if err := r.m.SetPreset(bg, id, "standard"); !errors.Is(err, ErrBadRequest) {
		t.Errorf("begun session: %v", err)
	}
	if st, _ := r.store.Settings(id); st.Preset != "ptc" {
		t.Errorf("kept: %+v", st)
	}
}

// A restart for a setting (dsh's effort, model, preset) is not a run: the session shows idle
// while its agent starts again, not starting.
func TestQuietRestartStatus(t *testing.T) {
	r := newRig(t, nil)
	h := r.m.newHosted(meta{ID: "cccccccc-cccc-4ccc-8ccc-cccccccccccc", Agent: api.AgentDsh, Cwd: r.cwd, Created: time.Now().UnixMilli()})
	h.starting = true
	if s := h.status(); s != api.StatusStarting {
		t.Errorf("a start: %s", s)
	}
	h.quiet = true
	if s := h.status(); s != api.StatusIdle {
		t.Errorf("a setting's restart: %s", s)
	}
}
