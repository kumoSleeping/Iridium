package session

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/hold"
)

// again closes the manager, as the daemon goes (the holders and agents stay), and starts a new
// one on the same state.
func (r *rig) again() {
	r.t.Helper()
	r.m.Close()
	r.start(r.options())
}

// An ask open when the daemon goes is open after it comes back: the agent says so when it is
// asked again (Claude: initialize's pending_permission_requests), and answering its card ends
// the turn.
func TestAskAfterRestart(t *testing.T) {
	t.Parallel()
	r := newRig(t, nil)
	id, err := r.m.Create(context.Background(), api.NewSession{Cwd: r.cwd, Text: "please ASK"}, "网页")
	if err != nil {
		t.Fatal(err)
	}
	r.waitState(id, "the ask", func(s *api.SessionState) bool { return s.Status == api.StatusWaiting })
	r.again()
	// Past the replayed output and the new handshake both.
	time.Sleep(300 * time.Millisecond)
	s := r.waitState(id, "the ask again", func(s *api.SessionState) bool { return s.Live && len(s.Asks) == 1 })
	if s.Status != api.StatusWaiting || s.Asks[0].Card == nil || s.Asks[0].Card.Kind != "permission" {
		t.Fatalf("after the restart: %+v", s)
	}
	if err := r.m.AnswerCard(context.Background(), api.CardAnswer{Card: s.Asks[0].Card.ID, Option: OptionAllow}); err != nil {
		t.Fatal(err)
	}
	s = r.waitState(id, "the reply", func(s *api.SessionState) bool {
		return s.Last != nil && s.Last.Text == "ok: please ASK" && s.Status == api.StatusIdle
	})
	if len(s.Asks) != 0 {
		t.Errorf("asks after the answer: %+v", s.Asks)
	}
}

// An ask answered before the daemon went is not open after it comes back, though its request is
// in the output the new daemon reads again.
func TestAnsweredAskAfterRestart(t *testing.T) {
	t.Parallel()
	r := newRig(t, nil)
	id, _ := r.m.Create(context.Background(), api.NewSession{Cwd: r.cwd, Text: "please ASK"}, "网页")
	s := r.waitState(id, "the ask", func(s *api.SessionState) bool { return s.Status == api.StatusWaiting })
	if err := r.m.Answer(context.Background(), id, api.Answer{RequestID: s.Asks[0].RequestID, Kind: "permission", Allow: true}); err != nil {
		t.Fatal(err)
	}
	r.waitState(id, "the reply", func(s *api.SessionState) bool {
		return s.Last != nil && s.Last.Text == "ok: please ASK" && s.Status == api.StatusIdle
	})
	r.again()
	time.Sleep(300 * time.Millisecond)
	s = r.waitState(id, "taken up", func(s *api.SessionState) bool { return s.Live && s.Mode != "" })
	if len(s.Asks) != 0 || s.Status != api.StatusIdle {
		t.Fatalf("an answered ask came back: %+v", s)
	}
}

// The context and the quota as last read come back after a restart, with their age: for a
// session whose agent was stopped, and for one taken up running.
func TestContextAndQuotaAfterRestart(t *testing.T) {
	t.Parallel()
	r := newRig(t, nil)
	stopped, _ := r.m.Create(context.Background(), api.NewSession{Cwd: r.cwd, Text: "one"}, "网页")
	running, _ := r.m.Create(context.Background(), api.NewSession{Cwd: r.cwd, Text: "two"}, "网页")
	for _, id := range []string{stopped, running} {
		r.waitState(id, "the context", func(s *api.SessionState) bool {
			return s.Context != nil && s.Context.Max == 1000 && s.Status == api.StatusIdle
		})
	}
	waitFor(t, "the quota", func() bool {
		q := r.m.quotaState()
		return len(q.Agents) == 1 && len(q.Agents[0].Windows) == 1 && q.Agents[0].Live
	})
	before := r.m.quotaState().Agents[0]
	ctxAt, _ := r.m.State(stopped)
	if err := r.m.Stop(context.Background(), stopped); err != nil {
		t.Fatal(err)
	}
	r.waitState(stopped, "stopped", func(s *api.SessionState) bool { return !s.Live })
	r.waitState(running, "running", func(s *api.SessionState) bool { return s.Live })
	// The running one's agent stops meanwhile too: no process of the account, the quota not live.
	r.m.Close()
	if err := r.l.Stop(context.Background(), "iridium-test-"+running); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the agent gone", func() bool { st, _ := r.l.State(context.Background(), "iridium-test-"+running); return !st.Exists })
	r.start(r.options())

	for _, id := range []string{stopped, running} {
		s, err := r.m.State(id)
		if err != nil {
			t.Fatal(err)
		}
		if s.Live || s.Context == nil || s.Context.Used != 100 || s.Context.Max != 1000 || s.Context.At == 0 {
			t.Fatalf("%s: state's context after the restart: live %v %+v", id, s.Live, s.Context)
		}
		var c api.Context
		if err := json.Unmarshal(r.hub.Get("context:"+id), &c); err != nil || c.Used != 100 || len(c.Categories) == 0 || c.At == 0 {
			t.Fatalf("%s: context:<id> after the restart: %s", id, r.hub.Get("context:"+id))
		}
	}
	if s, _ := r.m.State(stopped); s.Context.At != ctxAt.Context.At {
		t.Errorf("the context's age is lost: %d, was %d", s.Context.At, ctxAt.Context.At)
	}
	var q api.Quota
	if err := json.Unmarshal(r.hub.Get("quota"), &q); err != nil || len(q.Agents) != 1 {
		t.Fatalf("quota after the restart: %s", r.hub.Get("quota"))
	}
	a := q.Agents[0]
	if a.Live || a.At != before.At || a.Account != "user@example.com" || len(a.Windows) != 1 || a.Windows[0].Percent != 10 {
		t.Fatalf("quota after the restart: %+v, before %+v", a, before)
	}

	// A process of the account again: the quota is live.
	if _, err := r.m.Send(context.Background(), running, "three", "网页"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the quota live", func() bool {
		var q api.Quota
		json.Unmarshal(r.hub.Get("quota"), &q)
		return len(q.Agents) == 1 && q.Agents[0].Live
	})
}

// A session taken up running keeps its context and the quota stays live.
func TestContextTakenUpRunning(t *testing.T) {
	t.Parallel()
	r := newRig(t, nil)
	id, _ := r.m.Create(context.Background(), api.NewSession{Cwd: r.cwd, Text: "one"}, "网页")
	r.waitState(id, "the context", func(s *api.SessionState) bool {
		return s.Context != nil && s.Context.Max == 1000 && s.Status == api.StatusIdle
	})
	r.again()
	s := r.waitState(id, "taken up", func(s *api.SessionState) bool { return s.Live && s.Mode != "" })
	if s.Context == nil || s.Context.Used != 100 {
		t.Fatalf("context: %+v", s.Context)
	}
	var c api.Context
	if err := json.Unmarshal(r.hub.Get("context:"+id), &c); err != nil || c.Used != 100 {
		t.Fatalf("context:<id>: %s", r.hub.Get("context:"+id))
	}
	waitFor(t, "the quota live", func() bool {
		var q api.Quota
		json.Unmarshal(r.hub.Get("quota"), &q)
		return len(q.Agents) == 1 && q.Agents[0].Live && len(q.Agents[0].Windows) == 1
	})
}

// A turn that ends while no daemon reads its output is not lost: the daemon taken up reads on
// from where the last one had read, so the turn's end counts (its usage, its context, its quota),
// not replayed as seen.
func TestTurnEndWhileDown(t *testing.T) {
	t.Parallel()
	r := newRig(t, nil)
	id, _ := r.m.Create(context.Background(), api.NewSession{Cwd: r.cwd, Text: "first"}, "网页")
	first := r.waitState(id, "the context", func(s *api.SessionState) bool { return s.Context != nil && s.Status == api.StatusIdle })
	waitFor(t, "the usage row", func() bool { u, _ := r.store.UsageSince(0); return len(u) == 1 })
	time.Sleep(20 * time.Millisecond) // a later context reads later
	if _, err := r.m.Send(context.Background(), id, "slow WAIT", "网页"); err != nil {
		t.Fatal(err)
	}
	r.waitState(id, "the turn", func(s *api.SessionState) bool { return s.Status == api.StatusRunning })
	r.m.Close()
	// The turn ends meanwhile.
	waitFor(t, "the turn's end in out.log", func() bool {
		b, _ := os.ReadFile(filepath.Join(r.cfg.StatePath(), "hold", id, hold.FileOutLog))
		return strings.Count(string(b), `"type":"result"`) == 2
	})
	r.start(r.options())
	r.waitState(id, "the reply", func(s *api.SessionState) bool {
		return s.Last != nil && s.Last.Text == "ok: slow WAIT" && s.Status == api.StatusIdle
	})
	waitFor(t, "the turn's usage", func() bool { u, _ := r.store.UsageSince(0); return len(u) == 2 })
	r.waitState(id, "the context read again", func(s *api.SessionState) bool { return s.Context != nil && s.Context.At > first.Context.At })
}
