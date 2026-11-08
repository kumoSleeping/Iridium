package session

import "github.com/kumoSleeping/iridium/internal/api"

// NoticeKind is what happened in a session, for those that pass it on: Web Push, plugins.
type NoticeKind string

const (
	NoticeTurnStart NoticeKind = "turn_start"
	NoticeMessage   NoticeKind = "message"  // the agent said Text mid-turn (the last one is the reply)
	NoticeTool      NoticeKind = "tool"     // the agent calls Tool
	NoticeTurnEnd   NoticeKind = "turn_end" // Text: its reply; Background: tasks still run
	NoticeAsk       NoticeKind = "ask"      // Ask opened: needs you
	NoticeAskDone   NoticeKind = "ask_done" // RequestID answered or withdrawn
	NoticeError     NoticeKind = "error"    // the agent process ended badly: Text says how
	NoticeStatus    NoticeKind = "status"   // the lamp changed to Status
	NoticeCompacted NoticeKind = "compacted"
)

// Notice is one thing that happened in a session, live (never from a replay).
type Notice struct {
	Session    string
	Kind       NoticeKind
	Text       string
	Tool       string
	Ask        *api.Ask
	RequestID  string
	Status     api.Status
	Background bool
	// Asked: a compaction you asked for (/compact), not the agent's own as its context ran full.
	Asked bool
	// Denied: at a turn's end, how many of its calls auto mode denied.
	Denied int
}

// Watch calls f with every notice from now on, in order per session. f runs on the session's
// reading goroutine: it must not block for long, nor call back into a session synchronously.
func (m *Manager) Watch(f func(Notice)) {
	m.wmu.Lock()
	m.watchers = append(m.watchers, f)
	m.wmu.Unlock()
}

func (m *Manager) notify(ns ...Notice) {
	if len(ns) == 0 {
		return
	}
	m.wmu.Lock()
	ws := m.watchers
	m.wmu.Unlock()
	for _, n := range ns {
		for _, f := range ws {
			f(n)
		}
	}
}
