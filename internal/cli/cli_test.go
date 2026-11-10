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
	"github.com/kumoSleeping/iridium/internal/discover"
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
	poll, replyLag = 50*time.Millisecond, 500*time.Millisecond
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

// ir ls shows a subagent only while it works, as the sidebar does.
func TestAtWork(t *testing.T) {
	now := time.UnixMilli(10 * 60_000)
	byID := map[string]api.Session{"p": {ID: "p", Status: api.StatusIdle, Live: true}, "q": {ID: "q", Status: api.StatusIdle}}
	recent := now.Add(-time.Second).UnixMilli()
	for _, c := range []struct {
		s    api.Session
		want bool
	}{
		{api.Session{Subagent: true, Parent: "p", UpdatedAt: recent}, true},
		{api.Session{Subagent: true, Parent: "p", UpdatedAt: recent, Done: true}, false},
		{api.Session{Subagent: true, Parent: "p", UpdatedAt: now.Add(-6 * time.Minute).UnixMilli()}, false},
		{api.Session{Subagent: true, Parent: "q", UpdatedAt: recent}, false},
		{api.Session{Subagent: true, Parent: "gone", UpdatedAt: recent}, false},
	} {
		if got := atWork(c.s, byID, now); got != c.want {
			t.Errorf("%+v: %v", c.s, got)
		}
	}
	ts := turns([]api.Block{{Kind: api.BlockKindAssistant, Text: "carried on"}, {Kind: api.BlockKindUser, Text: "hi"}, {Kind: api.BlockKindAssistant, Text: "a"}, {Kind: api.BlockKindTool}, {Kind: api.BlockKindAssistant, Text: "final"}, {Kind: api.BlockKindUser, Text: "next"}})
	if len(ts) != 3 || ts[0].asked != "" || ts[0].reply != "carried on" || ts[1].reply != "final" || ts[2].reply != "" {
		t.Errorf("turns: %+v", ts)
	}
	order := underParents([]api.Session{{ID: "k", Subagent: true, Parent: "p"}, {ID: "x"}, {ID: "p"}, {ID: "o", Subagent: true, Parent: "gone"}})
	if got := []string{order[0].ID, order[1].ID, order[2].ID, order[3].ID}; !reflect.DeepEqual(got, []string{"x", "p", "k", "o"}) {
		t.Errorf("underParents: %v", got)
	}
	if clip("a\n b  c", 3) != "a b…" {
		t.Errorf("clip: %q", clip("a\n b  c", 3))
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
	for arg, peer := range map[string]bool{"mac:~/dir": true, "kumo-m2:/tmp/a": true, "工作站:/x": true, "/abs:path": false, "./a:b": false, "plain": false, "~/x": false, "host:/tmp": false, "主机:~/x": false} {
		_, err := localPath(arg)
		if _, isPeer := err.(errPeer); isPeer != peer {
			t.Errorf("%s: %v", arg, err)
		}
	}
	if p, err := localPath("host:/tmp/x"); err != nil || p != "/tmp/x" {
		t.Errorf("host:/tmp/x: %q %v", p, err)
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
	cfg.CrashResume = false // the crash below must stay one
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
	var s *server.Server
	// Discovery too: what lies over a session (ir archive) is its routes'.
	empty := t.TempDir()
	d := discover.New(discover.Options{ClaudeDir: cfg.ClaudeHome, CodexHome: empty, PiSessions: empty, DshHome: empty, Store: st, Hub: h, Procs: discover.ProcFS{Root: empty}, Logf: t.Logf, Settle: 10 * time.Millisecond})
	m, err := session.New(session.Options{Config: cfg, Store: st, Hub: h, Launcher: l, Exe: os.Args[0], UnitPrefix: "iridium-test-", Logf: t.Logf, Poll: 10 * time.Millisecond, ReadEvery: 20 * time.Millisecond, Discovery: d, Tag: func(id string) string { return s.Tag(id) }})
	if err != nil {
		t.Fatal(err)
	}
	d.SetOnChange(m.Refresh)
	if err := d.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Close)
	if err := m.Start(); err != nil {
		t.Fatal(err)
	}
	s = server.New(cfg, m, h, t.Logf)
	s.Discover, s.Folders = d, discover.DefaultFolders(root)
	t.Cleanup(s.Close)
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
		testagent.KillAll(l, "iridium-test-")
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
	if err != nil || !strings.Contains(string(prompt), "This session is hosted by Iridium.") || !strings.Contains(string(prompt), "ir send <id>") {
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
	if err := (&env{}).client(cfg).do("POST", "/api/sessions/"+full+"/answer", api.CardAnswer{Card: "ask:" + full + ":ask-1", Option: "allow"}, &ok); err != nil {
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

	if r := ir("ls"); r.code != 0 || !strings.HasPrefix(r.out, sid+"\tclaude\tidle\t") || !strings.HasSuffix(r.out, "\thello\n") { // its first message names it
		t.Errorf("ls: %+v", r)
	}
	// Archived (no shown marker): only ir ls --all lists it.
	if st, err := store.Open(filepath.Join(cfg.StateDir, "iridium.db")); err == nil {
		st.Hide("claude", full, 1<<40, 0)
		st.Close()
	}
	if r := ir("ls"); r.code != 0 || strings.Contains(r.out, sid) {
		t.Errorf("ls with it archived: %+v", r)
	}
	if r := ir("ls", "--all"); r.code != 0 || !strings.Contains(r.out, sid) {
		t.Errorf("ls --all: %+v", r)
	}
	if st, err := store.Open(filepath.Join(cfg.StateDir, "iridium.db")); err == nil {
		st.Show("claude", full)
		st.Close()
	}
	// read: each message and its final reply, cut short; -n the last ones; --full the page. ir runs
	// in caller-session here, so what it sent is a letter from there.
	if r := ir("read", sid[:6]); r.code != 0 || !strings.Contains(r.out, "> [ir:caller-session] hello\n  ok: hello\n") || !strings.Contains(r.out, "  ok: again\n") {
		t.Errorf("read: %+v", r)
	}
	if r := ir("read", sid, "-n", "1"); r.code != 0 || strings.Contains(r.out, "hello") || !strings.HasPrefix(r.out, "> ") || strings.Count(r.out, "> ") != 1 {
		t.Errorf("read -n 1: %+v", r)
	}
	if r := ir("read", sid, "--full"); r.code != 0 || !strings.Contains(r.out, "> [ir:caller-session] hello\n\nok: hello\n\n") {
		t.Errorf("read --full: %+v", r)
	}
	if r := ir("read", sid, "-n", "0"); r.code != 1 {
		t.Errorf("read -n 0: %+v", r)
	}
	// find: by its words, case aside; nothing found is an error.
	if r := ir("find", "OK:", "SEE"); r.code != 0 || !strings.HasPrefix(r.out, sid+"\t") {
		t.Errorf("find: %+v", r)
	}
	if r := ir("find", "no-such-words"); r.code != 1 || !strings.Contains(r.err, "没找到") {
		t.Errorf("find nothing: %+v", r)
	}
	// archive and unarchive, as the sidebar's menu: out of ls, still found, back.
	if r := ir("archive", sid); r.code != 0 {
		t.Errorf("archive: %+v", r)
	}
	if r := ir("ls"); strings.Contains(r.out, sid) {
		t.Errorf("ls after archive: %+v", r)
	}
	if r := ir("find", "hello"); r.code != 0 || !strings.Contains(r.out, "[归档]") {
		t.Errorf("find archived: %+v", r)
	}
	// Archived, its agent stops: a send while it is still stopping meets no holder.
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		var st api.SessionState
		if err := (&env{}).client(cfg).do("GET", "/api/sessions/"+full, nil, &st); err == nil && !st.Live {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the archived session's agent still runs")
		}
	}
	if r := ir("unarchive", sid); r.code != 0 {
		t.Errorf("unarchive: %+v", r)
	}
	if r := ir("ls"); !strings.Contains(r.out, sid) {
		t.Errorf("ls after unarchive: %+v", r)
	}
	// rename, run in a session: the agent's name, cleaned, over its first message; never over the
	// user's. Without an id, the session it runs in (caller-session is none here).
	if r := ir("rename", sid, "「会话命名」。"); r.code != 0 {
		t.Errorf("rename: %+v", r)
	}
	if r := ir("ls"); !strings.HasSuffix(strings.TrimSpace(r.out), "\t会话命名") {
		t.Errorf("ls after rename: %+v", r)
	}
	if err := (&env{}).client(cfg).do("PATCH", "/api/sessions/"+full, api.SessionPatch{Name: new("mine")}, nil); err != nil {
		t.Fatal(err)
	}
	if r := ir("rename", sid, "agent's"); r.code != 1 || !strings.Contains(r.err, "用户起的名") {
		t.Errorf("rename over the user's: %+v", r)
	}
	if r := ir("rename", "own"); r.code != 1 || !strings.Contains(r.err, "no such session") {
		t.Errorf("rename its own, not a session: %+v", r)
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
	// No 机器/<id> for a session (decisions 24): ir send 应用/登录 is a notice to a login; none
	// there is an error saying so.
	if r := ir("wait", "mac/"+sid); r.code != 1 || !strings.Contains(r.err, "no session mac/") {
		t.Errorf("machine/id: %+v", r)
	}
	if r := ir("send", "mac/"+sid, "x"); r.code != 1 || !strings.Contains(r.err, "no such app: mac") {
		t.Errorf("send to a login: %+v", r)
	}
	if r := ir("send", "ir_web/nobody", "x"); r.code != 1 || !strings.Contains(r.err, "no such login") {
		t.Errorf("send to no login: %+v", r)
	}
	if r := ir("send", "ir_web/nobody", "x", "--wait"); r.code != 1 || !strings.Contains(r.err, "usage: ir send 应用/登录") {
		t.Errorf("send to a login --wait: %+v", r)
	}
	// ir share: a session, a folder; to no login, an error saying so.
	for _, what := range []string{sid, work} {
		if r := ir("share", "ir_web/nobody", what, "看看"); r.code != 1 || !strings.Contains(r.err, "no such login") {
			t.Errorf("share %s: %+v", what, r)
		}
	}
	if r := ir("share", sid); r.code != 1 || !strings.Contains(r.err, "usage: ir share") {
		t.Errorf("share without a login: %+v", r)
	}
	// A place at an app not there; the host's by its name.
	if r := ir("new", "claude", "mac:~/dir", "x"); r.code != 1 || !strings.Contains(r.err, "no computer mac") {
		t.Errorf("place at mac: %+v", r)
	}
	if r := ir("new", "claude", "host:"+work, ""); r.code != 0 {
		t.Errorf("place at the host: %+v", r)
	} else if ls := ir("ls"); !strings.Contains(ls.out, "\thost:"+work) && !strings.Contains(ls.out, "\thost:~") {
		t.Errorf("ls without the host's prefix: %q", ls.out)
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
	// machine:path goes to that machine's executor (none here).
	if r := ir("cp", "mac:~/a", dst); r.code != 1 || !strings.Contains(r.err, "no executors here") {
		t.Errorf("cp from another machine: %+v", r)
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

// ir ws: a session makes its folder's group; listed, renamed by id or name; nothing else.
func TestWorkspaceCommands(t *testing.T) {
	_, work := rig(t)
	if r := ir("ws"); r.code != 0 || r.out != "" {
		t.Errorf("ws, no session: %+v", r)
	}
	dir := filepath.Join(work, "proj")
	os.MkdirAll(dir, 0o700)
	r := ir("new", "claude", dir, "hello", "--wait")
	if r.code != 0 {
		t.Fatalf("new: %+v", r)
	}
	r = ir("ws")
	if r.code != 0 || !strings.HasSuffix(r.out, "\t"+filepath.Base(dir)+"\thost:"+dir+"\t1\n") {
		t.Fatalf("ws: %+v", r)
	}
	id := strings.Split(r.out, "\t")[0]
	if r := ir("ws", "rename", filepath.Base(dir), "Bee"); r.code != 0 {
		t.Errorf("ws rename by name: %+v", r)
	}
	if r := ir("ws"); !strings.HasPrefix(r.out, id+"\tBee\t") {
		t.Errorf("ws after rename: %+v", r)
	}
	for _, args := range [][]string{{"new", dir}, {"rm", id}, {"mv", "x", id}, {"nope"}} {
		if r := ir(append([]string{"ws"}, args...)...); r.code != 1 || !strings.Contains(r.err, "usage") {
			t.Errorf("ws %v: %+v", args, r)
		}
	}
}
