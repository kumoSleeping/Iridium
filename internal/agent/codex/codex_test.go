package codex

import (
	"bytes"
	"context"
	"encoding/json"
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

// The recordings in testdata are Rhodium's (test/fixtures/codex, codex-cli 0.162.1): in.log is
// what Rhodium's probe wrote, each line after out.log's length at the time; out.log is what Codex
// said. The probe is not this driver, so a replay plays Codex: it feeds out.log, does what made
// each in.log line (a message, an interrupt, an answer), and checks the frame the driver writes
// against the probe's where they must agree, with the probe's request ids mapped to the driver's.
// What the driver asks that the probe did not (model/list, account/usage/read) is answered here.
// model-list.json is model/list's answer from the handshake recording.

const prompt = "use ir\n"

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
	ids    map[string]string // the probe's request ids to the driver's
	skip   map[string]bool   // the probe's requests the driver has no part in: their answers are dropped
	ops    sync.WaitGroup
	result map[string]any
	frames map[string][]map[string]any // what the driver wrote, by method
	thread string
}

type opts struct {
	mode, model string
	resume      string // a thread to resume
}

func replay(t *testing.T, name string, o opts) *harness {
	t.Helper()
	dir := filepath.Join("testdata", name)
	ins := readIn(t, filepath.Join(dir, "in.log"))
	out, err := os.ReadFile(filepath.Join(dir, "out.log"))
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, writes: make(chan map[string]any, 64), ids: map[string]string{}, skip: map[string]bool{}, result: map[string]any{}, frames: map[string][]map[string]any{}}
	h.d = New(Options{Emit: h.record, IDPrefix: "ir-test-", Logf: t.Logf})
	bin := t.TempDir()
	os.WriteFile(filepath.Join(bin, "codex"), []byte("#!/bin/sh\n"), 0o755)
	pf := filepath.Join(bin, "prompt.md")
	os.WriteFile(pf, []byte(prompt), 0o600)
	if _, err := h.d.Spawn(agent.SpawnOptions{Session: "s1", Cwd: "/w", Env: []string{"PATH=" + bin}, Program: "codex", SystemPromptFile: pf, Model: o.model, Mode: o.mode, Resume: o.resume != "", AgentSession: o.resume}); err != nil {
		t.Fatal(err)
	}
	h.d.Attach(h.write, 1)

	var off int64
	feedUntil := func(at int64) {
		// Only whole lines before at: a few recordings' offsets fall inside a line.
		for off < int64(len(out)) {
			i := bytes.IndexByte(out[off:], '\n')
			if off+int64(i)+1 > at {
				return
			}
			line := out[off : off+int64(i)]
			off += int64(i) + 1
			if l, ok := h.mapAnswer(line); ok {
				h.d.Feed(l, false)
			}
		}
	}
	for _, in := range ins {
		feedUntil(in.at)
		h.act(in.msg)
	}
	feedUntil(int64(len(out)))
	h.ops.Wait()
	time.Sleep(20 * time.Millisecond)
	select {
	case extra := <-h.writes:
		t.Fatalf("an extra write: %v", extra)
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
		var m map[string]any
		if err != nil || json.Unmarshal(rest, &m) != nil {
			t.Fatalf("in.log: %q", l)
		}
		ins = append(ins, inLine{at, m})
	}
	return ins
}

func idOf(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// mapAnswer rewrites an answer to a probe's request to the driver's id; false: drop it.
func (h *harness) mapAnswer(line []byte) ([]byte, bool) {
	var m map[string]any
	if json.Unmarshal(line, &m) != nil || m["method"] != nil || m["id"] == nil {
		return line, true
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	pid := idOf(m["id"])
	if h.skip[pid] {
		return nil, false
	}
	did, ok := h.ids[pid]
	if !ok {
		h.t.Fatalf("an answer to %s, which the driver never asked", pid)
	}
	m["id"] = did
	b, _ := json.Marshal(m)
	return b, true
}

// write is the driver's Writer: what the probe never asked is answered at once; the rest goes to
// act, which matches it to an in.log line.
func (h *harness) write(line []byte) error {
	var m map[string]any
	if err := json.Unmarshal(line, &m); err != nil {
		h.t.Errorf("the driver wrote %q", line)
		return nil
	}
	method, _ := m["method"].(string)
	h.mu.Lock()
	h.frames[method] = append(h.frames[method], m)
	h.mu.Unlock()
	switch method {
	case "model/list":
		models, _ := os.ReadFile("testdata/model-list.json")
		go h.d.Feed([]byte(fmt.Sprintf(`{"id":%q,"result":%s}`, m["id"], models)), false)
		return nil
	case "account/usage/read":
		go h.d.Feed([]byte(fmt.Sprintf(`{"id":%q,"error":{"code":-32601,"message":"unknown method"}}`, m["id"])), false)
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

func (h *harness) next(want string) map[string]any {
	h.t.Helper()
	select {
	case got := <-h.writes:
		if want != "" && got["method"] != want {
			h.t.Fatalf("wrote %v, want a %s", got, want)
		}
		return got
	case <-time.After(3 * time.Second):
		h.t.Fatalf("the driver wrote nothing; want %s", want)
	}
	return nil
}

func (h *harness) mapID(probe, got map[string]any) {
	h.mu.Lock()
	h.ids[idOf(probe["id"])] = idOf(got["id"])[1 : len(idOf(got["id"]))-1]
	h.mu.Unlock()
}

func params(m map[string]any) map[string]any { p, _ := m["params"].(map[string]any); return p }

// act does what wrote the probe's line and checks the driver's frame.
func (h *harness) act(probe map[string]any) {
	h.t.Helper()
	ctx := context.Background()
	method, _ := probe["method"].(string)
	pp := params(probe)
	switch method {
	case "initialize":
		got := h.next("initialize")
		h.mapID(probe, got)
		p := params(got)
		ci := p["clientInfo"].(map[string]any)
		caps := p["capabilities"].(map[string]any)
		if ci["name"] != "iridium" || caps["experimentalApi"] != false || !slices.Contains(caps["optOutNotificationMethods"].([]any), any("item/commandExecution/outputDelta")) {
			h.t.Errorf("initialize: %v", p)
		}
	case "initialized":
		h.next("initialized")
	case "thread/start", "thread/resume":
		got := h.next(method)
		h.mapID(probe, got)
		p := params(got)
		if p["cwd"] != "/w" || p["sandbox"] != "danger-full-access" || p["developerInstructions"] != prompt || !reflect.DeepEqual(p["approvalPolicy"], policyJSON(h.d.mode)) {
			h.t.Errorf("%s: %v", method, p)
		}
		if method == "thread/resume" && (p["threadId"] != pp["threadId"] || p["excludeTurns"] != true) {
			h.t.Errorf("resume: %v", p)
		}
	case "turn/start", "turn/steer":
		input := pp["input"].([]any)[0].(map[string]any)
		id, _ := pp["clientUserMessageId"].(string)
		if id == "" {
			id = fmt.Sprintf("input-%v", probe["id"])
		}
		h.op("send", func() (any, error) {
			return nil, h.d.Send(ctx, agent.Message{ID: id, Text: input["text"].(string), Human: true})
		})
		got := h.next("turn/start")
		h.mapID(probe, got)
		p := params(got)
		h.thread = pp["threadId"].(string)
		if got["id"] != id || p["threadId"] != pp["threadId"] || p["clientUserMessageId"] != id || !reflect.DeepEqual(p["input"], pp["input"]) ||
			!reflect.DeepEqual(p["sandboxPolicy"], map[string]any{"type": "dangerFullAccess"}) || !reflect.DeepEqual(p["approvalPolicy"], policyJSON(h.d.mode)) {
			h.t.Errorf("turn/start\n  %v\nprobe\n  %v", got, probe)
		}
	case "turn/interrupt":
		h.op("interrupt", func() (any, error) { return nil, h.d.Interrupt(ctx) })
		got := h.next("turn/interrupt")
		h.mapID(probe, got)
		if !reflect.DeepEqual(params(got), pp) {
			h.t.Errorf("interrupt %v, probe %v", got, probe)
		}
	case "thread/compact/start":
		h.op("compact", func() (any, error) { return nil, h.d.Send(ctx, agent.Message{ID: "compact-1", Text: "/compact"}) })
		got := h.next("thread/compact/start")
		h.mapID(probe, got)
		if !reflect.DeepEqual(params(got), pp) {
			h.t.Errorf("compact %v, probe %v", got, probe)
		}
	case "account/rateLimits/read":
		h.op("quota", func() (any, error) { return h.d.Quota(ctx) })
		got := h.next("account/rateLimits/read")
		h.mapID(probe, got)
	case "":
		// An answer to Codex's request: yours, if the driver put it to you; else its own.
		rid := idOf(probe["id"])
		res := probe["result"].(map[string]any)
		allow := res["decision"] == "accept" || res["action"] == "accept"
		h.mu.Lock()
		open := false
		for _, e := range h.events {
			if a, ok := e.(agent.AskOpen); ok && a.Ask.RequestID == rid {
				open = true
			}
		}
		h.mu.Unlock()
		if open {
			if err := h.d.Answer(ctx, api.Answer{RequestID: rid, Allow: allow}); err != nil {
				return // withdrawn before the probe's answer: nothing to write
			}
		}
		got := h.next("")
		if idOf(got["id"]) != rid || !reflect.DeepEqual(got["result"], probe["result"]) {
			h.t.Errorf("answered %v, probe %v", got, probe)
		}
	default:
		h.mu.Lock()
		h.skip[idOf(probe["id"])] = true
		h.mu.Unlock()
	}
}

func policyJSON(mode string) any {
	b, _ := json.Marshal(policy(mode))
	var v any
	_ = json.Unmarshal(b, &v)
	return v
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
		switch e := e.(type) {
		case agent.StateChange:
			s += ":" + string(e.State)
		case agent.TextDelta:
			if len(out) > 0 && out[len(out)-1] == "TextDelta" {
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

func TestTurn(t *testing.T) {
	h := replay(t, "turn", opts{mode: "never", model: "gpt-6-luna:low"})
	want := []string{"Started", "Hello", "TurnStart", "StateChange:running", "ToolStart", "ToolDone", "ToolStart", "ToolDone", "TextDelta", "MessageDone", "TurnUsage", "StateChange:idle", "TurnEnd"}
	if got := h.kinds(); !reflect.DeepEqual(got, want) {
		t.Fatalf("events\n  %v\nwant\n  %v", got, want)
	}
	if s := find[agent.Started](h)[0]; s.Session != "00000000-0000-7000-8000-8d5c554c3185" || s.Model != "gpt-6-luna" {
		t.Errorf("started %+v", s)
	}
	if hello := find[agent.Hello](h)[0]; hello.Version != "0.162.1" || hello.Mode != "never" || len(hello.Models) != 8 {
		t.Errorf("hello %+v", hello)
	}
	ts := find[agent.ToolStart](h)
	if ts[0].Name != "exec_command" || ts[0].Input.(map[string]any)["command"] != "env | grep ^RHODIUM_ | sort" || ts[1].Name != "apply_patch" {
		t.Errorf("tools %+v", ts)
	}
	start := find[agent.TurnStart](h)[0]
	if start.Input != "input-2" {
		t.Errorf("turn began by %q", start.Input)
	}
	// The probe sent the effort and the model it opened the thread with; the driver carries both.
	ts0 := h.frames["turn/start"][0]["params"].(map[string]any)
	if ts0["model"] != "gpt-6-luna" || ts0["effort"] != "low" || h.frames["thread/start"][0]["params"].(map[string]any)["model"] != "gpt-6-luna" {
		t.Errorf("turn/start %v", ts0)
	}
	u := find[agent.TurnUsage](h)[0]
	if u.Tokens != (agent.Tokens{Input: (14851 - 11520) + (14958 - 11520) + (15031 - 14720), Output: 48 + 49 + 5, CacheRead: 11520 + 11520 + 14720}) || u.Context != 15036 || u.Total != nil {
		t.Errorf("usage %+v", u)
	}
	if e := find[agent.TurnEnd](h)[0]; e.Reply != "ok" || e.CompactOnly || e.Background {
		t.Errorf("end %+v", e)
	}
	q := h.result["quota"].(*agent.Usage)
	if !reflect.DeepEqual(q.Windows, []api.QuotaWindow{{Group: "weekly", Percent: 35, ResetsAt: 1767737495000}}) || q.Models["gpt-6-luna"].Input != 44840-37760 {
		t.Errorf("quota %+v", q)
	}
	c, err := h.d.Context(context.Background())
	if err != nil || c.Used != 15036 || c.Max == 0 || len(c.Categories) != 0 {
		t.Errorf("context %+v %v", c, err)
	}
}

// Every message is a turn/start; one written mid-turn joins it. The user messages carry the
// inputs' ids, which say they are taken.
func TestStartMidTurn(t *testing.T) {
	h := replay(t, "start-mid-turn", opts{mode: "never", model: "gpt-6-luna:low"})
	var taken []string
	for _, e := range find[agent.Taken](h) {
		taken = append(taken, e.InputID)
	}
	if !reflect.DeepEqual(taken, []string{"c-1", "c-2", "c-3", "c-4"}) {
		t.Errorf("taken %v", taken)
	}
	starts := find[agent.TurnStart](h)
	if len(starts) != 2 || starts[0].Input != "c-1" || starts[1].Input != "c-3" || len(find[agent.TurnEnd](h)) != 2 {
		t.Errorf("starts %+v, ends %d", starts, len(find[agent.TurnEnd](h)))
	}
}

func TestInterrupt(t *testing.T) {
	h := replay(t, "interrupt", opts{mode: "never", model: "gpt-6-luna:low"})
	if len(find[agent.ToolStart](h)) != 1 || len(find[agent.ToolDone](h)) != 1 || len(find[agent.TurnEnd](h)) != 1 {
		t.Errorf("events %v", h.kinds())
	}
	if h.d.State() != agent.Idle {
		t.Errorf("state %s", h.d.State())
	}
}

// Interrupted with an approval open: withdrawn; the probe's late answer finds nothing to answer.
func TestInterruptWithAskOpen(t *testing.T) {
	h := replay(t, "interrupt-pending-approval", opts{mode: "on-request", model: "gpt-6-luna:low"})
	opened, withdrawn := find[agent.AskOpen](h), find[agent.AskCancel](h)
	if len(opened) != 1 || len(withdrawn) != 1 || opened[0].Ask.Tool != "exec_command" || opened[0].Ask.Input.(map[string]any)["command"] != "touch pending.txt" {
		t.Fatalf("opened %+v, withdrawn %+v", opened, withdrawn)
	}
	var states []string
	for _, k := range h.kinds() {
		if s, ok := strings.CutPrefix(k, "StateChange:"); ok {
			states = append(states, s)
		}
	}
	if !reflect.DeepEqual(states, []string{"running", "waiting", "idle"}) {
		t.Errorf("states %v", states)
	}
}

func TestApprovalDecline(t *testing.T) {
	h := replay(t, "approval-decline", opts{mode: "on-request", model: "gpt-6-luna:low"})
	if a := find[agent.AskOpen](h); len(a) != 1 || a[0].Ask.Kind != "permission" || a[0].Ask.ToolUseID == "" || a[0].Ask.Description == "" {
		t.Fatalf("asks %+v", a)
	}
}

// An MCP tool's approval is an elicitation marked so, asked as the tool it is about; any other
// elicitation the driver declines itself.
func TestMCPElicitation(t *testing.T) {
	h := replay(t, "mcp-elicitation", opts{mode: "untrusted", model: "gpt-6-luna:low"})
	a := find[agent.AskOpen](h)
	if len(a) != 2 || a[0].Ask.Tool != "mcp__rhprobe__note" || a[1].Ask.Tool != "mcp__rhprobe__ask" {
		t.Fatalf("asks %+v", a)
	}
	if h.d.Unknown()["request/mcpServer/elicitation/request"] != 1 {
		t.Errorf("unknown %v", h.d.Unknown())
	}
}

func TestCompact(t *testing.T) {
	h := replay(t, "compact", opts{mode: "never", resume: "00000000-0000-7000-8000-8d5c554c3185"})
	want := []string{"Started", "Hello", "Taken", "TurnStart", "StateChange:running", "StateChange:compacting", "Compacted", "StateChange:running", "StateChange:idle", "TurnEnd"}
	if got := h.kinds(); !reflect.DeepEqual(got, want) {
		t.Fatalf("events\n  %v\nwant\n  %v", got, want)
	}
	if c := find[agent.Compacted](h)[0]; c.Before != 19328 || c.After != 8204 || c.Failed {
		t.Errorf("compacted %+v", c)
	}
	if e := find[agent.TurnEnd](h)[0]; !e.CompactOnly {
		t.Errorf("end %+v", e)
	}
}

// A subagent is a background task until it completes; its own thread's turns are not the
// session's.
func TestSubagent(t *testing.T) {
	h := replay(t, "ultra-agents", opts{mode: "never", model: "gpt-5.6-terra:ultra"})
	tasks := find[agent.Tasks](h)
	if len(tasks) != 2 || len(tasks[0].Tasks) != 1 || tasks[0].Tasks[0].Description != "/root/touch_file" || len(tasks[1].Tasks) != 0 {
		t.Fatalf("tasks %+v", tasks)
	}
	if n := len(find[agent.TurnStart](h)); n != 1 {
		t.Errorf("%d turns", n)
	}
	if e := find[agent.TurnEnd](h)[0]; !e.Background {
		t.Errorf("end %+v", e)
	}
}

func TestUsageLimit(t *testing.T) {
	h := replay(t, "usage-limit", opts{mode: "never", model: "gpt-6-luna:low"})
	q := find[agent.QuotaHit](h)
	if len(q) != 1 || !q[0].Exhausted || q[0].ResetsAt != 1767737495000 {
		t.Fatalf("quota %+v", q)
	}
	if e := find[agent.TurnEnd](h)[0]; !strings.HasPrefix(e.Reply, "You’ve hit your usage limit") {
		t.Errorf("end %+v", e)
	}
}

func TestDeveloperResume(t *testing.T) {
	replay(t, "developer-resume", opts{mode: "never", resume: "00000000-0000-7000-8000-e4437708a374"})
}

// Replaying an earlier daemon's lines brings the thread back and never writes.
func TestReplayWritesNothing(t *testing.T) {
	out, _ := os.ReadFile("testdata/mcp-elicitation/out.log")
	var writes int
	d := New(Options{Logf: func(string, ...any) {}})
	d.Attach(func([]byte) error { writes++; return nil }, 1)
	for _, l := range bytes.Split(bytes.TrimSuffix(out, []byte("\n")), []byte("\n")) {
		d.Feed(l, true)
	}
	time.Sleep(50 * time.Millisecond)
	if writes != 0 || d.Thread() != "00000000-0000-7000-8000-250955a153ca" || d.modeNow() != "untrusted" {
		t.Errorf("%d writes, thread %q, mode %s", writes, d.Thread(), d.modeNow())
	}
}

func (d *Driver) modeNow() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.mode
}

func TestSpawn(t *testing.T) {
	bin := t.TempDir()
	os.WriteFile(filepath.Join(bin, "codex"), []byte("#!/bin/sh\n"), 0o755)
	env := []string{"PATH=" + bin, "CODEX_THREAD_ID=parent", "CODEX_HOME=/c", "CLAUDE_CODE_OAUTH_TOKEN=x", "ANTHROPIC_API_KEY=y", "CLAUDE_CONFIG_DIR=/cc", "OPENAI_API_KEY=z", "IRIDIUM_SESSION=parent"}
	d := New(Options{})
	spec, err := d.Spawn(agent.SpawnOptions{Session: "s1", Cwd: "/w", Env: env, Program: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(spec.Argv[1:], " ") != "app-server --listen stdio://" {
		t.Errorf("argv %v", spec.Argv)
	}
	slices.Sort(spec.Env)
	if want := []string{"CODEX_HOME=/c", "IRIDIUM_SESSION=s1", "OPENAI_API_KEY=z", "PATH=" + bin}; !reflect.DeepEqual(spec.Env, want) {
		t.Errorf("env %v", spec.Env)
	}
	if _, err := d.Spawn(agent.SpawnOptions{Session: "s1", Env: env, Program: "codex", Mode: "bogus"}); err == nil {
		t.Error("took a bogus mode")
	}
	if !reflect.DeepEqual(policyJSON("granular"), map[string]any{"granular": map[string]any{"sandbox_approval": true, "rules": true, "skill_approval": true, "request_permissions": true, "mcp_elicitations": true}}) {
		t.Errorf("granular %v", policyJSON("granular"))
	}
}

func TestTypedCommand(t *testing.T) {
	for in, want := range map[string]string{
		`/usr/bin/zsh -lc 'env | grep ''^RHODIUM_ | sort'`: "env | grep ^RHODIUM_ | sort",
		`/usr/bin/bash -lc "echo \"hi\""`:                  `echo "hi"`,
		`ls -la`:                                           "ls -la",
		`/bin/bash -lc 'open`:                              `/bin/bash -lc 'open`,
	} {
		if got := typedCommand(in); got != want {
			t.Errorf("%s: %q", in, got)
		}
	}
}

// account/usage/read's estimate for the thread, by model.
func TestThreadUsage(t *testing.T) {
	d := New(Options{Logf: t.Logf})
	d.mu.Lock()
	d.thread, d.model = "t1", "gpt-6-luna"
	close(d.ready)
	d.mu.Unlock()
	d.Attach(func(line []byte) error {
		var m map[string]any
		json.Unmarshal(line, &m)
		var res string
		switch m["method"] {
		case "account/rateLimits/read":
			res = `{"error":{"code":-32600,"message":"codex account authentication required to read rate limits"}}`
		case "account/usage/read":
			if params(m)["threadId"] != "t1" {
				t.Errorf("usage/read %v", m)
			}
			res = `{"result":{"summary":{},"dailyUsageBuckets":null,"threadUsage":{"threadId":"t1","estimatedUsageCreditsMicros":5,"estimatedUsageUsdMicros":1500,"groups":[{"model":"gpt-6-luna","reasoningEffort":"low","speed":null,"estimatedUsageCreditsMicros":5,"netNewInputTokens":10,"cachedInputTokens":90,"inputTokens":100,"outputTokens":7,"totalTokens":107}]}}}`
		}
		go d.Feed([]byte(fmt.Sprintf(`{"id":%q,%s`, m["id"], res[1:])), false)
		return nil
	}, 0)
	d.mu.Lock()
	d.thread = "t1"
	d.mu.Unlock()
	u, err := d.Quota(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if u.CostUSD != 0.0015 || u.Models["gpt-6-luna"] != (agent.ModelUsage{Tokens: agent.Tokens{Input: 10, CacheRead: 90, Output: 7}}) || len(u.Windows) != 0 {
		t.Errorf("usage %+v", u)
	}
}
