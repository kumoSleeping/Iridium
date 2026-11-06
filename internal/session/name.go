package session

import (
	"fmt"
	"path/filepath"
)

// A new session is named after its folder (the user's decision, 2026-11-06): "Iridium", or, if a
// session in the list already goes by that, "Iridium 2", "Iridium 3"… Archived ones do not count.

// nameNew names new session id after cwd, unless name is given (a /clear passing its own on).
func (m *Manager) nameNew(id, cwd, name string) {
	m.nmu.Lock()
	defer m.nmu.Unlock()
	if name == "" {
		name = m.folderName(cwd)
	}
	if err := m.o.Store.SetName(id, name); err != nil {
		m.o.Logf("session %s: naming it: %v", id, err)
	}
}

// folderName is cwd's base name, numbered past those the listed sessions already go by.
func (m *Manager) folderName(cwd string) string {
	base := filepath.Base(cwd)
	over, err := m.o.Store.Overlays()
	if err != nil {
		return base
	}
	shown := map[string]bool{}
	for k, r := range m.visibility() {
		if r.Shown {
			shown[k[1]] = true
		}
	}
	taken := map[string]bool{}
	for id, o := range over {
		if o.Name != "" && shown[id] {
			taken[o.Name] = true
		}
	}
	name := base
	for n := 2; taken[name]; n++ {
		name = fmt.Sprintf("%s %d", base, n)
	}
	return name
}
