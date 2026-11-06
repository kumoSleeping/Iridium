package session

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/config"
	"github.com/kumoSleeping/iridium/internal/hold"
)

// A message goes to the agent as "[name] text", the name the terminal had then; its block has the
// text alone, the name and the terminal's ID; the turn's usage row is the terminal's, by ID, so a
// rename keeps its usage.
func TestSourceTags(t *testing.T) {
	t.Parallel()
	names := map[string]string{"pppppppppppppppp": "mac", "local": "本机"}
	r := newRig(t, func(_ *config.Config, o *Options) { o.Tag = func(id string) string { return names[id] } })
	id, err := r.m.Create(context.Background(), api.NewSession{Agent: api.AgentClaude, Cwd: r.cwd, Text: "hello"}, "pppppppppppppppp")
	if err != nil {
		t.Fatal(err)
	}
	r.waitState(id, "the reply", func(s *api.SessionState) bool { return s.Last != nil && s.Last.Text == "ok: hello" })
	names["pppppppppppppppp"] = "renamed"
	if _, err := r.m.Send(context.Background(), id, "/compact", "local"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.m.Send(context.Background(), id, "again", "pppppppppppppppp"); err != nil {
		t.Fatal(err)
	}
	r.waitState(id, "the second reply", func(s *api.SessionState) bool { return s.Last != nil && s.Last.Text == "ok: again" })
	inLog, _ := os.ReadFile(filepath.Join(r.cfg.StatePath(), "hold", id, hold.FileInLog))
	for _, want := range []string{`"text":"[mac] hello"`, `"text":"/compact"`, `"text":"[renamed] again"`} {
		if !strings.Contains(string(inLog), want) {
			t.Errorf("in.log has no %s: %s", want, inLog)
		}
	}
	var users []api.Block
	for _, b := range r.messages(id) {
		if b.Kind == api.BlockKindUser {
			users = append(users, b)
		}
	}
	if len(users) < 2 || users[0].Text != "hello" || users[0].From != "mac" || users[0].FromTerminal != "pppppppppppppppp" ||
		users[len(users)-1].Text != "again" || users[len(users)-1].From != "renamed" {
		t.Fatalf("user blocks: %+v", users)
	}
	waitFor(t, "the usage rows", func() bool { u, _ := r.store.UsageSince(0); return len(u) >= 2 })
	u, _ := r.store.UsageSince(0)
	for _, row := range u {
		if row.Terminal != "pppppppppppppppp" && row.Terminal != "local" {
			t.Errorf("usage row of %q", row.Terminal)
		}
	}
	if n, _ := r.store.TokensSince("pppppppppppppppp", 0); n == 0 {
		t.Error("no tokens counted to the terminal")
	}
}
