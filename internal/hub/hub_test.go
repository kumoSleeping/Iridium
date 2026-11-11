package hub

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
)

type event struct {
	name string
	data string
}

// stream serves sub into a pipe and gives the events as they are read.
func stream(t *testing.T, s *Sub) (<-chan event, *io.PipeReader) {
	t.Helper()
	r, w := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); r.Close(); s.Close() })
	go serve(s, ctx, w, func() {})
	out := make(chan event, 1000)
	go func() {
		sc := bufio.NewScanner(r)
		var e event
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				e.name = line[len("event: "):]
			case strings.HasPrefix(line, "data: "):
				e.data = line[len("data: "):]
			case line == "" && e.name != "":
				out <- e
				e = event{}
			}
		}
	}()
	return out, r
}

func next(t *testing.T, ch <-chan event, within time.Duration) (event, bool) {
	t.Helper()
	select {
	case e := <-ch:
		return e, true
	case <-time.After(within):
		return event{}, false
	}
}

func state(t *testing.T, e event) api.StateEvent {
	t.Helper()
	var s api.StateEvent
	if e.name != "state" || json.Unmarshal([]byte(e.data), &s) != nil {
		t.Fatalf("not a state event: %+v", e)
	}
	return s
}

func TestSnapshotThenChanges(t *testing.T) {
	t.Parallel()
	h := New()
	h.Set("list", []int{1})
	h.Provide = func(key string) (any, bool) { return map[string]string{"made": key}, key == "quota" }
	ch, _ := stream(t, h.Subscribe([]string{"list", "session:x", "quota"}))
	got := map[string]string{}
	for range 3 {
		e, ok := next(t, ch, time.Second)
		if !ok {
			t.Fatal("no snapshot")
		}
		s := state(t, e)
		b, _ := json.Marshal(s.Value)
		got[s.Key] = string(b)
	}
	if got["list"] != "[1]" || got["session:x"] != "null" || got["quota"] != `{"made":"quota"}` {
		t.Fatalf("snapshot: %v", got)
	}
	// The same value again is no change; changes to states not wanted are not sent.
	h.Set("list", []int{1})
	h.Set("session:y", 1)
	if e, ok := next(t, ch, 400*time.Millisecond); ok {
		t.Fatalf("sent an unchanged state or one not wanted: %+v", e)
	}
	// Ten changes in a burst: the first at once, then only the last.
	for i := range 10 {
		h.Set("list", []int{i})
	}
	var last api.StateEvent
	n := 0
	for {
		e, ok := next(t, ch, 500*time.Millisecond)
		if !ok {
			break
		}
		last = state(t, e)
		n++
	}
	if n < 1 || n > 2 {
		t.Errorf("%d events for a burst of 10", n)
	}
	if b, _ := json.Marshal(last.Value); string(b) != "[9]" {
		t.Errorf("last value %s", b)
	}
}

// A reader that does not read gets, once it does, only the latest state, not all in between.
func TestSlowReaderSkipsStaleStates(t *testing.T) {
	t.Parallel()
	h := New()
	r, w := io.Pipe()
	s := h.Subscribe([]string{"list"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go serve(s, ctx, w, func() {})
	time.Sleep(50 * time.Millisecond) // the snapshot write now blocks: no one reads
	for i := range 200 {
		h.Set("list", i)
		if i%20 == 0 {
			time.Sleep(30 * time.Millisecond)
		}
	}
	sc := bufio.NewScanner(r)
	var values []string
	deadline := time.After(2 * time.Second)
	done := make(chan struct{})
	go func() {
		for sc.Scan() {
			if v, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
				values = append(values, v)
				if strings.Contains(v, `"value":199`) {
					close(done)
					return
				}
			}
		}
	}()
	select {
	case <-done:
	case <-deadline:
		t.Fatalf("never got the latest: %v", values)
	}
	if len(values) > 3 {
		t.Errorf("%d states sent to a slow reader: %v", len(values), values)
	}
}

func TestDraftsBatched(t *testing.T) {
	t.Parallel()
	h := New()
	ch, _ := stream(t, h.Subscribe([]string{"session:a"}))
	next(t, ch, time.Second) // the snapshot
	var want strings.Builder
	for i := range 50 {
		h.Draft("a", 1, 0, "x")
		want.WriteString("x")
		if i == 25 {
			h.Draft("b", 1, 0, "not for this stream")
		}
		time.Sleep(time.Millisecond)
	}
	h.Draft("a", 1, 1, "next")
	var text strings.Builder
	events, sawNext := 0, false
	for {
		e, ok := next(t, ch, 400*time.Millisecond)
		if !ok {
			break
		}
		var d api.DraftEvent
		if e.name != "draft" || json.Unmarshal([]byte(e.data), &d) != nil || d.Session != "a" {
			t.Fatalf("event %+v", e)
		}
		if d.N == 1 {
			sawNext = d.Text == "next"
			continue
		}
		text.WriteString(d.Text)
		events++
	}
	if text.String() != want.String() || !sawNext {
		t.Fatalf("drafts %q, next %v", text.String(), sawNext)
	}
	if events > 3 {
		t.Errorf("%d draft events for 50 deltas within ~60ms", events)
	}
}

func draftOf(t *testing.T, e event) api.DraftEvent {
	t.Helper()
	var d api.DraftEvent
	if e.name != "draft" || json.Unmarshal([]byte(e.data), &d) != nil {
		t.Fatalf("not a draft event: %+v", e)
	}
	return d
}

// A tool call being written: its draft starts with the call, its input JSON joined as it comes.
func TestDraftTool(t *testing.T) {
	h := New()
	ch, _ := stream(t, h.Subscribe([]string{"session:a"}))
	next(t, ch, time.Second) // the snapshot
	h.DraftTool("a", 1, 2, "toolu_1", "Bash", "")
	h.DraftTool("a", 1, 2, "toolu_1", "Bash", `{"command":"ir se`)
	h.DraftTool("a", 1, 2, "toolu_1", "Bash", `nd ab \"hi\""}`)
	h.Seal("a", 1, 2)
	// The pieces come in one event or several (a slow stream sends as they come): together they
	// are the call's arguments, the last one done.
	var text string
	for {
		e, ok := next(t, ch, time.Second)
		if !ok {
			t.Fatalf("no draft after %q", text)
		}
		d := draftOf(t, e)
		if d.N != 2 || d.Tool == nil || d.Tool.ID != "toolu_1" || d.Tool.Name != "Bash" {
			t.Fatalf("tool draft %+v", d)
		}
		text += d.Text
		if d.Done {
			break
		}
	}
	if text != `{"command":"ir send ab \"hi\""}` {
		t.Fatalf("tool draft text %q", text)
	}
}

// A stream that opens after a call's first piece still hears its pieces as the call's, not words.
func TestDraftToolMidway(t *testing.T) {
	h := New()
	h.DraftTool("a", 1, 2, "toolu_1", "Bash", `{"command":"ir se`)
	ch, _ := stream(t, h.Subscribe([]string{"session:a"}))
	next(t, ch, time.Second) // the snapshot
	h.DraftTool("a", 1, 2, "toolu_1", "Bash", `nd ab"}`)
	e, ok := next(t, ch, time.Second)
	if !ok {
		t.Fatal("no draft")
	}
	if d := draftOf(t, e); d.Tool == nil || d.Tool.ID != "toolu_1" || d.Text != `nd ab"}` {
		t.Fatalf("midway draft %+v", d)
	}
}

// A seal joins the draft's last piece; a later draft is another event.
func TestDraftSeal(t *testing.T) {
	h := New()
	ch, _ := stream(t, h.Subscribe([]string{"session:a"}))
	next(t, ch, time.Second) // the snapshot
	h.Draft("a", 1, 0, "x")
	h.Seal("a", 1, 0)
	h.Draft("a", 1, 1, "y")
	e, ok := next(t, ch, time.Second)
	if !ok {
		t.Fatal("no draft")
	}
	if d := draftOf(t, e); d.N != 0 || d.Text != "x" || !d.Done {
		t.Fatalf("first %+v", d)
	}
	e, ok = next(t, ch, time.Second)
	if !ok {
		t.Fatal("no second draft")
	}
	if d := draftOf(t, e); d.N != 1 || d.Text != "y" || d.Done {
		t.Fatalf("second %+v", d)
	}
	// A seal alone, its pieces sent before.
	h.Seal("a", 1, 1)
	e, ok = next(t, ch, time.Second)
	if !ok {
		t.Fatal("no seal")
	}
	if d := draftOf(t, e); d.N != 1 || d.Text != "" || !d.Done {
		t.Fatalf("seal %+v", d)
	}
}

// A state due takes the drafts pending with it, ahead of it, even within DraftEvery.
func TestDraftsBeforeState(t *testing.T) {
	t.Parallel()
	h := New()
	ch, _ := stream(t, h.Subscribe([]string{"session:a"}))
	next(t, ch, time.Second) // the snapshot
	time.Sleep(Coalesce)
	h.Draft("a", 1, 0, "one")
	if e, ok := next(t, ch, time.Second); !ok || draftOf(t, e).Text != "one" {
		t.Fatalf("first draft %+v", e)
	}
	h.Draft("a", 1, 0, " two")
	h.Set("session:a", map[string]int{"end": 1})
	e, ok := next(t, ch, time.Second)
	if !ok {
		t.Fatal("nothing")
	}
	if d := draftOf(t, e); d.Text != " two" {
		t.Fatalf("draft %+v", d)
	}
	e, ok = next(t, ch, time.Second)
	if !ok || state(t, e).Key != "session:a" {
		t.Fatalf("state %+v", e)
	}
}

// serve writes s as text, one "event:"/"data:" pair a line pair, until ctx ends or a write fails: "state" events
// (api.StateEvent) and "draft" events (api.DraftEvent), and a comment every Ping. flush pushes
// what was written to the reader.
func serve(s *Sub, ctx context.Context, w io.Writer, flush func()) error {
	return s.Run(ctx, func(event string, data []byte) error {
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data); err != nil {
			return err
		}
		return nil
	}, func() error {
		if _, err := io.WriteString(w, ": ping\n\n"); err != nil {
			return err
		}
		flush()
		return nil
	}, flush)
}
