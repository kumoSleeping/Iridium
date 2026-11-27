package session

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/kumoSleeping/iridium/internal/agent"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/config"
	"github.com/kumoSleeping/iridium/internal/harness"
)

// starter is a channel with start keys (harness.StartKeyer) that keeps the pick each launch was
// given.
type starter struct {
	mu       sync.Mutex
	keys     []api.Attached
	def      string
	attaches []string
}

func (k *starter) Agent() api.Agent  { return api.AgentAgy }
func (k *starter) Bind(harness.Host) {}
func (k *starter) StartKeys(string) []api.Attached {
	k.mu.Lock()
	defer k.mu.Unlock()
	return slices.Clone(k.keys)
}
func (k *starter) DefaultPreset(string) string {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.def
}
func (k *starter) Prepare(_ context.Context, _ harness.Ref, o *agent.SpawnOptions) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.attaches = append(k.attaches, o.Attach)
	return nil
}
func (k *starter) given() []string {
	k.mu.Lock()
	defer k.mu.Unlock()
	return slices.Clone(k.attaches)
}

const ultraKey = `<button id="o.u" type="action" title="a &gt; b">Ultra</button><button id="o.u.w" pressed>W</button><button id="o.u.t"/>`

// A key as shown for a pick: its head and the pick pressed, nothing else; its head disabled where
// it is not for the session's preset.
func TestRenderKey(t *testing.T) {
	k := api.Attached{Place: "effort", From: "m:x", Content: map[string]string{"en": ultraKey}, Presets: []string{"p"}}
	for _, c := range []struct {
		pick    string
		applies bool
		want    string
	}{
		{"", true, `<button id="o.u" type="action" title="a &gt; b">Ultra</button><button id="o.u.w">W</button><button id="o.u.t"/>`},
		{"o.u.t", true, `<button id="o.u" type="action" title="a &gt; b" pressed>Ultra</button><button id="o.u.w">W</button><button id="o.u.t" pressed/>`},
		{"o.u", true, `<button id="o.u" type="action" title="a &gt; b" pressed>Ultra</button><button id="o.u.w">W</button><button id="o.u.t"/>`},
		{"other", false, `<button id="o.u" type="action" title="a &gt; b" disabled>Ultra</button><button id="o.u.w">W</button><button id="o.u.t"/>`},
		{"o.u.w", false, `<button id="o.u" type="action" title="a &gt; b" pressed disabled>Ultra</button><button id="o.u.w" pressed>W</button><button id="o.u.t"/>`},
	} {
		got := renderKey(k, c.pick, c.applies)
		if got.Content["en"] != c.want || got.From != FromIR || got.Place != "effort" || got.Presets != nil {
			t.Errorf("%q %v: %+v", c.pick, c.applies, got)
		}
	}
	if ids := keyButtons(k); !slices.Equal(ids, []string{"o.u", "o.u.w", "o.u.t"}) {
		t.Errorf("buttons: %q", ids)
	}
}

// The page's cycle on a key: off, a sub, the next, the head (off); the head alone on and off.
func TestNextPick(t *testing.T) {
	pick := ""
	for _, c := range []struct{ press, want string }{
		{"o.u.w", "o.u.w"}, {"o.u.t", "o.u.t"}, {"o.u", ""}, {"o.u", "o.u"}, {"o.u", ""}, {"o.u.t", "o.u.t"}, {"o.u.t", ""},
	} {
		if pick = nextPick(pick, "o.u", c.press); pick != c.want {
			t.Fatalf("press %s: %q, want %q", c.press, pick, c.want)
		}
	}
}

// A session started with a key picked: given to its agent at its first launch alone; until its
// first turn the key is Iridium's to show and a press of it changes the pick (one of all its keys);
// an executor's own at its place hides it.
func TestStartKeys(t *testing.T) {
	t.Parallel()
	k := &starter{keys: []api.Attached{
		{Place: "effort", From: "", Content: map[string]string{"en": ultraKey}, Presets: []string{"p1"}},
		{Place: "mode", Content: map[string]string{"": `<button id="k2.on">K</button>`}},
	}, def: "p1"}
	r := newRig(t, func(_ *config.Config, o *Options) { o.Channels = harness.Set{api.AgentAgy: k} })
	if got := r.m.StartKeys(api.AgentAgy, ""); len(got) != 2 {
		t.Errorf("start keys: %+v", got)
	}
	if _, err := r.m.Create(bg, api.NewSession{Agent: api.AgentAgy, Cwd: r.cwd, Attach: "nope"}, "网页"); !errors.Is(err, ErrBadRequest) {
		t.Errorf("a key it has not: %v", err)
	}
	if _, err := r.m.Create(bg, api.NewSession{Agent: api.AgentCodex, Cwd: r.cwd, Attach: "o.u"}, "网页"); !errors.Is(err, ErrBadRequest) {
		t.Errorf("an agent with none: %v", err)
	}
	id, err := r.m.Create(bg, api.NewSession{Agent: api.AgentAgy, Cwd: r.cwd, Attach: "o.u.w"}, "网页")
	if err != nil {
		t.Fatal(err)
	}
	r.waitState(id, "live", func(s *api.SessionState) bool { return s.Live })
	if g := k.given(); !slices.Equal(g, []string{"o.u.w"}) {
		t.Fatalf("given at launch: %q", g)
	}
	shown := func(place string) string {
		t.Helper()
		s, err := r.m.State(id)
		if err != nil {
			t.Fatal(err)
		}
		for _, a := range s.Attached {
			if a.Place == place && a.From == FromIR {
				return a.Content["en"] + a.Content[""]
			}
		}
		return "-"
	}
	press := func(button string) {
		t.Helper()
		if _, err := r.m.Press(bg, id, FromIR, button, PressUser{}); err != nil {
			t.Fatalf("press %s: %v", button, err)
		}
	}
	pressed := func(place string) []string {
		t.Helper()
		var out []string
		for _, b := range buttonTag.FindAllStringSubmatch(shown(place), -1) {
			id, on := "", false
			for _, a := range tagAttr.FindAllStringSubmatch(b[1], -1) {
				switch a[1] {
				case "id":
					id = a[2]
				case "pressed":
					on = true
				}
			}
			if on {
				out = append(out, id)
			}
		}
		return out
	}
	if p := pressed("effort"); !slices.Equal(p, []string{"o.u", "o.u.w"}) {
		t.Errorf("as created: %q in %s", p, shown("effort"))
	}
	press("o.u.t")
	if p := pressed("effort"); !slices.Equal(p, []string{"o.u", "o.u.t"}) || r.store.Kept(pickKey(id)) != "o.u.t" {
		t.Errorf("the next sub: %q", p)
	}
	press("o.u")
	if p := pressed("effort"); p != nil || r.store.Kept(pickKey(id)) != "" {
		t.Errorf("the head, off: %q", p)
	}
	press("o.u")
	if p := pressed("effort"); !slices.Equal(p, []string{"o.u"}) {
		t.Errorf("the head, on: %q", p)
	}
	press("k2.on") // the other key: this one's off
	if p, q := pressed("effort"), pressed("mode"); p != nil || !slices.Equal(q, []string{"k2.on"}) {
		t.Errorf("switched: %q %q", p, q)
	}
	press("k2.on")
	if q := pressed("mode"); q != nil {
		t.Errorf("the other off: %q", q)
	}
	press("o.u.w")

	// Not for the preset the session runs with: its head disabled, the pick not given.
	k.mu.Lock()
	k.def = "p0"
	k.mu.Unlock()
	if s := shown("effort"); !strings.Contains(s, "disabled") {
		t.Errorf("another preset: %s", s)
	}
	h, _ := r.m.get(id)
	h.mu.Lock()
	if p := h.launchPickLocked(); p != "" {
		t.Errorf("given for another preset: %q", p)
	}
	h.mu.Unlock()
	// Its preset not known (none kept): taken as it is, the agent's to refuse.
	k.mu.Lock()
	k.def = ""
	k.mu.Unlock()
	if s := shown("effort"); strings.Contains(s, "disabled") {
		t.Errorf("preset not known: %s", s)
	}
	h.mu.Lock()
	if p := h.launchPickLocked(); p != "o.u.w" {
		t.Errorf("preset not known: given %q", p)
	}
	h.mu.Unlock()
	k.mu.Lock()
	k.def = "p1"
	k.mu.Unlock()

	// An executor's own at the place: its, not Iridium's.
	if err := r.m.Attach(id, FromMachine("x"), "effort", map[string]string{"": "<b>x</b>"}); err != nil {
		t.Fatal(err)
	}
	if s := shown("effort"); s != "-" {
		t.Errorf("beside the executor's: %s", s)
	}
	_ = r.m.Attach(id, FromMachine("x"), "effort", nil)

	// Once it has run a turn: none shown, a press refused; the next launch gives none.
	if _, err := r.m.Send(bg, id, "hello", "网页"); err != nil {
		t.Fatal(err)
	}
	r.waitState(id, "the reply", func(s *api.SessionState) bool {
		return s.Status == api.StatusIdle && s.Last != nil && s.Last.Text == "ok"
	})
	if s := shown("effort"); s != "-" {
		t.Errorf("after a turn: %s", s)
	}
	if _, err := r.m.Press(bg, id, FromIR, "o.u", PressUser{}); !errors.Is(err, ErrBadRequest) {
		t.Errorf("a press after a turn: %v", err)
	}
	if err := r.m.Stop(bg, id); err != nil {
		t.Fatal(err)
	}
	r.waitState(id, "the stop", func(s *api.SessionState) bool { return !s.Live })
	if _, err := r.m.Send(bg, id, "two", "网页"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the second launch", func() bool { return len(k.given()) == 2 })
	if g := k.given(); g[1] != "" {
		t.Errorf("given again: %q", g)
	}
}

// Claude started with UltraCode: its setting on before it starts; its one key, off, among the
// choices; another key refused.
func TestStartUltracode(t *testing.T) {
	t.Parallel()
	r := newRig(t, nil)
	keys := r.m.StartKeys(api.AgentClaude, "")
	if len(keys) != 1 || keys[0].From != FromIR || !strings.Contains(keys[0].Content["en"], `id="claude.ultracode"`) || strings.Contains(keys[0].Content["en"], "pressed") {
		t.Errorf("claude's keys: %+v", keys)
	}
	if _, err := r.m.Create(bg, api.NewSession{Cwd: r.cwd, Attach: "o.u"}, "网页"); !errors.Is(err, ErrBadRequest) {
		t.Errorf("another key: %v", err)
	}
	id, err := r.m.Create(bg, api.NewSession{Cwd: r.cwd, Attach: ultracodeButton}, "网页")
	if err != nil {
		t.Fatal(err)
	}
	if st, err := r.store.Settings(id); err != nil || st.Ultracode == nil || !*st.Ultracode {
		t.Errorf("settings: %+v %v", st, err)
	}
	if s, _ := r.m.State(id); s.Ultracode == nil || !s.Ultracode.On {
		t.Errorf("state: %+v", s.Ultracode)
	}
}
