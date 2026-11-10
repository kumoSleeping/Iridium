package server

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/config"
	"github.com/kumoSleeping/iridium/internal/push"
)

// The settings page's fields: read, changed in part, saved to the config file beside what the
// file has of its own, in force at once; only panels change them, channels see none.
func TestSettings(t *testing.T) {
	var path, state string
	l := startWith(t, func(s *Server, cfg config.Config, work string) {
		path, state = filepath.Join(filepath.Dir(cfg.StateDir), "config.json"), cfg.StateDir
		// The file has a field of its own the settings must keep.
		c := cfg
		c.ChatDir = work
		if err := c.Save(path); err != nil {
			t.Fatal(err)
		}
		s.ConfigPath = path
		var err error
		s.Push, err = push.Open(push.Options{StateDir: cfg.StateDir, Store: s.st, Manager: s.m, Logf: t.Logf, Save: func(on bool) error {
			return s.SaveConfig(func(c *config.Config) { c.Push = on })
		}})
		if err != nil {
			t.Fatal(err)
		}
	})
	var st api.Settings
	l.want(200, "web", "GET", "/api/settings", nil, &st)
	if st.IdleReap != "1h" || !st.CrashResume || !st.QuotaResume || st.Push || len(st.QuotaHidden) != 0 || st.Modes == nil || st.EnterInTurn != "steer" || st.Lang != "" || !st.AutoUpdate {
		t.Fatalf("defaults: %+v", st)
	}

	off, reap, hidden, queue, later := false, "15m", []string{"claude:me@example.com:weekly:", "claude:me@example.com:weekly:"}, "queue", "later"
	fr, zh := "fr", "zh"
	l.want(400, "web", "PUT", "/api/settings", api.SettingsPatch{EnterInTurn: &later}, nil)
	l.want(400, "web", "PUT", "/api/settings", api.SettingsPatch{Lang: &fr}, nil)
	l.want(200, "web", "PUT", "/api/settings", api.SettingsPatch{IdleReap: &reap, CrashResume: &off, QuotaHidden: &hidden, Modes: map[string]string{"claude": "plan", "codex": "untrusted"}, EnterInTurn: &queue, Lang: &zh, AutoUpdate: &off}, &st)
	if st.AutoUpdate {
		t.Fatalf("automatic updates still on: %+v", st)
	}
	if st.IdleReap != "15m" || st.CrashResume || !st.QuotaResume || len(st.QuotaHidden) != 1 || st.Modes["claude"] != "plan" || st.Modes["codex"] != "untrusted" || st.EnterInTurn != "queue" || st.Lang != "zh" {
		t.Fatalf("after the change: %+v", st)
	}
	if tu := l.s.m.Tuned(); tu.IdleReap != 15*time.Minute || tu.CrashResume || !tu.QuotaResume || tu.Modes["claude"] != "plan" {
		t.Fatalf("not in force: %+v", tu)
	}
	saved, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if saved.IdleReap.D() != 15*time.Minute || saved.CrashResume || saved.Modes["codex"] != "untrusted" || len(saved.QuotaHidden) != 1 || saved.ChatDir != l.work || saved.EnterInTurn != "queue" || saved.Lang != "zh" || !saved.Update.Manual {
		t.Fatalf("file: %+v", saved)
	}

	// A new session starts in the agent's default mode, now plan.
	var created api.NewSessionReply
	l.want(200, "web", "POST", "/api/sessions", api.NewSession{Agent: api.AgentClaude, Cwd: l.work, Text: "hi"}, &created)
	l.waitReply("web", created.ID, "ok: hi")
	var ss api.SessionState
	l.want(200, "web", "GET", "/api/sessions/"+created.ID, nil, &ss)
	if ss.Mode != "plan" {
		t.Errorf("new session's mode %q, want plan", ss.Mode)
	}
	// Its agent is told the user's language.
	if b, err := os.ReadFile(filepath.Join(state, "hold", created.ID, "system-prompt.md")); err != nil || !strings.Contains(string(b), "web settings: Chinese.") {
		t.Errorf("system prompt %q %v", b, err)
	}

	// A mode back to the agent's own; push on through the settings is push on.
	on := true
	st = api.Settings{} // decoding into a map adds to it
	l.want(200, "web", "PUT", "/api/settings", api.SettingsPatch{Modes: map[string]string{"claude": ""}, Push: &on}, &st)
	if _, ok := st.Modes["claude"]; ok || !st.Push || !l.s.Push.Enabled() {
		t.Fatalf("mode cleared, push on: %+v", st)
	}
	var ps api.PushState
	l.want(200, "web", "GET", "/api/push", nil, &ps)
	if !ps.Enabled {
		t.Fatal("GET /api/push says off")
	}
	// PUT /api/push is the same switch.
	l.want(200, "web", "PUT", "/api/push", api.PushSwitch{Enabled: false}, nil)
	l.want(200, "web", "GET", "/api/settings", nil, &st)
	if saved, _ = config.Load(path); st.Push || saved.Push {
		t.Fatalf("push off: settings %v, file %v", st.Push, saved.Push)
	}

	// Refused: a bad duration, a mode the agent has not, an agent there is none of.
	bad := "soon"
	l.want(400, "web", "PUT", "/api/settings", api.SettingsPatch{IdleReap: &bad}, nil)
	l.want(400, "web", "PUT", "/api/settings", api.SettingsPatch{Modes: map[string]string{"claude": "yolo"}}, nil)
	l.want(400, "web", "PUT", "/api/settings", api.SettingsPatch{Modes: map[string]string{"nobody": "plan"}}, nil)
	never := "0"
	l.want(200, "web", "PUT", "/api/settings", api.SettingsPatch{IdleReap: &never}, &st)
	if st.IdleReap != "0" || l.s.m.Tuned().IdleReap != 0 {
		t.Fatalf("never: %+v", st)
	}

	// A channel sees none of it; an agent's ir reads but does not change it.
	var tok api.Token
	l.want(200, "socket", "POST", "/api/terminals/tokens", api.NewInvite{Name: "bot"}, &tok)
	l.want(403, tok.Token, "GET", "/api/settings", nil, nil)
	l.want(403, tok.Token, "PUT", "/api/settings", api.SettingsPatch{CrashResume: &on}, nil)
	l.want(200, "agent", "GET", "/api/settings", nil, nil)
	l.want(403, "agent", "PUT", "/api/settings", api.SettingsPatch{CrashResume: &on}, nil)

	// Changes at once from many pages, and the daemon reading them meanwhile: no race.
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v := i%2 == 0
			l.do("web", "PUT", "/api/settings", api.SettingsPatch{QuotaResume: &v}, nil)
			_ = l.s.m.Tuned()
		}()
	}
	wg.Wait()
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), `"chatDir"`) {
		t.Fatalf("the file lost its own field: %s", b)
	}
}

func TestShortDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{0: "0", time.Hour: "1h", 15 * time.Minute: "15m", 90 * time.Minute: "1h30m", 12 * time.Hour: "12h", 45 * time.Second: "45s"} {
		if got := shortDuration(d); got != want {
			t.Errorf("%s: %q, want %q", d, got, want)
		}
	}
}
