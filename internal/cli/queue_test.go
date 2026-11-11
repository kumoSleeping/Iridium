package cli

import (
	"io"
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
	// A person's --queue waits for the turn to end.
	for _, k := range []string{"IRIDIUM_SESSION", "CLAUDE_CODE_SESSION_ID", "CODEX_THREAD_ID", "CODEX_SESSION_ID", "PI_SESSION_ID"} {
		t.Setenv(k, "")
	}
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
	// An agent's --queue joins the turn all the same: queued, it would wait for a person to press
	// it in (the user, 2026-11-06).
	ir("send", sid, "seven WAIT")
	time.Sleep(100 * time.Millisecond)
	t.Setenv("IRIDIUM_SESSION", "caller-session")
	if r := ir("send", sid, "eight", "--queue"); r.code != 0 {
		t.Errorf("agent --queue: %+v", r)
	}
	var st struct {
		Queue []struct{ Text string } `json:"queue"`
	}
	e := newEnv(io.Discard, io.Discard)
	full, err := e.resolve(sid)
	if err == nil {
		err = e.do("GET", "/v1/internal/sessions/"+full, nil, &st)
	}
	if err != nil || len(st.Queue) != 0 {
		t.Errorf("agent --queue queued: %+v %v", st, err)
	}
}
