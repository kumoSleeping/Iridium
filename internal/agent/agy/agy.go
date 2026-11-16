// Package agy is the Antigravity CLI driver: one `agy acp` per session (the Agent Client Protocol
// over stdio, newline-delimited JSON-RPC 2.0; agy 1.5.9), held by the holder, turned into the
// agent package's operations and events.
//
// agy makes the session's id (session/new) and the driver reports it with Started; a later start
// resumes it with session/resume, which gives no history again, and sets the model, its thinking
// level and the mode again: agy goes back to its own default for them. Iridium's system prompt goes
// in at session/new only (agy keeps the one a session began with).
//
// agy keeps its conversations in a store Iridium cannot read (SQLite of protobufs), so the driver
// hands each piece of the conversation over as a Record once it is whole, for the record Iridium
// keeps itself (transcript.Own). A session taken up from agy's store, of which Iridium has no
// record yet, is opened with session/load, whose history, replayed, is recorded the same way.
//
// A message sent while a turn runs joins it: agy answers the running prompt at once (end_turn, no
// usage) and runs the new one after; the turn ends with the answer to the last prompt.
//
// Approval modes: agy's own default, accept-edits and plan, and auto, which is agy's default with
// every permission it asks answered allow by the driver itself (agy's --dangerously-skip-permissions
// is a process's, so a switch to it would need a restart).
package agy

import (
	"bytes"
	"cmp"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/kumoSleeping/iridium/internal/agent"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/hold"
	"github.com/kumoSleeping/iridium/internal/transcript"
)

// MaxLine: longer output lines are skipped with a protocol error.
const MaxLine = 64 << 20

// Version is what Iridium tells agy it is (clientInfo).
const Version = "0.1.0"

// Modes: auto (agy's default, every ask allowed by the driver), then agy's own.
var Modes = []string{"auto", "default", "accept-edits", "plan"}

// DefaultMode is the mode an empty one means: auto, asking nothing, as for every agent.
const DefaultMode = "auto"

// Efforts are the thinking levels agy has (agy --help: --effort); each model takes some.
var Efforts = []string{"low", "medium", "high", "xhigh", "max"}

// agyMode is agy's mode for one of Iridium's.
func agyMode(mode string) string {
	if mode == "auto" {
		return "default"
	}
	return mode
}

var (
	ErrNotAttached = errors.New("agy: no agent process attached")
	errReplaced    = errors.New("agy: the agent process ended")
	// ErrSignedOut: agy has no login (session/new answers "Authentication required").
	ErrSignedOut = errors.New("agy is not signed in: run agy in a terminal once and sign in with Google")
)

type Options struct {
	Emit func(agent.Event)
	// IDPrefix starts every request id the driver makes; default "ir-" and a random part. A
	// prompt's id is its input's, so its answer names the input after a restart.
	IDPrefix string
	Logf     func(format string, a ...any)
	// Home is agy's directory (~/.gemini/antigravity-cli): its login, read for the account and
	// the quota. Empty: the user's.
	Home string
}

// open is what a process just spawned opens.
type open struct {
	cwd, session, prompt string
	history              bool // session/load: the history comes again, to be recorded
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("agy: %s (%d)", e.Message, e.Code) }

type reply struct {
	result json.RawMessage
	err    error
}

// tool is a call begun and not yet done.
type tool struct {
	name  string
	input any
}

type pendingAsk struct {
	id    json.RawMessage // agy's request id, as sent
	allow string          // its options' ids
	deny  string
	ask   api.Ask
}

// Driver is one session's agy driver. See agent.Driver.
type Driver struct {
	emitFn func(agent.Event)
	prefix string
	logf   func(string, ...any)
	home   string

	mu       sync.Mutex
	w        agent.Writer
	proc     int
	seq      int
	pending  map[string]chan reply
	want     *open
	ready    chan struct{} // closed once the session is open, or failed to (readyErr)
	readyErr error
	version  string
	session  string
	model    string // agy's model (a configOptions value), as it last said
	effort   string // its thinking level
	pick     struct{ model, effort string }
	mode     string
	remote   bool // agy runs at an executor: its login is there, not read here
	models   []agent.Model
	commands []agent.Command
	plan     string
	state    agent.State
	starting bool // a prompt went out while idle; its message has not come back yet
	loading  bool // session/load replays the history: recorded, nothing else
	// waiting: inputs sent and not yet in the conversation, oldest first; outstanding: prompts
	// sent by this daemon not yet answered.
	waiting     []string
	outstanding []string
	asks        []pendingAsk
	tools       map[string]tool
	text        strings.Builder // the message being written
	thought     strings.Builder // the thought being written
	reply       string
	title       string
	context     int
	window      int
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
	if o.Home == "" {
		if h, err := os.UserHomeDir(); err == nil {
			o.Home = filepath.Join(h, ".gemini", "antigravity-cli")
		}
	}
	d := &Driver{emitFn: o.Emit, prefix: o.IDPrefix, logf: o.Logf, home: o.Home, unknown: map[string]int{}, mode: DefaultMode}
	d.reset()
	return d
}

func (d *Driver) reset() {
	d.pending = map[string]chan reply{}
	d.ready = make(chan struct{})
	d.tools = map[string]tool{}
	d.state = agent.Idle
	d.starting, d.loading = false, false
	d.waiting, d.outstanding, d.asks = nil, nil, nil
	d.text.Reset()
	d.thought.Reset()
	d.reply = ""
}

// resetLocked forgets the process: its requests fail, its asks are gone, the turn is over.
func (d *Driver) resetLocked() {
	for id, ch := range d.pending {
		ch <- reply{err: errReplaced}
		delete(d.pending, id)
	}
	select {
	case <-d.ready:
	default:
		d.readyErr = errReplaced
		close(d.ready)
	}
	d.readyErr = nil
	d.session, d.model, d.effort, d.version = "", "", "", ""
	d.context, d.window, d.title = 0, 0, ""
	d.reset()
}

// SplitEffort splits "model:effort"; either may be empty.
func SplitEffort(s string) (model, effort string) {
	if i := strings.LastIndexByte(s, ':'); i >= 0 && slices.Contains(Efforts, s[i+1:]) {
		return s[:i], s[i+1:]
	}
	return s, ""
}

// Spawn gives the holder's spec: `agy acp`, without Claude's keys or a parent session's variables,
// its self-update off (a protocol that moves under a running session), IRIDIUM_SESSION set.
func (d *Driver) Spawn(o agent.SpawnOptions) (hold.Spec, error) {
	env := agent.ForeignEnv(o.Env)
	argv, err := o.Command("agy", env)
	if err != nil {
		return hold.Spec{}, err
	}
	mode := cmp.Or(o.Mode, DefaultMode)
	if !slices.Contains(Modes, mode) {
		return hold.Spec{}, fmt.Errorf("agy: no approval mode %q (%s)", mode, strings.Join(Modes, ", "))
	}
	// The prompt is read here and goes in the protocol, wherever agy runs.
	prompt := ""
	if o.SystemPromptFile != "" {
		b, err := os.ReadFile(o.SystemPromptFile)
		if err != nil {
			return hold.Spec{}, err
		}
		prompt = string(b)
	}
	w := &open{cwd: o.Cwd, prompt: prompt}
	if o.Resume {
		w.session, w.history = o.AgentSession, o.History
	}
	model, effort := SplitEffort(o.Model)
	d.mu.Lock()
	d.want, d.mode, d.remote = w, mode, o.Remote != nil
	d.pick.model, d.pick.effort = model, cmp.Or(effort, o.Effort)
	d.mu.Unlock()
	env = agent.SetEnv(env, "AGY_CLI_DISABLE_AUTO_UPDATE", "1")
	env = agent.SetEnv(env, "IRIDIUM_SESSION", o.Session)
	return hold.Spec{Argv: append(argv, "acp"), Env: env, Cwd: o.Cwd}, nil
}

// Attach connects agent process proc. One just spawned gets the handshake and its session; one an
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

// opened is what session/new, /resume and /load answer.
type opened struct {
	SessionID string `json:"sessionId"`
	Models    *struct {
		Available []struct {
			ID   string `json:"modelId"`
			Name string `json:"name"`
		} `json:"availableModels"`
		Current string `json:"currentModelId"`
	} `json:"models"`
	Modes *struct {
		Current string `json:"currentModeId"`
	} `json:"modes"`
	Config []configOption `json:"configOptions"`
	Meta   *struct {
		Antigravity *struct {
			Tier *struct {
				Name string `json:"name"`
			} `json:"tier"`
		} `json:"antigravity"`
	} `json:"_meta"`
}

type configOption struct {
	ID      string `json:"id"`
	Current string `json:"currentValue"`
	Options []struct {
		Value string `json:"value"`
		Name  string `json:"name"`
	} `json:"options"`
}

func (o *opened) option(id string) *configOption {
	for i := range o.Config {
		if o.Config[i].ID == id {
			return &o.Config[i]
		}
	}
	return nil
}

// catalog is the models agy offers, each with the levels it takes: its availableModels are each a
// model at a level ("gemini-4-argon-high"), its model option the models.
func (o *opened) catalog() []agent.Model {
	mo := o.option("model")
	if mo == nil || o.Models == nil {
		return nil
	}
	def := ""
	var out []agent.Model
	for _, opt := range mo.Options {
		m := agent.Model{Value: opt.Value, Name: opt.Name}
		for _, a := range o.Models.Available {
			if lvl, ok := strings.CutPrefix(a.ID, opt.Value+"-"); ok && slices.Contains(Efforts, lvl) {
				m.Efforts = append(m.Efforts, lvl)
				if a.ID == o.Models.Current {
					def = opt.Value
				}
			} else if a.ID == opt.Value && a.ID == o.Models.Current {
				def = opt.Value
			}
		}
		slices.SortFunc(m.Efforts, func(a, b string) int { return slices.Index(Efforts, a) - slices.Index(Efforts, b) })
		m.NoEffort = len(m.Efforts) == 0
		out = append(out, m)
	}
	for i := range out {
		out[i].Default = out[i].Value == def
	}
	return out
}

// open does the handshake, opens the session and sets it as the session wants it.
func (d *Driver) open(proc int, w *open, ready chan struct{}) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var o opened
	err := func() error {
		r, err := d.call(ctx, "initialize", map[string]any{
			"protocolVersion":    1,
			"clientInfo":         map[string]string{"name": "iridium", "title": "Iridium", "version": Version},
			"clientCapabilities": map[string]any{"fs": map[string]bool{"readTextFile": false, "writeTextFile": false}, "terminal": false},
		})
		if err != nil {
			return err
		}
		var hello struct {
			Info struct {
				Version string `json:"version"`
			} `json:"agentInfo"`
		}
		_ = json.Unmarshal(r, &hello)
		d.mu.Lock()
		d.version = hello.Info.Version
		d.mu.Unlock()
		params := map[string]any{"cwd": w.cwd, "mcpServers": []any{}}
		switch {
		case w.session != "" && w.history:
			d.mu.Lock()
			d.loading = true
			d.mu.Unlock()
			params["sessionId"] = w.session
			r, err = d.call(ctx, "session/load", params)
			d.mu.Lock()
			d.sealLocked()
			d.loading = false
			d.mu.Unlock()
		case w.session != "":
			params["sessionId"] = w.session
			r, err = d.call(ctx, "session/resume", params)
		default:
			if w.prompt != "" {
				params["_meta"] = map[string]any{"antigravity": map[string]any{"systemPrompt": map[string]any{"append": w.prompt}}}
			}
			r, err = d.call(ctx, "session/new", params)
		}
		if err != nil {
			return err
		}
		if err := json.Unmarshal(r, &o); err != nil {
			return fmt.Errorf("agy: opening the session: %w", err)
		}
		o.SessionID = cmp.Or(o.SessionID, w.session)
		d.mu.Lock()
		d.learnLocked(&o)
		model, effort, mode := d.pick.model, d.pick.effort, d.mode
		d.mu.Unlock()
		// agy goes back to its own default model, level and mode at every start.
		if model != "" && model != d.modelNow() {
			if err := d.setOption(ctx, "model", model); err != nil {
				d.logf("agy: model %s: %v", model, err)
			}
		}
		if effort != "" {
			if err := d.SetEffort(ctx, effort); err != nil {
				d.logf("agy: level %s: %v", effort, err)
			}
		}
		return d.setOption(ctx, "mode", agyMode(mode))
	}()
	// The commands come right after the session opens.
	time.Sleep(300 * time.Millisecond)
	d.mu.Lock()
	if d.proc != proc || d.ready != ready {
		d.mu.Unlock()
		return
	}
	d.readyErr = err
	if d.session != "" {
		d.emitLocked(agent.Started{Session: d.session, Model: d.model, Effort: d.effort})
	}
	d.emitLocked(agent.Hello{Version: d.version, Mode: d.mode, Models: d.models, Commands: d.commands, Account: d.account(), Plan: d.plan})
	if err != nil {
		why := "agy did not open the session: " + err.Error()
		var re *rpcError
		if errors.As(err, &re) && re.Code == -32000 && strings.Contains(strings.ToLower(re.Message), "auth") {
			why = ErrSignedOut.Error()
		}
		d.emitLocked(agent.GiveUp{Why: why})
	}
	close(ready)
	evs := d.take()
	d.mu.Unlock()
	d.flush(evs)
}

// learnLocked takes in what opening the session said.
func (d *Driver) learnLocked(o *opened) {
	if o.SessionID != "" {
		d.session = o.SessionID
	}
	if c := o.catalog(); c != nil {
		d.models = c
	}
	if mo := o.option("model"); mo != nil {
		d.model = mo.Current
	}
	if lo := o.option("thought_level"); lo != nil {
		d.effort = lo.Current
	}
	if o.Meta != nil && o.Meta.Antigravity != nil && o.Meta.Antigravity.Tier != nil {
		d.plan = o.Meta.Antigravity.Tier.Name
	}
}

func (d *Driver) modelNow() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.model
}

// account is the e-mail agy is signed in with, from its login file; with d.mu held.
func (d *Driver) account() string {
	if d.remote {
		return ""
	}
	t, err := readLogin(d.home)
	if err != nil {
		return ""
	}
	return t.Email
}

func (d *Driver) Detach() {
	d.mu.Lock()
	d.resetLocked()
	d.w = nil
	evs := d.take()
	d.mu.Unlock()
	d.flush(evs)
}

func (d *Driver) State() agent.State {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.state
}

func (d *Driver) busy() bool { return d.starting || d.state != agent.Idle }

// waitReady waits for the session to open.
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
	if d.session == "" {
		return errors.New("agy: no session open")
	}
	return nil
}

// Send writes a prompt under the input's id, its pictures with it. One while a turn runs joins
// the turn: agy runs it after the one running.
func (d *Driver) Send(ctx context.Context, m agent.Message) error {
	text := m.Text.String()
	if m.ID == "" || (text == "" && len(m.Images) == 0) {
		return errors.New("agy: a message needs an id and text")
	}
	if err := d.waitReady(ctx); err != nil {
		return err
	}
	blocks := []map[string]any{}
	if text != "" {
		blocks = append(blocks, map[string]any{"type": "text", "text": text})
	}
	for _, im := range m.Images {
		blocks = append(blocks, map[string]any{"type": "image", "data": base64.StdEncoding.EncodeToString(im.Data), "mimeType": im.MediaType})
	}
	d.mu.Lock()
	if d.w == nil {
		d.mu.Unlock()
		return ErrNotAttached
	}
	if !d.busy() {
		d.starting = true
	}
	d.waiting = append(d.waiting, m.ID)
	d.outstanding = append(d.outstanding, m.ID)
	ch := make(chan reply, 1)
	d.pending[m.ID] = ch
	session := d.session
	d.mu.Unlock()
	if err := d.write(request(m.ID, "session/prompt", map[string]any{"sessionId": session, "prompt": blocks})); err != nil {
		d.mu.Lock()
		delete(d.pending, m.ID)
		d.waiting = slices.DeleteFunc(d.waiting, func(id string) bool { return id == m.ID })
		d.outstanding = slices.DeleteFunc(d.outstanding, func(id string) bool { return id == m.ID })
		if d.state == agent.Idle {
			d.starting = false
		}
		d.mu.Unlock()
		return err
	}
	go func() { <-ch }() // the answer ends the turn in feed
	return nil
}

// Interrupt cancels the turn: agy answers the prompt "cancelled". What it still asks is void.
func (d *Driver) Interrupt(ctx context.Context) error {
	if err := d.waitReady(ctx); err != nil {
		return err
	}
	d.mu.Lock()
	asks := d.asks
	d.asks = nil
	session := d.session
	for _, a := range asks {
		d.emitLocked(agent.AskCancel{RequestID: a.ask.RequestID})
	}
	evs := d.take()
	d.mu.Unlock()
	d.flush(evs)
	for _, a := range asks {
		_ = d.write(map[string]any{"jsonrpc": "2.0", "id": a.id, "result": map[string]any{"outcome": map[string]string{"outcome": "cancelled"}}})
	}
	return d.write(map[string]any{"jsonrpc": "2.0", "method": "session/cancel", "params": map[string]string{"sessionId": session}})
}

// Answer answers a permission ask: allow (always too: Iridium keeps the rule), or deny.
func (d *Driver) Answer(ctx context.Context, a api.Answer) error {
	d.mu.Lock()
	i := slices.IndexFunc(d.asks, func(p pendingAsk) bool { return p.ask.RequestID == a.RequestID })
	if i < 0 {
		d.mu.Unlock()
		return fmt.Errorf("agy: no open ask %s", a.RequestID)
	}
	p := d.asks[i]
	if a.Kind != "" && a.Kind != p.ask.Kind {
		d.mu.Unlock()
		return fmt.Errorf("agy: ask %s is a %s, not a %s", a.RequestID, p.ask.Kind, a.Kind)
	}
	if d.w == nil {
		d.mu.Unlock()
		return ErrNotAttached
	}
	d.asks = slices.Delete(d.asks, i, i+1)
	proc := d.proc
	d.mu.Unlock()
	option := p.deny
	if a.Allow || a.Always {
		option = p.allow
	}
	if err := d.write(map[string]any{"jsonrpc": "2.0", "id": p.id, "result": map[string]any{"outcome": map[string]string{"outcome": "selected", "optionId": option}}}); err != nil {
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
	return nil
}

// SetModel switches the model, maybe "model:level"; the level set goes back on where the new
// model takes it (agy's levels are each model's).
func (d *Driver) SetModel(ctx context.Context, model string) error {
	name, level := SplitEffort(model)
	if err := d.waitReady(ctx); err != nil {
		return err
	}
	if err := d.setOption(ctx, "model", name); err != nil {
		return err
	}
	d.mu.Lock()
	d.pick.model = name
	level = cmp.Or(level, d.pick.effort)
	d.emitLocked(agent.Started{Session: d.session, Model: d.model, Effort: d.effort})
	evs := d.take()
	d.mu.Unlock()
	d.flush(evs)
	if level == "" || !d.takes(name, level) {
		return nil
	}
	return d.SetEffort(ctx, level)
}

// takes: model takes level, as the catalog says (one it does not know: yes).
func (d *Driver) takes(model, level string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, m := range d.models {
		if m.Value == model {
			return slices.Contains(m.Efforts, level)
		}
	}
	return true
}

// SetEffort sets the thinking level, if the model takes it.
func (d *Driver) SetEffort(ctx context.Context, level string) error {
	d.mu.Lock()
	model := d.model
	d.mu.Unlock()
	if !d.takes(model, level) {
		return fmt.Errorf("agy: %s takes no thinking level %q", model, level)
	}
	if err := d.setOption(ctx, "thought_level", level); err != nil {
		return err
	}
	d.mu.Lock()
	d.pick.effort = level
	d.emitLocked(agent.Started{Session: d.session, Model: d.model, Effort: d.effort})
	evs := d.take()
	d.mu.Unlock()
	d.flush(evs)
	return nil
}

// SetMode switches at once: agy's mode, auto being its default.
func (d *Driver) SetMode(ctx context.Context, mode string) error {
	if !slices.Contains(Modes, mode) {
		return fmt.Errorf("agy: no approval mode %q (%s)", mode, strings.Join(Modes, ", "))
	}
	if err := d.waitReady(ctx); err != nil {
		return err
	}
	if err := d.setOption(ctx, "mode", agyMode(mode)); err != nil {
		return err
	}
	d.mu.Lock()
	d.mode = mode
	// Asks open when auto comes on are allowed now.
	var allow []pendingAsk
	if mode == "auto" {
		allow = slices.Clone(d.asks)
	}
	d.mu.Unlock()
	for _, p := range allow {
		_ = d.Answer(ctx, api.Answer{RequestID: p.ask.RequestID, Allow: true})
	}
	return nil
}

// setOption sets one of the session's config options; what agy answers is taken in.
func (d *Driver) setOption(ctx context.Context, id, value string) error {
	d.mu.Lock()
	session := d.session
	d.mu.Unlock()
	r, err := d.call(ctx, "session/set_config_option", map[string]any{"sessionId": session, "configId": id, "value": value})
	if err != nil {
		return err
	}
	var o opened
	if json.Unmarshal(r, &o) == nil {
		d.mu.Lock()
		d.configLocked(o.Config)
		d.mu.Unlock()
	}
	return nil
}

// configLocked takes in the config options agy says it has now.
func (d *Driver) configLocked(opts []configOption) {
	for _, c := range opts {
		switch c.ID {
		case "model":
			d.model = c.Current
		case "thought_level":
			d.effort = c.Current
		}
	}
}

// Context is what agy last said of the context: tokens used of its size; no breakdown.
func (d *Driver) Context(ctx context.Context) (*api.Context, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.window == 0 {
		return nil, errors.New("agy: no context usage until the next answer")
	}
	return &api.Context{Used: d.context, Max: d.window, At: time.Now().UnixMilli()}, nil
}

// Quota is the account's quota windows, read as agy reads them (quota.go), once the session is
// open; none where agy runs at
// an executor (its login is there) or its token has run out. Each turn's tokens come with
// TurnUsage; agy keeps no totals.
func (d *Driver) Quota(ctx context.Context) (*agent.Usage, error) {
	// After the handshake: the account the windows are kept under is known by then. A process
	// taken up knows its session once its output fed again has said it.
	for {
		err := d.waitReady(ctx)
		if err == nil {
			break
		}
		d.mu.Lock()
		opening := d.readyErr == nil && d.w != nil && d.session == ""
		d.mu.Unlock()
		if !opening {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	u := &agent.Usage{Models: map[string]agent.ModelUsage{}}
	d.mu.Lock()
	remote, version := d.remote, d.version
	d.mu.Unlock()
	if remote {
		return u, nil
	}
	ws, err := Windows(ctx, d.home, version)
	if err != nil {
		d.logf("agy: quota: %v", err)
		return u, nil
	}
	u.Windows = ws
	return u, nil
}

// ---- requests ----

func (d *Driver) nextID() string {
	d.seq++
	return fmt.Sprintf("%s%d", d.prefix, d.seq)
}

func request(id, method string, params any) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}
}

// call sends a request and waits for its answer; agy's error is an error.
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
	if err := d.write(request(id, method, params)); err != nil {
		drop()
		return nil, err
	}
	select {
	case r := <-ch:
		return r.result, r.err
	case <-ctx.Done():
		drop()
		return nil, ctx.Err()
	}
}

func (d *Driver) write(v any) error {
	d.mu.Lock()
	w := d.w
	d.mu.Unlock()
	if w == nil {
		return ErrNotAttached
	}
	line, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return w(line)
}

// ---- output ----

type frame struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

func idText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return string(raw)
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
	line = bytes.TrimSpace(line)
	var f frame
	if len(line) == 0 || line[0] != '{' || json.Unmarshal(line, &f) != nil {
		d.emitLocked(agent.ProtocolError{Reason: "bad_line", Detail: string(line[:min(len(line), 200)])})
		return nil
	}
	switch {
	case f.Method != "" && len(f.ID) > 0:
		return d.serverRequest(&f, replay)
	case f.Method != "":
		if f.Method == "session/update" {
			d.update(f.Params, replay)
		} else {
			d.note(f.Method)
		}
	case len(f.ID) > 0:
		d.response(&f, replay)
	}
	return nil
}

// stopped is a prompt's answer.
type stopped struct {
	StopReason string `json:"stopReason"`
	Usage      *struct {
		Input      int `json:"inputTokens"`
		Output     int `json:"outputTokens"`
		CacheRead  int `json:"cachedReadTokens"`
		CacheWrite int `json:"cachedWriteTokens"`
	} `json:"usage"`
}

// response: an answer to a request. A prompt's ends the turn when it answers the last prompt sent;
// one of an earlier daemon's (a process taken up) when it carries usage or is not end_turn (agy
// answers a prompt another joined at once, end_turn without usage).
func (d *Driver) response(f *frame, replay bool) {
	id := idText(f.ID)
	if ch, ok := d.pending[id]; ok {
		delete(d.pending, id)
		var err error
		if f.Error != nil {
			err = f.Error
		}
		ch <- reply{result: f.Result, err: err}
	}
	if f.Error != nil {
		if i := slices.Index(d.outstanding, id); i >= 0 {
			// A prompt refused: it never joins the conversation.
			d.outstanding = slices.Delete(d.outstanding, i, i+1)
			d.take1(id)
			d.emitLocked(agent.ProtocolError{Reason: "unexpected", Detail: "agy refused a message: " + f.Error.Message})
			if len(d.outstanding) == 0 && d.starting && d.state == agent.Idle {
				d.starting = false
			}
		}
		return
	}
	var s stopped
	if json.Unmarshal(f.Result, &s) != nil || s.StopReason == "" {
		// Opening the session, as fed again: what it says comes back.
		if replay {
			var o opened
			if json.Unmarshal(f.Result, &o) == nil && o.SessionID != "" && o.Config != nil {
				d.learnLocked(&o)
				d.emitLocked(agent.Started{Session: d.session, Model: d.model, Effort: d.effort})
				d.emitLocked(agent.Hello{Version: d.version, Mode: d.mode, Models: d.models, Account: d.account(), Plan: d.plan})
			}
		}
		return
	}
	last := false
	if i := slices.Index(d.outstanding, id); i >= 0 {
		d.outstanding = slices.Delete(d.outstanding, i, i+1)
		last = len(d.outstanding) == 0
	} else {
		last = s.Usage != nil || s.StopReason != "end_turn"
	}
	if !last {
		return
	}
	d.sealLocked()
	if u := s.Usage; u != nil {
		d.emitLocked(agent.TurnUsage{Tokens: agent.Tokens{Input: u.Input, Output: u.Output, CacheRead: u.CacheRead, CacheWrite: u.CacheWrite}, Context: d.context})
	}
	if s.StopReason == "cancelled" {
		d.record(transcript.Entry{Kind: string(api.BlockKindSystem), Event: string(api.SystemEventInterrupted), Text: "Interrupted"})
	}
	d.settled()
}

// settled: the turn is over. What is still asked or running is void.
func (d *Driver) settled() {
	for _, a := range d.asks {
		d.emitLocked(agent.AskCancel{RequestID: a.ask.RequestID})
	}
	d.asks = nil
	for id, t := range d.tools {
		d.emitLocked(agent.ToolDone{ID: id, Name: t.name})
	}
	d.tools = map[string]tool{}
	d.starting = false
	if d.state == agent.Idle {
		return
	}
	d.state = agent.Idle
	d.emitLocked(agent.StateChange{State: agent.Idle})
	d.emitLocked(agent.TurnEnd{Reply: d.reply})
	d.reply = ""
}

// serverRequest: agy asks. A permission is put to you, or allowed at once in auto; one fed again
// was answered already, unless it is still open (a tool_call_update says when it is no longer).
// Anything else agy may ask (files, terminals) Iridium said it does not do.
func (d *Driver) serverRequest(f *frame, replay bool) []byte {
	if f.Method != "session/request_permission" {
		d.note("request/" + f.Method)
		if replay {
			return nil
		}
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": f.ID, "error": map[string]any{"code": -32601, "message": "Iridium does not do " + f.Method}})
		return b
	}
	var p struct {
		ToolCall update `json:"toolCall"`
		Options  []struct {
			ID   string `json:"optionId"`
			Kind string `json:"kind"`
		} `json:"options"`
	}
	_ = json.Unmarshal(f.Params, &p)
	pa := pendingAsk{id: f.ID}
	for _, o := range p.Options {
		switch {
		case strings.HasPrefix(o.Kind, "allow") && (pa.allow == "" || o.Kind == "allow_once"):
			pa.allow = o.ID
		case strings.HasPrefix(o.Kind, "reject") && (pa.deny == "" || o.Kind == "reject_once"):
			pa.deny = o.ID
		}
	}
	name, input := toolOf(&p.ToolCall)
	pa.ask = api.Ask{RequestID: idText(f.ID), Kind: "permission", Tool: name, Input: input, ToolUseID: p.ToolCall.ToolCallID, Description: p.ToolCall.Title}
	if d.mode == "auto" && !replay {
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": f.ID, "result": map[string]any{"outcome": map[string]string{"outcome": "selected", "optionId": cmp.Or(pa.allow, "allow")}}})
		return b
	}
	if d.mode == "auto" {
		return nil // answered by the daemon that saw it
	}
	d.asks = append(d.asks, pa)
	d.emitLocked(agent.AskOpen{Ask: pa.ask})
	if d.state == agent.Running {
		d.move(agent.Waiting, "")
	}
	return nil
}

// update is one session/update's.
type update struct {
	Kind    string          `json:"sessionUpdate"`
	Content json.RawMessage `json:"content"`
	// tool_call, tool_call_update; Title too session_info_update's
	ToolCallID string          `json:"toolCallId"`
	Name       string          `json:"name"`
	Title      string          `json:"title"`
	Status     string          `json:"status"`
	RawInput   json.RawMessage `json:"rawInput"`
	RawOutput  json.RawMessage `json:"rawOutput"`
	// usage_update
	Used int `json:"used"`
	Size int `json:"size"`
	// available_commands_update
	Commands []struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	} `json:"availableCommands"`
	// config_option_update, current_mode_update
	Config []configOption `json:"configOptions"`
	Mode   string         `json:"currentModeId"`
}

// piece is one content block: text, or a diff of a file.
type piece struct {
	Type    string          `json:"type"`
	Text    string          `json:"text"`
	Content json.RawMessage `json:"content"`
	Path    string          `json:"path"`
	OldText *string         `json:"oldText"`
	NewText string          `json:"newText"`
}

func (d *Driver) update(raw json.RawMessage, replay bool) {
	var p struct {
		Update update `json:"update"`
	}
	if json.Unmarshal(raw, &p) != nil {
		return
	}
	u := &p.Update
	live := !replay && !d.loading
	switch u.Kind {
	case "user_message_chunk":
		d.sealLocked()
		var c piece
		_ = json.Unmarshal(u.Content, &c)
		input := ""
		if !d.loading {
			if len(d.waiting) > 0 && !replay {
				input = d.waiting[0]
			}
			d.take1(input)
			if d.state == agent.Idle {
				d.reply = ""
				d.move(agent.Running, input)
			}
		}
		if c.Type == "text" {
			d.record(transcript.Entry{Kind: string(api.BlockKindUser), Text: c.Text, ID: input})
		}
	case "agent_message_chunk":
		if d.thought.Len() > 0 {
			d.sealLocked()
		}
		var c piece
		_ = json.Unmarshal(u.Content, &c)
		if c.Type != "text" || c.Text == "" {
			return
		}
		d.started()
		d.text.WriteString(c.Text)
		if live {
			d.emitLocked(agent.TextDelta{Text: c.Text})
		}
	case "agent_thought_chunk":
		if d.text.Len() > 0 {
			d.sealLocked()
		}
		var c piece
		_ = json.Unmarshal(u.Content, &c)
		if c.Type == "text" {
			d.started()
			d.thought.WriteString(c.Text)
		}
	case "tool_call":
		d.sealLocked()
		d.started()
		d.toolBegun(u, live)
	case "tool_call_update":
		if _, ok := d.tools[u.ToolCallID]; !ok && u.Name != "" && u.Status != "" {
			d.sealLocked()
			d.started()
			d.toolBegun(u, live) // only its update was said (a history replayed)
		}
		switch u.Status {
		case "completed", "failed":
			t, ok := d.tools[u.ToolCallID]
			if !ok {
				return
			}
			delete(d.tools, u.ToolCallID)
			d.withdrawFor(u.ToolCallID)
			d.record(transcript.Entry{Kind: string(api.BlockKindResult), ID: u.ToolCallID, Text: resultOf(u), Error: u.Status == "failed"})
			if !d.loading {
				d.emitLocked(agent.ToolDone{ID: u.ToolCallID, Name: t.name})
			}
		case "in_progress":
			d.withdrawFor(u.ToolCallID) // allowed: no longer asked
		}
	case "usage_update":
		d.context, d.window = u.Used, u.Size
	case "session_info_update":
		if u.Title != "" && u.Title != d.title {
			d.title = u.Title
			d.record(transcript.Entry{Kind: transcript.OwnTitle, Text: u.Title})
		}
	case "available_commands_update":
		d.commands = d.commands[:0:0]
		for _, c := range u.Commands {
			d.commands = append(d.commands, agent.Command{Name: c.Name, Description: c.Description})
		}
	case "config_option_update":
		model, effort := d.model, d.effort
		d.configLocked(u.Config)
		if d.model != model || d.effort != effort {
			d.emitLocked(agent.Started{Session: d.session, Model: d.model, Effort: d.effort})
		}
	case "current_mode_update":
		// agy's own: auto is its default, kept as Iridium's.
		if u.Mode != "" && u.Mode != agyMode(d.mode) {
			d.mode = u.Mode
		}
	default:
		d.note("update/" + u.Kind)
	}
}

// started: agy writes, so a turn runs (one an earlier daemon began, its prompt's message not seen).
func (d *Driver) started() {
	if !d.loading && d.state == agent.Idle {
		d.reply = ""
		d.move(agent.Running, "")
	}
}

func (d *Driver) toolBegun(u *update, live bool) {
	name, input := toolOf(u)
	d.tools[u.ToolCallID] = tool{name: name, input: input}
	in, _ := json.Marshal(input)
	d.record(transcript.Entry{Kind: string(api.BlockKindTool), ID: u.ToolCallID, Name: name, Input: in})
	if live {
		d.emitLocked(agent.ToolStart{ID: u.ToolCallID, Name: name, Input: input})
	}
}

// withdrawFor: an ask open for call id is answered (the call runs, or ran).
func (d *Driver) withdrawFor(id string) {
	d.asks = slices.DeleteFunc(d.asks, func(a pendingAsk) bool {
		if a.ask.ToolUseID != id {
			return false
		}
		d.emitLocked(agent.AskCancel{RequestID: a.ask.RequestID})
		return true
	})
	if len(d.asks) == 0 && d.state == agent.Waiting {
		d.move(agent.Running, "")
	}
}

// sealLocked: the thought or the message being written is whole: recorded, and a message told.
func (d *Driver) sealLocked() {
	if d.thought.Len() > 0 {
		d.record(transcript.Entry{Kind: string(api.BlockKindThinking), Text: d.thought.String()})
		d.thought.Reset()
	}
	if d.text.Len() > 0 {
		text := d.text.String()
		d.text.Reset()
		d.record(transcript.Entry{Kind: string(api.BlockKindAssistant), Text: text})
		if !d.loading {
			d.reply = text
			d.emitLocked(agent.MessageDone{Text: text})
		}
	}
}

func (d *Driver) record(e transcript.Entry) { d.emitLocked(agent.Record{Entry: e}) }

// take1: the input is out of agy's queue: in the conversation, or turned down.
func (d *Driver) take1(id string) {
	if i := slices.Index(d.waiting, id); i >= 0 {
		d.waiting = slices.Delete(d.waiting, i, i+1)
	}
	if id != "" {
		d.emitLocked(agent.Taken{InputID: id})
	}
	if d.state == agent.Idle {
		d.starting = len(d.waiting) > 0
	}
}

func (d *Driver) move(s agent.State, input string) {
	was := d.state
	if s == was {
		return
	}
	d.state = s
	if was == agent.Idle {
		d.starting = false
		d.emitLocked(agent.TurnStart{Input: input})
	}
	d.emitLocked(agent.StateChange{State: s})
}

func (d *Driver) note(kind string) {
	d.unknown[kind]++
	if d.unknown[kind] == 1 {
		d.logf("agy: skipping unknown %s", kind)
	}
}

// Unknown is how often each unknown kind of output came.
func (d *Driver) Unknown() map[string]int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return maps.Clone(d.unknown)
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

// RunsItself: agy's commands (/compact, /goal...) go to it as a message; none is Iridium's to do.
func (d *Driver) RunsItself(string) bool { return false }
