package dsh

import (
	"errors"
	"fmt"
	"testing"
)

// A session another dsh writes (DSH Desktop has it open): told apart from other refusals.
func TestHeldElsewhere(t *testing.T) {
	held := &rpcError{Code: -32000, Message: `session "s1" is already owned by an active write handle`, Data: []byte(`{"code":"session/writer-held","sessionId":"s1"}`)}
	if !heldElsewhere(fmt.Errorf("resume: %w", held)) {
		t.Error("writer-held not told")
	}
	if heldElsewhere(&rpcError{Code: -32000, Message: "session not found"}) || heldElsewhere(errors.New("already owned")) {
		t.Error("another error taken for writer-held")
	}
}
