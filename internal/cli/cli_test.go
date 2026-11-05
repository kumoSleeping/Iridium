package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
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
	"github.com/kumoSleeping/iridium/internal/testagent"
)

// The test binary is also the holder and, through a link named claude, a fake Claude.
func TestMain(m *testing.M) {
	if code, ok := testagent.Main(); ok {
		os.Exit(code)
	}
	os.Exit(m.Run())
}

func TestHelp(t *testing.T) {
	var out, errs bytes.Buffer
	if code := run([]string{"--help"}, &out, &errs); code != 0 || out.String() != Help {
		t.Fatalf("--help: %d %q", code, out.String())
	}
	if n := strings.Count(Help, "\n"); n > 20 {
		t.Errorf("help is %d lines, more than 20", n)
	}
	if code := run([]string{"nope"}, &out, &errs); code != 1 || !strings.Contains(errs.String(), "no command") {
		t.Errorf("nope: %d %q", code, errs.String())
	}
}

func TestParse(t *testing.T) {
	o, err := parse([]string{"id", "--wait", "-a", "x.png", "msg", "--attach=y.txt", "--", "--not-a-flag"}, "wait attach")
	if err != nil || !o.wait || !reflect.DeepEqual(o.attach, []string{"x.png", "y.txt"}) || !reflect.DeepEqual(o.pos, []string{"id", "msg", "--not-a-flag"}) {
		t.Errorf("parse: %+v %v", o, err)
	}
	if _, err := parse([]string{"--model", "haiku"}, "wait"); err == nil {
		t.Error("took a flag not allowed")
	}
	if _, err := parse([]string{"--model"}, "model"); err == nil {
		t.Error("took a flag without its value")
	}
	for arg, peer := range map[string]bool{"mac:~/dir": true, "kumo-m2:/tmp/a": true, "/abs:path": false, "./a:b": false, "plain": false, "~/x": false} {
		_, err := localPath(arg)
		if _, isPeer := err.(errPeer); isPeer != peer {
			t.Errorf("%s: %v", arg, err)
		}
	}
	if short("abcdefgh1234", []string{"abcdefgh1234", "abcdefgh5678"}) != "abcdefgh1" {
		t.Error("short id")
	}
}

// rig runs a daemon with the fake Claude on a socket in a temp dir.
func rig(t *testing.T) (config.Config, string) {
	t.Helper()
	root, _ := filepath.EvalSymlinks(t.TempDir())
	bin := filepath.Join(root, "bin")
	os.MkdirAll(bin, 0o700)
	os.Symlink(os.Args[0], filepath.Join(bin, "claude"))
	cfg := config.Default()
	cfg.Listen = "127.0.0.1:0"
	cfg.StateDir = filepath.Join(root, "state")
	cfg.Socket = filepath.Join(root, "run", "ir.sock")
	cfg.ClaudeHome = filepath.Join(root, "claude")
	cfg.Programs["claude"] = filepath.Join(bin, "claude")
	os.MkdirAll(cfg.StateDir, 0o700)
	st, err := store.Open(filepath.Join(cfg.StateDir, "iridium.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	h := hub.New()
	l := &platform.Direct{}
	m, err := session.New(session.Options{Config: cfg, Store: st, Hub: h, Launcher: l, Exe: os.Args[0], UnitPrefix: "iridium-test-", Logf: t.Logf, Poll: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Start(); err != nil {
		t.Fatal(err)
	}
	s := server.New(cfg, m, h, t.Logf)
	tcp, unix, err := s.Listen()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Serve(ctx, tcp, unix); close(done) }()
	t.Cleanup(func() {
		cancel()
		<-done
		m.Close()
		names, _ := l.List(context.Background(), "iridium-test-")
		for _, n := range names {
			l.Kill(context.Background(), n, "KILL", true)
		}
	})
	t.Setenv("IRIDIUM_SOCKET", cfg.SocketPath())
	t.Setenv("IRIDIUM_SESSION", "caller-session")
	work := filepath.Join(root, "work")
	os.MkdirAll(work, 0o700)
	return cfg, work
}

type result struct {
	code     int
	out, err string
}

func ir(args ...string) result {
	var out, errs bytes.Buffer
	code := run(args, &out, &errs)
	return result{code, out.String(), errs.String()}
}

// The agent commands against the fake Claude, and wait's exit codes: 0 done, 2 needs you, 1
// the agent ended badly.
func TestAgentCommands(t *testing.T) {
	cfg, work := rig(t)

	r := ir("new", "claude", work, "hello", "--wait")
	lines := strings.Split(strings.TrimSpace(r.out), "\n")
	if r.code != 0 || len(lines) != 2 || len(lines[0]) != 8 || lines[1] != "ok: hello" {
		t.Fatalf("new --wait: %+v", r)
	}
	sid := lines[0]
	var full string
	for _, id := range (&env{}).idsFrom(t, cfg) {
		if strings.HasPrefix(id, sid) {
			full = id
		}
	}
	prompt, err := os.ReadFile(filepath.Join(cfg.StatePath(), "hold", full, "system-prompt.md"))
	if err != nil || !strings.Contains(string(prompt), "用 ir 开别的 agent 会话、发消息、等结果、跨机器拷文件；见 ir --help") {
		t.Errorf("system prompt %q %v", prompt, err)
	}

	if r := ir("send", sid, "again", "--wait"); r.code != 0 || r.out != "ok: again\n" {
		t.Errorf("send --wait: %+v", r)
	}
	// send without --wait returns once the agent took it; wait then waits for that turn.
	if r := ir("send", sid, "third"); r.code != 0 || r.out != "" {
		t.Errorf("send: %+v", r)
	}
	if r := ir("wait", sid); r.code != 0 || r.out != "ok: third\n" {
		t.Errorf("wait: %+v", r)
	}

	// An ask: 2, and what it asks.
	r = ir("send", sid, "please ASK", "--wait")
	if r.code != 2 || !strings.Contains(r.out, "需要你：审批 Bash") || !strings.Contains(r.out, `"command":"ls"`) {
		t.Fatalf("ask: %+v", r)
	}
	if r := ir("wait", sid); r.code != 2 {
		t.Errorf("wait with the ask open: %+v", r)
	}
	var ok map[string]any
	if err := (&env{}).client(cfg).do("POST", "/api/sessions/"+full+"/answer", api.Answer{RequestID: "ask-1", Kind: "permission", Allow: true}, &ok); err != nil {
		t.Fatal(err)
	}
	if r := ir("wait", sid); r.code != 0 || r.out != "ok: please ASK\n" {
		t.Errorf("wait after the answer: %+v", r)
	}

	// An attachment: its path in the message.
	att := filepath.Join(work, "notes.txt")
	os.WriteFile(att, []byte("notes"), 0o600)
	if r := ir("send", sid, "see", "-a", att, "--wait"); r.code != 0 || r.out != "ok: see\n\n[附件] `"+att+"`\n" {
		t.Errorf("send -a: %+v", r)
	}

	if r := ir("ls"); r.code != 0 || !strings.HasPrefix(r.out, sid+"\tclaude\tidle\t") || !strings.Contains(r.out, "hello") {
		t.Errorf("ls: %+v", r)
	}
	if r := ir("read", sid[:6]); r.code != 0 || !strings.Contains(r.out, "> [本机] hello\n") || !strings.Contains(r.out, "ok: again") {
		t.Errorf("read: %+v", r)
	}
	if r := ir("wait", "ffffffff"); r.code != 1 || !strings.Contains(r.err, "no session") {
		t.Errorf("unknown id: %+v", r)
	}

	// The agent crashes on this one: 1.
	if r := ir("send", sid, "now crash", "--wait"); r.code != 1 || !strings.Contains(r.err, "ended badly") {
		t.Errorf("crash: %+v", r)
	}
	if r := ir("stop", sid); r.code != 0 {
		t.Errorf("stop: %+v", r)
	}
	if r := ir("send", "mac/"+sid, "x"); r.code != 1 || !strings.Contains(r.err, "peer not connected: mac") {
		t.Errorf("peer id: %+v", r)
	}
	if r := ir("new", "claude", "mac:~/dir", "x"); r.code != 1 || !strings.Contains(r.err, "peer not connected: mac") {
		t.Errorf("peer dir: %+v", r)
	}
}

func TestFileCommands(t *testing.T) {
	_, work := rig(t)
	data := bytes.Repeat([]byte("x"), 100000)
	src := filepath.Join(work, "a.bin")
	os.WriteFile(src, data, 0o600)
	dst := filepath.Join(work, "out")
	os.Mkdir(dst, 0o700)
	sum := sha256.Sum256(data)
	want := filepath.Join(dst, "a.bin") + "\t100000\t" + hex.EncodeToString(sum[:]) + "\n"
	if r := ir("cp", src, dst); r.code != 0 || r.out != want {
		t.Errorf("cp: %+v", r)
	}
	if r := ir("cp", src, dst); r.code != 1 || !strings.Contains(r.err, "never overwritten") {
		t.Errorf("cp over: %+v", r)
	}
	if r := ir("cp", "mac:~/a", dst); r.code != 1 || !strings.Contains(r.err, "peer not connected: mac") {
		t.Errorf("cp from a peer: %+v", r)
	}
	r := ir("files", work)
	if r.code != 0 || !strings.HasPrefix(r.out, "a.bin\t100000\t") || !strings.Contains(r.out, "out/\t0\t") {
		t.Errorf("files: %+v", r)
	}
	home, _ := os.UserHomeDir()
	if r := ir("files", filepath.Join(home, ".ssh")); r.code != 1 || !strings.Contains(r.err, "denied") {
		t.Errorf("files ~/.ssh: %+v", r)
	}
}

func (*env) client(cfg config.Config) *env {
	sock := cfg.SocketPath()
	return &env{sock: sock, hc: &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", sock)
	}}}}
}

func (e *env) idsFrom(t *testing.T, cfg config.Config) []string { return e.client(cfg).ids() }
