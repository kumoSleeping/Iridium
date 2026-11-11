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

// What ir api shows: a list a line an item (a table of the fields named), an object a field a
// line with its arrays' fields as tables, nothing empty, words cut short.
func TestShown(t *testing.T) {
	show := func(c *apiCall, raw string) string {
		var out bytes.Buffer
		(&env{out: &out}).render(c, []byte(raw))
		return out.String()
	}
	c := &apiCall{method: "GET", path: "/v1/x/y", clip: 5}
	if got := show(c, `[{"id":"a","name":"long name","on":false,"tags":["p","q"],"o":{"k":1}}]`); got != "id=a  name=long …(+4)  o.k=1  tags=p,q\n" {
		t.Errorf("list: %q", got)
	}
	c.pick = []string{"id", "o"}
	if got := show(c, `{"data":[{"id":"a","o":{"k":1}},{"id":"b"}],"next":"2"}`); got != "id\to\na\tk=1\nb\t-\n(next=2; --all takes every page)\n" {
		t.Errorf("table: %q", got)
	}
	c.pick = []string{"n", "apps.id", "apps.logins.id"}
	if got := show(c, `{"n":3,"apps":[{"id":"x","logins":[{"id":"l1"},{"id":"l2"}]}]}`); got != "n: 3\napps:\nid\tlogins.id\nx\tl1,l2\n" {
		t.Errorf("object: %q", got)
	}
	c.pick, c.clip = []string{"content"}, 0
	if got := show(c, `[{"content":"<iridium:user from=\"web\"/>hi &lt;b&gt;"},{"content":"done"}]`); got != "content\n> [web] hi <b>\ndone\n" {
		t.Errorf("content: %q", got)
	}
	if got := show(c, `[{"content":"<iridium:tool id=\"1\" name=\"Bash\"><iridium:input>{&quot;command&quot;:&quot;ls&quot;}</iridium:input><iridium:result>out</iridium:result></iridium:tool>"}]`); got != "content\n[tool Bash] {\"command\":\"ls\"}\n" {
		t.Errorf("tool: %q", got)
	}
	c.pick = []string{"a", "b"}
	if got := show(c, `{"a":1,"b":""}`); got != "a: 1\n" {
		t.Errorf("empty field: %q", got)
	}
	c.pick, c.grep = nil, []string{"done"}
	if got := show(c, `[{"content":"hi"},{"content":"done"}]`); got != "content=done\n" {
		t.Errorf("grep: %q", got)
	}
	if clip("a\n b  c", 3) != "a b…" {
		t.Errorf("clip: %q", clip("a\n b  c", 3))
	}
}

// ir api's arguments: a path or a Satori method, the method, the body, the query.
func TestParseAPI(t *testing.T) {
	c, err := parseAPI([]string{"internal/sessions/ab/model", "model=haiku", "n:=2", "a.b=c", "--pick", "id,x"})
	if err != nil || c.method != "POST" || c.path != "/v1/internal/sessions/ab/model" || !reflect.DeepEqual(c.body, map[string]any{"model": "haiku", "n": 2.0, "a": map[string]any{"b": "c"}}) || !reflect.DeepEqual(c.pick, []string{"id", "x"}) {
		t.Errorf("parse: %+v %v", c, err)
	}
	if c, err = parseAPI([]string{"PUT", "/v1/internal/x", "k=v"}); err != nil || c.method != "PUT" || c.path != "/v1/internal/x" {
		t.Errorf("PUT: %+v %v", c, err)
	}
	if c, err = parseAPI([]string{"internal/usage", "days==3"}); err != nil || c.method != "GET" || c.path != "/v1/internal/usage?days=3" {
		t.Errorf("query: %+v %v", c, err)
	}
	if c, err = parseAPI([]string{"channel.list", "--all"}); err != nil || !c.satori || c.method != "POST" || c.path != "/v1/channel.list" {
		t.Errorf("satori: %+v %v", c, err)
	}
	for _, bad := range [][]string{{"GET", "internal/x", "k=v"}, {"PUT", "channel.list"}, {"internal/x", "--all"}, {"x/y", "nokey"}, {}} {
		if _, err := parseAPI(bad); err == nil {
			t.Errorf("%q: no error", bad)
		}
	}
}

// rig runs a daemon with the fake Claude on a socket in a temp dir.
func rig(t *testing.T) (config.Config, string) {
	t.Helper()
	root, _ := filepath.EvalSymlinks(t.TempDir())
	bin := filepath.Join(root, "bin")
	os.MkdirAll(bin, 0o700)
	cfg := config.Default()
	cfg.CrashResume = false // the crash below must stay one
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

	r := ir("api", "POST", "internal/sessions", "agent=claude", "cwd="+work, "text=hello", "--wait")
	lines := strings.Split(strings.TrimSpace(r.out), "\n")
	if r.code != 0 || len(lines) != 2 || len(lines[0]) != 36 || lines[1] != "ok: hello" {
		t.Fatalf("POST internal/sessions --wait: %+v", r)
	}
	sid := lines[0][:8]
	var full string
	for _, id := range (&env{}).idsFrom(t, cfg) {
		if strings.HasPrefix(id, sid) {
			full = id
		}
	}
	prompt, err := os.ReadFile(filepath.Join(cfg.StatePath(), "hold", full, "system-prompt.md"))
	if err != nil || !strings.Contains(string(prompt), "This session is hosted by Iridium.") || !strings.Contains(string(prompt), "ir api [METHOD]") {
		t.Errorf("system prompt %q %v", prompt, err)
	}

	send := func(id, text string, more ...string) result {
		return ir(append([]string{"api", "message.create", "channel_id=" + id, "_data.text=" + text}, more...)...)
	}
	if r := send(sid, "again", "--wait"); r.code != 0 || r.out != "ok: again\n" {
		t.Errorf("message.create --wait: %+v", r)
	}
	// Without --wait it returns once the agent took it; wait then waits for that turn.
	if r := send(sid, "third"); r.code != 0 || r.out != "" {
		t.Errorf("message.create: %+v", r)
	}
	if r := ir("api", "internal/sessions/"+sid, "--wait"); r.code != 0 || r.out != "ok: third\n" {
		t.Errorf("wait: %+v", r)
	}
	// _data.text is plain words: what looks like markup goes as it is.
	if r := send(sid, "a <b> & c", "--wait"); r.code != 0 || r.out != "ok: a <b> & c\n" {
		t.Errorf("_data.text escaped: %+v", r)
	}

	// An ask: 2, and what it asks.
	r = send(sid, "please ASK", "--wait")
	if r.code != 2 || !strings.Contains(r.out, "需要你：审批 Bash") || !strings.Contains(r.out, `"command":"ls"`) {
		t.Fatalf("ask: %+v", r)
	}
	if r := ir("api", "internal/sessions/"+sid, "--wait"); r.code != 2 {
		t.Errorf("wait with the ask open: %+v", r)
	}
	var ok map[string]any
	if err := (&env{}).client(cfg).do("POST", "/v1/internal/sessions/"+full+"/answer", api.CardAnswer{Card: "ask:" + full + ":ask-1", Option: "allow"}, &ok); err != nil {
		t.Fatal(err)
	}
	if r := ir("api", "internal/sessions/"+sid, "--wait"); r.code != 0 || r.out != "ok: please ASK\n" {
		t.Errorf("wait after the answer: %+v", r)
	}

	// An attachment: its path in the message.
	att := filepath.Join(work, "notes.txt")
	os.WriteFile(att, []byte("notes"), 0o600)
	if r := send(sid, "see", `_data.paths:=["`+att+`"]`, "--wait"); r.code != 0 || r.out != "ok: see\n\n[附件] `"+att+"`\n" {
		t.Errorf("message.create _data.paths: %+v", r)
	}

	ls := func(more ...string) result { return ir(append([]string{"api", "channel.list"}, more...)...) }
	// A table of the view's fields; its first message names it.
	if r := ls(); r.code != 0 || !strings.HasPrefix(r.out, "id\tname\t_data.status\t") || !strings.Contains(r.out, "\n"+full+"\thello\tidle\tclaude\t"+work+"\thello\n") {
		t.Errorf("channel.list: %+v", r)
	}
	if r := ir("api", "channel.list", "--json"); r.code != 0 || !strings.HasPrefix(r.out, `{"data":[{"id":"`+full+`"`) || !strings.Contains(r.out, `"_data":{"id":"`+full+`"`) {
		t.Errorf("channel.list --json: %+v", r)
	}
	// Archived (no shown marker): only with _data.all listed.
	if st, err := store.Open(filepath.Join(cfg.StateDir, "iridium.db")); err == nil {
		st.Hide("claude", full, 1<<40, 0)
		st.Close()
	}
	if r := ls(); r.code != 0 || strings.Contains(r.out, sid) {
		t.Errorf("channel.list with it archived: %+v", r)
	}
	if r := ls("_data.all:=true"); r.code != 0 || !strings.Contains(r.out, sid) {
		t.Errorf("channel.list _data.all: %+v", r)
	}
	if st, err := store.Open(filepath.Join(cfg.StateDir, "iridium.db")); err == nil {
		st.Show("claude", full)
		st.Close()
	}
	// message.list: a block a line, a person's after "> " and who. ir runs in caller-session here,
	// so what it sent is a letter from there; a prefix of the id will do.
	if r := ir("api", "message.list", "channel_id="+sid[:6]); r.code != 0 || !strings.Contains(r.out, "\t> [ir:caller-session] hello\n") || !strings.Contains(r.out, "\tok: again\n") {
		t.Errorf("message.list: %+v", r)
	}
	if r := ir("api", "message.list", "channel_id="+sid, "--grep", "AGAIN"); r.code != 0 || strings.Contains(r.out, "hello") || strings.Count(r.out, "> ") != 1 {
		t.Errorf("message.list --grep: %+v", r)
	}
	// --grep: by its words, case aside, in every field; nothing found, nothing shown.
	if r := ls("--grep", "OK: SEE"); r.code != 0 || !strings.Contains(r.out, "\n"+full+"\t") {
		t.Errorf("channel.list --grep: %+v", r)
	}
	if r := ls("--grep", "no-such-words"); r.code != 0 || r.out != "" {
		t.Errorf("--grep nothing: %+v", r)
	}
	// Archived and back, as the sidebar's menu: out of the list, still there with _data.all.
	if r := ir("api", "channel.delete", "channel_id="+sid); r.code != 0 {
		t.Errorf("channel.delete: %+v", r)
	}
	if r := ls(); strings.Contains(r.out, sid) {
		t.Errorf("channel.list after channel.delete: %+v", r)
	}
	if r := ls("_data.all:=true", "--grep", "hello", "--pick", "id,_data.archived"); r.code != 0 || !strings.Contains(r.out, full+"\ttrue\n") {
		t.Errorf("archived, found: %+v", r)
	}
	// Archived, its agent stops: a message while it is still stopping meets no holder.
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		var st api.SessionState
		if err := (&env{}).client(cfg).do("GET", "/v1/internal/sessions/"+full, nil, &st); err == nil && !st.Live {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the archived session's agent still runs")
		}
	}
	if r := ir("api", "channel.update", "channel_id="+sid, "_data.archived:=false"); r.code != 0 {
		t.Errorf("_data.archived false: %+v", r)
	}
	if r := ls(); !strings.Contains(r.out, sid) {
		t.Errorf("channel.list after it came back: %+v", r)
	}
	// A name, run in a session: the agent's, cleaned, over its first message; never over the user's.
	// self: the session it runs in (caller-session is none here).
	if r := ir("api", "channel.update", "channel_id="+sid, "data.name=「会话命名」。"); r.code != 0 {
		t.Errorf("data.name: %+v", r)
	}
	if r := ls(); !strings.Contains(r.out, full+"\t会话命名\t") {
		t.Errorf("channel.list after the name: %+v", r)
	}
	if err := (&env{}).client(cfg).do("PATCH", "/v1/internal/sessions/"+full, api.SessionPatch{Name: new("mine")}, nil); err != nil {
		t.Fatal(err)
	}
	if r := ir("api", "channel.update", "channel_id="+sid, "data.name=agent's"); r.code != 1 || !strings.Contains(r.err, "用户起的名") {
		t.Errorf("a name over the user's: %+v", r)
	}
	if r := ir("api", "channel.update", "channel_id=self", "data.name=own"); r.code != 1 || !strings.Contains(r.err, "no such session") {
		t.Errorf("self, not a session: %+v", r)
	}
	if r := ir("api", "internal/sessions/ffffffff", "--wait"); r.code != 1 || !strings.Contains(r.err, "no such session") {
		t.Errorf("unknown id: %+v", r)
	}
	// What ir api takes: a path or a method; k=v, k:=json or k==v. Alone, its help.
	if r := ir("api"); r.code != 0 || !strings.Contains(r.out, "# topics: ") {
		t.Errorf("ir api: %+v", r)
	}
	for _, bad := range [][]string{{"api", "channel.list", "nokey"}, {"api", "channel.list", "a:=nope"}, {"api", "channel.list", "--text", "x"}} {
		if r := ir(bad...); r.code != 1 || !strings.Contains(r.err, "usage: ir api") {
			t.Errorf("%v: %+v", bad, r)
		}
	}
	if r := ir("api", "no.such"); r.code != 1 || !strings.Contains(r.err, "no API no.such") {
		t.Errorf("no such method: %+v", r)
	}
	if r := ir("api", "login.get"); r.code != 0 || !strings.Contains(r.out, "platform: local\n") {
		t.Errorf("login.get: %+v", r)
	}

	// The agent crashes on this one: 1.
	if r := send(sid, "now crash", "--wait"); r.code != 1 || !strings.Contains(r.err, "ended badly") {
		t.Errorf("crash: %+v", r)
	}
	if r := ir("api", "POST", "internal/sessions/"+sid+"/stop"); r.code != 0 || r.out != "ok: true\n" {
		t.Errorf("stop: %+v", r)
	}
	if r := ir("api", "internal/sessions/mac/"+sid, "--wait"); r.code != 1 || r.err == "" {
		t.Errorf("machine/id: %+v", r)
	}
	// Sharing a session, a folder: to no login, an error saying so.
	for _, what := range [][]string{{"what=session", "session=" + sid}, {"what=place", "place=" + work}} {
		if r := ir(append([]string{"api", "POST", "internal/share", "to=ir_web/nobody", "note=看看"}, what...)...); r.code != 1 || !strings.Contains(r.err, "no such login") {
			t.Errorf("share %s: %+v", what, r)
		}
	}
	// A place at an app not there; the host's by its name.
	if r := ir("api", "POST", "internal/sessions", "agent=claude", "cwd=mac:~/dir", "text=x"); r.code != 1 || !strings.Contains(r.err, "no computer mac") {
		t.Errorf("place at mac: %+v", r)
	}
	if r := ir("api", "POST", "internal/sessions", "agent=claude", "cwd=host:"+work); r.code != 0 {
		t.Errorf("place at the host: %+v", r)
	} else if ls := ls(); strings.Count(ls.out, "\t"+work+"\t") != 2 {
		t.Errorf("a folder at the host, as it is: %q", ls.out)
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
	cp := func(from, to string) result { return ir("api", "POST", "internal/files/copy", "from="+from, "to="+to) }
	if r := cp(src, dst); r.code != 0 || r.out != want {
		t.Errorf("cp: %+v", r)
	}
	if r := cp(src, dst); r.code != 1 || !strings.Contains(r.err, "never overwritten") {
		t.Errorf("cp over: %+v", r)
	}
	// machine:path goes to that machine's executor (none here).
	if r := cp("mac:~/a", dst); r.code != 1 || !strings.Contains(r.err, "no executors here") {
		t.Errorf("cp from another machine: %+v", r)
	}
	r := ir("api", "internal/files", "path=="+work)
	if r.code != 0 || !strings.Contains(r.out, "entries:\nname\tsize\ttype\na.bin\t100000\tfile\n") || !strings.Contains(r.out, "out\t") {
		t.Errorf("files: %+v", r)
	}
	home, _ := os.UserHomeDir()
	if r := ir("api", "internal/files", "path=="+filepath.Join(home, ".ssh")); r.code != 1 || !strings.Contains(r.err, "denied") {
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

// Folder groups: a session makes its folder's group; listed, renamed through the API.
func TestWorkspaceCommands(t *testing.T) {
	_, work := rig(t)
	if r := ir("api", "guild.list"); r.code != 0 || r.out != "id\tname\t_data.path\nloose\t未分组\t-\n" {
		t.Errorf("guild.list, no session: %+v", r)
	}
	dir := filepath.Join(work, "proj")
	os.MkdirAll(dir, 0o700)
	r := ir("api", "POST", "internal/sessions", "agent=claude", "cwd="+dir, "text=hello", "--wait")
	if r.code != 0 {
		t.Fatalf("new: %+v", r)
	}
	r = ir("api", "guild.list")
	if r.code != 0 || !strings.Contains(r.out, "\t"+filepath.Base(dir)+"\t"+dir+"\n") {
		t.Fatalf("guild.list: %+v", r)
	}
	id := strings.Split(strings.Split(r.out, "\n")[1], "\t")[0]
	if r := ir("api", "PATCH", "internal/workspaces/"+id, "name=Bee"); r.code != 0 {
		t.Errorf("rename: %+v", r)
	}
	if r := ir("api", "guild.list"); !strings.Contains(r.out, "\n"+id+"\tBee\t") {
		t.Errorf("guild.list after rename: %+v", r)
	}
	if r := ir("api", "guild.get", "guild_id="+id, "--json"); r.code != 0 || !strings.Contains(r.out, `"name":"Bee"`) {
		t.Errorf("guild.get: %+v", r)
	}
	// A new session put in a group by its id.
	if r := ir("api", "POST", "internal/sessions", "agent=claude", "cwd="+work, "workspace:="+id); r.code != 0 {
		t.Errorf("new in a group: %+v", r)
	}
}
