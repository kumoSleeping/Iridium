package remote

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"sync"

	"github.com/kumoSleeping/iridium/internal/remote/wire"
)

// A command line run at a machine through run (wire.CapRun, docs/exec.md "命令"): no holder, no
// files; its output comes as it is written, and it ends with the connection.

// runQueue: the most output kept for a reader that does not keep up; past it the run is stopped.
const runQueue = 8 << 20

// RunEvent is one thing a run did: wrote Data (to its stderr if Err), or ended (Exit), or was cut
// off with its connection (Gone).
type RunEvent struct {
	Data string
	Err  bool
	Exit *wire.RunExit
	Gone bool
}

// Run is a command line running at a machine.
type Run struct {
	id string
	m  *Machine
	c  *wire.Conn

	mu     sync.Mutex
	q      []RunEvent
	queued int
	over   bool // its last event is queued
	kick   chan struct{}
	ended  chan struct{}
}

// Run starts line at the machine, in cwd ("" its home), with env (nil: its own): only at an
// executor that speaks run (Has(wire.CapRun)). Read it with Next till it ends; Stop ends it.
func (m *Machine) Run(ctx context.Context, line, cwd string, env []string) (*Run, error) {
	c, err := m.Conn()
	if err != nil {
		return nil, err
	}
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	r := &Run{id: "run-" + hex.EncodeToString(b), m: m, c: c, kick: make(chan struct{}, 1), ended: make(chan struct{})}
	m.mu.Lock()
	m.runs[r.id] = r
	m.mu.Unlock()
	if err := c.Call(ctx, "run", wire.RunParams{Run: r.id, Line: line, Cwd: cwd, Env: env}, nil); err != nil {
		r.forget()
		return nil, err
	}
	go func() {
		select {
		case <-c.Done():
			r.push(RunEvent{Gone: true})
		case <-r.ended:
		}
	}()
	return r, nil
}

// ID is the run's ID, as the executor knows it.
func (r *Run) ID() string { return r.id }

// Next is the run's next event, waiting for it; io.EOF after its last.
func (r *Run) Next(ctx context.Context) (RunEvent, error) {
	for {
		r.mu.Lock()
		if len(r.q) > 0 {
			ev := r.q[0]
			r.q = r.q[1:]
			r.queued -= len(ev.Data)
			r.mu.Unlock()
			return ev, nil
		}
		over := r.over
		r.mu.Unlock()
		if over {
			return RunEvent{}, io.EOF
		}
		select {
		case <-r.kick:
		case <-ctx.Done():
			return RunEvent{}, ctx.Err()
		}
	}
}

// Stop ends the run there (its whole tree); its run.exit follows.
func (r *Run) Stop(ctx context.Context) error {
	r.mu.Lock()
	over := r.over
	r.mu.Unlock()
	if over {
		return nil
	}
	return r.c.Call(ctx, "run.stop", wire.RunStopParams{Run: r.id}, nil)
}

func (r *Run) push(ev RunEvent) {
	last := ev.Exit != nil || ev.Gone
	r.mu.Lock()
	if r.over {
		r.mu.Unlock()
		return
	}
	stop := false
	if !last && r.queued+len(ev.Data) > runQueue {
		stop = true // not read: what comes after is dropped, and the run is ended
	} else {
		r.q = append(r.q, ev)
		r.queued += len(ev.Data)
	}
	if last {
		r.over = true
	}
	r.mu.Unlock()
	if last {
		r.forget()
	}
	if stop {
		go func() { _ = r.Stop(context.Background()) }()
	}
	select {
	case r.kick <- struct{}{}:
	default:
	}
}

func (r *Run) forget() {
	r.m.mu.Lock()
	if r.m.runs[r.id] == r {
		delete(r.m.runs, r.id)
		close(r.ended)
	}
	r.m.mu.Unlock()
}

// runEvent is a run.out or run.exit from the executor.
func (m *Machine) runEvent(name string, params json.RawMessage) {
	var p struct {
		Run string `json:"run"`
	}
	if json.Unmarshal(params, &p) != nil {
		return
	}
	m.mu.Lock()
	r := m.runs[p.Run]
	m.mu.Unlock()
	if r == nil {
		return
	}
	switch name {
	case "run.out":
		var e wire.RunOut
		if json.Unmarshal(params, &e) == nil && e.Data != "" {
			r.push(RunEvent{Data: e.Data, Err: e.Err})
		}
	case "run.exit":
		var e wire.RunExit
		if json.Unmarshal(params, &e) == nil {
			r.push(RunEvent{Exit: &e})
		}
	}
}
