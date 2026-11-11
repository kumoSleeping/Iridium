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
	"github.com/kumoSleeping/iridium/internal/store"
)

// Colour tags from a row's menu: a session's on its row in the list, a folder's carried as
// "colors"; one that is none of api.Colors, or a folder not absolute, is refused.
func TestColors(t *testing.T) {
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

	var created api.NewSessionReply
	if code := call(t, c, "POST", base+"/v1/internal/sessions", api.NewSession{Agent: api.AgentClaude, Cwd: work}, &created); code != 200 {
		t.Fatalf("new session: %d", code)
	}
	color := func() api.Color {
		var l []api.Session
		call(t, c, "GET", base+"/v1/internal/sessions", nil, &l)
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
	if code := call(t, c, "PATCH", base+"/v1/internal/sessions/"+created.ID, api.SessionPatch{Color: &blue}, nil); code != 200 {
		t.Fatalf("tag: %d", code)
	}
	wait(api.ColorBlue)
	if code := call(t, c, "PATCH", base+"/v1/internal/sessions/"+created.ID, api.SessionPatch{Color: &bad}, nil); code != http.StatusBadRequest {
		t.Errorf("a colour that is none: %d", code)
	}
	if code := call(t, c, "PATCH", base+"/v1/internal/sessions/"+created.ID, api.SessionPatch{Color: &none}, nil); code != 200 {
		t.Fatalf("untag: %d", code)
	}
	wait("")

	carried := func() api.FolderColors {
		var m api.FolderColors
		json.Unmarshal(s.hub.Get("colors"), &m)
		return m
	}
	if code := call(t, c, "PUT", base+"/v1/internal/colors", api.FolderColor{Path: work + "/", Color: api.ColorGreen}, nil); code != 200 {
		t.Fatalf("folder tag: %d", code)
	}
	if m := carried(); len(m) != 1 || m[work] != api.ColorGreen {
		t.Errorf("carried: %+v", m)
	}
	if code := call(t, c, "PUT", base+"/v1/internal/colors", api.FolderColor{Path: "rel", Color: api.ColorGreen}, nil); code != http.StatusBadRequest {
		t.Errorf("a folder not absolute: %d", code)
	}
	if code := call(t, c, "PUT", base+"/v1/internal/colors", api.FolderColor{Path: work, Color: bad}, nil); code != http.StatusBadRequest {
		t.Errorf("a folder colour that is none: %d", code)
	}
	if code := call(t, c, "PUT", base+"/v1/internal/colors", api.FolderColor{Path: work}, nil); code != 200 {
		t.Fatalf("folder untag: %d", code)
	}
	if m := carried(); len(m) != 0 {
		t.Errorf("carried after untag: %+v", m)
	}
}
