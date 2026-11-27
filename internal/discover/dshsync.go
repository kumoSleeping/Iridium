package discover

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/dshhome"
)

// DSH Desktop on this computer shares dsh's home with the dsh the user runs in a terminal: both
// write sessions/. Its sessions are the ones its workspace registry files (storages/
// workspace.json: Desktop files every session it makes; a dsh run by hand files none). They are
// shown by activity only while "sync new sessions" is on (off as it comes), and then only by
// activity after it was set: what Desktop did before does not come in. The others are a
// terminal's dsh sessions, shown by activity as every agent's are. Iridium's own sessions are
// shown by the session manager, whoever runs them.

// syncState is this computer's "sync new sessions", kept in the state directory.
type syncState struct {
	On      bool     `json:"on"`
	At      int64    `json:"at,omitempty"` // ms, when it was set on
	Adopted []string `json:"adopted,omitempty"`
}

// freshFor: a session's file younger than this may not be filed yet (Desktop writes its registry
// after it makes the session): its first look says nothing, its growth does.
const freshFor = 3 * time.Second

func (x *Index) syncPath() string { return filepath.Join(x.o.State, "dsh-desktop-sync.json") }

func (x *Index) loadSyncLocked() {
	if x.sync != nil {
		return
	}
	x.sync = &syncState{}
	if x.o.State != "" {
		if b, err := os.ReadFile(x.syncPath()); err == nil {
			_ = json.Unmarshal(b, x.sync)
		}
	}
}

func (x *Index) saveSync(b []byte) {
	if x.o.State == "" {
		return
	}
	tmp := x.syncPath() + ".tmp"
	err := os.WriteFile(tmp, b, 0o600)
	if err == nil {
		err = os.Rename(tmp, x.syncPath())
	}
	if err != nil {
		x.o.Logf("discover: keeping the sync switch: %v", err)
	}
}

// localSync is this computer's switch, and since when it is on.
func (x *Index) localSync() (on bool, at int64) {
	x.syncMu.Lock()
	defer x.syncMu.Unlock()
	x.loadSyncLocked()
	return x.sync.On, x.sync.At
}

func (x *Index) localAdopted(id string) bool {
	x.syncMu.Lock()
	defer x.syncMu.Unlock()
	x.loadSyncLocked()
	return slices.Contains(x.sync.Adopted, id)
}

func (x *Index) setLocalSync(on bool) {
	x.syncMu.Lock()
	x.loadSyncLocked()
	if x.sync.On == on {
		x.syncMu.Unlock()
		return
	}
	x.sync.On = on
	x.sync.At = 0
	if on {
		x.sync.At = time.Now().UnixMilli()
	}
	b, _ := json.Marshal(x.sync)
	x.syncMu.Unlock()
	x.saveSync(b)
}

// adoptLocal shows a local DSH Desktop session picked in search, whatever the sync switch says.
func (x *Index) adoptLocal(id string) error {
	if !validID(id) {
		return fmt.Errorf("%w: invalid session %q", os.ErrNotExist, id)
	}
	x.mu.Lock()
	e := x.entries[id]
	x.mu.Unlock()
	if e == nil {
		x.scanDisk()
		x.mu.Lock()
		e = x.entries[id]
		x.mu.Unlock()
	}
	if e == nil {
		return fmt.Errorf("%w: session %s", os.ErrNotExist, id)
	}
	x.syncMu.Lock()
	x.loadSyncLocked()
	var b []byte
	if !slices.Contains(x.sync.Adopted, id) {
		x.sync.Adopted = append(x.sync.Adopted, id)
		b, _ = json.Marshal(x.sync)
	}
	x.syncMu.Unlock()
	if len(b) > 0 {
		x.saveSync(b)
	}
	if x.o.Store != nil {
		if _, err := x.o.Store.Show(string(e.agent), id); err != nil {
			x.o.Logf("discover: showing adopted %s: %v", id, err)
		}
	}
	x.Changed(id)
	return nil
}

// localRegistry is this computer's workspace registry as last read: the sessions it files.
type localRegistry struct {
	checked time.Time
	mtime   time.Time
	size    int64
	filed   map[string]bool
}

// desktopFiled: DSH Desktop here files session id (read again when the registry's file changed,
// looked at once a second at most).
func (x *Index) desktopFiled(id string) bool {
	x.syncMu.Lock()
	defer x.syncMu.Unlock()
	r := &x.reg
	if time.Since(r.checked) >= time.Second {
		r.checked = time.Now()
		path := filepath.Join(append([]string{x.o.DshHome}, dshhome.RegistryPath...)...)
		st, err := os.Stat(path)
		switch {
		case err != nil:
			r.filed, r.size, r.mtime = nil, 0, time.Time{}
		case st.Size() != r.size || !st.ModTime().Equal(r.mtime):
			r.size, r.mtime, r.filed = st.Size(), st.ModTime(), map[string]bool{}
			if b, err := os.ReadFile(path); err == nil {
				if reg, err := dshhome.ParseRegistry(b); err == nil {
					for _, w := range reg.Workspaces {
						for _, s := range w.Sessions {
							r.filed[s] = true
						}
					}
				}
			}
		}
	}
	return r.filed[id]
}

// desktopKept: activity in f, changed at mtime, is not to show it: a session of DSH Desktop's
// here (or a subagent of one) with the switch off, or last active before it was set on. first:
// f was not seen before; with the switch off, a file that young is not judged yet.
func (x *Index) desktopKept(f onDisk, g glimpse, mtime time.Time, first bool) bool {
	if f.agent != api.AgentDsh || x.o.DshHome == "" {
		return false
	}
	if x.localAdopted(f.id) || (g.sub && g.parent != "" && x.localAdopted(g.parent)) {
		return false
	}
	on, at := x.localSync()
	if !on && first && time.Since(mtime) < freshFor {
		return true
	}
	if !x.desktopFiled(f.id) && !(g.sub && g.parent != "" && x.desktopFiled(g.parent)) {
		return false
	}
	return !on || mtime.UnixMilli() <= at
}
