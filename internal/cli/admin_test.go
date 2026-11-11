package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/agents"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/config"
	"github.com/kumoSleeping/iridium/internal/hub"
	"github.com/kumoSleeping/iridium/internal/platform"
	"github.com/kumoSleeping/iridium/internal/server"
	"github.com/kumoSleeping/iridium/internal/session"
	"github.com/kumoSleeping/iridium/internal/store"
	"github.com/kumoSleeping/iridium/internal/testagent"
)

// daemonFor runs a test daemon; person and agent are ir as run by a person at a terminal and by
// an agent.
func daemonFor(t *testing.T) (person, agent adminEnv) {
	t.Helper()
	// Not the session these tests run in (an agent's): ir api would say it runs there.
	for _, k := range append(agents.SessionVars(), "CLAUDECODE") {
		t.Setenv(k, "")
	}
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	os.MkdirAll(bin, 0o700)
	cfg := config.Default()
	cfg.Listen = "127.0.0.1:0"
	cfg.StateDir = filepath.Join(root, "state")
	cfg.Socket = filepath.Join(root, "run", "ir.sock")
	cfg.ClaudeHome = filepath.Join(root, "claude")
	cfg.Programs = testagent.Programs(t, bin)
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

// apiAs runs ir api as e: a person, or an agent (X-Iridium-Agent).
func apiAs(as adminEnv, args ...string) (int, string, string) {
	var out, errs bytes.Buffer
	e := &env{out: &out, err: &errs, sock: as.cfg.SocketPath(), hc: as.client, agent: as.agent}
	code := cmdAPI(e, args)
	return code, out.String(), errs.String()
}

// apiRun runs ir api as e in the background, while the test plays the panel.
func apiRun(e adminEnv, args ...string) chan [3]string {
	got := make(chan [3]string, 1)
	go func() {
		code, out, errs := apiAs(e, args...)
		got <- [3]string{string(rune('0' + code)), out, errs}
	}()
	return got
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
		var cs api.Needs
		as.call("GET", "/v1/internal/needs", nil, &cs)
		if len(cs.Pending) == 1 {
			var raw json.RawMessage
			if _, err := as.call("POST", "/v1/internal/needs/"+cs.Pending[0].ID, api.NeedAnswer{Allow: allow}, &raw); err != nil {
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

// pairApp makes app name by pairing, the one way an app comes in (POST /v1/internal/pair), allowed by the
// person: its TOKEN. claude: allowed as the owner's own.
func pairApp(t *testing.T, person adminEnv, name string, claude bool) string {
	t.Helper()
	if code, _, errs := apiAs(person, "PUT", "internal/pairing", "open:=true"); code != 0 {
		t.Fatalf("pairing open: %d %s", code, errs)
	}
	var p api.Pairing
	if _, err := person.call("POST", "/v1/internal/pair", api.PairRequest{Name: name}, &p); err != nil {
		t.Fatal(err)
	}
	var cs api.Needs
	person.call("GET", "/v1/internal/needs", nil, &cs)
	for _, c := range cs.Pending {
		if c.Code == p.Code {
			if _, err := person.call("POST", "/v1/internal/needs/"+c.ID, api.NeedAnswer{Allow: true, Owner: claude}, nil); err != nil {
				t.Fatal(err)
			}
		}
	}
	var st api.PairState
	if _, err := person.call("GET", "/v1/internal/pair/"+p.Request, nil, &st); err != nil || st.Status != "allowed" {
		t.Fatalf("pairing %s: %+v %v", name, st, err)
	}
	return st.Token
}

func TestAdmin(t *testing.T) {
	person, agent := daemonFor(t)

	// ir token is gone: an app comes by pairing. So are the commands ir api covers.
	for _, gone := range []string{"token", "invite", "apps", "profile", "secret", "usage", "needs", "allow", "deny"} {
		if _, ok := adminCommands[gone]; ok {
			t.Errorf("ir %s is still there", gone)
		}
	}
	pairApp(t, person, "bot", false)
	pairApp(t, person, "bot2", false)

	// Invites need a public origin; then a person gets the link and an agent does not: its asks a
	// person, and --wait hears only how it went.
	if code, _, errs := apiAs(agent, "POST", "internal/invites", "name=pad"); code != 1 || !strings.Contains(errs, "public origin") {
		t.Fatalf("invite without an origin: %d %q", code, errs)
	}
	if err := (daemon{person}).SetOrigin(context.Background(), "https://ir.example.com"); err != nil {
		t.Fatal(err)
	}
	if code, out, _ := apiAs(person, "POST", "internal/invites", "name=pad"); code != 0 || !strings.Contains(out, "https://ir.example.com/_ir/join#") {
		t.Fatalf("invite: %d %q", code, out)
	}
	got := apiRun(agent, "POST", "internal/invites", "name=tab", "--wait")
	panel := approve(t, person, true)
	r := <-got
	if r[0] != "0" || !strings.Contains(r[1], "state: allowed") || strings.Contains(r[1]+r[2], "/_ir/join") || !strings.Contains(panel, "/_ir/join#") {
		t.Fatalf("agent invite: %q / %s", r, panel)
	}
	got = apiRun(agent, "POST", "internal/invites", "name=tab2", "--wait")
	approve(t, person, false)
	if r := <-got; r[0] != "1" || !strings.Contains(r[1], "state: denied") {
		t.Fatalf("denied: %q", r)
	}
	if code, _, errs := apiAs(person, "POST", "internal/invites", "name=a/b"); code != 1 || errs == "" {
		t.Fatalf("a bad name: %d %q", code, errs)
	}

	// The list: the host, ir_web, the apps, their logins. No secrets in it.
	code, out, _ := apiAs(agent, "internal/apps", "--json")
	if code != 0 || !strings.Contains(out, `"bot2"`) || regexp.MustCompile(`ir_[A-Za-z0-9_-]{43}`).MatchString(out) || strings.Contains(out, "#") {
		t.Fatalf("apps: %d\n%s", code, out)
	}
	var as api.Apps
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &as); err != nil {
		t.Fatal(err)
	}
	idOf := func(name string) string {
		for _, a := range as.Apps {
			if a.Name == name {
				return a.ID
			}
		}
		t.Fatalf("no app %s", name)
		return ""
	}
	bot := idOf("bot")

	// Rename, avatar, device.
	if code, out, errs := apiAs(agent, "PUT", "internal/apps/"+bot+"/profile", "name=robot"); code != 0 || !strings.Contains(out, "robot") {
		t.Fatalf("rename: %d %q %q", code, out, errs)
	}
	for name, why := range map[string]string{"bot2": "taken", "x": "letter", "主机": "reserved", "a/b": "invalid"} {
		if code, _, errs := apiAs(agent, "PUT", "internal/apps/"+bot+"/profile", "name="+name); code != 1 || !strings.Contains(errs, why) {
			t.Fatalf("rename to %s: %d %q", name, code, errs)
		}
	}
	var img bytes.Buffer
	png.Encode(&img, image.NewRGBA(image.Rect(0, 0, 40, 30)))
	if code, out, errs := apiAs(agent, "PUT", "internal/apps/"+bot+"/profile", "avatar="+base64.StdEncoding.EncodeToString(img.Bytes())); code != 0 || !strings.Contains(out, "avatar") {
		t.Fatalf("avatar: %d %q %q", code, out, errs)
	}
	if code, out, errs := apiAs(agent, "PUT", "internal/apps/"+bot+"/device", "device=phone"); code != 0 || !strings.Contains(out, "phone") {
		t.Fatalf("device: %d %q %q", code, out, errs)
	}
	if code, _, _ := apiAs(agent, "PUT", "internal/apps/"+bot+"/device", "device=tablet"); code != 1 {
		t.Fatal("a device that is none")
	}

	// Remove: an app, by a person only; the host and ir_web are built in.
	if code, _, errs := apiAs(agent, "DELETE", "internal/apps/"+bot); code != 1 || errs == "" {
		t.Fatalf("an agent's rm: %d %q", code, errs)
	}
	if code, _, errs := apiAs(person, "DELETE", "internal/apps/"+bot); code != 0 {
		t.Fatalf("rm: %q", errs)
	}
	if _, out, _ := apiAs(agent, "internal/apps", "--json"); strings.Contains(out, `"robot"`) {
		t.Fatalf("removed, still listed:\n%s", out)
	}
	for _, ref := range []string{"nobody", "local", "ir_web"} {
		if code, _, _ := apiAs(person, "DELETE", "internal/apps/"+ref); code != 1 {
			t.Fatalf("removed %s", ref)
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
	if code, _, _ := apiAs(person, "POST", "internal/update/check"); code != 1 {
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
	if _, err := e.call("GET", "/v1/internal/me", nil, nil); err == nil || !strings.Contains(err.Error(), "没在运行") {
		t.Fatalf("no daemon: %v", err)
	}
}
