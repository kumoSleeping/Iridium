package agy

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/agent"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/ctx/assemble"
	"github.com/kumoSleeping/iridium/internal/transcript"
)

// The frames below are agy 1.5.9's, as recorded with `agy acp` (2026-11-16), cut down.

const sid = "f6a99e69-97b5-45fb-b14d-fd7b70376d17"

// openedJSON is session/new's answer: the models at each level, the model option, the tier.
const openedJSON = `{"sessionId":"` + sid + `","models":{"availableModels":[{"modelId":"gemini-4-argon-medium","name":"Gemini 4 Argon (Medium)"},{"modelId":"gemini-4-argon-high","name":"Gemini 4 Argon (High)"},{"modelId":"gemini-4-argon-low","name":"Gemini 4 Argon (Low)"},{"modelId":"gemini-3.8-flash-low","name":"Gemini 3.8 Flash (Low)"},{"modelId":"gemini-3.8-flash-high","name":"Gemini 3.8 Flash (High)"},{"modelId":"gemini-pro-orchestrator-g1-pro","name":"Orchestrator (Argon)"},{"modelId":"gpt-oss-120b-medium","name":"GPT-OSS 120B (Medium)"}],"currentModelId":"gemini-4-argon-medium"},"modes":{"currentModeId":"default"},"configOptions":[{"id":"model","currentValue":"gemini-4-argon","options":[{"value":"gemini-4-argon","name":"Gemini 4 Argon"},{"value":"gemini-pro-orchestrator-g1-pro","name":"Orchestrator (Argon)"},{"value":"gemini-3.8-flash","name":"Gemini 3.8 Flash"},{"value":"gpt-oss-120b-medium","name":"GPT-OSS 120B (Medium)"}]},{"id":"thought_level","currentValue":"medium","options":[{"value":"low"},{"value":"medium"},{"value":"high"}]},{"id":"mode","currentValue":"default","options":[{"value":"default"},{"value":"accept-edits"},{"value":"plan"}]}],"_meta":{"antigravity":{"tier":{"name":"Google AI Pro","id":"g1-pro-tier"}}}}`

func config(model, level, mode string) string {
	return `{"configOptions":[{"id":"model","currentValue":"` + model + `"},{"id":"thought_level","currentValue":"` + level + `"},{"id":"mode","currentValue":"` + mode + `"}]}`
}

func upd(u string) string {
	return `{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"` + sid + `","update":` + u + `}}`
}

type rig struct {
	t   *testing.T
	d   *Driver
	out chan map[string]any
	mu  sync.Mutex
	evs []agent.Event
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{t: t, out: make(chan map[string]any, 64)}
	home := t.TempDir()
	login := `{"token":{"access_token":"x"},"id_token_email":"me@example.com"}`
	if err := os.WriteFile(filepath.Join(home, LoginFile), []byte(login), 0o600); err != nil {
		t.Fatal(err)
	}
	r.d = New(Options{Emit: r.emit, IDPrefix: "t-", Logf: t.Logf, Home: home})
	return r
}

func (r *rig) emit(e agent.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.evs = append(r.evs, e)
}

// take is the events so far, and forgets them.
func (r *rig) take() []agent.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	e := r.evs
	r.evs = nil
	return e
}

func (r *rig) write(line []byte) error {
	var m map[string]any
	if err := json.Unmarshal(line, &m); err != nil {
		r.t.Errorf("driver wrote no JSON: %s", line)
	}
	r.out <- m
	return nil
}

// next is the next line the driver writes, which must be method's (an answer: "").
func (r *rig) next(method string) map[string]any {
	r.t.Helper()
	select {
	case m := <-r.out:
		if got, _ := m["method"].(string); got != method {
			r.t.Fatalf("driver wrote %v, want %q", m, method)
		}
		return m
	case <-time.After(3 * time.Second):
		r.t.Fatalf("driver wrote no %s", method)
	}
	return nil
}

func (r *rig) answer(req map[string]any, result string) {
	id, _ := json.Marshal(req["id"])
	r.d.Feed([]byte(`{"jsonrpc":"2.0","id":`+string(id)+`,"result":`+result+`}`), false)
}

func (r *rig) feed(lines ...string) {
	for _, l := range lines {
		r.d.Feed([]byte(l), false)
	}
}

// open spawns and opens a new session in mode, at flash low.
func (r *rig) open(mode string) {
	r.t.Helper()
	prog := filepath.Join(r.t.TempDir(), "agy")
	if err := os.WriteFile(prog, nil, 0o700); err != nil {
		r.t.Fatal(err)
	}
	prompt := filepath.Join(r.t.TempDir(), "prompt.md")
	_ = os.WriteFile(prompt, []byte("Be brief."), 0o600)
	spec, err := r.d.Spawn(agent.SpawnOptions{Session: "ir-1", Cwd: "/w", Program: prog, Model: "gemini-3.8-flash", Effort: "low", Mode: mode, SystemPromptFile: prompt})
	if err != nil {
		r.t.Fatal(err)
	}
	if !slices.Equal(spec.Argv, []string{prog, "acp"}) || !slices.Contains(spec.Env, "AGY_CLI_DISABLE_AUTO_UPDATE=1") || !slices.Contains(spec.Env, "IRIDIUM_SESSION=ir-1") {
		r.t.Fatalf("spec %v %v", spec.Argv, spec.Env)
	}
	r.d.Attach(r.write, 1)
	init := r.next("initialize")
	r.answer(init, `{"protocolVersion":1,"agentInfo":{"name":"Antigravity ACP Server","version":"1.5.9"}}`)
	nw := r.next("session/new")
	p := nw["params"].(map[string]any)
	meta, _ := json.Marshal(p["_meta"])
	if p["cwd"] != "/w" || string(meta) != `{"antigravity":{"systemPrompt":{"append":"Be brief."}}}` {
		r.t.Fatalf("session/new %v", p)
	}
	r.answer(nw, openedJSON)
	for _, want := range [][2]string{{"model", "gemini-3.8-flash"}, {"thought_level", "low"}, {"mode", agyMode(cmpOr(mode, DefaultMode))}} {
		c := r.next("session/set_config_option")
		cp := c["params"].(map[string]any)
		if cp["configId"] != want[0] || cp["value"] != want[1] || cp["sessionId"] != sid {
			r.t.Fatalf("set_config_option %v, want %v", cp, want)
		}
		r.answer(c, config("gemini-3.8-flash", map[bool]string{true: "low", false: "medium"}[want[0] != "model"], "default"))
	}
	if err := r.d.waitReady(context.Background()); err != nil {
		r.t.Fatal(err)
	}
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func find[T agent.Event](evs []agent.Event) []T {
	var out []T
	for _, e := range evs {
		if x, ok := e.(T); ok {
			out = append(out, x)
		}
	}
	return out
}

func records(evs []agent.Event) []transcript.Entry {
	var out []transcript.Entry
	for _, r := range find[agent.Record](evs) {
		out = append(out, r.Entry)
	}
	return out
}

func send(t *testing.T, d *Driver, id, text string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := d.Send(ctx, agent.Message{ID: id, Text: assemble.Input(text)}); err != nil {
		t.Fatal(err)
	}
}

func TestOpen(t *testing.T) {
	r := newRig(t)
	r.open("")
	evs := r.take()
	st := find[agent.Started](evs)
	if len(st) == 0 || st[len(st)-1] != (agent.Started{Session: sid, Model: "gemini-3.8-flash", Effort: "low"}) {
		t.Fatalf("Started %v", st)
	}
	h := find[agent.Hello](evs)
	if len(h) != 1 || h[0].Version != "1.5.9" || h[0].Mode != "auto" || h[0].Plan != "Google AI Pro" || h[0].Account != "me@example.com" {
		t.Fatalf("Hello %+v", h)
	}
	byValue := map[string]agent.Model{}
	for _, m := range h[0].Models {
		byValue[m.Value] = m
	}
	if m := byValue["gemini-4-argon"]; !slices.Equal(m.Efforts, []string{"low", "medium", "high"}) || !m.Default {
		t.Errorf("argon %+v", m)
	}
	if m := byValue["gemini-3.8-flash"]; !slices.Equal(m.Efforts, []string{"low", "high"}) || m.Default {
		t.Errorf("flash %+v", m)
	}
	if m := byValue["gpt-oss-120b-medium"]; !m.NoEffort || len(m.Efforts) != 0 {
		t.Errorf("gpt-oss %+v", m)
	}
}

// A turn: the message back, a thought, a command allowed by the driver itself in auto, its
// output past agy's header, the reply, usage.
func TestTurnInAuto(t *testing.T) {
	r := newRig(t)
	r.open("auto")
	r.take()
	send(t, r.d, "in-1", "run echo hi")
	p := r.next("session/prompt")
	if p["id"] != "in-1" {
		t.Fatalf("prompt id %v", p["id"])
	}
	r.feed(
		upd(`{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"run echo hi"}}`),
		upd(`{"sessionUpdate":"session_info_update","title":"Echo Hi"}`),
		upd(`{"sessionUpdate":"agent_thought_chunk","content":{"type":"text","text":"I will "}}`),
		upd(`{"sessionUpdate":"agent_thought_chunk","content":{"type":"text","text":"run it."}}`),
		upd(`{"sessionUpdate":"tool_call","toolCallId":"c1","name":"run_command","args":{"CommandLine":"echo hi"},"status":"in_progress","kind":"execute","title":"echo hi","rawInput":{"CommandLine":"echo hi","WaitMsBeforeAsync":5000,"toolSummary":"Say hi"}}`),
		`{"jsonrpc":"2.0","method":"session/request_permission","params":{"sessionId":"`+sid+`","toolCall":{"sessionUpdate":"tool_call","toolCallId":"c1","name":"run_command","status":"pending","kind":"execute","title":"echo hi","rawInput":{"CommandLine":"echo hi"}},"options":[{"optionId":"allow","name":"Allow","kind":"allow_once"},{"optionId":"deny","name":"Deny","kind":"reject_once"}]},"id":"1"}`,
	)
	a := r.next("")
	if a["id"] != "1" || !strings.Contains(mustJSON(a["result"]), `"optionId":"allow"`) {
		t.Fatalf("auto answer %v", a)
	}
	r.feed(
		upd(`{"sessionUpdate":"tool_call_update","toolCallId":"c1","name":"run_command","status":"completed","rawOutput":{"output":"x","exit_code":0},"content":[{"type":"content","content":{"type":"text","text":"Created At: 2026-11-16T09:42:08+01:00\nCompleted At: 2026-11-16T09:42:08+01:00\nCommand exited with code 0.\n\nCommand output:\nhi\r\n"}}]}`),
		upd(`{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"It said "}}`),
		upd(`{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"hi."}}`),
		upd(`{"sessionUpdate":"usage_update","used":8874,"size":1048576}`),
		`{"jsonrpc":"2.0","result":{"stopReason":"end_turn","usage":{"inputTokens":17550,"outputTokens":85,"cachedReadTokens":100,"cachedWriteTokens":0,"thoughtTokens":28,"totalTokens":17735}},"id":"in-1"}`,
	)
	evs := r.take()
	if tk := find[agent.Taken](evs); len(tk) != 1 || tk[0].InputID != "in-1" {
		t.Errorf("Taken %v", tk)
	}
	if ts := find[agent.TurnStart](evs); len(ts) != 1 || ts[0].Input != "in-1" {
		t.Errorf("TurnStart %v", ts)
	}
	if ts := find[agent.ToolStart](evs); len(ts) != 1 || ts[0].Name != "bash" || mustJSON(ts[0].Input) != `{"command":"echo hi","description":"Say hi"}` {
		t.Errorf("ToolStart %+v", ts)
	}
	if len(find[agent.AskOpen](evs)) != 0 {
		t.Error("auto asked")
	}
	if md := find[agent.MessageDone](evs); len(md) != 1 || md[0].Text != "It said hi." {
		t.Errorf("MessageDone %v", md)
	}
	if u := find[agent.TurnUsage](evs); len(u) != 1 || u[0].Tokens != (agent.Tokens{Input: 17550, Output: 85, CacheRead: 100}) || u[0].Context != 8874 {
		t.Errorf("TurnUsage %+v", u)
	}
	if te := find[agent.TurnEnd](evs); len(te) != 1 || te[0].Reply != "It said hi." {
		t.Errorf("TurnEnd %v", te)
	}
	got := records(evs)
	want := []transcript.Entry{
		{Kind: "user", Text: "run echo hi", ID: "in-1"},
		{Kind: "title", Text: "Echo Hi"},
		{Kind: "thinking", Text: "I will run it."},
		{Kind: "tool", ID: "c1", Name: "bash", Input: json.RawMessage(`{"command":"echo hi","description":"Say hi"}`)},
		{Kind: "result", ID: "c1", Text: "hi\n"},
		{Kind: "assistant", Text: "It said hi."},
	}
	if len(got) != len(want) {
		t.Fatalf("records %+v", got)
	}
	for i := range want {
		if g, w := mustJSON(got[i]), mustJSON(want[i]); g != w {
			t.Errorf("record %d = %s, want %s", i, g, w)
		}
	}
	// The record before the message is told whole: the page finds it there.
	ri := slices.IndexFunc(evs, func(e agent.Event) bool { r, ok := e.(agent.Record); return ok && r.Entry.Kind == "assistant" })
	mi := slices.IndexFunc(evs, func(e agent.Event) bool { _, ok := e.(agent.MessageDone); return ok })
	if ri > mi {
		t.Error("MessageDone before its record")
	}
	if r.d.State() != agent.Idle {
		t.Error("not idle")
	}
	if c, err := r.d.Context(context.Background()); err != nil || c.Used != 8874 || c.Max != 1048576 {
		t.Errorf("Context %+v %v", c, err)
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// A message sent during a turn joins it: agy answers the running prompt at once without usage,
// and the turn ends with the last one's answer.
func TestJoinsTheTurn(t *testing.T) {
	r := newRig(t)
	r.open("")
	send(t, r.d, "in-1", "count")
	r.next("session/prompt")
	r.feed(upd(`{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"count"}}`))
	r.take()
	send(t, r.d, "in-2", "then say second")
	r.next("session/prompt")
	if tk := find[agent.Taken](r.take()); len(tk) != 1 || tk[0].InputID != "in-2" {
		t.Errorf("Taken as sent %v", tk)
	}
	r.feed(
		`{"jsonrpc":"2.0","result":{"stopReason":"end_turn"},"id":"in-1"}`,
		upd(`{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"1 2 3"}}`),
	)
	if r.d.State() == agent.Idle || len(find[agent.TurnEnd](r.take())) != 0 {
		t.Fatal("the first prompt's answer ended the turn")
	}
	r.feed(
		upd(`{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"then say second"}}`),
		upd(`{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"second"}}`),
		`{"jsonrpc":"2.0","result":{"stopReason":"end_turn","usage":{"inputTokens":5,"outputTokens":2}},"id":"in-2"}`,
	)
	evs := r.take()
	if tk := find[agent.Taken](evs); len(tk) != 0 {
		t.Errorf("Taken again at the echo %v", tk)
	}
	if rec := records(evs); len(rec) != 2 || rec[0].Kind != "assistant" || rec[1].Kind != "assistant" {
		t.Errorf("records %+v", rec) // the echo is not recorded again
	}
	if te := find[agent.TurnEnd](evs); len(te) != 1 || te[0].Reply != "second" {
		t.Errorf("TurnEnd %v", te)
	}
	if md := find[agent.MessageDone](evs); len(md) != 2 || md[0].Text != "1 2 3" {
		t.Errorf("MessageDone %v", md)
	}
}

// agy 1.5.10 says no prompt back: each is recorded and taken as it goes out, joined ones too.
func TestNoEcho(t *testing.T) {
	r := newRig(t)
	r.open("")
	r.take()
	send(t, r.d, "in-1", "count")
	r.next("session/prompt")
	evs := r.take()
	if tk := find[agent.Taken](evs); len(tk) != 1 || tk[0].InputID != "in-1" {
		t.Errorf("Taken %v", tk)
	}
	if ts := find[agent.TurnStart](evs); len(ts) != 1 || ts[0].Input != "in-1" {
		t.Errorf("TurnStart %v", ts)
	}
	rs := find[agent.Record](evs)
	if len(rs) != 1 || !rs[0].Sent || mustJSON(rs[0].Entry) != mustJSON(transcript.Entry{Kind: "user", Text: "count", ID: "in-1"}) {
		t.Fatalf("records %+v", rs)
	}
	r.feed(upd(`{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"1 2 3"}}`))
	send(t, r.d, "in-2", "then say second")
	r.next("session/prompt")
	r.feed(
		`{"jsonrpc":"2.0","result":{"stopReason":"end_turn"},"id":"in-1"}`,
		upd(`{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"second"}}`),
		`{"jsonrpc":"2.0","result":{"stopReason":"end_turn","usage":{"inputTokens":5,"outputTokens":2}},"id":"in-2"}`,
	)
	evs = r.take()
	var kinds []string
	for _, e := range records(evs) {
		kinds = append(kinds, e.Kind+":"+e.Text)
	}
	if want := []string{"assistant:1 2 3", "user:then say second", "assistant:second"}; !slices.Equal(kinds, want) {
		t.Errorf("records %v, want %v", kinds, want)
	}
	if tk := find[agent.Taken](evs); len(tk) != 1 || tk[0].InputID != "in-2" {
		t.Errorf("Taken %v", tk)
	}
	if te := find[agent.TurnEnd](evs); len(te) != 1 || te[0].Reply != "second" {
		t.Errorf("TurnEnd %v", te)
	}
	if r.d.State() != agent.Idle {
		t.Error("not idle")
	}
}

// In default mode a permission is put to you; allowed, agy is told so.
func TestAskAndAnswer(t *testing.T) {
	r := newRig(t)
	r.open("default")
	send(t, r.d, "in-1", "edit")
	r.next("session/prompt")
	r.feed(
		upd(`{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"edit"}}`),
		upd(`{"sessionUpdate":"tool_call","toolCallId":"c2","name":"edit_file","status":"in_progress","kind":"edit","title":"File a.txt","rawInput":{"Path":"/w/a.txt","Replacement":"there","Target":"world"}}`),
		`{"jsonrpc":"2.0","method":"session/request_permission","params":{"sessionId":"`+sid+`","toolCall":{"toolCallId":"c2","name":"edit_file","status":"pending","kind":"edit","title":"File a.txt","rawInput":{"Path":"/w/a.txt","Replacement":"there","Target":"world"}},"options":[{"optionId":"allow","kind":"allow_once"},{"optionId":"deny","kind":"reject_once"}]},"id":"7"}`,
	)
	evs := r.take()
	asks := find[agent.AskOpen](evs)
	if len(asks) != 1 || asks[0].Ask.RequestID != "7" || asks[0].Ask.Tool != "edit" || asks[0].Ask.ToolUseID != "c2" || mustJSON(asks[0].Ask.Input) != `{"file_path":"/w/a.txt","new_string":"there","old_string":"world"}` {
		t.Fatalf("AskOpen %+v", asks)
	}
	if r.d.State() != agent.Waiting {
		t.Errorf("state %s", r.d.State())
	}
	if err := r.d.Answer(context.Background(), api.Answer{RequestID: "7", Kind: "permission", Allow: true}); err != nil {
		t.Fatal(err)
	}
	a := r.next("")
	if a["id"] != "7" || mustJSON(a["result"]) != `{"outcome":{"optionId":"allow","outcome":"selected"}}` {
		t.Fatalf("answer %v", a)
	}
	if r.d.State() != agent.Running {
		t.Errorf("state %s", r.d.State())
	}
	// Interrupted: agy is told, the turn ends cancelled.
	if err := r.d.Interrupt(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c := r.next("session/cancel"); c["params"].(map[string]any)["sessionId"] != sid {
		t.Fatalf("cancel %v", c)
	}
	r.feed(`{"jsonrpc":"2.0","result":{"stopReason":"cancelled"},"id":"in-1"}`)
	evs = r.take()
	if len(find[agent.TurnEnd](evs)) != 1 {
		t.Error("no TurnEnd")
	}
	if rec := records(evs); len(rec) == 0 || rec[len(rec)-1].Event != string(api.SystemEventInterrupted) {
		t.Errorf("records %+v", rec)
	}
}

// A session taken up from agy's store: session/load, its history replayed recorded, nothing else.
func TestLoadRecordsHistory(t *testing.T) {
	r := newRig(t)
	prog := filepath.Join(t.TempDir(), "agy")
	_ = os.WriteFile(prog, nil, 0o700)
	if _, err := r.d.Spawn(agent.SpawnOptions{Session: "ir-2", Cwd: "/w", Program: prog, Resume: true, AgentSession: sid, History: true}); err != nil {
		t.Fatal(err)
	}
	r.d.Attach(r.write, 1)
	r.answer(r.next("initialize"), `{"protocolVersion":1,"agentInfo":{"version":"1.5.9"}}`)
	ld := r.next("session/load")
	if ld["params"].(map[string]any)["sessionId"] != sid {
		t.Fatalf("load %v", ld)
	}
	r.feed(
		upd(`{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"hello"}}`),
		upd(`{"sessionUpdate":"tool_call","toolCallId":"c1","name":"read_file","status":"in_progress","kind":"read","title":"File a.txt","rawInput":{"Path":"/w/a.txt"}}`),
		upd(`{"sessionUpdate":"tool_call_update","toolCallId":"c1","name":"read_file","status":"completed"}`),
		upd(`{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"hi"}}`),
	)
	r.answer(ld, openedJSON) // no Hydrogen in its catalog: agy's own default stays
	r.answer(r.next("session/set_config_option"), config("gemini-4-argon", "medium", "default"))
	if err := r.d.waitReady(context.Background()); err != nil {
		t.Fatal(err)
	}
	evs := r.take()
	for _, e := range evs {
		switch e.(type) {
		case agent.TurnStart, agent.TextDelta, agent.ToolStart, agent.Taken, agent.MessageDone:
			t.Errorf("a history replayed told %T", e)
		}
	}
	var kinds []string
	for _, e := range records(evs) {
		kinds = append(kinds, e.Kind+":"+e.Name+e.Text)
	}
	if want := []string{"user:hello", "tool:read", "result:", "assistant:hi"}; !slices.Equal(kinds, want) {
		t.Errorf("records %v, want %v", kinds, want)
	}
}

// A process an earlier daemon started: its output fed again brings the session back; the turn it
// was in ends with an answer that carries usage.
func TestTakenUp(t *testing.T) {
	r := newRig(t)
	r.d.Attach(r.write, 3)
	// The quota asked before the output fed again names the session waits for it.
	quota := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, err := r.d.Quota(ctx)
		quota <- err
	}()
	time.Sleep(200 * time.Millisecond)
	for _, l := range []string{
		`{"jsonrpc":"2.0","result":` + openedJSON + `,"id":"ir-aa-2"}`,
		upd(`{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"go"}}`),
		upd(`{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"work"}}`),
	} {
		r.d.Feed([]byte(l), true)
	}
	evs := r.take()
	if err := <-quota; err != nil {
		t.Errorf("Quota of a process taken up: %v", err)
	}
	if h := find[agent.Hello](evs); len(h) != 1 || h[0].Plan != "Google AI Pro" {
		t.Errorf("Hello %+v", h)
	}
	if st := find[agent.Started](evs); len(st) == 0 || st[0].Session != sid {
		t.Errorf("Started %v", st)
	}
	if r.d.State() != agent.Running {
		t.Fatalf("state %s", r.d.State())
	}
	r.feed(`{"jsonrpc":"2.0","result":{"stopReason":"end_turn"},"id":"x"}`) // one another joined
	if r.d.State() != agent.Running {
		t.Fatal("an answer without usage ended the turn")
	}
	r.feed(`{"jsonrpc":"2.0","result":{"stopReason":"end_turn","usage":{"inputTokens":1}},"id":"y"}`)
	if r.d.State() != agent.Idle {
		t.Fatal("the turn did not end")
	}
}

func TestSignedOut(t *testing.T) {
	r := newRig(t)
	prog := filepath.Join(t.TempDir(), "agy")
	_ = os.WriteFile(prog, nil, 0o700)
	if _, err := r.d.Spawn(agent.SpawnOptions{Session: "ir-3", Cwd: "/w", Program: prog}); err != nil {
		t.Fatal(err)
	}
	r.d.Attach(r.write, 1)
	r.answer(r.next("initialize"), `{"protocolVersion":1}`)
	nw := r.next("session/new")
	id, _ := json.Marshal(nw["id"])
	r.feed(`{"jsonrpc":"2.0","id":` + string(id) + `,"error":{"code":-32000,"message":"Authentication required"}}`)
	if err := r.d.waitReady(context.Background()); err == nil {
		t.Fatal("open went well")
	}
	if g := find[agent.GiveUp](r.take()); len(g) != 1 || g[0].Why != ErrSignedOut.Error() {
		t.Errorf("GiveUp %v", g)
	}
}

func TestToolsAndResults(t *testing.T) {
	for _, c := range []struct{ name, in, wantName, wantIn string }{
		{"run_command", `{"CommandLine":"ls -la","Cwd":"/w","WaitMsBeforeAsync":5000,"toolSummary":"List"}`, "bash", `{"command":"ls -la","cwd":"/w","description":"List"}`},
		{"write_file", `{"Content":"hello\n","Path":"/w/a.txt"}`, "write", `{"content":"hello\n","file_path":"/w/a.txt"}`},
		{"read_file", `{"Path":"/w/a.txt"}`, "read", `{"file_path":"/w/a.txt"}`},
		{"edit_file", `{"Path":"/w/a.txt","Replacement":"there","Target":"world"}`, "edit", `{"file_path":"/w/a.txt","new_string":"there","old_string":"world"}`},
		{"swarm", `{"action":"models"}`, "swarm", `{"action":"models"}`},
	} {
		name, in := toolOf(&update{Name: c.name, RawInput: json.RawMessage(c.in)})
		if name != c.wantName || mustJSON(in) != c.wantIn {
			t.Errorf("%s: %s %s, want %s %s", c.name, name, mustJSON(in), c.wantName, c.wantIn)
		}
	}
	failed := resultOf(&update{Content: json.RawMessage(`[{"type":"content","content":{"type":"text","text":"Created At: x\nCommand exited with code 2.\n\nCommand output:\nno\r\n"}}]`)})
	if !strings.HasPrefix(failed, "Created At: x\nCommand exited with code 2.") {
		t.Errorf("a failed command's header went: %q", failed)
	}
	if got := resultOf(&update{RawOutput: json.RawMessage(`{"output":"out"}`)}); got != "out" {
		t.Errorf("raw output %q", got)
	}
}

func init() { Endpoint = "" } // no test asks Google

// retrieveUserQuotaSummary as Antigravity's account gave it (2026-11-16), cut down: two groups, a
// week's and five hours' bucket each.
func TestWindows(t *testing.T) {
	var sum quotaSummary
	raw := `{"groups":[
		{"displayName":"Gemini Models","buckets":[
			{"bucketId":"gemini-weekly","window":"weekly","resetTime":"2026-11-23T06:54:28Z","remainingFraction":0.94489574},
			{"bucketId":"gemini-5h","window":"5h","resetTime":"2026-11-16T11:54:28Z","remainingFraction":0.8071351}]},
		{"displayName":"Claude and GPT models","buckets":[
			{"bucketId":"3p-weekly","window":"weekly","resetTime":"2026-11-20T01:05:02Z","remainingFraction":0.4992128},
			{"bucketId":"3p-5h","window":"5h","resetTime":"2026-11-16T16:43:06Z","remainingFraction":1},
			{"bucketId":"odd","window":"daily","remainingFraction":0.5}]}]}`
	if err := json.Unmarshal([]byte(raw), &sum); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, w := range windowsOf(sum) {
		got = append(got, fmt.Sprintf("%s %s %.0f %d", w.Model, w.Group, w.Percent, w.ResetsAt))
	}
	want := []string{
		fmt.Sprintf("Claude · GPT session 0 %d", time.Date(2026, 11, 16, 16, 43, 6, 0, time.UTC).UnixMilli()),
		fmt.Sprintf("Claude · GPT weekly 50 %d", time.Date(2026, 11, 20, 1, 5, 2, 0, time.UTC).UnixMilli()),
		fmt.Sprintf("Gemini session 19 %d", time.Date(2026, 11, 16, 11, 54, 28, 0, time.UTC).UnixMilli()),
		fmt.Sprintf("Gemini weekly 6 %d", time.Date(2026, 11, 23, 6, 54, 28, 0, time.UTC).UnixMilli()),
	}
	if !slices.Equal(got, want) {
		t.Errorf("windows\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	// No login whose token has not run out: no quota, and no error to the session.
	Endpoint = "http://127.0.0.1:1"
	defer func() { Endpoint = "" }()
	if _, err := Windows(context.Background(), t.TempDir(), "1.5.9"); err != ErrNoLogin {
		t.Errorf("Windows without a login: %v", err)
	}
}

// A session that names no model runs Iridium's default, Gemini 4 Hydrogen, and the catalog says
// so where agy has it; agy's own default otherwise.
func TestDefaultModel(t *testing.T) {
	withH := strings.Replace(openedJSON, `{"value":"gemini-4-argon","name":"Gemini 4 Argon"},`, `{"value":"gemini-4-argon","name":"Gemini 4 Argon"},{"value":"gemini-4-hydrogen","name":"Gemini 4 Hydrogen"},`, 1)
	withH = strings.Replace(withH, `{"modelId":"gemini-4-argon-low","name":"Gemini 4 Argon (Low)"},`, `{"modelId":"gemini-4-argon-low","name":"Gemini 4 Argon (Low)"},{"modelId":"gemini-4-hydrogen-xhigh","name":"Gemini 4 Hydrogen (XHigh)"},`, 1)
	var o opened
	if err := json.Unmarshal([]byte(withH), &o); err != nil {
		t.Fatal(err)
	}
	for _, m := range o.catalog() {
		if m.Default != (m.Value == DefaultModel) {
			t.Errorf("%s default %v", m.Value, m.Default)
		}
	}
	r := newRig(t)
	prog := filepath.Join(t.TempDir(), "agy")
	_ = os.WriteFile(prog, nil, 0o700)
	if _, err := r.d.Spawn(agent.SpawnOptions{Session: "ir-4", Cwd: "/w", Program: prog}); err != nil {
		t.Fatal(err)
	}
	r.d.Attach(r.write, 1)
	r.answer(r.next("initialize"), `{"protocolVersion":1}`)
	r.answer(r.next("session/new"), withH)
	c := r.next("session/set_config_option")
	if p := c["params"].(map[string]any); p["configId"] != "model" || p["value"] != DefaultModel {
		t.Fatalf("set_config_option %v", p)
	}
}

// A message sent while a process taken up is still being fed again: the end of a turn fed again
// does not forget its echo (agy before 1.5.10), so the prompt is recorded once.
func TestEchoAfterReplay(t *testing.T) {
	r := newRig(t)
	r.d.Attach(r.write, 3)
	for _, l := range []string{
		`{"jsonrpc":"2.0","result":` + openedJSON + `,"id":"ir-aa-2"}`,
		upd(`{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"go"}}`),
	} {
		r.d.Feed([]byte(l), true)
	}
	r.take()
	send(t, r.d, "in-9", "next")
	r.next("session/prompt")
	r.d.Feed([]byte(`{"jsonrpc":"2.0","result":{"stopReason":"end_turn","usage":{"inputTokens":1,"outputTokens":1}},"id":"x"}`), true)
	r.feed(
		upd(`{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"next"}}`),
		upd(`{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"ok"}}`),
	)
	var users []string
	for _, e := range records(r.take()) {
		if e.Kind == "user" {
			users = append(users, e.ID+":"+e.Text)
		}
	}
	if !slices.Equal(users, []string{"in-9:next"}) {
		t.Errorf("user records %v", users)
	}
}

// The model the list marks default picked (the session layer sends "default"): agy is set to that
// model by its id, not "default", which agy 1.5.10 refuses ("Unknown model ID: default").
func TestSetModelDefault(t *testing.T) {
	r := newRig(t)
	r.open("")
	for _, picked := range []string{"default", ""} {
		done := make(chan error, 1)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			done <- r.d.SetModel(ctx, picked)
		}()
		c := r.next("session/set_config_option")
		cp := c["params"].(map[string]any)
		if cp["configId"] != "model" || cp["value"] != "gemini-4-argon" {
			t.Fatalf("SetModel(%q) sent %v, want the default model gemini-4-argon", picked, cp)
		}
		r.answer(c, config("gemini-4-argon", "medium", "default"))
		if lv := r.next("session/set_config_option"); lv != nil {
			r.answer(lv, config("gemini-4-argon", "low", "default"))
		}
		if err := <-done; err != nil {
			t.Fatalf("SetModel(%q): %v", picked, err)
		}
	}
}
