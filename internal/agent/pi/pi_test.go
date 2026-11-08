package pi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
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

// The recordings in testdata are Rhodium's (test/fixtures/pi, pi 1.3.0 with DeepSeek): in.log is
// what Rhodium's probe wrote, each line after out.log's length at the time; out.log is what pi
// said. The approval recordings' title key is renamed from rhodium to iridium (meta: edited). A
// replay feeds out.log, does what made each in.log line, and checks the driver writes the same
// frame, with the probe's ids mapped to the driver's. What the driver asks that the probe did not
// (get_state and get_available_models at attach, thinking levels) is answered here.

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
	ids    map[string]string
	skip   map[string]bool
	ops    sync.WaitGroup
	result map[string]any
}

func replay(t *testing.T, name string) *harness {
	t.Helper()
	dir := filepath.Join("testdata", name)
	ins := readIn(t, filepath.Join(dir, "in.log"))
	out, err := os.ReadFile(filepath.Join(dir, "out.log"))
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, writes: make(chan map[string]any, 64), ids: map[string]string{}, skip: map[string]bool{}, result: map[string]any{}}
	h.d = New(Options{Emit: h.record, IDPrefix: "ir-test-", Logf: t.Logf})
	h.d.spawned = true
	h.d.Attach(h.write, 1)
	// The attach's get_state answered first, as pi answers it before any prompt's run.
	for deadline := time.Now().Add(2 * time.Second); len(find[agent.Started](h)) == 0; time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("no get_state at attach")
		}
	}
	var off int64
	feedUntil := func(at int64) {
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

func (h *harness) mapAnswer(line []byte) ([]byte, bool) {
	var m map[string]any
	if json.Unmarshal(line, &m) != nil || m["type"] != "response" {
		return line, true
	}
	id, _ := m["id"].(string)
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.skip[id] {
		return nil, false
	}
	if to, ok := h.ids[id]; ok {
		m["id"] = to
	}
	b, _ := json.Marshal(m)
	return b, true
}

// synthetic answers to what only the driver asks.
var synthetic = map[string]string{
	"get_state":                     `{"model":{"id":"deepseek-flash","name":"DeepSeek V4.1 Flash","provider":"deepseek"},"thinkingLevel":"high","sessionId":"00000000-0000-4000-8000-4c4a2a950dc8"}`,
	"get_available_models":          `{"models":[{"id":"deepseek-flash","name":"DeepSeek V4.1 Flash","provider":"deepseek"},{"id":"deepseek-v4-pro","name":"DeepSeek V4 Pro","provider":"deepseek"}]}`,
	"get_available_thinking_levels": `{"levels":["off","low","medium","high","max"]}`,
}

func (h *harness) write(line []byte) error {
	var m map[string]any
	if err := json.Unmarshal(line, &m); err != nil {
		h.t.Errorf("the driver wrote %q", line)
		return nil
	}
	if data, ok := synthetic[m["type"].(string)]; ok {
		go h.d.Feed([]byte(fmt.Sprintf(`{"id":%q,"type":"response","command":%q,"success":true,"data":%s}`, m["id"], m["type"], data)), false)
		return nil
	}
	if m["type"] == "set_thinking_level" {
		h.mu.Lock()
		h.result["level"] = m["level"]
		h.mu.Unlock()
		go h.d.Feed([]byte(fmt.Sprintf(`{"id":%q,"type":"response","command":"set_thinking_level","success":true}`, m["id"])), false)
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
	select {
	case got := <-h.writes:
		return got
	case <-time.After(3 * time.Second):
		h.t.Fatal("the driver wrote nothing")
	}
	return nil
}

// compare checks the driver wrote the probe's frame, its own id aside, which it maps.
func (h *harness) compare(got, probe map[string]any) {
	h.t.Helper()
	pid, _ := probe["id"].(string)
	if gid, _ := got["id"].(string); probe["type"] != "extension_ui_response" && gid != pid {
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
		h.t.Errorf("wrote\n  %v\nprobe\n  %v", got, probe)
	}
}

func (h *harness) act(probe map[string]any) {
	h.t.Helper()
	ctx := context.Background()
	id, _ := probe["id"].(string)
	switch probe["type"] {
	case "prompt":
		h.op("send", func() (any, error) {
			return nil, h.d.Send(ctx, agent.Message{ID: id, Text: probe["message"].(string), Human: true})
		})
		h.compare(h.next(), probe)
	case "compact":
		h.op("compact", func() (any, error) { return nil, h.d.Send(ctx, agent.Message{ID: id, Text: "/compact"}) })
		h.compare(h.next(), probe)
	case "abort":
		h.op("abort", func() (any, error) { return nil, h.d.Interrupt(ctx) })
		h.compare(h.next(), probe)
	case "get_session_stats":
		h.op("quota", func() (any, error) { return h.d.Quota(ctx) })
		h.compare(h.next(), probe)
	case "set_model":
		model := probe["provider"].(string) + "/" + probe["modelId"].(string)
		h.ops.Add(1)
		go func() {
			defer h.ops.Done()
			err := h.d.SetModel(ctx, model)
			h.mu.Lock()
			h.result["set_model "+model] = err
			h.mu.Unlock()
		}()
		h.compare(h.next(), probe)
	case "extension_ui_response":
		value := probe["value"].(string)
		msg, _ := strings.CutPrefix(value, "deny: ")
		if value == "allow" || value == "deny" {
			msg = ""
		}
		if err := h.d.Answer(ctx, api.Answer{RequestID: id, Allow: value == "allow", Message: msg}); err != nil {
			return // withdrawn before the probe's answer
		}
		h.compare(h.next(), probe)
	default:
		h.mu.Lock()
		h.skip[id] = true
		h.mu.Unlock()
	}
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
	h := replay(t, "turn")
	k := h.kinds()
	// Started and Hello come from the attach's own questions, whenever they are answered.
	k = slices.DeleteFunc(k, func(s string) bool { return s == "Started" || s == "Hello" })
	want := []string{"TurnStart", "StateChange:running", "Taken", "ToolStart", "ToolDone", "TextDelta", "MessageDone", "TurnUsage", "StateChange:idle", "TurnEnd"}
	if !reflect.DeepEqual(k, want) {
		t.Fatalf("events\n  %v\nwant\n  %v", k, want)
	}
	if s := find[agent.TurnStart](h)[0]; s.Input != "prompt-0" {
		t.Errorf("start %+v", s)
	}
	if e := find[agent.TurnEnd](h)[0]; e.Reply != "done" {
		t.Errorf("end %+v", e)
	}
	u := find[agent.TurnUsage](h)[0]
	if u.Tokens.Output == 0 || u.Tokens.CacheRead == 0 || u.CostUSD == nil || *u.CostUSD <= 0 || u.Context != 141+1664 {
		t.Errorf("usage %+v", u)
	}
	hello := find[agent.Hello](h)
	if len(hello) != 1 || hello[0].Mode != "auto" || len(hello[0].Models) != 2 || hello[0].Models[0].Value != "deepseek/deepseek-flash" {
		t.Errorf("hello %+v", hello)
	}
	if s := find[agent.Started](h); len(s) == 0 || s[0].Model != "deepseek/deepseek-flash" || s[0].Effort != "high" {
		t.Errorf("started %+v", s)
	}
}

// A message while the turn runs steers it, and joins once the tool call ends: one turn.
func TestSteer(t *testing.T) {
	h := replay(t, "steer")
	var taken []string
	for _, e := range find[agent.Taken](h) {
		taken = append(taken, e.InputID)
	}
	if !reflect.DeepEqual(taken, []string{"prompt-0", "prompt-55"}) || len(find[agent.TurnEnd](h)) != 1 {
		t.Errorf("taken %v, ends %d", taken, len(find[agent.TurnEnd](h)))
	}
}

func TestAbort(t *testing.T) {
	h := replay(t, "abort")
	if len(find[agent.TurnEnd](h)) != 1 || h.d.State() != agent.Idle {
		t.Errorf("events %v", h.kinds())
	}
}

// Aborted with an approval open: withdrawn at the settle; the late answer finds nothing.
func TestAbortWithAskOpen(t *testing.T) {
	h := replay(t, "abort-pending-approval")
	opened, withdrawn := find[agent.AskOpen](h), find[agent.AskCancel](h)
	if len(opened) != 1 || len(withdrawn) != 1 || opened[0].Ask.Tool != "bash" || opened[0].Ask.ToolUseID == "" {
		t.Fatalf("opened %+v, withdrawn %+v", opened, withdrawn)
	}
}

func TestApprovals(t *testing.T) {
	h := replay(t, "approval-allow")
	if a := find[agent.AskOpen](h); len(a) != 1 || a[0].Ask.Input.(map[string]any)["command"] != "echo approved" {
		t.Errorf("asks %+v", a)
	}
	h = replay(t, "approval-deny")
	var states []string
	for _, k := range h.kinds() {
		if s, ok := strings.CutPrefix(k, "StateChange:"); ok {
			states = append(states, s)
		}
	}
	if !reflect.DeepEqual(states, []string{"running", "waiting", "running", "idle"}) {
		t.Errorf("states %v", states)
	}
}

func TestCompact(t *testing.T) {
	h := replay(t, "compact")
	k := slices.DeleteFunc(h.kinds(), func(s string) bool { return s == "Started" || s == "Hello" })
	want := []string{"Taken", "TurnStart", "StateChange:compacting", "Compacted", "StateChange:idle", "TurnEnd"}
	if !reflect.DeepEqual(k, want) {
		t.Fatalf("events\n  %v\nwant\n  %v", k, want)
	}
	if c := find[agent.Compacted](h)[0]; c.Before == 0 || c.Failed {
		t.Errorf("compacted %+v", c)
	}
	if e := find[agent.TurnEnd](h)[0]; !e.CompactOnly {
		t.Errorf("end %+v", e)
	}
}

func TestSessionStats(t *testing.T) {
	h := replay(t, "get-session-stats")
	u := h.result["quota"].(*agent.Usage)
	if u.CostUSD != 0.000492156 || u.Models["deepseek/deepseek-flash"].Tokens != (agent.Tokens{Input: 1361, Output: 59, CacheRead: 2176}) || len(u.Windows) != 0 {
		t.Errorf("usage %+v", u)
	}
}

func TestSetModel(t *testing.T) {
	h := replay(t, "set-model")
	if err := h.result["set_model deepseek/deepseek-v4-pro"]; err != nil {
		t.Errorf("set_model: %v", err)
	}
	// The level set on the process goes back on after the switch, which resets it.
	if h.result["level"] != "high" {
		t.Errorf("level set again: %v", h.result["level"])
	}
	if err, _ := h.result["set_model deepseek/no-such-model"].(error); err == nil || !strings.Contains(err.Error(), "Model not found") {
		t.Errorf("set_model of a model that is not: %v", err)
	}
}

func TestSpawn(t *testing.T) {
	bin := t.TempDir()
	os.WriteFile(filepath.Join(bin, "pi"), []byte("#!/usr/bin/env node\n"), 0o755)
	os.WriteFile(filepath.Join(bin, "node"), []byte("#!/bin/sh\n"), 0o755)
	state := t.TempDir()
	pf := filepath.Join(state, "system-prompt.md")
	env := []string{"PATH=" + bin, "ANTHROPIC_API_KEY=a", "ANTHROPIC_BASE_URL=b", "CLAUDE_CODE_OAUTH_TOKEN=c", "CLAUDE_CONFIG_DIR=/cc", "CLAUDECODE=1", "AI_AGENT=pi", "DEEPSEEK_API_KEY=d", "IRIDIUM_SESSION=parent"}
	d := New(Options{})
	spec, err := d.Spawn(agent.SpawnOptions{Session: "s1", Cwd: "/w", Env: env, Program: "pi", SystemPromptFile: pf, Model: "opencode-go/deepseek-v4.1-flash:low", Mode: "ask"})
	if err != nil {
		t.Fatal(err)
	}
	ext := filepath.Join(state, ExtensionFile)
	want := filepath.Join(bin, "pi") + " --mode rpc --session-id s1 --append-system-prompt " + pf + " --model opencode-go/deepseek-v4.1-flash --thinking low -e " + ext
	if got := strings.Join(spec.Argv, " "); got != want {
		t.Errorf("argv\n  %s\nwant\n  %s", got, want)
	}
	slices.Sort(spec.Env)
	if w := []string{"DEEPSEEK_API_KEY=d", "IRIDIUM_SESSION=s1", "PATH=" + bin}; !reflect.DeepEqual(spec.Env, w) {
		t.Errorf("env %v", spec.Env)
	}
	if b, _ := os.ReadFile(ext); !bytes.Equal(b, approvalSource) || !bytes.Contains(b, []byte(`iridium: "approval"`)) || !bytes.Contains(b, []byte("signal: ctx.signal")) {
		t.Errorf("extension %q", b)
	}
	spec, _ = d.Spawn(agent.SpawnOptions{Session: "s1", Env: env, Program: "pi", SystemPromptFile: pf})
	if slices.Contains(spec.Argv, "-e") {
		t.Errorf("auto loads the extension: %v", spec.Argv)
	}
	os.Remove(filepath.Join(bin, "node"))
	if _, err := d.Spawn(agent.SpawnOptions{Session: "s1", Env: env, Program: "pi"}); err == nil {
		t.Error("spawned without node")
	}
	if err := d.SetMode(context.Background(), "ask"); err != ErrRestart {
		t.Errorf("set mode: %v", err)
	}
}

// At an executor: its pi, the system prompt and the approval extension written there, nothing of
// pi looked up here.
func TestSpawnRemote(t *testing.T) {
	state := t.TempDir()
	pf := filepath.Join(state, "system-prompt.md")
	os.WriteFile(pf, []byte("be brief"), 0o600)
	there := map[string]string{}
	r := &agent.Remote{
		Argv: []string{`C:\node\node.exe`, `C:\pi\cli.js`}, Scratch: `C:\x\scratch`,
		Join:      func(elem ...string) string { return strings.Join(elem, `\`) },
		WriteFile: func(path string, data []byte) error { there[path] = string(data); return nil },
	}
	spec, err := New(Options{}).Spawn(agent.SpawnOptions{Session: "s1", Cwd: `C:\w`, Env: []string{"PATH=/nowhere"}, SystemPromptFile: pf, Mode: "ask", Remote: r})
	if err != nil {
		t.Fatal(err)
	}
	prompt, ext := `C:\x\scratch\s1\system-prompt.md`, `C:\x\scratch\s1\`+ExtensionFile
	want := `C:\node\node.exe C:\pi\cli.js --mode rpc --session-id s1 --append-system-prompt ` + prompt + " -e " + ext
	if got := strings.Join(spec.Argv, " "); got != want {
		t.Errorf("argv\n  %s\nwant\n  %s", got, want)
	}
	if there[prompt] != "be brief" || there[ext] != string(approvalSource) || len(there) != 2 {
		t.Errorf("written there: %v", slices.Collect(maps.Keys(there)))
	}
	if spec.Cwd != `C:\w` {
		t.Errorf("cwd %q", spec.Cwd)
	}
}
