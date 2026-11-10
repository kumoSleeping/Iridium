package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/config"
	"github.com/kumoSleeping/iridium/internal/hub"
	"github.com/kumoSleeping/iridium/internal/platform"
	"github.com/kumoSleeping/iridium/internal/server"
	"github.com/kumoSleeping/iridium/internal/session"
	"github.com/kumoSleeping/iridium/internal/store"
)

// daemonFor runs a test daemon; person and agent are ir as run by a person at a terminal and by
// an agent.
func daemonFor(t *testing.T) (person, agent adminEnv) {
	t.Helper()
	root := t.TempDir()
	cfg := config.Default()
	cfg.Listen = "127.0.0.1:0"
	cfg.StateDir = filepath.Join(root, "state")
	cfg.Socket = filepath.Join(root, "run", "ir.sock")
	os.MkdirAll(cfg.StateDir, 0o700)
	st, err := store.Open(filepath.Join(cfg.StateDir, "iridium.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	h := hub.New()
	m, err := session.New(session.Options{Config: cfg, Store: st, Hub: h, Launcher: &platform.Direct{}, Exe: os.Args[0], UnitPrefix: "iridium-test-", Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	srv := server.New(cfg, m, h, t.Logf)
	t.Cleanup(srv.Close)
	tcp, sock, err := srv.Listen()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, tcp, sock) }()
	t.Cleanup(func() { cancel(); <-done })
	person = adminEnv{cfg: cfg, tty: true, stdin: strings.NewReader(""), client: socketClient(cfg.SocketPath())}
	agent = person
	agent.agent, agent.tty, agent.session = true, false, "the-calling-session"
	return person, agent
}

func runAs(e adminEnv, args ...string) (int, string, string) {
	var out, errs bytes.Buffer
	code := adminCommands[args[0]](e, args[1:], &out, &errs)
	return code, out.String(), errs.String()
}

// approve answers the one pending confirmation as a panel would, and gives what that panel sees.
func approve(t *testing.T, as adminEnv, allow bool) string {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		var cs api.Confirms
		as.call("GET", "/api/confirms", nil, &cs)
		if len(cs.Pending) == 1 {
			var raw json.RawMessage
			if _, err := as.call("POST", "/api/confirms/"+cs.Pending[0].ID, api.ConfirmAnswer{Allow: allow}, &raw); err != nil {
				t.Fatal(err)
			}
			return string(raw)
		}
		if time.Now().After(deadline) {
			t.Fatalf("no confirmation pending: %+v", cs)
		}
	}
}

// agentRun runs ir as an agent in the background, while the test plays the panel.
func agentRun(e adminEnv, args ...string) chan [3]string {
	got := make(chan [3]string, 1)
	go func() {
		code, out, errs := runAs(e, args...)
		got <- [3]string{string(rune('0' + code)), out, errs}
	}()
	return got
}

// pairApp makes app name by pairing, the one way an app comes in (POST /api/pair), allowed by the
// person: its TOKEN. claude: allowed as the owner's own.
func pairApp(t *testing.T, person adminEnv, name string, claude bool) string {
	t.Helper()
	var p api.ExecPairing
	if _, err := person.call("POST", "/api/pair", api.ExecPair{Name: name}, &p); err != nil {
		t.Fatal(err)
	}
	var cs api.Confirms
	person.call("GET", "/api/confirms", nil, &cs)
	for _, c := range cs.Pending {
		if c.Code == p.Code {
			if _, err := person.call("POST", "/api/confirms/"+c.ID, api.ConfirmAnswer{Allow: true, Owner: claude}, nil); err != nil {
				t.Fatal(err)
			}
		}
	}
	var st api.ExecPairState
	if _, err := person.call("GET", "/api/pair/"+p.Request, nil, &st); err != nil || st.Status != "allowed" {
		t.Fatalf("pairing %s: %+v %v", name, st, err)
	}
	return st.Token
}

func TestAdmin(t *testing.T) {
	person, agent := daemonFor(t)

	// ir token is gone: an app comes by pairing.
	if _, ok := adminCommands["token"]; ok {
		t.Fatal("ir token is still there")
	}
	pairApp(t, person, "bot", false)
	pairApp(t, person, "bot2", false)

	// Invites need a public origin; then a person gets the link and an agent does not.
	if code, _, errs := runAs(agent, "invite", "pad"); code != 1 || !strings.Contains(errs, "public origin") {
		t.Fatalf("invite without an origin: %d %q", code, errs)
	}
	if err := (daemon{person}).SetOrigin(context.Background(), "https://ir.example.com"); err != nil {
		t.Fatal(err)
	}
	if code, out, _ := runAs(person, "invite", "pad"); code != 0 || !strings.Contains(out, "https://ir.example.com/_ir/join#") {
		t.Fatalf("invite: %d %q", code, out)
	}
	got := agentRun(agent, "invite", "tab")
	panel := approve(t, person, true)
	r := <-got
	if r[0] != "0" || !strings.Contains(r[1], "已同意") || strings.Contains(r[1]+r[2], "/_ir/join") || !strings.Contains(panel, "/_ir/join#") {
		t.Fatalf("agent invite: %q / %s", r, panel)
	}
	got = agentRun(agent, "invite", "tab2")
	approve(t, person, false)
	if r := <-got; r[0] != "1" || !strings.Contains(r[1], "已拒绝") {
		t.Fatalf("denied: %q", r)
	}
	if code, _, errs := runAs(agent, "invite", "a/b"); code != 1 || errs == "" {
		t.Fatalf("a bad name: %d %q", code, errs)
	}

	// The list: the host, ir_web, the apps; the logins and invites apart. No secrets in it.
	code, out, _ := runAs(agent, "apps")
	if code != 0 || !strings.Contains(out, "bot2") || !strings.Contains(out, "主机") || !strings.Contains(out, "网页") ||
		regexp.MustCompile(`ir_[A-Za-z0-9_-]{43}`).MatchString(out) || strings.Contains(out, "#") {
		t.Fatalf("apps: %d\n%s", code, out)
	}
	code, out, _ = runAs(agent, "apps", "logins")
	if code != 0 || !strings.Contains(out, "未用的邀请") || !strings.Contains(out, "pad") || strings.Contains(out, "#") {
		t.Fatalf("apps logins: %d\n%s", code, out)
	}
	if code, _, errs := runAs(agent, "apps", "logins", "nobody"); code != 1 || !strings.Contains(errs, "nobody") {
		t.Fatalf("logins of no app: %d %q", code, errs)
	}

	// Rename and avatar.
	img := filepath.Join(t.TempDir(), "a.png")
	f, _ := os.Create(img)
	png.Encode(f, image.NewRGBA(image.Rect(0, 0, 40, 30)))
	f.Close()
	if code, out, errs := runAs(agent, "apps", "rename", "bot", "robot"); code != 0 || !strings.Contains(out, "robot") {
		t.Fatalf("rename: %d %q %q", code, out, errs)
	}
	for name, why := range map[string]string{"bot2": "taken", "x": "letter", "主机": "reserved", "a/b": "invalid"} {
		if code, _, errs := runAs(agent, "apps", "rename", "robot", name); code != 1 || !strings.Contains(errs, why) {
			t.Fatalf("rename to %s: %d %q", name, code, errs)
		}
	}
	if code, out, errs := runAs(agent, "profile", "robot", "--avatar", img); code != 0 || !strings.Contains(out, "robot（有头像）") {
		t.Fatalf("profile: %d %q %q", code, out, errs)
	}
	if code, _, _ := runAs(agent, "profile", "robot", "--name", "x"); code != 2 {
		t.Fatal("profile --name: rename is ir apps rename")
	}
	if code, _, _ := runAs(agent, "profile", "robot"); code != 2 {
		t.Fatal("profile with nothing to change")
	}
	// The device.
	if code, out, errs := runAs(agent, "profile", "robot", "--device", "phone"); code != 0 || !strings.Contains(out, "robot（手机）") {
		t.Fatalf("device: %d %q %q", code, out, errs)
	}
	if code, _, _ := runAs(agent, "profile", "robot", "--device", "tablet"); code != 2 {
		t.Fatal("a device that is none")
	}
	if code, out, errs := runAs(agent, "profile", "主机", "--device=computer"); code != 0 || !strings.Contains(out, "（电脑）") {
		t.Fatalf("the host's device: %d %q %q", code, out, errs)
	}

	// Remove: an app; the host and ir_web are built in.
	if code, _, errs := runAs(agent, "apps", "rm", "robot"); code != 0 {
		t.Fatalf("rm: %q", errs)
	}
	if _, out, _ := runAs(agent, "apps"); strings.Contains(out, "robot") {
		t.Fatalf("removed, still listed:\n%s", out)
	}
	for _, ref := range []string{"nobody", "主机", "ir_web"} {
		if code, _, _ := runAs(agent, "apps", "rm", ref); code != 1 {
			t.Fatalf("removed %s", ref)
		}
	}
	for _, args := range [][]string{{"apps", "rm"}, {"apps", "rename", "a"}, {"apps", "x"}, {"apps", "ls", "y"}} {
		if code, _, _ := runAs(agent, args...); code != 2 {
			t.Errorf("%v: want usage", args)
		}
	}

	// ir public is a person's, at a terminal.
	for _, e := range []adminEnv{agent, func() adminEnv { p := person; p.tty = false; return p }()} {
		if code, _, errs := runAs(e, "public"); code != 2 || !strings.Contains(errs, "终端") {
			t.Fatalf("public without a terminal: %d %q", code, errs)
		}
	}
	// What ir public works with: Iridium's own directories, the daemon over the socket.
	o, err := publicOptions(person, &bytes.Buffer{})
	if err != nil || o.Dir != filepath.Join(person.cfg.StatePath(), "cloudflared") || !strings.HasSuffix(o.UnitDir, filepath.Join("systemd", "user")) {
		t.Fatalf("public options: %+v %v", o, err)
	}
	if link, until, err := o.Daemon.Invite(context.Background()); err != nil || !strings.HasPrefix(link, "https://ir.example.com/_ir/join#") || time.Until(until) < 9*time.Minute {
		t.Fatalf("the daemon's invite: %v %v", until, err)
	}
	if err := o.Daemon.SetOrigin(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	// No updater in this daemon.
	if code, _, _ := runAs(person, "update", "--check"); code != 1 {
		t.Fatal("update without an updater")
	}
}

func TestDetect(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	cfg := config.Default()
	cfg.Socket = filepath.Join(dir, "ir.sock")
	cfg.Save(path)
	t.Setenv("IRIDIUM_CONFIG", path)
	t.Setenv("CLAUDECODE", "1")
	t.Setenv("IRIDIUM_SESSION", "s1")
	e, err := detect()
	if err != nil || !e.agent || e.session != "s1" || e.cfg.Socket != cfg.Socket {
		t.Fatalf("detect: %+v %v", e, err)
	}
	// Not running: a plain error, nothing else.
	if code, _, errs := runAs(e, "apps"); code != 1 || !strings.Contains(errs, "没在运行") {
		t.Fatalf("no daemon: %d %q", code, errs)
	}
}
