// Package dsh is dsh's own channel (internal/harness): what Iridium does for dsh alone. DSH
// Desktop holding a session at an executor's machine (held.go: its live state, a message given to
// its agent, a setting set there), the models dsh offers where it runs (catalog.go), its agent
// presets (presets.go), and what a launch there gets from Desktop (launch.go). The core asks it
// through harness's capabilities and never names dsh; it speaks to the dsh plugin at an executor
// (plugins/dsh-remote-iridium) in held.* and dsh.* (docs/exec.md "远端 DSH").
package dsh

import (
	"context"
	"sync"

	"github.com/kumoSleeping/iridium/internal/agent"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/harness"
	"github.com/kumoSleeping/iridium/internal/remote"
)

// Options are what the channel needs besides its Host.
type Options struct {
	Remote *remote.Hub // nil: no executors served
	Logf   func(string, ...any)
}

// Channel is dsh's channel.
type Channel struct {
	o    Options
	ctx  context.Context
	stop context.CancelFunc

	hostMu sync.Mutex
	host   harness.Host

	heldMu sync.Mutex
	helds  map[string]*heldEntry // by session (Iridium's id)
	many   map[string]bool       // machines told of more than maxFeatures on a session
	told   []heldTold            // held.changed events not taken in yet
	taking bool                  // a goroutine takes them in
	filing map[string]bool       // sessions whose filing in Desktop's workspace is asked for (launch.go)

	presetMu sync.Mutex
	presets  map[string]presetList    // by machine
	catalogs map[string]*catalogEntry // by machine
}

var (
	_ harness.Stater   = (*Channel)(nil)
	_ harness.Sender   = (*Channel)(nil)
	_ harness.Setter   = (*Channel)(nil)
	_ harness.Retuner  = (*Channel)(nil)
	_ harness.Launcher = (*Channel)(nil)
	_ harness.Models   = (*Channel)(nil)
	_ harness.Presets  = (*Channel)(nil)
	_ harness.Syncer   = (*Channel)(nil)
)

// New is dsh's channel; it takes held.changed from the executors of o.Remote.
func New(o Options) *Channel {
	if o.Logf == nil {
		o.Logf = func(string, ...any) {}
	}
	ctx, stop := context.WithCancel(context.Background())
	c := &Channel{o: o, ctx: ctx, stop: stop, helds: map[string]*heldEntry{}, many: map[string]bool{}}
	if o.Remote != nil {
		o.Remote.Handle(eventHeldChanged, c.onHeld)
	}
	return c
}

func (c *Channel) Agent() api.Agent { return api.AgentDsh }

// Bind is the core the channel asks.
func (c *Channel) Bind(h harness.Host) {
	c.hostMu.Lock()
	c.host = h
	c.hostMu.Unlock()
}

// Close stops what the channel does in the background.
func (c *Channel) Close() { c.stop() }

// h is the host; nil before Bind.
func (c *Channel) h() harness.Host {
	c.hostMu.Lock()
	defer c.hostMu.Unlock()
	return c.host
}

// machine is executor id, connected now; nil if not.
func (c *Channel) machine(id string) *remote.Machine {
	if c.o.Remote == nil || id == "" {
		return nil
	}
	mc := c.o.Remote.Find(id)
	if mc == nil {
		return nil
	}
	if _, online := mc.Hello(); !online {
		return nil
	}
	return mc
}

// ours: r is a dsh session.
func ours(r harness.Ref) bool { return r.Agent == api.AgentDsh }

// spawnPresets is a launch's Presets: the machine's, as the page lists them.
func (c *Channel) spawnPresets(ctx context.Context, machine string) func() ([]agent.Preset, string, error) {
	return func() ([]agent.Preset, string, error) { return c.Presets(ctx, machine) }
}
