package claude

import (
	"encoding/json"
	"testing"
)

// The auto mode classifier's denial, as a string or as text blocks; its reason, or "denied".
func TestAutoDenied(t *testing.T) {
	for raw, want := range map[string]string{
		`"Permission for this action was denied by the Claude Code auto mode classifier. Reason: [Code from External]. If you have"`:           "Code from External",
		`[{"type":"text","text":"Permission for this action was denied by the Claude Code auto mode classifier. Reason: [Git Destructive]."}]`: "Git Destructive",
		`"Permission for this action was denied by the Claude Code auto mode classifier."`:                                                     "denied",
		`"command not found"`: "",
		`{"x":1}`:             "",
	} {
		if got := autoDenied(json.RawMessage(raw)); got != want {
			t.Errorf("%s: %q, want %q", raw, got, want)
		}
	}
}
