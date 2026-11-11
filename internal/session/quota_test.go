package session

import (
	"testing"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/config"
	"github.com/kumoSleeping/iridium/internal/hub"
)

// A Codex session here and one at another machine report no account, and the other machine's
// login may be another one: their windows are kept apart, each session's its own, not the last
// written for both.
func TestQuotaByMachine(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.StateDir = t.TempDir()
	m := &Manager{o: Options{Config: cfg, Hub: hub.New(), Logf: t.Logf}, hosted: map[string]*Hosted{}, quota: map[string]api.AgentQuota{}}
	here := &Hosted{m: m, id: "here", meta: meta{ID: "here", Agent: api.AgentCodex}, live: true}
	there := &Hosted{m: m, id: "there", meta: meta{ID: "there", Agent: api.AgentCodex, Machine: "m1"}, live: true}
	m.hosted["here"], m.hosted["there"] = here, there
	here.setQuota([]api.QuotaWindow{{Group: "session", Percent: 10}}, nil)
	there.setQuota([]api.QuotaWindow{{Group: "session", Percent: 90}}, nil)

	if got := m.quotaState().Agents; len(got) != 2 {
		t.Fatalf("windows %+v, want one for each machine", got)
	}
	for id, want := range map[string]float64{"here": 10, "there": 90} {
		q, err := m.QuotaOf(id)
		if err != nil || q == nil || q.Windows[0].Percent != want || !q.Live {
			t.Fatalf("%s: %+v %v, want %v%% live", id, q, err, want)
		}
	}
	if q, _ := m.QuotaOf("there"); q.Machine != "m1" {
		t.Fatalf("machine %q", q.Machine)
	}

	// Saved and read again, still apart.
	m2 := &Manager{o: m.o, hosted: map[string]*Hosted{}, quota: map[string]api.AgentQuota{}}
	m2.loadQuota()
	if len(m2.quota) != 2 {
		t.Fatalf("loaded %+v", m2.quota)
	}
}
