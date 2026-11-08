// Package claude is the Claude driver: it speaks `claude -p` stream-json itself (Rhodium §6.2,
// ADR-0004) and turns it into the agent package's operations and events.
//
// The control-request handling follows Rhodium's port of the Claude Agent SDK for Python
// (https://github.com/anthropics/claude-agent-sdk-python, MIT License, Copyright (c) 2025
// Anthropic, PBC). Field names follow the official TS SDK's sdk.d.ts, read only.
package claude

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/kumoSleeping/iridium/internal/agent"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/hold"
)

// MaxLine: longer output lines are skipped with a protocol error; out.log still has them whole.
const MaxLine = 64 << 20

// ErrNotAttached: no agent process is attached.
var ErrNotAttached = errors.New("claude: no agent process attached")

// errReplaced fails a request whose process was replaced or ended before it answered.
var errReplaced = errors.New("claude: the agent process ended")

// Options configure a Driver.
type Options struct {
	Emit func(agent.Event)
	// IDPrefix starts every control request's id. Default: "ir-" and a random part, so an answer
	// meant for an earlier daemon matches nothing.
	IDPrefix string
	Logf     func(format string, a ...any)
}

// Driver is one session's Claude driver. See agent.Driver.
type Driver struct {
	emitFn func(agent.Event)
	prefix string
	logf   func(string, ...any)

	mu      sync.Mutex
	w       agent.Writer
	proc    int
	seq     int
	pending map[string]chan ctlResponse
	helloID string
	state   agent.State
	asks    []api.Ask
	tasks   []api.Task
	agents  bool   // a background task other than a shell runs: the CLI holds back idle (#226)
	reply   string // the turn's last assistant message so far
	sent    string // since the last turn ended: "", "compact" (only /compact), "other"
	waiting []string
	model   string
	session string
	caps    map[string]bool
	context int
	queue   []agent.Event
	unknown map[string]int
}

func New(o Options) *Driver {
	if o.IDPrefix == "" {
		b := make([]byte, 3)
		_, _ = rand.Read(b)
		o.IDPrefix = "ir-" + hex.EncodeToString(b) + "-"
	}
	if o.Logf == nil {
		o.Logf = log.Printf
	}
	if o.Emit == nil {
		o.Emit = func(agent.Event) {}
	}
	return &Driver{emitFn: o.Emit, prefix: o.IDPrefix, logf: o.Logf, pending: map[string]chan ctlResponse{}, state: agent.Idle, caps: map[string]bool{}, unknown: map[string]int{}}
}

// Spawn gives the holder's spec. Never --bare: it skips the subscription login. No
// --setting-sources: the user's settings, plugins, skills, MCP servers and hooks load as in a
// terminal. --allow-dangerously-skip-permissions does not change the starting mode; it only lets
// SetMode switch to bypassPermissions later, which the CLI refuses a process launched without it.
func (d *Driver) Spawn(o agent.SpawnOptions) (hold.Spec, error) {
	// CleanEnv drops every CLAUDE_CODE_ variable, CLAUDE_CODE_SIMPLE (what --bare sets) too.
	env := agent.CleanEnv(o.Env)
	var argv []string
	prompt := o.SystemPromptFile
	if r := o.Remote; r != nil {
		// At an executor (docs/exec.md): its claude, the system prompt copied there.
		argv = slices.Clone(r.Argv)
		data, err := os.ReadFile(o.SystemPromptFile)
		if err != nil {
			return hold.Spec{}, err
		}
		prompt = r.Join(r.Scratch, o.Session, filepath.Base(o.SystemPromptFile))
		if err := r.WriteFile(prompt, data); err != nil {
			return hold.Spec{}, fmt.Errorf("claude: the system prompt there: %w", err)
		}
	} else {
		program, err := agent.LookPath(o.Program, env)
		if err != nil {
			return hold.Spec{}, err
		}
		argv = []string{program}
	}
	argv = append(argv,
		"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose",
		"--permission-prompt-tool", "stdio", "--include-partial-messages",
		"--append-system-prompt-file", prompt, "--system-prompt-snapshot", "off",
	)
	if o.Model != "" {
		argv = append(argv, "--model", o.Model)
	}
	if o.Mode != "" {
		argv = append(argv, "--permission-mode", o.Mode)
	}
	if o.Effort != "" {
		// Not --settings effortLevel, which drops max (Rhodium §6.2, 2.1.309).
		argv = append(argv, "--effort", o.Effort)
	}
	if o.Ultracode != nil {
		// Not --effort ultracode, which pins xhigh; false too, over the user's own settings.
		argv = append(argv, "--settings", fmt.Sprintf(`{"ultracode":%t}`, *o.Ultracode))
	}
	argv = append(argv, "--allow-dangerously-skip-permissions")
	if o.Resume {
		argv = append(argv, "--resume="+o.Session)
	} else {
		argv = append(argv, "--session-id="+o.Session)
	}
	env = agent.SetEnv(env, "CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS", "1")
	env = agent.SetEnv(env, "IRIDIUM_SESSION", o.Session)
	if o.CrashResume {
		// Claude runs the interrupted turn again by itself (Rhodium §4.8).
		env = agent.SetEnv(env, "CLAUDE_CODE_RESUME_INTERRUPTED_TURN", "1")
	}
	return hold.Spec{Argv: argv, Env: env, Cwd: o.Cwd}, nil
}

// Attach connects agent process proc and sends initialize, as on every connect: its answer names
// the commands, the models and the asks still open.
func (d *Driver) Attach(w agent.Writer, proc int) {
	d.mu.Lock()
	if proc != d.proc {
		d.reset()
	}
	d.w, d.proc = w, proc
	d.helloID = d.nextID()
	id := d.helloID
	d.mu.Unlock()
	line, _ := json.Marshal(controlRequest{Type: "control_request", RequestID: id, Request: map[string]any{"subtype": "initialize", "perTaskStopAffordance": true}})
	if err := w(line); err != nil {
		d.logf("claude: writing initialize: %v", err)
	}
}

// Detach: the process ended. What it waited on is void.
func (d *Driver) Detach() {
	d.mu.Lock()
	d.reset()
	d.w = nil
	evs := d.take()
	d.mu.Unlock()
	d.flush(evs)
}

// reset forgets the process: its requests fail, its asks and tasks are gone, the turn is over.
func (d *Driver) reset() {
	for id, ch := range d.pending {
		ch <- ctlResponse{Subtype: "error", Error: errReplaced.Error()}
		delete(d.pending, id)
	}
	d.helloID = ""
	d.state = agent.Idle
	d.asks, d.tasks, d.agents = nil, nil, false
	d.reply, d.sent, d.waiting = "", "", nil
	d.model, d.session, d.context = "", "", 0
	d.caps = map[string]bool{}
}

func (d *Driver) State() agent.State {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.state
}

// Send writes a user frame. The CLI queues a message that comes mid-turn and runs it before it
// goes idle, or takes it in between two tool calls, so a new turn and a steer are the same frame.
// origin human: without it the CLI treats the message as of unknown origin.
func (d *Driver) Send(ctx context.Context, m agent.Message) error {
	if m.ID == "" || m.Text == "" {
		return errors.New("claude: a message needs an id and text") // the CLI exits on a user frame without content
	}
	d.mu.Lock()
	w := d.w
	if w == nil {
		d.mu.Unlock()
		return ErrNotAttached
	}
	// Counted before it is written: the turn it starts may show before the write returns.
	d.waiting = append(d.waiting, m.ID)
	if t := strings.TrimSpace(m.Text); (t == "/compact" || strings.HasPrefix(t, "/compact ")) && d.sent != "other" {
		d.sent = "compact"
	} else {
		d.sent = "other"
	}
	d.mu.Unlock()
	content := []textBlock{{Type: "text", Text: m.Text}}
	for _, img := range m.Images {
		content = append(content, textBlock{Type: "image", Source: &imageSource{Type: "base64", MediaType: img.MediaType, Data: img.Data}})
	}
	f := userFrame{Type: "user", UUID: m.ID, Message: userMessage{Role: "user", Content: content}}
	if m.Human {
		f.Origin = &origin{Kind: "human"}
	}
	line, _ := json.Marshal(f)
	if err := w(line); err != nil {
		d.mu.Lock()
		d.waiting = slices.DeleteFunc(d.waiting, func(id string) bool { return id == m.ID })
		d.mu.Unlock()
		return err
	}
	return nil
}

// Interrupt stops the running turn; background tasks go on (perTaskStopAffordance). With an ask
// open, the CLI withdraws it first.
func (d *Driver) Interrupt(ctx context.Context) error {
	d.mu.Lock()
	receipt := d.caps["interrupt_receipt_v1"]
	d.mu.Unlock()
	// Without the receipt the CLI may never answer: then the write is all there is to wait for.
	_, err := d.request(ctx, "interrupt", nil, receipt)
	return err
}

// Answer answers an open ask. Allowing passes the input back as updatedInput; a question's answers
// go in it by question text; denying gives the model a message.
func (d *Driver) Answer(ctx context.Context, a api.Answer) error {
	d.mu.Lock()
	i := slices.IndexFunc(d.asks, func(x api.Ask) bool { return x.RequestID == a.RequestID })
	if i < 0 {
		d.mu.Unlock()
		return fmt.Errorf("claude: no open ask %s", a.RequestID)
	}
	ask := d.asks[i]
	if a.Kind != "" && a.Kind != ask.Kind {
		d.mu.Unlock()
		return fmt.Errorf("claude: ask %s is a %s, not a %s", a.RequestID, ask.Kind, a.Kind)
	}
	w := d.w
	if w == nil {
		d.mu.Unlock()
		return ErrNotAttached
	}
	// Taken off before the write, so a second answer meanwhile finds nothing.
	d.asks = slices.Delete(d.asks, i, i+1)
	proc := d.proc
	d.mu.Unlock()

	var resp map[string]any
	switch {
	case ask.Kind == "question":
		input, _ := ask.Input.(map[string]any)
		updated := map[string]any{}
		for k, v := range input {
			updated[k] = v
		}
		updated["answers"] = a.Answers
		resp = map[string]any{"behavior": "allow", "updatedInput": updated, "decisionClassification": "user_temporary"}
	case a.Allow:
		resp = map[string]any{"behavior": "allow", "updatedInput": ask.Input, "decisionClassification": "user_temporary"}
	default:
		msg := a.Message
		if msg == "" {
			msg = "The user said no."
		}
		resp = map[string]any{"behavior": "deny", "message": msg, "decisionClassification": "user_reject"}
	}
	line, _ := json.Marshal(controlResponse{Type: "control_response", Response: ctlResponse{Subtype: "success", RequestID: a.RequestID, Body: resp}})
	if err := w(line); err != nil {
		d.mu.Lock()
		if d.proc == proc {
			d.asks = append(d.asks, ask)
		}
		d.mu.Unlock()
		return err
	}
	d.emit(agent.AskCancel{RequestID: a.RequestID})
	return nil
}

func (d *Driver) SetModel(ctx context.Context, model string) error {
	_, err := d.request(ctx, "set_model", map[string]any{"model": model}, true)
	return err
}

// SetMode switches the approval mode: default, acceptEdits, plan, bypassPermissions...
func (d *Driver) SetMode(ctx context.Context, mode string) error {
	_, err := d.request(ctx, "set_permission_mode", map[string]any{"mode": mode}, true)
	return err
}

// SetEffort sets effortLevel with apply_flag_settings. Setting it alone turns ultracode off, so
// get_settings first says whether it was asked for, and it goes along as it was (Rhodium §6.2).
// A CLI without get_settings reads as ultracode off.
func (d *Driver) SetEffort(ctx context.Context, level string) error {
	a, err := d.Applied(ctx)
	if err != nil {
		return err
	}
	_, err = d.request(ctx, "apply_flag_settings", map[string]any{"settings": map[string]any{"effortLevel": level, "ultracode": a.Ultracode}}, true)
	return err
}

// SetUltracode asks for ultracode or not, alone in apply_flag_settings: the effort level stays.
// Turning it on for a model that cannot take it (ultracodeAvailable false: no xhigh, or workflows
// off), or on a CLI without get_settings, is agent.ErrNoUltracode; get_settings says first
// (Rhodium §6.2: an unsupported get_settings reads as unavailable).
func (d *Driver) SetUltracode(ctx context.Context, on bool) error {
	a, err := d.Applied(ctx)
	if err != nil {
		return err
	}
	if on && (!a.Known || !a.UltracodeAvailable) {
		return agent.ErrNoUltracode
	}
	_, err = d.request(ctx, "apply_flag_settings", map[string]any{"settings": map[string]any{"ultracode": on}}, true)
	return err
}

// Applied reads get_settings' applied: the model, the effort level, ultracode. A CLI without
// get_settings says nothing (Known false), which is no error.
func (d *Driver) Applied(ctx context.Context) (agent.Applied, error) {
	r, err := d.request(ctx, "get_settings", nil, true)
	if err != nil {
		if strings.HasPrefix(err.Error(), "Unsupported control request subtype") {
			return agent.Applied{}, nil
		}
		return agent.Applied{}, err
	}
	var body struct {
		Applied *struct {
			Model              string `json:"model"`
			Effort             string `json:"effort"`
			Ultracode          bool   `json:"ultracode"`
			UltracodeRequested bool   `json:"ultracodeRequested"`
			UltracodeAvailable bool   `json:"ultracodeAvailable"`
		} `json:"applied"`
	}
	_ = json.Unmarshal(r.raw(), &body)
	if body.Applied == nil {
		return agent.Applied{}, nil
	}
	a := body.Applied
	return agent.Applied{Model: a.Model, Effort: a.Effort, Ultracode: a.UltracodeRequested, UltracodeOn: a.Ultracode, UltracodeAvailable: a.UltracodeAvailable, Known: true}, nil
}

// Context asks get_context_usage, from the last answer's usage (summary: no count call per part).
func (d *Driver) Context(ctx context.Context) (*api.Context, error) {
	r, err := d.request(ctx, "get_context_usage", map[string]any{"detail": "summary"}, true)
	if err != nil {
		return nil, err
	}
	var body struct {
		Categories []struct {
			ID     string `json:"id"`
			Name   string `json:"name"`
			Tokens int    `json:"tokens"`
			Kind   string `json:"kind"`
		} `json:"categories"`
		TotalTokens int `json:"totalTokens"`
		MaxTokens   int `json:"maxTokens"`
	}
	if err := json.Unmarshal(r.raw(), &body); err != nil {
		return nil, fmt.Errorf("claude: get_context_usage: %w", err)
	}
	c := &api.Context{Used: body.TotalTokens, Max: body.MaxTokens, At: time.Now().UnixMilli()}
	for _, k := range body.Categories {
		c.Categories = append(c.Categories, api.ContextCategory{ID: k.ID, Name: k.Name, Tokens: k.Tokens, Kind: k.Kind})
	}
	return c, nil
}

// Quota asks get_usage (experimental; costs no quota): the quota windows and the session's use so
// far. A CLI without it reads as an empty answer.
func (d *Driver) Quota(ctx context.Context) (*agent.Usage, error) {
	r, err := d.request(ctx, "get_usage", map[string]any{"skip_behaviors": true}, true)
	if err != nil {
		if strings.HasPrefix(err.Error(), "Unsupported control request subtype") {
			return &agent.Usage{}, nil
		}
		return nil, err
	}
	return parseUsage(r.raw())
}

// ---- control requests ----

// request writes a control request and, if wait, waits for its answer; an error answer is an
// error with the CLI's words.
func (d *Driver) request(ctx context.Context, subtype string, fields map[string]any, wait bool) (ctlResponse, error) {
	d.mu.Lock()
	w := d.w
	if w == nil {
		d.mu.Unlock()
		return ctlResponse{}, ErrNotAttached
	}
	id := d.nextID()
	ch := make(chan ctlResponse, 1)
	if wait {
		d.pending[id] = ch
	}
	d.mu.Unlock()
	body := map[string]any{"subtype": subtype}
	for k, v := range fields {
		body[k] = v
	}
	line, _ := json.Marshal(controlRequest{Type: "control_request", RequestID: id, Request: body})
	drop := func() {
		d.mu.Lock()
		delete(d.pending, id)
		d.mu.Unlock()
	}
	if err := w(line); err != nil {
		drop()
		return ctlResponse{}, err
	}
	if !wait {
		return ctlResponse{}, nil
	}
	select {
	case r := <-ch:
		if r.Subtype == "error" {
			return r, errors.New(r.Error)
		}
		return r, nil
	case <-ctx.Done():
		drop()
		return ctlResponse{}, ctx.Err()
	}
}

func (d *Driver) nextID() string {
	d.seq++
	return fmt.Sprintf("%s%d", d.prefix, d.seq)
}

// ---- output ----

// Feed handles one line of output. Lines not starting with '{' are skipped: some CLI versions
// print debug lines to stdout.
func (d *Driver) Feed(line []byte, replay bool) {
	d.mu.Lock()
	reply := d.feed(line, replay)
	w := d.w
	evs := d.take()
	d.mu.Unlock()
	d.flush(evs)
	if reply != nil && w != nil {
		go func() { _ = w(reply) }()
	}
}

// feed handles a line with the lock held; it returns a line to write back, if any.
func (d *Driver) feed(line []byte, replay bool) []byte {
	if len(line) > MaxLine {
		d.emitLocked(agent.ProtocolError{Reason: "line_too_long", Detail: fmt.Sprintf("%d bytes, skipped", len(line))})
		return nil
	}
	if len(line) == 0 || line[0] != '{' {
		d.note("non_json")
		return nil
	}
	var f frame
	if err := json.Unmarshal(line, &f); err != nil {
		d.emitLocked(agent.ProtocolError{Reason: "bad_line", Detail: err.Error()})
		return nil
	}
	switch f.Type {
	case "control_response":
		var r ctlResponse
		if err := json.Unmarshal(f.Response, &r); err == nil {
			d.response(r)
		}
	case "control_request":
		if ask, ok := askOf(f.RequestID, f.Request); ok {
			d.asks = append(d.asks, ask)
			d.emitLocked(agent.AskOpen{Ask: ask})
			return nil
		}
		var req struct {
			Subtype string `json:"subtype"`
		}
		_ = json.Unmarshal(f.Request, &req)
		d.note("control_request/" + req.Subtype)
		if replay {
			return nil // answered by the daemon that saw it
		}
		// Unanswered, the CLI would wait forever.
		b, _ := json.Marshal(controlResponse{Type: "control_response", Response: ctlResponse{Subtype: "error", RequestID: f.RequestID, Error: "Unsupported control request subtype: " + req.Subtype}})
		return b
	case "control_cancel_request":
		// The CLI gave up on this ask (the turn was interrupted): withdraw it, never answer it.
		if i := slices.IndexFunc(d.asks, func(a api.Ask) bool { return a.RequestID == f.RequestID }); i >= 0 {
			d.asks = slices.Delete(d.asks, i, i+1)
			d.emitLocked(agent.AskCancel{RequestID: f.RequestID})
		}
	case "system":
		d.system(&f)
	case "conversation_reset":
		// Its /clear: the init after it names the new id (Cleared).
	case "stream_event":
		var ev struct {
			Type  string `json:"type"`
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				PartialJSON string `json:"partial_json"`
			} `json:"delta"`
			Block struct {
				Type string `json:"type"`
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"content_block"`
		}
		_ = json.Unmarshal(f.Event, &ev)
		// A subagent's text is its own; only the main conversation's is drafted: its words, and
		// the tool calls it writes, from their start (the call's frame comes only once it is whole).
		main := f.ParentToolUseID == nil
		switch ev.Type {
		case "content_block_start":
			if main && ev.Block.Type == "tool_use" && ev.Block.ID != "" {
				d.emitLocked(agent.ToolDraft{ID: ev.Block.ID, Name: ev.Block.Name})
			}
			return nil
		case "content_block_delta":
		case "message_start", "message_delta", "message_stop", "content_block_stop":
			return nil
		default:
			d.note("stream_event/" + ev.Type)
			return nil
		}
		switch {
		case main && ev.Delta.Type == "text_delta":
			d.emitLocked(agent.TextDelta{Text: ev.Delta.Text})
		case main && ev.Delta.Type == "input_json_delta" && ev.Delta.PartialJSON != "":
			d.emitLocked(agent.ToolDraftDelta{JSON: ev.Delta.PartialJSON})
		}
	case "assistant":
		d.assistant(&f)
	case "user":
		d.user(&f)
	case "rate_limit_event":
		if f.RateLimit != nil {
			d.emitLocked(agent.QuotaHit{Exhausted: f.RateLimit.Status == "rejected", ResetsAt: int64(f.RateLimit.ResetsAt * 1000)})
		}
	case "command_lifecycle":
		// Taken into a turn (started, completed) or never to be (cancelled). Only queued, it still
		// sits in the CLI's memory, where a crash loses it.
		if f.State == "queued" {
			return nil
		}
		i := slices.Index(d.waiting, f.CommandUUID)
		if i < 0 {
			return nil
		}
		// With a subagent in the background the CLI never said idle, so it says no running either.
		if f.State == "started" {
			d.move(agent.Running, f.CommandUUID)
		}
		d.waiting = slices.Delete(d.waiting, i, i+1)
		d.emitLocked(agent.Taken{InputID: f.CommandUUID})
	case "result":
		d.result(&f)
	default:
		d.note(f.Type)
	}
	return nil
}

func (d *Driver) response(r ctlResponse) {
	if r.RequestID == d.helloID && r.RequestID != "" {
		// In line order, so an ask after the answer is not lost to it.
		d.helloID = ""
		d.hello(r)
	}
	ch, ok := d.pending[r.RequestID]
	if !ok {
		return // an earlier daemon's or process's, or not waited on
	}
	delete(d.pending, r.RequestID)
	ch <- r
}

// hello reads initialize's answer: commands, models, version, mode, and the asks the CLI still
// waits on (a sibling of subtype; missing before 2.1.268, when no one can tell which are open).
func (d *Driver) hello(r ctlResponse) {
	var body struct {
		Commands []struct {
			Name         string `json:"name"`
			Description  string `json:"description"`
			ArgumentHint string `json:"argumentHint"`
		} `json:"commands"`
		Models []struct {
			Value          string   `json:"value"`
			Resolved       string   `json:"resolvedModel"`
			DisplayName    string   `json:"displayName"`
			Description    string   `json:"description"`
			SupportsEffort *bool    `json:"supportsEffort"`
			Efforts        []string `json:"supportedEffortLevels"`
		} `json:"models"`
		Version      string   `json:"claude_code_version"`
		Mode         string   `json:"current_permission_mode"`
		Capabilities []string `json:"capabilities"`
		Account      struct {
			Email            string `json:"email"`
			SubscriptionType string `json:"subscriptionType"`
		} `json:"account"`
	}
	_ = json.Unmarshal(r.raw(), &body)
	for _, c := range body.Capabilities {
		d.caps[c] = true
	}
	h := agent.Hello{Version: body.Version, Mode: body.Mode, Account: body.Account.Email, Plan: body.Account.SubscriptionType}
	for _, m := range body.Models {
		mm := agent.Model{Value: m.Value, Name: m.DisplayName, Description: m.Description, Resolved: m.Resolved, Efforts: m.Efforts}
		// A CLI that names the resolved model says supportsEffort for each that takes any: one
		// without it (Haiku 4.5) takes none. An older one says neither.
		if len(m.Efforts) == 0 && (m.SupportsEffort != nil && !*m.SupportsEffort || m.SupportsEffort == nil && m.Resolved != "") {
			mm.NoEffort = true
		}
		h.Models = append(h.Models, mm)
	}
	for _, c := range body.Commands {
		h.Commands = append(h.Commands, agent.Command{Name: c.Name, Description: c.Description, ArgumentHint: c.ArgumentHint})
	}
	d.emitLocked(h)
	var list []struct {
		RequestID string          `json:"request_id"`
		Request   json.RawMessage `json:"request"`
	}
	known := r.Pending != nil && json.Unmarshal(r.Pending, &list) == nil
	// Any other ask is no longer waited on; answering it would match nothing.
	d.asks = nil
	for _, p := range list {
		if a, ok := askOf(p.RequestID, p.Request); ok {
			d.asks = append(d.asks, a)
		}
	}
	d.emitLocked(agent.PendingAsks{Asks: slices.Clone(d.asks), Known: known})
}

func (d *Driver) system(f *frame) {
	switch f.Subtype {
	case "init":
		// Each turn begins with one. Past a turn that ended on its result, the next one the CLI
		// begins by itself, as a subagent's end wakes it, says so only here.
		if d.state == agent.Idle {
			d.move(agent.Running, "")
		}
		for _, c := range f.Capabilities {
			d.caps[c] = true
		}
		// The main conversation's: one of a subagent's, if any, names neither.
		if f.ParentToolUseID == nil && (f.Model != d.model || f.SessionID != d.session) {
			// Another id than the process had: its own /clear (conversation_reset comes first)
			// began a conversation of that id.
			moved := d.session != "" && f.SessionID != "" && f.SessionID != d.session
			d.model, d.session = f.Model, f.SessionID
			d.emitLocked(agent.Started{Session: f.SessionID, Model: f.Model})
			if moved {
				d.emitLocked(agent.Cleared{Session: f.SessionID})
			}
		}
	case "session_state_changed":
		var s agent.State
		switch f.State {
		case "idle":
			s = agent.Idle
		case "running":
			s = agent.Running
		case "requires_action":
			s = agent.Waiting
		default:
			d.note("session_state/" + f.State)
			return
		}
		input := ""
		if len(d.waiting) > 0 {
			input = d.waiting[0]
		}
		d.move(s, input)
	case "status":
		// "compacting" while it compacts, then a null status with the compact_result.
		if f.Status != nil && *f.Status == "compacting" {
			d.move(agent.Compacting, "")
		} else if d.state == agent.Compacting {
			d.move(agent.Running, "")
		}
		if f.CompactResult == "failed" {
			d.emitLocked(agent.Compacted{Failed: true})
		}
	case "compact_boundary":
		c := agent.Compacted{}
		if m := f.CompactMetadata; m != nil {
			c.Before, c.After = m.PreTokens, m.PostTokens
			d.context = m.PostTokens
		}
		d.emitLocked(c)
	case "background_tasks_changed":
		d.tasks, d.agents = nil, false
		for _, t := range f.Tasks {
			d.tasks = append(d.tasks, api.Task{ID: t.TaskID, Type: t.TaskType, Description: t.Description})
			d.agents = d.agents || t.TaskType != "local_bash"
		}
		d.emitLocked(agent.Tasks{Tasks: slices.Clone(d.tasks)})
	case "hook_started", "hook_response", "task_started", "task_updated", "task_progress", "task_notification", "thinking_tokens":
	default:
		d.note("system/" + f.Subtype)
	}
}

func (d *Driver) assistant(f *frame) {
	if f.ParentToolUseID != nil {
		return
	}
	var m struct {
		Usage   *usage `json:"usage"`
		Content []struct {
			Type  string `json:"type"`
			Text  string `json:"text"`
			ID    string `json:"id"`
			Name  string `json:"name"`
			Input any    `json:"input"`
		} `json:"content"`
	}
	if json.Unmarshal(f.Message, &m) != nil {
		return
	}
	if m.Usage != nil {
		d.context = m.Usage.Input + m.Usage.CacheWrite + m.Usage.CacheRead
	}
	var text strings.Builder
	for _, b := range m.Content {
		if b.Type == "text" {
			text.WriteString(b.Text)
		}
	}
	if text.Len() > 0 {
		d.reply = text.String()
		d.emitLocked(agent.MessageDone{Text: d.reply})
	}
	// What it said comes before the tool calls it makes.
	for _, b := range m.Content {
		if b.Type == "tool_use" {
			d.emitLocked(agent.ToolStart{ID: b.ID, Name: b.Name, Input: b.Input})
		}
	}
}

func (d *Driver) user(f *frame) {
	if f.ParentToolUseID != nil {
		return
	}
	var m struct {
		Content []struct {
			Type      string          `json:"type"`
			ToolUseID string          `json:"tool_use_id"`
			IsError   bool            `json:"is_error"`
			Content   json.RawMessage `json:"content"`
		} `json:"content"`
	}
	if json.Unmarshal(f.Message, &m) != nil {
		return // a string content: no tool results
	}
	for _, b := range m.Content {
		if b.Type == "tool_result" {
			denied := ""
			if b.IsError {
				denied = autoDenied(b.Content)
			}
			d.emitLocked(agent.ToolDone{ID: b.ToolUseID, Denied: denied})
		}
	}
}

// result: one per turn the CLI runs. Its usage is the turn's; total_cost_usd and modelUsage are
// the session's running totals, carried across --resume.
func (d *Driver) result(f *frame) {
	u := agent.TurnUsage{Context: d.context}
	if f.Usage != nil {
		u.Tokens = agent.Tokens{Input: f.Usage.Input, Output: f.Usage.Output, CacheRead: f.Usage.CacheRead, CacheWrite: f.Usage.CacheWrite}
	}
	if f.TotalCostUSD != nil || f.ModelUsage != nil {
		total := &agent.Usage{Models: map[string]agent.ModelUsage{}}
		if f.TotalCostUSD != nil {
			total.CostUSD = *f.TotalCostUSD
		}
		for name, m := range f.ModelUsage {
			total.Models[name] = agent.ModelUsage{Tokens: agent.Tokens{Input: m.Input, Output: m.Output, CacheRead: m.CacheRead, CacheWrite: m.CacheWrite}, CostUSD: m.CostUSD}
		}
		u.Total = total
	}
	d.emitLocked(u)
	// A subagent in the background holds back idle though the turn waits for you: the turn ends
	// on its result, unless a queued message runs next (Rhodium §6.1).
	if d.agents && f.QueuedTurnCount == 0 {
		d.move(agent.Idle, "")
	}
}

// move moves the turn to s. Leaving idle begins a turn (input: the message that began it, if
// known); coming back ends it. The CLI's idle after a turn that ended on its result is no news.
func (d *Driver) move(s agent.State, input string) {
	was := d.state
	if s == was {
		return
	}
	d.state = s
	if was == agent.Idle {
		d.reply = ""
		d.emitLocked(agent.TurnStart{Input: input})
	}
	d.emitLocked(agent.StateChange{State: s})
	if s != agent.Idle {
		return
	}
	end := agent.TurnEnd{Reply: d.reply, Background: len(d.tasks) > 0, CompactOnly: d.sent == "compact"}
	d.sent = ""
	d.emitLocked(end)
}

// note counts a kind of output the driver does not know, logged once.
func (d *Driver) note(kind string) {
	d.unknown[kind]++
	if d.unknown[kind] == 1 {
		d.logf("claude: skipping unknown %s", kind)
	}
}

// Unknown is how often each unknown kind of output came.
func (d *Driver) Unknown() map[string]int {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := map[string]int{}
	for k, v := range d.unknown {
		out[k] = v
	}
	return out
}

func (d *Driver) emitLocked(e agent.Event) { d.queue = append(d.queue, e) }

func (d *Driver) take() []agent.Event {
	q := d.queue
	d.queue = nil
	return q
}

func (d *Driver) flush(evs []agent.Event) {
	for _, e := range evs {
		d.emitFn(e)
	}
}

func (d *Driver) emit(e agent.Event) { d.emitFn(e) }

// askOf is the ask a can_use_tool request makes. AskUserQuestion is a question, ExitPlanMode a
// plan to confirm, anything else a tool to approve, whose Pattern is the CLI's own suggested
// rule for that tool (permission_suggestions), if it made one.
func askOf(id string, raw json.RawMessage) (api.Ask, bool) {
	var r struct {
		Subtype     string `json:"subtype"`
		ToolName    string `json:"tool_name"`
		Input       any    `json:"input"`
		ToolUseID   string `json:"tool_use_id"`
		Description string `json:"description"`
		Suggestions []struct {
			Type  string `json:"type"`
			Rules []struct {
				ToolName    string `json:"toolName"`
				RuleContent string `json:"ruleContent"`
			} `json:"rules"`
		} `json:"permission_suggestions"`
	}
	if id == "" || json.Unmarshal(raw, &r) != nil || r.Subtype != "can_use_tool" {
		return api.Ask{}, false
	}
	kind := "permission"
	switch r.ToolName {
	case "AskUserQuestion":
		kind = "question"
	case "ExitPlanMode":
		kind = "plan"
	}
	a := api.Ask{RequestID: id, Kind: kind, Tool: r.ToolName, Input: r.Input, ToolUseID: r.ToolUseID, Description: r.Description}
	for _, s := range r.Suggestions {
		for _, rule := range s.Rules {
			if a.Pattern == "" && s.Type == "addRules" && rule.ToolName == r.ToolName {
				a.Pattern = rule.RuleContent
			}
		}
	}
	return a, true
}

// parseUsage reads get_usage's answer: rate_limits.limits (group, percent, resets_at, a model's
// scope), or an older CLI's five_hour and seven_day; and session's running totals.
func parseUsage(raw json.RawMessage) (*agent.Usage, error) {
	type window struct {
		Utilization *float64 `json:"utilization"`
		ResetsAt    string   `json:"resets_at"`
	}
	var body struct {
		Session *struct {
			TotalCostUSD float64               `json:"total_cost_usd"`
			ModelUsage   map[string]modelUsage `json:"model_usage"`
		} `json:"session"`
		RateLimits *struct {
			Limits []struct {
				Group    string   `json:"group"`
				Percent  *float64 `json:"percent"`
				ResetsAt string   `json:"resets_at"`
				Scope    *struct {
					Model *struct {
						DisplayName string `json:"display_name"`
					} `json:"model"`
				} `json:"scope"`
			} `json:"limits"`
			FiveHour *window `json:"five_hour"`
			SevenDay *window `json:"seven_day"`
		} `json:"rate_limits"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, fmt.Errorf("claude: get_usage: %w", err)
	}
	u := &agent.Usage{Models: map[string]agent.ModelUsage{}}
	if s := body.Session; s != nil {
		u.CostUSD = s.TotalCostUSD
		for name, m := range s.ModelUsage {
			u.Models[name] = agent.ModelUsage{Tokens: agent.Tokens{Input: m.Input, Output: m.Output, CacheRead: m.CacheRead, CacheWrite: m.CacheWrite}, CostUSD: m.CostUSD}
		}
	}
	at := func(s string) int64 {
		t, err := time.Parse(time.RFC3339Nano, s)
		if err != nil {
			return 0
		}
		return t.UnixMilli()
	}
	if rl := body.RateLimits; rl != nil {
		for _, l := range rl.Limits {
			if l.Percent == nil {
				continue
			}
			w := api.QuotaWindow{Group: l.Group, Percent: *l.Percent, ResetsAt: at(l.ResetsAt)}
			if l.Scope != nil && l.Scope.Model != nil {
				w.Model = l.Scope.Model.DisplayName
			}
			u.Windows = append(u.Windows, w)
		}
		if rl.Limits == nil {
			for group, w := range map[string]*window{"session": rl.FiveHour, "weekly": rl.SevenDay} {
				if w != nil && w.Utilization != nil {
					u.Windows = append(u.Windows, api.QuotaWindow{Group: group, Percent: *w.Utilization, ResetsAt: at(w.ResetsAt)})
				}
			}
			slices.SortFunc(u.Windows, func(a, b api.QuotaWindow) int { return strings.Compare(a.Group, b.Group) })
		}
	}
	return u, nil
}

// ---- frames ----

// frame is the fields of every output line the driver reads.
type frame struct {
	Type            string          `json:"type"`
	Subtype         string          `json:"subtype"`
	RequestID       string          `json:"request_id"`
	Request         json.RawMessage `json:"request"`
	Response        json.RawMessage `json:"response"`
	Event           json.RawMessage `json:"event"`
	ParentToolUseID *string         `json:"parent_tool_use_id"`
	Message         json.RawMessage `json:"message"`
	State           string          `json:"state"`
	Status          *string         `json:"status"`
	CompactResult   string          `json:"compact_result"`
	CompactMetadata *struct {
		PreTokens  int `json:"pre_tokens"`
		PostTokens int `json:"post_tokens"`
	} `json:"compact_metadata"`
	Model        string   `json:"model"`
	SessionID    string   `json:"session_id"`
	Capabilities []string `json:"capabilities"`
	Tasks        []struct {
		TaskID      string `json:"task_id"`
		TaskType    string `json:"task_type"`
		Description string `json:"description"`
	} `json:"tasks"`
	CommandUUID string `json:"command_uuid"`
	RateLimit   *struct {
		Status   string  `json:"status"`
		ResetsAt float64 `json:"resetsAt"`
	} `json:"rate_limit_info"`
	QueuedTurnCount int                   `json:"queued_turn_count"`
	Usage           *usage                `json:"usage"`
	TotalCostUSD    *float64              `json:"total_cost_usd"`
	ModelUsage      map[string]modelUsage `json:"modelUsage"`
}

type usage struct {
	Input      int `json:"input_tokens"`
	Output     int `json:"output_tokens"`
	CacheRead  int `json:"cache_read_input_tokens"`
	CacheWrite int `json:"cache_creation_input_tokens"`
}

type modelUsage struct {
	Input      int     `json:"inputTokens"`
	Output     int     `json:"outputTokens"`
	CacheRead  int     `json:"cacheReadInputTokens"`
	CacheWrite int     `json:"cacheCreationInputTokens"`
	CostUSD    float64 `json:"costUSD"`
}

type controlRequest struct {
	Type      string         `json:"type"`
	RequestID string         `json:"request_id"`
	Request   map[string]any `json:"request"`
}

type controlResponse struct {
	Type     string      `json:"type"`
	Response ctlResponse `json:"response"`
}

// ctlResponse is a control response's inner object, both ways. Body: what we send, or the raw
// json.RawMessage the CLI sent.
type ctlResponse struct {
	Subtype   string          `json:"subtype"`
	RequestID string          `json:"request_id"`
	Body      any             `json:"response,omitempty"`
	Error     string          `json:"error,omitempty"`
	Pending   json.RawMessage `json:"pending_permission_requests,omitempty"`
}

func (r *ctlResponse) UnmarshalJSON(b []byte) error {
	type plain ctlResponse
	var p struct {
		plain
		Body json.RawMessage `json:"response"`
	}
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	*r = ctlResponse(p.plain)
	r.Body = p.Body
	if r.Body == nil {
		r.Body = json.RawMessage("{}")
	}
	return nil
}

// raw is the CLI's answer body, {} if it gave none.
func (r ctlResponse) raw() json.RawMessage {
	if b, ok := r.Body.(json.RawMessage); ok && b != nil {
		return b
	}
	return json.RawMessage("{}")
}

type userFrame struct {
	Type            string      `json:"type"`
	UUID            string      `json:"uuid"`
	ParentToolUseID *string     `json:"parent_tool_use_id"`
	Origin          *origin     `json:"origin,omitempty"`
	Message         userMessage `json:"message"`
}

type origin struct {
	Kind string `json:"kind"`
}

type userMessage struct {
	Role    string      `json:"role"`
	Content []textBlock `json:"content"`
}

// textBlock is a text block, or an image one with Source.
type textBlock struct {
	Type   string       `json:"type"`
	Text   string       `json:"text,omitempty"`
	Source *imageSource `json:"source,omitempty"`
}

type imageSource struct {
	Type      string `json:"type"` // base64
	MediaType string `json:"media_type"`
	Data      []byte `json:"data"` // base64 in JSON
}

// deniedReason is the classifier's reason in its denial: "Reason: [...]".
var deniedReason = regexp.MustCompile(`Reason: \[([^\]]+)\]`)

// autoDenied: a tool result's content (a string, or text blocks) is the auto mode classifier's
// denial; its reason, "denied" for none. "" if it is not one.
func autoDenied(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) != nil {
		var parts []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(raw, &parts) != nil {
			return ""
		}
		for _, p := range parts {
			if p.Type == "text" {
				text += p.Text
			}
		}
	}
	if !strings.Contains(text, agent.AutoDenied) {
		return ""
	}
	if r := deniedReason.FindStringSubmatch(text); r != nil {
		return strings.TrimSpace(r[1])
	}
	return "denied"
}
