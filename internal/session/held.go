package session

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/kumoSleeping/iridium/internal/agents"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/remote"
	"github.com/kumoSleeping/iridium/internal/remote/wire"
)

// A held session: one at an executor's machine that another process there holds, DSH Desktop
// (loaded in it, its writer lock): Iridium cannot resume it, so it asks the holder through the
// executor (held.*, wire.CapHeldFeatures): the model and the level it runs at, and the features
// plugins there add to a session (a mode of a Desktop plugin's, the user, 2026-11-09). Iridium
// shows and switches those without knowing what they are: it reads only on, active, available,
// place and tone, and passes the rest through, checked and cut short. Claude's UltraCode is a
// feature too, its driver's (ultracodeFeature).

const (
	// ultracodeFeature is Claude's UltraCode as a feature (hosted.state, SetFeature).
	ultracodeFeature = "claude.ultracode"

	heldFor = 10 * time.Second // how long a session's live state is taken as it was told
	// held.get's: the plugin answers within 3s (its providers', each a feature's, the effort's
	// owner's: at once, not one after another), past that the way here and back.
	heldWait = 6 * time.Second
	// held.set's: a plugin's switch may take 15s, its owner and the answer after 3s each; with a
	// held.get before it (SetFeature, nothing kept), within the route's 30s.
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

// heldEntry is what Iridium knows of a session's live state at its machine: as last told (held.get,
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

// heldView is a session's wire.HeldLive, its features checked (features).
type heldView struct {
	held          bool
	model, effort string
	features      []api.Feature
}

// holdable: a holder at an executor may hold agent a's sessions (agents.Info.Holdable): the others'
// are not asked about there, nor shown a plugin's switch.
func holdable(a api.Agent) bool {
	i, ok := agents.Get(a)
	return ok && i.Holdable
}

// heldAt is where session id is asked about: its machine (connected), the id the agent there
// knows it by, and whether a holder there has it (discovery's HeldElsewhere). ok false: not a
// session a holder may have, or its machine is not connected.
func (m *Manager) heldAt(id string) (mc *remote.Machine, as string, held bool, ok bool) {
	a, machine := api.Agent(""), ""
	as = id
	if h, err := m.get(id); err == nil {
		a, machine, as = h.meta.Agent, h.meta.Machine, h.discoveredAs()
	} else if s, _, found := m.found(id); found {
		a, machine = s.Agent, s.Machine
	} else {
		return nil, "", false, false
	}
	if !holdable(a) || machine == "" || m.o.Remote == nil {
		return nil, "", false, false
	}
	if mc = m.o.Remote.Find(machine); mc == nil {
		return nil, "", false, false
	}
	if _, online := mc.Hello(); !online {
		return nil, "", false, false
	}
	if f, can := m.finder().(interface{ HeldElsewhere(string) bool }); can {
		held = f.HeldElsewhere(as)
	}
	return mc, as, held, true
}

// heldLive asks session id's machine for its live state now (held.get, in the user's language)
// and keeps it.
func (m *Manager) heldLive(ctx context.Context, id string) (heldView, error) {
	mc, as, _, ok := m.heldAt(id)
	if !ok || !mc.Has(wire.CapHeldFeatures) {
		return heldView{}, fmt.Errorf("%w: no holder to ask", ErrBadRequest)
	}
	ctx, cancel := context.WithTimeout(ctx, heldWait)
	defer cancel()
	lang := m.tuned().Lang
	live, err := mc.HeldGet(ctx, as, lang)
	if err != nil {
		return heldView{}, err
	}
	return m.keepHeld(id, mc.ID(), live, &lang), nil
}

// keepHeld keeps live as session id's, told by machine; the view kept. lang: the language it was
// asked in; nil: as before (held.changed, in the one last asked in).
func (m *Manager) keepHeld(id, machine string, live wire.HeldLive, lang *string) heldView {
	v := heldView{held: live.Held, features: m.features(machine, live.Features)}
	if live.Held {
		v.model, v.effort = live.Model, live.Effort
	}
	m.heldMu.Lock()
	defer m.heldMu.Unlock()
	e := m.heldEntryLocked(id)
	e.view, e.at, e.tried = v, time.Now(), time.Time{}
	if lang != nil {
		e.lang = *lang
	}
	return v
}

func (m *Manager) heldEntryLocked(id string) *heldEntry {
	if m.helds == nil {
		m.helds = map[string]*heldEntry{}
	}
	e := m.helds[id]
	if e == nil {
		e = &heldEntry{}
		m.helds[id] = e
	}
	return e
}

// cachedHeld is session id's live state as last told; ok false if never.
func (m *Manager) cachedHeld(id string) (heldView, bool) {
	m.heldMu.Lock()
	defer m.heldMu.Unlock()
	e := m.helds[id]
	if e == nil || e.at.IsZero() {
		return heldView{}, false
	}
	return e.view, true
}

// warmHeld asks for session id's live state in the background if what is kept is older than
// heldFor or in another language than the user's now, then shows the session again. One that
// failed is asked again after heldFor.
func (m *Manager) warmHeld(id string) {
	lang := m.tuned().Lang
	m.heldMu.Lock()
	e := m.heldEntryLocked(id)
	fresh := time.Since(e.at) < heldFor && e.lang == lang
	if e.busy || fresh || time.Since(e.tried) < heldFor {
		m.heldMu.Unlock()
		return
	}
	e.busy = true
	m.heldMu.Unlock()
	go func() {
		_, err := m.heldLive(m.ctx, id)
		m.heldMu.Lock()
		e.busy = false
		tell := err != nil && !e.fail
		e.fail = err != nil
		if err != nil {
			e.tried = time.Now() // not asked again at each look; what was told kept, none if nothing was
		}
		m.heldMu.Unlock()
		if tell {
			m.o.Logf("session %s: its holder's state: %v", id, err)
		}
		if err == nil {
			m.showAgain(id)
		}
	}()
}

// heldState puts what session s's machine says of it into s (hosted.state, foreignState): the
// features plugins there add, and while a holder there has it (not here: liveHere false), the
// model and the level it runs at. What is kept is used; if it is old, it is asked for again and
// the session shown again when it comes, so a state does not wait on the machine.
func (m *Manager) heldState(s *api.SessionState, liveHere bool) {
	if !holdable(s.Agent) || s.Machine == "" || m.o.Remote == nil {
		return
	}
	mc := m.o.Remote.Find(s.Machine)
	if mc == nil || !mc.Has(wire.CapHeldFeatures) {
		return
	}
	m.warmHeld(s.ID)
	v, ok := m.cachedHeld(s.ID)
	if !ok {
		return
	}
	s.Features = append(s.Features, v.features...)
	if !v.held || liveHere {
		return
	}
	// The holder's, over what a hosted session keeps from when it ran here (its settings): what
	// runs is Desktop's, and a level or model set here goes there (heldSetting), not to those.
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

// heldLang: the user's language is lang now (Tune): the sessions whose live state is kept are
// shown again, so each is asked in it (warmHeld) and its features' texts come in it.
func (m *Manager) heldLang() {
	m.heldMu.Lock()
	ids := make([]string, 0, len(m.helds))
	for id, e := range m.helds {
		if !e.at.IsZero() {
			ids = append(ids, id)
		}
	}
	m.heldMu.Unlock()
	if len(ids) == 0 {
		return
	}
	go func() {
		for _, id := range ids {
			m.showAgain(id)
		}
	}()
}

// showAgain publishes session id's state again, hosted or found.
func (m *Manager) showAgain(id string) {
	if m.ctx.Err() != nil {
		return
	}
	if h, err := m.get(id); err == nil {
		h.publish()
		return
	}
	if st, err := m.foreignState(id, nil); err == nil {
		m.o.Hub.Set("session:"+id, st)
	}
}

// heldTold is a held.changed from machine, waiting to be taken in.
type heldTold struct {
	machine string
	c       wire.HeldChanged
}

// onHeld is the event held.changed from machine (remote.Hub.OnHeld): taken in by its own
// goroutine, in order, so the connection's reading one waits on nothing here.
func (m *Manager) onHeld(machine string, c wire.HeldChanged) {
	m.heldMu.Lock()
	m.told = append(m.told, heldTold{machine, c})
	start := !m.taking
	m.taking = true
	m.heldMu.Unlock()
	if start {
		go m.takeTold()
	}
}

func (m *Manager) takeTold() {
	for {
		m.heldMu.Lock()
		if len(m.told) == 0 {
			m.taking = false
			m.heldMu.Unlock()
			return
		}
		t := m.told[0]
		m.told = m.told[1:]
		m.heldMu.Unlock()
		m.heldChanged(t.machine, t.c)
	}
}

// heldChanged takes in a held.changed: kept, and the session shown again, at most once in
// heldEvery (the last told). One not known here as a session at that machine a holder may have
// is let be.
func (m *Manager) heldChanged(machine string, c wire.HeldChanged) {
	id := c.Session
	if h := m.byAgentID(id); h != nil {
		id = h.id
	}
	a, at := api.Agent(""), ""
	if h, err := m.get(id); err == nil {
		a, at = h.meta.Agent, h.meta.Machine
	} else if s, _, ok := m.found(c.Session); ok {
		a, at = s.Agent, s.Machine
	}
	if !holdable(a) || at != machine {
		return
	}
	m.keepHeld(id, machine, c.Live, nil)
	m.heldMu.Lock()
	e := m.heldEntryLocked(id)
	if e.due {
		m.heldMu.Unlock()
		return // the showing that waits shows this
	}
	e.due = true
	wait := max(heldEvery-time.Since(e.shown), 0)
	m.heldMu.Unlock()
	time.AfterFunc(wait, func() {
		m.heldMu.Lock()
		e.due, e.shown = false, time.Now()
		m.heldMu.Unlock()
		m.showAgain(id)
	})
}

// features are a plugin's features as Iridium shows them: an ID of the form "<plugin>.<name>", the
// first of each; a shape it knows (V 1), at the place the page has (effort); at most maxFeatures.
// Texts are trimmed, without control characters and cut short; options at most maxOptions, the
// one picked one of them.
func (m *Manager) features(machine string, in []wire.Feature) []api.Feature {
	var out []api.Feature
	for _, f := range in {
		if !featureID.MatchString(f.ID) || f.V > 1 || f.Place != featurePlace ||
			slices.ContainsFunc(out, func(x api.Feature) bool { return x.ID == f.ID }) {
			continue
		}
		if len(out) == maxFeatures {
			m.heldMu.Lock()
			told := m.many[machine]
			if m.many == nil {
				m.many = map[string]bool{}
			}
			m.many[machine] = true
			m.heldMu.Unlock()
			if !told {
				m.o.Logf("executor %s: more than %d features on a session; the rest not shown", machine, maxFeatures)
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

// SetFeature switches feature fid of session id: Claude's UltraCode as SetUltracode does (no
// option); another, a plugin's where a holder has the session, at its machine (held.set). It must
// be one the session has as last told, its option one of the feature's: else nothing is asked.
func (m *Manager) SetFeature(ctx context.Context, id, fid string, req api.SetFeature) error {
	if fid == ultracodeFeature {
		return m.SetUltracode(ctx, id, req.On)
	}
	if !m.Known(id) {
		return ErrNotFound
	}
	mc, as, held, ok := m.heldAt(id)
	if !ok || !mc.Has(wire.CapHeldFeatures) {
		return fmt.Errorf("%w: no feature %q", ErrBadRequest, fid)
	}
	v, kept := m.cachedHeld(id)
	if !kept {
		var err error
		if v, err = m.heldLive(ctx, id); err != nil {
			return err
		}
	}
	i := slices.IndexFunc(v.features, func(f api.Feature) bool { return f.ID == fid })
	if i < 0 {
		return fmt.Errorf("%w: no feature %q", ErrBadRequest, fid)
	}
	f := v.features[i]
	if req.Option != "" && !slices.ContainsFunc(f.Options, func(o api.FeatureOption) bool { return o.ID == req.Option }) {
		return fmt.Errorf("%w: %s has no option %q", ErrBadRequest, f.Name, req.Option)
	}
	if !held && !v.held {
		if f.Reason != "" {
			return fmt.Errorf("%w: %s", ErrBadRequest, f.Reason)
		}
		return fmt.Errorf("%w: %s: the session is not open in DSH Desktop", ErrBadRequest, f.Name)
	}
	ctx, cancel := context.WithTimeout(ctx, heldSet)
	defer cancel()
	lang := m.tuned().Lang
	live, err := mc.HeldSet(ctx, wire.HeldSetParams{Session: as, Lang: lang, Feature: &wire.HeldSetFeature{ID: fid, On: req.On, Option: req.Option}})
	if err != nil {
		return heldErr(err)
	}
	m.keepHeld(id, mc.ID(), live, &lang)
	m.o.Logf("session %s: %s %s at %s", id, fid, map[bool]string{true: "on", false: "off"}[req.On], mc.ID())
	m.showAgain(id)
	return nil
}

// heldSetting sets session id's model or effort (p's) at its machine when a holder there has it
// and it does not run here (held.set): SetModel and SetEffort would refuse it (ErrElsewhere).
// done false: not such a session, or the holder there does not have it after all; the usual way
// goes.
func (m *Manager) heldSetting(ctx context.Context, id string, p wire.HeldSetParams) (done bool, err error) {
	if h, err := m.get(id); err == nil && h.isLive() {
		return false, nil
	}
	mc, as, held, ok := m.heldAt(id)
	if !ok || !held || !mc.Has(wire.CapHeldFeatures) {
		return false, nil
	}
	p.Session, p.Lang = as, m.tuned().Lang
	ctx, cancel := context.WithTimeout(ctx, heldSet)
	defer cancel()
	live, err := mc.HeldSet(ctx, p)
	if wire.IsCode(err, wire.CodeNotFound) || wire.IsCode(err, wire.CodeUnsupported) {
		return false, nil
	}
	if err != nil {
		return true, heldErr(err)
	}
	m.keepHeld(id, mc.ID(), live, &p.Lang)
	m.o.Logf("session %s: %s set at %s, in DSH Desktop", id, map[bool]string{true: "model", false: "effort"}[p.Model != ""], mc.ID())
	m.showAgain(id)
	return true, nil
}

// heldErr is a held.set's error as the session's: what the plugin turned down is a bad request.
func heldErr(err error) error {
	var e *wire.Error
	if errors.As(err, &e) && (e.Code == wire.CodeBadRequest || e.Code == wire.CodeNotFound || e.Code == wire.CodeUnsupported) {
		return fmt.Errorf("%w: %s", ErrBadRequest, e.Message)
	}
	return err
}
