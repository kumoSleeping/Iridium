package dsh

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

// The recordings in testdata/<case> are dsh 0.2.2-rc.1 (`dsh --profile sdk --patch <standard
// preset with Iridium's system prompt>`) against a fake Anthropic Messages endpoint, never a real
// model, recorded as the holder records: out.log is dsh's stdout, in.log each line written to it
// after out.log's size at the time. The probe wrote what this driver writes, frame for frame. A
// replay feeds out.log, does what made each in.log line (the handshake's lines the driver writes
// by itself), and checks the driver writes the same frame, with the probe's ids mapped to the
// driver's. testdata/record has the tools: the fake endpoint (`node stub.mjs`, port 18732), the
// recorder and each case's steps (`DSH_HOME=<temp> DEEPSEEK_API_KEY=sk-stub
// DEEPSEEK_BASE_URL=http://127.0.0.1:18732/anthropic node record.mjs <dir> <case>.json <patch>`).

type inLine struct {
	at  int64
	msg map[string]any
}

type harness struct {
	t      *testing.T
	d      *Driver
	mu     sync.Mutex
	events []agent.Event
	writes chan map[string]any
	held   map[string]any // a write taken early, for next
	ids    map[string]string
	ops    sync.WaitGroup
	result map[string]any
	own    int // in.log lines a mode switch writes by itself, still to come
}

type setup struct {
	launch
	mode string
	exit bool // the process ended after out.log: detach then
}

// testCatalog is what the probe of the recorded dsh prints (dsh 0.2.2-rc.1, a DeepSeek key).
var testCatalog = agent.Catalog{Default: "deepseek-official/deepseek-flash", Models: []agent.Model{
	{Value: "deepseek-official/deepseek-flash", Name: "DeepSeek-V4.1-Flash", Efforts: []string{"off", "low", "high", "max"}},
	{Value: "deepseek-official/deepseek-v4-pro", Name: "DeepSeek-V4-Pro", Efforts: []string{"off", "low", "high", "max"}},
}}

func newSetup(session string) setup {
	return setup{launch: launch{session: session, cwd: "/tmp/dsh-drv/w1", model: DefaultModel, provider: LegacyProvider, modelID: "deepseek-flash", catalog: testCatalog, version: "0.2.2-rc.1"}, mode: DefaultMode}
}

func replay(t *testing.T, name string, s setup) *harness {
	t.Helper()
	dir := filepath.Join("testdata", name)
	ins := readIn(t, filepath.Join(dir, "in.log"))
	out, err := os.ReadFile(filepath.Join(dir, "out.log"))
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, writes: make(chan map[string]any, 64), ids: map[string]string{}, result: map[string]any{}}
	h.d = New(Options{Emit: h.record, IDPrefix: "ir-test-", Logf: t.Logf})
	h.d.spawned, h.d.next, h.d.session, h.d.mode, h.d.catalog = true, s.launch, s.session, s.mode, s.catalog
	h.d.Attach(h.write, 1)
	var off int64
	feedUntil := func(at int64) {
		for off < int64(len(out)) {
			i := bytes.IndexByte(out[off:], '\n')
			if off+int64(i)+1 > at {
				return
			}
			line := out[off : off+int64(i)]
			off += int64(i) + 1
			h.d.Feed(h.mapAnswer(line), false)
		}
	}
	for i, in := range ins {
		feedUntil(in.at)
		h.act(ins, i)
	}
	feedUntil(int64(len(out)))
	if s.exit {
		h.d.Detach()
	}
	h.ops.Wait()
	time.Sleep(20 * time.Millisecond)
	select {
	case extra := <-h.writes:
		t.Fatalf("an extra write: %v", extra)
	default:
	}
	if u := h.d.Unknown(); len(u) > 0 {
		t.Errorf("unknown output: %v", u)
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
		var m map[string]any
		if err != nil || json.Unmarshal(rest, &m) != nil {
			t.Fatalf("in.log: %q", l)
		}
		ins = append(ins, inLine{at, m})
	}
	return ins
}

// mapAnswer gives the answer to a probe's request the driver's id.
func (h *harness) mapAnswer(line []byte) []byte {
	var m map[string]any
	if json.Unmarshal(line, &m) != nil || m["method"] != nil {
		return line
	}
	id, _ := m["id"].(string)
	h.mu.Lock()
	to, ok := h.ids[id]
	h.mu.Unlock()
	if !ok {
		h.t.Fatalf("an answer to %s, which the driver did not ask", id)
	}
	m["id"] = to
	b, _ := json.Marshal(m)
	return b
}

func (h *harness) write(line []byte) error {
	var m map[string]any
	if err := json.Unmarshal(line, &m); err != nil {
		h.t.Errorf("the driver wrote %q", line)
		return nil
	}
	h.writes <- m
	return nil
}

func (h *harness) record(e agent.Event) {
	h.mu.Lock()
	h.events = append(h.events, e)
	h.mu.Unlock()
}

func (h *harness) next() map[string]any {
	h.t.Helper()
	if w := h.held; w != nil {
		h.held = nil
		return w
	}
	select {
	case got := <-h.writes:
		return got
	case <-time.After(3 * time.Second):
		h.t.Fatal("the driver wrote nothing")
	}
	return nil
}

// ownWrite: the driver writes the next line by itself (the handshake's), rather than wait for an
// operation: it writes before the handshake is done.
func (h *harness) ownWrite() bool {
	h.d.mu.Lock()
	ready := h.d.ready
	h.d.mu.Unlock()
	select {
	case w := <-h.writes:
		h.held = w
		return true
	case <-ready:
		select {
		case w := <-h.writes:
			h.held = w
			return true
		default:
			return false
		}
	case <-time.After(3 * time.Second):
		h.t.Fatal("neither a write nor the handshake's end")
	}
	return false
}

// compare checks the driver wrote the probe's frame, its own id aside, which it maps.
func (h *harness) compare(got, probe map[string]any) {
	h.t.Helper()
	pid, _ := probe["id"].(string)
	if gid, _ := got["id"].(string); probe["method"] != nil && gid != pid {
		h.mu.Lock()
		h.ids[pid] = gid
		h.mu.Unlock()
		g := map[string]any{}
		for k, v := range got {
			g[k] = v
		}
		g["id"] = pid
		got = g
	}
	if !reflect.DeepEqual(got, probe) {
		a, _ := json.Marshal(got)
		b, _ := json.Marshal(probe)
		h.t.Errorf("wrote\n  %s\nprobe\n  %s", a, b)
	}
}

func text(m map[string]any) string {
	p, _ := m["params"].(map[string]any)
	blocks, _ := p["contentBlocks"].([]any)
	if len(blocks) == 0 {
		return ""
	}
	b, _ := blocks[0].(map[string]any)
	s, _ := b["text"].(string)
	return s
}

// modeOf is the mode a mode switch's first line asks for, and how many lines after it the switch
// writes too.
func modeOf(t string, rest []inLine) (string, int) {
	switch {
	case strings.HasPrefix(t, "/permission "):
		return strings.TrimPrefix(t, "/permission "), 0
	case t == "/plan":
		return "plan", 0
	case t == "/plan off":
		if len(rest) > 0 {
			if p, ok := strings.CutPrefix(text(rest[0].msg), "/permission "); ok {
				return p, 1
			}
		}
	}
	return "", 0
}

func (h *harness) act(ins []inLine, i int) {
	h.t.Helper()
	ctx := context.Background()
	probe := ins[i].msg
	method, _ := probe["method"].(string)
	input := fmt.Sprintf("in-%d", i)
	switch method {
	case "":
		h.answer(probe)
	case "initialize", "session/resume", "session/working-directory/get":
		h.compare(h.next(), probe)
	case "session/query":
		if h.own > 0 {
			h.own--
			h.compare(h.next(), probe)
			return
		}
		if h.ownWrite() {
			h.compare(h.next(), probe)
			return
		}
		if mode, own := modeOf(text(probe), ins[i+1:]); mode != "" {
			h.own = own
			h.op("mode "+mode, func() (any, error) { return nil, h.d.SetMode(ctx, mode) })
		} else {
			h.op("send "+input, func() (any, error) {
				return nil, h.d.Send(ctx, agent.Message{ID: input, Text: text(probe), Human: true})
			})
		}
		h.compare(h.next(), probe)
	case "session/prompt":
		h.op("send "+input, func() (any, error) {
			return nil, h.d.Send(ctx, agent.Message{ID: input, Text: text(probe), Human: true})
		})
		h.compare(h.next(), probe)
	case "shutdown":
		h.op("interrupt", func() (any, error) { return nil, h.d.Interrupt(ctx) })
		h.compare(h.next(), probe)
	default:
		h.t.Fatalf("in.log: %s", method)
	}
}

// answer answers the open ask as the probe did, through Answer as the card would.
func (h *harness) answer(probe map[string]any) {
	h.t.Helper()
	opened := find[agent.AskOpen](h)
	if len(opened) == 0 {
		h.t.Fatal("an answer with no ask open")
	}
	k := opened[len(opened)-1].Ask
	res, _ := probe["result"].(map[string]any)
	a := api.Answer{RequestID: k.RequestID, Kind: k.Kind}
	items, _ := res["answers"].([]any)
	switch k.Kind {
	case "permission":
		a.Allow = res["outcome"] == "allowed-once"
	case "plan":
		item, _ := items[0].(map[string]any)
		sel, _ := item["selected"].([]any)
		a.Allow = slices.Contains(sel, any("Approve"))
		a.Message, _ = item["custom"].(string)
	case "question":
		qs, _ := k.Input.(map[string]any)["questions"].([]any)
		a.Answers = map[string]string{}
		for j, it := range items {
			item, _ := it.(map[string]any)
			q, _ := qs[j].(map[string]any)
			var parts []string
			for _, s := range item["selected"].([]any) {
				parts = append(parts, s.(string))
			}
			if c, ok := item["custom"].(string); ok {
				parts = append(parts, c)
			}
			a.Answers[q["question"].(string)] = strings.Join(parts, ", ")
		}
	}
	if err := h.d.Answer(context.Background(), a); err != nil {
		h.t.Fatalf("answer: %v", err)
	}
	h.compare(h.next(), probe)
}

func (h *harness) op(name string, f func() (any, error)) {
	h.ops.Add(1)
	go func() {
		defer h.ops.Done()
		v, err := f()
		if err != nil {
			h.t.Errorf("%s: %v", name, err)
		}
		h.mu.Lock()
		h.result[name] = v
		h.mu.Unlock()
	}()
}

func (h *harness) kinds() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	for _, e := range h.events {
		s := fmt.Sprintf("%T", e)[len("agent."):]
		if e, ok := e.(agent.StateChange); ok {
			s += ":" + string(e.State)
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

func states(h *harness) []string {
	var out []string
	for _, k := range h.kinds() {
		if s, ok := strings.CutPrefix(k, "StateChange:"); ok {
			out = append(out, s)
		}
	}
	return out
}

func TestTurn(t *testing.T) {
	h := replay(t, "turn", newSetup("5f0c1d2e-0001-4000-8000-000000000001"))
	want := []string{"Started", "Hello", "TurnStart", "StateChange:running", "Taken", "MessageDone", "ToolStart", "ToolDone", "MessageDone", "TurnUsage", "StateChange:idle", "TurnEnd"}
	if k := h.kinds(); !reflect.DeepEqual(k, want) {
		t.Fatalf("events\n  %v\nwant\n  %v", k, want)
	}
	if s := find[agent.TurnStart](h)[0]; s.Input != "in-3" {
		t.Errorf("start %+v", s)
	}
	if tk := find[agent.Taken](h); tk[0].InputID != "in-3" {
		t.Errorf("taken %+v", tk)
	}
	if e := find[agent.TurnEnd](h)[0]; e.Reply != "Done after the tool. " || e.Background {
		t.Errorf("end %+v", e)
	}
	if ts := find[agent.ToolStart](h)[0]; ts.Name != "bash" || ts.Input.(map[string]any)["command"] != "echo hi" {
		t.Errorf("tool %+v", ts)
	}
	u := find[agent.TurnUsage](h)[0]
	if u.Tokens != (agent.Tokens{Input: 200, Output: 32, CacheRead: 80}) || u.Context != 140 || u.Total != nil || u.CostUSD != nil {
		t.Errorf("usage %+v", u)
	}
	hello := find[agent.Hello](h)[0]
	if hello.Mode != "danger-full-access" || hello.Version != "0.2.2-rc.1" || len(hello.Models) != 2 || hello.Models[0].Value != "deepseek-official/deepseek-flash" || len(hello.Commands) != 3 {
		t.Errorf("hello %+v", hello)
	}
	if s := find[agent.Started](h)[0]; s != (agent.Started{Session: "5f0c1d2e-0001-4000-8000-000000000001", Model: "deepseek-official/deepseek-flash", Effort: "high"}) {
		t.Errorf("started %+v", s)
	}
	c, err := h.d.Context(context.Background())
	if err != nil || c.Used != 140 || c.Max != 1048576 {
		t.Errorf("context %+v %v", c, err)
	}
	if _, err := h.d.Quota(context.Background()); !errors.Is(err, ErrNoQuota) {
		t.Errorf("quota: %v", err)
	}
}

// A message while the run goes waits in dsh's inbox and runs as dsh's next turn, in the same
// run: one turn of Iridium's, both messages taken.
func TestSteer(t *testing.T) {
	h := replay(t, "steer", newSetup("5f0c1d2e-0002-4000-8000-000000000002"))
	var taken []string
	for _, e := range find[agent.Taken](h) {
		taken = append(taken, e.InputID)
	}
	if !reflect.DeepEqual(taken, []string{"in-3", "in-4"}) || len(find[agent.TurnEnd](h)) != 1 || len(find[agent.TurnStart](h)) != 1 {
		t.Errorf("taken %v, events %v", taken, h.kinds())
	}
	if e := find[agent.TurnEnd](h)[0]; e.Reply != "Echo: [web] steered in " {
		t.Errorf("end %+v", e)
	}
}

func TestApproval(t *testing.T) {
	s := newSetup("5f0c1d2e-0003-4000-8000-000000000003")
	s.mode = "read-only"
	h := replay(t, "approval", s)
	a := find[agent.AskOpen](h)
	if len(a) != 1 || a[0].Ask.Kind != "permission" || a[0].Ask.Tool != "write" || a[0].Ask.ToolUseID == "" ||
		a[0].Ask.Input.(map[string]any)["file_path"] != "/tmp/dsh-drv/w1/out.txt" || !strings.Contains(a[0].Ask.Description, "escalate sandbox") {
		t.Fatalf("asks %+v", a)
	}
	if st := states(h); !reflect.DeepEqual(st, []string{"running", "waiting", "running", "idle"}) {
		t.Errorf("states %v", st)
	}
	if c := find[agent.AskCancel](h); len(c) != 1 || c[0].RequestID != a[0].Ask.RequestID {
		t.Errorf("withdrawn %+v", c)
	}
}

func TestQuestion(t *testing.T) {
	h := replay(t, "question", newSetup("5f0c1d2e-0004-4000-8000-000000000004"))
	a := find[agent.AskOpen](h)
	if len(a) != 1 || a[0].Ask.Kind != "question" || a[0].Ask.ToolUseID == "" {
		t.Fatalf("asks %+v", a)
	}
	qs := a[0].Ask.Input.(map[string]any)["questions"].([]any)
	if len(qs) != 2 || qs[1].(map[string]any)["multiSelect"] != true || qs[0].(map[string]any)["question"] != "Which colour?" {
		t.Errorf("questions %v", qs)
	}
}

func TestPlan(t *testing.T) {
	s := newSetup("5f0c1d2e-0005-4000-8000-000000000005")
	s.mode = "plan"
	h := replay(t, "plan", s)
	a := find[agent.AskOpen](h)
	if len(a) != 2 || a[0].Ask.Kind != "plan" || a[0].Ask.Input.(map[string]any)["plan"] != "# Plan\n\n1. Do a thing.\n2. Do another." || a[0].Ask.ToolUseID == "" {
		t.Fatalf("asks %+v", a)
	}
	if hello := find[agent.Hello](h); hello[0].Mode != "plan" {
		t.Errorf("hello %+v", hello)
	}
	// Approved: it goes on in the preset beneath, which a later switch starts from.
	if h.d.mode != "danger-full-access" || h.d.plan {
		t.Errorf("mode %s, plan %v", h.d.mode, h.d.plan)
	}
	if n := len(find[agent.TurnEnd](h)); n != 2 {
		t.Errorf("%d turns", n)
	}
}

func TestCompact(t *testing.T) {
	h := replay(t, "compact", newSetup("5f0c1d2e-0006-4000-8000-000000000006"))
	k := h.kinds()
	i := slices.Index(k, "TurnEnd")
	want := []string{"Taken", "TurnStart", "StateChange:compacting", "Compacted", "StateChange:idle", "TurnEnd"}
	if got := k[i+1:]; !reflect.DeepEqual(got, want) {
		t.Fatalf("events after the first turn\n  %v\nwant\n  %v", got, want)
	}
	ends := find[agent.TurnEnd](h)
	if !ends[1].CompactOnly || find[agent.Compacted](h)[0].Failed || find[agent.TurnStart](h)[1].Input != "in-4" {
		t.Errorf("end %+v", ends)
	}
	if _, err := h.d.Context(context.Background()); err == nil {
		t.Error("a context known right after the compaction")
	}
	if h.d.perm != "read-only" || h.d.plan {
		t.Errorf("perm %s plan %v", h.d.perm, h.d.plan)
	}
}

// An interrupt shuts the process down: the turn ends at once, and Interrupt returns once the
// process is gone.
func TestInterrupt(t *testing.T) {
	s := newSetup("5f0c1d2e-0007-4000-8000-000000000007")
	s.exit = true
	h := replay(t, "interrupt", s)
	if e := find[agent.TurnEnd](h); len(e) != 1 || h.d.State() != agent.Idle {
		t.Errorf("events %v", h.kinds())
	}
	if err := h.d.Send(context.Background(), agent.Message{ID: "x", Text: "late"}); !errors.Is(err, ErrNotAttached) {
		t.Errorf("send after: %v", err)
	}
}

// The next process resumes the session, with another model and effort, and puts it in its mode:
// dsh said nothing of it yet, so every switch goes.
func TestResume(t *testing.T) {
	s := newSetup("5f0c1d2e-0007-4000-8000-000000000007")
	s.resume, s.model, s.modelID, s.effort, s.mode = true, "deepseek-official/deepseek-v4-pro", "deepseek-v4-pro", "max", "workspace-write"
	h := replay(t, "resume", s)
	st := find[agent.Started](h)
	if st[0] != (agent.Started{Session: s.session, Model: "deepseek-official/deepseek-v4-pro", Effort: "max"}) || len(st) != 1 {
		t.Errorf("started %+v", st)
	}
	if e := find[agent.TurnEnd](h); len(e) != 1 {
		t.Errorf("events %v", h.kinds())
	}
	ctx := context.Background()
	if err := h.d.SetModel(ctx, "deepseek-v4-pro"); err != nil {
		t.Errorf("the same model: %v", err)
	}
	if err := h.d.SetModel(ctx, "default"); !errors.Is(err, agent.ErrRestart) {
		t.Errorf("another model: %v", err)
	}
	if err := h.d.SetEffort(ctx, "max"); err != nil {
		t.Errorf("the same effort: %v", err)
	}
	if err := h.d.SetEffort(ctx, "low"); !errors.Is(err, agent.ErrRestart) {
		t.Errorf("another effort: %v", err)
	}
	if err := h.d.SetEffort(ctx, "medium"); err == nil || errors.Is(err, agent.ErrRestart) {
		t.Errorf("an effort that is not: %v", err)
	}
}

// Replayed by a daemon taking the process up, the output brings the state back and writes nothing.
func TestAdopt(t *testing.T) {
	out, err := os.ReadFile(filepath.Join("testdata", "approval", "out.log"))
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSuffix(out, []byte("\n")), []byte("\n"))
	cut := slices.IndexFunc(lines, func(l []byte) bool { return bytes.Contains(l, []byte(`"approval/request"`)) })
	var evs []agent.Event
	var mu sync.Mutex
	d := New(Options{Emit: func(e agent.Event) { mu.Lock(); evs = append(evs, e); mu.Unlock() }, Logf: t.Logf})
	var wrote [][]byte
	d.Attach(func(l []byte) error { wrote = append(wrote, l); return nil }, 3)
	for _, l := range lines[:cut+1] {
		d.Feed(l, true)
	}
	time.Sleep(20 * time.Millisecond)
	if len(wrote) != 0 {
		t.Fatalf("wrote %q", wrote)
	}
	if d.State() != agent.Waiting || len(d.asks) != 1 || d.session != "5f0c1d2e-0003-4000-8000-000000000003" || d.perm != "read-only" || d.model != "deepseek-official/deepseek-flash" {
		t.Fatalf("state %s, asks %d, session %s, perm %s, model %s", d.State(), len(d.asks), d.session, d.perm, d.model)
	}
	// The ask is still answerable on the same process.
	if err := d.Answer(context.Background(), api.Answer{RequestID: d.asks[0].RequestID, Allow: true}); err != nil {
		t.Fatal(err)
	}
	var res map[string]any
	if len(wrote) != 1 || json.Unmarshal(wrote[0], &res) != nil || res["result"].(map[string]any)["outcome"] != "allowed-once" {
		t.Errorf("wrote %q", wrote)
	}
	for _, l := range lines[cut+1:] {
		d.Feed(l, false)
	}
	if d.State() != agent.Idle {
		t.Errorf("state %s", d.State())
	}
}

func TestAnswers(t *testing.T) {
	var qs []question
	_ = json.Unmarshal([]byte(`[{"id":"a","question":"One?","options":[{"label":"X"},{"label":"Y"}]},
		{"id":"b","question":"Many?","multiSelect":true,"options":[{"label":"S"},{"label":"M"}]},
		{"id":"c","question":"Skipped?"},{"id":"d","question":"Words?","options":[{"label":"Z"}]}]`), &qs)
	got, _ := json.Marshal(answers(qs, map[string]string{"One?": "Y", "Many?": "S, M, XL", "Words?": "my own"}))
	want := `[{"id":"a","selected":["Y"]},{"custom":"XL","id":"b","selected":["S","M"]},{"id":"c","selected":[]},{"custom":"my own","id":"d","selected":[]}]`
	if string(got) != want {
		t.Errorf("answers\n  %s\nwant\n  %s", got, want)
	}
}

func TestRunsItself(t *testing.T) {
	d := New(Options{})
	for text, want := range map[string]bool{"/compact": true, " /compact ": true, "/compact more": false, "/plan": true, "/plan off": true,
		"/plan do it": true, "/planet": false, "/goal ship it": true, "/目标 x": true, "/压缩": true, "/permission read-only": false, "hi": false} {
		if got := d.RunsItself(text); got != want {
			t.Errorf("%q: %v", text, got)
		}
	}
}

// fakeDsh is a PATH with dsh (npm's symlink to the package's lib/bin.js) and node.
func fakeDsh(t *testing.T) (bin string) {
	bin = t.TempDir()
	root, _ := filepath.Abs(filepath.Join("testdata", "pkg"))
	if err := os.Symlink(filepath.Join(root, "lib", "bin.js"), filepath.Join(bin, "dsh")); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(bin, "node"), []byte("#!/bin/sh\n"), 0o755)
	return bin
}

func TestSpawn(t *testing.T) {
	bin := fakeDsh(t)
	state := t.TempDir()
	pf := filepath.Join(state, "system-prompt.md")
	os.WriteFile(pf, []byte("Use ir {{name}}.\n"), 0o600)
	env := []string{"PATH=" + bin, "ANTHROPIC_API_KEY=a", "CLAUDE_CODE_OAUTH_TOKEN=c", "CLAUDECODE=1", "IRIDIUM_SESSION=parent", "DEEPSEEK_API_KEY=d", "DEEPSEEK_BASE_URL=u", "DSH_HOME=/h"}
	d := New(Options{})
	spec, err := d.Spawn(agent.SpawnOptions{Session: "s1", Resume: true, Cwd: "/w", Env: env, Program: "dsh", SystemPromptFile: pf, Model: "deepseek-v4-pro", Effort: "max", Mode: "plan", Preset: "minimal"})
	if err != nil {
		t.Fatal(err)
	}
	patch := filepath.Join(state, PatchFile)
	if want := []string{filepath.Join(bin, "dsh"), "--profile", "sdk", "--patch", patch}; !reflect.DeepEqual(spec.Argv, want) || spec.Cwd != "/w" {
		t.Errorf("spec %+v", spec)
	}
	slices.Sort(spec.Env)
	if w := []string{"DEEPSEEK_API_KEY=d", "DEEPSEEK_BASE_URL=u", "DSH_HOME=/h", "IRIDIUM_SESSION=s1", "PATH=" + bin}; !reflect.DeepEqual(spec.Env, w) {
		t.Errorf("env %v", spec.Env)
	}
	b, _ := os.ReadFile(patch)
	if !bytes.Contains(b, []byte(`"minimal" Agent preset`)) || !bytes.Contains(b, []byte("    personaSuffix: |-\n      Use ir { {name}}.\n")) {
		t.Errorf("patch %s", b)
	}
	if n := d.next; n.session != "s1" || n.cwd != "/w" || n.model != "deepseek-official/deepseek-v4-pro" || n.provider != LegacyProvider || n.modelID != "deepseek-v4-pro" || n.effort != "max" || n.version != "0.2.2-rc.1" || !n.resume || d.mode != "plan" {
		t.Errorf("launch %+v, mode %s", d.next, d.mode)
	}
	// The defaults: the standard preset, Iridium's default mode, dsh's default model.
	if _, err := d.Spawn(agent.SpawnOptions{Session: "s2", Env: env, Program: "dsh", SystemPromptFile: pf}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(patch); !bytes.Contains(b, []byte(`"standard" Agent preset`)) || d.next.model != DefaultModel || d.next.effort != "" || d.mode != DefaultMode {
		t.Errorf("defaults: %+v %s", d.next, d.mode)
	}
	for name, o := range map[string]agent.SpawnOptions{
		"preset":    {Preset: "cordis"},
		"mode":      {Mode: "auto"},
		"effort":    {Effort: "medium"},
		"no prompt": {SystemPromptFile: "-"},
	} {
		o.Session, o.Env, o.Program = "s3", env, "dsh"
		o.Catalog = func() (agent.Catalog, error) { return testCatalog, nil }
		if o.SystemPromptFile == "-" {
			o.SystemPromptFile = ""
		} else {
			o.SystemPromptFile = pf
		}
		if _, err := d.Spawn(o); err == nil {
			t.Errorf("%s: spawned", name)
		}
	}
	// A program outside a dsh package cannot build its preset.
	other := filepath.Join(bin, "other")
	os.WriteFile(other, []byte("#!/bin/sh\n"), 0o755)
	if _, err := d.Spawn(agent.SpawnOptions{Session: "s4", Env: env, Program: other, SystemPromptFile: pf}); err == nil || !strings.Contains(err.Error(), "cannot build agent preset") {
		t.Errorf("no package: %v", err)
	}
	os.Remove(filepath.Join(bin, "node"))
	if _, err := d.Spawn(agent.SpawnOptions{Session: "s1", Env: env, Program: "dsh", SystemPromptFile: pf}); err == nil {
		t.Error("spawned without node")
	}
}

// The generated patch is what /tmp/dsh-tools/gen-preset.mjs (verified against dsh) made from
// the same package files.
func TestPatch(t *testing.T) {
	root, _ := filepath.Abs(filepath.Join("testdata", "pkg"))
	p, err := FindPackage(filepath.Join(root, "lib", "bin.js"))
	if err != nil || p.Version != "0.2.2-rc.1" {
		t.Fatalf("%+v %v", p, err)
	}
	presets, def, err := p.Presets()
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, pr := range presets {
		ids = append(ids, pr.ID)
	}
	if def != "standard" || strings.Join(ids, " ") != "standard ptc minimal" {
		t.Fatalf("presets %q, default %q", ids, def)
	}
	for _, preset := range presets {
		got, err := p.Patch(preset, "")
		if err != nil {
			t.Fatal(err)
		}
		want, _ := os.ReadFile(filepath.Join("testdata", "presets", preset.ID+".yml"))
		if !bytes.Equal(got, want) {
			t.Errorf("%s:\n%s", preset.ID, got)
		}
	}
	ptc, _ := FindPreset(presets, "ptc")
	got, _ := p.Patch(ptc, "line {{a}}\n\nmore {{{b}}}")
	if !bytes.Contains(got, []byte("    personaPrefix: You are a coding agent powered by the {{model}} model.\n    personaSuffix: |-\n      line { {a}}\n\n      more { { {b}}}\n- insert:")) {
		t.Errorf("suffix:\n%s", got)
	}
	if _, err := FindPreset(presets, "cordis"); err == nil {
		t.Error("cordis")
	}
}

func TestScalar(t *testing.T) {
	for _, c := range []struct {
		in   []string
		want string
	}{
		{[]string{" plain words"}, "plain words"},
		{[]string{" 'it''s'"}, "it's"},
		{[]string{` "a\nb"`}, "a\nb"},
		{[]string{" |", "                one", "", "                two"}, "one\n\ntwo"},
	} {
		if got, err := scalar(c.in); err != nil || got != c.want {
			t.Errorf("%q: %q %v", c.in, got, err)
		}
	}
	if _, err := scalar([]string{" >-", "  folded"}); err == nil {
		t.Error("a folded scalar")
	}
}
