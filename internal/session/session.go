// Package session hosts agent sessions: each runs through a holder (package hold) started by
// the platform's launcher, spoken to by its agent's driver, its history read from the agent's
// own transcript. It carries each session's small state to the hub and reaps idle agents.
//
// The session list is the disk's (plan "数据"): finding the sessions agents started by
// themselves is a Discovery's job; the Manager lists the ones it hosts.
package session

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log"
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
	"github.com/kumoSleeping/iridium/internal/config"
	"github.com/kumoSleeping/iridium/internal/harness"
	"github.com/kumoSleeping/iridium/internal/hub"
	"github.com/kumoSleeping/iridium/internal/irhelp"
	"github.com/kumoSleeping/iridium/internal/platform"
	"github.com/kumoSleeping/iridium/internal/remote"
	"github.com/kumoSleeping/iridium/internal/store"
)

// UnitPrefix names the holders' units; tests use iridium-test-.
const UnitPrefix = "iridium-hold-"

// SystemPrompt is appended to every hosted agent's system prompt (plan "ir"), written anew at each
// start of its process: ir's agent commands (irhelp), how to ask for a secret, hand over a file
// and title the session (transcript/named.go), and lang, the user's language as the web settings
// set it ("zh", "en"; "" leaves that line out).
func SystemPrompt(lang string) string {
	var b strings.Builder
	b.WriteString("This session is hosted by Iridium. Its ir command reaches Iridium: other agent sessions (start them, message them, wait for them), files and command lines at the user's other computers, secrets.\n")
	b.WriteString(irhelp.Agent())
	b.WriteString(irhelp.Layers())
	b.WriteString(`Secrets: never ask for one in the chat: ir api help secrets.
Files: to show or hand the user a file, put the Markdown that ir api POST internal/links path=<path> prints in your reply: ![caption](ir:id) shows a picture, a video or a sound in place, [text](ir:id) is a file to download. Any other path, file:// link or local image is plain text.
`)
	b.WriteString(namesLine)
	b.WriteString(titleLine)
	if name := langNames[lang]; name != "" {
		b.WriteString("The user's language, as set in the web settings: " + name + ".\n")
	}
	return b.String()
}

// RemotePrompt is SystemPrompt for a session hosted at another machine: with ir (its executor
// puts one there, wire.Hello.IRClient), the commands that ir has (irhelp.Remote); without, none.
// No file links: its files are that machine's, which ir attach there does not reach.
func RemotePrompt(lang string, ir bool) string {
	var b strings.Builder
	if ir {
		b.WriteString("This session is hosted by Iridium and runs at another machine than Iridium's own. Use its ir command to start other agent sessions (here or at other machines), message them and wait for their results:\n\n")
		b.WriteString(irhelp.Remote())
		b.WriteString(irhelp.Layers())
	} else {
		b.WriteString("This session is hosted by Iridium and runs at another machine than Iridium's own.\n")
	}
	b.WriteString(namesLine)
	b.WriteString(titleLine)
	if name := langNames[lang]; name != "" {
		b.WriteString("The user's language, as set in the web settings: " + name + ".\n")
	}
	return b.String()
}

// namesLine: how words name a session or a folder, as the page's composer writes them (docs/plan.md
// "@") and draws them, in the user's words and in a reply.
const namesLine = "Names: [title](ir:session/<id>) is a session, [name](ir:folder/<path>) a folder (<computer>:<path> at another), as the user's messages write them, shown as links to them. Satori's <img src=\"ir:id\"/>, <file src=\"ir:id\"/> and <sharp id name/> work too.\n"

const titleLine = "Title: as your first action in a session, and again only when its subject changes, run ir api channel.update channel_id=self data.name=\"<title>\": a short name for the specific thing and task, in the user's language, without punctuation, quotes or emoji. If the user named it, leave it.\n"

var langNames = map[string]string{"zh": "Chinese", "en": "English"}

// Discovery finds the sessions on disk that agents started by themselves (agents' record
// directories, ~/.claude/sessions/, /proc). Another package implements it.
type Discovery interface {
	Sessions(ctx context.Context) ([]api.Session, error)
}

// grouper is a Discovery that puts a new session in its folder's group at once (discover.Index).
type grouper interface {
	GroupFor(cwd string, prefer int64) int64
}

type Options struct {
	Config   config.Config
	Store    *store.Store
	Hub      *hub.Hub
	Launcher platform.Launcher
	// Remote: the executors sessions may run at (docs/exec.md); nil: none.
	Remote *remote.Hub
	// Channels: the harnesses' own code, by agent (internal/harness); bound to the manager.
	Channels   harness.Set
	Exe        string // the binary that runs `hold`; "" is this one
	UnitPrefix string // "" is UnitPrefix
	Discovery  Discovery
	Logf       func(string, ...any)
	ReapEvery  time.Duration // how often idle agents are looked for; 0 is 30s
	Poll       time.Duration // how often a live agent's out.log is read; 0 is 50ms
	ReadEvery  time.Duration // how often a live agent's transcript is looked at; 0 is 500ms
	// ClearPoll: how often a session whose agent cleared itself is looked at, for its process
	// quiet and then gone (clearedInside); 0 is half a second.
	ClearPoll time.Duration
	// Tag names an interactive terminal (by ID) in the source tag "[name] "; nil or "" is the ID.
	Tag func(terminal string) string
	// QuotaEvery: how often running agents' quota windows are read, on their own, not with a turn
	// (the user, 2026-11-06); 0 is a minute.
	QuotaEvery time.Duration
	// QuotaStagger: sessions going on after the quota came back wait up to this much more, so
	// they do not all go at once; 0 is a minute, below 0 none.
	QuotaStagger time.Duration
}

var (
	ErrNotFound = errors.New("no such session")
	ErrNotLive  = errors.New("the session's agent is not running")
)

type Manager struct {
	o      Options
	att    attachments // what is attached to sessions' pages (attach.go)
	shell  shells      // command lines run at sessions' machines (shell.go)
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu     sync.Mutex
	hosted map[string]*Hosted
	quota  map[string]api.AgentQuota // by quotaKey

	wmu      sync.Mutex
	watchers []func(Notice)

	tune     atomic.Pointer[Tunables] // tune.go
	annotate atomic.Pointer[Annotate] // carry.go
}

func New(o Options) (*Manager, error) {
	if o.Exe == "" {
		exe, err := os.Executable()
		if err != nil {
			return nil, err
		}
		o.Exe = exe
	}
	if o.UnitPrefix == "" {
		o.UnitPrefix = UnitPrefix
	}
	if o.Logf == nil {
		o.Logf = log.Printf
	}
	if o.ReapEvery == 0 {
		o.ReapEvery = 30 * time.Second
	}
	if o.ClearPoll == 0 {
		o.ClearPoll = 500 * time.Millisecond
	}
	if o.Poll == 0 {
		o.Poll = 50 * time.Millisecond
	}
	if o.ReadEvery == 0 {
		o.ReadEvery = 500 * time.Millisecond
	}
	if o.QuotaEvery == 0 {
		o.QuotaEvery = time.Minute
	}
	if o.QuotaStagger == 0 {
		o.QuotaStagger = time.Minute
	}
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{o: o, ctx: ctx, cancel: cancel, hosted: map[string]*Hosted{}, quota: map[string]api.AgentQuota{}}
	m.Tune(TunablesOf(o.Config))
	o.Hub.Provide = m.provide
	if o.Remote != nil {
		o.Remote.Dir = m.HoldDir
		o.Remote.Changed = m.machineChanged
		o.Remote.Handle(eventAttach, m.onAttach)
	}
	for _, c := range o.Channels {
		c.Bind(host{m})
	}
	return m, nil
}

func (m *Manager) holdRoot() string { return filepath.Join(m.o.Config.StatePath(), "hold") }

// Store is the database the manager keeps its inputs and usage in.
func (m *Manager) Store() *store.Store { return m.o.Store }

// Start takes up the sessions an earlier daemon hosted, whose holders may still run, and starts
// reaping idle agents.
func (m *Manager) Start() error {
	if err := os.MkdirAll(m.holdRoot(), 0o700); err != nil {
		return err
	}
	m.loadQuota()
	m.sweepShellDirs()
	vis := m.visibility()
	metas, _ := filepath.Glob(filepath.Join(m.holdRoot(), "*", fileMeta))
	for _, p := range metas {
		var mt meta
		b, err := os.ReadFile(p)
		if err == nil {
			err = json.Unmarshal(b, &mt)
		}
		if err != nil || mt.ID != filepath.Base(filepath.Dir(p)) {
			m.o.Logf("session: skipping %s: %v", p, err)
			continue
		}
		h := m.newHosted(mt)
		m.mu.Lock()
		m.hosted[mt.ID] = h
		m.mu.Unlock()
		h.adoptShown(vis)
		if err := h.adopt(); err != nil {
			m.o.Logf("session %s: %v", mt.ID, err)
		}
	}
	m.wg.Add(2)
	go m.reap()
	go m.quotaLoop()
	m.publishList()
	return nil
}

// Close stops reading the agents' output. The holders and their agents keep running.
func (m *Manager) Close() {
	m.cancel()
	m.wg.Wait()
}

func (m *Manager) get(id string) (*Hosted, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	h := m.hosted[id]
	if h == nil {
		return nil, ErrNotFound
	}
	return h, nil
}

// MachineOf is hosted session id's computer: its executor's app ID, "" here; ok false for a
// session Iridium does not host.
func (m *Manager) MachineOf(id string) (machine string, ok bool) {
	h, err := m.get(id)
	if err != nil {
		return "", false
	}
	return h.meta.Machine, true
}

// TurnFrom is who began hosted session id's latest turn: the terminal, and the session whose agent
// sent the message that began it, if one did. ok is false for a session Iridium does not host, or
// one with no turn yet.
func (m *Manager) TurnFrom(id string) (from, via string, ok bool) {
	h, err := m.get(id)
	if err != nil {
		return "", "", false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.turnFrom, h.turnVia, h.turnFrom != ""
}

// AgentID is the agent's own id for hosted session id where it is another (Codex's thread, made
// after Iridium's id); "" otherwise. Discovery lists the session under it.
func (m *Manager) AgentID(id string) string {
	h, err := m.get(id)
	if err != nil {
		return ""
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if a := h.meta.agentID(); a != id {
		return a
	}
	return ""
}

// byAgentID is the hosted session whose agent knows it as id under another id of Iridium's.
func (m *Manager) byAgentID(id string) *Hosted {
	m.mu.Lock()
	hosted := make([]*Hosted, 0, len(m.hosted))
	for _, h := range m.hosted {
		hosted = append(hosted, h)
	}
	m.mu.Unlock()
	for _, h := range hosted {
		h.mu.Lock()
		a := h.meta.agentID()
		h.mu.Unlock()
		if a == id && h.id != id {
			return h
		}
	}
	return nil
}

// discoveredAs is the id discovery lists hosted session h under: the agent's own.
func (h *Hosted) discoveredAs() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if a := h.meta.agentID(); a != "" {
		return a
	}
	return h.id
}

// Create starts a hosted session in req.Cwd and, if req.Text is given, sends it as the first
// message from `from`. The id is the agent's own session id.
func (m *Manager) Create(ctx context.Context, req api.NewSession, from string) (string, error) {
	return m.create(ctx, req, from, "", nil, req.Caller == "")
}

// create is Create, the session named name (else after its folder); prep, if any, runs on the
// new id before its agent starts. byHand: put in req.Workspace as you put it (Own, if it has a
// launcher); else as discovery would, so one an agent started (ir new --workspace) still hangs
// under it.
func (m *Manager) create(ctx context.Context, req api.NewSession, from, name string, prep func(id string) error, byHand bool) (string, error) {
	if req.Agent == "" {
		req.Agent = api.AgentClaude
	}
	info, ok := agents.Get(req.Agent)
	if !ok {
		return "", fmt.Errorf("agent %s is not supported yet", req.Agent)
	}
	var machine string
	if req.Machine != "" {
		mc, err := m.remoteFor(ctx, req.Machine, info, req.Cwd)
		if err != nil {
			return "", err
		}
		machine = mc.ID()
	} else if m.Missing(req.Agent) {
		// Not installed: say so at once, not after the wait for an agent updating itself (start).
		return "", fmt.Errorf("%w: %s (%s is not on PATH)", ErrNotInstalled, info.Name, m.program(req.Agent))
	}
	if req.Mode != "" && !slices.Contains(info.Modes, req.Mode) {
		return "", fmt.Errorf("%s has no approval mode %q (%s)", info.Name, req.Mode, strings.Join(info.Modes, ", "))
	}
	cwd := req.Cwd
	if machine == "" {
		if st, err := os.Stat(req.Cwd); err != nil || !st.IsDir() || !filepath.IsAbs(req.Cwd) {
			return "", fmt.Errorf("not a directory: %q", req.Cwd)
		}
		cwd = filepath.Clean(req.Cwd)
	}
	if _, err := m.o.Store.Workspace(req.Workspace); req.Workspace != 0 && err != nil {
		return "", err
	}
	mt := meta{ID: newUUID(), Agent: req.Agent, Cwd: cwd, Created: time.Now().UnixMilli(), Model: req.Model, Mode: req.Mode, Launcher: req.Caller, Machine: machine}
	if h := m.byAgentID(req.Caller); req.Caller != "" && h != nil {
		mt.Launcher = h.id // a hosted Codex session calls itself by its thread
	}
	if req.Effort != "" {
		if len(info.Efforts) > 0 && !slices.Contains(info.Efforts, req.Effort) {
			return "", fmt.Errorf("%s has no effort %q (%s)", info.Name, req.Effort, strings.Join(info.Efforts, ", "))
		}
		if err := m.o.Store.UpdateSettings(mt.ID, func(s *store.Settings) { s.Effort = req.Effort }); err != nil {
			return "", err
		}
	}
	if req.Preset != "" {
		if _, err := m.checkPreset(ctx, req.Agent, machine, req.Preset); err != nil {
			return "", err
		}
		if err := m.o.Store.UpdateSettings(mt.ID, func(s *store.Settings) { s.Preset = req.Preset }); err != nil {
			return "", err
		}
	}
	if prep != nil {
		if err := prep(mt.ID); err != nil {
			return "", err
		}
	}
	h := m.newHosted(mt)
	if err := os.MkdirAll(h.dir, 0o700); err != nil {
		return "", err
	}
	if err := writeJSON(filepath.Join(h.dir, fileMeta), mt); err != nil {
		return "", err
	}
	m.nameNew(mt.ID, name)
	m.mu.Lock()
	m.hosted[mt.ID] = h
	m.mu.Unlock()
	h.newSession = true
	h.show() // Iridium's own: shown from the start
	if req.Workspace != 0 {
		if err := m.o.Store.Assign(mt.ID, req.Workspace, byHand); err != nil {
			return mt.ID, err
		}
	} else {
		m.intoGroup(mt.ID, groupPath(mt.Machine, mt.Cwd), req.Caller)
	}
	if err := h.start(ctx); err != nil {
		return mt.ID, err
	}
	if strings.TrimSpace(req.Text) != "" {
		if _, err := m.Send(ctx, mt.ID, req.Text, from); err != nil {
			return mt.ID, err
		}
	}
	return mt.ID, nil
}

// tagOf is the name terminal goes by in source tags now (Options.Tag), else its ID.
func (m *Manager) tagOf(terminal string) string {
	if m.o.Tag != nil {
		if t := m.o.Tag(terminal); t != "" {
			return t
		}
	}
	return terminal
}

const fromSep = "\x1f"

// From is a sender for Send and Create whose agent sees it by tag rather than by the terminal's
// own name (a plugin's "[QQ·张三]"): the terminal's ID still counts the message and its usage.
func From(terminal, tag string) string {
	if tag == "" {
		return terminal
	}
	return terminal + fromSep + tag
}

// FromLogin is From for a login of an app (not ir_web's): its message counted to the app, the
// login kept with it.
func FromLogin(terminal, login, tag string) string {
	if login == "" {
		return From(terminal, tag)
	}
	return terminal + fromSep + tag + fromSep + login
}

// viaOf is the session a message's tag says its agent sent it from ("ir:<session>"), else "".
func viaOf(tag string) string {
	id, _ := strings.CutPrefix(tag, "ir:")
	if id == tag {
		return ""
	}
	return id
}

// source splits a sender into the terminal's ID, the tag the agent sees and the login of the app
// it came from (if one).
func (m *Manager) source(from string) (terminal, tag, login string) {
	if t, g, ok := strings.Cut(from, fromSep); ok {
		g, login, _ = strings.Cut(g, fromSep)
		return t, g, login
	}
	return from, m.tagOf(from), ""
}

// Interrupt stops the running turn; the agent stays.
func (m *Manager) Interrupt(ctx context.Context, id string) error {
	h, err := m.get(id)
	if err != nil {
		return err
	}
	h.mu.Lock()
	live, quota := h.live, h.quotaUntil != 0
	h.quotaStop = h.quotaStop || quota // no "继续" when the quota is back; what waits still goes
	h.mu.Unlock()
	if quota && (!live || h.drv.State() == agent.Idle) {
		h.publish()
		return nil
	}
	if !live {
		return ErrNotLive
	}
	h.pause()
	return h.drv.Interrupt(ctx)
}

// Stop ends the session's agent: its stdin closes, and it exits once it has written its
// transcript. The session stays; the next message starts the agent again.
func (m *Manager) Stop(ctx context.Context, id string) error {
	h, err := m.get(id)
	if err != nil {
		return err
	}
	h.mu.Lock()
	h.quotaStop = h.quotaStop || h.quotaUntil != 0
	h.mu.Unlock()
	h.pause()
	return h.stop(ctx)
}

// List is the hosted sessions, then those the Discovery found that Iridium does not host,
// pinned first, then the most recent: by the key one was dragged to, else its last activity.
func (m *Manager) List(ctx context.Context) ([]api.Session, error) {
	over, err := m.o.Store.Overlays()
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	hosted := make([]*Hosted, 0, len(m.hosted))
	for _, h := range m.hosted {
		hosted = append(hosted, h)
	}
	m.mu.Unlock()
	vis := m.visibility()
	out := make([]api.Session, 0, len(hosted))
	seen := map[string]bool{}
	for _, h := range hosted {
		as := h.discoveredAs()
		out = append(out, m.elsewhere(h.summary(over[h.id], !h.shownIn(vis)), as))
		// A hosted Codex session is on disk under its thread: listed once, as hosted.
		seen[h.id], seen[as] = true, true
	}
	if m.o.Discovery != nil {
		found, err := m.o.Discovery.Sessions(ctx)
		if err != nil {
			m.o.Logf("session: discovery: %v", err)
		}
		for _, s := range found {
			if !seen[s.ID] {
				o := over[s.ID]
				s.Name, s.Pinned, s.Workspace, s.Color, s.Order = o.Name, o.Pinned, o.Workspace, api.Color(o.Color), o.Order
				s.Archived = !vis.Shown(string(s.Agent), s.ID)
				m.parentOf(&s)
				s.Own = ownOf(s, o)
				out = append(out, s)
			}
		}
	}
	slices.SortStableFunc(out, func(a, b api.Session) int {
		if a.Pinned != b.Pinned {
			if a.Pinned {
				return -1
			}
			return 1
		}
		return cmp.Compare(sortKey(b), sortKey(a))
	})
	return out, nil
}

// sortKey is what a session is sorted by in the list: where it was dragged to, else when it
// last worked.
func sortKey(s api.Session) float64 {
	if s.Order != 0 {
		return s.Order
	}
	return float64(s.UpdatedAt)
}

// State is the open session's carried state.
func (m *Manager) State(id string) (*api.SessionState, error) {
	h, err := m.get(id)
	if err != nil {
		st, err := m.foreignState(id, nil)
		if err == nil {
			m.warmModels(st.Agent, st.Machine)
		}
		return st, err
	}
	m.warmModels(h.meta.Agent, h.meta.Machine)
	over, _ := m.o.Store.Overlays()
	s := h.state(over[id], !h.shown())
	return &s, nil
}

// Messages is a page of the session's transcript: the latest (before and after < 0), the one
// before an offset, or what came after one.
func (m *Manager) Messages(id string, before, after int64) (api.MessagePage, error) {
	f, err := m.readable(id)
	if err != nil {
		return api.MessagePage{}, err
	}
	if f == nil {
		return api.MessagePage{Blocks: []api.Block{}}, nil
	}
	switch {
	case after >= 0:
		return f.After(after)
	case before >= 0:
		return f.Before(before)
	}
	return f.Latest()
}

// Full is the whole text of the block at at.
func (m *Manager) Full(id, at string) (string, error) {
	f, err := m.readable(id)
	if err != nil {
		return "", err
	}
	if f == nil {
		return "", ErrNotFound
	}
	return f.Full(at)
}

// provide makes a state the hub has not been given yet.
func (m *Manager) provide(key string) (any, bool) {
	switch {
	case key == "list":
		l, err := m.List(m.ctx)
		return l, err == nil
	case key == "quota":
		return m.quotaState(), true
	case strings.HasPrefix(key, "session:"):
		s, err := m.State(strings.TrimPrefix(key, "session:"))
		if err != nil {
			return nil, true
		}
		return s, true
	case strings.HasPrefix(key, "context:"):
		c, err := m.Context(strings.TrimPrefix(key, "context:"))
		if err != nil || c == nil {
			return nil, true
		}
		return c, true
	}
	return nil, false
}

// PublishList sends every stream the list again (a chat moved to another session).
func (m *Manager) PublishList() { m.publishList() }

func (m *Manager) publishList() {
	l, err := m.List(m.ctx)
	if err != nil {
		m.o.Logf("session: list: %v", err)
		return
	}
	m.o.Hub.Set("list", l)
}

// reap stops agents idle for longer than their session's idle reap, or the config's: no turn,
// no ask, no background task.
func (m *Manager) reap() {
	defer m.wg.Done()
	t := time.NewTicker(m.o.ReapEvery)
	defer t.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-t.C:
		}
		m.mu.Lock()
		hosted := make([]*Hosted, 0, len(m.hosted))
		for _, h := range m.hosted {
			hosted = append(hosted, h)
		}
		m.mu.Unlock()
		for _, h := range hosted {
			after := m.tuned().IdleReap
			if st, err := m.o.Store.Settings(h.id); err == nil && st.IdleReap != nil {
				after = *st.IdleReap
			}
			if idle, since := h.idleSince(); after > 0 && idle && time.Since(since) > after {
				m.o.Logf("session %s: idle since %s, stopping its agent", h.id, since.Format(time.TimeOnly))
				if err := h.stop(m.ctx); err != nil {
					m.o.Logf("session %s: %v", h.id, err)
				}
			}
		}
	}
}

// ErrNotInstalled: a new session's agent program is not there.
var ErrNotInstalled = harness.ErrNotInstalled

// program is the program agent a runs: the config's, else its own name.
func (m *Manager) program(a api.Agent) string {
	return cmp.Or(m.o.Config.Programs[string(a)], string(a))
}

// Missing: agent a's program is not on the agents' PATH, nor provided here otherwise.
func (m *Manager) Missing(a api.Agent) bool {
	if _, err := agent.LookPath(m.program(a), m.agentEnv()); err == nil {
		return false
	}
	p, ok := channelOf[harness.Provider](m, a)
	return !ok || !p.ProvidedHere()
}

// agentEnv is what agents start from: the daemon's environment, with the config's PATH and,
// where the config moves Claude's directory, CLAUDE_CONFIG_DIR. The driver drops what a parent
// session hands down.
func (m *Manager) agentEnv() []string {
	env := os.Environ()
	if p := m.o.Config.Path; p != "" {
		env = agent.SetEnv(env, "PATH", p)
	}
	if m.o.Config.ClaudeHome != "" {
		env = agent.SetEnv(env, "CLAUDE_CONFIG_DIR", m.o.Config.ClaudeHome)
	}
	return env
}

func (m *Manager) newDriver(a api.Agent, emit func(agent.Event)) agent.Driver {
	return agents.NewDriver(a, emit, m.o.Logf)
}

func newUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func writeJSON(path string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	// A name of its own: two writers of one file must not share a temporary file.
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(f.Name())
		return err
	}
	return os.Rename(f.Name(), path)
}
