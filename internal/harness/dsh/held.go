package dsh

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/harness"
	"github.com/kumoSleeping/iridium/internal/remote"
	"github.com/kumoSleeping/iridium/internal/remote/wire"
)

// A held session: one at an executor's machine that DSH Desktop there holds (loaded in it, its
// writer lock): Iridium cannot resume it, so it asks Desktop through the dsh plugin (held.*,
// wire.CapHeldFeatures): the model and the level it runs at, and the features plugins there add
// to a session (a mode of a Desktop plugin's, the user, 2026-11-09). Iridium shows and switches
// those without knowing what they are: it reads only on, active, available, place and tone, and
// passes the rest through, checked and cut short. A message for it goes to Desktop's own agent of
// it (held.send).

const (
	heldFor = 10 * time.Second // how long a session's live state is taken as it was told
	// held.get's: the plugin answers within 3s (its providers', each a feature's, the effort's
	// owner's: at once, not one after another), past that the way here and back.
	heldWait = 6 * time.Second
	// held.set's: a plugin's switch may take 15s, its owner and the answer after 3s each; with a
	// held.get before it (a feature, nothing kept), within the route's 30s.
	heldSet   = 23 * time.Second
	heldEvery = 500 * time.Millisecond // the least between two showings of a session held.changed tells of

	maxFeatures    = 4
	maxOptions     = 8
	maxFeatureName = 24
	maxFeatureText = 120
	featurePlace   = "effort" // the one place the page knows
)

var (
	featureID = regexp.MustCompile(`^[a-z0-9-]+\.[a-z0-9-]+$`)
	optionID  = regexp.MustCompile(`^[a-z0-9-]+$`)
)

// heldEntry is what is known of a session's live state at its machine: as last told (held.get,
// held.set's reply or held.changed), when, and how its showing goes.
type heldEntry struct {
	view  heldView
	at    time.Time // when told; zero: never
	lang  string    // the language its texts are in (held.get's, held.set's)
	tried time.Time // the last held.get that failed: not asked again before heldFor
	busy  bool      // a held.get on its way
	fail  bool      // the last held.get failed (told once)
	shown time.Time // last shown for a held.changed
	due   bool      // a showing for a held.changed waits
}

// heldView is a session's wire.HeldLive, its features checked (features); owner, the feature
// that owns its effort now (effort its level), held or not.
type heldView struct {
	held          bool
	model, effort string
	owner         string
	features      []api.Feature
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

// heldLive asks session r's machine for its live state now (held.get, in the user's language)
// and keeps it.
func (c *Channel) heldLive(ctx context.Context, r harness.Ref) (heldView, error) {
	mc, _, ok := c.heldAt(r)
	if !ok || !mc.Has(wire.CapHeldFeatures) {
		return heldView{}, fmt.Errorf("%w: no holder to ask", harness.ErrBadRequest)
	}
	ctx, cancel := context.WithTimeout(ctx, heldWait)
	defer cancel()
	lang := c.h().Lang()
	live, err := desktopGet(ctx, mc, r.As, lang)
	if err != nil {
		return heldView{}, err
	}
	return c.keepHeld(r.ID, mc.ID(), live, &lang), nil
}

// keepHeld keeps live as session id's, told by machine; the view kept. lang: the language it was
// asked in; nil: as before (held.changed, in the one last asked in).
func (c *Channel) keepHeld(id, machine string, live wire.HeldLive, lang *string) heldView {
	v := heldView{held: live.Held, features: c.features(machine, live.Features)}
	if live.Held {
		v.model, v.effort = live.Model, live.Effort
	}
	if live.Owner != "" && slices.ContainsFunc(v.features, func(f api.Feature) bool { return f.ID == live.Owner }) {
		v.owner, v.effort = live.Owner, live.Effort
	}
	c.heldMu.Lock()
	defer c.heldMu.Unlock()
	e := c.heldEntryLocked(id)
	e.view, e.at, e.tried = v, time.Now(), time.Time{}
	if lang != nil {
		e.lang = *lang
	}
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
// heldFor or in another language than the user's now, then shows the session again. One that
// failed is asked again after heldFor.
func (c *Channel) warmHeld(r harness.Ref) {
	h := c.h()
	lang := h.Lang()
	c.heldMu.Lock()
	e := c.heldEntryLocked(r.ID)
	fresh := time.Since(e.at) < heldFor && e.lang == lang
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

// State puts what session r's machine says of it into s: the features plugins there add, and
// while Desktop has it (not here: r.Live false), the model and the level it runs at; and the
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
	mc := c.o.Remote.Find(r.Machine)
	if mc == nil || !mc.Has(wire.CapHeldFeatures) {
		return
	}
	c.warmHeld(r)
	v, ok := c.cachedHeld(r.ID)
	if !ok {
		return
	}
	s.Features = append(s.Features, v.features...)
	if !v.held || r.Live {
		// A feature of the process there runs its effort (a session Iridium started there):
		// the level it runs at is that one's.
		if v.owner != "" && v.effort != "" {
			s.Effort, s.EffortNow = v.effort, v.effort
		}
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

// Retuned: the user's language is another now: the sessions whose live state is kept are shown
// again, so each is asked in it (warmHeld) and its features' texts come in it.
func (c *Channel) Retuned() {
	h := c.h()
	if h == nil {
		return
	}
	c.heldMu.Lock()
	ids := make([]string, 0, len(c.helds))
	for id, e := range c.helds {
		if !e.at.IsZero() {
			ids = append(ids, id)
		}
	}
	c.heldMu.Unlock()
	if len(ids) == 0 {
		return
	}
	go func() {
		for _, id := range ids {
			h.ShowAgain(id)
		}
	}()
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
	c.keepHeld(id, machine, ch.Live, nil)
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

// features are a plugin's features as Iridium shows them: an ID of the form "<plugin>.<name>", the
// first of each; a shape it knows (V 1), at the place the page has (effort); at most maxFeatures.
// Texts are trimmed, without control characters and cut short; options at most maxOptions, the
// one picked one of them.
func (c *Channel) features(machine string, in []wire.Feature) []api.Feature {
	var out []api.Feature
	for _, f := range in {
		if !featureID.MatchString(f.ID) || f.V > 1 || f.Place != featurePlace ||
			slices.ContainsFunc(out, func(x api.Feature) bool { return x.ID == f.ID }) {
			continue
		}
		if len(out) == maxFeatures {
			c.heldMu.Lock()
			told := c.many[machine]
			c.many[machine] = true
			c.heldMu.Unlock()
			if !told {
				c.o.Logf("executor %s: more than %d features on a session; the rest not shown", machine, maxFeatures)
			}
			break
		}
		x := api.Feature{ID: f.ID, Name: cut(f.Name, maxFeatureName), On: f.On, Active: f.Active, Available: f.Available,
			Reason: cut(f.Reason, maxFeatureText), Hint: cut(f.Hint, maxFeatureText), Place: f.Place}
		if f.Tone == "warm" {
			x.Tone = f.Tone
		}
		for _, o := range f.Options {
			if len(x.Options) == maxOptions {
				break
			}
			if optionID.MatchString(o.ID) && !slices.ContainsFunc(x.Options, func(y api.FeatureOption) bool { return y.ID == o.ID }) {
				x.Options = append(x.Options, api.FeatureOption{ID: o.ID, Name: cut(o.Name, maxFeatureName)})
			}
		}
		if slices.ContainsFunc(x.Options, func(y api.FeatureOption) bool { return y.ID == f.Option }) {
			x.Option = f.Option
		}
		out = append(out, x)
	}
	return out
}

// cut is s trimmed, without control characters, at most n runes.
func cut(s string, n int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > n {
		s = strings.TrimSpace(string(r[:n]))
	}
	return s
}

// Set sets a model, an effort or a feature of session r where DSH Desktop holds it (held.set).
// A model or an effort only while Desktop has it and it does not run here (the core would refuse
// it: elsewhere); an effort also while a feature there owns it (as last told), held or run here;
// a feature, one the session has as last told, its option one of the feature's, else nothing is
// asked. done false: not such a session, or the machine does not take it after all.
func (c *Channel) Set(ctx context.Context, r harness.Ref, ch harness.Change) (bool, error) {
	if ch.Feature != nil {
		return c.setFeature(ctx, r, *ch.Feature)
	}
	v, _ := c.cachedHeld(r.ID)
	owned := ch.Effort != "" && v.owner != ""
	if r.Live && !owned {
		return false, nil
	}
	mc, held, ok := c.heldAt(r)
	if !ok || !(held || owned) || !mc.Has(wire.CapHeldFeatures) {
		return false, nil
	}
	lang := c.h().Lang()
	p := wire.HeldSetParams{Session: r.As, Lang: lang, Model: ch.Model, Effort: ch.Effort}
	ctx, cancel := context.WithTimeout(ctx, heldSet)
	defer cancel()
	live, err := desktopSet(ctx, mc, p)
	if wire.IsCode(err, wire.CodeNotFound) || wire.IsCode(err, wire.CodeUnsupported) {
		return false, nil
	}
	if err != nil {
		return true, heldErr(err)
	}
	c.keepHeld(r.ID, mc.ID(), live, &lang)
	c.o.Logf("session %s: %s set at %s, in DSH Desktop", r.ID, map[bool]string{true: "model", false: "effort"}[ch.Model != ""], mc.ID())
	c.h().ShowAgain(r.ID)
	return true, nil
}

// setFeature switches feature f of session r at its machine (held.set). done false: no plugin
// there to ask (none of its features, then).
func (c *Channel) setFeature(ctx context.Context, r harness.Ref, f harness.FeatureChange) (bool, error) {
	mc, _, ok := c.heldAt(r)
	if !ok || !mc.Has(wire.CapHeldFeatures) {
		return false, nil
	}
	v, kept := c.cachedHeld(r.ID)
	if !kept {
		var err error
		if v, err = c.heldLive(ctx, r); err != nil {
			return true, err
		}
	}
	i := slices.IndexFunc(v.features, func(x api.Feature) bool { return x.ID == f.ID })
	if i < 0 {
		return true, fmt.Errorf("%w: no feature %q", harness.ErrBadRequest, f.ID)
	}
	x := v.features[i]
	if f.Option != "" && !slices.ContainsFunc(x.Options, func(o api.FeatureOption) bool { return o.ID == f.Option }) {
		return true, fmt.Errorf("%w: %s has no option %q", harness.ErrBadRequest, x.Name, f.Option)
	}
	// The plugin says whether it can be switched now: held there, or run by a process there.
	if !x.Available {
		if x.Reason != "" {
			return true, fmt.Errorf("%w: %s", harness.ErrBadRequest, x.Reason)
		}
		return true, fmt.Errorf("%w: %s cannot be switched now", harness.ErrBadRequest, x.Name)
	}
	ctx, cancel := context.WithTimeout(ctx, heldSet)
	defer cancel()
	lang := c.h().Lang()
	live, err := desktopSet(ctx, mc, wire.HeldSetParams{Session: r.As, Lang: lang, Feature: &wire.HeldSetFeature{ID: f.ID, On: f.On, Option: f.Option}})
	if err != nil {
		return true, heldErr(err)
	}
	c.keepHeld(r.ID, mc.ID(), live, &lang)
	c.o.Logf("session %s: %s %s at %s", r.ID, f.ID, map[bool]string{true: "on", false: "off"}[f.On], mc.ID())
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
// it, through the dsh plugin (held.send; dsh.prompt of an older one): the work goes on there, its
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
	if mc == nil {
		return "", false, nil
	}
	line := strings.TrimSpace(text)
	if !strings.HasPrefix(line, "/") {
		line = "[" + cmpOr(from.Tag, from.Terminal) + "] " + line
	}
	err := desktopSend(ctx, mc, r.As, line, steer)
	if wire.IsCode(err, wire.CodeUnsupported) || wire.IsCode(err, wire.CodeNotFound) {
		return "", false, nil // an older plugin, or Desktop holds no agent of it: the usual way says why not
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
