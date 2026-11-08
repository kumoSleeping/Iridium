package session

// A new session goes by the opening of its first message, its title (the user, 2026-11-06), till
// its agent names it (ir rename); before its first message, the page shows 新会话. A name the user
// gives (or a /clear passes on) stays.

// nameNew names new session id name, if given: a /clear passing on the one its old session was
// given.
func (m *Manager) nameNew(id, name string) {
	if name == "" {
		return
	}
	if err := m.o.Store.SetName(id, name); err != nil {
		m.o.Logf("session %s: naming it: %v", id, err)
	}
}
