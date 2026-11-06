package session

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/kumoSleeping/iridium/internal/agent"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/store"
)

// The queue (Rhodium §6.1 "网页的待发队列"): a message sent while a turn runs waits for it to end,
// then goes as the next turn, all waiting ones together. Each can be steered into the running
// turn, edited or deleted meanwhile. An interrupt, a stop, or the agent's exit or crash pauses the
// queue: what waits then goes only on continue, or by its own steer. While the quota is out
// messages wait too, and go when it is back. The queue is in the store (inputs not sent).

// Send gives the session a message from `from` (an interactive terminal's ID), starting its
// agent if it is not running: a new turn when idle; while a turn runs it waits in the queue.
// The agent sees "[name] " before it (the terminal's name then, Options.Tag), unless it is a slash
// command, which must reach the agent as typed.
func (m *Manager) Send(ctx context.Context, id, text, from string) (string, error) {
	input, _, err := m.SendWith(ctx, id, text, from, false)
	return input, err
}

// SendImages is Send with images for the agent to see; the text names their files.
func (m *Manager) SendImages(ctx context.Context, id, text, from string, images []agent.Image) (string, error) {
	input, _, err := m.SendWith(ctx, id, text, from, false, images...)
	return input, err
}

// SendWith is Send; steer: join the running turn rather than wait for it. queued: it waits.
// A session found on disk is taken up first. A queued message's images wait in memory only: after
// a restart it goes with its text, which names their files.
func (m *Manager) SendWith(ctx context.Context, id, text, from string, steer bool, images ...agent.Image) (input string, queued bool, err error) {
	if strings.TrimSpace(text) == "" {
		return "", false, errors.New("an empty message")
	}
	// A command (/compact, /context…) waits for the turn's end: written into a running turn the
	// agent may take it as words, and Codex and pi cannot compact mid-turn.
	if strings.HasPrefix(strings.TrimSpace(text), "/") {
		text, steer = strings.TrimSpace(text), false
	}
	h, err := m.take(ctx, id)
	if err != nil {
		return "", false, err
	}
	h.qmu.Lock()
	defer h.qmu.Unlock()
	h.shownLater() // a message for it: out of the archive
	terminal, tag := m.source(from)
	in := store.Input{ID: newUUID(), Session: h.id, Terminal: terminal, Tag: tag, Text: text, State: "sent"}
	// Steering joins a running turn; while it waits for the quota there is none to join, so it waits too.
	if (!steer && h.busy()) || h.quotaWait() {
		in.State = "queued"
		if err := m.o.Store.AddInput(in); err != nil {
			return "", false, err
		}
		h.mu.Lock()
		h.queue = append(h.queue, api.Queued{ID: in.ID, Text: text, From: in.Tag, FromTerminal: terminal, At: time.Now().UnixMilli()})
		if len(images) > 0 {
			h.qimages[in.ID] = images
		}
		h.mu.Unlock()
		h.publish()
		return in.ID, true, nil
	}
	if err := m.o.Store.AddInput(in); err != nil {
		return "", false, err
	}
	return in.ID, false, h.write(ctx, in, images)
}

// queuedImages takes the images waiting with queued message id.
func (h *Hosted) queuedImages(id string) []agent.Image {
	h.mu.Lock()
	defer h.mu.Unlock()
	im := h.qimages[id]
	delete(h.qimages, id)
	return im
}

// quotaWait: the agent's quota ran out and Iridium goes on later.
func (h *Hosted) quotaWait() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.quotaUntil != 0
}

// busy: a message now would join a running turn, or wait behind others or for the quota.
func (h *Hosted) busy() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.quotaUntil != 0 || slices.ContainsFunc(h.queue, func(q api.Queued) bool { return !q.Paused }) {
		return true
	}
	return h.live && h.turn != agent.Idle
}

// write gives the agent an input already in the store, starting the agent if need be.
func (h *Hosted) write(ctx context.Context, in store.Input, images []agent.Image) error {
	if err := h.start(ctx); err != nil {
		return err
	}
	line := in.Text
	if !strings.HasPrefix(line, "/") {
		tag := in.Tag
		if tag == "" {
			tag = in.Terminal
		}
		line = "[" + tag + "] " + line
	}
	h.mu.Lock()
	h.lastFrom = in.Terminal
	ready := h.ready
	h.mu.Unlock()
	// The process says its use so far first, so this turn's share can be told from it.
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
	case <-ctx.Done():
		return ctx.Err()
	}
	return h.drv.Send(ctx, agent.Message{ID: in.ID, Text: line, Human: true, Images: images})
}

// release sends what waits in the queue, in order, as one turn: the queued ones, and the paused
// ones too if all. One that cannot be written stays, and so do those after it, paused.
func (h *Hosted) release(ctx context.Context, all bool) {
	h.qmu.Lock()
	defer h.qmu.Unlock()
	h.releaseLocked(ctx, all)
}

func (h *Hosted) releaseLocked(ctx context.Context, all bool) {
	h.mu.Lock()
	var due []api.Queued
	for _, q := range h.queue {
		if all || !q.Paused {
			due = append(due, q)
		}
	}
	h.mu.Unlock()
	for i, q := range due {
		h.mu.Lock()
		images := h.qimages[q.ID]
		h.mu.Unlock()
		err := h.write(ctx, queuedInput(h.id, q), images)
		if err == nil {
			err = h.m.o.Store.SetInputState(q.ID, "sent")
		}
		if err != nil {
			h.logf("queue: %v", err)
			h.pauseLocked(due[i:])
			return
		}
		h.mu.Lock()
		h.queue = slices.DeleteFunc(h.queue, func(x api.Queued) bool { return x.ID == q.ID })
		delete(h.qimages, q.ID)
		h.mu.Unlock()
	}
	if len(due) > 0 {
		h.publish()
	}
}

// pause pauses what waits in the queue.
func (h *Hosted) pause() {
	h.qmu.Lock()
	defer h.qmu.Unlock()
	h.mu.Lock()
	q := slices.Clone(h.queue)
	h.mu.Unlock()
	h.pauseLocked(q)
}

func (h *Hosted) pauseLocked(items []api.Queued) {
	changed := false
	for _, it := range items {
		if it.Paused {
			continue
		}
		if err := h.m.o.Store.SetInputState(it.ID, "paused"); err != nil {
			h.logf("queue: %v", err)
			continue
		}
		h.mu.Lock()
		if i := slices.IndexFunc(h.queue, func(x api.Queued) bool { return x.ID == it.ID }); i >= 0 {
			h.queue[i].Paused = true
			changed = true
		}
		h.mu.Unlock()
	}
	if changed {
		h.publish()
	}
}

// Continue lets the paused queue go: now if no turn runs, else when it ends.
func (m *Manager) Continue(ctx context.Context, id string) error {
	h, err := m.get(id)
	if err != nil {
		return err
	}
	h.qmu.Lock()
	defer h.qmu.Unlock()
	h.mu.Lock()
	running := (h.live && h.turn != agent.Idle) || h.quotaUntil != 0
	h.mu.Unlock()
	if !running {
		h.releaseLocked(ctx, true)
		return nil
	}
	if err := m.o.Store.MoveQueue(id, "paused", "queued"); err != nil {
		return err
	}
	h.mu.Lock()
	for i := range h.queue {
		h.queue[i].Paused = false
	}
	h.mu.Unlock()
	h.publish()
	return nil
}

// Steer sends a queued message now, into the running turn if there is one.
func (m *Manager) Steer(ctx context.Context, id, input string) error {
	h, err := m.get(id)
	if err != nil {
		return err
	}
	h.qmu.Lock()
	defer h.qmu.Unlock()
	h.mu.Lock()
	i := slices.IndexFunc(h.queue, func(q api.Queued) bool { return q.ID == input })
	var q api.Queued
	if i >= 0 {
		q = h.queue[i]
	}
	h.mu.Unlock()
	if i < 0 {
		return store.ErrNoInput
	}
	if err := h.write(ctx, queuedInput(id, q), h.queuedImages(q.ID)); err != nil {
		return err
	}
	if err := m.o.Store.SetInputState(q.ID, "sent"); err != nil {
		return err
	}
	h.mu.Lock()
	h.queue = slices.DeleteFunc(h.queue, func(x api.Queued) bool { return x.ID == input })
	h.mu.Unlock()
	h.publish()
	return nil
}

// EditQueued changes a queued message's text.
func (m *Manager) EditQueued(id, input, text string) error {
	if strings.TrimSpace(text) == "" {
		return errors.New("an empty message")
	}
	return m.changeQueued(id, input, func(h *Hosted, i int) error {
		if err := m.o.Store.EditInput(input, text); err != nil {
			return err
		}
		h.queue[i].Text = text
		return nil
	})
}

// DeleteQueued drops a queued message.
func (m *Manager) DeleteQueued(id, input string) error {
	return m.changeQueued(id, input, func(h *Hosted, i int) error {
		if err := m.o.Store.DeleteInput(input); err != nil {
			return err
		}
		h.queue = slices.Delete(h.queue, i, i+1)
		delete(h.qimages, input)
		return nil
	})
}

func (m *Manager) changeQueued(id, input string, f func(h *Hosted, i int) error) error {
	h, err := m.get(id)
	if err != nil {
		return err
	}
	h.qmu.Lock()
	defer h.qmu.Unlock()
	h.mu.Lock()
	i := slices.IndexFunc(h.queue, func(q api.Queued) bool { return q.ID == input })
	if i < 0 {
		h.mu.Unlock()
		return store.ErrNoInput
	}
	err = f(h, i)
	h.mu.Unlock()
	if err == nil {
		h.publish()
	}
	return err
}

// loadQueue reads the session's queue from the store.
func (h *Hosted) loadQueue() {
	ins, err := h.m.o.Store.Queued(h.id)
	if err != nil {
		h.logf("queue: %v", err)
		return
	}
	for _, in := range ins {
		tag := in.Tag
		if tag == "" {
			tag = in.Terminal
		}
		h.queue = append(h.queue, api.Queued{ID: in.ID, Text: in.Text, From: tag, FromTerminal: in.Terminal, Paused: in.State == "paused", At: in.Created})
	}
}

// queuedInput is a queued message as the input it was stored as: its terminal's ID, its tag.
func queuedInput(session string, q api.Queued) store.Input {
	terminal := q.FromTerminal
	if terminal == "" {
		terminal = q.From
	}
	return store.Input{ID: q.ID, Session: session, Terminal: terminal, Tag: q.From, Text: q.Text}
}
