package session

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kumoSleeping/iridium/internal/agent"
	"github.com/kumoSleeping/iridium/internal/agents"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/harness"
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
	fileSeen   = "seen.json"        // seenMeta: how far the daemon had read the agent's output
)

type meta struct {
	ID      string    `json:"id"`
	Agent   api.Agent `json:"agent"`
	Cwd     string    `json:"cwd"`
	Created int64     `json:"created"`
	Model   string    `json:"model,omitempty"`
	Mode    string    `json:"mode,omitempty"`
	// AgentID: the agent's own id for the session, where the agent makes it (Codex's thread).
	AgentID string `json:"agentId,omitempty"`
	// Launcher: the session that opened it (ir new from inside one), by Iridium's id.
	Launcher string `json:"launcher,omitempty"`
	// Machine: the executor it runs at, by terminal ID (docs/exec.md); "": here. Cwd is its path there.
	Machine string `json:"machine,omitempty"`
}

// agentID is the id the agent's records name the session by.
func (mt meta) agentID() string {
	if info, ok := agents.Get(mt.Agent); ok && info.OwnID {
		return mt.AgentID
	}
	return mt.ID
}

// findTranscript is the session's transcript file, "" if the agent wrote none yet.
func (h *Hosted) findTranscript() (string, error) {
	h.mu.Lock()
	mt := h.meta
	h.mu.Unlock()
	info, ok := agents.Get(mt.Agent)
	if !ok {
		return "", fmt.Errorf("no agent %s", mt.Agent)
	}
	if mc := h.machine(); mc != nil {
		return h.findRemoteTranscript(mc)
	}
	return info.Find(agents.Where{ID: mt.agentID(), Cwd: mt.Cwd, Env: h.m.agentEnv(), ClaudeHome: h.m.o.Config.ClaudeDir()})
}

// seenMeta: the daemon had read process Proc's output up to Offset in out.log. What comes after
// it a daemon taken up there has not seen: it is handled as it comes, not replayed.
type seenMeta struct {
	Proc   int   `json:"proc"`
	Offset int64 `json:"offset"`
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
	failed     string // why the driver gave the process up: the error once it has ended
	lastActive time.Time
	// shownAt: when the session last did something, as the list says (UpdatedAt): a turn begun or
	// ended, a process started. Unlike lastActive, not a daemon's restart taking it up, nor the
	// output replayed then (the user, 2026-11-06: every session read "just now" after a deploy).
	shownAt   time.Time
	lastFrom  string // who sent the last message: whose turn an agent's own turn is counted to
	compact   bool   // the last message was /compact: the compaction that follows was asked for
	turnFrom  string // who began the running turn
	draftN    int
	toolDraft string        // the tool call draft draftN is being written as, if one is
	toolName  string        // its tool's name
	ready     chan struct{} // closed once the process said its use so far (attached)
	base      *agent.Usage  // the session's use as of the last result, for each turn's share
	file      *transcript.File
	end       int64
	// mirroredAt: when the transcript's mirror was last brought up to date (remote.go).
	mirroredAt time.Time
	said       cached

	qmu     sync.Mutex   // one queue decision or release at a time; before mu
	queue   []api.Queued // under mu
	qimages map[string][]agent.Image

	restartDue bool // restart the agent at the turn's end, for its new mode
	restarting bool // stopped to start again: no pause, no crash
	// quiet: this start is a restart for a setting (model, effort, mode, preset), shown idle, not
	// starting: nothing was sent (the user, 2026-11-06). quietNext: the next start is one.
	quiet, quietNext bool
	// clearedTo: its agent cleared itself and went on as this conversation (clearedInside);
	// next: the session it goes on in, once taken up, for the open page to follow.
	clearedTo, next string

	effort        string
	preset        string        // the agent preset set for it (dsh); "": the agent's default
	effortNow     string        // the level the agent says it runs at
	ultracode     *bool         // set for it (Claude); nil: never set
	applied       agent.Applied // what the process said it applies, last read; none once it ends
	appliedRead   bool          // the process answered get_settings, Known or not (none: ultracode cannot be had)
	setMu         sync.Mutex    // one effort or ultracode change at a time: each writes back the other
	models        []agent.Model
	commands      []agent.Command
	account, plan string
	rules         []store.AllowRule
	rel           *released                  // a step auto mode denied, allowed from the page (release.go)
	turnAt        time.Time                  // when the running or last turn began
	denials       []api.Denial               // steps auto mode denied, shown till your next message (release.go)
	stuckOn       string                     // the turn's last call, if auto mode denied it: stopped on it, it needs you
	turnDenied    int                        // calls auto mode denied in this turn
	calls         map[string]agent.ToolStart // this turn's calls by id: what a denial was

	hadTurn     bool        // the agent has the running turn: a crash now is continued
	crashes     []time.Time // within the breaker's window
	crashResume bool        // the next start asks the agent to run the interrupted turn again
	resuming    bool        // this process was started so: no handshake yet
	turnSeen    bool        // a turn began since this process started
	quotaUntil  int64       // waiting for the quota until then (ms)
	quotaStop   bool        // stopped meanwhile: no "继续" when it is back
	quotaTimer  *time.Timer

	ctxSum   *api.ContextSummary
	ctxAsked time.Time // when the context was last asked for: within a turn, at most every ctxEvery
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
	h := &Hosted{m: m, meta: mt, id: mt.ID, dir: filepath.Join(m.holdRoot(), mt.ID), unit: m.o.UnitPrefix + mt.ID, model: mt.Model, mode: mt.Mode, lastActive: time.UnixMilli(mt.Created), shownAt: time.UnixMilli(mt.Created), turn: agent.Idle}
	h.drv = m.newDriver(mt.Agent, h.onEvent)
	h.qimages = map[string][]agent.Image{}
	h.loadQueue()
	if st, err := m.o.Store.Settings(mt.ID); err == nil {
		h.effort, h.ultracode, h.preset = st.Effort, st.Ultracode, st.Preset
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
	h.quiet, h.quietNext = h.quietNext, false
	h.next = "" // its own conversation again, resumed
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
		st, err := h.launcher().State(ctx, h.unit)
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
	found, err := h.findTranscript()
	if err != nil {
		return err
	}
	prompt := filepath.Join(h.dir, filePrompt)
	text := SystemPrompt(m.tuned().Lang)
	if mc := h.machine(); mc != nil {
		hi, _ := mc.Hello()
		text = RemotePrompt(m.tuned().Lang, hi != nil && hi.IRClient)
	}
	if err := os.WriteFile(prompt, []byte(text), 0o600); err != nil {
		return err
	}
	h.mu.Lock()
	o := agent.SpawnOptions{Session: h.id, Resume: found != "", AgentSession: h.meta.agentID(), Cwd: h.meta.Cwd, Env: m.agentEnv(), Program: m.o.Config.Programs[string(h.meta.Agent)], SystemPromptFile: prompt,
		Model: h.meta.Model, Mode: cmp.Or(h.meta.Mode, m.defaultMode(h.meta.Agent)), Effort: h.effort, Ultracode: h.ultracode, Preset: h.preset, CrashResume: h.crashResume && found != ""}
	h.resuming, h.crashResume, h.turnSeen = o.CrashResume, false, false
	h.mu.Unlock()
	if mc := h.machine(); mc != nil {
		if err := h.remoteSpawn(mc, &o); err != nil {
			return err
		}
	}
	if l, ok := channelOf[harness.Launcher](m, h.meta.Agent); ok {
		if err := l.Prepare(m.ctx, h.ref(), &o); err != nil {
			return err
		}
	}
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
	if err := h.launcher().Start(ctx, u); err != nil {
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
	h.shownAt = h.lastActive
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
	st, err := h.launcher().State(h.m.ctx, h.unit)
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
	// Taken up, not active: the list's time is when its transcript was last written, which only
	// the conversation does (not out.log: the handshake taking it up writes there).
	shown := h.shownAt
	if path, err := h.findTranscript(); err == nil && path != "" {
		if st, err := os.Stat(path); err == nil && st.ModTime().After(shown) {
			shown = st.ModTime()
		}
	}
	h.mu.Lock()
	h.in, h.live, h.lastActive, h.ready = in, true, time.Now(), ready
	h.shownAt = shown
	h.mu.Unlock()
	h.drv.Attach(in.WriteLine, pm.Proc)
	// The output before where the last daemon had read is replayed to bring the driver's state
	// back; what it wrote while no daemon read it (a turn's end, its usage) is new. A daemon
	// that saved no offset is taken to have read it all.
	replay := hold.OutSize(h.dir)
	var seen seenMeta
	if b, err := os.ReadFile(filepath.Join(h.dir, fileSeen)); err == nil && json.Unmarshal(b, &seen) == nil &&
		seen.Proc == pm.Proc && seen.Offset >= pm.OutStart && seen.Offset <= replay {
		replay = seen.Offset
	}
	h.m.wg.Add(1)
	go h.loop(pm.Proc, pm.OutStart, replay)
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
		go h.refreshQuota() // a balance instead (dsh), not waited for
		return
	}
	h.mu.Lock()
	if h.base == nil {
		h.base = u
	}
	h.mu.Unlock()
	if len(u.Windows) > 0 {
		h.setQuota(u.Windows, nil)
	} else {
		h.m.publishQuota() // live now
	}
}

// loop feeds the agent's output to the driver as it comes, from outStart; lines before replay
// an earlier daemon saw. It ends with the process.
func (h *Hosted) loop(proc int, outStart, replay int64) {
	defer h.m.wg.Done()
	tail := hold.NewTail(filepath.Join(h.dir, hold.FileOutLog), outStart, agents.MaxLine)
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
	// How far the output was read, saved now and then and as the daemon goes (seenMeta).
	// Saved too once the output is quiet: a turn's end read and not saved would be read again as
	// new by the next daemon, after a crash, and its usage counted twice.
	var saved time.Time
	savedAt := int64(-1)
	save := func() {
		off := tail.Offset()
		if err := writeJSON(filepath.Join(h.dir, fileSeen), seenMeta{Proc: proc, Offset: off}); err != nil {
			h.logf("saving how far out.log was read: %v", err)
		}
		saved, savedAt = time.Now(), off
	}
	var looked, read time.Time
	for {
		n := feed()
		if time.Since(read) > h.m.o.ReadEvery {
			h.readTranscript()
			read = time.Now()
		}
		if n > 0 {
			if time.Since(saved) > time.Second {
				save()
			}
			continue
		}
		if tail.Offset() != savedAt {
			save()
		}
		e, _ := hold.ReadExit(h.dir)
		if e == nil && time.Since(looked) > 2*time.Second {
			// A holder gone without an exit file: its ExecStopPost did not run.
			looked = time.Now()
			if st, err := h.launcher().State(h.m.ctx, h.unit); err == nil && !st.Exists {
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
			save()
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
	// What the process applied went with it: the next one is asked again.
	h.applied, h.appliedRead = agent.Applied{}, false
	// The next process says the session's use so far when it attaches.
	h.newSession, h.base = false, nil
	hadTurn := h.hadTurn
	h.hadTurn = false
	resume := false
	if e != nil && !asked && (e.From == "holder" || e.Signal != nil || (e.Code != nil && *e.Code != 0)) {
		h.errText = describe(e) + errTail(h.dir)
		resume = h.crashedLocked(hadTurn)
	}
	if h.failed != "" {
		h.errText, h.failed = h.failed, ""
	}
	errText := h.errText
	restarting := h.restarting
	h.mu.Unlock()
	if !restarting {
		h.pause()
	}
	h.readTranscript()
	h.endedHidden()
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
	return h.launcher().Stop(ctx, h.unit)
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
		n := h.draftN
		h.draftN++ // seals the draft: the transcript has it now
		h.mu.Unlock()
		if !replay {
			h.m.o.Hub.Seal(h.id, proc, n)
		}
		note(Notice{Kind: NoticeMessage, Text: transcript.WithoutNameLines(e.Text)})
		h.m.notify(notes...)
		return
	case agent.ToolDraft:
		// A call being written: a draft of its own, its input JSON as the text.
		n := h.draftN
		h.toolDraft, h.toolName = e.ID, e.Name
		h.mu.Unlock()
		if !replay {
			h.m.o.Hub.DraftTool(h.id, proc, n, e.ID, e.Name, "")
		}
		return
	case agent.ToolDraftDelta:
		n, id, name := h.draftN, h.toolDraft, h.toolName
		h.mu.Unlock()
		if id != "" && !replay {
			h.m.o.Hub.DraftTool(h.id, proc, n, id, name, e.JSON)
		}
		return
	case agent.ToolStart:
		// The call whole: its draft is sealed, the transcript has it now.
		if !replay {
			h.callLocked(e)
		}
		sealed := -1
		if h.toolDraft != "" {
			sealed = h.draftN
			h.draftN++
			h.toolDraft = ""
		}
		// A long turn's context grows with each call: read it in the turn too, not only at its end.
		due := !replay && time.Since(h.ctxAsked) >= ctxEvery
		if due {
			h.ctxAsked = time.Now()
		}
		h.mu.Unlock()
		if sealed >= 0 && !replay {
			h.m.o.Hub.Seal(h.id, proc, sealed)
		}
		if due {
			later(h.refreshContext)
		}
		note(Notice{Kind: NoticeTool, Tool: e.Name})
		h.m.notify(notes...)
		return
	case agent.TurnStart:
		h.turnFrom = h.lastFrom
		if e.Input != "" {
			if from, _, ok := h.m.o.Store.Source(e.Input); ok {
				h.turnFrom = from
			}
		}
		h.draftN++
		h.toolDraft = ""
		h.lastActive = time.Now()
		if !replay {
			h.shownAt = h.lastActive
		}
		h.hadTurn, h.turnSeen = true, true
		if !replay {
			h.turnAt = time.Now()
			h.calls = nil
			h.stuckOn, h.turnDenied = "", 0
		}
		if !replay && h.quotaUntil != 0 {
			h.quotaEndLocked() // it went on by itself, or the queue did
		}
		if !replay {
			h.shownLater() // activity: out of the archive
		}
		note(Notice{Kind: NoticeTurnStart})
	case agent.Taken:
		h.hadTurn = true
		// A message that runs on its own in the turn: its use is counted to who sent it.
		if e.Runs {
			if from, _, ok := h.m.o.Store.Source(e.InputID); ok {
				h.turnFrom = from
			}
		}
	case agent.ToolDone:
		if replay {
			break
		}
		if e.Denied != "" && h.deniedLocked(e) {
			list = true
			h.turnDenied++
		}
		// A call done after a denial: Claude went on another way. One denied last, it stopped there.
		if was := h.stuckOn; e.Denied != "" {
			h.stuckOn = e.ID
		} else if was != "" {
			h.stuckOn = ""
			list = true
		}
		if mode, over := h.releaseRanLocked(e.ID); over {
			list = true
			if mode != "" {
				later(func() { h.restoreMode(mode) })
			}
		}
	case agent.StateChange:
		h.turn = e.State
		h.lastActive = time.Now()
		if !replay {
			h.shownAt = h.lastActive
		}
		list = true
	case agent.TurnEnd:
		h.lastActive = time.Now()
		if !replay {
			h.shownAt = h.lastActive
		}
		h.hadTurn = false
		list = true
		h.ctxAsked = time.Now()
		later(h.refreshContext)
		// The quota is not read with the turn: quotaLoop reads it on its own, each minute (the user,
		// 2026-11-06).
		if !replay && h.restartDue {
			// A mode that switches only with a restart (pi): now, then what waited goes.
			h.restartDue = false
			later(h.restart)
		} else if !replay && h.quotaUntil == 0 {
			// What waited goes as the next turn; paused ones stay.
			later(func() {
				ctx, cancel := context.WithTimeout(h.m.ctx, time.Minute)
				defer cancel()
				h.release(ctx, false)
			})
		}
		if !replay {
			if mode := h.releaseOverLocked(); mode != "" {
				later(func() { h.restoreMode(mode) })
			}
		}
		note(Notice{Kind: NoticeTurnEnd, Text: transcript.WithoutNameLines(e.Reply), Background: e.Background, Denied: h.turnDenied})
	case agent.AskOpen:
		if !replay && e.Ask.Kind == "permission" && h.allowedLocked(e.Ask) {
			id := e.Ask.RequestID
			later(func() { h.allow(id) })
			break
		}
		if !slices.ContainsFunc(h.asks, func(a api.Ask) bool { return a.RequestID == e.Ask.RequestID }) {
			h.asks = append(h.asks, e.Ask)
		}
		if !replay {
			h.releaseAskedLocked(e.Ask)
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
		if e.Effort != "" {
			h.effortNow = e.Effort
		}
		// An agent that makes the session's id (Codex's thread) names it here: kept, so the next
		// start resumes it and its transcript is found.
		if info, ok := agents.Get(h.meta.Agent); ok && info.OwnID && e.Session != "" && e.Session != h.meta.AgentID {
			h.meta.AgentID = e.Session
			h.file, h.end, h.said = nil, 0, cached{}
			if err := writeJSON(filepath.Join(h.dir, fileMeta), h.meta); err != nil {
				h.logf("saving the agent's id: %v", err)
			}
		}
	case agent.Cleared:
		// Replayed too: a process taken up that cleared itself is still split from its session.
		if e.Session != h.clearedTo {
			h.clearedTo = e.Session
			go h.m.clearedInside(h, h.proc, e.Session)
		}
	case agent.Hello:
		h.starting = false
		h.mode = e.Mode
		if replay {
			h.rel = nil // the hello the process is taken up with says where it is
		} else {
			h.helloReleaseLocked(proc, e.Mode) // a new process: in the session's own mode
		}
		h.models, h.commands = e.Models, e.Commands
		h.account, h.plan = e.Account, e.Plan
		list = true
		// The model and the level in use, before a turn names them (Claude).
		later(func() { h.readApplied(proc) })
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
			note(Notice{Kind: NoticeCompacted, Asked: h.compact})
			h.compact = false
		}
	case agent.QuotaHit:
		if e.Exhausted && !replay {
			h.quotaOutLocked(e.ResetsAt)
		}
	case agent.ProtocolError:
		h.logf("protocol: %s: %s", e.Reason, e.Detail)
	case agent.GiveUp:
		if !replay {
			h.failed = e.Why
			h.logf("stopping its agent: %s", e.Why)
			go func() {
				if err := h.stop(h.m.ctx); err != nil {
					h.logf("stopping: %v", err)
				}
			}()
		}
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
		add(model, e.Tokens, e.CostUSD)
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
	path, err := h.findTranscript()
	if err != nil || path == "" {
		return nil
	}
	info, _ := agents.Get(h.meta.Agent)
	f = transcript.OpenAs(path, info.Format, h.m.o.Store.Source)
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
	h.syncRemote()
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
	case h.starting && !h.quiet:
		return api.StatusStarting
	case !h.live && h.errText != "":
		return api.StatusError
	case !h.live && h.stuckLocked():
		return api.StatusWaiting
	case !h.live:
		return api.StatusIdle
	case len(h.asks) > 0:
		return api.StatusWaiting
	case h.stuckLocked() && h.turn == agent.Idle:
		// The turn ended on a step auto mode denied: it waits for you. One Claude went past is
		// no one's business (the user, 2026-11-08).
		return api.StatusWaiting
	case h.turn == agent.Compacting:
		return api.StatusCompacting
	case h.turn != agent.Idle:
		return api.StatusRunning
	}
	return api.StatusIdle
}

// summary is h's row; archived: it carries no shown marker.
func (h *Hosted) summary(o store.Overlay, archived bool) api.Session {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.summaryLocked(o, archived)
}

func (h *Hosted) summaryLocked(o store.Overlay, archived bool) api.Session {
	return api.Session{
		ID: h.id, Agent: h.meta.Agent, Cwd: h.meta.Cwd, Name: o.Name, Title: h.said.title,
		Status: h.status(), Hosted: true, Live: h.live,
		Pinned: o.Pinned, Archived: archived, Workspace: o.Workspace, Color: api.Color(o.Color),
		Launcher: h.meta.Launcher, Last: h.said.last, UpdatedAt: h.shownAt.UnixMilli(),
		Machine: h.meta.Machine, Offline: h.offline(),
	}
}

func (h *Hosted) state(o store.Overlay, archived bool) api.SessionState {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := api.SessionState{Session: h.summaryLocked(o, archived), Asks: slices.Clone(h.asks), Tasks: slices.Clone(h.tasks), Denials: slices.Clone(h.denials), Model: h.model, Mode: h.mode, End: h.end, Proc: h.proc, Error: h.errText,
		Effort: h.effort, Queue: slices.Clone(h.queue), Context: h.ctxSum, QuotaUntil: h.quotaUntil, Tail: h.tail}
	s.Pick, s.EffortNow, s.Next, s.Preset = h.meta.Model, h.effortNow, h.next, h.preset
	for _, m := range h.models {
		s.Models = append(s.Models, api.Model{Value: m.Value, Name: m.Name, Description: m.Description, Resolved: m.Resolved, Efforts: slices.Clone(m.Efforts), NoEffort: m.NoEffort, EffortNames: maps.Clone(m.EffortNames), Group: m.Group})
	}
	h.m.channelState(h.refLocked(), &s)
	if _, ok := h.drv.(agent.Ultracoder); ok {
		u := &api.Ultracode{Available: true}
		if h.ultracode != nil {
			u.On = *h.ultracode
		}
		if a := h.applied; a.Known {
			u.On, u.Active, u.Available = a.Ultracode, a.UltracodeOn, a.UltracodeAvailable
		} else if h.appliedRead {
			u.Available = false // a CLI without get_settings: the driver refuses it
		}
		s.Ultracode = u
		s.Features = append(s.Features, api.Feature{ID: ultracodeFeature, Name: "UltraCode", On: u.On, Active: u.Active, Available: u.Available, Place: featurePlace, Tone: "warm"})
	}
	for _, c := range h.commandsLocked() {
		s.Commands = append(s.Commands, api.Command{Name: c.Name, Description: c.Description, ArgumentHint: c.ArgumentHint})
	}
	if info, ok := agents.Get(h.meta.Agent); ok {
		s.Modes = info.Modes
		if _, ok := h.drv.(agent.Efforter); ok {
			s.Efforts = info.Efforts
		}
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
	if s.Denials == nil {
		s.Denials = []api.Denial{}
	}
	return s
}

func (h *Hosted) publish() {
	over, _ := h.m.o.Store.Overlays()
	s := h.state(over[h.id], !h.shown())
	h.m.o.Hub.Set("session:"+h.id, s)
	h.mu.Lock()
	changed := s.Status != h.lamp
	h.lamp = s.Status
	h.mu.Unlock()
	if changed && !h.replaying.Load() {
		h.m.notify(Notice{Session: h.id, Kind: NoticeStatus, Status: s.Status})
	}
}

// Commands are the session's commands: those every agent has here (Iridium's /clear, and what
// the page or the driver does for any agent), then the others its agent's last handshake named.
// The common ones have no description: who shows them says it in its own language.
func (m *Manager) Commands(id string) ([]agent.Command, error) {
	h, err := m.get(id)
	if err != nil {
		return nil, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.commandsLocked(), nil
}

// commandsLocked is Commands; h.mu held.
func (h *Hosted) commandsLocked() []agent.Command {
	info, _ := agents.Get(h.meta.Agent)
	names := []string{"clear", "compact", "context"}
	if info.Quota {
		names = append(names, "usage")
	}
	names = append(names, "model")
	if _, ok := h.drv.(agent.Efforter); ok && (len(info.Efforts) > 0 || info.ModelEfforts) {
		names = append(names, "effort")
	}
	names = append(names, "mode")
	var out []agent.Command
	for _, n := range names {
		c := agent.Command{Name: n}
		if i := slices.IndexFunc(h.commands, func(a agent.Command) bool { return strings.TrimPrefix(a.Name, "/") == n }); i >= 0 && n != "clear" {
			c.ArgumentHint = h.commands[i].ArgumentHint
		}
		out = append(out, c)
	}
	for _, c := range h.commands {
		c.Name = strings.TrimPrefix(c.Name, "/")
		if !slices.Contains(names, c.Name) {
			out = append(out, c)
		}
	}
	return out
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

// readApplied asks process proc what it applies (agent.Applier: Claude's get_settings) and keeps
// it: the model in use, as a full id, the level, ultracode. Not on replay: it writes a request.
func (h *Hosted) readApplied(proc int) {
	r, ok := h.drv.(agent.Applier)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(h.m.ctx, 15*time.Second)
	defer cancel()
	a, err := r.Applied(ctx)
	if err != nil {
		h.logf("reading the settings applied: %v", err)
		return
	}
	h.mu.Lock()
	if h.proc != proc {
		h.mu.Unlock()
		return
	}
	h.appliedRead = true
	if !a.Known {
		h.mu.Unlock()
		h.publish()
		return
	}
	h.applied = a
	if a.Model != "" {
		h.model = a.Model
	}
	if a.Effort != "" {
		h.effortNow = a.Effort
	}
	h.mu.Unlock()
	h.publish()
}
