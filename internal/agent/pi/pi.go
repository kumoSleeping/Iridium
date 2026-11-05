// Package pi is the pi driver: it speaks `pi --mode rpc` (Rhodium §6.3) and turns it into the
// agent package's operations and events. Field names follow pi 1.4.0's docs/rpc*.md and
// Rhodium's recordings of pi 1.3.0. pi has no approval of its own: in mode "ask" Iridium's
// extension (approval.ts, Rhodium's src/pi-approval.ts) asks before every tool call but the
// read-only ones.
package pi

import (
	"bytes"
	"context"
	"crypto/rand"
	_ "embed"
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

//go:embed approval.ts
var approvalSource []byte

// ExtensionFile is the approval extension's name, written beside the session's system prompt.
const ExtensionFile = "iridium-approval.ts"

// MaxLine: longer output lines are skipped with a protocol error.
const MaxLine = 64 << 20

// Modes: auto runs every tool unasked, as pi does by itself; ask loads the approval extension.
var Modes = []string{"auto", "ask"}

const DefaultMode = "auto"

// Levels are pi's thinking levels (docs/cli.md --thinking).
var Levels = []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"}

var (
	ErrNotAttached = errors.New("pi: no agent process attached")
	// ErrRestart: the approval extension loads at launch or not at all; the mode switches when
	// the agent next starts (stop it while idle, and the next message starts it so).
	ErrRestart  = errors.New("pi: the approval mode switches only with a restart")
	errReplaced = errors.New("pi: the agent process ended")
)

// dialogs are extension UI methods that block until answered; others only inform.
var dialogs = map[string]bool{"select": true, "confirm": true, "input": true, "editor": true}

type Options struct {
	Emit     func(agent.Event)
	IDPrefix string // starts the driver's own request ids; a prompt's is its input's
	Logf     func(format string, a ...any)
}

type response struct {
	ID      *string         `json:"id"`
	Command string          `json:"command"`
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   string          `json:"error"`
}

// Driver is one session's pi driver. See agent.Driver.
type Driver struct {
	emitFn func(agent.Event)
	prefix string
	logf   func(string, ...any)

	mu       sync.Mutex
	w        agent.Writer
	proc     int
	seq      int
	pending  map[string]chan response
	spawned  bool   // the process attaching next was just spawned: say Hello
	mode     string // the mode it was spawned in
	effort   string // the level set on this process: a model switch resets pi's
	model    string // provider/id
	session  string
	state    agent.State
	starting bool          // a prompt went out while idle and its run has not started yet
	prompted chan struct{} // closed once the last prompt (or compact) was answered
	// startedBy: the message pi said it starts a run for (disposition started), until it starts.
	startedBy string
	// waiting: messages written and not yet in the conversation, oldest first.
	waiting []string
	asks    []api.Ask
	tools   map[string]string // started, not ended: their names
	reply   string
	run     agent.Tokens
	cost    float64
	priced  bool
	context int
	compact bool // a compaction began while idle: it is the turn
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
	d := &Driver{emitFn: o.Emit, prefix: o.IDPrefix, logf: o.Logf, unknown: map[string]int{}, mode: DefaultMode}
	d.reset()
	return d
}

func (d *Driver) reset() {
	d.pending = map[string]chan response{}
	d.prompted = closed()
	d.tools = map[string]string{}
	d.state = agent.Idle
	d.starting, d.startedBy, d.waiting, d.asks = false, "", nil, nil
	d.reply, d.run, d.cost, d.priced, d.context, d.compact = "", agent.Tokens{}, 0, false, 0, false
	d.effort, d.model, d.session = "", "", ""
}

func closed() chan struct{} {
	c := make(chan struct{})
	close(c)
	return c
}

// SplitLevel splits "provider/id:level" where level is one of pi's; either may be empty.
func SplitLevel(s string) (model, level string) {
	if i := strings.LastIndexByte(s, ':'); i >= 0 && slices.Contains(Levels, s[i+1:]) {
		return s[:i], s[i+1:]
	}
	return s, ""
}

// Spawn gives the holder's spec. --session-id opens the session if this directory has it and
// starts it if not, so a new session and a resumed one launch alike. pi never sees Claude's keys
// (its bundled Anthropic SDK reads them) nor a parent session's variables. Model is pi's
// "provider/id", maybe with ":level".
func (d *Driver) Spawn(o agent.SpawnOptions) (hold.Spec, error) {
	env := agent.ForeignEnv(o.Env)
	program, err := agent.LookPath(o.Program, env)
	if err != nil {
		return hold.Spec{}, err
	}
	// pi is a `#!/usr/bin/env node` script: without node on that PATH it cannot start at all.
	if head, err := readHead(program); err == nil && bytes.HasPrefix(head, []byte("#!")) && bytes.Contains(bytes.SplitN(head, []byte("\n"), 2)[0], []byte("env node")) {
		if _, err := agent.LookPath("node", env); err != nil {
			return hold.Spec{}, fmt.Errorf("pi: node not found: %w", err)
		}
	}
	mode := o.Mode
	if mode == "" {
		mode = DefaultMode
	}
	if !slices.Contains(Modes, mode) {
		return hold.Spec{}, fmt.Errorf("pi: no approval mode %q (%s)", mode, strings.Join(Modes, ", "))
	}
	argv := []string{program, "--mode", "rpc", "--session-id", o.Session}
	if o.SystemPromptFile != "" {
		argv = append(argv, "--append-system-prompt", o.SystemPromptFile)
	}
	model, level := SplitLevel(o.Model)
	if model != "" {
		argv = append(argv, "--model", model)
	}
	if level != "" {
		argv = append(argv, "--thinking", level)
	}
	if mode == "ask" {
		if o.SystemPromptFile == "" {
			return hold.Spec{}, errors.New("pi: approval mode ask needs a directory for its extension")
		}
		ext, err := installExtension(filepath.Dir(o.SystemPromptFile))
		if err != nil {
			return hold.Spec{}, err
		}
		argv = append(argv, "-e", ext)
	}
	d.mu.Lock()
	d.spawned, d.mode = true, mode
	d.mu.Unlock()
	env = agent.SetEnv(env, "IRIDIUM_SESSION", o.Session)
	return hold.Spec{Argv: argv, Env: env, Cwd: o.Cwd}, nil
}

func readHead(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b := make([]byte, 200)
	n, _ := f.Read(b)
	return b[:n], nil
}

// installExtension writes the approval extension into dir, whole or not at all: a pi starting
// meanwhile may be loading it.
func installExtension(dir string) (string, error) {
	path := filepath.Join(dir, ExtensionFile)
	if b, err := os.ReadFile(path); err == nil && bytes.Equal(b, approvalSource) {
		return path, nil
	}
	tmp := fmt.Sprintf("%s.%d", path, os.Getpid())
	if err := os.WriteFile(tmp, approvalSource, 0o600); err != nil {
		return "", err
	}
	return path, os.Rename(tmp, path)
}

// Attach connects agent process proc and asks its state: the session, the model. One just
// spawned also says Hello with its models; one an earlier daemon started keeps the mode the
// session has on record.
func (d *Driver) Attach(w agent.Writer, proc int) {
	d.mu.Lock()
	if proc != d.proc {
		d.resetLocked()
	}
	d.w, d.proc = w, proc
	spawned := d.spawned
	d.spawned = false
	d.mu.Unlock()
	go d.hello(proc, spawned)
}

func (d *Driver) hello(proc int, spawned bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var st struct {
		Model *struct {
			ID       string `json:"id"`
			Provider string `json:"provider"`
		} `json:"model"`
		Thinking  string `json:"thinkingLevel"`
		SessionID string `json:"sessionId"`
	}
	r, err := d.rpc(ctx, "get_state", nil)
	if err == nil {
		_ = json.Unmarshal(r, &st)
	}
	d.mu.Lock()
	if d.proc != proc {
		d.mu.Unlock()
		return
	}
	// What a command set meanwhile is newer.
	if st.Model != nil && d.model == "" {
		d.model = st.Model.Provider + "/" + st.Model.ID
	}
	d.session = st.SessionID
	if d.effort == "" {
		d.effort = st.Thinking
	}
	if d.model != "" || d.session != "" {
		d.emitLocked(agent.Started{Session: d.session, Model: d.model})
	}
	evs := d.take()
	d.mu.Unlock()
	d.flush(evs)
	if !spawned {
		return
	}
	h := agent.Hello{Mode: d.modeNow()}
	if r, err := d.rpc(ctx, "get_available_models", nil); err == nil {
		var body struct {
			Models []struct {
				ID       string `json:"id"`
				Name     string `json:"name"`
				Provider string `json:"provider"`
			} `json:"models"`
		}
		_ = json.Unmarshal(r, &body)
		for _, m := range body.Models {
			h.Models = append(h.Models, agent.Model{Value: m.Provider + "/" + m.ID, Name: m.Name, Description: m.Provider})
		}
	}
	d.mu.Lock()
	same := d.proc == proc
	d.mu.Unlock()
	if same {
		d.emit(h)
	}
}

func (d *Driver) modeNow() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.mode
}

func (d *Driver) Detach() {
	d.mu.Lock()
	d.resetLocked()
	d.w = nil
	evs := d.take()
	d.mu.Unlock()
	d.flush(evs)
}

func (d *Driver) resetLocked() {
	for id, ch := range d.pending {
		ch <- response{Error: errReplaced.Error()}
		delete(d.pending, id)
	}
	d.reset()
}

func (d *Driver) State() agent.State {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.state
}

func (d *Driver) busy() bool { return d.starting || d.state != agent.Idle }

// waitPrompted waits until every prompt written so far is answered: pi counts a prompt as
// running only once it answers it, so a prompt or abort written before then misses its run
// (Rhodium §6.3).
func (d *Driver) waitPrompted(ctx context.Context) error {
	for {
		d.mu.Lock()
		p := d.prompted
		d.mu.Unlock()
		select {
		case <-p:
		case <-ctx.Done():
			return ctx.Err()
		}
		d.mu.Lock()
		same := p == d.prompted
		d.mu.Unlock()
		if same {
			return nil
		}
	}
}

// Send writes a prompt under the input's id. One while a turn runs (or is about to) steers it:
// pi refuses a plain prompt then. "/compact" compacts instead, only between turns: pi's compact
// aborts the run first.
func (d *Driver) Send(ctx context.Context, m agent.Message) error {
	if m.ID == "" || m.Text == "" {
		return errors.New("pi: a message needs an id and text")
	}
	if err := d.waitPrompted(ctx); err != nil {
		return err
	}
	d.mu.Lock()
	if d.w == nil {
		d.mu.Unlock()
		return ErrNotAttached
	}
	cmd := map[string]any{"id": m.ID, "type": "prompt", "message": m.Text}
	compact := strings.TrimSpace(m.Text) == "/compact"
	steer := d.busy()
	switch {
	case compact && steer:
		d.mu.Unlock()
		return errors.New("pi: a turn is running; stop it before /compact")
	case compact:
		cmd = map[string]any{"id": m.ID, "type": "compact"}
	case steer:
		cmd["streamingBehavior"] = "steer"
		d.waiting = append(d.waiting, m.ID)
	default:
		d.starting = true
		d.waiting = append(d.waiting, m.ID)
	}
	ch := make(chan response, 1)
	d.pending[m.ID] = ch
	prompted := make(chan struct{})
	d.prompted = prompted
	d.mu.Unlock()
	go func() {
		<-ch
		close(prompted)
	}()
	if err := d.write(cmd); err != nil {
		d.mu.Lock()
		delete(d.pending, m.ID)
		d.waiting = slices.DeleteFunc(d.waiting, func(id string) bool { return id == m.ID })
		if !steer {
			d.starting = false
		}
		d.mu.Unlock()
		ch <- response{}
		return err
	}
	if compact {
		d.emit(agent.Taken{InputID: m.ID})
	}
	return nil
}

// Interrupt aborts the run: pi answers once it has stopped and settled.
func (d *Driver) Interrupt(ctx context.Context) error {
	if err := d.waitPrompted(ctx); err != nil {
		return err
	}
	_, err := d.rpc(ctx, "abort", nil)
	return err
}

// Answer answers an approval: allow, or deny with the words as the blocked tool's result.
func (d *Driver) Answer(ctx context.Context, a api.Answer) error {
	d.mu.Lock()
	i := slices.IndexFunc(d.asks, func(x api.Ask) bool { return x.RequestID == a.RequestID })
	if i < 0 {
		d.mu.Unlock()
		return fmt.Errorf("pi: no open ask %s", a.RequestID)
	}
	ask := d.asks[i]
	if a.Kind != "" && a.Kind != ask.Kind {
		d.mu.Unlock()
		return fmt.Errorf("pi: ask %s is a %s, not a %s", a.RequestID, ask.Kind, a.Kind)
	}
	if d.w == nil {
		d.mu.Unlock()
		return ErrNotAttached
	}
	d.asks = slices.Delete(d.asks, i, i+1)
	proc := d.proc
	d.mu.Unlock()
	value := "deny"
	switch {
	case a.Allow:
		value = "allow"
	case a.Message != "":
		value = "deny: " + a.Message
	}
	if err := d.write(map[string]any{"type": "extension_ui_response", "id": a.RequestID, "value": value}); err != nil {
		d.mu.Lock()
		if d.proc == proc {
			d.asks = append(d.asks, ask)
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
	return nil
}

// SetModel switches to "provider/id", maybe with ":level". A model switch resets pi's level to
// your settings' default: the level set on this process goes back on. pi's rpc sets them for
// this process only, never in your settings.
func (d *Driver) SetModel(ctx context.Context, model string) error {
	name, level := SplitLevel(model)
	provider, id, ok := strings.Cut(name, "/")
	if !ok {
		return fmt.Errorf("pi: a model is provider/id, not %q", name)
	}
	if _, err := d.rpc(ctx, "set_model", map[string]any{"provider": provider, "modelId": id}); err != nil {
		return err
	}
	d.mu.Lock()
	if level == "" {
		level = d.effort
	}
	d.model = name
	d.emitLocked(agent.Started{Session: d.session, Model: name})
	evs := d.take()
	d.mu.Unlock()
	d.flush(evs)
	if level == "" {
		return nil
	}
	// Checked first: pi takes a level it does not know as off, without a word.
	r, err := d.rpc(ctx, "get_available_thinking_levels", nil)
	if err != nil {
		return err
	}
	var body struct {
		Levels []string `json:"levels"`
	}
	_ = json.Unmarshal(r, &body)
	if !slices.Contains(body.Levels, level) {
		return fmt.Errorf("pi: %s takes no thinking level %q (%s)", name, level, strings.Join(body.Levels, ", "))
	}
	if _, err := d.rpc(ctx, "set_thinking_level", map[string]any{"level": level}); err != nil {
		return err
	}
	d.mu.Lock()
	d.effort = level
	d.mu.Unlock()
	return nil
}

// SetMode: the extension loads at launch only; see ErrRestart.
func (d *Driver) SetMode(ctx context.Context, mode string) error {
	if !slices.Contains(Modes, mode) {
		return fmt.Errorf("pi: no approval mode %q (%s)", mode, strings.Join(Modes, ", "))
	}
	if mode == d.modeNow() {
		return nil
	}
	return ErrRestart
}

type piTokens struct {
	Input      int `json:"input"`
	Output     int `json:"output"`
	CacheRead  int `json:"cacheRead"`
	CacheWrite int `json:"cacheWrite"`
}

type stats struct {
	Tokens       piTokens `json:"tokens"`
	Cost         float64  `json:"cost"`
	ContextUsage *struct {
		Tokens *int `json:"tokens"`
		Window *int `json:"contextWindow"`
	} `json:"contextUsage"`
}

func (d *Driver) stats(ctx context.Context) (*stats, error) {
	r, err := d.rpc(ctx, "get_session_stats", nil)
	if err != nil {
		return nil, err
	}
	var s stats
	if err := json.Unmarshal(r, &s); err != nil {
		return nil, fmt.Errorf("pi: get_session_stats: %w", err)
	}
	return &s, nil
}

// Context is get_session_stats' context usage: a total, no breakdown. Right after a compaction
// pi knows none until its next answer.
func (d *Driver) Context(ctx context.Context) (*api.Context, error) {
	s, err := d.stats(ctx)
	if err != nil {
		return nil, err
	}
	if s.ContextUsage == nil || s.ContextUsage.Tokens == nil {
		return nil, errors.New("pi: no context usage until the next answer")
	}
	c := &api.Context{Used: *s.ContextUsage.Tokens, At: time.Now().UnixMilli()}
	if s.ContextUsage.Window != nil {
		c.Max = *s.ContextUsage.Window
	}
	return c, nil
}

// Quota is the session's use so far: pi adds up its whole session file, across restarts, its
// cost at its catalogue's prices. pi has no quota windows.
func (d *Driver) Quota(ctx context.Context) (*agent.Usage, error) {
	s, err := d.stats(ctx)
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	model := d.model
	d.mu.Unlock()
	t := s.Tokens
	u := &agent.Usage{CostUSD: s.Cost, Models: map[string]agent.ModelUsage{}}
	if t != (piTokens{}) {
		u.Models[model] = agent.ModelUsage{Tokens: agent.Tokens{Input: t.Input, Output: t.Output, CacheRead: t.CacheRead, CacheWrite: t.CacheWrite}, CostUSD: s.Cost}
	}
	return u, nil
}

// ---- requests ----

func (d *Driver) nextID() string {
	d.seq++
	return fmt.Sprintf("%s%d", d.prefix, d.seq)
}

// rpc sends one command and waits for its data; pi's error is an error.
func (d *Driver) rpc(ctx context.Context, typ string, fields map[string]any) (json.RawMessage, error) {
	d.mu.Lock()
	if d.w == nil {
		d.mu.Unlock()
		return nil, ErrNotAttached
	}
	id := d.nextID()
	ch := make(chan response, 1)
	d.pending[id] = ch
	d.mu.Unlock()
	cmd := map[string]any{"id": id, "type": typ}
	for k, v := range fields {
		cmd[k] = v
	}
	drop := func() {
		d.mu.Lock()
		delete(d.pending, id)
		d.mu.Unlock()
	}
	if err := d.write(cmd); err != nil {
		drop()
		return nil, err
	}
	select {
	case r := <-ch:
		if !r.Success {
			return nil, errors.New(cmpOr(r.Error, "pi: "+typ+" failed"))
		}
		return r.Data, nil
	case <-ctx.Done():
		drop()
		return nil, ctx.Err()
	}
}

func (d *Driver) write(cmd any) error {
	d.mu.Lock()
	w := d.w
	d.mu.Unlock()
	if w == nil {
		return ErrNotAttached
	}
	line, err := json.Marshal(cmd)
	if err != nil {
		return err
	}
	return w(line)
}

// ---- output ----

type record struct {
	Type    string          `json:"type"`
	ID      *string         `json:"id"`
	Command string          `json:"command"`
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   json.RawMessage `json:"error"`
	// extension_ui_request
	Method string `json:"method"`
	Title  string `json:"title"`
	// message_*
	Message *struct {
		Role     string          `json:"role"`
		Content  json.RawMessage `json:"content"`
		Provider string          `json:"provider"`
		Model    string          `json:"model"`
		Usage    *struct {
			Input      int `json:"input"`
			Output     int `json:"output"`
			CacheRead  int `json:"cacheRead"`
			CacheWrite int `json:"cacheWrite"`
			Cost       *struct {
				Total float64 `json:"total"`
			} `json:"cost"`
		} `json:"usage"`
	} `json:"message"`
	Event *struct {
		Type  string `json:"type"`
		Delta string `json:"delta"`
	} `json:"assistantMessageEvent"`
	// tool_execution_*
	ToolCallID string `json:"toolCallId"`
	ToolName   string `json:"toolName"`
	Args       any    `json:"args"`
	// compaction_end
	Result *struct {
		Before int `json:"tokensBefore"`
		After  int `json:"estimatedTokensAfter"`
	} `json:"result"`
	Aborted      bool   `json:"aborted"`
	ErrorMessage string `json:"errorMessage"`
	// extension_error
	ExtensionPath string `json:"extensionPath"`
}

// ignored: kinds of output the driver knows and has no use for.
var ignored = map[string]bool{
	"agent_end":  true,                   // a run can go on after it (retries, steering); only agent_settled ends the turn
	"turn_start": true, "turn_end": true, // pi's turn is one model call; ours is up to agent_settled
	"tool_execution_update": true, "queue_update": true, "thinking_level_changed": true,
	"session_info_changed": true, "entry_appended": true, "auto_retry_start": true, "auto_retry_end": true,
	"summarization_retry_scheduled": true, "summarization_retry_attempt_start": true, "summarization_retry_finished": true,
	"model_changed": true,
}

// Feed handles one line of output. replay: an earlier daemon saw it; never answered.
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
	// pi keeps stdout for protocol records: anything else is a broken line.
	var m record
	if len(line) == 0 || line[0] != '{' || json.Unmarshal(line, &m) != nil {
		d.emitLocked(agent.ProtocolError{Reason: "bad_line", Detail: string(line[:min(len(line), 200)])})
		return nil
	}
	switch m.Type {
	case "response":
		d.response(&m)
	case "extension_ui_request":
		return d.dialog(&m, replay)
	case "agent_start":
		// Retries and steering start more runs before the turn settles; only the first starts it.
		d.starting = false
		input := d.startedBy
		d.startedBy = ""
		if d.state != agent.Idle {
			return nil
		}
		d.run, d.cost, d.priced = agent.Tokens{}, 0, false
		d.move(agent.Running, input)
	case "message_start":
		// Your message joins the conversation, in the order written. One fed again joined before.
		if m.Message != nil && m.Message.Role == "user" && !replay && len(d.waiting) > 0 {
			d.take1(d.waiting[0])
		}
	case "message_update":
		if m.Event != nil && m.Event.Type == "text_delta" {
			d.emitLocked(agent.TextDelta{Text: m.Event.Delta})
		}
	case "message_end":
		d.message(&m)
	case "tool_execution_start":
		// Out before the approval is asked: no sign the tool runs.
		d.tools[m.ToolCallID] = m.ToolName
		d.emitLocked(agent.ToolStart{ID: m.ToolCallID, Name: m.ToolName, Input: m.Args})
	case "tool_execution_end":
		delete(d.tools, m.ToolCallID)
		d.emitLocked(agent.ToolDone{ID: m.ToolCallID, Name: m.ToolName})
	case "agent_settled":
		d.settled()
	case "compaction_start":
		if d.state == agent.Idle {
			d.compact = true
		}
		d.move(agent.Compacting, "")
	case "compaction_end":
		c := agent.Compacted{Failed: m.Aborted || m.ErrorMessage != "" || m.Result == nil}
		if m.Result != nil {
			c.Before, c.After = m.Result.Before, m.Result.After
			d.context = m.Result.After
		}
		d.emitLocked(c)
		if d.compact {
			d.compact = false
			d.end(true)
		} else if d.state == agent.Compacting {
			d.move(agent.Running, "")
		}
	case "extension_error":
		// The approval extension threw: the tool call may have gone ahead unasked.
		d.emitLocked(agent.ProtocolError{Reason: "extension_error", Detail: "pi extension " + m.ExtensionPath + ": " + string(m.Error)})
	default:
		if !ignored[m.Type] {
			d.note(m.Type)
		}
	}
	return nil
}

// response: a command's answer. A prompt refused, or one an extension command handled, never
// joins the conversation; no run follows it.
func (d *Driver) response(m *record) {
	if m.ID == nil {
		d.emitLocked(agent.ProtocolError{Reason: "unexpected", Detail: "pi: " + m.Command + ": " + string(m.Error)})
		return
	}
	id := *m.ID
	var data struct {
		Disposition string `json:"disposition"`
	}
	_ = json.Unmarshal(m.Data, &data)
	if m.Command == "prompt" {
		switch {
		case !m.Success || data.Disposition == "handled":
			if !m.Success {
				d.emitLocked(agent.ProtocolError{Reason: "unexpected", Detail: "pi refused a message: " + errText(m.Error)})
			}
			d.take1(id)
			if d.starting && d.state == agent.Idle {
				d.starting = false
			}
		case data.Disposition == "started":
			d.startedBy = id
		}
	}
	if ch, ok := d.pending[id]; ok {
		delete(d.pending, id)
		ch <- response{ID: m.ID, Command: m.Command, Success: m.Success, Data: m.Data, Error: errText(m.Error)}
	}
}

func errText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return string(raw)
}

// take1: the message is out of pi's queue, in the conversation or turned down.
func (d *Driver) take1(id string) {
	i := slices.Index(d.waiting, id)
	if i < 0 {
		return
	}
	d.waiting = slices.Delete(d.waiting, i, i+1)
	d.emitLocked(agent.Taken{InputID: id})
}

// dialog: an extension asks. Only the approval extension's is put to you; any other dialog is
// cancelled, or pi would wait forever. One fed again was cancelled already.
func (d *Driver) dialog(m *record, replay bool) []byte {
	if !dialogs[m.Method] || m.ID == nil {
		return nil
	}
	var title struct {
		Iridium    string `json:"iridium"`
		ToolCallID string `json:"toolCallId"`
		ToolName   string `json:"toolName"`
		Input      any    `json:"input"`
	}
	if m.Method == "select" && json.Unmarshal([]byte(m.Title), &title) == nil && title.Iridium == "approval" {
		ask := api.Ask{RequestID: *m.ID, Kind: "permission", Tool: title.ToolName, Input: title.Input, ToolUseID: title.ToolCallID}
		d.asks = append(d.asks, ask)
		d.emitLocked(agent.AskOpen{Ask: ask})
		if d.state == agent.Running {
			d.move(agent.Waiting, "")
		}
		return nil
	}
	d.note("extension_ui_request/" + m.Method)
	if replay {
		return nil
	}
	b, _ := json.Marshal(map[string]any{"type": "extension_ui_response", "id": *m.ID, "cancelled": true})
	return b
}

func (d *Driver) message(m *record) {
	msg := m.Message
	if msg == nil || msg.Role != "assistant" {
		return // the system prompt, your messages, tool results
	}
	if msg.Provider != "" && msg.Model != "" {
		if model := msg.Provider + "/" + msg.Model; model != d.model {
			d.model = model
			d.emitLocked(agent.Started{Session: d.session, Model: model})
		}
	}
	if u := msg.Usage; u != nil {
		d.run = agent.Tokens{Input: d.run.Input + u.Input, Output: d.run.Output + u.Output, CacheRead: d.run.CacheRead + u.CacheRead, CacheWrite: d.run.CacheWrite + u.CacheWrite}
		d.context = u.Input + u.CacheRead + u.CacheWrite
		if u.Cost != nil {
			d.cost += u.Cost.Total
			d.priced = true
		}
	}
	var items []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	_ = json.Unmarshal(msg.Content, &items)
	var text strings.Builder
	for _, b := range items {
		if b.Type == "text" {
			text.WriteString(b.Text)
		}
	}
	if text.Len() > 0 {
		d.reply = text.String()
		d.emitLocked(agent.MessageDone{Text: d.reply})
	}
}

// settled: the turn ends only here. pi withdraws nothing when a run is aborted: what it still
// asks is void.
func (d *Driver) settled() {
	for _, a := range d.asks {
		d.emitLocked(agent.AskCancel{RequestID: a.RequestID})
	}
	d.asks = nil
	for id, name := range d.tools {
		d.emitLocked(agent.ToolDone{ID: id, Name: name})
	}
	d.tools = map[string]string{}
	d.starting = false
	if d.run != (agent.Tokens{}) {
		u := agent.TurnUsage{Tokens: d.run, Context: d.context}
		if d.priced {
			c := d.cost
			u.CostUSD = &c
		}
		d.emitLocked(u)
	}
	d.run, d.cost, d.priced = agent.Tokens{}, 0, false
	d.end(false)
}

func (d *Driver) end(compactOnly bool) {
	if d.state == agent.Idle {
		return
	}
	d.state = agent.Idle
	d.emitLocked(agent.StateChange{State: agent.Idle})
	d.emitLocked(agent.TurnEnd{Reply: d.reply, CompactOnly: compactOnly})
	d.reply = ""
}

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
}

func (d *Driver) note(kind string) {
	d.unknown[kind]++
	if d.unknown[kind] == 1 {
		d.logf("pi: skipping unknown %s", kind)
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

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
