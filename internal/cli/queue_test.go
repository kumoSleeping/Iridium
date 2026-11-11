package cli

import (
	"io"
	"strings"
	"testing"
	"time"
)

// While a turn runs, a message (ir api message.create) joins it; _data.steer:=false waits (and
// --wait waits for its own turn).
func TestSendQueueAndSteer(t *testing.T) {
	_, work := rig(t)
	r := ir("api", "POST", "internal/sessions", "agent=claude", "cwd="+work, "text=one WAIT")
	if r.code != 0 {
		t.Fatalf("new: %+v", r)
	}
	sid := strings.TrimSpace(r.out)
	send := func(text string, more ...string) result {
		return ir(append([]string{"api", "message.create", "channel_id=" + sid, "_data.text=" + text}, more...)...)
	}
	if r := send("two", "--wait"); r.code != 0 || r.out != "ok: two\n" {
		t.Errorf("steer: %+v", r)
	}
	send("three WAIT")
	time.Sleep(100 * time.Millisecond)
	// A person's _data.steer:=false waits for the turn to end.
	for _, k := range []string{"IRIDIUM_SESSION", "CLAUDE_CODE_SESSION_ID", "CODEX_THREAD_ID", "CODEX_SESSION_ID", "PI_SESSION_ID"} {
		t.Setenv(k, "")
	}
	if r := send("four", "_data.steer:=false", "--wait"); r.code != 0 || r.out != "ok: four\n" {
		t.Errorf("queued: %+v", r)
	}
	if r := ir("api", "message.list", "channel_id="+sid); !strings.Contains(r.out, "ok: three WAIT") {
		t.Errorf("message.list: %+v", r)
	}
	if r := send("five", "_data.steer:=true", "--wait"); r.code != 0 || r.out != "ok: five\n" {
		t.Errorf("steer still taken: %+v", r)
	}
	// An agent's message joins the turn all the same: queued, it would wait for a person to press
	// it in (the user, 2026-11-06).
	send("seven WAIT")
	time.Sleep(100 * time.Millisecond)
	t.Setenv("IRIDIUM_SESSION", "caller-session")
	if r := send("eight", "_data.steer:=false"); r.code != 0 {
		t.Errorf("an agent's steer false: %+v", r)
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
