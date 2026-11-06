package cli

import (
	"strings"
	"testing"
	"time"
)

// While a turn runs, ir send joins it (--steer says so too); --queue waits (and --wait waits for
// its own turn).
func TestSendQueueAndSteer(t *testing.T) {
	_, work := rig(t)
	r := ir("new", "claude", work, "one WAIT")
	if r.code != 0 {
		t.Fatalf("new: %+v", r)
	}
	sid := strings.TrimSpace(r.out)
	if r := ir("send", sid, "two", "--wait"); r.code != 0 || r.out != "ok: two\n" {
		t.Errorf("steer: %+v", r)
	}
	ir("send", sid, "three WAIT")
	time.Sleep(100 * time.Millisecond)
	if r := ir("send", sid, "four", "--queue", "--wait"); r.code != 0 || r.out != "ok: four\n" {
		t.Errorf("queued: %+v", r)
	}
	if r := ir("read", sid); !strings.Contains(r.out, "ok: three WAIT") {
		t.Errorf("read: %+v", r)
	}
	if r := ir("send", sid, "five", "--steer", "--wait"); r.code != 0 || r.out != "ok: five\n" {
		t.Errorf("--steer still taken: %+v", r)
	}
	if r := ir("send", sid, "six", "--steer", "--queue"); r.code != 1 {
		t.Errorf("both: %+v", r)
	}
}
