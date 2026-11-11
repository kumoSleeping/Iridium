package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/discover"
	"github.com/kumoSleeping/iridium/internal/satori"
	"github.com/kumoSleeping/iridium/internal/store"
)

// Colour tags from a row's menu (channel.update): a session's on its row in the list, a folder's
// on its category channel, carried in "guilds"; one that is none of api.Colors, or a folder not
// absolute, is refused.
func TestColors(t *testing.T) {
	t.Parallel()
	s, cfg, work := setup(t)
	st, err := store.Open(filepath.Join(cfg.StateDir, "iridium.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	empty := t.TempDir()
	d := discover.New(discover.Options{ClaudeDir: cfg.ClaudeHome, CodexHome: empty, PiSessions: empty, DshHome: empty, Store: st, Hub: s.hub, Procs: discover.ProcFS{Root: empty}, Logf: t.Logf, Settle: 10 * time.Millisecond})
	if err := d.Start(); err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	s.Discover, s.Folders = d, discover.DefaultFolders(filepath.Dir(work))
	srv := httptest.NewServer(s.Handler(false))
	defer srv.Close()
	c, base := srv.Client(), srv.URL

	created, code := createAt(t, c, base, api.NewSession{Agent: api.AgentClaude, Cwd: work})
	if code != 200 {
		t.Fatalf("new session: %d", code)
	}
	color := func() api.Color {
		l := sessionsAt(t, c, base)
		if len(l) != 1 {
			t.Fatalf("sessions: %+v", l)
		}
		return l[0].Color
	}
	wait := func(want api.Color) {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline) && color() != want; time.Sleep(10 * time.Millisecond) {
		}
		if got := color(); got != want {
			t.Fatalf("color %q, want %q", got, want)
		}
	}
	blue, none, bad := api.ColorBlue, api.Color(""), api.Color("pink")
	if code := patchAt(t, c, base, created, api.SessionPatch{Color: &blue}); code != 200 {
		t.Fatalf("tag: %d", code)
	}
	wait(api.ColorBlue)
	if code := patchAt(t, c, base, created, api.SessionPatch{Color: &bad}); code != http.StatusBadRequest {
		t.Errorf("a colour that is none: %d", code)
	}
	if code := patchAt(t, c, base, created, api.SessionPatch{Color: &none}); code != 200 {
		t.Fatalf("untag: %d", code)
	}
	wait("")

	carried := func() []api.FolderMark {
		var g api.Guilds
		json.Unmarshal(s.hub.Get("guilds"), &g)
		return g.Folders
	}
	folder := func(c2 api.Color) int {
		return satAt(t, c, base, "channel.update", map[string]any{"channel_id": satori.FolderPrefix + work, "_data": map[string]any{"color": c2}}, nil)
	}
	if code := folder(api.ColorGreen); code != 200 {
		t.Fatalf("folder tag: %d", code)
	}
	if m := carried(); len(m) != 1 || m[0].Path != work || m[0].Color != api.ColorGreen {
		t.Errorf("carried: %+v", m)
	}
	var ch satori.Channel
	if satAt(t, c, base, "channel.get", map[string]any{"channel_id": satori.FolderPrefix + work}, &ch); ch.Type != satori.ChannelCategory {
		t.Errorf("the folder's channel: %+v", ch)
	}
	if code := satAt(t, c, base, "channel.update", map[string]any{"channel_id": satori.FolderPrefix + "rel", "_data": map[string]any{"color": "green"}}, nil); code != http.StatusUnprocessableEntity {
		t.Errorf("a folder no session is in: %d", code)
	}
	if code := folder(bad); code != http.StatusBadRequest {
		t.Errorf("a folder colour that is none: %d", code)
	}
	if code := folder(""); code != 200 {
		t.Fatalf("folder untag: %d", code)
	}
	if m := carried(); len(m) != 0 {
		t.Errorf("carried after untag: %+v", m)
	}
}
