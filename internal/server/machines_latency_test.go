package server

import (
	"testing"
	"time"
)

func TestLatencyMoved(t *testing.T) {
	ms := time.Millisecond
	for _, c := range []struct {
		was, now time.Duration
		want     bool
	}{
		{40 * ms, 45 * ms, false},   // under the step
		{40 * ms, 70 * ms, true},    // by a quarter and the step
		{200 * ms, 230 * ms, false}, // the step, not a quarter
		{290 * ms, 310 * ms, true},  // crossing SlowRTT
		{0, 40 * ms, true},          // the first one
	} {
		if got := latencyMoved(c.was, c.now); got != c.want {
			t.Errorf("latencyMoved(%v, %v) = %v, want %v", c.was, c.now, got, c.want)
		}
	}
}
