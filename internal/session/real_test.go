package session

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/config"
)

// TestRealAgent runs one short turn on a real agent through the holder, the driver and the
// transcript, then stops it. It costs quota, so it runs only with IRIDIUM_REAL=<agent>:<model>,
// the cheapest model (AGENTS.md), e.g. pi:opencode-go/deepseek-v4.1-flash:off or
// codex:gpt-6-luna:low. pi's session goes into a temp dir (PI_CODING_AGENT_SESSION_DIR); Codex's
// rollout into its own home, as any of its threads.
func TestRealAgent(t *testing.T) {
	spec := os.Getenv("IRIDIUM_REAL")
	if spec == "" {
		t.Skip("set IRIDIUM_REAL=<agent>:<model> to run a real agent")
	}
	name, model, _ := strings.Cut(spec, ":")
	a := api.Agent(name)
	mode := map[api.Agent]string{api.AgentPi: "auto", api.AgentCodex: "never"}[a]
	program, err := exec.LookPath(name)
	if err != nil {
		t.Fatal(err)
	}
	if a == api.AgentPi {
		t.Setenv("PI_CODING_AGENT_SESSION_DIR", t.TempDir())
	}
	r := newRig(t, func(c *config.Config, o *Options) { c.Programs[name] = program })
	id, err := r.m.Create(context.Background(), api.NewSession{Agent: a, Cwd: r.cwd, Text: "reply with the word ok", Model: model, Mode: mode}, "网页")
	if err != nil {
		t.Fatal(err)
	}
	var s *api.SessionState
	for deadline := time.Now().Add(3 * time.Minute); ; time.Sleep(100 * time.Millisecond) {
		s, _ = r.m.State(id)
		if s.Status == api.StatusIdle && s.Last != nil && s.Last.Role == "assistant" {
			break
		}
		if time.Now().After(deadline) || s.Status == api.StatusError {
			t.Fatalf("no reply: %+v", s)
		}
	}
	t.Logf("state: status %s model %s mode %s live %v title %q last %+v", s.Status, s.Model, s.Mode, s.Live, s.Title, *s.Last)
	for _, b := range r.messages(id) {
		t.Logf("block %s %s from=%q id=%q text=%q", b.At, b.Kind, b.From, b.ID, b.Text)
	}
	waitFor(t, "the usage row", func() bool { u, _ := r.store.UsageSince(0); return len(u) > 0 })
	u, _ := r.store.UsageSince(0)
	for _, row := range u {
		b, _ := json.Marshal(row)
		t.Logf("usage %s", b)
	}
	time.Sleep(2 * time.Second) // the context and quota read after the turn
	t.Logf("context %s", r.hub.Get("context:"+id))
	t.Logf("quota %s", r.hub.Get("quota"))
	if b, err := os.ReadFile(r.cfg.StatePath() + "/hold/" + id + "/session.json"); err == nil {
		t.Logf("session.json %s", b)
	}
	if err := r.m.Stop(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		if s, _ = r.m.State(id); !s.Live || time.Now().After(deadline) {
			break
		}
	}
	if s.Live || s.Error != "" {
		t.Errorf("after the stop: %+v", s)
	}
	if !strings.EqualFold(strings.Trim(s.Last.Text, " .!\n"), "ok") {
		t.Errorf("the reply: %q", s.Last.Text)
	}
}
