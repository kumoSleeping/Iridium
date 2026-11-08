package discover

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/remote"
	"github.com/kumoSleeping/iridium/internal/remote/executor"
	"github.com/kumoSleeping/iridium/internal/remote/wire"
	"github.com/kumoSleeping/iridium/internal/store"
)

// remoteRig is an index with an executor connected whose dsh home is a temp directory.
type remoteRig struct {
	t     *testing.T
	x     *Index
	hub   *remote.Hub
	st    *store.Store
	home  string // dsh's there
	work  string // a folder there
	state string
	dsh   string // the node executor's stand-in for the dsh it runs in (run.mjs); "" for the Go one
}

// executors runs test with each executor: the Go one, and the dsh plugin's (what DSH Desktop runs)
// if node is there.
func executors(t *testing.T, test func(t *testing.T, kind string)) {
	for _, k := range []string{"go", "node"} {
		t.Run(k, func(t *testing.T) {
			if _, err := exec.LookPath("node"); k == "node" && err != nil {
				t.Skip("no node")
			}
			test(t, k)
		})
	}
}

func newRemoteRig(t *testing.T, kind string) *remoteRig {
	t.Helper()
	there := t.TempDir()
	r := &remoteRig{t: t, hub: &remote.Hub{Logf: t.Logf}, home: filepath.Join(there, ".dsh"), work: filepath.Join(there, "work"), state: t.TempDir()}
	os.MkdirAll(r.work, 0o700)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { r.hub.Serve(w, req, "m1", "pc") }))
	t.Cleanup(srv.Close)
	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	agents := []wire.AgentInfo{{Agent: "dsh", Argv: []string{"dsh"}, Version: "0.2.2-rc.1", Home: r.home}}
	if kind == "node" {
		b, _ := json.Marshal(agents)
		r.dsh = filepath.Join(there, "desktop")
		os.MkdirAll(r.dsh, 0o700)
		r.setOpen()
		cmd := exec.Command("node", "../../plugins/dsh-remote-iridium/test/run.mjs", url, "tok", filepath.Join(there, "executor"))
		cmd.Env = append(os.Environ(), "IRIDIUM_TEST_AGENTS="+string(b), "IRIDIUM_TEST_DSH="+r.dsh)
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = cmd.Process.Signal(syscall.SIGTERM); _ = cmd.Wait() })
	} else {
		exe, err := executor.New(executor.Options{URL: url, State: filepath.Join(there, "executor"), Logf: t.Logf, Agents: agents})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { _ = exe.Run(ctx); close(done) }()
		t.Cleanup(func() { cancel(); <-done; exe.Close() })
	}
	var err error
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, ok := r.hub.Machine("m1").Hello(); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the executor did not connect")
		}
		time.Sleep(20 * time.Millisecond)
	}
	r.st, err = store.Open(filepath.Join(t.TempDir(), "iridium.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.st.Close() })
	r.st.SetMachine("m1", []byte(`{}`)) // as the server notes an executor that connects
	r.x = r.index()
	return r
}

// setOpen says which sessions DSH Desktop has open (the node executor's dsh.open).
func (r *remoteRig) setOpen(ids ...string) {
	b, _ := json.Marshal(append([]string{}, ids...))
	os.WriteFile(filepath.Join(r.dsh, "open.json"), b, 0o600)
}

func (r *remoteRig) index() *Index {
	x := New(Options{Store: r.st, State: r.state, Logf: r.t.Logf, RemoteScan: time.Hour})
	x.SetRemote(r.hub)
	return x
}

func (r *remoteRig) scan() {
	r.t.Helper()
	if err := r.x.ScanMachine(context.Background(), "m1"); err != nil {
		r.t.Fatal(err)
	}
}

func (r *remoteRig) sessions() map[string]api.Session {
	r.t.Helper()
	l, _ := r.x.Sessions(context.Background())
	out := map[string]api.Session{}
	for _, s := range l {
		out[s.ID] = s
	}
	return out
}

// write puts a file at p there, its mtime moved on (a look sees it changed).
func (r *remoteRig) write(p, data string) {
	r.t.Helper()
	os.MkdirAll(filepath.Dir(p), 0o700)
	if err := os.WriteFile(p, []byte(data), 0o600); err != nil {
		r.t.Fatal(err)
	}
	later := time.Now().Add(time.Duration(len(data)) * time.Millisecond)
	os.Chtimes(p, later, later)
	os.Chtimes(filepath.Dir(p), later, later)
}

// zlines is a dsh transcript: its header, then lines, a zstd frame each.
func zlines(t *testing.T, head string, lines ...string) string {
	t.Helper()
	enc, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close()
	out := enc.EncodeAll([]byte(head+"\n"), nil)
	for _, l := range lines {
		out = enc.EncodeAll([]byte(l+"\n"), out)
	}
	return string(out)
}

func (r *remoteRig) session(project, id, data string) string {
	p := filepath.Join(r.home, "sessions", project, id, "session.v4.jsonl.zstd")
	r.write(p, data)
	return p
}

func (r *remoteRig) summary(id, cwd, title string) {
	r.write(filepath.Join(r.home, "storages", "session_projcache", "sessions", id+".json"),
		`{"version":7,"record":{"identity":{"cwd":`+quote(cwd)+`},"rows":{"title":{"ver":1,"seq":1,"val":`+quote(title)+`},"timeContext":{"ver":3,"seq":1,"val":{"lastMessageTime":1794142333807}}}}}`)
}

func (r *remoteRig) registry(archived string, paths ...string) {
	ws, ids := "", ""
	for i, p := range paths {
		id := "w" + string(rune('0'+i))
		if i > 0 {
			ws, ids = ws+",", ids+","
		}
		ws += quote(id) + `:{"path":` + quote(p) + `,"title":` + quote(filepath.Base(p)) + `,"sessionIds":[],"createdAt":"2026-11-01T00:00:00.000Z","updatedAt":"2026-11-02T00:00:00.000Z"}`
		ids += quote(id)
	}
	arch := ""
	if archived != "" {
		arch = quote(archived)
	}
	r.write(filepath.Join(r.home, "storages", "workspace.json"),
		`{"unit":{"name":"workspace","version":2},"global":{"initialized":true,"workspaceIds":[`+ids+`],"archivedSessionIds":[`+arch+`],"pinnedSessionIds":[]},"tables":{"workspaces":{`+ws+`}}}`)
}

func quote(s string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + `"`
}

// dsh's sessions at a machine (docs/exec.md "远端 DSH"): all listed, with the folder and title
// its projection cache says (else its header's), subagents as such, the ones archived in DSH
// Desktop archived; its workspaces for a new session; a transcript mirrored when read.
func TestRemoteDshSessions(t *testing.T) { executors(t, testRemoteDshSessions) }

func testRemoteDshSessions(t *testing.T, kind string) {
	r := newRemoteRig(t, kind)
	r.session("--x-work--", "s1", zlines(t, `{"type":"session","cwd":`+quote(r.work)+`}`, `{"type":"event","seq":1}`))
	r.summary("s1", r.work, "fix the build")
	r.session("--x-work--", "s2", zlines(t, `{"type":"session","cwd":`+quote(r.work)+`,"origin":"subagent","parentSession":"s1"}`))
	r.session("--x-old--", "s3", zlines(t, `{"type":"session","cwd":`+quote(r.work)+`}`))
	os.MkdirAll(filepath.Join(r.home, "sessions", "--x-work--", "s4"), 0o700) // not written yet
	r.registry("s3", r.work, filepath.Join(r.work, "gone"))
	r.scan()

	got := r.sessions()
	s1, s2 := got["s1"], got["s2"]
	if len(got) != 3 || s1.Machine != "m1" || s1.Cwd != r.work || s1.Title != "fix the build" || s1.Agent != api.AgentDsh || s1.Offline {
		t.Fatalf("sessions %+v", got)
	}
	if !s2.Subagent || s2.Parent != "s1" || s2.Cwd != r.work {
		t.Errorf("subagent %+v", s2)
	}
	vis, _ := r.st.Visibility()
	if !vis.Shown("dsh", "s1") || !vis.Shown("dsh", "s2") || vis.Shown("dsh", "s3") {
		t.Errorf("shown: s1 %v s2 %v s3 %v", vis.Shown("dsh", "s1"), vis.Shown("dsh", "s2"), vis.Shown("dsh", "s3"))
	}
	ws, err := r.x.DshWorkspaces(context.Background(), "pc")
	if err != nil || len(ws) != 1 || ws[0].Path != r.work || ws[0].Title != "work" {
		t.Fatalf("workspaces %+v %v", ws, err)
	}

	// Read: mirrored here, then kept up as it grows there.
	_, f, ok := r.x.Found("s1")
	if !ok || f == nil {
		t.Fatal("s1 not found")
	}
	if err := r.x.Fetch(context.Background(), "s1"); err != nil {
		t.Fatal(err)
	}
	n, err := f.Size()
	if err != nil || n == 0 {
		t.Fatalf("mirror: %d %v", n, err)
	}
	r.session("--x-work--", "s1", zlines(t, `{"type":"session","cwd":`+quote(r.work)+`}`, `{"type":"event","seq":1}`, `{"type":"event","seq":2}`))
	r.summary("s1", r.work, "fix the build again")
	r.scan()
	if m, _ := f.Size(); m <= n || r.sessions()["s1"].Title != "fix the build again" {
		t.Errorf("after growth: %d (was %d), %+v", m, n, r.sessions()["s1"])
	}

	// Archived in DSH Desktop since: archived here too.
	r.registry("s1", r.work)
	r.scan()
	if vis, _ := r.st.Visibility(); vis.Shown("dsh", "s1") {
		t.Error("s1 archived there, shown here")
	}

	// A restart lists them before the machine is looked at again; gone there, gone here.
	r.x = r.index()
	r.x.loadRemote()
	if len(r.sessions()) != 3 {
		t.Fatalf("after a restart %+v", r.sessions())
	}
	os.RemoveAll(filepath.Join(r.home, "sessions", "--x-old--"))
	os.Chtimes(filepath.Join(r.home, "sessions"), time.Now().Add(time.Hour), time.Now().Add(time.Hour))
	r.scan()
	if _, ok := r.sessions()["s3"]; ok || len(r.sessions()) != 2 {
		t.Errorf("s3 gone there: %+v", r.sessions())
	}
}

// Without a registry dsh knows, the folders of its sessions stand in for its workspaces.
func TestRemoteDshWorkspacesFallback(t *testing.T) { executors(t, testRemoteDshWorkspacesFallback) }

func testRemoteDshWorkspacesFallback(t *testing.T, kind string) {
	r := newRemoteRig(t, kind)
	other := filepath.Join(filepath.Dir(r.work), "other")
	r.session("--x-work--", "s1", zlines(t, `{"type":"session","cwd":`+quote(r.work)+`}`))
	r.session("--x-other--", "s2", zlines(t, `{"type":"session","cwd":`+quote(other)+`}`))
	r.session("--x-other--", "s3", zlines(t, `{"type":"session","cwd":`+quote(other)+`}`))
	r.session("--x-other--", "s3a", zlines(t, `{"type":"session","cwd":`+quote(other)+`,"origin":"subagent","parentSession":"s3"}`))
	r.write(filepath.Join(r.home, "storages", "workspace.json"), `{"unit":{"name":"workspace","version":9}}`)
	ws, err := r.x.DshWorkspaces(context.Background(), "m1")
	if err != nil || len(ws) != 2 {
		t.Fatalf("%+v %v", ws, err)
	}
	if ws[0].Path != other && ws[0].Path != r.work {
		t.Errorf("%+v", ws)
	}
}

// A session open in DSH Desktop is shown run elsewhere and not resumed here, as the dsh plugin says
// (dsh.open); an executor that cannot say (the Go one) leaves them as they are.
func TestRemoteDshOpen(t *testing.T) { executors(t, testRemoteDshOpen) }

func testRemoteDshOpen(t *testing.T, kind string) {
	r := newRemoteRig(t, kind)
	r.session("--x-work--", "s1", zlines(t, `{"type":"session","cwd":`+quote(r.work)+`}`))
	r.scan()
	if kind == "node" {
		r.setOpen("s1", "unmade")
	}
	r.x.pollOpen("m1")
	s := r.sessions()["s1"]
	err := r.x.Free("s1")
	if kind == "go" {
		if s.Status != api.StatusIdle || s.Live || err != nil || !r.x.noOpen["m1"] {
			t.Fatalf("go: %+v, free %v, not asked again %v", s, err, r.x.noOpen["m1"])
		}
		return
	}
	// Shown as run elsewhere; not barred: dsh's writer lock there says whether it may be resumed.
	if s.Status != api.StatusElsewhere || !s.Live || err != nil {
		t.Fatalf("open in DSH Desktop: %+v, free %v", s, err)
	}
	r.setOpen()
	r.x.pollOpen("m1")
	if s := r.sessions()["s1"]; s.Status != api.StatusIdle || s.Live || r.x.Free("s1") != nil {
		t.Errorf("closed there: %+v", s)
	}
}

// Workspaces the dsh plugin's settings keep from Iridium (dsh.open's unsynced): their sessions are
// not listed, nor offered for a new session, at once; listed again once taken off.
func TestRemoteDshUnsynced(t *testing.T) { executors(t, testRemoteDshUnsynced) }

func testRemoteDshUnsynced(t *testing.T, kind string) {
	if kind != "node" {
		return // only the plugin says
	}
	r := newRemoteRig(t, kind)
	other := filepath.Join(r.work, "other")
	os.MkdirAll(other, 0o700)
	r.session("--x-work--", "s1", zlines(t, `{"type":"session","cwd":`+quote(r.work)+`}`))
	r.session("--x-other--", "s2", zlines(t, `{"type":"session","cwd":`+quote(other)+`}`))
	r.session("--x-other--", "s3", zlines(t, `{"type":"session","cwd":`+quote(other)+`}`))
	r.session("--x-other--", "s3a", zlines(t, `{"type":"session","cwd":`+quote(other)+`,"origin":"subagent","parentSession":"s3"}`))
	r.write(filepath.Join(r.home, "storages", "workspace.json"),
		`{"unit":{"name":"workspace","version":2},"global":{"initialized":true,"workspaceIds":["w0","w1"],"archivedSessionIds":[],"pinnedSessionIds":[]},"tables":{"workspaces":{`+
			`"w0":{"path":`+quote(r.work)+`,"title":"Secret","sessionIds":["s1"],"createdAt":"2026-11-01T00:00:00.000Z","updatedAt":"2026-11-02T00:00:00.000Z"},`+
			`"w1":{"path":`+quote(other)+`,"title":"Open","sessionIds":["s2"],"createdAt":"2026-11-01T00:00:00.000Z","updatedAt":"2026-11-02T00:00:00.000Z"}}}}`)
	r.setOpen()
	r.scan()
	if s := r.sessions(); len(s) != 4 {
		t.Fatalf("before: %+v", s)
	}
	unsynced := func(names ...string) {
		b, _ := json.Marshal(append([]string{}, names...))
		os.WriteFile(filepath.Join(r.dsh, "unsynced.json"), b, 0o600)
		r.x.pollOpen("m1")
	}
	titles := func() (out []string) {
		ws, err := r.x.DshWorkspaces(context.Background(), "m1")
		if err != nil {
			t.Fatal(err)
		}
		for _, w := range ws {
			out = append(out, w.Title)
		}
		return out
	}
	unsynced(" secret ")
	if s := r.sessions(); len(s) != 3 || s["s1"].ID != "" {
		t.Errorf("Secret kept from Iridium: %+v", s)
	}
	if got := titles(); !slices.Equal(got, []string{"Open"}) {
		t.Errorf("workspaces for a new session: %v", got)
	}
	r.scan() // a look after keeps it out
	if s := r.sessions(); len(s) != 3 || s["s1"].ID != "" {
		t.Errorf("after a look: %+v", s)
	}
	unsynced()
	if s := r.sessions(); len(s) != 4 {
		t.Errorf("synced again: %+v", s)
	}
	// By its folder too.
	unsynced(strings.ToUpper(other) + "/")
	if s := r.sessions(); len(s) != 3 || s["s2"].ID != "" { // s3 is in no workspace: still listed
		t.Errorf("by its folder: %+v", s)
	}
	// DSH's 未分组 kept too: s3, in no workspace, and its subagent.
	unsynced()
	os.WriteFile(filepath.Join(r.dsh, "ungrouped.json"), []byte("false"), 0o600)
	r.x.pollOpen("m1")
	if s := r.sessions(); len(s) != 2 || s["s3"].ID != "" || s["s3a"].ID != "" {
		t.Errorf("未分组 kept: %+v", s)
	}
	r.scan()
	if s := r.sessions(); len(s) != 2 {
		t.Errorf("未分组 kept, after a look: %+v", s)
	}
	os.Remove(filepath.Join(r.dsh, "ungrouped.json"))
	r.x.pollOpen("m1")
	if s := r.sessions(); len(s) != 4 {
		t.Errorf("未分组 synced again: %+v", s)
	}
}

// A machine whose Bot is gone (revoked, or paired again under a new one): what was found there is
// no more listed, nor read back at a restart.
func TestRemoteDshGone(t *testing.T) { executors(t, testRemoteDshGone) }

func testRemoteDshGone(t *testing.T, kind string) {
	r := newRemoteRig(t, kind)
	r.session("--x-work--", "s1", zlines(t, `{"type":"session","cwd":`+quote(r.work)+`}`))
	r.scan()
	// Read back as Iridium starts, its executor not connected yet.
	later := func() int {
		x := New(Options{Store: r.st, State: r.state, Logf: t.Logf})
		x.loadRemote()
		l, _ := x.Sessions(context.Background())
		return len(l)
	}
	if n := later(); n != 1 {
		t.Fatalf("its Bot there: %d sessions read back", n)
	}
	r.st.ForgetMachine("m1") // revoked: its connection ends with it
	if n := later(); n != 0 {
		t.Errorf("its Bot gone: %d sessions read back", n)
	}
	if _, err := os.Stat(filepath.Join(r.state, "remote-dsh", "m1.json")); !os.IsNotExist(err) {
		t.Errorf("what was found there is kept: %v", err)
	}
}
