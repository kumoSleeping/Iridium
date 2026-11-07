package session

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/kumoSleeping/iridium/internal/agent"
	"github.com/kumoSleeping/iridium/internal/agents"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/store"
)

// A session's model, approval mode, thinking effort and ultracode are its own, remembered in the
// store: set on the running agent at once, and given to every later start. A new session's mode is the
// config's for its agent.

// SetModel sets the session's model; "" is the agent's default.
func (m *Manager) SetModel(ctx context.Context, id, model string) error {
	h, err := m.get(id)
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
	if model != "" {
		h.model = model // the next turn's init names it as the agent resolved it
	}
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
	h.rel = nil // yours now: a release does not put it back
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
	h, err := m.get(id)
	if err != nil {
		return err
	}
	info, _ := agents.Get(h.meta.Agent)
	if len(info.Presets) == 0 {
		return fmt.Errorf("%w: %s has no presets", ErrBadRequest, info.Name)
	}
	if preset != "" && !slices.Contains(info.Presets, preset) {
		return fmt.Errorf("%w: no preset %q (%s)", ErrBadRequest, preset, strings.Join(info.Presets, ", "))
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
	changed := cmp.Or(h.preset, info.DefaultPreset) != cmp.Or(preset, info.DefaultPreset)
	h.preset = preset
	h.mu.Unlock()
	h.publish()
	if changed && h.isLive() {
		h.restartForMode()
	}
	return nil
}

// SetEffort sets the session's thinking effort.
func (m *Manager) SetEffort(ctx context.Context, id, effort string) error {
	h, err := m.get(id)
	if err != nil {
		return err
	}
	e, ok := h.drv.(agent.Efforter)
	if !ok {
		return fmt.Errorf("%w: %s has no thinking effort", ErrBadRequest, h.meta.Agent)
	}
	if info, _ := agents.Get(h.meta.Agent); effort != "" && !slices.Contains(info.Efforts, effort) {
		return fmt.Errorf("%w: no effort %q (%s)", ErrBadRequest, effort, strings.Join(info.Efforts, ", "))
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
var ErrBadRequest = errors.New("bad request")

// Answer answers an open ask. Always (allowing a tool): its pattern is allowed from now on in this
// session, and other open asks it matches are allowed too.
func (m *Manager) Answer(ctx context.Context, id string, a api.Answer) error {
	h, err := m.get(id)
	if err != nil {
		return err
	}
	if !h.isLive() {
		return ErrNotLive
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
	if err := h.drv.Answer(ctx, a); err != nil {
		return err
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
	return slices.ContainsFunc(h.rules, func(r store.AllowRule) bool { return ruleMatches(r, a) })
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
