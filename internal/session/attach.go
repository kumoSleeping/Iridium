package session

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/remote/wire"
)

// What is attached to a session's page (docs/next/plugin.md): Satori elements at a place, from an
// executor's plugin (session.attach, its own until it goes), an app (PUT …/attach/{place}) or
// Iridium itself (Claude's UltraCode). Iridium reads none of it: it keeps it, in memory, shows it
// with the session, and gives a press of a button in it back to where it came from.

const (
	// FromIR is Iridium's own attachments' source.
	FromIR = "ir"
	// maxAttachText is the most one piece's text may be, a language; maxAttached, how many pieces
	// one source may attach to a session.
	maxAttachText = 4 << 10
	maxAttached   = 4
	// eventAttach and eventButton are the executor's event and the one it is sent.
	eventAttach = "session.attach"
	eventButton = "session.button"
)

var (
	placeName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,15}$`)
	langName  = regexp.MustCompile(`^([a-z]{2,3}(-[A-Za-z0-9]{2,8})?)?$`)
)

// attachments are the pieces attached to sessions, by session (Iridium's id), in order.
type attachments struct {
	mu   sync.Mutex
	list map[string][]api.Attached
}

// FromMachine and FromApp are the sources of an executor's and an app's attachments.
func FromMachine(id string) string { return "m:" + id }
func FromApp(id string) string     { return "a:" + id }

// AttachContent reads raw as a piece's content: Satori elements, or one a language; nil (or
// empty) takes it away.
func AttachContent(raw json.RawMessage) (map[string]string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var one string
	if json.Unmarshal(raw, &one) == nil {
		if one == "" {
			return nil, nil
		}
		return map[string]string{"": one}, nil
	}
	var by map[string]string
	if err := json.Unmarshal(raw, &by); err != nil {
		return nil, fmt.Errorf("%w: content: a string or one a language", ErrBadRequest)
	}
	for k, v := range by {
		if v == "" {
			delete(by, k)
		}
	}
	if len(by) == 0 {
		return nil, nil
	}
	return by, nil
}

// Attach puts content (nil: none) at place on session id, from source from. A session it may not
// see is the caller's to refuse; here only its shape is checked.
func (m *Manager) Attach(id, from, place string, content map[string]string) error {
	if !placeName.MatchString(place) {
		return fmt.Errorf("%w: place %q", ErrBadRequest, place)
	}
	for lang, text := range content {
		if !langName.MatchString(lang) || len(lang) > 12 {
			return fmt.Errorf("%w: language %q", ErrBadRequest, lang)
		}
		if len(text) > maxAttachText {
			return fmt.Errorf("%w: content over %d bytes", ErrBadRequest, maxAttachText)
		}
	}
	if len(content) > 8 {
		return fmt.Errorf("%w: at most 8 languages", ErrBadRequest)
	}
	a := &m.att
	a.mu.Lock()
	if a.list == nil {
		a.list = map[string][]api.Attached{}
	}
	l := a.list[id]
	i := slices.IndexFunc(l, func(x api.Attached) bool { return x.From == from && x.Place == place })
	switch {
	case content == nil && i < 0:
		a.mu.Unlock()
		return nil
	case content == nil:
		l = slices.Delete(l, i, i+1)
	case i >= 0:
		l[i].Content = content
	default:
		n := 0
		for _, x := range l {
			if x.From == from {
				n++
			}
		}
		if n >= maxAttached {
			a.mu.Unlock()
			return fmt.Errorf("%w: at most %d pieces a session", ErrBadRequest, maxAttached)
		}
		l = append(l, api.Attached{Place: place, From: from, Content: content})
	}
	if len(l) == 0 {
		delete(a.list, id)
	} else {
		a.list[id] = l
	}
	a.mu.Unlock()
	m.showAgain(id)
	return nil
}

// Unattach takes away everything from source from (an executor gone).
func (m *Manager) Unattach(from string) {
	a := &m.att
	a.mu.Lock()
	var ids []string
	for id, l := range a.list {
		k := slices.DeleteFunc(slices.Clone(l), func(x api.Attached) bool { return x.From == from })
		if len(k) == len(l) {
			continue
		}
		ids = append(ids, id)
		if len(k) == 0 {
			delete(a.list, id)
		} else {
			a.list[id] = k
		}
	}
	a.mu.Unlock()
	for _, id := range ids {
		m.showAgain(id)
	}
}

// attachedState puts session id's attachments into s, after Iridium's own (already there).
func (m *Manager) attachedState(id string, s *api.SessionState) {
	a := &m.att
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, x := range a.list[id] {
		s.Attached = append(s.Attached, api.Attached{Place: x.Place, From: x.From, Content: x.Content})
	}
}

// onAttach is an executor's session.attach: session is the agent's own id there (here, for one on
// this computer).
func (m *Manager) onAttach(machine string, params json.RawMessage) {
	var p wire.AttachParams
	if json.Unmarshal(params, &p) != nil || p.Session == "" {
		return
	}
	mc := m.o.Remote.Find(machine)
	if mc == nil {
		return
	}
	r, ok := m.ref(p.Session)
	at := machine
	if mc.IsLocal() {
		at = ""
	}
	if !ok || r.Machine != at {
		return
	}
	content, err := AttachContent(p.Content)
	if err == nil {
		err = m.Attach(r.ID, FromMachine(machine), p.Place, content)
	}
	if err != nil {
		m.o.Logf("executor %s: session.attach on %s: %v", machine, r.ID, err)
	}
}

// PressUser is who pressed a button attached to a session.
type PressUser struct{ ID, Name string }

// Press gives a press of button id, attached to session sid from source from, back to it: Iridium's
// own here; an executor's in session.button. app: an app's, for the caller to tell it.
func (m *Manager) Press(ctx context.Context, sid, from, id string, u PressUser) (app string, err error) {
	r, ok := m.ref(sid)
	if !ok {
		return "", ErrNotFound
	}
	switch {
	case from == FromIR:
		return "", m.pressOwn(ctx, r.ID, id)
	case strings.HasPrefix(from, "a:"):
		return strings.TrimPrefix(from, "a:"), nil
	case strings.HasPrefix(from, "m:") && m.o.Remote != nil:
		mc := m.o.Remote.Find(strings.TrimPrefix(from, "m:"))
		if mc == nil {
			return "", fmt.Errorf("%w: its executor is not connected", ErrBadRequest)
		}
		return "", mc.Notify(ctx, eventButton, wire.ButtonEvent{Session: r.As, ID: id, User: wire.ButtonUser{ID: u.ID, Name: u.Name}})
	}
	return "", fmt.Errorf("%w: no button %q", ErrBadRequest, id)
}

// ultracodeButton is Claude's UltraCode's key's id.
const ultracodeButton = "claude.ultracode"

// pressOwn is a button of Iridium's own pressed: UltraCode switched.
func (m *Manager) pressOwn(ctx context.Context, id, button string) error {
	if button != ultracodeButton {
		return fmt.Errorf("%w: no button %q", ErrBadRequest, button)
	}
	st, err := m.State(id)
	if err != nil {
		return err
	}
	on := st.Ultracode != nil && st.Ultracode.On
	return m.SetUltracode(ctx, id, !on)
}

// ultracodeTexts are UltraCode's key's words: its name, then why, by state.
var ultracodeTexts = map[string]struct{ hint, idle, unavailable string }{
	"zh": {"大任务先编排工作流再做", "已开，换到能用的模型才生效", "此模型不支持 UltraCode"},
	"en": {"Plans a workflow before big tasks", "On; takes effect on a model that can use it", "UltraCode is not available for this model"},
}

// ultracodeAttached is Claude's UltraCode as a key at the effort track: pressed while on; off and
// not available, it cannot be pressed.
func ultracodeAttached(u *api.Ultracode) api.Attached {
	content := map[string]string{}
	for lang, t := range ultracodeTexts {
		title := t.hint
		attrs := ""
		if u.On {
			attrs += " pressed"
		}
		if !u.Available {
			title = t.unavailable
			if u.On {
				title = t.idle
			} else {
				attrs += " disabled"
			}
		}
		content[lang] = fmt.Sprintf(`<button id="%s" type="action"%s title="%s">UltraCode</button>`, ultracodeButton, attrs, html.EscapeString(title))
	}
	return api.Attached{Place: "effort", From: FromIR, Content: content}
}

// SessionID is session id's own (Iridium's) id: id may be the agent's own.
func (m *Manager) SessionID(id string) (string, bool) {
	r, ok := m.ref(id)
	return r.ID, ok
}
