package session

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/kumoSleeping/iridium/internal/agent"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/transcript"
)

// What the carried states hold beyond the session's own fields (plan "实时数据（携带）"): the
// newest blocks while they are small, the context's size, and the quota windows per agent and
// account, kept with their age when no process of theirs runs.

const (
	fileContext = "context.json" // the last get_context_usage answer
	fileQuota   = "quota.json"   // in the state directory: the last windows per agent and account
	// TailBytes: the most of the transcript a tail is read from; TailJSON: the most a tail's
	// blocks may weigh. Past either the page fetches.
	TailBytes = 256 << 10
	TailJSON  = 16 << 10
	// tailSpan: a tail reaches back at least this far, past the states a page may have skipped.
	tailSpan = time.Second
)

// updateTail sets the tail after the transcript grew from old to size.
func (h *Hosted) updateTail(f *transcript.File, old, size int64) {
	now := time.Now()
	h.mu.Lock()
	if h.tailAt.IsZero() || now.Sub(h.tailAt) > tailSpan || h.tailFrom > old {
		h.tailFrom, h.tailAt = old, now
	}
	from := h.tailFrom
	h.mu.Unlock()
	t := &api.BlockTail{Start: from, End: size}
	if size-from <= TailBytes {
		if p, err := f.After(from); err == nil && p.End == size && small(p.Blocks) {
			t.Blocks = p.Blocks
			// The files they name, as a page of messages has them (Manager.SetAnnotate).
			if a := h.m.annotate.Load(); a != nil {
				(*a)(h.id, h.meta.Cwd, t.Blocks)
			}
		}
	}
	h.mu.Lock()
	h.tail = t
	h.mu.Unlock()
}

// Annotate fills in blocks' Files (files.Service.AnnotateBlocks): what they name that exists.
type Annotate func(session, cwd string, blocks []api.Block)

// SetAnnotate makes the tails of the sessions' states carry the files their blocks name, as a
// page of messages does. The server sets it; it comes after the manager.
func (m *Manager) SetAnnotate(a Annotate) { m.annotate.Store(&a) }

// small: no block cut short, and little to carry.
func small(blocks []api.Block) bool {
	for _, b := range blocks {
		if b.Length > 0 || (b.Result != nil && b.Result.Length > 0) {
			return false
		}
	}
	j, err := json.Marshal(blocks)
	return err == nil && len(j) <= TailJSON
}

// ctxEvery: within a turn, the context is read again on a tool call this long after the last read.
const ctxEvery = 30 * time.Second

// refreshContext asks the agent how full its context is (Claude: get_context_usage): at the end of
// each turn, after a compaction, and within a turn on a tool call at most every ctxEvery. The answer is kept beside the holder's files, so it is there
// when no process runs.
func (h *Hosted) refreshContext() {
	ctx, cancel := context.WithTimeout(h.m.ctx, 10*time.Second)
	defer cancel()
	c, err := h.drv.Context(ctx)
	if err != nil {
		return
	}
	if err := writeJSON(filepath.Join(h.dir, fileContext), c); err != nil {
		h.logf("context: %v", err)
	}
	h.mu.Lock()
	h.ctxSum = &api.ContextSummary{Used: c.Used, Max: c.Max, At: c.At}
	h.mu.Unlock()
	h.m.o.Hub.Set("context:"+h.id, c)
	h.publish()
}

// savedContext is the last context read, nil if none.
func (h *Hosted) savedContext() *api.Context {
	b, err := os.ReadFile(filepath.Join(h.dir, fileContext))
	if err != nil {
		return nil
	}
	var c api.Context
	if json.Unmarshal(b, &c) != nil {
		return nil
	}
	return &c
}

// Context is the session's context as last read; nil if never.
func (m *Manager) Context(id string) (*api.Context, error) {
	h, err := m.get(id)
	if err != nil {
		return nil, err
	}
	return h.savedContext(), nil
}

func (h *Hosted) refreshQuota() {
	ctx, cancel := context.WithTimeout(h.m.ctx, 10*time.Second)
	defer cancel()
	if u, err := h.drv.Quota(ctx); err == nil && len(u.Windows) > 0 {
		h.setQuota(u.Windows, nil)
	}
	h.refreshBalance(ctx)
}

// refreshBalance reads the account's money, for a driver that has it (dsh); true if it did.
func (h *Hosted) refreshBalance(ctx context.Context) bool {
	b, ok := h.drv.(agent.Balancer)
	if !ok {
		return false
	}
	got, err := b.Balance(ctx)
	if err != nil || len(got) == 0 {
		return false
	}
	h.setQuota(nil, got)
	return true
}

func (h *Hosted) setQuota(w []api.QuotaWindow, balance []api.Balance) {
	h.mu.Lock()
	account, plan := h.account, h.plan
	h.mu.Unlock()
	h.m.setQuota(h.meta.Agent, account, plan, w, balance)
}

func quotaKey(a api.Agent, account string) string { return string(a) + "\x00" + account }

func (m *Manager) setQuota(a api.Agent, account, plan string, windows []api.QuotaWindow, balance []api.Balance) {
	m.mu.Lock()
	m.quota[quotaKey(a, account)] = api.AgentQuota{Agent: a, Account: account, Plan: plan, Windows: windows, Balance: balance, At: time.Now().UnixMilli()}
	saved := make([]api.AgentQuota, 0, len(m.quota))
	for _, q := range m.quota {
		saved = append(saved, q)
	}
	m.mu.Unlock()
	if err := writeJSON(filepath.Join(m.o.Config.StatePath(), fileQuota), saved); err != nil {
		m.o.Logf("session: quota: %v", err)
	}
	m.publishQuota()
}

func (m *Manager) loadQuota() {
	b, err := os.ReadFile(filepath.Join(m.o.Config.StatePath(), fileQuota))
	if err != nil {
		return
	}
	var saved []api.AgentQuota
	if json.Unmarshal(b, &saved) != nil {
		return
	}
	m.mu.Lock()
	for _, q := range saved {
		m.quota[quotaKey(q.Agent, q.Account)] = q
	}
	m.mu.Unlock()
}

func (m *Manager) publishQuota() { m.o.Hub.Set("quota", m.quotaState()) }

// quotaState: the windows per agent and account; live where a process of theirs runs.
func (m *Manager) quotaState() api.Quota {
	live := map[string]bool{}
	for _, h := range m.all() {
		h.mu.Lock()
		if h.live {
			live[quotaKey(h.meta.Agent, h.account)] = true
		}
		h.mu.Unlock()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	q := api.Quota{Agents: []api.AgentQuota{}}
	for k, a := range m.quota {
		a.Live = live[k]
		q.Agents = append(q.Agents, a)
	}
	slices.SortFunc(q.Agents, func(a, b api.AgentQuota) int {
		return strings.Compare(quotaKey(a.Agent, a.Account), quotaKey(b.Agent, b.Account))
	})
	return q
}

// QuotaOf is the windows of the session's agent and account, nil if none were read.
func (m *Manager) QuotaOf(id string) (*api.AgentQuota, error) {
	h, err := m.get(id)
	if err != nil {
		return nil, err
	}
	h.mu.Lock()
	k := quotaKey(h.meta.Agent, h.account)
	h.mu.Unlock()
	for _, q := range m.quotaState().Agents {
		if quotaKey(q.Agent, q.Account) == k {
			return &q, nil
		}
	}
	return nil, nil
}

// quotaLoop reads the windows again every QuotaEvery, through one running process of each agent
// and account.
func (m *Manager) quotaLoop() {
	defer m.wg.Done()
	t := time.NewTicker(m.o.QuotaEvery)
	defer t.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-t.C:
		}
		seen := map[string]bool{}
		for _, h := range m.all() {
			h.mu.Lock()
			k, ok := quotaKey(h.meta.Agent, h.account), h.live && !h.starting
			h.mu.Unlock()
			if ok && !seen[k] {
				seen[k] = true
				go h.refreshQuota()
			}
		}
	}
}

func (m *Manager) all() []*Hosted {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*Hosted, 0, len(m.hosted))
	for _, h := range m.hosted {
		out = append(out, h)
	}
	return out
}
