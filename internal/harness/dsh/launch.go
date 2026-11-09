package dsh

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/kumoSleeping/iridium/internal/agent"
	"github.com/kumoSleeping/iridium/internal/harness"
	"github.com/kumoSleeping/iridium/internal/remote/wire"
)

// fileFiled, in a session's hold directory: it is filed under DSH Desktop's workspace of its
// folder there (or that dsh cannot file it).
const fileFiled = "remote-filed"

// Prepare gives a dsh launch what dsh takes at its start: its machine's models (the driver's
// catalog), and at another machine its presets there and DSH Desktop's models and providers
// (dsh.llm's patch).
func (c *Channel) Prepare(ctx context.Context, r harness.Ref, o *agent.SpawnOptions) error {
	if !ours(r) {
		return nil
	}
	machine := r.Machine
	o.Catalog = func() (agent.Catalog, error) { return c.Catalog(c.ctx, machine) }
	if o.Remote != nil {
		o.Remote.Presets = c.spawnPresets(ctx, machine)
		if mc := c.machine(machine); mc != nil {
			o.Remote.Patches = c.desktopLLM(ctx, mc)
		}
	}
	return nil
}

// Recorded asks once that a dsh session at another machine, its transcript there now, be filed
// under the workspace of its folder in the dsh the executor runs in (dsh.attach: DSH Desktop's
// sidebar has it then). dir is its hold directory, where that it was is noted.
func (c *Channel) Recorded(ctx context.Context, r harness.Ref, dir string) {
	if !ours(r) || r.Machine == "" {
		return
	}
	c.heldMu.Lock()
	if c.filing == nil {
		c.filing = map[string]bool{}
	}
	done := c.filing[r.ID]
	c.filing[r.ID] = true
	c.heldMu.Unlock()
	marker := filepath.Join(dir, fileFiled)
	if done {
		return
	}
	if _, err := os.Stat(marker); err == nil {
		return
	}
	mc := c.machine(r.Machine)
	if mc == nil {
		c.unfile(r.ID)
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(c.ctx, 30*time.Second)
		defer cancel()
		err := desktopAttach(ctx, mc, r.As, r.Cwd)
		if err != nil && !wire.IsCode(err, wire.CodeUnsupported) {
			c.o.Logf("session %s: filing it under DSH Desktop's workspace: %v", r.ID, err)
			c.unfile(r.ID) // the next look asks again
			return
		}
		if err := os.WriteFile(marker, nil, 0o600); err != nil {
			c.o.Logf("session %s: noting it filed: %v", r.ID, err)
		}
	}()
}

// unfile: session id's filing is to be asked for again.
func (c *Channel) unfile(id string) {
	c.heldMu.Lock()
	delete(c.filing, id)
	c.heldMu.Unlock()
}
