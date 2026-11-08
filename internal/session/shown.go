package session

import (
	"context"
	"time"

	"github.com/kumoSleeping/iridium/internal/store"
)

// The shown marker (store/shown.go; the user's decision, 2026-11-05): a hosted session carries
// one from the moment Iridium makes it, and again whenever its agent begins a turn or is sent a
// message. Archiving takes it off and stops the agent, as idle reaping does.

// visibility is every marker; none on a store error (all hidden would be wrong, all shown too:
// the list is the store's to say).
func (m *Manager) visibility() store.Visibility {
	v, err := m.o.Store.Visibility()
	if err != nil {
		m.o.Logf("session: shown markers: %v", err)
		return store.Visibility{}
	}
	return v
}

// keys are the ids session h's marker may be under: Iridium's, and the agent's own where it is
// another (Codex's thread).
func (h *Hosted) keys() []string {
	if as := h.discoveredAs(); as != h.id {
		return []string{h.id, as}
	}
	return []string{h.id}
}

// shownIn says whether h carries the marker under either of its ids.
func (h *Hosted) shownIn(v store.Visibility) bool {
	for _, k := range h.keys() {
		if v.Shown(string(h.meta.Agent), k) {
			return true
		}
	}
	return false
}

// shown says whether h carries the marker now, asking the store of its own ids only.
func (h *Hosted) shown() bool {
	for _, k := range h.keys() {
		if r, ok, err := h.m.o.Store.ShownRow(string(h.meta.Agent), k); err == nil && ok && r.Shown {
			return true
		}
	}
	return false
}

// show sets h's marker; true if it was hidden.
func (h *Hosted) show() bool {
	changed := false
	for _, k := range h.keys() {
		c, err := h.m.o.Store.Show(string(h.meta.Agent), k)
		if err != nil {
			h.logf("showing it: %v", err)
		}
		changed = changed || c
	}
	return changed
}

// shownLater: activity of h's. The marker is set, and if that brought it back from the archive,
// its state and the list say so.
func (h *Hosted) shownLater() {
	go func() {
		if h.show() {
			h.publish()
			h.m.publishList()
		}
	}()
}

// SetArchived archives session id (on: its marker taken off, how far its transcript goes noted,
// a hosted one's agent stopped) or brings it back from the archive (its marker set).
func (m *Manager) SetArchived(ctx context.Context, id string, on bool) error {
	h, err := m.get(id)
	if err != nil {
		h = m.byAgentID(id)
	}
	if h == nil {
		s, f, ok := m.found(id)
		if !ok {
			return ErrNotFound
		}
		if !on {
			_, err = m.o.Store.Show(string(s.Agent), id)
		} else {
			size, _ := m.sizeOf(id)
			if f != nil && s.Machine == "" { // at another machine: its size there, as discovery counts
				size, _ = f.Size()
			}
			err = m.o.Store.Hide(string(s.Agent), id, size, 0)
		}
		if err != nil {
			return err
		}
		m.Refresh([]string{id})
		return nil
	}
	if !on {
		h.show()
		h.publish()
		m.publishList()
		return nil
	}
	live := h.isLive()
	quiet := int64(0)
	if live {
		// It may write as it ends: that is not activity (ended notes the size again).
		quiet = time.Now().Add(2*m.o.Config.StopGrace.D() + 15*time.Second).UnixMilli()
	}
	if err := h.hide(quiet); err != nil {
		return err
	}
	h.publish()
	m.publishList()
	if live {
		m.o.Logf("session %s: archived, stopping its agent", h.id)
		return h.stop(ctx)
	}
	return nil
}

// hide takes h's marker off under each of its ids, noting its transcript's size.
func (h *Hosted) hide(quietUntil int64) error {
	size := int64(0)
	if f := h.transcript(); f != nil {
		size, _ = f.Size()
	}
	for _, k := range h.keys() {
		if err := h.m.o.Store.Hide(string(h.meta.Agent), k, size, quietUntil); err != nil {
			return err
		}
	}
	return nil
}

// endedHidden: h's agent ended while h is archived: what it wrote ending is noted as seen.
func (h *Hosted) endedHidden() {
	if h.shown() {
		return
	}
	if err := h.hide(0); err != nil {
		h.logf("noting its size: %v", err)
	}
}

// sizeOf is how long discovery saw session id's transcript; false if it did not.
func (m *Manager) sizeOf(id string) (int64, bool) {
	if s, ok := m.o.Discovery.(interface{ Size(string) (int64, bool) }); ok {
		return s.Size(id)
	}
	return 0, false
}

// adoptShown: a session an earlier daemon hosted, taken up. It is Iridium's, so shown, unless
// the old archive flag (or a marker) says otherwise.
func (h *Hosted) adoptShown(v store.Visibility) {
	a := string(h.meta.Agent)
	for _, k := range h.keys() {
		r, ok := v.Row(a, k)
		switch {
		case ok && r.Legacy:
			if err := h.hide(0); err != nil {
				h.logf("%v", err)
			}
			return
		case ok:
			return
		}
	}
	h.show()
}
