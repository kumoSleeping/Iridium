package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/agent"
	"github.com/kumoSleeping/iridium/internal/api"
)

// The recordings in testdata are Rhodium's (test/fixtures/claude, Claude 2.1.304-2.1.310 with
// haiku): in.log is what Rhodium wrote, each line after out.log's length at the time; out.log
// is what the CLI said. A replay feeds out.log to the driver, makes the driver write each in.log
// line by the operation that writes it, and checks that it writes the same frame.

type inLine struct {
	at   int64
	line []byte
}

type harness struct {
	t      *testing.T
	d      *Driver
	mu     sync.Mutex
	events []agent.Event
	writes chan []byte
	ids    map[string]string // the recording's request ids to the driver's
	ops    sync.WaitGroup
	result map[string]any // what the operations returned, by control request subtype
	next   []byte         // the in.log line after the one acted on
}

func replay(t *testing.T, name string) *harness {
	t.Helper()
	dir := filepath.Join("testdata", name)
	ins := readIn(t, filepath.Join(dir, "in.log"))
	out, err := os.ReadFile(filepath.Join(dir, "out.log"))
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, writes: make(chan []byte, 16), ids: map[string]string{}, result: map[string]any{}}
	h.d = New(Options{Emit: h.record, IDPrefix: "ir-test-", Logf: t.Logf})
	h.d.Attach(h.write, 1)

	var off int64
	feedUntil := func(at int64) {
		for off < at && off < int64(len(out)) {
			i := bytes.IndexByte(out[off:], '\n')
			line := out[off : off+int64(i)]
			off += int64(i) + 1
			for from, to := range h.ids {
				line = bytes.ReplaceAll(line, []byte(`"request_id":"`+from+`"`), []byte(`"request_id":"`+to+`"`))
			}
			h.d.Feed(line, false)
		}
	}
	for i, in := range ins {
		feedUntil(in.at)
		h.next = nil
		if i+1 < len(ins) {
			h.next = ins[i+1].line
		}
		if i > 0 {
			h.act(in.line)
		}
		select {
		case got := <-h.writes:
			h.compare(got, in.line)
		case <-time.After(2 * time.Second):
			t.Fatalf("in.log line %d: the driver wrote nothing; want %s", i+1, in.line)
		}
	}
	feedUntil(int64(len(out)))
	h.ops.Wait()
	select {
	case extra := <-h.writes:
		t.Fatalf("an extra write: %s", extra)
	default:
	}
	return h
}

func readIn(t *testing.T, path string) []inLine {
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var ins []inLine
	for _, l := range bytes.Split(bytes.TrimSuffix(b, []byte("\n")), []byte("\n")) {
		n, rest, _ := bytes.Cut(l, []byte(" "))
		at, err := strconv.ParseInt(string(n), 10, 64)
		if err != nil {
			t.Fatalf("in.log: %q", l)
		}
		ins = append(ins, inLine{at, rest})
	}
	return ins
}

func (h *harness) write(line []byte) error {
	h.writes <- slices.Clone(line)
	return nil
}

func (h *harness) record(e agent.Event) {
	h.mu.Lock()
	h.events = append(h.events, e)
	h.mu.Unlock()
}

// act does what wrote line: a message, an answer, a control request.
func (h *harness) act(line []byte) {
	var f struct {
		Type    string `json:"type"`
		UUID    string `json:"uuid"`
		Origin  any    `json:"origin"`
		Message struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"message"`
		Request  map[string]any `json:"request"`
		Response struct {
			Subtype   string `json:"subtype"`
			RequestID string `json:"request_id"`
			Response  struct {
				Behavior     string         `json:"behavior"`
				Message      string         `json:"message"`
				UpdatedInput map[string]any `json:"updatedInput"`
			} `json:"response"`
		} `json:"response"`
	}
	if err := json.Unmarshal(line, &f); err != nil {
		h.t.Fatal(err)
	}
	ctx := context.Background()
	switch f.Type {
	case "user":
		if err := h.d.Send(ctx, agent.Message{ID: f.UUID, Text: f.Message.Content[0].Text, Human: f.Origin != nil}); err != nil {
			h.t.Fatal(err)
		}
	case "control_response":
		if f.Response.Subtype != "success" {
			return // an error answer to the CLI's request, which the driver writes as it reads that
		}
		a := api.Answer{RequestID: f.Response.RequestID, Allow: f.Response.Response.Behavior == "allow", Message: f.Response.Response.Message}
		if answers, ok := f.Response.Response.UpdatedInput["answers"].(map[string]any); ok {
			a.Answers = map[string]string{}
			for k, v := range answers {
				a.Answers[k] = v.(string)
			}
		}
		h.op("answer", func() (any, error) { return nil, h.d.Answer(ctx, a) })
	case "control_request":
		sub := f.Request["subtype"].(string)
		switch sub {
		case "initialize":
			h.d.Attach(h.write, 1) // a daemon reconnecting to the same process
		case "interrupt":
			h.op(sub, func() (any, error) { return nil, h.d.Interrupt(ctx) })
		case "get_context_usage":
			h.op(sub, func() (any, error) { return h.d.Context(ctx) })
		case "get_usage":
			h.op(sub, func() (any, error) { return h.d.Quota(ctx) })
		case "set_model":
			h.op(sub, func() (any, error) { return nil, h.d.SetModel(ctx, f.Request["model"].(string)) })
		case "set_permission_mode":
			h.op(sub, func() (any, error) { return nil, h.d.SetMode(ctx, f.Request["mode"].(string)) })
		case "get_settings":
			// SetEffort writes get_settings, then apply_flag_settings; Effort only the first.
			var next struct {
				Request struct {
					Subtype  string         `json:"subtype"`
					Settings map[string]any `json:"settings"`
				} `json:"request"`
			}
			_ = json.Unmarshal(h.next, &next)
			level, isLevel := next.Request.Settings["effortLevel"].(string)
			on, isUltra := next.Request.Settings["ultracode"].(bool)
			switch {
			case next.Request.Subtype == "apply_flag_settings" && isLevel:
				h.op("set_effort", func() (any, error) { return nil, h.d.SetEffort(ctx, level) })
			case next.Request.Subtype == "apply_flag_settings" && isUltra:
				h.op("set_ultracode", func() (any, error) { return on, h.d.SetUltracode(ctx, on) })
			default:
				h.op(sub, func() (any, error) { return h.d.Applied(ctx) })
			}
		case "apply_flag_settings":
			// written by the SetEffort or SetUltracode begun at the get_settings before
		default:
			h.t.Fatalf("no operation writes %s", sub)
		}
	default:
		h.t.Fatalf("no operation writes %s", line)
	}
}

// op runs an operation that waits for the CLI's answer, which comes as the replay goes on.
func (h *harness) op(name string, f func() (any, error)) {
	h.ops.Add(1)
	go func() {
		defer h.ops.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = ctx
		v, err := f()
		if err != nil {
			h.t.Errorf("%s: %v", name, err)
		}
		h.mu.Lock()
		h.result[name] = v
		h.mu.Unlock()
	}()
}

// compare checks the driver wrote what Rhodium did, as JSON values, its own request ids aside.
func (h *harness) compare(got, want []byte) {
	h.t.Helper()
	var g, w map[string]any
	if err := json.Unmarshal(got, &g); err != nil {
		h.t.Fatalf("the driver wrote %q: %v", got, err)
	}
	if err := json.Unmarshal(want, &w); err != nil {
		h.t.Fatal(err)
	}
	if g["type"] == "control_request" && w["type"] == "control_request" {
		h.ids[w["request_id"].(string)] = g["request_id"].(string)
		delete(g, "request_id")
		delete(w, "request_id")
	}
	if !reflect.DeepEqual(g, w) {
		h.t.Fatalf("wrote\n  %s\nwant\n  %s", got, want)
	}
}

// kinds lists the events by type, deltas joined, for comparing sequences.
func (h *harness) kinds() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	for _, e := range h.events {
		s := fmt.Sprintf("%T", e)[len("agent."):]
		switch e := e.(type) {
		case agent.StateChange:
			s += ":" + string(e.State)
		case agent.TextDelta, agent.ToolDraftDelta:
			if len(out) > 0 && out[len(out)-1] == s {
				continue
			}
		}
		out = append(out, s)
	}
	return out
}

func find[T agent.Event](h *harness) []T {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []T
	for _, e := range h.events {
		if v, ok := e.(T); ok {
			out = append(out, v)
		}
	}
	return out
}

func deltas(h *harness) string {
	var b strings.Builder
	for _, d := range find[agent.TextDelta](h) {
		b.WriteString(d.Text)
	}
	return b.String()
}

func TestBasicTurn(t *testing.T) {
	h := replay(t, "basic-turn")
	const input = "00000000-0000-4000-8000-ee09bd398767"
	want := []string{"Hello", "PendingAsks", "TurnStart", "StateChange:running", "Taken", "Started", "ToolDraft", "ToolDraftDelta", "ToolStart", "QuotaHit", "ToolDone", "TextDelta", "MessageDone", "TurnUsage", "StateChange:idle", "TurnEnd"}
	if got := h.kinds(); !reflect.DeepEqual(got, want) {
		t.Fatalf("events\n  %v\nwant\n  %v", got, want)
	}
	// The call is drafted from its start: its id and name, then its input as it is written, the
	// pieces joined the input the whole call has.
	draft, start := find[agent.ToolDraft](h)[0], find[agent.ToolStart](h)[0]
	if draft.ID != start.ID || draft.Name != start.Name {
		t.Errorf("tool draft %+v, call %+v", draft, start)
	}
	var in strings.Builder
	for _, d := range find[agent.ToolDraftDelta](h) {
		in.WriteString(d.JSON)
	}
	var drafted, whole any
	if err := json.Unmarshal([]byte(in.String()), &drafted); err != nil {
		t.Fatalf("drafted input %q: %v", in.String(), err)
	}
	if b, _ := json.Marshal(start.Input); json.Unmarshal(b, &whole) != nil || !reflect.DeepEqual(drafted, whole) {
		t.Errorf("drafted input %v, call's %v", drafted, whole)
	}
	if s := find[agent.TurnStart](h); s[0].Input != input {
		t.Errorf("turn began by %q", s[0].Input)
	}
	if hello := find[agent.Hello](h)[0]; hello.Version != "2.1.304" || hello.Mode != "default" || len(hello.Models) == 0 || hello.Models[0].Value != "default" {
		t.Errorf("hello: %+v", hello)
	}
	if p := find[agent.PendingAsks](h)[0]; !p.Known || len(p.Asks) != 0 {
		t.Errorf("pending asks: %+v", p)
	}
	if s := find[agent.Started](h)[0]; s.Session != "00000000-0000-4000-8000-762bb2e0d9a7" || s.Model != "claude-haiku-5-5" {
		t.Errorf("started: %+v", s)
	}
	reply := "There are 12 wicks in notes.txt."
	if deltas(h) != reply || find[agent.TurnEnd](h)[0].Reply != reply {
		t.Errorf("deltas %q, reply %q", deltas(h), find[agent.TurnEnd](h)[0].Reply)
	}
	u := find[agent.TurnUsage](h)[0]
	if u.Tokens != (agent.Tokens{Input: 4, Output: 139, CacheRead: 39181, CacheWrite: 15903}) || u.Total == nil || u.Total.Models["claude-haiku-5-5"].Output != 139 || u.Context != 2+2126+26479 {
		t.Errorf("usage: %+v %+v", u, u.Total)
	}
	if h.d.State() != agent.Idle {
		t.Errorf("state %s", h.d.State())
	}
}

func TestApprovals(t *testing.T) {
	h := replay(t, "approval-allow-deny")
	asks := find[agent.AskOpen](h)
	if len(asks) != 2 || asks[0].Ask.Kind != "permission" || asks[0].Ask.Tool != "Bash" || asks[0].Ask.ToolUseID == "" {
		t.Fatalf("asks: %+v", asks)
	}
	if len(find[agent.AskCancel](h)) != 2 || len(find[agent.TurnEnd](h)) != 2 {
		t.Errorf("answered %d, turns %d", len(find[agent.AskCancel](h)), len(find[agent.TurnEnd](h)))
	}
	// An answer to an ask no longer open is refused.
	if err := h.d.Answer(context.Background(), api.Answer{RequestID: asks[0].Ask.RequestID, Allow: true}); err == nil {
		t.Error("answered twice")
	}
}

func TestQuestionAndPlan(t *testing.T) {
	h := replay(t, "question")
	if a := find[agent.AskOpen](h); len(a) != 1 || a[0].Ask.Kind != "question" {
		t.Fatalf("asks: %+v", a)
	}
	h = replay(t, "plan-confirm")
	a := find[agent.AskOpen](h)
	if len(a) != 2 || a[0].Ask.Kind != "plan" || a[1].Ask.Kind != "permission" {
		t.Fatalf("asks: %+v", a)
	}
}

func TestInterruptWithAskOpen(t *testing.T) {
	h := replay(t, "interrupt-pending-approval")
	opened, cancelled := find[agent.AskOpen](h), find[agent.AskCancel](h)
	if len(opened) != 1 || len(cancelled) != 1 || cancelled[0].RequestID != opened[0].Ask.RequestID {
		t.Fatalf("opened %+v, withdrawn %+v", opened, cancelled)
	}
	if n := len(find[agent.TurnEnd](h)); n != 2 {
		t.Errorf("%d turns", n)
	}
	replay(t, "cmd-stop")
}

func TestCompact(t *testing.T) {
	h := replay(t, "cmd-compact")
	ends := find[agent.TurnEnd](h)
	if len(ends) != 2 || ends[0].CompactOnly || !ends[1].CompactOnly {
		t.Fatalf("turn ends: %+v", ends)
	}
	if c := find[agent.Compacted](h); len(c) != 1 || c[0].Before != 26478 || c[0].After != 8274 {
		t.Errorf("compacted: %+v", c)
	}
	states := []string{}
	for _, k := range h.kinds() {
		if strings.HasPrefix(k, "StateChange:") {
			states = append(states, k[len("StateChange:"):])
		}
	}
	if want := []string{"running", "idle", "running", "compacting", "running", "idle"}; !reflect.DeepEqual(states, want) {
		t.Errorf("states %v, want %v", states, want)
	}
}

func TestContextAndQuota(t *testing.T) {
	h := replay(t, "cmd-context")
	c := h.result["get_context_usage"].(*api.Context)
	if c.Used != 22358 || c.Max != 1000000 || len(c.Categories) != 9 || c.Categories[0].ID != "system_prompt" {
		t.Errorf("context: %+v", c)
	}
	h = replay(t, "cmd-usage")
	u := h.result["get_usage"].(*agent.Usage)
	want := []api.QuotaWindow{
		{Group: "session", Percent: 12, ResetsAt: time.Date(2026, 1, 4, 0, 40, 0, 574822000, time.UTC).UnixMilli()},
		{Group: "weekly", Percent: 52, ResetsAt: time.Date(2026, 1, 8, 0, 0, 0, 574843000, time.UTC).UnixMilli()},
		{Group: "weekly", Model: "Fable", Percent: 38, ResetsAt: time.Date(2026, 1, 8, 0, 0, 0, 575041000, time.UTC).UnixMilli()},
	}
	if !reflect.DeepEqual(u.Windows, want) || u.CostUSD != 0 {
		t.Errorf("quota: %+v", u)
	}
	replay(t, "cmd-model")
}

// A message written mid-turn joins it: one turn, two inputs taken, ended once.
func TestMidTurnMessage(t *testing.T) {
	h := replay(t, "mid-turn-message")
	if n := len(find[agent.TurnEnd](h)); n != 1 {
		t.Errorf("%d turn ends", n)
	}
	if taken := find[agent.Taken](h); len(taken) != 2 {
		t.Errorf("taken: %+v", taken)
	}
}

// With a subagent in the background the CLI holds back idle: each result ends a turn, the next
// begins at its input's started, and the CLI's own wake-up turn begins at its init.
func TestBackgroundAgent(t *testing.T) {
	h := replay(t, "background-agent")
	starts, ends := find[agent.TurnStart](h), find[agent.TurnEnd](h)
	if len(starts) != 3 || len(ends) != 3 {
		t.Fatalf("starts %+v, ends %+v", starts, ends)
	}
	if starts[0].Input != "00000000-0000-4000-8000-ee0e46e8f27c" || starts[1].Input != "00000000-0000-4000-8000-dcb8fd9e3c55" || starts[2].Input != "" {
		t.Errorf("starts: %+v", starts)
	}
	if !ends[0].Background {
		t.Errorf("first turn ended without its background task: %+v", ends[0])
	}
	tasks := find[agent.Tasks](h)
	if len(tasks) == 0 || tasks[0].Tasks[0].Type != "local_agent" {
		t.Errorf("tasks: %+v", tasks)
	}
	if h.d.State() != agent.Idle {
		t.Errorf("state %s", h.d.State())
	}
}

// A daemon reconnecting sends initialize again; its answer names the ask still open, which can
// then be answered.
func TestReinitializeWithAskOpen(t *testing.T) {
	h := replay(t, "reinitialize-pending-approval")
	p := find[agent.PendingAsks](h)
	if len(p) != 2 || len(p[0].Asks) != 0 || len(p[1].Asks) != 1 || p[1].Asks[0].Tool != "Bash" {
		t.Fatalf("pending asks: %+v", p)
	}
}

// Unknown kinds are counted and skipped, a non-JSON line too; an unknown request from the CLI is
// answered with an error, or it would wait forever.
func TestUnknownMessages(t *testing.T) {
	h := replay(t, "unknown-messages")
	u := h.d.Unknown()
	if u["non_json"] != 1 || u["control_request/rh_future_request"] != 1 {
		t.Errorf("unknown: %v", u)
	}
	if n := len(find[agent.TurnEnd](h)); n != 1 {
		t.Errorf("%d turn ends", n)
	}
}

func TestRateLimitRejected(t *testing.T) {
	h := replay(t, "rate-limit-rejected")
	q := find[agent.QuotaHit](h)
	if len(q) != 1 || !q[0].Exhausted || q[0].ResetsAt != 1767235292000 {
		t.Fatalf("quota: %+v", q)
	}
}

// Replaying an earlier daemon's lines never writes: the request was answered then.
func TestReplayWritesNothing(t *testing.T) {
	out, err := os.ReadFile("testdata/unknown-messages/out.log")
	if err != nil {
		t.Fatal(err)
	}
	var writes int
	d := New(Options{IDPrefix: "x-", Logf: func(string, ...any) {}})
	d.Attach(func([]byte) error { writes++; return nil }, 1)
	for _, l := range bytes.Split(bytes.TrimSuffix(out, []byte("\n")), []byte("\n")) {
		d.Feed(l, true)
	}
	time.Sleep(50 * time.Millisecond)
	if writes != 1 { // initialize
		t.Errorf("%d writes", writes)
	}
}

func TestSpawn(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	env := []string{"PATH=" + bin, "CLAUDECODE=1", "CLAUDE_CODE_SESSION_ID=parent", "CLAUDE_CODE_ENTRYPOINT=cli", "HERDR_PANE=3", "CLAUDE_JOB_DIR=/tmp/job", "IRIDIUM_SESSION=parent", "HOME=/home/x", "ANTHROPIC_MODEL=x"}
	d := New(Options{})
	spec, err := d.Spawn(agent.SpawnOptions{Session: "s1", Cwd: "/w", Env: env, Program: "claude", SystemPromptFile: "/p.md", Model: "haiku"})
	if err != nil {
		t.Fatal(err)
	}
	argv := strings.Join(spec.Argv, " ")
	want := filepath.Join(bin, "claude") + " -p --input-format stream-json --output-format stream-json --verbose --permission-prompt-tool stdio --include-partial-messages --append-system-prompt-file /p.md --system-prompt-snapshot off --model haiku --allow-dangerously-skip-permissions --session-id=s1"
	if argv != want {
		t.Errorf("argv\n  %s\nwant\n  %s", argv, want)
	}
	slices.Sort(spec.Env)
	wantEnv := []string{"ANTHROPIC_MODEL=x", "CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS=1", "HOME=/home/x", "IRIDIUM_SESSION=s1", "PATH=" + bin}
	if !reflect.DeepEqual(spec.Env, wantEnv) {
		t.Errorf("env %v", spec.Env)
	}
	spec, _ = d.Spawn(agent.SpawnOptions{Session: "s1", Resume: true, Env: env, Program: "claude", SystemPromptFile: "/p.md", CrashResume: true})
	if spec.Argv[len(spec.Argv)-1] != "--resume=s1" || agent.GetEnv(spec.Env, "CLAUDE_CODE_RESUME_INTERRUPTED_TURN") != "1" {
		t.Errorf("resume: %v", spec.Argv)
	}
	if _, err := d.Spawn(agent.SpawnOptions{Session: "s1", Env: append(env, "CLAUDE_CODE_SIMPLE=1"), Program: "claude"}); err != nil {
		t.Errorf("CLAUDE_CODE_SIMPLE from a parent is dropped, not refused: %v", err)
	}
	if _, err := d.Spawn(agent.SpawnOptions{Session: "s1", Env: env, Program: "nope"}); err == nil {
		t.Error("spawned a missing program")
	}
}

// /effort lists (get_settings' applied.effort), then sets a level with ultracode as it was.
func TestEffort(t *testing.T) {
	h := replay(t, "cmd-effort")
	if got, _ := h.result["get_settings"].(agent.Applied); got.Effort != "medium" || got.Model != "claude-haiku-5-5" || !got.Known || !got.UltracodeAvailable || got.Ultracode {
		t.Errorf("applied: %+v", h.result["get_settings"])
	}
	if _, ok := h.result["set_effort"]; !ok {
		t.Error("set_effort did not return")
	}
	d := New(Options{})
	bin := t.TempDir()
	os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\n"), 0o755)
	spec, err := d.Spawn(agent.SpawnOptions{Session: "s", Env: []string{"PATH=" + bin}, Program: "claude", SystemPromptFile: "/p", Effort: "high"})
	if err != nil || !strings.Contains(strings.Join(spec.Argv, " "), " --effort high ") {
		t.Errorf("spawn: %v %v", spec.Argv, err)
	}
}

// Ultracode goes alone in apply_flag_settings, the effort level as it was; get_settings says what
// is asked for and whether the model takes it. At launch it is --settings, never --effort.
func TestUltracode(t *testing.T) {
	h := replay(t, "cmd-ultracode")
	if _, ok := h.result["set_ultracode"]; !ok {
		t.Error("set_ultracode did not return")
	}
	if got, _ := h.result["get_settings"].(agent.Applied); !got.Ultracode || !got.UltracodeOn || !got.UltracodeAvailable || got.Effort != "low" {
		t.Errorf("applied: %+v", h.result["get_settings"])
	}
	d := New(Options{})
	bin := t.TempDir()
	os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\n"), 0o755)
	on := true
	spec, err := d.Spawn(agent.SpawnOptions{Session: "s", Env: []string{"PATH=" + bin}, Program: "claude", SystemPromptFile: "/p", Effort: "high", Ultracode: &on})
	if argv := strings.Join(spec.Argv, " "); err != nil || !strings.Contains(argv, ` --effort high --settings {"ultracode":true} `) {
		t.Errorf("spawn: %v %v", spec.Argv, err)
	}
	spec, _ = d.Spawn(agent.SpawnOptions{Session: "s", Env: []string{"PATH=" + bin}, Program: "claude", SystemPromptFile: "/p"})
	if slices.Contains(spec.Argv, "--settings") {
		t.Errorf("spawn, never set: %v", spec.Argv)
	}
	// A model that cannot take it: refused before anything is written.
	d = New(Options{Logf: t.Logf})
	var wrote []string
	d.Attach(func(l []byte) error {
		var f struct {
			ID  string         `json:"request_id"`
			Req map[string]any `json:"request"`
		}
		json.Unmarshal(l, &f)
		wrote = append(wrote, fmt.Sprint(f.Req["subtype"]))
		if f.Req["subtype"] == "get_settings" {
			go d.Feed([]byte(`{"type":"control_response","response":{"subtype":"success","request_id":"`+f.ID+`","response":{"applied":{"model":"claude-haiku-4-5","ultracodeAvailable":false}}}}`), false)
		}
		return nil
	}, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := d.SetUltracode(ctx, true); !errors.Is(err, agent.ErrNoUltracode) || !slices.Equal(wrote, []string{"initialize", "get_settings"}) {
		t.Errorf("unavailable: %v, wrote %v", err, wrote)
	}
	// A CLI without get_settings: unknown is unavailable (Rhodium §6.2), off still goes.
	d = New(Options{Logf: t.Logf})
	wrote = nil
	d.Attach(func(l []byte) error {
		var f struct {
			ID  string         `json:"request_id"`
			Req map[string]any `json:"request"`
		}
		json.Unmarshal(l, &f)
		wrote = append(wrote, fmt.Sprint(f.Req["subtype"]))
		switch f.Req["subtype"] {
		case "get_settings":
			go d.Feed([]byte(`{"type":"control_response","response":{"subtype":"error","request_id":"`+f.ID+`","error":"Unsupported control request subtype: get_settings"}}`), false)
		case "apply_flag_settings":
			go d.Feed([]byte(`{"type":"control_response","response":{"subtype":"success","request_id":"`+f.ID+`","response":{}}}`), false)
		}
		return nil
	}, 1)
	if err := d.SetUltracode(ctx, true); !errors.Is(err, agent.ErrNoUltracode) || !slices.Equal(wrote, []string{"initialize", "get_settings"}) {
		t.Errorf("no get_settings: %v, wrote %v", err, wrote)
	}
	if err := d.SetUltracode(ctx, false); err != nil || wrote[len(wrote)-1] != "apply_flag_settings" {
		t.Errorf("no get_settings, off: %v, wrote %v", err, wrote)
	}
}

// Each model says what it resolves to and the levels it takes; Haiku 4.5 takes none.
func TestModelsResolved(t *testing.T) {
	h := replay(t, "basic-turn")
	ms := find[agent.Hello](h)[0].Models
	byValue := map[string]agent.Model{}
	for _, m := range ms {
		byValue[m.Value] = m
	}
	if d := byValue["default"]; d.Resolved != "claude-opus-5-5" || len(d.Efforts) != 5 || d.NoEffort {
		t.Errorf("default: %+v", d)
	}
	if m := byValue["claude-haiku-4-5-20251001"]; !m.NoEffort || len(m.Efforts) != 0 {
		t.Errorf("haiku 4.5: %+v", m)
	}
	if m := byValue["claude-opus-4-6"]; slices.Contains(m.Efforts, "xhigh") || !slices.Contains(m.Efforts, "max") {
		t.Errorf("opus 4.6: %+v", m)
	}
}

// The CLI's suggested rule is the ask's pattern; initialize names the account.
func TestPatternAndAccount(t *testing.T) {
	h := replay(t, "approval-allow-deny")
	asks := find[agent.AskOpen](h)
	if asks[0].Ask.Pattern != "echo first *" || asks[1].Ask.Pattern != "echo second *" {
		t.Errorf("patterns %q %q", asks[0].Ask.Pattern, asks[1].Ask.Pattern)
	}
	h = replay(t, "basic-turn")
	if hello := find[agent.Hello](h)[0]; hello.Account != "user@example.com" || hello.Plan != "Claude Pro" {
		t.Errorf("hello: %+v", hello)
	}
}

// Images go after the text as base64 image blocks.
func TestSendImages(t *testing.T) {
	var lines [][]byte
	d := New(Options{Logf: func(string, ...any) {}})
	d.Attach(func(l []byte) error { lines = append(lines, l); return nil }, 1)
	err := d.Send(context.Background(), agent.Message{ID: "u1", Text: "[本机] look\n\n[附件] `/w/a.png`", Human: true, Images: []agent.Image{{MediaType: "image/png", Data: []byte{0x89, 'P', 'N', 'G'}}}})
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Message struct {
			Content []map[string]any `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(lines[len(lines)-1], &f); err != nil || len(f.Message.Content) != 2 {
		t.Fatalf("frame %s: %v", lines[len(lines)-1], err)
	}
	src, _ := f.Message.Content[1]["source"].(map[string]any)
	if f.Message.Content[0]["type"] != "text" || f.Message.Content[1]["type"] != "image" || src["type"] != "base64" || src["media_type"] != "image/png" || src["data"] != "iVBORw==" {
		t.Errorf("content %v", f.Message.Content)
	}
}
