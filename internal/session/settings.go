package session

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/kumoSleeping/iridium/internal/agent"
	"github.com/kumoSleeping/iridium/internal/agents"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/harness"
	"github.com/kumoSleeping/iridium/internal/store"
)

// A session's model, approval mode, thinking effort and ultracode are its own, remembered in the
// store: set on the running agent at once, and given to every later start. A new session's mode is the
// config's for its agent.

// SetModel sets the session's model; "" is the agent's default. One another process at its machine
// holds is set there, through its channel (harness.Setter).
func (m *Manager) SetModel(ctx context.Context, id, model string) error {
	if model != "" {
		if done, err := m.channelSet(ctx, id, harness.Change{Model: model}); done {
			return err
		}
	}
	h, err := m.settable(ctx, id)
	if err != nil {
		return err
	}
	restart := false // the agent takes it only at launch (dsh): it restarts with it
	if h.isLive() {
		if err := h.drv.SetModel(ctx, cmp.Or(model, "default")); errors.Is(err, agent.ErrRestart) {
			restart = true
		} else if err != nil {
			return err
		}
	}
	if err := m.o.Store.UpdateSettings(id, func(s *store.Settings) { s.Model = model }); err != nil {
		return err
	}
	h.mu.Lock()
	h.meta.Model = model
	// The next turn's init names it as the agent resolved it; none set: the agent's own, which
	// the state names from its models till then (api.NameModel).
	h.model = model
	proc := h.proc
	h.mu.Unlock()
	h.publish()
	if restart {
		h.restartForMode()
	} else if h.isLive() {
		// A model switch may change the level in use and whether ultracode can be had.
		go h.readApplied(proc)
	}
	return nil
}

// SetMode sets the session's approval mode.
func (m *Manager) SetMode(ctx context.Context, id, mode string) error {
	h, err := m.get(id)
	if err != nil {
		return err
	}
	info, _ := agents.Get(h.meta.Agent)
	if mode != "" && !slices.Contains(info.Modes, mode) {
		return fmt.Errorf("%w: no mode %q (%s)", ErrBadRequest, mode, strings.Join(info.Modes, ", "))
	}
	restart := false
	if h.isLive() {
		if info.ModeSwitch == "restart" {
			h.mu.Lock()
			restart = mode != h.mode
			h.mu.Unlock()
		} else if err := h.drv.SetMode(ctx, mode); err != nil {
			return err
		}
	}
	if err := m.o.Store.UpdateSettings(id, func(s *store.Settings) { s.Mode = mode }); err != nil {
		return err
	}
	h.mu.Lock()
	h.meta.Mode = mode
	h.mode = mode
	h.setRelLocked(nil) // yours now: a release does not put it back
	h.mu.Unlock()
	h.publish()
	if restart {
		h.restartForMode()
	}
	return nil
}

// restartForMode restarts the agent for a mode or preset it takes only at launch (pi's mode, dsh's
// preset): now if it is idle,
// else when the turn ends. The session resumes; what waits in the queue goes after.
func (h *Hosted) restartForMode() {
	h.mu.Lock()
	idle := h.live && h.turn == agent.Idle && len(h.asks) == 0
	if !idle {
		h.restartDue = true
	}
	h.mu.Unlock()
	if idle {
		go h.restart()
	}
}

func (h *Hosted) restart() {
	ctx, cancel := context.WithTimeout(h.m.ctx, 2*h.m.o.Config.StopGrace.D()+30*time.Second)
	defer cancel()
	h.mu.Lock()
	h.restarting = true
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		h.restarting = false
		h.mu.Unlock()
	}()
	if err := h.stop(ctx); err != nil {
		h.logf("restarting: %v", err)
		return
	}
	for h.isLive() {
		select {
		case <-ctx.Done():
			h.logf("restarting: the agent did not stop")
			return
		case <-time.After(50 * time.Millisecond):
		}
	}
	h.mu.Lock()
	h.restarting = false
	h.quietNext = true // a setting's restart, not a run: not shown as starting
	h.mu.Unlock()
	if err := h.start(ctx); err != nil {
		h.logf("restarting: %v", err)
		return
	}
	h.logf("restarted for its approval mode or preset")
	h.release(ctx, false)
}

// SetPreset sets the session's agent preset (dsh's tools and prompt); "" is the agent's default.
// The agent takes it only at launch: a running one restarts with it, now if idle. Only before the
// session's first message: dsh keeps a started session's tools, a new set only as deferred
// additions, and a set all deferred (PTC's one run_code) the API turns down (dsh's own app picks a
// preset for a blank session alone).
func (m *Manager) SetPreset(ctx context.Context, id, preset string) error {
	h, err := m.settable(ctx, id)
	if err != nil {
		return err
	}
	def, err := m.checkPreset(ctx, h.meta.Agent, h.meta.Machine, preset)
	if err != nil {
		return err
	}
	h.mu.Lock()
	begun := h.said.last != nil || h.turnSeen
	h.mu.Unlock()
	if begun {
		return fmt.Errorf("%w: the preset is fixed once the session has begun", ErrBadRequest)
	}
	if err := m.o.Store.UpdateSettings(id, func(s *store.Settings) { s.Preset = preset }); err != nil {
		return err
	}
	h.mu.Lock()
	changed := cmp.Or(h.preset, def) != cmp.Or(preset, def)
	h.preset = preset
	h.mu.Unlock()
	h.publish()
	if changed && h.isLive() {
		h.restartForMode()
	}
	return nil
}

// SetEffort sets the session's thinking effort. One another process at its machine holds
// is set there, through its channel (harness.Setter).
func (m *Manager) SetEffort(ctx context.Context, id, effort string) error {
	if effort != "" {
		if done, err := m.channelSet(ctx, id, harness.Change{Effort: effort}); done {
			return err
		}
	}
	h, err := m.settable(ctx, id)
	if err != nil {
		return err
	}
	e, ok := h.drv.(agent.Efforter)
	if !ok {
		return fmt.Errorf("%w: %s has no thinking effort", ErrBadRequest, h.meta.Agent)
	}
	if levels := h.efforts(); effort != "" && levels != nil && !slices.Contains(levels, effort) {
		return fmt.Errorf("%w: no effort %q (%s)", ErrBadRequest, effort, strings.Join(levels, ", "))
	}
	h.setMu.Lock()
	defer h.setMu.Unlock()
	live, restart := h.isLive(), false
	if live && effort != "" {
		if err := e.SetEffort(ctx, effort); errors.Is(err, agent.ErrRestart) {
			restart = true // as for SetModel
		} else if err != nil {
			return err
		}
	}
	if err := m.o.Store.UpdateSettings(id, func(s *store.Settings) { s.Effort = effort }); err != nil {
		return err
	}
	h.mu.Lock()
	h.effort = effort
	proc := h.proc
	h.mu.Unlock()
	h.publish()
	if restart {
		h.restartForMode()
	} else if live {
		h.readApplied(proc)
	}
	return nil
}

// SetUltracode turns the session's ultracode on or off (Claude): on the running process, the level
// as it was, and at every later start. On is refused while its model cannot take it.
func (m *Manager) SetUltracode(ctx context.Context, id string, on bool) error {
	h, err := m.get(id)
	if err != nil {
		return err
	}
	u, ok := h.drv.(agent.Ultracoder)
	if !ok {
		return fmt.Errorf("%w: %s has no ultracode", ErrBadRequest, h.meta.Agent)
	}
	h.setMu.Lock()
	defer h.setMu.Unlock()
	live := h.isLive()
	if live {
		if err := u.SetUltracode(ctx, on); errors.Is(err, agent.ErrNoUltracode) {
			return fmt.Errorf("%w: %v", ErrBadRequest, err)
		} else if err != nil {
			return err
		}
	}
	if err := m.o.Store.UpdateSettings(id, func(s *store.Settings) { s.Ultracode = &on }); err != nil {
		return err
	}
	h.mu.Lock()
	h.ultracode = &on
	h.applied.Ultracode = on
	proc := h.proc
	h.mu.Unlock()
	h.publish()
	if live {
		h.readApplied(proc)
	}
	return nil
}

// ErrBadRequest: the request makes no sense for this session.
var ErrBadRequest = harness.ErrBadRequest

// Answer answers an open ask. Always (allowing a tool): its pattern is allowed from now on in this
// session, and other open asks it matches are allowed too. One for manual mode is Iridium's own
// (manual.go), not the agent's.
func (m *Manager) Answer(ctx context.Context, id string, a api.Answer) error {
	h, err := m.get(id)
	if err != nil {
		return err
	}
	if !h.isLive() {
		return ErrNotLive
	}
	if m.AskKind(id, a.RequestID) == AskManual {
		if a.Always {
			return fmt.Errorf("%w: approve or refuse", ErrBadRequest)
		}
		return h.answerManual(ctx, a.RequestID, a.Allow)
	}
	if a.Always {
		h.mu.Lock()
		i := slices.IndexFunc(h.asks, func(x api.Ask) bool { return x.RequestID == a.RequestID })
		var ask api.Ask
		if i >= 0 {
			ask = h.asks[i]
		}
		h.mu.Unlock()
		if i < 0 {
			return fmt.Errorf("%w: no open ask %s", ErrBadRequest, a.RequestID)
		}
		if ask.Kind != "permission" {
			return fmt.Errorf("%w: only a tool's approval can be always allowed", ErrBadRequest)
		}
		r := store.AllowRule{Tool: ask.Tool, Pattern: patternOf(ask)}
		if err := m.o.Store.AddRule(id, r); err != nil {
			return err
		}
		h.mu.Lock()
		h.rules = append(h.rules, r)
		h.mu.Unlock()
		a.Allow, a.Message = true, ""
	}
	step := m.AskKind(id, a.RequestID) == "permission"
	if err := h.drv.Answer(ctx, a); err != nil {
		return err
	}
	if step {
		h.releaseStep()
	}
	if a.Always {
		h.allowMatching()
	}
	return nil
}

// Rules are the session's "always allow" rules.
func (m *Manager) Rules(id string) ([]api.AllowRule, error) {
	h, err := m.get(id)
	if err != nil {
		return nil, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	out := []api.AllowRule{}
	for _, r := range h.rules {
		out = append(out, api.AllowRule{Tool: r.Tool, Pattern: r.Pattern})
	}
	return out, nil
}

// ClearRules forgets the session's "always allow" rules.
func (m *Manager) ClearRules(id string) error {
	h, err := m.get(id)
	if err != nil {
		return err
	}
	if err := m.o.Store.ClearRules(id); err != nil {
		return err
	}
	h.mu.Lock()
	h.rules = nil
	h.mu.Unlock()
	return nil
}

// allowMatching allows every open ask a rule matches.
func (h *Hosted) allowMatching() {
	h.mu.Lock()
	var due []string
	for _, a := range h.asks {
		if h.allowedLocked(a) {
			due = append(due, a.RequestID)
		}
	}
	h.mu.Unlock()
	for _, id := range due {
		h.allow(id)
	}
}

// allow allows ask id, as a rule says.
func (h *Hosted) allow(id string) {
	ctx, cancel := context.WithTimeout(h.m.ctx, 10*time.Second)
	defer cancel()
	if err := h.drv.Answer(ctx, api.Answer{RequestID: id, Kind: "permission", Allow: true}); err != nil {
		h.logf("always allow: %v", err)
	}
}

func (h *Hosted) allowedLocked(a api.Ask) bool {
	in := func(ch string) bool { return ch == h.id || h.m.speaks(h.id, ch) }
	return renamesOnly(a, in) || looksUpOnly(a) || speaksOnly(a, in) || reactsOnly(a, in) || slices.ContainsFunc(h.rules, func(r store.AllowRule) bool { return ruleMatches(r, a) })
}

// renamesOnly: a shell call that only names the session or a channel it is in (in: ir api
// channel.update channel_id=<id> data.name=…, plan "会话标题"; the older ir rename), which the
// system prompt has every agent run: never asked about. Whatever chains, pipes, substitutes or
// redirects is asked about as any command is, and so is one that sets anything else.
func renamesOnly(a api.Ask, in func(string) bool) bool {
	input, _ := a.Input.(map[string]any)
	s, _ := input["command"].(string)
	if s == "" {
		s, _ = input["cmd"].(string)
	}
	s = strings.TrimSpace(s)
	if a.Kind != "permission" || strings.ContainsAny(s, ";|&`$<>\n\\") {
		return false
	}
	f := strings.Fields(s)
	if len(f) >= 3 && f[0] == "ir" && f[1] == "rename" {
		return true
	}
	if len(f) < 5 || f[0] != "ir" || f[1] != "api" || f[2] != "channel.update" {
		return false
	}
	named, ch := false, 0
	for _, w := range f[3:] {
		named = named || strings.HasPrefix(w, "data.name=")
		if strings.HasPrefix(w, "channel_id") {
			id, ok := strings.CutPrefix(w, "channel_id=")
			if !ok || !in(id) {
				return false
			}
			ch++
		}
		if strings.HasPrefix(w, "-") || strings.HasPrefix(w, "_data") || strings.HasPrefix(w, "content") {
			return false
		}
	}
	return named && ch == 1
}

// looksUpOnly: a shell call that only reads Iridium's records of the agent itself or keeps a
// note of its own (docs/channels.md "ir 工具", "记忆"): ir api [GET] internal/self,
// internal/memory, internal/roles, internal/members/<who>, internal/recall q=… and
// internal/remember text=…; never asked about. One simple command, its words as speaksOnly's.
func looksUpOnly(a api.Ask) bool {
	input, _ := a.Input.(map[string]any)
	s, _ := input["command"].(string)
	if s == "" {
		s, _ = input["cmd"].(string)
	}
	if a.Kind != "permission" {
		return false
	}
	f, ok := shellWords(strings.TrimSpace(s))
	if !ok || len(f) < 3 || f[0] != "ir" || f[1] != "api" {
		return false
	}
	f = f[2:]
	get := false
	if f[0] == "GET" {
		get, f = true, f[1:]
	}
	if len(f) == 0 {
		return false
	}
	path, args := f[0], f[1:]
	switch {
	case path == "internal/self" || path == "internal/memory" || path == "internal/roles" || (get && strings.HasPrefix(path, "internal/members/")):
		return len(args) == 0
	case path == "internal/recall" && !get:
		for _, w := range args {
			if !strings.HasPrefix(w, "q=") && w != "all:=true" && !strings.HasPrefix(w, "limit:=") {
				return false
			}
		}
		return len(args) > 0
	case path == "internal/remember" && !get:
		return len(args) == 1 && strings.HasPrefix(args[0], "text=")
	}
	return false
}

// speaksOnly: a shell call that only says something in a channel the agent is in (in: its own,
// or one it is in by a subscription, docs/channels.md), which is how such an agent speaks
// there: never asked about. One way of writing it (kumo, 2026-11-15: no k=@-):
//
//	ir api message.create channel_id=<id> content='<words>'      (or _data.text=)
//
// The words bare, in single quotes (over several lines, a ' in them as '\”), or in double
// quotes with no $, ` or \ in them: nothing the shell runs or substitutes. Nothing may chain,
// pipe or redirect; any other field (a flag, a file, an attachment, JSON) is asked about as any
// command is.
func speaksOnly(a api.Ask, in func(channel string) bool) bool {
	input, _ := a.Input.(map[string]any)
	s, _ := input["command"].(string)
	if s == "" {
		s, _ = input["cmd"].(string)
	}
	if a.Kind != "permission" {
		return false
	}
	f, ok := shellWords(strings.TrimSpace(s))
	if !ok || len(f) < 5 || f[0] != "ir" || f[1] != "api" || f[2] != "message.create" {
		return false
	}
	channels, words := 0, 0
	for _, w := range f[3:] {
		switch {
		case strings.HasPrefix(w, "channel_id="):
			channels++
			if !in(strings.TrimPrefix(w, "channel_id=")) {
				return false
			}
		case strings.HasPrefix(w, "content=") || strings.HasPrefix(w, "_data.text="):
			words++
		default:
			return false
		}
	}
	return channels == 1 && words == 1
}

// reactsOnly: a shell call that only puts an emoji on a message of a channel the agent is in
// (docs/channels.md "表情"), as speaksOnly speaks there: never asked about.
//
//	ir api reaction.create channel_id=<id> message_id=<number> emoji=👍      (or emoji_id=)
func reactsOnly(a api.Ask, in func(channel string) bool) bool {
	input, _ := a.Input.(map[string]any)
	s, _ := input["command"].(string)
	if s == "" {
		s, _ = input["cmd"].(string)
	}
	if a.Kind != "permission" {
		return false
	}
	f, ok := shellWords(strings.TrimSpace(s))
	if !ok || len(f) != 6 || f[0] != "ir" || f[1] != "api" || f[2] != "reaction.create" {
		return false
	}
	var channels, messages, emoji int
	for _, w := range f[3:] {
		k, v, _ := strings.Cut(w, "=")
		switch k {
		case "channel_id":
			channels++
			if !in(v) {
				return false
			}
		case "message_id":
			messages++
			if _, err := strconv.ParseInt(v, 10, 64); err != nil {
				return false
			}
		case "emoji", "emoji_id":
			emoji++
		default:
			return false
		}
	}
	return channels == 1 && messages == 1 && emoji == 1
}

// shellWords splits s as sh would into words, if it is one simple command whose words need
// nothing run, substituted or expanded: bare (letters, digits, non-ASCII and -_.=:/@,+%),
// '…' (anything but '), "…" (no $, ` or \ inside) and \' outside quotes, joined; blanks
// between them. ok false for anything else (; | & < > $ ` ( ) a glob, a line end outside
// quotes, an unclosed quote).
func shellWords(s string) (words []string, ok bool) {
	var cur strings.Builder
	in := false // a word has begun
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch {
		case r == ' ' || r == '\t':
			if in {
				words, in = append(words, cur.String()), false
				cur.Reset()
			}
		case r == '\'':
			j := i + 1
			for j < len(rs) && rs[j] != '\'' {
				j++
			}
			if j == len(rs) {
				return nil, false
			}
			cur.WriteString(string(rs[i+1 : j]))
			i, in = j, true
		case r == '"':
			j := i + 1
			for j < len(rs) && rs[j] != '"' {
				if rs[j] == '$' || rs[j] == '`' || rs[j] == '\\' {
					return nil, false
				}
				j++
			}
			if j == len(rs) {
				return nil, false
			}
			cur.WriteString(string(rs[i+1 : j]))
			i, in = j, true
		case r == '\\':
			if i+1 >= len(rs) || rs[i+1] != '\'' {
				return nil, false
			}
			cur.WriteRune('\'')
			i, in = i+1, true
		case r > 127 || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_.=:/@,+%", r):
			cur.WriteRune(r)
			in = true
		default:
			return nil, false
		}
	}
	if in {
		words = append(words, cur.String())
	}
	return words, len(words) > 0
}

// fileTools are the tools whose subject is a file path.
var fileTools = map[string]string{"Read": "file_path", "Edit": "file_path", "MultiEdit": "file_path", "Write": "file_path", "NotebookEdit": "notebook_path"}

// subjectOf is what a rule's pattern is matched against: a command, a file path; "" for other
// tools, which only a rule without a pattern allows.
func subjectOf(a api.Ask) string {
	in, _ := a.Input.(map[string]any)
	if a.Tool == "Bash" {
		s, _ := in["command"].(string)
		return s
	}
	if k, ok := fileTools[a.Tool]; ok {
		s, _ := in[k].(string)
		return s
	}
	return ""
}

// patternOf is what "always allow" remembers for an ask: the agent's own suggestion, else the
// command's first word for a shell command, else every call of the tool.
func patternOf(a api.Ask) string {
	if a.Pattern != "" {
		return a.Pattern
	}
	if a.Tool == "Bash" {
		if f := strings.Fields(subjectOf(a)); len(f) > 0 {
			return f[0] + " *"
		}
	}
	return ""
}

// ruleMatches: '*' in a pattern is any run of characters; Claude's older "prefix:*" is "prefix*".
// A shell command that chains another (; && || | ` $( or a newline) never matches a pattern, so
// "git status *" does not allow "git status; rm -rf ~".
func ruleMatches(r store.AllowRule, a api.Ask) bool {
	if a.Kind != "permission" || r.Tool != a.Tool {
		return false
	}
	if r.Pattern == "" {
		return true
	}
	s := subjectOf(a)
	if s == "" {
		return false
	}
	if a.Tool == "Bash" && (strings.ContainsAny(s, ";|`\n") || strings.Contains(s, "&&") || strings.Contains(s, "$(")) {
		return false
	}
	p := r.Pattern
	if prefix, ok := strings.CutSuffix(p, ":*"); ok {
		p = prefix + "*"
	}
	return glob(p, s)
}

func glob(p, s string) bool {
	parts := strings.Split(p, "*")
	if len(parts) == 1 {
		return p == s
	}
	if !strings.HasPrefix(s, parts[0]) {
		return false
	}
	s = s[len(parts[0]):]
	for _, part := range parts[1 : len(parts)-1] {
		i := strings.Index(s, part)
		if i < 0 {
			return false
		}
		s = s[i+len(part):]
	}
	return strings.HasSuffix(s, parts[len(parts)-1])
}

// efforts are the levels the session's effort may be: its agent's; for one whose efforts are each
// model's, those of the model in use as the agent offered it. nil: not known (the agent checks).
func (h *Hosted) efforts() []string {
	info, _ := agents.Get(h.meta.Agent)
	if !info.ModelEfforts {
		return info.Efforts
	}
	h.mu.Lock()
	models, now := h.models, cmp.Or(h.model, h.meta.Model)
	h.mu.Unlock()
	if mo, ok := channelOf[harness.Models](h.m, h.meta.Agent); ok {
		if cat, ok := mo.Cached(h.meta.Machine); ok { // its machine's, as the page lists them
			models, now = cat.Models, cmp.Or(now, cat.Default)
		}
	}
	for _, m := range models {
		if m.Value == now {
			if m.NoEffort && len(m.Efforts) == 0 {
				return []string{}
			}
			return m.Efforts // pi's that does not reason: off alone
		}
	}
	return nil
}

// settable is the hosted session a setting goes to: one found on disk (another app's) is taken
// up first, as a message would, without starting its agent: its model, effort or preset is set
// before the first message there (the user, 2026-11-09).
func (m *Manager) settable(ctx context.Context, id string) (*Hosted, error) {
	if h, err := m.get(id); err == nil {
		return h, nil
	}
	if _, _, ok := m.found(id); !ok {
		return nil, ErrNotFound
	}
	h, err := m.take(ctx, id)
	if err != nil {
		return nil, err
	}
	m.Refresh([]string{id})
	return h, nil
}
