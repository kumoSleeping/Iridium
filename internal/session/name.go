package session

import (
	"fmt"
	"path/filepath"
)

// A new session goes by the opening of its first message, its title (the user, 2026-11-06). Till
// it has one, Iridium's placeholder stands for it: its folder's name, "Iridium", or, if a listed
// session goes by that, "Iridium 2", "Iridium 3"… Archived ones do not count. A name the user
// gives (or a /clear passes on) stays.

// nameNew names new session id: name, if given (a /clear passing on the one its old session was
// given), else the placeholder after cwd.
func (m *Manager) nameNew(id, cwd, name string) {
	m.nmu.Lock()
	defer m.nmu.Unlock()
	var err error
	if name != "" {
		err = m.o.Store.SetName(id, name)
	} else {
		err = m.o.Store.SetAutoName(id, m.folderName(cwd))
	}
	if err != nil {
		m.o.Logf("session %s: naming it: %v", id, err)
	}
}

// folderName is cwd's base name, numbered past the names the listed sessions go by now: a
// placeholder its title has replaced is not one.
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
	m.mu.Lock()
	hosted := make([]*Hosted, 0, len(m.hosted))
	for _, h := range m.hosted {
		hosted = append(hosted, h)
	}
	m.mu.Unlock()
	titles := map[string]string{}
	for _, h := range hosted {
		h.mu.Lock()
		titles[h.id] = h.said.title
		h.mu.Unlock()
	}
	taken := map[string]bool{}
	for id, o := range over {
		if n := o.NameOver(titles[id]); n != "" && shown[id] {
			taken[n] = true
		}
	}
	name := base
	for n := 2; taken[name]; n++ {
		name = fmt.Sprintf("%s %d", base, n)
	}
	return name
}
