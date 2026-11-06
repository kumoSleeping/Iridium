// Package dsh is the DeepSeek Harness driver: it speaks `dsh --profile sdk` (newline-delimited
// JSON-RPC 2.0 on stdin/stdout; dsh 0.2.2-rc.1, @deepseek-ai/dsh-sdk-protocol's README) and turns
// it into the agent package's operations and events.
//
// One process serves one Iridium session, under Iridium's own id as dsh's sessionId. The model and
// the effort are the process's (initialize); the approval mode is dsh's permission preset or its
// plan mode, switched on the running process with session/query "/permission <preset>" and
// "/plan"; the agent preset runs process-wide through a generated --patch (preset.go). dsh has
// no cancel: an interrupt shuts the process down, and the next message resumes the session.
//
// Iridium's turn is dsh's whole-agent run, session.status running to idle: a message sent during
// it waits in dsh's inbox and runs as dsh's next turn by itself, within the same run.
package dsh

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

// Modes are dsh's permission presets, and its plan mode (the preset beneath stays as it was).
var Modes = []string{"read-only", "workspace-write", "danger-full-access", "plan"}

// DefaultMode never asks: dsh's danger-full-access (dsh's own default is workspace-write).
const DefaultMode = "danger-full-access"

// Efforts are the DeepSeek adapter's reasoning efforts (dsh-llm-deepseek).
var Efforts = []string{"off", "low", "high", "max"}

const (
	Provider      = "deepseek-official"
	DefaultModel  = "deepseek-flash"
	DefaultEffort = "high" // what the adapter runs at when none is given
)

// Models are the official models dsh-llm-deepseek's catalog names (DEFAULT_MODELS).
var Models = []agent.Model{
	{Value: "deepseek-flash", Name: "DeepSeek-V4.1-Flash", Efforts: Efforts},
	{Value: "deepseek-v4-pro", Name: "DeepSeek-V4-Pro", Efforts: Efforts},
}

// Commands are the slash commands the sdk server runs itself (session/query).
var Commands = []agent.Command{
	{Name: "compact", Description: "Compact the conversation"},
	{Name: "plan", Description: "Plan mode: on, off, or plan this", ArgumentHint: "[off|message]"},
	{Name: "goal", Description: "Work toward a goal over several rounds", ArgumentHint: "[objective|clear|edit objective|pause|resume]"},
}

var (
	ErrNotAttached = errors.New("dsh: no agent process attached")
	// ErrNoQuota: dsh reports no quota windows.
	ErrNoQuota  = errors.New("dsh: no quota windows")
	errReplaced = errors.New("dsh: the agent process ended")
	errEnding   = errors.New("dsh: the agent process is stopping after an interrupt; send it again in a moment")
)

type Options struct {
	Emit     func(agent.Event)
	IDPrefix string // starts the driver's request ids
	Logf     func(format string, a ...any)
}

// launch is what the process attaching next was spawned with.
type launch struct {
	session, cwd, model, effort, version string
	resume                               bool
}

type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("dsh: %s (%d)", e.Message, e.Code) }

type result struct {
	data json.RawMessage
	err  error
}

// call is a request waiting for its answer. prompt: the input it gives the session, if any.
type call struct {
	ch     chan result
	prompt string
	query  bool
	stop   bool // shutdown: its answer ends the run
}

// ask is an open ask and how to answer it.
type ask struct {
	api.Ask
	rpcID     json.RawMessage
	approval  string // approval/asked's id
	questions []question
	approve   string // plan review: the option that approves
	keep      string // and the one that keeps planning
}

type question struct {
	ID       string `json:"id"`
	Question string `json:"question"`
	Header   string `json:"header,omitempty"`
	Detail   string `json:"detail,omitempty"`
	Options  []struct {
		Label       string `json:"label"`
		Description string `json:"description,omitempty"`
	} `json:"options,omitempty"`
	MultiSelect bool `json:"multiSelect,omitempty"`
	Intent      *struct {
		Kind    string `json:"kind"`
		Approve string `json:"approve"`
		CallID  string `json:"callId"`
	} `json:"intent,omitempty"`
}

type job struct {
	task       api.Task
	call       string
	background bool
}

// Driver is one session's dsh driver. See agent.Driver.
type Driver struct {
	emitFn func(agent.Event)
	prefix string
	logf   func(string, ...any)

	modeMu sync.Mutex // one mode change at a time

	mu      sync.Mutex
	w       agent.Writer
	proc    int
	seq     int
	pending map[string]*call
	spawned bool // the process attaching next was just spawned: hand it the handshake
	next    launch
	session string
	mode    string // the mode the session should be in
	ready   chan struct{}
	failed  error         // the handshake failed
	gone    chan struct{} // closed when the attached process ends
	ending  bool          // shut down for an interrupt

	model, effort, version string // the process's
	perm                   string // dsh's permission preset, as it last said; "": not known
	plan, planKnown        bool

	state    agent.State
	starting bool          // an input went out while idle and its run has not started yet
	started  string        // that input
	prompted chan struct{} // closed once the last input's receipt came
	// outstanding: the prompt written and not yet receipted; byDsh: dsh's ids of the prompts not
	// yet in the conversation; early: dsh's user messages seen before their receipt.
	outstanding string
	byDsh       map[string]string
	early       map[string]bool
	asks        []*ask
	tools       map[string]string // open tool calls: their names
	args        map[string]any    // tool calls' arguments, by call id
	approvals   map[string]string // approval/asked ids by call id
	jobs        map[string]*job
	children    map[string]bool // subagent sessions under this one
	subagents   []api.Task
	tasks       []api.Task // as last told
	reply       string
	run         agent.Tokens
	context     int
	window      int
	compact     bool // a compaction began while idle: it is the turn
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
	d := &Driver{emitFn: o.Emit, prefix: o.IDPrefix, logf: o.Logf, unknown: map[string]int{}, mode: DefaultMode, ready: closed(), gone: make(chan struct{})}
	d.reset()
	return d
}

func (d *Driver) reset() {
	d.pending = map[string]*call{}
	d.prompted = closed()
	d.byDsh, d.early = map[string]string{}, map[string]bool{}
	d.tools, d.args, d.approvals = map[string]string{}, map[string]any{}, map[string]string{}
	d.jobs, d.children = map[string]*job{}, map[string]bool{}
	d.subagents, d.tasks = nil, nil
	d.state = agent.Idle
	d.starting, d.started, d.outstanding, d.asks = false, "", "", nil
	d.reply, d.run, d.context, d.window, d.compact = "", agent.Tokens{}, 0, 0, false
	d.model, d.effort, d.version, d.perm, d.plan, d.planKnown = "", "", "", "", false, false
	d.failed, d.ending = nil, false
}

func closed() chan struct{} {
	c := make(chan struct{})
	close(c)
	return c
}

// Spawn gives the holder's spec: `dsh --profile sdk --patch <generated>`. The patch runs the
// agent preset and appends the system prompt (re-read now, so a resume takes the current one);
// it is made from the installed package each time, beside the system prompt. dsh never sees
// Claude's keys nor a parent session's variables; DEEPSEEK_API_KEY and DEEPSEEK_BASE_URL go
// through as they are.
func (d *Driver) Spawn(o agent.SpawnOptions) (hold.Spec, error) {
	env := agent.ForeignEnv(o.Env)
	program, err := agent.LookPath(cmp.Or(o.Program, "dsh"), env)
	if err != nil {
		return hold.Spec{}, err
	}
	// dsh is a `#!/usr/bin/env node` script: without node on that PATH it cannot start at all.
	if head, err := readHead(program); err == nil && bytes.HasPrefix(head, []byte("#!")) && bytes.Contains(bytes.SplitN(head, []byte("\n"), 2)[0], []byte("env node")) {
		if _, err := agent.LookPath("node", env); err != nil {
			return hold.Spec{}, fmt.Errorf("dsh: node not found: %w", err)
		}
	}
	mode := cmp.Or(o.Mode, DefaultMode)
	if !slices.Contains(Modes, mode) {
		return hold.Spec{}, fmt.Errorf("dsh: no approval mode %q (%s)", mode, strings.Join(Modes, ", "))
	}
	if o.Effort != "" && !slices.Contains(Efforts, o.Effort) {
		return hold.Spec{}, fmt.Errorf("dsh: no effort %q (%s)", o.Effort, strings.Join(Efforts, ", "))
	}
	if o.SystemPromptFile == "" {
		return hold.Spec{}, errors.New("dsh: the agent preset needs a directory for its patch")
	}
	suffix := ""
	if b, err := os.ReadFile(o.SystemPromptFile); err == nil {
		suffix = string(b)
	} else if !errors.Is(err, os.ErrNotExist) {
		return hold.Spec{}, fmt.Errorf("dsh: %w", err)
	}
	pkg, err := FindPackage(program)
	if err != nil {
		return hold.Spec{}, fmt.Errorf("dsh: cannot build agent preset %q: %w", cmp.Or(o.Preset, DefaultPreset), err)
	}
	patch, err := pkg.Patch(cmp.Or(o.Preset, DefaultPreset), suffix)
	if err != nil {
		return hold.Spec{}, err
	}
	path := filepath.Join(filepath.Dir(o.SystemPromptFile), PatchFile)
	if err := writeFile(path, patch); err != nil {
		return hold.Spec{}, fmt.Errorf("dsh: writing the preset's patch: %w", err)
	}
	cwd := o.Cwd
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	model := o.Model
	if model == "" || model == "default" {
		model = DefaultModel
	}
	d.mu.Lock()
	d.spawned = true
	d.next = launch{session: o.Session, cwd: cwd, model: model, effort: o.Effort, version: pkg.Version, resume: o.Resume}
	d.session, d.mode = o.Session, mode
	d.mu.Unlock()
	env = agent.SetEnv(env, "IRIDIUM_SESSION", o.Session)
	return hold.Spec{Argv: []string{program, "--profile", "sdk", "--patch", path}, Env: env, Cwd: o.Cwd}, nil
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

// writeFile writes path whole or not at all: a dsh starting meanwhile may be reading it.
func writeFile(path string, data []byte) error {
	if b, err := os.ReadFile(path); err == nil && bytes.Equal(b, data) {
		return nil
	}
	tmp := fmt.Sprintf("%s.%d", path, os.Getpid())
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Attach connects agent process proc. One just spawned gets the handshake: initialize, then the
// session resumed or made, then the mode; Hello once it is done. One an earlier daemon started
// was given it then: its replayed output brings the state back.
func (d *Driver) Attach(w agent.Writer, proc int) {
	d.mu.Lock()
	if proc != d.proc {
		d.resetLocked()
	}
	d.w, d.proc = w, proc
	d.gone = make(chan struct{})
	spawned, l := d.spawned, d.next
	d.spawned = false
	if spawned {
		d.ready = make(chan struct{})
		d.model, d.effort, d.version = l.model, l.effort, l.version
	} else {
		d.ready = closed()
	}
	ready := d.ready
	d.mu.Unlock()
	if spawned {
		go d.handshake(proc, l, ready)
	}
}

func (d *Driver) handshake(proc int, l launch, ready chan struct{}) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	fail := func(why string) {
		d.mu.Lock()
		same := d.proc == proc
		if same {
			d.failed = errors.New(why)
		}
		d.mu.Unlock()
		close(ready)
		if same {
			d.emit(agent.GiveUp{Why: why})
		}
	}
	params := map[string]any{"cwd": l.cwd, "provider": Provider, "model": l.model, "approvalRequests": true, "capabilities": map[string]any{"userQuestions": true}}
	if l.effort != "" {
		params["reasoningEffort"] = l.effort
	}
	r, err := d.rpc(ctx, "initialize", params)
	if err != nil {
		fail(fmt.Sprintf("dsh: initialize (%s/%s): %v", l.model, cmp.Or(l.effort, DefaultEffort), err))
		return
	}
	var info struct {
		ServerInfo struct {
			Version string `json:"version"`
		} `json:"serverInfo"`
	}
	_ = json.Unmarshal(r, &info)
	session := map[string]any{"sessionId": l.session}
	// The transcript says whether dsh has the session; it is asked again if it disagrees.
	resumed := l.resume
	if resumed {
		_, err = d.rpc(ctx, "session/resume", session)
		if err != nil && strings.Contains(err.Error(), "not found") {
			resumed = false
			_, err = d.rpc(ctx, "session/working-directory/get", session)
		}
	} else {
		_, err = d.rpc(ctx, "session/working-directory/get", session)
		if err != nil && strings.Contains(err.Error(), "already exists") {
			resumed = true
			_, err = d.rpc(ctx, "session/resume", session)
		}
	}
	if err != nil {
		fail(fmt.Sprintf("dsh: opening session %s: %v", l.session, err))
		return
	}
	d.mu.Lock()
	if d.proc != proc {
		d.mu.Unlock()
		close(ready)
		return
	}
	if !resumed {
		d.plan, d.planKnown = false, true // a new session is not in plan mode
	}
	d.emitLocked(agent.Started{Session: l.session, Model: d.model, Effort: cmp.Or(d.effort, DefaultEffort)})
	mode := d.mode
	evs := d.take()
	d.mu.Unlock()
	d.flush(evs)
	// A new session in plan mode plans over Iridium's default preset, which it leaves plan mode to.
	if mode == "plan" && !resumed {
		if err := d.applyMode(ctx, DefaultMode); err != nil {
			d.emit(agent.ProtocolError{Reason: "mode", Detail: err.Error()})
		}
	}
	if err := d.applyMode(ctx, mode); err != nil {
		d.emit(agent.ProtocolError{Reason: "mode", Detail: err.Error()})
	}
	d.mu.Lock()
	same := d.proc == proc
	h := agent.Hello{Version: cmp.Or(l.version, info.ServerInfo.Version), Mode: d.mode, Models: slices.Clone(Models), Commands: slices.Clone(Commands)}
	d.mu.Unlock()
	close(ready)
	if same {
		d.emit(h)
	}
}

// waitReady waits for the handshake.
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
	if d.failed != nil {
		return d.failed
	}
	if d.w == nil {
		return ErrNotAttached
	}
	return nil
}

func (d *Driver) Detach() {
	d.mu.Lock()
	d.resetLocked()
	d.w = nil
	close(d.gone)
	d.gone = make(chan struct{})
	evs := d.take()
	d.mu.Unlock()
	d.flush(evs)
}

func (d *Driver) resetLocked() {
	for id, c := range d.pending {
		c.ch <- result{err: errReplaced}
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

// waitPrompted waits until every input written so far has its receipt, so that dsh's ids map to
// Iridium's in order.
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

// Send writes a message: session/prompt, or session/query for the commands dsh runs itself
// (RunsItself). One while a run goes waits in dsh's inbox and runs next in the same run.
func (d *Driver) Send(ctx context.Context, m agent.Message) error {
	if m.ID == "" || (m.Text == "" && len(m.Images) == 0) {
		return errors.New("dsh: a message needs an id and text")
	}
	if err := d.waitReady(ctx); err != nil {
		return err
	}
	if err := d.waitPrompted(ctx); err != nil {
		return err
	}
	command := d.RunsItself(m.Text)
	blocks := []map[string]any{}
	if m.Text != "" {
		blocks = append(blocks, map[string]any{"type": "text", "text": m.Text})
	}
	for _, im := range m.Images {
		blocks = append(blocks, map[string]any{"type": "image", "data": base64.StdEncoding.EncodeToString(im.Data), "mimeType": im.MediaType})
	}
	d.mu.Lock()
	if d.w == nil {
		d.mu.Unlock()
		return ErrNotAttached
	}
	if d.ending {
		d.mu.Unlock()
		return errEnding
	}
	busy := d.busy()
	if command && busy && isCompact(m.Text) {
		d.mu.Unlock()
		return errors.New("dsh: a turn is running; stop it before /compact")
	}
	method := "session/prompt"
	if command {
		method = "session/query"
	}
	id := d.nextID()
	c := &call{ch: make(chan result, 1), query: command}
	if !command {
		c.prompt = m.ID
		d.outstanding = m.ID
	}
	if !busy {
		d.starting, d.started = true, m.ID
	}
	d.pending[id] = c
	prompted := make(chan struct{})
	d.prompted = prompted
	d.mu.Unlock()
	go func() {
		<-c.ch
		close(prompted)
	}()
	if err := d.write(request(id, method, map[string]any{"sessionId": d.sessionID(), "contentBlocks": blocks})); err != nil {
		d.mu.Lock()
		delete(d.pending, id)
		if d.outstanding == m.ID {
			d.outstanding = ""
		}
		if !busy {
			d.starting, d.started = false, ""
		}
		d.mu.Unlock()
		c.ch <- result{err: err}
		return err
	}
	if command {
		d.emit(agent.Taken{InputID: m.ID})
	}
	return nil
}

func isCompact(text string) bool {
	t := strings.TrimSpace(text)
	return t == "/compact" || t == "/压缩"
}

func (d *Driver) sessionID() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.session
}

// Interrupt: dsh has no cancel. The process is shut down (its turn ends aborted on disk, an open
// tool call gets an error result) and the next message starts it again, resuming the session.
// It returns once the process has ended.
func (d *Driver) Interrupt(ctx context.Context) error {
	d.mu.Lock()
	if d.w == nil {
		d.mu.Unlock()
		return ErrNotAttached
	}
	if !d.busy() || d.ending {
		d.mu.Unlock()
		return nil
	}
	d.ending = true
	gone := d.gone
	d.mu.Unlock()
	if _, err := d.call(ctx, "shutdown", nil, &call{stop: true}); err != nil && !errors.Is(err, errReplaced) {
		d.mu.Lock()
		d.ending = false
		d.mu.Unlock()
		return err
	}
	select {
	case <-gone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Answer answers an approval (allowed once, or rejected: dsh takes no words with it), a
// question (by question text, as the card answers; a text that is no option is the custom
// answer), or a plan review (approve, or keep planning with the words as feedback).
func (d *Driver) Answer(ctx context.Context, a api.Answer) error {
	d.mu.Lock()
	i := slices.IndexFunc(d.asks, func(x *ask) bool { return x.RequestID == a.RequestID })
	if i < 0 {
		d.mu.Unlock()
		return fmt.Errorf("dsh: no open ask %s", a.RequestID)
	}
	k := d.asks[i]
	if a.Kind != "" && a.Kind != k.Kind {
		d.mu.Unlock()
		return fmt.Errorf("dsh: ask %s is a %s, not a %s", a.RequestID, k.Kind, a.Kind)
	}
	if d.w == nil {
		d.mu.Unlock()
		return ErrNotAttached
	}
	d.asks = slices.Delete(d.asks, i, i+1)
	proc := d.proc
	d.mu.Unlock()
	var res any
	switch k.Kind {
	case "permission":
		outcome := "rejected"
		if a.Allow {
			outcome = "allowed-once"
		}
		res = map[string]any{"outcome": outcome}
	case "plan":
		q := k.questions[0]
		item := map[string]any{"id": q.ID, "selected": []string{k.approve}}
		switch {
		case a.Allow:
		case strings.TrimSpace(a.Message) != "":
			// One choice: words in place of it are the feedback, and keep planning.
			item = map[string]any{"id": q.ID, "selected": []string{}, "custom": strings.TrimSpace(a.Message)}
		default:
			item["selected"] = []string{k.keep}
		}
		res = map[string]any{"answers": []any{item}}
	default:
		res = map[string]any{"answers": answers(k.questions, a.Answers)}
	}
	frame, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": k.rpcID, "result": res})
	if err := d.writeLine(frame); err != nil {
		d.mu.Lock()
		if d.proc == proc {
			d.asks = append(d.asks, k)
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

// answers names every question once. A single choice is its label, or else custom words; a
// multiple one is labels joined by ", " (as the card joins them), what is no label custom words.
// A question not answered is skipped (selected empty).
func answers(qs []question, by map[string]string) []any {
	out := []any{}
	for _, q := range qs {
		item := map[string]any{"id": q.ID, "selected": []string{}}
		text := strings.TrimSpace(by[q.Question])
		label := func(s string) bool {
			return slices.ContainsFunc(q.Options, func(o struct {
				Label       string `json:"label"`
				Description string `json:"description,omitempty"`
			}) bool {
				return o.Label == s
			})
		}
		switch {
		case text == "":
		case q.MultiSelect:
			var sel, rest []string
			for _, p := range strings.Split(text, ", ") {
				if label(p) {
					sel = append(sel, p)
				} else {
					rest = append(rest, p)
				}
			}
			if sel != nil {
				item["selected"] = sel
			}
			if rest != nil {
				item["custom"] = strings.Join(rest, ", ")
			}
		case label(text):
			item["selected"] = []string{text}
		default:
			item["custom"] = text
		}
		out = append(out, item)
	}
	return out
}

// SetModel: the model is the process's (initialize). Another one takes a restart:
// agent.ErrRestart, and the next start runs it.
func (d *Driver) SetModel(ctx context.Context, model string) error {
	if model == "" || model == "default" {
		model = DefaultModel
	}
	d.mu.Lock()
	now := d.model
	d.mu.Unlock()
	if model == now {
		return nil
	}
	return agent.ErrRestart
}

// SetEffort: the effort is the process's too; see SetModel.
func (d *Driver) SetEffort(ctx context.Context, level string) error {
	if !slices.Contains(Efforts, level) {
		return fmt.Errorf("dsh: no effort %q (%s)", level, strings.Join(Efforts, ", "))
	}
	d.mu.Lock()
	now := cmp.Or(d.effort, DefaultEffort)
	d.mu.Unlock()
	if level == now {
		return nil
	}
	return agent.ErrRestart
}

// SetMode switches the running session at once: "/permission <preset>", or "/plan" (the preset
// beneath stays); leaving plan mode is "/plan off", then the preset.
func (d *Driver) SetMode(ctx context.Context, mode string) error {
	mode = cmp.Or(mode, DefaultMode)
	if !slices.Contains(Modes, mode) {
		return fmt.Errorf("dsh: no approval mode %q (%s)", mode, strings.Join(Modes, ", "))
	}
	d.mu.Lock()
	d.mode = mode
	d.mu.Unlock()
	if err := d.waitReady(ctx); err != nil {
		return err
	}
	return d.applyMode(ctx, mode)
}

// applyMode puts the session in mode, as far as dsh's last word says it is not; with no word
// (a resumed session) every switch goes, which dsh takes as nothing when it is so already.
func (d *Driver) applyMode(ctx context.Context, mode string) error {
	d.modeMu.Lock()
	defer d.modeMu.Unlock()
	d.mu.Lock()
	perm, plan, known := d.perm, d.plan, d.planKnown
	d.mu.Unlock()
	if mode == "plan" {
		if !known || !plan {
			return d.command(ctx, "/plan")
		}
		return nil
	}
	if !known || plan {
		if err := d.command(ctx, "/plan off"); err != nil {
			return err
		}
	}
	if perm != mode {
		return d.command(ctx, "/permission "+mode)
	}
	return nil
}

// command runs a slash command dsh runs itself, waiting for it.
func (d *Driver) command(ctx context.Context, text string) error {
	r, err := d.rpc(ctx, "session/query", map[string]any{"sessionId": d.sessionID(), "contentBlocks": []any{map[string]any{"type": "text", "text": text}}})
	if err != nil {
		return fmt.Errorf("%s: %w", text, err)
	}
	var q queryResult
	_ = json.Unmarshal(r, &q)
	if q.Kind != "command" || q.Execution.Result.Kind != "success" {
		return fmt.Errorf("dsh: %s: %s", text, cmp.Or(q.Execution.Result.Text, string(r)))
	}
	return nil
}

type queryResult struct {
	Kind      string `json:"kind"`
	MessageID string `json:"messageId"`
	Execution struct {
		Result struct {
			Kind string `json:"kind"`
			Text string `json:"text"`
		} `json:"result"`
	} `json:"execution"`
}

// Context is the last request's prompt (its answer's input, cached or not) against the model's
// window. Right after a compaction dsh says none until its next answer.
func (d *Driver) Context(ctx context.Context) (*api.Context, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.context == 0 {
		return nil, errors.New("dsh: no context usage until the next answer")
	}
	return &api.Context{Used: d.context, Max: d.window, At: time.Now().UnixMilli()}, nil
}

// Quota: dsh reports no quota windows, nor running totals.
func (d *Driver) Quota(ctx context.Context) (*agent.Usage, error) { return nil, ErrNoQuota }

// RunsItself: what the sdk server runs as a command rather than a prompt (bare /compact,
// /plan [off|message], /goal [...], and their Chinese names).
func (d *Driver) RunsItself(text string) bool {
	t := strings.TrimSpace(text)
	if isCompact(t) {
		return true
	}
	for _, c := range []string{"/plan", "/计划", "/goal", "/目标"} {
		if rest, ok := strings.CutPrefix(t, c); ok && (rest == "" || rest[0] == ' ' || rest[0] == '\n') {
			return true
		}
	}
	return false
}

// ---- requests ----

func (d *Driver) nextID() string {
	d.seq++
	return fmt.Sprintf("%s%d", d.prefix, d.seq)
}

func request(id, method string, params any) map[string]any {
	m := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		m["params"] = params
	}
	return m
}

// rpc sends one request and waits for its result; dsh's error is an error.
func (d *Driver) rpc(ctx context.Context, method string, params any) (json.RawMessage, error) {
	return d.call(ctx, method, params, &call{})
}

func (d *Driver) call(ctx context.Context, method string, params any, c *call) (json.RawMessage, error) {
	d.mu.Lock()
	if d.w == nil {
		d.mu.Unlock()
		return nil, ErrNotAttached
	}
	id := d.nextID()
	c.ch = make(chan result, 1)
	d.pending[id] = c
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
	case r := <-c.ch:
		return r.data, r.err
	case <-ctx.Done():
		drop()
		return nil, ctx.Err()
	}
}

func (d *Driver) write(v any) error {
	line, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return d.writeLine(line)
}

func (d *Driver) writeLine(line []byte) error {
	d.mu.Lock()
	w := d.w
	d.mu.Unlock()
	if w == nil {
		return ErrNotAttached
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

type model struct {
	Model  string `json:"model"`
	Effort string `json:"reasoningEffort"`
}

type event struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// ignored: events the driver knows and has no use for.
var ignored = map[string]bool{
	"turn/start": true, "step/start": true, "step/end": true, "system/message": true, "developer/message": true,
	"session/title": true, "session-log-deepseek/delivery-accepted": true, "sandbox/mode": true, "approval/policy": true,
	"command/run": true, "command/done": true, "compaction/summary": true, "session/end-seed": true,
}

// Feed handles one line of output. replay: an earlier daemon saw it; never answered.
func (d *Driver) Feed(line []byte, replay bool) {
	d.mu.Lock()
	answer := d.feed(line, replay)
	w := d.w
	evs := d.take()
	d.mu.Unlock()
	d.flush(evs)
	if answer != nil && w != nil && !replay {
		go func() { _ = w(answer) }()
	}
}

func (d *Driver) feed(line []byte, replay bool) []byte {
	if len(line) > MaxLine {
		d.emitLocked(agent.ProtocolError{Reason: "line_too_long", Detail: fmt.Sprintf("%d bytes, skipped", len(line))})
		return nil
	}
	// Stdout is the protocol's only: anything else is a broken line.
	var f frame
	if len(line) == 0 || line[0] != '{' || json.Unmarshal(line, &f) != nil {
		d.emitLocked(agent.ProtocolError{Reason: "bad_line", Detail: string(line[:min(len(line), 200)])})
		return nil
	}
	hasID := len(f.ID) > 0 && string(f.ID) != "null"
	switch {
	case f.Method != "" && hasID:
		return d.serverRequest(&f)
	case f.Method != "":
		d.notification(&f)
	case hasID:
		d.response(&f)
	}
	return nil
}

func (d *Driver) response(f *frame) {
	var id string
	if json.Unmarshal(f.ID, &id) != nil {
		id = string(f.ID)
	}
	c, ok := d.pending[id]
	if !ok {
		return // an earlier daemon's, or one given up on
	}
	delete(d.pending, id)
	var err error
	if f.Error != nil {
		err = f.Error
	}
	if c.prompt != "" || c.query {
		d.receipt(c, f.Result, err)
	}
	if c.stop && err == nil {
		d.settled() // dsh says no more of the run it disposes of
	}
	c.ch <- result{data: f.Result, err: err}
}

// receipt: an input's answer. A prompt's names dsh's id of it; a command's says it ran. One
// refused never joins the conversation, and no run follows it.
func (d *Driver) receipt(c *call, raw json.RawMessage, err error) {
	// Not outstanding any more: the inbox named it first.
	mine := c.prompt != "" && d.outstanding == c.prompt
	if mine {
		d.outstanding = ""
	}
	if err != nil {
		d.emitLocked(agent.ProtocolError{Reason: "unexpected", Detail: "dsh refused a message: " + err.Error()})
		if c.prompt != "" {
			d.take1(c.prompt)
		}
		if d.starting && d.state == agent.Idle {
			d.starting, d.started = false, ""
		}
		return
	}
	var q queryResult
	_ = json.Unmarshal(raw, &q)
	if q.Kind == "command" {
		if q.Execution.Result.Kind != "success" {
			d.emitLocked(agent.ProtocolError{Reason: "command", Detail: "dsh: " + cmp.Or(q.Execution.Result.Text, string(raw))})
		}
		// One that started no run (a mode, a goal paused) leaves it idle.
		if d.starting && d.state == agent.Idle {
			d.starting, d.started = false, ""
		}
		return
	}
	if !mine || q.MessageID == "" {
		return
	}
	d.byDsh[q.MessageID] = c.prompt
	if d.early[q.MessageID] {
		delete(d.early, q.MessageID)
		d.take1(q.MessageID)
	}
}

// take1: dsh's message id is in the conversation (or turned down for good); for an Iridium id
// not mapped yet, that id.
func (d *Driver) take1(id string) {
	if in, ok := d.byDsh[id]; ok {
		delete(d.byDsh, id)
		d.emitLocked(agent.Taken{InputID: in})
		return
	}
	d.emitLocked(agent.Taken{InputID: id})
}

func (d *Driver) notification(f *frame) {
	var p struct {
		SessionID       string          `json:"sessionId"`
		Status          string          `json:"status"`
		Event           json.RawMessage `json:"event"`
		RequestID       string          `json:"requestId"`
		ParentSessionID string          `json:"parentSessionId"`
		ChildSessionID  string          `json:"childSessionId"`
	}
	_ = json.Unmarshal(f.Params, &p)
	// An adopted process's session is the first one named: the handshake opened it.
	if d.session == "" && p.SessionID != "" && (f.Method == "session.event" || f.Method == "session.status") {
		d.session = p.SessionID
	}
	switch f.Method {
	case "session.event":
		var e event
		if json.Unmarshal(p.Event, &e) != nil {
			return
		}
		switch {
		case p.SessionID == d.session:
			d.event(&e)
		case d.children[p.SessionID] && e.Type == "assistant/message":
			d.usage(e.Data, false) // a subagent's tokens count to the turn
		}
	case "session.status":
		if p.SessionID != d.session {
			return
		}
		switch p.Status {
		case "running":
			if d.state != agent.Idle {
				return
			}
			input := ""
			if d.starting {
				input = d.started
			}
			d.starting, d.started = false, ""
			d.run = agent.Tokens{}
			d.move(agent.Running, input)
		case "idle":
			d.settled()
		}
	case "user-question.cancelled":
		d.withdraw(func(k *ask) bool { return k.RequestID == p.RequestID })
	case "subagent.started":
		if p.ParentSessionID == d.session || d.children[p.ParentSessionID] {
			d.children[p.ChildSessionID] = true
			d.subagents = append(d.subagents, api.Task{ID: p.ChildSessionID, Type: "subagent"})
			d.tellTasks()
		}
	case "subagent.finished":
		if i := slices.IndexFunc(d.subagents, func(t api.Task) bool { return t.ID == p.ChildSessionID }); i >= 0 {
			d.subagents = slices.Delete(d.subagents, i, i+1)
			d.tellTasks()
		}
	case "session.query.completed":
	default:
		d.note(f.Method)
	}
}

func (d *Driver) event(e *event) {
	switch e.Type {
	case "permission/preset":
		var v struct {
			Preset string `json:"preset"`
		}
		_ = json.Unmarshal(e.Data, &v)
		d.perm = v.Preset
	case "plan/mode":
		var v struct {
			Active bool `json:"active"`
		}
		_ = json.Unmarshal(e.Data, &v)
		if d.planKnown && d.plan && !v.Active && d.mode == "plan" {
			// The plan was approved: it goes on in the preset beneath.
			d.mode = cmp.Or(d.perm, DefaultMode)
		}
		d.plan, d.planKnown = v.Active, true
	case "agent/inbox/spliced":
		// A prompt waiting for its receipt is the next message of yours the inbox takes in.
		var v struct {
			Inserted []struct {
				ID     string `json:"id"`
				Role   string `json:"role"`
				Source struct {
					Kind string `json:"kind"`
				} `json:"source"`
			} `json:"inserted"`
		}
		_ = json.Unmarshal(e.Data, &v)
		for _, m := range v.Inserted {
			if m.Role == "user" && m.Source.Kind == "user" && d.outstanding != "" {
				if _, ok := d.byDsh[m.ID]; !ok {
					d.byDsh[m.ID] = d.outstanding
					d.outstanding = ""
				}
			}
		}
	case "user/message":
		var v struct {
			ID     string `json:"id"`
			Source struct {
				Kind string `json:"kind"`
			} `json:"source"`
		}
		_ = json.Unmarshal(e.Data, &v)
		if v.Source.Kind != "user" {
			return
		}
		if _, ok := d.byDsh[v.ID]; ok {
			d.take1(v.ID)
		} else if d.outstanding != "" {
			d.early[v.ID] = true
		}
	case "request/header", "model/selection":
		// The route a request goes on, or the one a resume records: model and effort.
		var v struct {
			model
			Header struct {
				Config model `json:"config"`
			} `json:"header"`
		}
		_ = json.Unmarshal(e.Data, &v)
		c := v.model
		if e.Type == "request/header" {
			c = v.Header.Config
		}
		if c.Model != "" && (c.Model != d.model || (c.Effort != "" && c.Effort != cmp.Or(d.effort, DefaultEffort))) {
			d.model = c.Model
			if c.Effort != "" {
				d.effort = c.Effort
			}
			d.emitLocked(agent.Started{Session: d.session, Model: d.model, Effort: cmp.Or(d.effort, DefaultEffort)})
		}
	case "request/context":
		var v struct {
			Window int `json:"contextWindow"`
		}
		_ = json.Unmarshal(e.Data, &v)
		if v.Window > 0 {
			d.window = v.Window
		}
	case "assistant/message":
		d.usage(e.Data, true)
	case "tool/call":
		var v struct {
			CallID    string `json:"callId"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		}
		_ = json.Unmarshal(e.Data, &v)
		var args any
		if json.Unmarshal([]byte(v.Arguments), &args) != nil {
			args = v.Arguments
		}
		d.tools[v.CallID] = v.Name
		d.args[v.CallID] = args
		d.emitLocked(agent.ToolStart{ID: v.CallID, Name: v.Name, Input: args})
	case "tool/result":
		var v struct {
			Message struct {
				ToolCallID string `json:"toolCallId"`
			} `json:"message"`
		}
		_ = json.Unmarshal(e.Data, &v)
		id := v.Message.ToolCallID
		if name, ok := d.tools[id]; ok {
			delete(d.tools, id)
			d.emitLocked(agent.ToolDone{ID: id, Name: name})
		}
		delete(d.args, id)
		delete(d.approvals, id)
		// What it asked is answered, here or by an earlier daemon.
		d.withdraw(func(k *ask) bool { return k.ToolUseID == id })
		// A job still running when its call is answered goes on in the background.
		changed := false
		for _, j := range d.jobs {
			if j.call == id && !j.background {
				j.background, changed = true, true
			}
		}
		if changed {
			d.tellTasks()
		}
	case "approval/asked":
		var v struct {
			ID     string `json:"id"`
			CallID string `json:"callId"`
		}
		_ = json.Unmarshal(e.Data, &v)
		if v.CallID != "" {
			d.approvals[v.CallID] = v.ID
		}
	case "approval/decided":
		var v struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(e.Data, &v)
		d.withdraw(func(k *ask) bool { return k.approval != "" && k.approval == v.ID })
	case "job/registered", "job/settled":
		var v struct {
			JobID    string `json:"jobId"`
			Owner    string `json:"owner"`
			Kind     string `json:"kind"`
			Label    string `json:"label"`
			ToolCall struct {
				CallID string `json:"callId"`
			} `json:"toolCall"`
		}
		_ = json.Unmarshal(e.Data, &v)
		if e.Type == "job/registered" {
			if v.Owner == "" || v.Owner == d.session {
				d.jobs[v.JobID] = &job{task: api.Task{ID: v.JobID, Type: v.Kind, Description: v.Label}, call: v.ToolCall.CallID}
			}
		} else if j, ok := d.jobs[v.JobID]; ok {
			delete(d.jobs, v.JobID)
			if j.background {
				d.tellTasks()
			}
		}
	case "compaction/start":
		if d.state == agent.Idle {
			d.compact = true
			input := ""
			if d.starting {
				input = d.started
			}
			d.starting, d.started = false, ""
			d.move(agent.Compacting, input)
		} else {
			d.move(agent.Compacting, "")
		}
	case "compaction/end":
		var v struct {
			Error json.RawMessage `json:"error"`
		}
		_ = json.Unmarshal(e.Data, &v)
		d.emitLocked(agent.Compacted{Failed: len(v.Error) > 0 && string(v.Error) != "null"})
		d.context = 0 // known again with the next answer
		if d.compact {
			d.compact = false
			d.end(true)
		} else if d.state == agent.Compacting {
			d.move(agent.Running, "")
		}
	case "turn/end":
		var v struct {
			Reason struct {
				Kind string `json:"kind"`
			} `json:"reason"`
		}
		_ = json.Unmarshal(e.Data, &v)
		if v.Reason.Kind == "error" || v.Reason.Kind == "blocked" {
			d.emitLocked(agent.ProtocolError{Reason: "turn_" + v.Reason.Kind, Detail: string(e.Data)})
		}
	default:
		if !ignored[e.Type] {
			d.note("event " + e.Type)
		}
	}
}

// usage reads an assistant message: its tokens, and for this session's own, its text and the
// context its request carried.
func (d *Driver) usage(raw json.RawMessage, own bool) {
	var v struct {
		Message struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			Source struct {
				Model string `json:"model"`
			} `json:"source"`
		} `json:"message"`
		Usage *struct {
			Input      int `json:"inputTokens"`
			Output     int `json:"outputTokens"`
			CacheRead  int `json:"cacheReadTokens"`
			CacheWrite int `json:"cacheWriteTokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(raw, &v) != nil {
		return
	}
	if u := v.Usage; u != nil {
		d.run = agent.Tokens{Input: d.run.Input + u.Input, Output: d.run.Output + u.Output, CacheRead: d.run.CacheRead + u.CacheRead, CacheWrite: d.run.CacheWrite + u.CacheWrite}
		if own {
			d.context = u.Input + u.CacheRead + u.CacheWrite
		}
	}
	if !own {
		return
	}
	if m := v.Message.Source.Model; m != "" && m != d.model {
		d.model = m
		d.emitLocked(agent.Started{Session: d.session, Model: m, Effort: cmp.Or(d.effort, DefaultEffort)})
	}
	var text strings.Builder
	for _, b := range v.Message.Content {
		if b.Type == "text" {
			text.WriteString(b.Text)
		}
	}
	if text.Len() > 0 {
		d.reply = text.String()
		d.emitLocked(agent.MessageDone{Text: d.reply})
	}
}

// serverRequest: dsh asks, an approval or questions. One fed again may be open still (a daemon's
// restart does not end the process): it is put to you again, and goes once its tool call has its
// result. Anything else is refused.
func (d *Driver) serverRequest(f *frame) []byte {
	switch f.Method {
	case "approval/request":
		var p struct {
			ToolName string `json:"toolName"`
			CallID   string `json:"callId"`
			Reason   string `json:"reason"`
		}
		_ = json.Unmarshal(f.Params, &p)
		k := &ask{Ask: api.Ask{RequestID: idText(f.ID), Kind: "permission", Tool: p.ToolName, Input: d.args[p.CallID], ToolUseID: p.CallID, Description: p.Reason}, rpcID: f.ID, approval: d.approvals[p.CallID]}
		d.open(k)
		return nil
	case "user-question/ask":
		var p struct {
			RequestID string     `json:"requestId"`
			Questions []question `json:"questions"`
		}
		if json.Unmarshal(f.Params, &p) != nil || len(p.Questions) == 0 {
			break
		}
		k := &ask{rpcID: f.ID, questions: p.Questions}
		k.RequestID = cmp.Or(p.RequestID, idText(f.ID))
		if q := p.Questions[0]; len(p.Questions) == 1 && q.Intent != nil && q.Intent.Kind == "plan-review" {
			k.Kind, k.Tool, k.ToolUseID = "plan", "exit_plan_mode", q.Intent.CallID
			k.Input = map[string]any{"plan": q.Detail}
			k.approve = cmp.Or(q.Intent.Approve, "Approve")
			k.keep = "Keep planning"
			for _, o := range q.Options {
				if o.Label != k.approve {
					k.keep = o.Label
					break
				}
			}
		} else {
			k.Kind, k.Tool = "question", "ask_user_question"
			qs := []any{}
			for _, q := range p.Questions {
				opts := []any{}
				for _, o := range q.Options {
					opts = append(opts, map[string]any{"label": o.Label, "description": o.Description})
				}
				m := map[string]any{"question": q.Question, "header": q.Header, "options": opts, "multiSelect": q.MultiSelect}
				if q.Detail != "" {
					m["detail"] = q.Detail
				}
				qs = append(qs, m)
			}
			k.Input = map[string]any{"questions": qs}
			// It holds the tool call that asks; that call's result ends it.
			for id, name := range d.tools {
				if name == "ask_user_question" {
					k.ToolUseID = id
				}
			}
		}
		d.open(k)
		return nil
	}
	d.note("request " + f.Method)
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": f.ID, "error": map[string]any{"code": -32601, "message": "Iridium does not answer " + f.Method}})
	return b
}

func idText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return string(raw)
}

func (d *Driver) open(k *ask) {
	if slices.ContainsFunc(d.asks, func(x *ask) bool { return x.RequestID == k.RequestID }) {
		return
	}
	d.asks = append(d.asks, k)
	d.emitLocked(agent.AskOpen{Ask: k.Ask})
	if d.state == agent.Running {
		d.move(agent.Waiting, "")
	}
}

// withdraw takes back the open asks f picks.
func (d *Driver) withdraw(f func(*ask) bool) {
	n := len(d.asks)
	d.asks = slices.DeleteFunc(d.asks, func(k *ask) bool {
		if f(k) {
			d.emitLocked(agent.AskCancel{RequestID: k.RequestID})
			return true
		}
		return false
	})
	if n > 0 && len(d.asks) == 0 && d.state == agent.Waiting {
		d.move(agent.Running, "")
	}
}

// tellTasks says the background work: jobs that outlived their tool call, and subagents.
func (d *Driver) tellTasks() {
	var t []api.Task
	for _, j := range d.jobs {
		if j.background {
			t = append(t, j.task)
		}
	}
	slices.SortFunc(t, func(a, b api.Task) int { return strings.Compare(a.ID, b.ID) })
	t = append(t, d.subagents...)
	if slices.Equal(t, d.tasks) {
		return
	}
	d.tasks = t
	d.emitLocked(agent.Tasks{Tasks: slices.Clone(t)})
}

// settled: the run is over. What it still asks is void.
func (d *Driver) settled() {
	d.withdraw(func(*ask) bool { return true })
	for id, name := range d.tools {
		d.emitLocked(agent.ToolDone{ID: id, Name: name})
	}
	d.tools, d.args, d.approvals = map[string]string{}, map[string]any{}, map[string]string{}
	clear(d.early)
	for _, j := range d.jobs {
		j.background = true
	}
	d.tellTasks()
	d.starting, d.started = false, ""
	if d.run != (agent.Tokens{}) {
		d.emitLocked(agent.TurnUsage{Tokens: d.run, Context: d.context})
	}
	d.run = agent.Tokens{}
	d.end(false)
}

func (d *Driver) end(compactOnly bool) {
	if d.state == agent.Idle {
		return
	}
	d.state = agent.Idle
	d.emitLocked(agent.StateChange{State: agent.Idle})
	d.emitLocked(agent.TurnEnd{Reply: d.reply, CompactOnly: compactOnly, Background: len(d.tasks) > 0})
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
		d.logf("dsh: skipping unknown %s", kind)
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
