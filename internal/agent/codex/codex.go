// Package codex is the Codex driver: one private `codex app-server --listen stdio://` per
// session, held by the holder (Rhodium §6.4, ADR-0011), its JSON-RPC turned into the agent
// package's operations and events. Field names follow codex-cli 0.165.0's generated types
// (`codex app-server generate-ts`) and Rhodium's recordings of 0.162.1.
package codex

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
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/kumoSleeping/iridium/internal/agent"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/hold"
)

// MaxLine: longer output lines are skipped with a protocol error.
const MaxLine = 64 << 20

// Version is what Iridium tells Codex it is (clientInfo); Codex writes the name into the
// rollout's originator.
const Version = "0.1.0"

// Modes are Codex's approval policies, as Iridium's approval modes. The sandbox is always
// danger-full-access (Rhodium §6.4: a resumed thread otherwise falls back to read-only, and the
// sandbox could not start on this machine), so the policy alone says what Codex asks:
// untrusted every command and patch; on-request what the model asks for (with full access, next
// to nothing); granular like on-request with every kind of prompt let through; auto (Codex's
// policy "never") nothing.
var Modes = []string{"untrusted", "on-request", "granular", "auto"}

// DefaultMode is the mode an empty one means: auto, asking nothing (the user's choice, 2026-11-05;
// the stricter ones stay selectable).
const DefaultMode = "auto"

// modeOf is Iridium's mode for one given: Codex's own name "never" is auto.
func modeOf(mode string) string {
	if mode == "never" {
		return "auto"
	}
	return mode
}

// optOut are deltas Iridium has no use for, left out of out.log; the reply's text stays.
var optOut = []string{
	"item/commandExecution/outputDelta",
	"item/fileChange/outputDelta",
	"item/reasoning/summaryTextDelta",
	"item/reasoning/summaryPartAdded",
	"item/reasoning/textDelta",
	"item/plan/delta",
	"turn/diff/updated",
}

// ErrNotAttached: no agent process is attached.
var ErrNotAttached = errors.New("codex: no agent process attached")

var errReplaced = errors.New("codex: the agent process ended")

// policy is the approvalPolicy for mode.
func policy(mode string) any {
	switch mode {
	case "granular":
		return map[string]any{"granular": map[string]bool{"sandbox_approval": true, "rules": true, "skill_approval": true, "request_permissions": true, "mcp_elicitations": true}}
	case "auto":
		return "never"
	}
	return mode
}

var sandboxPolicy = map[string]string{"type": "dangerFullAccess"}

type Options struct {
	Emit func(agent.Event)
	// IDPrefix starts every request id the driver makes; default "ir-" and a random part. A
	// turn/start's id is its input's instead, so its answer names the input after a restart.
	IDPrefix string
	Logf     func(format string, a ...any)
}

// open is what a process just spawned opens: a thread to resume, or a new one.
type open struct {
	cwd, thread, instructions string
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type reply struct {
	result json.RawMessage
	err    *rpcError
}

type tool struct {
	id, name string
	input    any
}

type pendingAsk struct {
	id     json.RawMessage // Codex's request id, as sent
	method string
	params map[string]any
	ask    api.Ask
}

type breakdown struct {
	Total      int `json:"totalTokens"`
	Input      int `json:"inputTokens"` // cached ones included
	Cached     int `json:"cachedInputTokens"`
	CacheWrite int `json:"cacheWriteInputTokens"`
	Output     int `json:"outputTokens"`
}

func (b breakdown) tokens() agent.Tokens {
	return agent.Tokens{Input: b.Input - b.Cached, Output: b.Output, CacheRead: b.Cached, CacheWrite: b.CacheWrite}
}

type tokenUsage struct {
	Total  breakdown `json:"total"`
	Last   breakdown `json:"last"`
	Window *int      `json:"modelContextWindow"`
}

type modelInfo struct {
	ID            string `json:"id"`
	DisplayName   string `json:"displayName"`
	Description   string `json:"description"`
	Hidden        bool   `json:"hidden"`
	IsDefault     bool   `json:"isDefault"`
	DefaultEffort string `json:"defaultReasoningEffort"`
	Efforts       []struct {
		Effort string `json:"reasoningEffort"`
	} `json:"supportedReasoningEfforts"`
}

func (m modelInfo) takes(effort string) bool {
	return slices.ContainsFunc(m.Efforts, func(e struct {
		Effort string `json:"reasoningEffort"`
	}) bool {
		return e.Effort == effort
	})
}

// Driver is one session's Codex driver. See agent.Driver.
type Driver struct {
	emitFn func(agent.Event)
	prefix string
	logf   func(string, ...any)

	mu       sync.Mutex
	w        agent.Writer
	proc     int
	seq      int
	pending  map[string]chan reply
	want     *open
	ready    chan struct{} // closed once the thread is open, or failed to (readyErr)
	readyErr error
	version  string
	thread   string
	model    string // the thread's, as Codex said opening it, or the one picked since
	effort   string
	pick     struct{ model, effort string } // what every turn/start carries
	mode     string
	models   []modelInfo // model/list's, once asked
	state    agent.State
	turn     string // the running turn's id
	starting bool   // a turn/start went out while idle; its turn has not started yet
	// startedBy: a turn's id, from turn/start's answer, to the input that began it.
	startedBy map[string]string
	prompted  chan struct{} // closed once the last turn/start was answered
	tools     map[string]tool
	asks      []pendingAsk
	reply     struct{ final, last string }
	run       agent.Tokens // the turn's tokens so far
	usage     *tokenUsage  // the thread's last thread/tokenUsage/updated
	limits    map[string]map[string]any
	subagents map[string]string // at work, by thread id: their paths
	// compact: a /compact went out; the next turn is its own. compactTurn: that turn's id.
	compact     bool
	compactTurn string
	compactDone chan struct{}
	compactAt   int // the context before a compaction item
	queue       []agent.Event
	unknown     map[string]int
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
	d := &Driver{emitFn: o.Emit, prefix: o.IDPrefix, logf: o.Logf, unknown: map[string]int{}, mode: DefaultMode}
	d.reset()
	return d
}

// SplitEffort splits "model:effort" (Codex's model ids have no colon); either may be empty.
func SplitEffort(s string) (model, effort string) {
	if i := strings.LastIndexByte(s, ':'); i >= 0 {
		return s[:i], s[i+1:]
	}
	return s, ""
}

// Spawn gives the holder's spec: Codex's private app-server, without Claude's keys or a parent
// session's variables, IRIDIUM_SESSION set. Model may carry an effort: "gpt-6-luna:low".
func (d *Driver) Spawn(o agent.SpawnOptions) (hold.Spec, error) {
	env := agent.ForeignEnv(o.Env)
	program, err := agent.LookPath(o.Program, env)
	if err != nil {
		return hold.Spec{}, err
	}
	mode := modeOf(o.Mode)
	if mode == "" {
		mode = DefaultMode
	}
	if !slices.Contains(Modes, mode) {
		return hold.Spec{}, fmt.Errorf("codex: no approval mode %q (%s)", mode, strings.Join(Modes, ", "))
	}
	instructions := ""
	if o.SystemPromptFile != "" {
		b, err := os.ReadFile(o.SystemPromptFile)
		if err != nil {
			return hold.Spec{}, err
		}
		instructions = string(b)
	}
	w := &open{cwd: o.Cwd, instructions: instructions}
	if o.Resume {
		w.thread = o.AgentSession
	}
	model, effort := SplitEffort(o.Model)
	if effort == "" {
		effort = o.Effort
	}
	d.mu.Lock()
	d.want, d.mode = w, mode
	d.pick.model, d.pick.effort = model, effort
	d.mu.Unlock()
	env = agent.SetEnv(env, "IRIDIUM_SESSION", o.Session)
	return hold.Spec{Argv: []string{program, "app-server", "--listen", "stdio://"}, Env: env, Cwd: o.Cwd}, nil
}

// Attach connects agent process proc. One just spawned gets the handshake and its thread; one an
// earlier daemon started has both, and its output fed again brings them back.
func (d *Driver) Attach(w agent.Writer, proc int) {
	d.mu.Lock()
	if proc != d.proc {
		d.resetLocked()
	}
	d.w, d.proc = w, proc
	want := d.want
	d.want = nil
	ready := d.ready
	if want == nil {
		select {
		case <-ready:
		default:
			close(ready)
		}
	}
	d.mu.Unlock()
	if want != nil {
		go d.open(proc, want, ready)
	}
}

// open does the handshake, then opens the thread: approval and sandbox every time, the prompt as
// developerInstructions (a resumed thread keeps the ones it began with, Rhodium developer-resume).
func (d *Driver) open(proc int, w *open, ready chan struct{}) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	err := func() error {
		r, err := d.call(ctx, "initialize", map[string]any{
			"clientInfo":   map[string]string{"name": "iridium", "title": "Iridium", "version": Version},
			"capabilities": map[string]any{"experimentalApi": false, "optOutNotificationMethods": optOut},
		})
		if err != nil {
			return err
		}
		var hello struct {
			UserAgent string `json:"userAgent"`
		}
		_ = json.Unmarshal(r, &hello)
		if err := d.write(map[string]any{"method": "initialized"}); err != nil {
			return err
		}
		d.mu.Lock()
		d.version = agentVersion(hello.UserAgent)
		want := d.pick.model
		d.mu.Unlock()
		// The model wanted, if Codex still has it; else Codex's default, by its id, as a resumed
		// thread keeps the model it had.
		models, err := d.listModels(ctx)
		if err != nil {
			d.logf("codex: model/list: %v", err)
		}
		d.mu.Lock()
		if want != "" && len(models) > 0 {
			m, ok := findModel(models, want)
			if !ok {
				d.logf("codex: model %s is gone, using Codex's default", want)
				m, _ = findModel(models, "default")
			}
			d.pick.model = m.ID
			if d.pick.effort != "" && len(m.Efforts) > 0 && !m.takes(d.pick.effort) {
				d.pick.effort = m.DefaultEffort
			}
		}
		params := map[string]any{"cwd": w.cwd, "approvalPolicy": policy(d.mode), "sandbox": "danger-full-access", "developerInstructions": w.instructions}
		if d.pick.model != "" {
			params["model"] = d.pick.model
		}
		d.mu.Unlock()
		if w.thread != "" {
			resume := map[string]any{"threadId": w.thread, "excludeTurns": true}
			for k, v := range params {
				resume[k] = v
			}
			_, err = d.call(ctx, "thread/resume", resume)
			if err == nil || !strings.Contains(err.Error(), "no rollout found") {
				return err
			}
			// A thread that never ran a turn has no rollout: a new one then.
			d.logf("codex: thread %s has no rollout; starting a new one", w.thread)
		}
		_, err = d.call(ctx, "thread/start", params)
		return err
	}()
	d.mu.Lock()
	if d.proc != proc {
		d.mu.Unlock()
		return
	}
	d.readyErr = err
	h := agent.Hello{Version: d.version, Mode: d.mode}
	for _, m := range d.models {
		if !m.Hidden {
			h.Models = append(h.Models, agent.Model{Value: m.ID, Name: m.DisplayName, Description: m.Description})
		}
	}
	d.emitLocked(h)
	if err != nil {
		// Nothing to do with a process without its thread: it goes, saying why.
		why := "Codex did not open the thread: " + err.Error()
		if strings.Contains(err.Error(), "already has an active writer") {
			why = "Codex says another program has the thread open (your own codex, or the app-server it left it loaded in): " + err.Error()
		}
		d.emitLocked(agent.GiveUp{Why: why})
	}
	close(ready)
	evs := d.take()
	d.mu.Unlock()
	d.flush(evs)
}

func agentVersion(ua string) string {
	_, v, ok := strings.Cut(ua, "/")
	if !ok {
		return ""
	}
	v, _, _ = strings.Cut(v, " ")
	return v
}

func findModel(models []modelInfo, id string) (modelInfo, bool) {
	for _, m := range models {
		if m.ID == id || (id == "default" && m.IsDefault) {
			return m, true
		}
	}
	return modelInfo{}, false
}

// listModels is model/list's models, asked once a process.
func (d *Driver) listModels(ctx context.Context) ([]modelInfo, error) {
	d.mu.Lock()
	if d.models != nil {
		m := d.models
		d.mu.Unlock()
		return m, nil
	}
	d.mu.Unlock()
	r, err := d.call(ctx, "model/list", map[string]any{"includeHidden": false})
	if err != nil {
		return nil, err
	}
	var body struct {
		Data []modelInfo `json:"data"`
	}
	if err := json.Unmarshal(r, &body); err != nil {
		return nil, err
	}
	if body.Data == nil {
		body.Data = []modelInfo{}
	}
	d.mu.Lock()
	d.models = body.Data
	d.mu.Unlock()
	return body.Data, nil
}

func (d *Driver) Detach() {
	d.mu.Lock()
	d.resetLocked()
	d.w = nil
	evs := d.take()
	d.mu.Unlock()
	d.flush(evs)
}

func (d *Driver) reset() {
	d.pending = map[string]chan reply{}
	d.ready = make(chan struct{})
	d.prompted = closed()
	d.startedBy = map[string]string{}
	d.tools = map[string]tool{}
	d.limits = map[string]map[string]any{}
	d.subagents = map[string]string{}
	d.state = agent.Idle
}

// resetLocked forgets the process: its requests fail, its asks are gone, the turn is over.
func (d *Driver) resetLocked() {
	for id, ch := range d.pending {
		ch <- reply{err: &rpcError{Message: errReplaced.Error()}}
		delete(d.pending, id)
	}
	select {
	case <-d.ready:
	default:
		d.readyErr = errReplaced
		close(d.ready)
	}
	if d.compactDone != nil {
		close(d.compactDone)
	}
	d.compact, d.compactTurn, d.compactDone = false, "", nil
	d.readyErr = nil
	d.thread, d.model, d.effort, d.version = "", "", "", ""
	d.models, d.usage, d.asks = nil, nil, nil
	d.turn, d.starting = "", false
	d.reply.final, d.reply.last = "", ""
	d.run = agent.Tokens{}
	d.reset()
}

func closed() chan struct{} {
	c := make(chan struct{})
	close(c)
	return c
}

func (d *Driver) State() agent.State {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.state
}

// waitReady waits for the thread to open.
func (d *Driver) waitReady(ctx context.Context) error {
	d.mu.Lock()
	ready := d.ready
	d.mu.Unlock()
	select {
	case <-ready:
	case <-ctx.Done():
		return ctx.Err()
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.readyErr != nil {
		return d.readyErr
	}
	if d.w == nil {
		return ErrNotAttached
	}
	if d.thread == "" {
		return errors.New("codex: no thread open")
	}
	return nil
}

// Send writes every message as a turn/start: while a turn runs Codex makes it join that turn
// (Rhodium start-mid-turn), so no steer comes too late. Its id and clientUserMessageId are the
// input's: the user message it becomes carries it. "/compact" compacts the thread instead.
func (d *Driver) Send(ctx context.Context, m agent.Message) error {
	if m.ID == "" || m.Text == "" {
		return errors.New("codex: a message needs an id and text")
	}
	if err := d.waitReady(ctx); err != nil {
		return err
	}
	if strings.TrimSpace(m.Text) == "/compact" {
		return d.startCompact(ctx, m.ID)
	}
	for {
		// Codex refuses a turn/start while it compacts: the message waits for the compaction's end.
		d.mu.Lock()
		done := d.compactDone
		d.mu.Unlock()
		if done == nil {
			break
		}
		select {
		case <-done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	d.mu.Lock()
	if d.w == nil {
		d.mu.Unlock()
		return ErrNotAttached
	}
	idle := !d.starting && d.state == agent.Idle
	if idle {
		d.starting = true
	}
	params := map[string]any{
		"threadId":            d.thread,
		"input":               []map[string]any{{"type": "text", "text": m.Text, "text_elements": []any{}}},
		"clientUserMessageId": m.ID,
		"approvalPolicy":      policy(d.mode),
		"sandboxPolicy":       sandboxPolicy,
	}
	if d.pick.model != "" {
		params["model"] = d.pick.model
	}
	if d.pick.effort != "" {
		params["effort"] = d.pick.effort
	}
	ch := make(chan reply, 1)
	d.pending[m.ID] = ch
	prompted := make(chan struct{})
	d.prompted = prompted
	d.mu.Unlock()
	go func() {
		<-ch
		close(prompted)
	}()
	if err := d.write(map[string]any{"id": m.ID, "method": "turn/start", "params": params}); err != nil {
		d.mu.Lock()
		delete(d.pending, m.ID)
		if idle {
			d.starting = false
		}
		d.mu.Unlock()
		ch <- reply{}
		return err
	}
	return nil
}

// startCompact compacts the thread between turns: Codex runs it as a turn of its own, a
// contextCompaction item in it, which ends CompactOnly.
func (d *Driver) startCompact(ctx context.Context, input string) error {
	d.mu.Lock()
	if d.starting || d.state != agent.Idle {
		d.mu.Unlock()
		return errors.New("codex: a turn is running; stop it before /compact")
	}
	d.compact, d.starting = true, true
	d.compactDone = make(chan struct{})
	thread := d.thread
	d.emitLocked(agent.Taken{InputID: input})
	evs := d.take()
	d.mu.Unlock()
	d.flush(evs)
	if _, err := d.call(ctx, "thread/compact/start", map[string]any{"threadId": thread}); err != nil {
		d.mu.Lock()
		d.endCompactLocked()
		d.starting = false
		d.mu.Unlock()
		return err
	}
	return nil
}

func (d *Driver) endCompactLocked() {
	if d.compactDone != nil {
		close(d.compactDone)
	}
	d.compact, d.compactTurn, d.compactDone = false, "", nil
}

// Interrupt stops the running turn, once the last turn/start is answered: it may be the one that
// starts it.
func (d *Driver) Interrupt(ctx context.Context) error {
	d.mu.Lock()
	prompted := d.prompted
	d.mu.Unlock()
	select {
	case <-prompted:
	case <-ctx.Done():
		return ctx.Err()
	}
	d.mu.Lock()
	thread, turn := d.thread, d.turn
	d.mu.Unlock()
	if thread == "" || turn == "" {
		return nil
	}
	_, err := d.call(ctx, "turn/interrupt", map[string]any{"threadId": thread, "turnId": turn})
	return err
}

// Answer answers what Codex asked under its own request id. Codex's no takes no words: a message
// that comes with one goes into the turn that asked, right after it (Rhodium §6.4).
func (d *Driver) Answer(ctx context.Context, a api.Answer) error {
	d.mu.Lock()
	i := slices.IndexFunc(d.asks, func(p pendingAsk) bool { return p.ask.RequestID == a.RequestID })
	if i < 0 {
		d.mu.Unlock()
		return fmt.Errorf("codex: no open ask %s", a.RequestID)
	}
	p := d.asks[i]
	if a.Kind != "" && a.Kind != p.ask.Kind {
		d.mu.Unlock()
		return fmt.Errorf("codex: ask %s is a %s, not a %s", a.RequestID, p.ask.Kind, a.Kind)
	}
	if d.w == nil {
		d.mu.Unlock()
		return ErrNotAttached
	}
	d.asks = slices.Delete(d.asks, i, i+1)
	proc, thread, turn := d.proc, d.thread, d.turn
	d.mu.Unlock()
	if err := d.write(map[string]any{"id": p.id, "result": result(p, a)}); err != nil {
		d.mu.Lock()
		if d.proc == proc {
			d.asks = append(d.asks, p)
		}
		d.mu.Unlock()
		return err
	}
	d.mu.Lock()
	if len(d.asks) == 0 && d.state == agent.Waiting {
		d.move(agent.Running, "")
	}
	d.emitLocked(agent.AskCancel{RequestID: a.RequestID})
	evs := d.take()
	d.mu.Unlock()
	d.flush(evs)
	if p.ask.Kind == "permission" && !a.Allow && a.Message != "" && thread != "" {
		expected, _ := p.params["turnId"].(string)
		if expected == "" {
			expected = turn
		}
		input := []map[string]any{{"type": "text", "text": a.Message, "text_elements": []any{}}}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if _, err := d.call(ctx, "turn/steer", map[string]any{"threadId": thread, "input": input, "expectedTurnId": expected}); err != nil {
				d.emit(agent.ProtocolError{Reason: "unexpected", Detail: "Codex did not take the words that came with the no: " + err.Error()})
			}
		}()
	}
	return nil
}

// result is Codex's answer to a request put to you (Rhodium §6.4).
func result(p pendingAsk, a api.Answer) any {
	if p.ask.Kind == "question" {
		answers := map[string]any{}
		qs, _ := p.params["questions"].([]any)
		for _, q := range qs {
			q, _ := q.(map[string]any)
			id, _ := q["id"].(string)
			text, _ := q["question"].(string)
			answers[id] = map[string]any{"answers": []string{a.Answers[text]}}
		}
		return map[string]any{"answers": answers}
	}
	switch p.method {
	case "item/permissions/requestApproval":
		perms := map[string]any{}
		if a.Allow {
			if asked, ok := p.params["permissions"].(map[string]any); ok {
				for k, v := range asked {
					if v != nil {
						perms[k] = v
					}
				}
			}
		}
		return map[string]any{"permissions": perms, "scope": "turn"}
	case "mcpServer/elicitation/request":
		action := "decline"
		if a.Allow {
			action = "accept"
		}
		return map[string]any{"action": action, "content": nil, "_meta": nil}
	}
	if a.Allow {
		return map[string]any{"decision": "accept"}
	}
	return map[string]any{"decision": "decline"}
}

// SetModel picks the model, "model" or "model:effort" ("default": Codex's own), from the next
// turn/start on, which carries it. An effort the model does not take moves to its default.
func (d *Driver) SetModel(ctx context.Context, model string) error {
	if err := d.waitReady(ctx); err != nil {
		return err
	}
	name, effort := SplitEffort(model)
	models, err := d.listModels(ctx)
	if err != nil {
		return err
	}
	m, ok := findModel(models, name)
	if !ok {
		return fmt.Errorf("codex: no model %q", name)
	}
	d.mu.Lock()
	if effort == "" {
		effort = d.pick.effort
		if effort == "" {
			effort = d.effort
		}
	}
	if effort != "" && len(m.Efforts) > 0 && !m.takes(effort) {
		if model != name {
			d.mu.Unlock()
			return fmt.Errorf("codex: %s takes no effort %q", m.ID, effort)
		}
		effort = m.DefaultEffort
	}
	d.pick.model, d.pick.effort = m.ID, effort
	d.model = m.ID
	d.emitLocked(agent.Started{Session: d.thread, Model: m.ID})
	evs := d.take()
	d.mu.Unlock()
	d.flush(evs)
	return nil
}

// SetEffort sets the reasoning effort from the next turn/start on, which carries it, if the model
// takes it.
func (d *Driver) SetEffort(ctx context.Context, level string) error {
	if err := d.waitReady(ctx); err != nil {
		return err
	}
	d.mu.Lock()
	name := d.pick.model
	if name == "" {
		name = d.model
	}
	d.mu.Unlock()
	if name != "" {
		models, err := d.listModels(ctx)
		if err != nil {
			return err
		}
		if m, ok := findModel(models, name); ok && len(m.Efforts) > 0 && !m.takes(level) {
			return fmt.Errorf("codex: %s takes no effort %q", m.ID, level)
		}
	}
	d.mu.Lock()
	d.pick.effort = level
	d.mu.Unlock()
	return nil
}

// SetMode switches the approval policy from the next turn/start on, which carries it.
func (d *Driver) SetMode(ctx context.Context, mode string) error {
	mode = modeOf(mode)
	if !slices.Contains(Modes, mode) {
		return fmt.Errorf("codex: no approval mode %q (%s)", mode, strings.Join(Modes, ", "))
	}
	d.mu.Lock()
	d.mode = mode
	d.mu.Unlock()
	return nil
}

// Context is how full the context is: the last model request's tokens out of the model's
// window, as Codex's own terminal counts it. Codex gives no breakdown.
func (d *Driver) Context(ctx context.Context) (*api.Context, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.usage == nil {
		return nil, errors.New("codex: no context usage until the thread's first answer")
	}
	c := &api.Context{Used: d.usage.Last.Total, At: time.Now().UnixMilli()}
	if d.usage.Window != nil {
		c.Max = *d.usage.Window
	}
	return c, nil
}

// window names a quota window by its length in minutes, as Claude's are named.
var window = map[float64]string{300: "session", 10080: "weekly"}

// Quota reads the account's quota windows (account/rateLimits/read; none for an API key or own
// provider) and the thread's use so far (account/usage/read for the thread, else the last
// thread/tokenUsage/updated's total). Neither costs quota.
func (d *Driver) Quota(ctx context.Context) (*agent.Usage, error) {
	if err := d.waitReady(ctx); err != nil {
		return nil, err
	}
	u := &agent.Usage{Models: map[string]agent.ModelUsage{}}
	if r, err := d.call(ctx, "account/rateLimits/read", map[string]any{}); err == nil {
		u.Windows = d.windows(ctx, r)
	}
	d.mu.Lock()
	thread, model, usage := d.thread, d.model, d.usage
	d.mu.Unlock()
	if r, err := d.call(ctx, "account/usage/read", map[string]any{"threadId": thread}); err == nil {
		var body struct {
			Thread *struct {
				USDMicros *float64 `json:"estimatedUsageUsdMicros"`
				Groups    []struct {
					Model  *string  `json:"model"`
					Input  *float64 `json:"inputTokens"`
					Cached *float64 `json:"cachedInputTokens"`
					Output *float64 `json:"outputTokens"`
				} `json:"groups"`
			} `json:"threadUsage"`
		}
		if json.Unmarshal(r, &body) == nil && body.Thread != nil && len(body.Thread.Groups) > 0 {
			if body.Thread.USDMicros != nil {
				u.CostUSD = *body.Thread.USDMicros / 1e6
			}
			n := func(p *float64) int {
				if p == nil {
					return 0
				}
				return int(*p)
			}
			for _, g := range body.Thread.Groups {
				name := model
				if g.Model != nil {
					name = *g.Model
				}
				mu := u.Models[name]
				mu.Input += n(g.Input) - n(g.Cached)
				mu.CacheRead += n(g.Cached)
				mu.Output += n(g.Output)
				u.Models[name] = mu
			}
			return u, nil
		}
	}
	if usage != nil {
		u.Models[model] = agent.ModelUsage{Tokens: usage.Total.tokens()}
	}
	return u, nil
}

// windows is account/rateLimits/read's windows: the main bucket, then a model's.
func (d *Driver) windows(ctx context.Context, raw json.RawMessage) []api.QuotaWindow {
	type win struct {
		Used    float64  `json:"usedPercent"`
		Mins    *float64 `json:"windowDurationMins"`
		ResetAt *float64 `json:"resetsAt"`
	}
	type bucket struct {
		LimitID   *string `json:"limitId"`
		ModelSlug *string `json:"normalModelSlug"`
		Primary   *win    `json:"primary"`
		Secondary *win    `json:"secondary"`
	}
	var body struct {
		Main bucket            `json:"rateLimits"`
		By   map[string]bucket `json:"rateLimitsByLimitId"`
	}
	if json.Unmarshal(raw, &body) != nil {
		return nil
	}
	buckets := []bucket{body.Main}
	keys := make([]string, 0, len(body.By))
	for k := range body.By {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		b := body.By[k]
		if b.LimitID != nil && body.Main.LimitID != nil && *b.LimitID == *body.Main.LimitID {
			continue
		}
		buckets = append(buckets, b)
	}
	var out []api.QuotaWindow
	for _, b := range buckets {
		model := ""
		if b.ModelSlug != nil {
			model = *b.ModelSlug
			if models, err := d.listModels(ctx); err == nil {
				if m, ok := findModel(models, model); ok && m.DisplayName != "" {
					model = m.DisplayName
				}
			}
		}
		for _, w := range []*win{b.Primary, b.Secondary} {
			if w == nil || w.Mins == nil || window[*w.Mins] == "" {
				continue
			}
			q := api.QuotaWindow{Group: window[*w.Mins], Model: model, Percent: w.Used}
			if w.ResetAt != nil {
				q.ResetsAt = int64(*w.ResetAt * 1000)
			}
			out = append(out, q)
		}
	}
	return out
}

// ---- requests ----

func (d *Driver) nextID() string {
	d.seq++
	return fmt.Sprintf("%s%d", d.prefix, d.seq)
}

// call writes a request and waits for its answer; an error answer is an error with Codex's words.
func (d *Driver) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	d.mu.Lock()
	if d.w == nil {
		d.mu.Unlock()
		return nil, ErrNotAttached
	}
	id := d.nextID()
	ch := make(chan reply, 1)
	d.pending[id] = ch
	d.mu.Unlock()
	drop := func() {
		d.mu.Lock()
		delete(d.pending, id)
		d.mu.Unlock()
	}
	if err := d.write(map[string]any{"id": id, "method": method, "params": params}); err != nil {
		drop()
		return nil, err
	}
	select {
	case r := <-ch:
		if r.err != nil {
			return nil, errors.New(r.err.Message)
		}
		return r.result, nil
	case <-ctx.Done():
		drop()
		return nil, ctx.Err()
	}
}

func (d *Driver) write(msg any) error {
	d.mu.Lock()
	w := d.w
	d.mu.Unlock()
	if w == nil {
		return ErrNotAttached
	}
	line, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return w(line)
}

// ---- output ----

type message struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

type item struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	ClientID  *string         `json:"clientId"`
	Text      string          `json:"text"`
	Phase     string          `json:"phase"`
	Command   string          `json:"command"`
	Changes   json.RawMessage `json:"changes"`
	Query     string          `json:"query"`
	Path      string          `json:"path"`
	Server    string          `json:"server"`
	Tool      string          `json:"tool"`
	Arguments json.RawMessage `json:"arguments"`
	Kind      string          `json:"kind"`
	AgentID   string          `json:"agentThreadId"`
	AgentPath string          `json:"agentPath"`
}

// Feed handles one line of output. replay: an earlier daemon saw it; it brings state back and
// is never answered.
func (d *Driver) Feed(line []byte, replay bool) {
	d.mu.Lock()
	answer := d.feed(line, replay)
	w := d.w
	evs := d.take()
	d.mu.Unlock()
	d.flush(evs)
	if answer != nil && w != nil {
		go func() { _ = w(answer) }()
	}
}

func (d *Driver) feed(line []byte, replay bool) []byte {
	if len(line) > MaxLine {
		d.emitLocked(agent.ProtocolError{Reason: "line_too_long", Detail: fmt.Sprintf("%d bytes, skipped", len(line))})
		return nil
	}
	if len(line) == 0 || line[0] != '{' {
		d.note("non_json")
		return nil
	}
	var m message
	if err := json.Unmarshal(line, &m); err != nil {
		d.emitLocked(agent.ProtocolError{Reason: "bad_line", Detail: err.Error()})
		return nil
	}
	if m.Method == "" {
		d.response(&m)
		return nil
	}
	if len(m.ID) > 0 && string(m.ID) != "null" {
		return d.asked(&m, replay)
	}
	var p struct {
		ThreadID   *string         `json:"threadId"`
		Turn       json.RawMessage `json:"turn"`
		Item       json.RawMessage `json:"item"`
		Delta      string          `json:"delta"`
		TokenUsage *tokenUsage     `json:"tokenUsage"`
		RequestID  json.RawMessage `json:"requestId"`
		RateLimits map[string]any  `json:"rateLimits"`
	}
	_ = json.Unmarshal(m.Params, &p)
	// A subagent's thread is not the conversation: its turns, items and tokens are its own.
	if d.thread != "" && p.ThreadID != nil && *p.ThreadID != d.thread {
		return nil
	}
	switch m.Method {
	case "turn/started":
		var t struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(p.Turn, &t)
		d.starting = false
		if t.ID == "" || d.turn == t.ID {
			return nil
		}
		d.turn = t.ID
		if d.compact && d.compactTurn == "" {
			d.compactTurn = t.ID
		}
		input := d.startedBy[t.ID]
		delete(d.startedBy, t.ID)
		d.move(agent.Running, input)
	case "item/started":
		var it item
		_ = json.Unmarshal(p.Item, &it)
		switch it.Type {
		case "userMessage":
			// Your message joins the conversation under the id it was sent with.
			if it.ClientID != nil && *it.ClientID != "" && !replay {
				d.emitLocked(agent.Taken{InputID: *it.ClientID})
			}
		case "subAgentActivity":
			d.subAgent(it)
		case "contextCompaction":
			if d.usage != nil {
				d.compactAt = d.usage.Last.Total
			}
			d.move(agent.Compacting, "")
		default:
			if name, input, ok := toolOf(it); ok {
				d.tools[it.ID] = tool{it.ID, name, input}
				d.emitLocked(agent.ToolStart{ID: it.ID, Name: name, Input: input})
			}
		}
	case "item/completed":
		var it item
		_ = json.Unmarshal(p.Item, &it)
		switch it.Type {
		case "agentMessage":
			if it.Text != "" {
				d.reply.last = it.Text
				if it.Phase == "final_answer" {
					d.reply.final = it.Text
				}
				d.emitLocked(agent.MessageDone{Text: it.Text})
			}
		case "subAgentActivity":
			d.subAgent(it)
		case "contextCompaction":
			c := agent.Compacted{Before: d.compactAt}
			if d.usage != nil {
				c.After = d.usage.Last.Total
			}
			d.emitLocked(c)
			if d.state == agent.Compacting {
				d.move(agent.Running, "")
			}
		default:
			if t, ok := d.tools[it.ID]; ok {
				delete(d.tools, it.ID)
				d.emitLocked(agent.ToolDone{ID: t.id, Name: t.name})
			} else if name, _, ok := toolOf(it); ok {
				d.emitLocked(agent.ToolDone{ID: it.ID, Name: name})
			}
		}
	case "item/agentMessage/delta":
		d.emitLocked(agent.TextDelta{Text: p.Delta})
	case "thread/tokenUsage/updated":
		if p.TokenUsage == nil {
			return nil
		}
		d.usage = p.TokenUsage
		// One model request's tokens; thread/resume sends the last again, outside any turn.
		if d.turn != "" {
			t := p.TokenUsage.Last.tokens()
			d.run = agent.Tokens{Input: d.run.Input + t.Input, Output: d.run.Output + t.Output, CacheRead: d.run.CacheRead + t.CacheRead, CacheWrite: d.run.CacheWrite + t.CacheWrite}
		}
	case "serverRequest/resolved":
		// Settled without you, as by an interrupt: withdrawn. One you answered is gone already.
		id := rawID(p.RequestID)
		if i := slices.IndexFunc(d.asks, func(a pendingAsk) bool { return a.ask.RequestID == id }); i >= 0 {
			d.asks = slices.Delete(d.asks, i, i+1)
			d.emitLocked(agent.AskCancel{RequestID: id})
			if len(d.asks) == 0 && d.state == agent.Waiting {
				d.move(agent.Running, "")
			}
		}
	case "account/rateLimits/updated":
		d.rated(p.RateLimits)
	case "turn/completed":
		d.completed(p.Turn)
	case "thread/status/changed", "thread/started", "configWarning", "remoteControl/status/changed", "account/updated",
		"mcpServer/startupStatus/updated", "deprecationNotice", "turn/moderationMetadata", "thread/goal/cleared",
		"hook/started", "hook/completed", "thread/settings/updated", "turn/diff/updated", "thread/queue/changed",
		"thread/name/updated", "skills/changed", "turn/plan/updated", "thread/compacted", "item/reasoning/summaryTextDelta":
	default:
		d.note(m.Method)
	}
	return nil
}

func rawID(r json.RawMessage) string {
	var s string
	if json.Unmarshal(r, &s) == nil {
		return s
	}
	return string(r)
}

// response is an answer. What it says of the thread and the turn counts also when fed again.
func (d *Driver) response(m *message) {
	id := rawID(m.ID)
	var r struct {
		Thread *struct {
			ID string `json:"id"`
		} `json:"thread"`
		Model  *string `json:"model"`
		Effort *string `json:"reasoningEffort"`
		Turn   *struct {
			ID string `json:"id"`
		} `json:"turn"`
		Policy json.RawMessage `json:"approvalPolicy"`
	}
	_ = json.Unmarshal(m.Result, &r)
	if r.Thread != nil && r.Thread.ID != "" {
		d.thread = r.Thread.ID
		// The policy it opened with: a process an earlier daemon started keeps the session's.
		var s string
		var g map[string]any
		if json.Unmarshal(r.Policy, &s) == nil && slices.Contains(Modes, modeOf(s)) {
			d.mode = modeOf(s)
		} else if json.Unmarshal(r.Policy, &g) == nil && g["granular"] != nil {
			d.mode = "granular"
		}
		if r.Model != nil {
			d.model = *r.Model
		}
		if r.Effort != nil {
			d.effort = *r.Effort
		}
		if d.pick.model != "" {
			d.model = d.pick.model
		}
		d.emitLocked(agent.Started{Session: d.thread, Model: d.model})
	}
	// A turn/start's id is its input's (never the driver's own prefix "ir-"): the turn it starts
	// begins with it; one that joins the running turn names that one. One refused never joins.
	input := ""
	if !strings.HasPrefix(id, "ir-") && id != "" && m.ID[0] == '"' {
		input = id
	}
	if input != "" && r.Turn != nil && r.Turn.ID != "" && r.Turn.ID != d.turn {
		d.startedBy[r.Turn.ID] = input
	}
	if input != "" && m.Error != nil {
		d.emitLocked(agent.Taken{InputID: input})
		d.emitLocked(agent.ProtocolError{Reason: "unexpected", Detail: "Codex refused a message: " + m.Error.Message})
		if d.state == agent.Idle {
			d.starting = false
		}
	}
	if ch, ok := d.pending[id]; ok {
		delete(d.pending, id)
		ch <- reply{result: m.Result, err: m.Error}
	}
}

// asked: Codex asks something. An approval or a question is put to you; an MCP server's own form
// or link is declined, and anything else gets an error at once, or its turn would wait for good.
func (d *Driver) asked(m *message, replay bool) []byte {
	params := map[string]any{}
	_ = json.Unmarshal(m.Params, &params)
	if ask, ok := d.toAsk(m, params); ok {
		d.asks = append(d.asks, pendingAsk{id: m.ID, method: m.Method, params: params, ask: ask})
		d.emitLocked(agent.AskOpen{Ask: ask})
		if d.state == agent.Running {
			d.move(agent.Waiting, "")
		}
		return nil
	}
	d.note("request/" + m.Method)
	if replay {
		return nil
	}
	var answer map[string]any
	if m.Method == "mcpServer/elicitation/request" {
		answer = map[string]any{"id": m.ID, "result": map[string]any{"action": "decline", "content": nil, "_meta": nil}}
	} else {
		answer = map[string]any{"id": m.ID, "error": map[string]any{"code": -32601, "message": "iridium does not handle " + m.Method}}
	}
	b, _ := json.Marshal(answer)
	return b
}

func (d *Driver) toAsk(m *message, p map[string]any) (api.Ask, bool) {
	id := rawID(m.ID)
	str := func(k string) string { s, _ := p[k].(string); return s }
	itemID := str("itemId")
	a := api.Ask{RequestID: id, Kind: "permission", ToolUseID: itemID, Description: str("reason")}
	switch m.Method {
	case "item/commandExecution/requestApproval":
		a.Tool, a.Input = "exec_command", map[string]any{"command": typedCommand(str("command"))}
	case "item/fileChange/requestApproval":
		// It has no changes: the item it asks about came with them.
		a.Tool, a.Input = "apply_patch", map[string]any{"changes": []any{}}
		if t, ok := d.tools[itemID]; ok {
			a.Input = t.input
		}
	case "item/permissions/requestApproval":
		a.Tool, a.Input = "request_permissions", p["permissions"]
	case "item/tool/requestUserInput":
		var qs []map[string]any
		raw, _ := p["questions"].([]any)
		for _, q := range raw {
			q, _ := q.(map[string]any)
			opts := q["options"]
			if opts == nil {
				opts = []any{}
			}
			qs = append(qs, map[string]any{"question": q["question"], "header": q["header"], "options": opts, "multiSelect": false})
		}
		a.Kind, a.Tool, a.Input = "question", "request_user_input", map[string]any{"questions": qs}
	case "mcpServer/elicitation/request":
		// An MCP tool's approval is a form marked so; it asks about that server's call just begun.
		meta, _ := p["_meta"].(map[string]any)
		if str("mode") != "form" || meta == nil || meta["codex_approval_kind"] != "mcp_tool_call" {
			return api.Ask{}, false
		}
		server := str("serverName")
		a.Tool, a.Input = "mcp__"+server, meta["tool_params"]
		for _, t := range d.tools {
			if strings.HasPrefix(t.name, "mcp__"+server+"__") {
				a.Tool, a.Input, a.ToolUseID = t.name, t.input, t.id
			}
		}
	default:
		return api.Ask{}, false
	}
	return a, true
}

// subAgent: a subagent Codex set to work is a background task, which keeps the process; its end
// wakes no turn here.
func (d *Driver) subAgent(it item) {
	switch it.Kind {
	case "started":
		if _, ok := d.subagents[it.AgentID]; ok {
			return
		}
		d.subagents[it.AgentID] = cmpOr(it.AgentPath, it.AgentID)
	case "completed", "interrupted":
		if _, ok := d.subagents[it.AgentID]; !ok {
			return
		}
		delete(d.subagents, it.AgentID)
	default:
		return
	}
	tasks := []api.Task{}
	for id, path := range d.subagents {
		tasks = append(tasks, api.Task{ID: id, Type: "subagent", Description: path})
	}
	slices.SortFunc(tasks, func(a, b api.Task) int { return strings.Compare(a.ID, b.ID) })
	d.emitLocked(agent.Tasks{Tasks: tasks})
}

// rated merges a bucket of the account's quota into what Codex said of it before: an update
// leaves out what it does not know.
func (d *Driver) rated(b map[string]any) {
	if b == nil {
		return
	}
	id, _ := b["limitId"].(string)
	if id == "" {
		id = "codex"
	}
	was := d.limits[id]
	if was == nil {
		was = map[string]any{}
	}
	for k, v := range b {
		if v != nil {
			was[k] = v
		}
	}
	d.limits[id] = was
}

// reset is when the quota used up comes back (ms): the latest reset of the full windows; 0 if none.
func (d *Driver) resetAt() int64 {
	var at int64
	for _, b := range d.limits {
		for _, k := range []string{"primary", "secondary"} {
			w, _ := b[k].(map[string]any)
			used, _ := w["usedPercent"].(float64)
			r, ok := w["resetsAt"].(float64)
			if ok && used >= 100 && int64(r*1000) > at {
				at = int64(r * 1000)
			}
		}
	}
	return at
}

// completed ends the turn: a command it cut short never completes and is closed off here; what
// it still asked is void.
func (d *Driver) completed(raw json.RawMessage) {
	var t struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Error  *struct {
			Message string `json:"message"`
			Info    any    `json:"codexErrorInfo"`
		} `json:"error"`
	}
	_ = json.Unmarshal(raw, &t)
	for _, a := range d.asks {
		d.emitLocked(agent.AskCancel{RequestID: a.ask.RequestID})
	}
	d.asks = nil
	for _, tl := range d.tools {
		d.emitLocked(agent.ToolDone{ID: tl.id, Name: tl.name})
	}
	d.tools = map[string]tool{}
	compact := d.compact && (d.compactTurn == "" || d.compactTurn == t.ID)
	reply := cmpOr(d.reply.final, d.reply.last)
	if t.Error != nil {
		if info, _ := t.Error.Info.(string); info == "usageLimitExceeded" {
			d.emitLocked(agent.QuotaHit{Exhausted: true, ResetsAt: d.resetAt()})
			reply = t.Error.Message
		} else if t.Status == "failed" {
			d.emitLocked(agent.ProtocolError{Reason: "turn_failed", Detail: "Codex: " + t.Error.Message})
		}
		if compact {
			d.emitLocked(agent.Compacted{Failed: true})
		}
	}
	u := agent.TurnUsage{Tokens: d.run}
	if d.usage != nil {
		u.Context = d.usage.Last.Total
	}
	if u.Tokens != (agent.Tokens{}) {
		d.emitLocked(u)
	}
	d.run = agent.Tokens{}
	d.turn, d.starting = "", false
	if compact {
		d.endCompactLocked()
	}
	d.state = agent.Idle
	d.emitLocked(agent.StateChange{State: agent.Idle})
	d.emitLocked(agent.TurnEnd{Reply: reply, Background: len(d.subagents) > 0, CompactOnly: compact})
	d.reply.final, d.reply.last = "", ""
}

// move moves the turn to s; leaving idle begins it (input: the message that began it).
func (d *Driver) move(s agent.State, input string) {
	was := d.state
	if s == was {
		return
	}
	d.state = s
	if was == agent.Idle {
		d.reply.final, d.reply.last = "", ""
		d.emitLocked(agent.TurnStart{Input: input})
	}
	d.emitLocked(agent.StateChange{State: s})
}

func (d *Driver) note(kind string) {
	d.unknown[kind]++
	if d.unknown[kind] == 1 {
		d.logf("codex: skipping unknown %s", kind)
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

// Thread is the thread open on the process, "" before.
func (d *Driver) Thread() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.thread
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

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// toolOf is a tool-like item as a call, by the name Codex's model calls the tool (Rhodium §6.4's
// table), and what it was given; ok false for no tool.
func toolOf(it item) (string, any, bool) {
	var args any = map[string]any{}
	if len(it.Arguments) > 0 && string(it.Arguments) != "null" {
		_ = json.Unmarshal(it.Arguments, &args)
	}
	switch it.Type {
	case "commandExecution":
		return "exec_command", map[string]any{"command": typedCommand(it.Command)}, true
	case "fileChange":
		var changes any = []any{}
		if len(it.Changes) > 0 {
			_ = json.Unmarshal(it.Changes, &changes)
		}
		return "apply_patch", map[string]any{"changes": changes}, true
	case "webSearch":
		return "web_search", map[string]any{"query": it.Query}, true
	case "imageView":
		return "view_image", map[string]any{"path": it.Path}, true
	case "imageGeneration":
		return "image_gen", map[string]any{}, true
	case "mcpToolCall":
		return "mcp__" + it.Server + "__" + it.Tool, args, true
	case "dynamicToolCall":
		return it.Tool, args, true
	case "collabAgentToolCall":
		var b strings.Builder
		for _, c := range it.Tool {
			if c >= 'A' && c <= 'Z' {
				b.WriteByte('_')
				c += 'a' - 'A'
			}
			b.WriteRune(c)
		}
		return b.String(), map[string]any{}, true
	}
	return "", nil, false
}

// typedCommand is the command as typed: Codex runs it as one quoted string after `<shell> -lc`
// or `-c` (bash here, zsh in Rhodium's recordings); anything else is shown whole.
func typedCommand(command string) string {
	w, ok := words(command)
	if ok && len(w) == 3 && (w[1] == "-lc" || w[1] == "-c") {
		switch filepath.Base(w[0]) {
		case "bash", "zsh", "sh":
			return w[2]
		}
	}
	return command
}

// words splits a shell line into words, quotes taken off; ok false if a quote is left open.
func words(s string) ([]string, bool) {
	var out []string
	var word strings.Builder
	in := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\'':
			end := strings.IndexByte(s[i+1:], '\'')
			if end < 0 {
				return nil, false
			}
			word.WriteString(s[i+1 : i+1+end])
			i += end + 1
			in = true
		case c == '"':
			i++
			for ; i < len(s) && s[i] != '"'; i++ {
				if s[i] == '\\' && i+1 < len(s) && strings.IndexByte("$`\"\\\n", s[i+1]) >= 0 {
					i++
				}
				word.WriteByte(s[i])
			}
			if i >= len(s) {
				return nil, false
			}
			in = true
		case c == '\\':
			if i+1 < len(s) {
				i++
				word.WriteByte(s[i])
			}
			in = true
		case c == ' ' || c == '\t' || c == '\n':
			if in {
				out = append(out, word.String())
				word.Reset()
				in = false
			}
		default:
			word.WriteByte(c)
			in = true
		}
	}
	if in {
		out = append(out, word.String())
	}
	return out, true
}
