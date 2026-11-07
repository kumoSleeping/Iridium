package access

import (
	"testing"

	"github.com/kumoSleeping/iridium/internal/api"
)

// Every agent for every terminal, but Claude: only the owner's own ("" is Claude).
func TestClaudeOnly(t *testing.T) {
	none, local := Default(), Full()
	if HasClaude(none) || !HasClaude(local) {
		t.Fatalf("defaults: %+v %+v", none, local)
	}
	for _, a := range []api.Agent{api.AgentCodex, api.AgentPi, api.AgentDsh} {
		if Agent(none, a) != nil {
			t.Errorf("%s refused", a)
		}
	}
	for _, a := range []api.Agent{api.AgentClaude, ""} {
		if Agent(none, a) != ErrClaude || Agent(local, a) != nil {
			t.Errorf("%q: %v %v", a, Agent(none, a), Agent(local, a))
		}
	}
	got := WithClaude(none, ByFirst, 5)
	if !HasClaude(got) || got.ClaudeOwner.By != ByFirst || got.ClaudeOwner.At != 5 {
		t.Errorf("with claude: %+v", got)
	}
	if again := WithClaude(got, ByPanel, 9); again.ClaudeOwner.By != ByFirst {
		t.Errorf("how it got claude changed: %+v", again.ClaudeOwner)
	}
	if !IsDenied(ErrClaude) {
		t.Error("ErrClaude is not a denial")
	}
}
