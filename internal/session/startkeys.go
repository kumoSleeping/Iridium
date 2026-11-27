package session

import (
	"cmp"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/kumoSleeping/iridium/internal/agents"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/harness"
	"github.com/kumoSleeping/iridium/internal/store"
)

// Start keys: keys a session may start with pressed (api.NewSession.Attach), chosen before it
// exists and changed until its agent has it: Claude's UltraCode (its ultracode setting), else its
// channel's (harness.StartKeyer). The pick, one button's id ("" none), is kept under pickKey and
// given to the agent at its launch for a session new to it (agent.SpawnOptions.Attach); from then
// on the agent's own attachment (its plugin's) is the key. A press of a key's head (its first
// button) while nothing of it is picked picks the head, else nothing; of another button, picks it,
// or nothing if it was the pick.

// pickKey is where session id's pick is kept.
func pickKey(id string) string { return "attach:" + id }

// StartKeys are the keys a new session of agent a at machine ("" this one) may start with, all
// off.
func (m *Manager) StartKeys(a api.Agent, machine string) []api.Attached {
	if info, _ := agents.Get(a); info.Ultracode {
		return []api.Attached{ultracodeAttached(&api.Ultracode{Available: true})}
	}
	if sk, ok := channelOf[harness.StartKeyer](m, a); ok {
		return sk.StartKeys(machine)
	}
	return nil
}

// startPick is what create does with req.Attach for session id, before its agent starts.
func (m *Manager) startPick(id string, a api.Agent, machine, pick string) error {
	if pick == "" {
		return nil
	}
	if info, _ := agents.Get(a); info.Ultracode {
		if pick != ultracodeButton {
			return fmt.Errorf("%w: no key %q for %s", ErrBadRequest, pick, info.Name)
		}
		on := true
		return m.o.Store.UpdateSettings(id, func(s *store.Settings) { s.Ultracode = &on })
	}
	sk, ok := channelOf[harness.StartKeyer](m, a)
	if !ok {
		return fmt.Errorf("%w: %s has no keys to start with", ErrBadRequest, a)
	}
	// Keys not told yet (its computer not there): taken as given, the agent's to refuse.
	if keys := sk.StartKeys(machine); keys != nil && !slices.ContainsFunc(keys, func(k api.Attached) bool { return keyHas(k, pick) }) {
		return fmt.Errorf("%w: no key %q for %s", ErrBadRequest, pick, a)
	}
	return m.o.Store.Keep(pickKey(id), pick)
}

// nextPick is the pick after a press of button id of a key whose head is head, pick being the
// pick among its buttons ("" none of them).
func nextPick(pick, head, id string) string {
	switch {
	case id == head && pick == "":
		return head
	case id == head || id == pick:
		return ""
	}
	return id
}

var (
	buttonTag = regexp.MustCompile(`<button(\s[^>]*?)?(/?)>`)
	tagAttr   = regexp.MustCompile(`([A-Za-z_:][-A-Za-z0-9_:.]*)(?:\s*=\s*(?:"([^"]*)"|'([^']*)'))?`)
)

// buttonIDs are the ids of the <button>s in content, in order ("" for one with none).
func buttonIDs(content string) []string {
	var ids []string
	for _, b := range buttonTag.FindAllStringSubmatch(content, -1) {
		id := ""
		for _, a := range tagAttr.FindAllStringSubmatch(b[1], -1) {
			if a[1] == "id" {
				id = cmp.Or(a[2], a[3])
			}
		}
		ids = append(ids, id)
	}
	return ids
}

// keyButtons are key k's buttons' ids, as one of its languages has them (each has the same).
func keyButtons(k api.Attached) []string {
	langs := make([]string, 0, len(k.Content))
	for l := range k.Content {
		langs = append(langs, l)
	}
	slices.Sort(langs)
	for _, l := range langs {
		if ids := buttonIDs(k.Content[l]); len(ids) > 0 {
			return ids
		}
	}
	return nil
}

// keyHas: key k has a button id.
func keyHas(k api.Attached, id string) bool {
	return id != "" && slices.Contains(keyButtons(k), id)
}

// keyApplies: key k is for preset (none: for any; preset "" not known: taken as it is, the
// agent's to refuse).
func keyApplies(k api.Attached, preset string) bool {
	return len(k.Presets) == 0 || preset == "" || slices.Contains(k.Presets, preset)
}

// setButtons rewrites each <button> start tag in content with its pressed and disabled attributes
// as set says (head: the first).
func setButtons(content string, set func(id string, head bool) (pressed, disabled bool)) string {
	n := 0
	return buttonTag.ReplaceAllStringFunc(content, func(tag string) string {
		sub := buttonTag.FindStringSubmatch(tag)
		var b strings.Builder
		b.WriteString("<button")
		id := ""
		for _, a := range tagAttr.FindAllStringSubmatch(sub[1], -1) {
			switch a[1] {
			case "pressed", "disabled":
				continue
			case "id":
				id = cmp.Or(a[2], a[3])
			}
			b.WriteByte(' ')
			b.WriteString(a[0])
		}
		pressed, disabled := set(id, n == 0)
		n++
		if pressed {
			b.WriteString(" pressed")
		}
		if disabled {
			b.WriteString(" disabled")
		}
		b.WriteString(sub[2])
		b.WriteByte('>')
		return b.String()
	})
}

// renderKey is key k as shown for pick: its head pressed while one of its buttons is picked, and
// that one; its head disabled where it is not for the session's preset (applies false).
func renderKey(k api.Attached, pick string, applies bool) api.Attached {
	if !keyHas(k, pick) {
		pick = ""
	}
	content := make(map[string]string, len(k.Content))
	for l, text := range k.Content {
		content[l] = setButtons(text, func(id string, head bool) (bool, bool) {
			return pick != "" && (head || id == pick), head && !applies
		})
	}
	return api.Attached{Place: k.Place, From: FromIR, Content: content}
}

// startKeysLocked are session h's channel's start keys as shown for its pick while its agent has
// no session of it yet, each but where an executor attached something at its place (s.Attached:
// the agent's own key then); none else. Under h.mu.
func (h *Hosted) startKeysLocked(s *api.SessionState) []api.Attached {
	if !h.unborn {
		return nil
	}
	sk, ok := channelOf[harness.StartKeyer](h.m, h.meta.Agent)
	if !ok {
		return nil
	}
	preset := cmp.Or(h.preset, sk.DefaultPreset(h.meta.Machine))
	var out []api.Attached
	for _, k := range sk.StartKeys(h.meta.Machine) {
		if slices.ContainsFunc(s.Attached, func(a api.Attached) bool { return a.Place == k.Place && strings.HasPrefix(a.From, "m:") }) {
			continue
		}
		out = append(out, renderKey(k, h.pick, keyApplies(k, preset)))
	}
	return out
}

// launchPickLocked is the pick given to the agent at a launch for a session new to it: none where
// the key it is of is not for the session's preset. A key not told (its computer's channel not
// there yet): given as picked, the agent's to refuse. Under h.mu.
func (h *Hosted) launchPickLocked() string {
	if h.pick == "" {
		return ""
	}
	sk, ok := channelOf[harness.StartKeyer](h.m, h.meta.Agent)
	if !ok {
		return ""
	}
	preset := cmp.Or(h.preset, sk.DefaultPreset(h.meta.Machine))
	for _, k := range sk.StartKeys(h.meta.Machine) {
		if keyHas(k, h.pick) {
			if !keyApplies(k, preset) {
				return ""
			}
			break
		}
	}
	return h.pick
}

// pressStart is a press of button id of a start key Iridium shows for session sid (its agent has
// none of it yet): the pick changed. false: no such key shown.
func (m *Manager) pressStart(sid, id string) (bool, error) {
	h, err := m.get(sid)
	if err != nil {
		return false, nil
	}
	sk, ok := channelOf[harness.StartKeyer](m, h.meta.Agent)
	if !ok {
		return false, nil
	}
	h.mu.Lock()
	unborn, pick, machine := h.unborn, h.pick, h.meta.Machine
	h.mu.Unlock()
	if !unborn {
		return false, nil
	}
	for _, k := range sk.StartKeys(machine) {
		ids := keyButtons(k)
		if !slices.Contains(ids, id) {
			continue
		}
		in := pick
		if !slices.Contains(ids, in) {
			in = "" // another key's pick: this one's is none
		}
		next := nextPick(in, ids[0], id)
		if err := m.o.Store.Keep(pickKey(sid), next); err != nil {
			return true, err
		}
		h.mu.Lock()
		h.pick = next
		h.mu.Unlock()
		h.publish()
		return true, nil
	}
	return false, nil
}
