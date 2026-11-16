package session

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/hold"
	"github.com/kumoSleeping/iridium/internal/transcript"
)

// agy keeps no record Iridium can read: Iridium keeps it, from the driver, in the session's hold
// directory. A stop and a new message resume agy's session (no second one opened); a new daemon
// taken up writes nothing twice. Not in parallel: its daemon restart beside the others' turns
// made their timing flaky.
func TestAgyRecord(t *testing.T) {
	r := newRig(t, nil)
	id, err := r.m.Create(context.Background(), api.NewSession{Agent: api.AgentAgy, Cwd: r.cwd, Text: "hello"}, "网页")
	if err != nil {
		t.Fatal(err)
	}
	s := r.waitState(id, "the reply", func(s *api.SessionState) bool {
		return s.Status == api.StatusIdle && s.Last != nil && s.Last.Text == "ok"
	})
	if !s.Live || s.Model != "fake-flash" || s.Mode != "auto" || s.Title != "hello" {
		t.Errorf("state: %+v", s)
	}
	b := r.messages(id)
	if len(b) != 2 || b[0].Kind != api.BlockKindUser || b[0].Text != "hello" || b[0].From != "网页" || b[1].Text != "ok" {
		t.Fatalf("blocks: %+v", b)
	}
	dir := filepath.Join(r.cfg.StatePath(), "hold", id)
	if _, err := os.Stat(filepath.Join(dir, transcript.OwnFile)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the usage row", func() bool { u, _ := r.store.UsageSince(0); return len(u) == 1 })

	if err := r.m.Stop(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	r.waitState(id, "the stop", func(s *api.SessionState) bool { return !s.Live })
	if _, err := r.m.Send(context.Background(), id, "two", "网页"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the second reply", func() bool { return len(r.messages(id)) >= 4 })
	in, _ := os.ReadFile(filepath.Join(dir, hold.FileInLog))
	if n := strings.Count(string(in), `"method":"session/new"`); n != 1 || !strings.Contains(string(in), `"method":"session/resume"`) {
		t.Errorf("session/new %d times; in.log %s", n, in)
	}
	if b := r.messages(id); len(b) != 4 {
		out, _ := os.ReadFile(filepath.Join(dir, hold.FileOutLog))
		rec, _ := os.ReadFile(filepath.Join(dir, transcript.OwnFile))
		t.Fatalf("blocks after resume: %+v\nin %s\nout %s\nrecord %s", b, in, out, rec)
	}

	r.m.Close() // the daemon goes; the holder and agy stay
	time.Sleep(300 * time.Millisecond)
	r.start(r.options())
	r.waitState(id, "the session taken up", func(s *api.SessionState) bool { return s.Live && s.Model == "fake-flash" })
	if _, err := r.m.Send(context.Background(), id, "three", "网页"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the third reply", func() bool { return len(r.messages(id)) >= 6 })
	time.Sleep(100 * time.Millisecond)
	if b := r.messages(id); len(b) != 6 || b[4].Text != "three" {
		t.Errorf("blocks after the restart: %+v", b)
	}
}
