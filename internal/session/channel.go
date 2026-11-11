package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/harness"
	"github.com/kumoSleeping/iridium/internal/hold"
	"github.com/kumoSleeping/iridium/internal/platform"
)

// A harness's own code is its channel (internal/harness): the core asks it by the session's
// agent, for a capability, and never names the agent. The manager is its Host.

// channelOf is agent a's channel as capability T, if it has one.
func channelOf[T any](m *Manager, a api.Agent) (T, bool) {
	return harness.As[T](m.o.Channels, a)
}

// ref is session id (Iridium's, or the agent's own) as a channel sees it.
func (m *Manager) ref(id string) (harness.Ref, bool) {
	if h := m.byAgentID(id); h != nil {
		id = h.id
	}
	if h, err := m.get(id); err == nil {
		return h.ref(), true
	}
	s, _, ok := m.found(id)
	if !ok {
		return harness.Ref{}, false
	}
	return harness.Ref{ID: id, As: id, Agent: s.Agent, Machine: s.Machine, Cwd: s.Cwd, Found: true, Status: s.Status}, true
}

// ref is h as a channel sees it.
func (h *Hosted) ref() harness.Ref {
	h.mu.Lock()
	r := h.refLocked()
	h.mu.Unlock()
	if s, _, ok := h.m.found(r.As); ok {
		r.Found, r.Status = true, s.Status
	}
	return r
}

// refLocked is h as a channel sees it, but for discovery's (Found, Status); h.mu held.
func (h *Hosted) refLocked() harness.Ref {
	return harness.Ref{ID: h.id, As: cmpOr(h.meta.agentID(), h.id), Agent: h.meta.Agent, Machine: h.meta.Machine, Cwd: h.meta.Cwd, Hosted: true, Live: h.live}
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// channelState adds to s what the session's channel knows of it (r's).
func (m *Manager) channelState(r harness.Ref, s *api.SessionState) {
	if st, ok := channelOf[harness.Stater](m, r.Agent); ok {
		st.State(r, s)
	}
	m.attachedState(r.ID, s)
}

// host is the manager as its channels' Host.
type host struct{ m *Manager }

func (x host) Ref(id string) (harness.Ref, bool) { return x.m.ref(id) }
func (x host) ShowAgain(id string)               { x.m.showAgain(id) }
func (x host) Lang() string                      { return x.m.tuned().Lang }
func (x host) Program(a api.Agent) string        { return x.m.program(a) }
func (x host) AgentEnv() []string                { return x.m.agentEnv() }
func (x host) Refresh(ids []string)              { x.m.Refresh(ids) }
func (x host) Logf(format string, args ...any)   { x.m.o.Logf(format, args...) }

// HeldElsewhere is discovery's, where it can tell (a process holds the session, idle or not);
// else whether it is found running elsewhere.
func (x host) HeldElsewhere(as string) bool {
	f := x.m.finder()
	if f == nil {
		return false
	}
	if h, ok := f.(interface{ HeldElsewhere(string) bool }); ok {
		return h.HeldElsewhere(as)
	}
	s, _, ok := f.Found(as)
	return ok && s.Status == api.StatusElsewhere
}

func (x host) Fetch(ctx context.Context, as string) {
	if f, ok := x.m.o.Discovery.(fetcher); ok {
		_ = f.Fetch(ctx, as)
	}
}

// Sessions are the sessions of agent a at machine, hosted or found.
func (x host) Sessions(a api.Agent, machine string) []string {
	m := x.m
	var ids []string
	if d := m.o.Discovery; d != nil {
		if l, err := d.Sessions(m.ctx); err == nil {
			for _, s := range l {
				if s.Agent == a && s.Machine == machine {
					ids = append(ids, s.ID)
				}
			}
		}
	}
	m.mu.Lock()
	for id, h := range m.hosted {
		if h.meta.Agent == a && h.meta.Machine == machine {
			ids = append(ids, id)
		}
	}
	m.mu.Unlock()
	return ids
}

// Rescan has discovery look again now at machine ("" this one): here, or at an executor.
func (x host) Rescan(ctx context.Context, machine string) error {
	sc, ok := x.m.o.Discovery.(scanner)
	if !ok {
		return nil
	}
	if machine == "" {
		sc.Scan()
		return nil
	}
	return sc.ScanMachine(ctx, machine)
}

// scanner is a Discovery that looks again now: here, or at a machine.
type scanner interface {
	Scan()
	ScanMachine(ctx context.Context, id string) error
}

// RunOnce runs r in a holder unit of its own, here or at machine, waits for its end (stopping it
// when ctx ends) and gives what it printed.
func (x host) RunOnce(ctx context.Context, machine string, r harness.Run) ([]byte, []byte, error) {
	m := x.m
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	unit := m.o.UnitPrefix + "once-" + hex.EncodeToString(b)
	dir := m.HoldDir(unit)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(dir)
	var launcher platform.Launcher = m.o.Launcher
	if machine != "" {
		if m.o.Remote == nil {
			return nil, nil, fmt.Errorf("no machine %q", machine)
		}
		mc := m.o.Remote.Find(machine)
		if mc == nil {
			return nil, nil, fmt.Errorf("no machine %q", machine)
		}
		if _, ok := mc.Hello(); !ok {
			return nil, nil, ErrOffline
		}
		launcher = mc.Launcher()
	}
	spec := hold.Spec{Argv: r.Argv, Env: r.Env, Cwd: r.Cwd, StopGraceMS: 1000}
	if err := hold.Prepare(dir, spec); err != nil {
		return nil, nil, err
	}
	u := platform.Unit{
		Name:        unit,
		Argv:        []string{m.o.Exe, "hold", dir},
		StopPost:    []string{m.o.Exe, "hold", "--stop-post", dir},
		StopTimeout: 10 * time.Second,
	}
	if err := launcher.Start(ctx, u); err != nil {
		return nil, nil, err
	}
	for {
		if e, _ := hold.ReadExit(dir); e != nil {
			break
		}
		select {
		case <-ctx.Done():
			stop, done := context.WithTimeout(context.Background(), 15*time.Second)
			_ = launcher.Stop(stop, unit)
			done()
			return nil, nil, errors.New("it did not end")
		case <-time.After(200 * time.Millisecond):
		}
	}
	out, _ := os.ReadFile(filepath.Join(dir, hold.FileOutLog))
	errOut, _ := os.ReadFile(filepath.Join(dir, hold.FileErrLog))
	return out, errOut, nil
}

var _ harness.Host = host{}

// showAgain publishes session id's state again, hosted or found.
func (m *Manager) showAgain(id string) {
	if m.ctx.Err() != nil {
		return
	}
	if h, err := m.get(id); err == nil {
		h.publish()
		return
	}
	if st, err := m.foreignState(id, nil); err == nil {
		m.o.Hub.Set("session:"+id, st)
	}
}
