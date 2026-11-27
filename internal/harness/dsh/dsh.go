// Package dsh is dsh's own channel (internal/harness): what Iridium does for dsh alone. DSH
// Desktop holding a session at an executor's machine (held.go: its live state, a message given to
// its agent, a setting set there), the models dsh offers where it runs (catalog.go), its agent
// presets (presets.go), what a launch there gets from Desktop (launch.go), and the pictures its
// sessions' records name by attachment (image.go). The core asks it
// through harness's capabilities and never names dsh; it speaks to the dsh plugin at an executor
// (plugins/dsh-remote-iridium) in held.* and dsh.* (docs/exec.md "远端 DSH").
package dsh

import (
	"context"
	"encoding/json"
	"os"
	"sync"

	"github.com/kumoSleeping/iridium/internal/agent"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/harness"
	"github.com/kumoSleeping/iridium/internal/remote"
	"github.com/kumoSleeping/iridium/internal/transcript"
)

// Options are what the channel needs besides its Host.
type Options struct {
	Remote   *remote.Hub // nil: no executors served
	StateDir string      // where verified Desktop install paths are persisted
	Headless *Headless   // optional override; if nil and StateDir != "", created automatically
	Logf     func(string, ...any)

	// EnsureHeadless, when set, overrides Headless.Ensure for tests.
	EnsureHeadless func(ctx context.Context) (string, bool)

	// Discovery hooks for per-computer "sync new sessions" and on-demand session adopt.
	GetSync func(machine string) bool
	SetSync func(machine string, on bool)
	Adopt   func(ctx context.Context, machine string, s DesktopSession) error

	// Optional event hooks (called off the WS reader goroutine).
	OnWorkspaces func(machine string, params json.RawMessage)
	OnSessions   func(machine string, params json.RawMessage)
	OnConfig     func(machine string, cfg DesktopConfigEvent)
}

// Channel is dsh's channel.
type Channel struct {
	o    Options
	ctx  context.Context
	stop context.CancelFunc

	hostMu sync.Mutex
	host   harness.Host

	headless *Headless
	localMu  sync.Mutex
	localUp  map[string]bool // local CapInproc machine ID -> headless flag

	presetMu sync.Mutex
	presets  map[string]presetList    // by machine
	catalogs map[string]*catalogEntry // by machine

	keyMu sync.Mutex
	keys  map[string]startKeys // what desktop.config last told (keys.go), by executor id

	imageMu    sync.Mutex
	images     map[string][]byte // pictures read (image.go), by sha256
	imageOrder []string          // their sums, oldest first
	imageBytes int
}

var (
	_ harness.Stater          = (*Channel)(nil)
	_ harness.Launcher        = (*Channel)(nil)
	_ harness.LocalHost       = (*Channel)(nil)
	_ harness.Provider        = (*Channel)(nil)
	_ harness.Models          = (*Channel)(nil)
	_ harness.Presets         = (*Channel)(nil)
	_ harness.Syncer          = (*Channel)(nil)
	_ harness.ComputerStater  = (*Channel)(nil)
	_ harness.SessionSearcher = (*Channel)(nil)
	_ harness.StartKeyer      = (*Channel)(nil)
)

// New is dsh's channel; it registers event handlers on o.Remote for DSH Desktop updates.
func New(o Options) *Channel {
	if o.Logf == nil {
		o.Logf = func(string, ...any) {}
	}
	ctx, stop := context.WithCancel(context.Background())
	c := &Channel{o: o, ctx: ctx, stop: stop, localUp: map[string]bool{}}
	if o.Headless != nil {
		c.headless = o.Headless
	} else if o.StateDir != "" && o.Remote != nil {
		c.headless = NewHeadless(HeadlessOptions{
			Find: func() (string, bool) {
				inst, ok := DiscoverDesktop(DesktopDiscoverOptions{
					StateDir:   o.StateDir,
					ManualPath: LoadManualDesktopPath(o.StateDir),
				})
				return inst.Exe, ok
			},
			Home: transcript.DshHome(os.Environ()),
			Yield: func(ctx context.Context) error {
				mc := o.Remote.LocalHeadless(CapInproc)
				if mc == nil {
					return nil
				}
				_, err := desktopFreeze(ctx, mc)
				return err
			},
			Busy: c.localHeadlessBusy,
			Logf: o.Logf,
		})
	}
	if o.Remote != nil {
		o.Remote.Handle(eventDesktopModelsChanged, c.onModelsChanged)
		o.Remote.Handle(eventDesktopPresetsChanged, c.onPresetsChanged)
		o.Remote.Handle(eventDesktopWorkspacesChanged, c.onWorkspacesChanged)
		o.Remote.Handle(eventDesktopSessionsChanged, c.onSessionsChanged)
		o.Remote.Handle(eventDesktopConfig, c.onDesktopConfig)
		o.Remote.OnChanged(c.onMachineChanged)
	}
	return c
}

func (c *Channel) localHeadlessBusy() bool {
	h := c.h()
	if h == nil {
		return false
	}
	for _, id := range h.Sessions(api.AgentDsh, "") {
		if r, ok := h.Ref(id); ok && r.Live {
			return true
		}
	}
	return false
}

func (c *Channel) onMachineChanged(id string) {
	if c.o.Remote == nil {
		return
	}
	mc := c.o.Remote.Machine(id)
	if mc == nil {
		return
	}
	hi, online := mc.Hello()
	if !online {
		c.dropKeys(id)
	}
	if !mc.IsLocal() {
		return
	}
	if online && mc.Has(CapInproc) {
		c.localMu.Lock()
		_, seen := c.localUp[id]
		c.localUp[id] = hi.Headless
		c.localMu.Unlock()
		if !seen && c.headless != nil {
			go c.headless.Hello(hi.Headless)
		}
		if c.o.StateDir != "" {
			if ai, ok := mc.HostAgent(string(api.AgentDsh)); ok && len(ai.Argv) > 0 {
				if inst, ok := DesktopInstallFromArgv("", ai.Argv); ok {
					inst.Source = "hello"
					go func() { _ = SaveDesktopInstall(c.o.StateDir, inst) }()
				}
			}
		}
		return
	}
	c.localMu.Lock()
	wasHeadless, existed := c.localUp[id]
	if existed {
		delete(c.localUp, id)
	}
	c.localMu.Unlock()
	if existed && c.headless != nil {
		go c.headless.Bye(wasHeadless)
	}
}

// machineKey maps a remote.Hub machine ID to the channel's cache key ("" for a local executor).
func (c *Channel) machineKey(id string) string {
	if c.o.Remote != nil {
		if mc := c.o.Remote.Machine(id); mc != nil && mc.IsLocal() {
			return ""
		}
	}
	return id
}

func (c *Channel) onModelsChanged(machine string, params json.RawMessage) {
	key := c.machineKey(machine)
	var rep DesktopModelsReply
	if len(params) > 0 && json.Unmarshal(params, &rep) == nil && len(rep.Models) > 0 {
		c.putCatalog(key, rep.toCatalog())
		go c.refreshMachine(key)
		return
	}
	c.invalidateCatalog(key)
	go func() {
		if _, err := c.catalog(c.ctx, key, true); err == nil {
			c.refreshMachine(key)
		}
	}()
}

func (c *Channel) onPresetsChanged(machine string, _ json.RawMessage) {
	key := c.machineKey(machine)
	c.presetMu.Lock()
	delete(c.presets, key)
	c.presetMu.Unlock()
}

func (c *Channel) onWorkspacesChanged(machine string, params json.RawMessage) {
	if c.o.OnWorkspaces != nil {
		go c.o.OnWorkspaces(c.machineKey(machine), params)
	}
}

func (c *Channel) onSessionsChanged(machine string, params json.RawMessage) {
	if c.o.OnSessions != nil {
		go c.o.OnSessions(c.machineKey(machine), params)
	}
}

func (c *Channel) onDesktopConfig(machine string, params json.RawMessage) {
	var cfg DesktopConfigEvent
	if json.Unmarshal(params, &cfg) != nil {
		return
	}
	key := c.machineKey(machine)
	if c.putKeys(machine, key, cfg.Keys) {
		// The sessions there not started yet show the keys as now told.
		if h := c.h(); h != nil {
			go func() {
				for _, id := range h.Sessions(api.AgentDsh, key) {
					h.ShowAgain(id)
				}
			}()
		}
	}
	if key == "" && c.o.StateDir != "" {
		go c.persistDesktopPath(cfg)
	}
	if c.o.OnConfig != nil {
		go c.o.OnConfig(key, cfg)
	}
}

func (c *Channel) persistDesktopPath(cfg DesktopConfigEvent) {
	var inst DesktopInstall
	switch {
	case cfg.Exe != "" && cfg.HostEntry != "":
		inst = DesktopInstall{
			Exe:        cfg.Exe,
			HostEntry:  cfg.HostEntry,
			RuntimeDir: cfg.RuntimeDir,
			Scope:      cfg.Scope,
			Source:     "hello",
		}
	case cfg.DesktopPath != "":
		if resolved, ok := ResolveDesktopRoot(cfg.DesktopPath); ok {
			inst = resolved
			inst.Source = "hello"
		}
	}
	if inst.Valid() {
		_ = SaveDesktopInstall(c.o.StateDir, inst)
	}
}

func (c *Channel) Agent() api.Agent { return api.AgentDsh }

// Bind is the core the channel asks.
func (c *Channel) Bind(h harness.Host) {
	c.hostMu.Lock()
	c.host = h
	c.hostMu.Unlock()
}

// Close stops what the channel does in the background.
func (c *Channel) Close() {
	c.stop()
	if c.headless != nil {
		c.headless.Close()
	}
}

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
