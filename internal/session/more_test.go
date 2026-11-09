package session

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/config"
	"github.com/kumoSleeping/iridium/internal/hold"
	"github.com/kumoSleeping/iridium/internal/store"
)

var bg = context.Background()

func (r *rig) inLog(id string) string {
	b, _ := os.ReadFile(filepath.Join(r.cfg.StatePath(), "hold", id, hold.FileInLog))
	return string(b)
}

func said(s *api.SessionState, text string) bool { return s.Last != nil && s.Last.Text == text }

// A message sent while a turn runs waits for it, then goes as the next turn; one can be steered
// into the running turn, edited or deleted meanwhile.
func TestQueue(t *testing.T) {
	t.Parallel()
	r := newRig(t, nil)
	id, _ := r.m.Create(bg, api.NewSession{Cwd: r.cwd, Text: "one WAIT"}, "网页")
	r.waitState(id, "the turn", func(s *api.SessionState) bool { return s.Status == api.StatusRunning })
	a, queued, err := r.m.SendWith(bg, id, "two", "网页", false)
	if err != nil || !queued {
		t.Fatalf("queued %v: %v", queued, err)
	}
	b, _, _ := r.m.SendWith(bg, id, "three", "网页", false)
	c, _, _ := r.m.SendWith(bg, id, "gone", "网页", false)
	s, _ := r.m.State(id)
	if len(s.Queue) != 3 || s.Queue[0].ID != a || s.Queue[0].From != "网页" || s.Queue[0].Paused {
		t.Fatalf("queue: %+v", s.Queue)
	}
	if err := r.m.EditQueued(id, b, "three, edited"); err != nil {
		t.Fatal(err)
	}
	if err := r.m.DeleteQueued(id, c); err != nil {
		t.Fatal(err)
	}
	if err := r.m.DeleteQueued(id, c); err != store.ErrNoInput {
		t.Errorf("deleted twice: %v", err)
	}
	// Steered, "two" joins the WAIT turn; the edited "three" waits for its end.
	if err := r.m.Steer(bg, id, a); err != nil {
		t.Fatal(err)
	}
	s = r.waitState(id, "the next turn", func(s *api.SessionState) bool { return said(s, "ok: three, edited") && s.Status == api.StatusIdle })
	if len(s.Queue) != 0 {
		t.Errorf("queue after: %+v", s.Queue)
	}
	var texts []string
	for _, b := range r.messages(id) {
		texts = append(texts, b.Text)
	}
	if got := strings.Join(texts, "|"); got != "one WAIT|two|ok: one WAIT|ok: two|three, edited|ok: three, edited" {
		t.Errorf("blocks: %s", got)
	}
	if strings.Contains(r.inLog(id), "gone") {
		t.Error("a deleted message was sent")
	}
	// Steer: true joins at once. "four WAIT" goes straight too: the fake writes its transcript
	// ahead of out.log, so the turn above may not have ended for the driver yet, and a plain send
	// would wait in the queue behind it.
	r.m.SendWith(bg, id, "four WAIT", "网页", true)
	r.waitState(id, "the turn", func(s *api.SessionState) bool { return s.Status == api.StatusRunning })
	if _, queued, err := r.m.SendWith(bg, id, "five", "网页", true); queued || err != nil {
		t.Errorf("steer: queued %v, %v", queued, err)
	}
	r.waitState(id, "the joined reply", func(s *api.SessionState) bool { return said(s, "ok: five") })
	if b := r.messages(id); b[len(b)-2].Text != "ok: four WAIT" {
		t.Errorf("five did not join four's turn: %+v", b[len(b)-4:])
	}
}

// An interrupt pauses the queue: it goes only on continue.
func TestQueuePaused(t *testing.T) {
	t.Parallel()
	r := newRig(t, nil)
	id, _ := r.m.Create(bg, api.NewSession{Cwd: r.cwd, Text: "one WAIT"}, "网页")
	r.waitState(id, "the turn", func(s *api.SessionState) bool { return s.Status == api.StatusRunning })
	r.m.SendWith(bg, id, "two", "网页", false)
	if err := r.m.Interrupt(bg, id); err != nil {
		t.Fatal(err)
	}
	s := r.waitState(id, "the interrupt", func(s *api.SessionState) bool { return s.Status == api.StatusIdle })
	time.Sleep(200 * time.Millisecond)
	s, _ = r.m.State(id)
	if len(s.Queue) != 1 || !s.Queue[0].Paused {
		t.Fatalf("queue: %+v", s.Queue)
	}
	// Paused in the store too: a new daemon finds it so.
	if q, _ := r.store.Queued(id); len(q) != 1 || q[0].State != "paused" {
		t.Errorf("stored: %+v", q)
	}
	if err := r.m.Continue(bg, id); err != nil {
		t.Fatal(err)
	}
	s = r.waitState(id, "the queue sent", func(s *api.SessionState) bool { return said(s, "ok: two") })
	if len(s.Queue) != 0 {
		t.Errorf("queue: %+v", s.Queue)
	}
}

func TestSettings(t *testing.T) {
	t.Parallel()
	r := newRig(t, func(c *config.Config, _ *Options) { c.Modes = map[string]string{"claude": "acceptEdits"} })
	id, _ := r.m.Create(bg, api.NewSession{Cwd: r.cwd, Text: "hi"}, "网页")
	s := r.waitState(id, "the reply", func(s *api.SessionState) bool { return said(s, "ok: hi") })
	if s.Mode != "acceptEdits" || len(s.Models) != 2 || len(s.Modes) != 6 || len(s.Efforts) != 5 {
		t.Fatalf("state: mode %q models %v modes %v efforts %v", s.Mode, s.Models, s.Modes, s.Efforts)
	}
	// The commands every session has, for the palette, without words (the page says them in its
	// language); the agent's argument hint kept.
	var names []string
	for _, c := range s.Commands {
		names = append(names, c.Name)
	}
	if strings.Join(names, " ") != "clear compact context usage model effort mode" || s.Commands[1] != (api.Command{Name: "compact", ArgumentHint: "<instructions>"}) {
		t.Fatalf("commands: %+v", s.Commands)
	}
	// The default mode changed while it runs: the next new session takes it.
	tu := r.m.Tuned()
	tu.Modes = map[string]string{"claude": "plan"}
	r.m.Tune(tu)
	id2, _ := r.m.Create(bg, api.NewSession{Cwd: r.cwd, Text: "hi"}, "网页")
	if s2 := r.waitState(id2, "the reply", func(s *api.SessionState) bool { return said(s, "ok: hi") }); s2.Mode != "plan" {
		t.Fatalf("tuned default mode: %q", s2.Mode)
	}
	if err := r.m.SetModel(bg, id, "haiku"); err != nil {
		t.Fatal(err)
	}
	if err := r.m.SetMode(bg, id, "plan"); err != nil {
		t.Fatal(err)
	}
	if err := r.m.SetEffort(bg, id, "high"); err != nil {
		t.Fatal(err)
	}
	if err := r.m.SetMode(bg, id, "nope"); err == nil {
		t.Error("set an unknown mode")
	}
	in := r.inLog(id)
	for _, want := range []string{`"model":"haiku","subtype":"set_model"`, `"mode":"plan","subtype":"set_permission_mode"`, `"settings":{"effortLevel":"high","ultracode":false},"subtype":"apply_flag_settings"`} {
		if !strings.Contains(in, want) {
			t.Errorf("in.log lacks %s", want)
		}
	}
	s, _ = r.m.State(id)
	if s.Mode != "plan" || s.Effort != "high" {
		t.Errorf("state: %+v", s)
	}
	// Remembered: the next process starts with them.
	r.m.Stop(bg, id)
	s = r.waitState(id, "the stop", func(s *api.SessionState) bool { return !s.Live })
	// What the process applied went with it: haiku's "cannot" no longer holds the switch.
	if s.Ultracode == nil || !s.Ultracode.Available || s.Ultracode.Active || s.Ultracode.On {
		t.Errorf("stopped: %+v", s.Ultracode)
	}
	r.m.Send(bg, id, "again", "网页")
	r.waitState(id, "the second reply", func(s *api.SessionState) bool { return said(s, "ok: again") })
	spec, _ := os.ReadFile(filepath.Join(r.cfg.StatePath(), "hold", id, hold.FileSpec))
	for _, want := range []string{`"--model","haiku"`, `"--permission-mode","plan"`, `"--effort","high"`} {
		if !strings.Contains(string(spec), want) {
			t.Errorf("spec lacks %s: %s", want, spec)
		}
	}
}

// A new Claude session says the model and the level in use before any turn (get_settings);
// ultracode is set alone, remembered, given to the next start, and refused where the model cannot
// take it.
func TestAppliedAndUltracode(t *testing.T) {
	t.Parallel()
	r := newRig(t, nil)
	id, err := r.m.Create(bg, api.NewSession{Cwd: r.cwd}, "网页")
	if err != nil {
		t.Fatal(err)
	}
	s := r.waitState(id, "the settings applied", func(s *api.SessionState) bool { return s.EffortNow != "" })
	if s.Model != "fake-model" || s.EffortNow != "medium" || s.Effort != "" || s.Pick != "" || s.Ultracode == nil || s.Ultracode.On || !s.Ultracode.Available {
		t.Fatalf("state: model %q effortNow %q effort %q pick %q ultracode %+v", s.Model, s.EffortNow, s.Effort, s.Pick, s.Ultracode)
	}
	if len(s.Models) != 2 || s.Models[0].Resolved != "fake-model" || len(s.Models[0].Efforts) != 5 || !s.Models[1].NoEffort {
		t.Fatalf("models: %+v", s.Models)
	}
	if err := r.m.SetUltracode(bg, id, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.inLog(id), `"settings":{"ultracode":true},"subtype":"apply_flag_settings"`) {
		t.Errorf("in.log: %s", r.inLog(id))
	}
	s = r.waitState(id, "ultracode on", func(s *api.SessionState) bool { return s.Ultracode.On && s.Ultracode.Active })
	// The level set keeps ultracode as it was.
	if err := r.m.SetEffort(bg, id, "high"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.inLog(id), `"settings":{"effortLevel":"high","ultracode":true}`) {
		t.Errorf("in.log: %s", r.inLog(id))
	}
	s = r.waitState(id, "the level applied", func(s *api.SessionState) bool { return s.EffortNow == "high" })
	// A model switch: what it applies is read again; it cannot take ultracode.
	if err := r.m.SetModel(bg, id, "haiku"); err != nil {
		t.Fatal(err)
	}
	s = r.waitState(id, "haiku applied", func(s *api.SessionState) bool { return s.Model == "fake-haiku" })
	if s.Pick != "haiku" || s.Ultracode.Available || s.Ultracode.Active || !s.Ultracode.On {
		t.Errorf("after the switch: pick %q ultracode %+v", s.Pick, s.Ultracode)
	}
	// The same as a feature.
	if f := s.Features; len(f) != 1 || f[0].ID != "claude.ultracode" || f[0].Available || f[0].Active || !f[0].On || f[0].Place != "effort" || f[0].Tone != "warm" {
		t.Errorf("features: %+v", f)
	}
	if err := r.m.SetUltracode(bg, id, true); !errors.Is(err, ErrBadRequest) {
		t.Errorf("ultracode on haiku: %v", err)
	}
	if err := r.m.SetFeature(bg, id, "claude.ultracode", api.SetFeature{On: true}); !errors.Is(err, ErrBadRequest) {
		t.Errorf("ultracode on haiku, as a feature: %v", err)
	}
	if err := r.m.SetFeature(bg, id, "x.other", api.SetFeature{On: true}); !errors.Is(err, ErrBadRequest) {
		t.Errorf("a feature it has not: %v", err)
	}
	if err := r.m.SetUltracode(bg, id, false); err != nil {
		t.Fatal(err)
	}
	if st, _ := r.store.Settings(id); st.Ultracode == nil || *st.Ultracode {
		t.Errorf("stored: %+v", st)
	}
	// Remembered: the next process starts with it, off over the user's own settings.
	r.m.Stop(bg, id)
	r.waitState(id, "the stop", func(s *api.SessionState) bool { return !s.Live })
	r.m.Send(bg, id, "again", "网页")
	r.waitState(id, "the reply", func(s *api.SessionState) bool { return said(s, "ok: again") })
	spec, _ := os.ReadFile(filepath.Join(r.cfg.StatePath(), "hold", id, hold.FileSpec))
	if !strings.Contains(string(spec), `"--settings","{\"ultracode\":false}"`) {
		t.Errorf("spec: %s", spec)
	}
}

// "Always allow" remembers the tool and pattern; the next matching ask is answered by itself.
func TestAlwaysAllow(t *testing.T) {
	t.Parallel()
	r := newRig(t, nil)
	var mu sync.Mutex
	var notes []Notice
	r.m.Watch(func(n Notice) { mu.Lock(); notes = append(notes, n); mu.Unlock() })
	id, _ := r.m.Create(bg, api.NewSession{Cwd: r.cwd, Text: "please ASKASK"}, "网页")
	s := r.waitState(id, "the ask", func(s *api.SessionState) bool { return s.Status == api.StatusWaiting })
	card := s.Asks[0].Card
	if card == nil || card.Kind != "permission" || card.Summary[0] != "ls" || len(card.Options) != 3 || card.Options[2].ID != OptionAlways {
		t.Fatalf("card: %+v", card)
	}
	if err := r.m.AnswerCard(bg, api.CardAnswer{Card: card.ID, Option: OptionAlways}); err != nil {
		t.Fatal(err)
	}
	r.waitState(id, "the reply", func(s *api.SessionState) bool { return said(s, "ok: please ASKASK") })
	if rules, _ := r.m.Rules(id); len(rules) != 1 || rules[0] != (api.AllowRule{Tool: "Bash", Pattern: "ls *"}) {
		t.Errorf("rules: %+v", rules)
	}
	mu.Lock()
	asks := 0
	for _, n := range notes {
		if n.Kind == NoticeAsk {
			asks++
		}
	}
	mu.Unlock()
	if asks != 1 {
		t.Errorf("%d asks told; the second should have been answered by the rule", asks)
	}
	if err := r.m.AnswerCard(bg, api.CardAnswer{Card: card.ID, Option: OptionAllow}); err == nil {
		t.Error("answered a card no longer open")
	}
}

func TestRuleMatches(t *testing.T) {
	ask := func(tool, cmd string) api.Ask {
		return api.Ask{Kind: "permission", Tool: tool, Input: map[string]any{"command": cmd, "file_path": cmd}}
	}
	for _, c := range []struct {
		rule store.AllowRule
		ask  api.Ask
		want bool
	}{
		{store.AllowRule{Tool: "Bash", Pattern: "git status *"}, ask("Bash", "git status -s"), true},
		{store.AllowRule{Tool: "Bash", Pattern: "git status *"}, ask("Bash", "git status; rm -rf ~"), false},
		{store.AllowRule{Tool: "Bash", Pattern: "git status *"}, ask("Bash", "git log"), false},
		{store.AllowRule{Tool: "Bash", Pattern: "npm test:*"}, ask("Bash", "npm test -- -v"), true},
		{store.AllowRule{Tool: "Bash", Pattern: "ls *"}, ask("Bash", "ls -la | sh"), false},
		{store.AllowRule{Tool: "Edit", Pattern: ""}, ask("Edit", "/a/b"), true},
		{store.AllowRule{Tool: "Edit", Pattern: "/a/*"}, ask("Edit", "/a/b/c"), true},
		{store.AllowRule{Tool: "Edit", Pattern: "/a/*"}, ask("Write", "/a/b"), false},
	} {
		if got := ruleMatches(c.rule, c.ask); got != c.want {
			t.Errorf("%+v on %v: %v", c.rule, c.ask.Input, got)
		}
	}
}

// An agent naming its session (ir rename) is never asked about; anything chained to it is.
func TestRenamesOnly(t *testing.T) {
	for cmd, want := range map[string]bool{
		`ir rename "会话命名"`:      true,
		`ir rename x`:           true,
		`ir rename`:             false,
		`ir rename x; rm -rf ~`: false,
		`ir rename x && ls`:     false,
		`ir rename $(cat f)`:    false,
		`ir rename x > f`:       false,
		"ir rename x\nls":       false,
		`ir ls`:                 false,
		`irx rename x`:          false,
	} {
		if got := renamesOnly(api.Ask{Kind: "permission", Tool: "Bash", Input: map[string]any{"command": cmd}}); got != want {
			t.Errorf("%q: %v", cmd, got)
		}
	}
	if renamesOnly(api.Ask{Kind: "question", Input: map[string]any{"command": "ir rename x"}}) {
		t.Error("a question")
	}
}

// A crash mid-turn: Claude is started again with CLAUDE_CODE_RESUME_INTERRUPTED_TURN and runs the
// turn again itself; no recovery prompt.
func TestCrashResumed(t *testing.T) {
	t.Parallel()
	r := newRig(t, nil)
	id, _ := r.m.Create(bg, api.NewSession{Cwd: r.cwd, Text: "please crash"}, "网页")
	s := r.waitState(id, "the turn run again", func(s *api.SessionState) bool { return said(s, "ok: continued") && s.Status == api.StatusIdle })
	if s.Proc != 2 || s.Error != "" {
		t.Errorf("state: %+v", s)
	}
	spec, _ := os.ReadFile(filepath.Join(r.cfg.StatePath(), "hold", id, hold.FileSpec))
	if !strings.Contains(string(spec), "CLAUDE_CODE_RESUME_INTERRUPTED_TURN=1") {
		t.Errorf("spec: %s", spec)
	}
	if strings.Contains(r.inLog(id), RecoveryPrompt) {
		t.Error("a recovery prompt went to an agent that ran the turn again itself")
	}
}

// An agent that does not run the turn again gets Iridium's recovery prompt, shown as Iridium's
// own line.
func TestCrashRecoveryPrompt(t *testing.T) {
	t.Parallel()
	r := newRig(t, nil)
	id, _ := r.m.Create(bg, api.NewSession{Cwd: r.cwd, Text: "please crash!"}, "网页")
	r.waitState(id, "the recovery", func(s *api.SessionState) bool { return said(s, "ok: "+RecoveryPrompt) })
	b := r.messages(id)
	var line *api.Block
	for i := range b {
		if b[i].Kind == api.BlockKindSystem && b[i].Event == api.SystemEventIridium {
			line = &b[i]
		}
	}
	if line == nil || line.Text != RecoveryPrompt {
		t.Fatalf("blocks: %+v", b)
	}
	if !strings.Contains(r.inLog(id), `"[Iridium] `+RecoveryPrompt+`"`) {
		t.Errorf("in.log: %s", r.inLog(id))
	}
}

// Three crashes in ten minutes and it is not continued again.
func TestCrashBreaker(t *testing.T) {
	t.Parallel()
	r := newRig(t, nil)
	id, _ := r.m.Create(bg, api.NewSession{Cwd: r.cwd, Text: "please crashloop"}, "网页")
	s := r.waitState(id, "the breaker", func(s *api.SessionState) bool { return s.Proc == 3 && !s.Live && s.Status == api.StatusError })
	time.Sleep(300 * time.Millisecond)
	if s, _ = r.m.State(id); s.Proc != 3 {
		t.Errorf("went on past the breaker: %+v", s)
	}
}

// Refused for the quota: Iridium waits for it to come back, then says "继续"; in a second session
// a message meanwhile waits, and goes instead of "继续".
func TestQuotaResume(t *testing.T) {
	t.Parallel()
	r := newRig(t, func(_ *config.Config, o *Options) { o.QuotaStagger = -1 })
	id, _ := r.m.Create(bg, api.NewSession{Cwd: r.cwd, Text: "QUOTA"}, "网页")
	queued, _ := r.m.Create(bg, api.NewSession{Cwd: r.cwd, Text: "QUOTA"}, "网页")
	s := r.waitState(id, "the wait", func(s *api.SessionState) bool { return s.QuotaUntil != 0 })
	if s.QuotaUntil < time.Now().UnixMilli() {
		t.Errorf("until %d", s.QuotaUntil)
	}
	r.waitState(queued, "the wait", func(s *api.SessionState) bool { return s.QuotaUntil != 0 })
	if _, queued, _ := r.m.SendWith(bg, queued, "later", "网页", false); !queued {
		t.Error("not queued while out of quota")
	}
	r.waitState(id, "going on", func(s *api.SessionState) bool { return said(s, "ok: "+QuotaPrompt) && s.QuotaUntil == 0 })
	r.waitState(queued, "the queue sent", func(s *api.SessionState) bool { return said(s, "ok: later") })
	if strings.Contains(r.inLog(queued), QuotaPrompt) {
		t.Error("继续 went though a message waited")
	}
}

// The tail carries the newest small blocks; context and quota are kept, quota per account.
func TestCarried(t *testing.T) {
	t.Parallel()
	r := newRig(t, nil)
	id, _ := r.m.Create(bg, api.NewSession{Cwd: r.cwd, Text: "hello"}, "网页")
	s := r.waitState(id, "the tail", func(s *api.SessionState) bool {
		return s.Tail != nil && len(s.Tail.Blocks) > 0 && s.Tail.Blocks[len(s.Tail.Blocks)-1].Text == "ok: hello" && s.Context != nil
	})
	if s.Tail.End != s.End || s.Context.Used != 100 {
		t.Errorf("tail %+v context %+v", s.Tail, s.Context)
	}
	waitFor(t, "the quota", func() bool {
		var q api.Quota
		json.Unmarshal(r.hub.Get("quota"), &q)
		return len(q.Agents) == 1 && q.Agents[0].Account == "user@example.com" && q.Agents[0].Live
	})
	r.m.Stop(bg, id)
	r.waitState(id, "the stop", func(s *api.SessionState) bool { return !s.Live })
	waitFor(t, "the quota kept, not live", func() bool {
		var q api.Quota
		json.Unmarshal(r.hub.Get("quota"), &q)
		return len(q.Agents) == 1 && !q.Agents[0].Live && len(q.Agents[0].Windows) == 1
	})
	// A new daemon has both from the disk: TestContextAndQuotaAfterRestart.
}
