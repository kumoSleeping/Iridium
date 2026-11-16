package satori

import (
	"testing"

	"github.com/kumoSleeping/iridium/internal/api"
)

// A session goes by its harness, model and effort; agy's by its model and effort alone, as its
// models say whose they are.
func TestSessionName(t *testing.T) {
	for _, c := range []struct {
		st   api.SessionState
		want string
	}{
		{api.SessionState{Agent: api.AgentClaude, Model: "opus", Models: []api.Model{{Value: "opus", Name: "Claude Opus 5.5"}}, Effort: "high"}, "Claude Opus 5.5 High"},
		{api.SessionState{Agent: api.AgentAgy, Model: "gemini-4-hydrogen", Models: []api.Model{{Value: "gemini-4-hydrogen", Name: "Gemini 4 Hydrogen"}}, EffortNow: "medium"}, "Gemini 4 Hydrogen Medium"},
		{api.SessionState{Agent: api.AgentAgy, Model: "claude-opus-5-5", Models: []api.Model{{Value: "claude-opus-5-5", Name: "Claude Opus 5.5"}}}, "Claude Opus 5.5"},
		{api.SessionState{Agent: api.AgentAgy}, "Antigravity"},
	} {
		if got := SessionName(c.st); got != c.want {
			t.Errorf("%s %s: %q, want %q", c.st.Agent, c.st.Model, got, c.want)
		}
	}
}
