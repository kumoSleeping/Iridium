package dsh

import (
	"maps"
	"reflect"
	"slices"

	"github.com/kumoSleeping/iridium/internal/api"
)

// Start keys (harness.StartKeyer): what the dsh plugin at a machine said, in its last
// desktop.config, a new session there may start with (OMD's Ultra), all off. Kept by executor
// with its machine key ("" this computer); a config with none, or the executor gone, drops them.

type startKeys struct {
	key  string // machineKey of the executor
	keys []api.Attached
}

// putKeys keeps keys as executor id's (key its machine key); none: drops them. Whether they
// changed.
func (c *Channel) putKeys(id, key string, keys []api.Attached) bool {
	c.keyMu.Lock()
	defer c.keyMu.Unlock()
	was, had := c.keys[id]
	if len(keys) == 0 {
		delete(c.keys, id)
		return had
	}
	if c.keys == nil {
		c.keys = map[string]startKeys{}
	}
	now := startKeys{key: key, keys: copyKeys(keys)}
	c.keys[id] = now
	return !had || !reflect.DeepEqual(was, now)
}

func (c *Channel) dropKeys(id string) {
	c.keyMu.Lock()
	delete(c.keys, id)
	c.keyMu.Unlock()
}

// StartKeys are the keys a session at machine ("": this computer) may start with, as last told.
// This computer's: those of the executor a launch here goes to, else of any other here.
func (c *Channel) StartKeys(machine string) []api.Attached {
	var host string
	if machine == "" {
		if mc := c.inprocHost(""); mc != nil {
			host = mc.ID()
		}
	}
	c.keyMu.Lock()
	defer c.keyMu.Unlock()
	if machine != "" {
		if k, ok := c.keys[machine]; ok && k.key == machine {
			return copyKeys(k.keys)
		}
		return nil
	}
	if k, ok := c.keys[host]; ok && k.key == "" {
		return copyKeys(k.keys)
	}
	ids := slices.Sorted(maps.Keys(c.keys))
	for _, id := range ids {
		if k := c.keys[id]; k.key == "" {
			return copyKeys(k.keys)
		}
	}
	return nil
}

// DefaultPreset is the preset a session given none runs with at machine, from the presets kept
// (not asked: "" when none are).
func (c *Channel) DefaultPreset(machine string) string {
	c.presetMu.Lock()
	defer c.presetMu.Unlock()
	return c.presets[machine].def
}

func copyKeys(keys []api.Attached) []api.Attached {
	if len(keys) == 0 {
		return nil
	}
	out := make([]api.Attached, len(keys))
	for i, k := range keys {
		k.Content = maps.Clone(k.Content)
		k.Presets = slices.Clone(k.Presets)
		out[i] = k
	}
	return out
}
