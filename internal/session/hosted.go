package session

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kumoSleeping/iridium/internal/agent"
	"github.com/kumoSleeping/iridium/internal/agent/claude"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/hold"
	"github.com/kumoSleeping/iridium/internal/platform"
	"github.com/kumoSleeping/iridium/internal/store"
	"github.com/kumoSleeping/iridium/internal/transcript"
)

// Files the daemon keeps in a hold directory beside the holder's.
const (
	fileMeta   = "session.json"     // meta: what the session is
	fileProc   = "proc.json"        // procMeta: the agent process last started
	filePrompt = "system-prompt.md" // appended to the agent's system prompt
	fileStop   = "stop"             // the daemon asked the agent to stop: its exit is no crash
)

type meta struct {
	ID      string    `json:"id"`
	Agent   api.Agent `json:"agent"`
	Cwd     string    `json:"cwd"`
	Created int64     `json:"created"`
	Model   string    `json:"model,omitempty"`
	Mode    string    `json:"mode,omitempty"`
}

// procMeta: proc goes up by one each start; outStart is where its output begins in out.log.
type procMeta struct {
	Proc     int   `json:"proc"`
	OutStart int64 `json:"outStart"`
	Started  int64 `json:"started"`
}

// Hosted is one session Iridium hosts.
type Hosted struct {
	m    *Manager
	meta meta
	id   string
	dir  string
	unit string
	drv  agent.Driver

	startMu   sync.Mutex // one start at a time
	replaying atomic.Bool

	mu         sync.Mutex
	newSession bool // never started: its usage so far is nothing
	in         *hold.Input
	proc       int
	live       bool
	starting   bool
	stopAsked  bool
	turn       agent.State // where the agent's turn is
	asks       []api.Ask
	tasks      []api.Task
	model      string
	mode       string
	errText    string
	lastActive time.Time
	lastFrom   string // who sent the last message: whose turn an agent's own turn is counted to
	turnFrom   string // who began the running turn
	draftN     int
	ready      chan struct{} // closed once the process said its use so far (attached)
	base       *agent.Usage  // the session's use as of the last result, for each turn's share
	file       *transcript.File
	end        int64
	said       cached

	qmu   sync.Mutex   // one queue decision or release at a time; before mu
	queue []api.Queued // under mu

	effort        string
	models        []agent.Model
	commands      []agent.Command
	account, plan string
	rules         []store.AllowRule

	hadTurn     bool        // the agent has the running turn: a crash now is continued
	crashes     []time.Time // within the breaker's window
	crashResume bool        // the next start asks the agent to run the interrupted turn again
	resuming    bool        // this process was started so: no handshake yet
	turnSeen    bool        // a turn began since this process started
	quotaUntil  int64       // waiting for the quota until then (ms)
	quotaStop   bool        // stopped meanwhile: no "继续" when it is back
	quotaTimer  *time.Timer

	ctxSum   *api.ContextSummary
	tail     *api.BlockTail
	tailFrom int64
	tailAt   time.Time
	lamp     api.Status // the status last told to the watchers
}

// cached is what was read of the transcript at a size.
type cached struct {
	size  int64
	title string
	last  *api.LastSaid
}

func (m *Manager) newHosted(mt meta) *Hosted {
	h := &Hosted{m: m, meta: mt, id: mt.ID, dir: filepath.Join(m.holdRoot(), mt.ID), unit: m.o.UnitPrefix + mt.ID, model: mt.Model, mode: mt.Mode, lastActive: time.UnixMilli(mt.Created), turn: agent.Idle}
	h.drv = m.newDriver(mt.Agent, h.onEvent)
	h.loadQueue()
	if st, err := m.o.Store.Settings(mt.ID); err == nil {
		h.effort = st.Effort
		h.meta.Model = cmp.Or(st.Model, h.meta.Model)
		h.meta.Mode = cmp.Or(st.Mode, h.meta.Mode)
		h.model, h.mode = h.meta.Model, h.meta.Mode
	}
	h.rules, _ = m.o.Store.Rules(mt.ID)
	if c := h.savedContext(); c != nil {
		h.ctxSum = &api.ContextSummary{Used: c.Used, Max: c.Max, At: c.At}
	}
	return h
}

func (h *Hosted) logf(format string, a ...any) {
	h.m.o.Logf("session %s: "+format, append([]any{h.id}, a...)...)
}

func (h *Hosted) isLive() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.live
}

// start starts the session's agent unless it runs: through a holder in its own unit, resuming
// the session if the agent has it on record.
func (h *Hosted) start(ctx context.Context) error {
	h.startMu.Lock()
	defer h.startMu.Unlock()
	h.mu.Lock()
	if h.live {
		h.mu.Unlock()
		return nil
	}
	h.starting, h.errText = true, ""
	h.proc++
	proc := h.proc
	h.mu.Unlock()
	h.publish()
	err := h.launch(ctx, proc)
	if err != nil {
		h.mu.Lock()
		h.starting, h.errText = false, err.Error()
		h.mu.Unlock()
		h.publish()
	}
	return err
}

func (h *Hosted) launch(ctx context.Context, proc int) error {
	m := h.m
	// The last unit may still be ending (its ExecStopPost running): systemd takes no second one
	// of the name meanwhile.
	for i := 0; ; i++ {
		st, err := m.o.Launcher.State(ctx, h.unit)
		if err != nil {
			return err
		}
		if !st.Exists {
			break
		}
		if i == 100 {
			return fmt.Errorf("unit %s is still there", h.unit)
		}
		time.Sleep(100 * time.Millisecond)
	}
	found, err := transcript.Find(m.o.Config.ClaudeDir(), h.id)
	if err != nil {
		return err
	}
	prompt := filepath.Join(h.dir, filePrompt)
	if err := os.WriteFile(prompt, []byte(SystemPrompt), 0o600); err != nil {
		return err
	}
	h.mu.Lock()
	o := agent.SpawnOptions{Session: h.id, Resume: found != "", Cwd: h.meta.Cwd, Env: m.agentEnv(), Program: m.o.Config.Programs[string(h.meta.Agent)], SystemPromptFile: prompt,
		Model: h.meta.Model, Mode: cmp.Or(h.meta.Mode, m.o.Config.Modes[string(h.meta.Agent)]), Effort: h.effort, CrashResume: h.crashResume && found != ""}
	h.resuming, h.crashResume, h.turnSeen = o.CrashResume, false, false
	h.mu.Unlock()
	// An agent updating itself is gone for a few seconds: wait for it (Rhodium §4.2).
	var spec hold.Spec
	for wait := time.Now().Add(30 * time.Second); ; {
		spec, err = h.drv.Spawn(o)
		if !errors.Is(err, agent.ErrNoProgram) || time.Now().After(wait) {
			break
		}
		time.Sleep(2 * time.Second)
	}
	if err != nil {
		return err
	}
	spec.StopGraceMS = int(m.o.Config.StopGrace.D().Milliseconds())
	if err := hold.Prepare(h.dir, spec); err != nil {
		return err
	}
	_ = os.Remove(filepath.Join(h.dir, fileStop))
	pm := procMeta{Proc: proc, OutStart: hold.OutSize(h.dir), Started: time.Now().UnixMilli()}
	if err := writeJSON(filepath.Join(h.dir, fileProc), pm); err != nil {
		return err
	}
	u := platform.Unit{
		Name:        h.unit,
		Argv:        []string{m.o.Exe, "hold", h.dir},
		StopPost:    []string{m.o.Exe, "hold", "--stop-post", h.dir},
		StopTimeout: 2*m.o.Config.StopGrace.D() + 10*time.Second,
	}
	if err := m.o.Launcher.Start(ctx, u); err != nil {
		return err
	}
	in := hold.NewInput(h.dir)
	for deadline := time.Now().Add(10 * time.Second); !in.Ready(); time.Sleep(20 * time.Millisecond) {
		if e, _ := hold.ReadExit(h.dir); e != nil || time.Now().After(deadline) {
			break // the loop below reads its exit
		}
	}
	ready := make(chan struct{})
	h.mu.Lock()
	h.in, h.live, h.lastActive, h.stopAsked, h.ready = in, true, time.Now(), false, ready
	h.mu.Unlock()
	h.drv.Attach(in.WriteLine, proc)
	m.wg.Add(1)
	go h.loop(proc, pm.OutStart, pm.OutStart)
	go h.attached(ready)
	h.logf("started (%s, process %d)", map[bool]string{true: "resumed", false: "new"}[o.Resume], proc)
	return nil
}

// adopt takes up a session an earlier daemon hosted: if its holder still runs, the output seen
// before is fed again to bring the driver's state back, and initialize is sent again.
func (h *Hosted) adopt() error {
	var pm procMeta
	b, err := os.ReadFile(filepath.Join(h.dir, fileProc))
	if err == nil {
		err = json.Unmarshal(b, &pm)
	}
	if err != nil {
		return nil // never started
	}
	h.proc = pm.Proc
	st, err := h.m.o.Launcher.State(h.m.ctx, h.unit)
	if err != nil {
		return err
	}
	e, _ := hold.ReadExit(h.dir)
	if !st.Exists || e != nil {
		h.ended(e)
		return nil
	}
	in := hold.NewInput(h.dir)
	ready := make(chan struct{})
	h.mu.Lock()
	h.in, h.live, h.lastActive, h.ready = in, true, time.Now(), ready
	h.mu.Unlock()
	h.drv.Attach(in.WriteLine, pm.Proc)
	h.m.wg.Add(1)
	go h.loop(pm.Proc, pm.OutStart, hold.OutSize(h.dir))
	go h.attached(ready)
	h.logf("taken up (process %d)", pm.Proc)
	return nil
}

// attached reads the quota windows and the session's use so far, which each turn's share is
// counted from; then ready closes. Claude answers it right after initialize, before any turn.
func (h *Hosted) attached(ready chan struct{}) {
	defer close(ready)
	ctx, cancel := context.WithTimeout(h.m.ctx, 10*time.Second)
	defer cancel()
	u, err := h.drv.Quota(ctx)
	if err != nil {
		return
	}
	h.mu.Lock()
	if h.base == nil {
		h.base = u
	}
	h.mu.Unlock()
	if len(u.Windows) > 0 {
		h.setQuota(u.Windows)
	} else {
		h.m.publishQuota() // live now
	}
}

// loop feeds the agent's output to the driver as it comes, from outStart; lines before replay
// an earlier daemon saw. It ends with the process.
func (h *Hosted) loop(proc int, outStart, replay int64) {
	defer h.m.wg.Done()
	tail := hold.NewTail(filepath.Join(h.dir, hold.FileOutLog), outStart, claude.MaxLine)
	defer tail.Close()
	feed := func() int {
		lines, err := tail.Read()
		if err != nil {
			h.logf("reading out.log: %v", err)
		}
		for _, l := range lines {
			if l.Data == nil {
				h.logf("skipping a line of %d bytes at %d", l.Len, l.Off)
				continue
			}
			h.replaying.Store(l.Off < replay)
			h.drv.Feed(l.Data, l.Off < replay)
		}
		h.replaying.Store(false)
		return len(lines)
	}
	var looked, read time.Time
	for {
		n := feed()
		if time.Since(read) > 500*time.Millisecond {
			h.readTranscript()
			read = time.Now()
		}
		if n > 0 {
			continue
		}
		e, _ := hold.ReadExit(h.dir)
		if e == nil && time.Since(looked) > 2*time.Second {
			// A holder gone without an exit file: its ExecStopPost did not run.
			looked = time.Now()
			if st, err := h.m.o.Launcher.State(h.m.ctx, h.unit); err == nil && !st.Exists {
				if e, _ = hold.ReadExit(h.dir); e == nil {
					e = &hold.Exit{From: "holder"}
				}
			}
		}
		if e != nil {
			feed()
			h.exited(proc, e)
			return
		}
		select {
		case <-h.m.ctx.Done():
			return
		case <-time.After(h.m.o.Poll):
		}
	}
}

// exited: process proc ended as e says.
func (h *Hosted) exited(proc int, e *hold.Exit) {
	h.mu.Lock()
	if proc != h.proc {
		h.mu.Unlock()
		return
	}
	in := h.in
	h.in = nil
	h.mu.Unlock()
	if in != nil {
		in.Close()
	}
	h.drv.Detach()
	h.ended(e)
	h.logf("agent process %d ended: %s", proc, describe(e))
}

// ended sets the state of a session whose agent is not running; e: how it ended, if known. An
// end the daemon did not ask for, other than a clean exit, is an error.
func (h *Hosted) ended(e *hold.Exit) {
	_, err := os.Stat(filepath.Join(h.dir, fileStop))
	h.mu.Lock()
	asked := h.stopAsked || err == nil
	h.live, h.starting, h.stopAsked, h.resuming = false, false, false, false
	h.turn, h.asks, h.tasks = agent.Idle, nil, nil
	h.errText = ""
	// The next process says the session's use so far when it attaches.
	h.newSession, h.base = false, nil
	hadTurn := h.hadTurn
	h.hadTurn = false
	resume := false
	if e != nil && !asked && (e.From == "holder" || e.Signal != nil || (e.Code != nil && *e.Code != 0)) {
		h.errText = describe(e) + errTail(h.dir)
		resume = h.crashedLocked(hadTurn)
	}
	errText := h.errText
	h.mu.Unlock()
	h.pause()
	h.readTranscript()
	h.publish()
	h.m.publishQuota()
	if errText != "" {
		h.m.notify(Notice{Session: h.id, Kind: NoticeError, Text: errText})
	}
	if resume {
		go h.resume()
	}
}

func (h *Hosted) stop(ctx context.Context) error {
	h.mu.Lock()
	live := h.live
	h.stopAsked = live
	h.mu.Unlock()
	if !live {
		return nil
	}
	if err := os.WriteFile(filepath.Join(h.dir, fileStop), nil, 0o600); err != nil {
		return err
	}
	return h.m.o.Launcher.Stop(ctx, h.unit)
}

// idleSince: whether the agent runs with nothing to do, and since when.
func (h *Hosted) idleSince() (bool, time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.live && !h.starting && h.turn == agent.Idle && len(h.asks) == 0 && len(h.tasks) == 0, h.lastActive
}

// onEvent is the driver's emit.
func (h *Hosted) onEvent(e agent.Event) {
	replay := h.replaying.Load()
	var notes []Notice
	note := func(n Notice) {
		if !replay {
			n.Session = h.id
			notes = append(notes, n)
		}
	}
	later := func(f func()) {
		if !replay {
			go f()
		}
	}
	h.mu.Lock()
	proc := h.proc
	list := false // whether the session list changes
	switch e := e.(type) {
	case agent.TextDelta:
		n := h.draftN
		h.mu.Unlock()
		if !replay {
			h.m.o.Hub.Draft(h.id, proc, n, e.Text)
		}
		return
	case agent.MessageDone:
		h.draftN++ // seals the draft: the transcript has it now
		h.mu.Unlock()
		note(Notice{Kind: NoticeMessage, Text: e.Text})
		h.m.notify(notes...)
		return
	case agent.ToolStart:
		h.mu.Unlock()
		note(Notice{Kind: NoticeTool, Tool: e.Name})
		h.m.notify(notes...)
		return
	case agent.TurnStart:
		h.turnFrom = h.lastFrom
		if e.Input != "" {
			if from, ok := h.m.o.Store.Source(e.Input); ok {
				h.turnFrom = from
			}
		}
		h.draftN++
		h.lastActive = time.Now()
		h.hadTurn, h.turnSeen = true, true
		if !replay && h.quotaUntil != 0 {
			h.quotaEndLocked() // it went on by itself, or the queue did
		}
		note(Notice{Kind: NoticeTurnStart})
	case agent.Taken:
		h.hadTurn = true
	case agent.StateChange:
		h.turn = e.State
		h.lastActive = time.Now()
		list = true
	case agent.TurnEnd:
		h.lastActive = time.Now()
		h.hadTurn = false
		list = true
		later(h.refreshContext)
		later(h.refreshQuota)
		if !replay && h.quotaUntil == 0 {
			// What waited goes as the next turn; paused ones stay.
			later(func() {
				ctx, cancel := context.WithTimeout(h.m.ctx, time.Minute)
				defer cancel()
				h.release(ctx, false)
			})
		}
		note(Notice{Kind: NoticeTurnEnd, Text: e.Reply, Background: e.Background})
	case agent.AskOpen:
		if !replay && e.Ask.Kind == "permission" && h.allowedLocked(e.Ask) {
			id := e.Ask.RequestID
			later(func() { h.allow(id) })
			break
		}
		if !slices.ContainsFunc(h.asks, func(a api.Ask) bool { return a.RequestID == e.Ask.RequestID }) {
			h.asks = append(h.asks, e.Ask)
		}
		list = true
		ask := e.Ask
		note(Notice{Kind: NoticeAsk, Ask: &ask})
	case agent.AskCancel:
		if slices.ContainsFunc(h.asks, func(a api.Ask) bool { return a.RequestID == e.RequestID }) {
			note(Notice{Kind: NoticeAskDone, RequestID: e.RequestID}) // else a rule answered it
		}
		h.asks = slices.DeleteFunc(h.asks, func(a api.Ask) bool { return a.RequestID == e.RequestID })
		list = true
	case agent.PendingAsks:
		h.asks = e.Asks
		list = true
		later(h.allowMatching)
	case agent.Tasks:
		h.tasks = e.Tasks
	case agent.Started:
		h.model = e.Model
	case agent.Hello:
		h.starting = false
		h.mode = e.Mode
		h.models, h.commands = e.Models, e.Commands
		h.account, h.plan = e.Account, e.Plan
		list = true
		if h.resuming && !replay {
			seen := h.turnSeen
			later(func() { h.helloAfterResume(seen) })
		}
		h.resuming = false
		if h.turn == agent.Idle && h.quotaUntil == 0 {
			// Taken up idle with messages waiting: they go now.
			later(func() {
				ctx, cancel := context.WithTimeout(h.m.ctx, time.Minute)
				defer cancel()
				h.release(ctx, false)
			})
		}
	case agent.TurnUsage:
		base, from, model := h.base, h.turnFrom, h.model
		if e.Total != nil {
			h.base = e.Total
		}
		h.mu.Unlock()
		if !replay {
			h.recordUsage(e, base, from, model)
		}
		return
	case agent.Compacted:
		if !e.Failed {
			later(h.refreshContext)
			note(Notice{Kind: NoticeCompacted})
		}
	case agent.QuotaHit:
		if e.Exhausted && !replay {
			h.quotaOutLocked(e.ResetsAt)
		}
	case agent.ProtocolError:
		h.logf("protocol: %s: %s", e.Reason, e.Detail)
	}
	h.mu.Unlock()
	h.publish()
	if list {
		h.m.publishList()
	}
	h.m.notify(notes...)
}

// recordUsage writes the turn's usage rows, one per model: its share of the session's running
// totals where the totals before it are known, else the result's tokens, without a cost.
func (h *Hosted) recordUsage(e agent.TurnUsage, base *agent.Usage, from, model string) {
	if base == nil {
		h.mu.Lock()
		if h.newSession {
			base = &agent.Usage{}
		}
		h.mu.Unlock()
	}
	add := func(model string, t agent.Tokens, cost *float64) {
		err := h.m.o.Store.AddUsage(store.Usage{Terminal: from, Session: h.id, Agent: string(h.meta.Agent), Model: model, Input: t.Input, Output: t.Output, CacheRead: t.CacheRead, CacheWrite: t.CacheWrite, CostUSD: cost})
		if err != nil {
			h.logf("usage: %v", err)
		}
	}
	if base != nil && e.Total != nil {
		for name, d := range e.Total.Since(base) {
			cost := d.CostUSD
			add(name, d.Tokens, &cost)
		}
		return
	}
	if e.Tokens != (agent.Tokens{}) {
		add(model, e.Tokens, nil)
	}
}

// transcript is the session's transcript file, once the agent wrote one.
func (h *Hosted) transcript() *transcript.File {
	h.mu.Lock()
	f := h.file
	h.mu.Unlock()
	if f != nil {
		return f
	}
	path, err := transcript.Find(h.m.o.Config.ClaudeDir(), h.id)
	if err != nil || path == "" {
		return nil
	}
	f = transcript.Open(path, h.m.o.Store.Source)
	h.mu.Lock()
	if h.file == nil {
		h.file = f
	}
	f = h.file
	h.mu.Unlock()
	return f
}

// readTranscript notes how far the transcript goes, and what was last said: when it grew, the
// page fetches what came after its end.
func (h *Hosted) readTranscript() {
	f := h.transcript()
	if f == nil {
		return
	}
	size, err := f.Size()
	if err != nil {
		return
	}
	h.mu.Lock()
	grew := size != h.end
	old := h.end
	h.end = size
	stale := h.said.size != size
	h.mu.Unlock()
	if !grew {
		return
	}
	h.updateTail(f, old, size)
	if stale {
		title, _ := f.Title()
		last, _ := f.LastSaid()
		h.mu.Lock()
		h.said = cached{size: size, title: title, last: last}
		h.mu.Unlock()
	}
	h.publish()
	h.m.publishList()
}

func (h *Hosted) status() api.Status {
	switch {
	case h.starting:
		return api.StatusStarting
	case !h.live && h.errText != "":
		return api.StatusError
	case !h.live:
		return api.StatusIdle
	case len(h.asks) > 0:
		return api.StatusWaiting
	case h.turn == agent.Compacting:
		return api.StatusCompacting
	case h.turn != agent.Idle:
		return api.StatusRunning
	}
	return api.StatusIdle
}

func (h *Hosted) summary(o store.Overlay) api.Session {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.summaryLocked(o)
}

func (h *Hosted) summaryLocked(o store.Overlay) api.Session {
	return api.Session{
		ID: h.id, Agent: h.meta.Agent, Cwd: h.meta.Cwd, Name: o.Name, Title: h.said.title,
		Status: h.status(), Hosted: true, Live: h.live,
		Pinned: o.Pinned, Archived: o.Archived, Workspace: o.Workspace,
		Last: h.said.last, UpdatedAt: h.lastActive.UnixMilli(),
	}
}

func (h *Hosted) state(o store.Overlay) api.SessionState {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := api.SessionState{Session: h.summaryLocked(o), Asks: slices.Clone(h.asks), Tasks: slices.Clone(h.tasks), Model: h.model, Mode: h.mode, End: h.end, Proc: h.proc, Error: h.errText,
		Effort: h.effort, Queue: slices.Clone(h.queue), Context: h.ctxSum, QuotaUntil: h.quotaUntil, Tail: h.tail}
	for _, m := range h.models {
		s.Models = append(s.Models, api.Model{Value: m.Value, Name: m.Name, Description: m.Description})
	}
	if opts, ok := h.drv.(agent.Options); ok {
		s.Modes, s.Efforts = opts.Modes(), opts.Efforts()
	}
	for i := range s.Asks {
		s.Asks[i].Card = CardOf(h.id, s.Asks[i])
	}
	if s.Asks == nil {
		s.Asks = []api.Ask{}
	}
	if s.Queue == nil {
		s.Queue = []api.Queued{}
	}
	if s.Tasks == nil {
		s.Tasks = []api.Task{}
	}
	return s
}

func (h *Hosted) publish() {
	over, _ := h.m.o.Store.Overlays()
	s := h.state(over[h.id])
	h.m.o.Hub.Set("session:"+h.id, s)
	h.mu.Lock()
	changed := s.Status != h.lamp
	h.lamp = s.Status
	h.mu.Unlock()
	if changed && !h.replaying.Load() {
		h.m.notify(Notice{Session: h.id, Kind: NoticeStatus, Status: s.Status})
	}
}

// Commands are the commands the session's agent offers, as its last handshake named them.
func (m *Manager) Commands(id string) ([]agent.Command, error) {
	h, err := m.get(id)
	if err != nil {
		return nil, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.commands), nil
}

func describe(e *hold.Exit) string {
	if e == nil {
		return "unknown"
	}
	s := e.From
	if e.Code != nil {
		s += fmt.Sprintf(" exited %d", *e.Code)
	}
	if e.Signal != nil {
		s += " killed by " + *e.Signal
	}
	return s
}

// errTail is the last lines of err.log, for a crash's error.
func errTail(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, hold.FileErrLog))
	if err != nil || len(b) == 0 {
		return ""
	}
	if len(b) > 2000 {
		b = b[len(b)-2000:]
	}
	lines := strings.Split(strings.TrimSpace(string(bytes.ToValidUTF8(b, nil))), "\n")
	if len(lines) > 5 {
		lines = lines[len(lines)-5:]
	}
	return ": " + strings.Join(lines, " / ")
}
