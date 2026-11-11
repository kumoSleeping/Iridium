package dsh

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/harness"
	"github.com/kumoSleeping/iridium/internal/remote"
	"github.com/kumoSleeping/iridium/internal/remote/wire"
)

// A held session: one at an executor's machine that DSH Desktop there holds (loaded in it, its
// writer lock): Iridium cannot resume it, so it asks Desktop through the dsh plugin (held.*,
// wire.CapHeld): the model and the level it runs at. A message for it goes to Desktop's own agent
// of it (held.send). What plugins there add to a session they attach themselves
// (session.attach, internal/session/attach.go).

const (
	heldFor = 10 * time.Second // how long a session's live state is taken as it was told
	// held.get's: the plugin answers within 3s, past that the way here and back.
	heldWait = 6 * time.Second
	// held.set's: a model switched there may take a while; within the route's 30s.
	heldSet   = 23 * time.Second
	heldEvery = 500 * time.Millisecond // the least between two showings of a session held.changed tells of
)

// heldEntry is what is known of a session's live state at its machine: as last told (held.get,
// held.set's reply or held.changed), when, and how its showing goes.
type heldEntry struct {
	view  heldView
	at    time.Time // when told; zero: never
	tried time.Time // the last held.get that failed: not asked again before heldFor
	busy  bool      // a held.get on its way
	fail  bool      // the last held.get failed (told once)
	shown time.Time // last shown for a held.changed
	due   bool      // a showing for a held.changed waits
}

// heldView is a session's wire.HeldLive.
type heldView struct {
	held          bool
	model, effort string
}

// heldAt is where session r is asked about: its machine (connected) and whether Desktop there has
// it (discovery's HeldElsewhere). ok false: not a dsh session at a connected machine.
func (c *Channel) heldAt(r harness.Ref) (mc *remote.Machine, held bool, ok bool) {
	h := c.h()
	if h == nil || !ours(r) || r.Machine == "" {
		return nil, false, false
	}
	if mc = c.machine(r.Machine); mc == nil {
		return nil, false, false
	}
	return mc, h.HeldElsewhere(r.As), true
}

// heldLive asks session r's machine for its live state now (held.get) and keeps it.
func (c *Channel) heldLive(ctx context.Context, r harness.Ref) (heldView, error) {
	mc, _, ok := c.heldAt(r)
	if !ok || !mc.Has(wire.CapHeld) {
		return heldView{}, fmt.Errorf("%w: no holder to ask", harness.ErrBadRequest)
	}
	ctx, cancel := context.WithTimeout(ctx, heldWait)
	defer cancel()
	live, err := desktopGet(ctx, mc, r.As)
	if err != nil {
		return heldView{}, err
	}
	return c.keepHeld(r.ID, live), nil
}

// keepHeld keeps live as session id's; the view kept.
func (c *Channel) keepHeld(id string, live wire.HeldLive) heldView {
	v := heldView{held: live.Held}
	if live.Held {
		v.model, v.effort = live.Model, live.Effort
	}
	c.heldMu.Lock()
	defer c.heldMu.Unlock()
	e := c.heldEntryLocked(id)
	e.view, e.at, e.tried = v, time.Now(), time.Time{}
	return v
}

func (c *Channel) heldEntryLocked(id string) *heldEntry {
	e := c.helds[id]
	if e == nil {
		e = &heldEntry{}
		c.helds[id] = e
	}
	return e
}

// cachedHeld is session id's live state as last told; ok false if never.
func (c *Channel) cachedHeld(id string) (heldView, bool) {
	c.heldMu.Lock()
	defer c.heldMu.Unlock()
	e := c.helds[id]
	if e == nil || e.at.IsZero() {
		return heldView{}, false
	}
	return e.view, true
}

// warmHeld asks for session r's live state in the background if what is kept is older than
// heldFor, then shows the session again. One that failed is asked again after heldFor.
func (c *Channel) warmHeld(r harness.Ref) {
	h := c.h()
	c.heldMu.Lock()
	e := c.heldEntryLocked(r.ID)
	fresh := time.Since(e.at) < heldFor
	if e.busy || fresh || time.Since(e.tried) < heldFor {
		c.heldMu.Unlock()
		return
	}
	e.busy = true
	c.heldMu.Unlock()
	go func() {
		_, err := c.heldLive(c.ctx, r)
		c.heldMu.Lock()
		e.busy = false
		tell := err != nil && !e.fail
		e.fail = err != nil
		if err != nil {
			e.tried = time.Now() // not asked again at each look; what was told kept, none if nothing was
		}
		c.heldMu.Unlock()
		if tell {
			c.o.Logf("session %s: its holder's state: %v", r.ID, err)
		}
		if err == nil {
			h.ShowAgain(r.ID)
		}
	}()
}

// State puts what session r's machine says of it into s: while Desktop has it (not here: r.Live
// false), the model and the level it runs at; and the
// models dsh offers there (catalogState). What is kept is used; if it is old, it is asked for
// again and the session shown again when it comes, so a state does not wait on the machine.
func (c *Channel) State(r harness.Ref, s *api.SessionState) {
	if !ours(r) {
		return
	}
	c.heldState(r, s)
	c.catalogState(r.Machine, s)
}

func (c *Channel) heldState(r harness.Ref, s *api.SessionState) {
	if c.h() == nil || r.Machine == "" || c.o.Remote == nil {
		return
	}
	if r.Live || !c.h().HeldElsewhere(r.As) {
		return
	}
	mc := c.o.Remote.Find(r.Machine)
	if mc == nil || !mc.Has(wire.CapHeld) {
		return
	}
	c.warmHeld(r)
	v, ok := c.cachedHeld(r.ID)
	if !ok || !v.held {
		return
	}
	// Desktop's, over what a hosted session keeps from when it ran here (its settings): what
	// runs is Desktop's, and a level or model set here goes there (Set), not to those.
	if v.effort != "" {
		s.Effort, s.EffortNow = v.effort, v.effort
	}
	if v.model != "" {
		s.Model = v.model
		if s.Pick != "" {
			s.Pick = v.model
		}
	}
}

// heldTold is a held.changed from machine, waiting to be taken in.
type heldTold struct {
	machine string
	c       wire.HeldChanged
}

// onHeld is the event held.changed from machine (remote.Hub.Handle): taken in by its own
// goroutine, in order, so the connection's reading one waits on nothing here.
func (c *Channel) onHeld(machine string, params json.RawMessage) {
	var ch wire.HeldChanged
	if json.Unmarshal(params, &ch) != nil || ch.Session == "" {
		return
	}
	c.heldMu.Lock()
	c.told = append(c.told, heldTold{machine, ch})
	start := !c.taking
	c.taking = true
	c.heldMu.Unlock()
	if start {
		go c.takeTold()
	}
}

func (c *Channel) takeTold() {
	for {
		c.heldMu.Lock()
		if len(c.told) == 0 {
			c.taking = false
			c.heldMu.Unlock()
			return
		}
		t := c.told[0]
		c.told = c.told[1:]
		c.heldMu.Unlock()
		c.heldChanged(t.machine, t.c)
	}
}

// heldChanged takes in a held.changed: kept, and the session shown again, at most once in
// heldEvery (the last told). One not known as a dsh session at that machine is let be.
func (c *Channel) heldChanged(machine string, ch wire.HeldChanged) {
	h := c.h()
	if h == nil {
		return
	}
	r, ok := h.Ref(ch.Session)
	if !ok || !ours(r) || r.Machine != machine {
		return
	}
	id := r.ID
	c.keepHeld(id, ch.Live)
	c.heldMu.Lock()
	e := c.heldEntryLocked(id)
	if e.due {
		c.heldMu.Unlock()
		return // the showing that waits shows this
	}
	e.due = true
	wait := max(heldEvery-time.Since(e.shown), 0)
	c.heldMu.Unlock()
	time.AfterFunc(wait, func() {
		c.heldMu.Lock()
		e.due, e.shown = false, time.Now()
		c.heldMu.Unlock()
		h.ShowAgain(id)
	})
}

// Set sets a model or an effort of session r where DSH Desktop holds it (held.set): only while
// Desktop has it and it does not run here (the core would refuse it: elsewhere). done false: not
// such a session, or the machine does not take it after all.
func (c *Channel) Set(ctx context.Context, r harness.Ref, ch harness.Change) (bool, error) {
	if r.Live {
		return false, nil
	}
	mc, held, ok := c.heldAt(r)
	if !ok || !held || !mc.Has(wire.CapHeld) {
		return false, nil
	}
	p := wire.HeldSetParams{Session: r.As, Model: ch.Model, Effort: ch.Effort}
	ctx, cancel := context.WithTimeout(ctx, heldSet)
	defer cancel()
	live, err := desktopSet(ctx, mc, p)
	if wire.IsCode(err, wire.CodeNotFound) || wire.IsCode(err, wire.CodeUnsupported) {
		return false, nil
	}
	if err != nil {
		return true, heldErr(err)
	}
	c.keepHeld(r.ID, live)
	c.o.Logf("session %s: %s set at %s, in DSH Desktop", r.ID, map[bool]string{true: "model", false: "effort"}[ch.Model != ""], mc.ID())
	c.h().ShowAgain(r.ID)
	return true, nil
}

// heldErr is a held.set's error as the session's: what the plugin turned down is a bad request.
func heldErr(err error) error {
	var e *wire.Error
	if errors.As(err, &e) && (e.Code == wire.CodeBadRequest || e.Code == wire.CodeNotFound || e.Code == wire.CodeUnsupported) {
		return fmt.Errorf("%w: %s", harness.ErrBadRequest, e.Message)
	}
	return err
}

// Send gives a message for a dsh session at another machine that DSH Desktop there holds (open
// in it, so its writer lock is Desktop's and a resume here is refused) to Desktop's own agent of
// it, through the dsh plugin (held.send): the work goes on there, its
// transcript mirrored here as for any of Desktop's sessions (the user, 2026-11-08: a session the
// executor lost, Desktop took up on its return). done false: not such a session, or the plugin
// cannot, so the message goes the usual way. Images do not go: Desktop's agent would not see them.
func (c *Channel) Send(ctx context.Context, r harness.Ref, text string, from harness.Source, steer, images bool) (string, bool, error) {
	h := c.h()
	if h == nil || images || r.Live || !ours(r) || r.Machine == "" || !r.Found {
		return "", false, nil
	}
	if !h.HeldElsewhere(r.As) { // loaded there, idle or not: Desktop holds its writer lock
		return "", false, nil
	}
	if c.o.Remote == nil {
		return "", false, nil
	}
	mc := c.o.Remote.Find(r.Machine)
	if mc == nil || !mc.Has(wire.CapHeld) {
		return "", false, nil
	}
	line := strings.TrimSpace(text)
	if !strings.HasPrefix(line, "/") {
		line = "[" + cmpOr(from.Tag, from.Terminal) + "] " + line
	}
	err := desktopSend(ctx, mc, r.As, line, steer)
	if wire.IsCode(err, wire.CodeUnsupported) || wire.IsCode(err, wire.CodeNotFound) {
		return "", false, nil // a plugin without it, or Desktop holds no agent of it: the usual way says why not
	}
	if err != nil {
		return "", true, err
	}
	c.o.Logf("session %s: given to DSH Desktop's agent at %s", r.ID, r.Machine)
	go h.Fetch(context.Background(), r.As) // its mirror follows sooner
	return "DSH Desktop", true, nil
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
