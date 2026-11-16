package session

import (
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/store"
)

// A session that names no model, not running: the model it last ran (its usage) is its model
// (its name, its model's face), not nothing until it runs again; one that names one keeps it.
func TestIdleRanModel(t *testing.T) {
	t.Parallel()
	r := newRig(t, nil)
	const a, b = "aaaaaaaa-1111-4aaa-8aaa-aaaaaaaaaaaa", "bbbbbbbb-1111-4bbb-8bbb-bbbbbbbbbbbb"
	for _, id := range []string{a, b} {
		if err := r.m.o.Store.AddUsage(store.Usage{Session: id, Agent: "claude", Model: "claude-opus-5-5", CacheRead: 100}); err != nil {
			t.Fatal(err)
		}
	}
	if h := r.m.newHosted(meta{ID: a, Agent: api.AgentClaude, Cwd: r.cwd, Created: time.Now().UnixMilli()}); h.model != "claude-opus-5-5" || h.meta.Model != "" {
		t.Errorf("none named: model %q, pick %q", h.model, h.meta.Model)
	}
	if h := r.m.newHosted(meta{ID: b, Agent: api.AgentClaude, Cwd: r.cwd, Created: time.Now().UnixMilli(), Model: "haiku"}); h.model != "haiku" {
		t.Errorf("one named: model %q", h.model)
	}
}
