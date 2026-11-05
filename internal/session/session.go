// Package session hosts agent sessions: each runs through a holder (package hold) started by
// the platform's launcher, spoken to by its agent's driver, its history read from the agent's
// own transcript. It carries each session's small state to the hub and reaps idle agents.
//
// The session list is the disk's (plan "数据"): finding the sessions agents started by
// themselves is a Discovery's job; the Manager lists the ones it hosts.
package session

import (
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
	"time"

	"github.com/kumoSleeping/iridium/internal/agent"
	"github.com/kumoSleeping/iridium/internal/agent/claude"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/config"
	"github.com/kumoSleeping/iridium/internal/hub"
	"github.com/kumoSleeping/iridium/internal/platform"
	"github.com/kumoSleeping/iridium/internal/store"
)

// UnitPrefix names the holders' units; tests use iridium-test-.
const UnitPrefix = "iridium-hold-"

// SystemPrompt is appended to every hosted agent's system prompt (plan "ir").
const SystemPrompt = "用 ir 开别的 agent 会话、发消息、等结果、跨机器拷文件；见 ir --help\n"

// Discovery finds the sessions on disk that agents started by themselves (agents' record
// directories, ~/.claude/sessions/, /proc). Another package implements it.
type Discovery interface {
	Sessions(ctx context.Context) ([]api.Session, error)
}

type Options struct {
	Config     config.Config
	Store      *store.Store
	Hub        *hub.Hub
	Launcher   platform.Launcher
	Exe        string // the binary that runs `hold`; "" is this one
	UnitPrefix string // "" is UnitPrefix
	Discovery  Discovery
	Logf       func(string, ...any)
	ReapEvery  time.Duration // how often idle agents are looked for; 0 is 30s
	Poll       time.Duration // how often a live agent's out.log is read; 0 is 50ms
	// Tag names an interactive terminal (by ID) in the source tag "[name] "; nil or "" is the ID.
	Tag func(terminal string) string
}

var (
	ErrNotFound = errors.New("no such session")
	ErrNotLive  = errors.New("the session's agent is not running")
)

type Manager struct {
	o      Options
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu     sync.Mutex
	hosted map[string]*Hosted
	quota  map[api.Agent]api.AgentQuota
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
	if o.Poll == 0 {
		o.Poll = 50 * time.Millisecond
	}
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{o: o, ctx: ctx, cancel: cancel, hosted: map[string]*Hosted{}, quota: map[api.Agent]api.AgentQuota{}}
	o.Hub.Provide = m.provide
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
		if err := h.adopt(); err != nil {
			m.o.Logf("session %s: %v", mt.ID, err)
		}
	}
	m.wg.Add(1)
	go m.reap()
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

// Create starts a hosted session in req.Cwd and, if req.Text is given, sends it as the first
// message from `from`. The id is the agent's own session id.
func (m *Manager) Create(ctx context.Context, req api.NewSession, from string) (string, error) {
	if req.Agent == "" {
		req.Agent = api.AgentClaude
	}
	if req.Agent != api.AgentClaude {
		return "", fmt.Errorf("agent %s is not supported yet", req.Agent)
	}
	if st, err := os.Stat(req.Cwd); err != nil || !st.IsDir() || !filepath.IsAbs(req.Cwd) {
		return "", fmt.Errorf("not a directory: %q", req.Cwd)
	}
	mt := meta{ID: newUUID(), Agent: req.Agent, Cwd: filepath.Clean(req.Cwd), Created: time.Now().UnixMilli(), Model: req.Model, Mode: req.Mode}
	h := m.newHosted(mt)
	if err := os.MkdirAll(h.dir, 0o700); err != nil {
		return "", err
	}
	if err := writeJSON(filepath.Join(h.dir, fileMeta), mt); err != nil {
		return "", err
	}
	m.mu.Lock()
	m.hosted[mt.ID] = h
	m.mu.Unlock()
	h.newSession = true
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

// Send gives the session a message from `from` (an interactive terminal's ID), starting its
// agent if it is not running: a new turn when idle, else it joins the running one. The agent sees
// "[name] " before it (the terminal's name, Options.Tag), unless it is a slash command, which must
// reach the agent as typed.
func (m *Manager) Send(ctx context.Context, id, text, from string) (string, error) {
	h, err := m.get(id)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(text) == "" {
		return "", errors.New("an empty message")
	}
	tag := from
	if m.o.Tag != nil {
		if t := m.o.Tag(from); t != "" {
			tag = t
		}
	}
	input := newUUID()
	if err := m.o.Store.AddInput(store.Input{ID: input, Session: id, Terminal: from, Tag: tag, Text: text, State: "sent"}); err != nil {
		return "", err
	}
	if err := h.start(ctx); err != nil {
		return "", err
	}
	line := text
	if !strings.HasPrefix(text, "/") {
		line = "[" + tag + "] " + text
	}
	h.mu.Lock()
	h.lastFrom = from
	ready := h.ready
	h.mu.Unlock()
	// The process says its use so far first, so this turn's share can be told from it.
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
	case <-ctx.Done():
		return "", ctx.Err()
	}
	return input, h.drv.Send(ctx, agent.Message{ID: input, Text: line, Human: true})
}

// Interrupt stops the running turn; the agent stays.
func (m *Manager) Interrupt(ctx context.Context, id string) error {
	h, err := m.get(id)
	if err != nil {
		return err
	}
	if !h.isLive() {
		return ErrNotLive
	}
	return h.drv.Interrupt(ctx)
}

// Stop ends the session's agent: its stdin closes, and it exits once it has written its
// transcript. The session stays; the next message starts the agent again.
func (m *Manager) Stop(ctx context.Context, id string) error {
	h, err := m.get(id)
	if err != nil {
		return err
	}
	return h.stop(ctx)
}

func (m *Manager) Answer(ctx context.Context, id string, a api.Answer) error {
	h, err := m.get(id)
	if err != nil {
		return err
	}
	if !h.isLive() {
		return ErrNotLive
	}
	return h.drv.Answer(ctx, a)
}

// List is the hosted sessions, then those the Discovery found that Iridium does not host,
// pinned first, then the most recent.
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
	out := make([]api.Session, 0, len(hosted))
	seen := map[string]bool{}
	for _, h := range hosted {
		out = append(out, h.summary(over[h.id]))
		seen[h.id] = true
	}
	if m.o.Discovery != nil {
		found, err := m.o.Discovery.Sessions(ctx)
		if err != nil {
			m.o.Logf("session: discovery: %v", err)
		}
		for _, s := range found {
			if !seen[s.ID] {
				o := over[s.ID]
				s.Name, s.Pinned, s.Archived, s.Workspace = o.Name, o.Pinned, o.Archived, o.Workspace
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
		return int(b.UpdatedAt - a.UpdatedAt)
	})
	return out, nil
}

// State is the open session's carried state.
func (m *Manager) State(id string) (*api.SessionState, error) {
	h, err := m.get(id)
	if err != nil {
		return nil, err
	}
	over, _ := m.o.Store.Overlays()
	s := h.state(over[id])
	return &s, nil
}

// Messages is a page of the session's transcript: the latest (before and after < 0), the one
// before an offset, or what came after one.
func (m *Manager) Messages(id string, before, after int64) (api.MessagePage, error) {
	h, err := m.get(id)
	if err != nil {
		return api.MessagePage{}, err
	}
	f := h.transcript()
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
	h, err := m.get(id)
	if err != nil {
		return "", err
	}
	f := h.transcript()
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
	}
	return nil, false
}

func (m *Manager) publishList() {
	l, err := m.List(m.ctx)
	if err != nil {
		m.o.Logf("session: list: %v", err)
		return
	}
	m.o.Hub.Set("list", l)
}

func (m *Manager) setQuota(a api.Agent, windows []api.QuotaWindow) {
	m.mu.Lock()
	m.quota[a] = api.AgentQuota{Agent: a, Windows: windows, At: time.Now().UnixMilli()}
	m.mu.Unlock()
	m.o.Hub.Set("quota", m.quotaState())
}

func (m *Manager) quotaState() api.Quota {
	m.mu.Lock()
	defer m.mu.Unlock()
	q := api.Quota{Agents: []api.AgentQuota{}}
	for _, a := range m.quota {
		q.Agents = append(q.Agents, a)
	}
	slices.SortFunc(q.Agents, func(a, b api.AgentQuota) int { return strings.Compare(string(a.Agent), string(b.Agent)) })
	return q
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
			after := m.o.Config.IdleReap.D()
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
	// One kind for now; the others come with their drivers.
	return claude.New(claude.Options{Emit: emit, Logf: m.o.Logf})
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
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
