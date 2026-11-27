package server

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/config"
)

// The settings page's fields: read, changed in part, saved to the config file beside what the
// file has of its own, in force at once; only panels change them, channels see none.
func TestSettings(t *testing.T) {
	t.Parallel()
	var path, state string
	l := startWith(t, func(s *Server, cfg config.Config, work string) {
		path, state = filepath.Join(filepath.Dir(cfg.StateDir), "config.json"), cfg.StateDir
		// The file has a field of its own the settings must keep.
		c := cfg
		c.FolderRoots = []string{work}
		if err := c.Save(path); err != nil {
			t.Fatal(err)
		}
		s.ConfigPath = path
	})
	var st api.Settings
	l.want(200, "web", "GET", "/v1/internal/settings", nil, &st)
	if st.IdleReap != "1h" || len(st.QuotaHidden) != 0 || st.Modes == nil || st.Lang != "" {
		t.Fatalf("defaults: %+v", st)
	}

	reap, hidden := "15m", []string{"claude:me@example.com:weekly:", "claude:me@example.com:weekly:"}
	fr, zh := "fr", "zh"
	l.want(400, "web", "PUT", "/v1/internal/settings", api.SettingsPatch{Lang: &fr}, nil)
	l.want(200, "web", "PUT", "/v1/internal/settings", api.SettingsPatch{IdleReap: &reap, QuotaHidden: &hidden, Modes: map[string]string{"claude": "plan", "codex": "untrusted"}, Lang: &zh}, &st)
	if st.IdleReap != "15m" || len(st.QuotaHidden) != 1 || st.Modes["claude"] != "plan" || st.Modes["codex"] != "untrusted" || st.Lang != "zh" {
		t.Fatalf("after the change: %+v", st)
	}
	if tu := l.s.m.Tuned(); tu.IdleReap != 15*time.Minute || tu.Modes["claude"] != "plan" {
		t.Fatalf("not in force: %+v", tu)
	}
	saved, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if saved.IdleReap.D() != 15*time.Minute || saved.Modes["codex"] != "untrusted" || len(saved.QuotaHidden) != 1 || !slices.Equal(saved.FolderRoots, []string{l.work}) || saved.Lang != "zh" {
		t.Fatalf("file: %+v", saved)
	}

	// A new session starts in the agent's default mode, now plan.
	created := l.create(200, "web", api.NewSession{Agent: api.AgentClaude, Cwd: l.work, Text: "hi"})
	l.waitReply("web", created, "ok: hi")
	var ss api.SessionState
	l.want(200, "web", "GET", "/v1/internal/sessions/"+created, nil, &ss)
	if ss.Mode != "plan" {
		t.Errorf("new session's mode %q, want plan", ss.Mode)
	}
	// Its agent is told the user's language.
	if b, err := os.ReadFile(filepath.Join(state, "hold", created, "system-prompt.md")); err != nil || !strings.Contains(string(b), "- User's language: Chinese\n") {
		t.Errorf("system prompt %q %v", b, err)
	}

	// A mode back to the agent's own.
	st = api.Settings{} // decoding into a map adds to it
	l.want(200, "web", "PUT", "/v1/internal/settings", api.SettingsPatch{Modes: map[string]string{"claude": ""}}, &st)
	if _, ok := st.Modes["claude"]; ok {
		t.Fatalf("mode cleared: %+v", st)
	}

	// Refused: a bad duration, a mode the agent has not, an agent there is none of.
	bad := "soon"
	l.want(400, "web", "PUT", "/v1/internal/settings", api.SettingsPatch{IdleReap: &bad}, nil)
	l.want(400, "web", "PUT", "/v1/internal/settings", api.SettingsPatch{Modes: map[string]string{"claude": "yolo"}}, nil)
	l.want(400, "web", "PUT", "/v1/internal/settings", api.SettingsPatch{Modes: map[string]string{"nobody": "plan"}}, nil)
	never := "0"
	l.want(200, "web", "PUT", "/v1/internal/settings", api.SettingsPatch{IdleReap: &never}, &st)
	if st.IdleReap != "0" || l.s.m.Tuned().IdleReap != 0 {
		t.Fatalf("never: %+v", st)
	}

	// A channel sees none of it; an agent's ir reads but does not change it.
	var tok api.Token
	tok = l.newApp("bot")
	l.want(403, tok.Token, "GET", "/v1/internal/settings", nil, nil)
	l.want(403, tok.Token, "PUT", "/v1/internal/settings", api.SettingsPatch{IdleReap: &never}, nil)
	l.want(200, "agent", "GET", "/v1/internal/settings", nil, nil)
	l.want(403, "agent", "PUT", "/v1/internal/settings", api.SettingsPatch{IdleReap: &never}, nil)

	// Changes at once from many pages, and the daemon reading them meanwhile: no race.
	var wg sync.WaitGroup
	reaps := []string{"1h", "15m"}
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l.do("web", "PUT", "/v1/internal/settings", api.SettingsPatch{IdleReap: &reaps[i%2]}, nil)
			_ = l.s.m.Tuned()
		}()
	}
	wg.Wait()
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), `"folderRoots"`) {
		t.Fatalf("the file lost its own field: %s", b)
	}
}

func TestShortDuration(t *testing.T) {
	t.Parallel()
	for d, want := range map[time.Duration]string{0: "0", time.Hour: "1h", 15 * time.Minute: "15m", 90 * time.Minute: "1h30m", 12 * time.Hour: "12h", 45 * time.Second: "45s"} {
		if got := shortDuration(d); got != want {
			t.Errorf("%s: %q, want %q", d, got, want)
		}
	}
}
