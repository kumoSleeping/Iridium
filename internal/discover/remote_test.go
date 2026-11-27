package discover

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/gate"
	"github.com/kumoSleeping/iridium/internal/remote"
	"github.com/kumoSleeping/iridium/internal/remote/executor"
	"github.com/kumoSleeping/iridium/internal/remote/wire"
	"github.com/kumoSleeping/iridium/internal/store"
	"github.com/kumoSleeping/iridium/internal/transcript"
)

// remoteRig is an index with an executor connected whose dsh home is a temp directory. What the
// dsh plugin would tell of is told by hand (told): the Go executor serves the files.
type remoteRig struct {
	t     *testing.T
	x     *Index
	hub   *remote.Hub
	st    *store.Store
	home  string // dsh's there
	work  string // a folder there
	state string
}

func newRemoteRig(t *testing.T) *remoteRig {
	t.Helper()
	there := t.TempDir()
	r := &remoteRig{t: t, hub: &remote.Hub{Logf: t.Logf}, home: filepath.Join(there, ".dsh"), work: filepath.Join(there, "work"), state: t.TempDir()}
	os.MkdirAll(r.work, 0o700)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { r.hub.Serve(w, req, "m1", "pc") }))
	t.Cleanup(srv.Close)
	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	agents := []wire.AgentInfo{{Agent: "dsh", Argv: []string{"dsh"}, Version: "0.2.2-rc.1", Home: r.home}}
	{
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

func (r *remoteRig) index() *Index {
	x := New(Options{Store: r.st, State: r.state, Logf: r.t.Logf, RemoteScan: time.Hour})
	x.SetRemote(r.hub)
	return x
}

// told is the machine's dsh telling of sessions (EventSessions).
func (r *remoteRig) told(ss ...DesktopSession) {
	r.t.Helper()
	r.x.ToldSessions(context.Background(), "m1", DesktopSessions{Sessions: ss})
}

func (r *remoteRig) shown(id string) bool {
	r.t.Helper()
	vis, err := r.st.Visibility()
	if err != nil {
		r.t.Fatal(err)
	}
	return vis.Shown("dsh", id)
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

// dsh's sessions at a machine (docs/exec.md "远端 DSH"): nothing is listed till its dsh tells of
// it; one told of is listed with the folder and title said, a subagent as its header says, and
// shown only if taken in or, with the machine's sync on, active since; its workspaces for a new
// session; a transcript mirrored when read.
func TestRemoteDshSessions(t *testing.T) {
	r := newRemoteRig(t)
	r.session("--x-work--", "s1", zlines(t, `{"type":"session","cwd":`+quote(r.work)+`}`, `{"type":"event","seq":1}`))
	r.session("--x-work--", "s2", zlines(t, `{"type":"session","cwd":`+quote(r.work)+`,"origin":"subagent","parentSession":"s1"}`))
	r.session("--x-old--", "s3", zlines(t, `{"type":"session","cwd":`+quote(r.work)+`}`))
	os.MkdirAll(filepath.Join(r.home, "sessions", "--x-work--", "s4"), 0o700) // not written yet
	r.registry("s3", r.work, filepath.Join(r.work, "gone"))
	if err := r.x.ScanMachine(context.Background(), "m1"); err != nil || len(r.sessions()) != 0 {
		t.Fatalf("found by looking: %+v %v", r.sessions(), err)
	}

	// Told of with the sync off: listed (the archive), not shown.
	r.told(DesktopSession{ID: "s1", Cwd: r.work, Title: "fix the build", Last: time.Now().UnixMilli()}, DesktopSession{ID: "s4", Cwd: r.work})
	got := r.sessions()
	s1 := got["s1"]
	if len(got) != 1 || s1.Machine != "m1" || s1.Cwd != r.work || s1.Title != "fix the build" || s1.Agent != api.AgentDsh || s1.Offline {
		t.Fatalf("sessions %+v", got)
	}
	if r.shown("s1") {
		t.Error("shown with the sync off")
	}
	// On: what was active before stays out, what is active after comes in, its subagent with it;
	// one archived in DSH Desktop does not.
	r.x.SetSync("m1", true)
	r.x.remotes["m1"].SyncAt += 10_000 // past the files' times (write moves them on)
	if !r.x.Sync("m1") || r.x.Sync("") {
		t.Errorf("sync: m1 %v, here %v", r.x.Sync("m1"), r.x.Sync(""))
	}
	r.told(DesktopSession{ID: "s1", Last: time.Now().Add(-time.Hour).UnixMilli()})
	if r.shown("s1") {
		t.Error("shown for what it did before the sync was on")
	}
	later := time.Now().Add(time.Minute).UnixMilli()
	r.told(DesktopSession{ID: "s1", Last: later}, DesktopSession{ID: "s2", Last: later}, DesktopSession{ID: "s3", Last: later, Archived: true})
	got = r.sessions()
	if s2 := got["s2"]; len(got) != 3 || !s2.Subagent || s2.Parent != "s1" || s2.Cwd != r.work {
		t.Errorf("sessions %+v", got)
	}
	if !r.shown("s1") || !r.shown("s2") || r.shown("s3") {
		t.Errorf("shown: s1 %v s2 %v s3 %v", r.shown("s1"), r.shown("s2"), r.shown("s3"))
	}
	ws, err := r.x.DshWorkspaces(context.Background(), "pc")
	if err != nil || len(ws) != 1 || ws[0].Path != r.work || ws[0].Title != "work" {
		t.Fatalf("workspaces %+v %v", ws, err)
	}

	// Read: mirrored here, then kept up as it grows there, when told of or read again.
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
	r.x.remotes["m1"].Sessions["s1"].looked = time.Time{}
	r.told(DesktopSession{ID: "s1", Title: "fix the build again"})
	if m, _ := f.Size(); m <= n || r.sessions()["s1"].Title != "fix the build again" {
		t.Errorf("after growth: %d (was %d), %+v", m, n, r.sessions()["s1"])
	}

	// Archived in DSH Desktop since: archived here too, as its workspaces say or as it is told of.
	r.x.ToldWorkspaces("m1", DesktopWorkspaces{Archived: []string{"s1"}})
	if r.shown("s1") {
		t.Error("s1 archived there, shown here")
	}
	r.told(DesktopSession{ID: "s1", Last: later + 1})
	if r.shown("s1") {
		t.Error("s1 archived there, shown again by its activity")
	}

	// A restart lists them before the machine says anything; deleted there, gone here.
	r.x = r.index()
	r.x.loadRemote()
	if len(r.sessions()) != 3 || !r.x.Sync("m1") {
		t.Fatalf("after a restart %+v, sync %v", r.sessions(), r.x.Sync("m1"))
	}
	r.x.ToldSessions(context.Background(), "m1", DesktopSessions{Gone: []string{"s3"}})
	if _, ok := r.sessions()["s3"]; ok || len(r.sessions()) != 2 {
		t.Errorf("s3 gone there: %+v", r.sessions())
	}
}

// A session picked in a search is taken in whatever the switch says, found in the directory dsh
// keeps its folder's sessions in; one that is not there is an error.
func TestRemoteDshAdopt(t *testing.T) {
	r := newRemoteRig(t)
	r.session(transcript.DshProjectDir(r.work), "s1", zlines(t, `{"type":"session","cwd":`+quote(r.work)+`}`))
	if err := r.x.Adopt(context.Background(), "pc", DesktopSession{ID: "s1", Cwd: r.work, Title: "old work", Archived: true}); err != nil {
		t.Fatal(err)
	}
	if s := r.sessions()["s1"]; s.Title != "old work" || !r.shown("s1") {
		t.Errorf("taken in: %+v, shown %v", s, r.shown("s1"))
	}
	if d := r.x.remotes["m1"].Sessions["s1"]; d.Project != transcript.DshProjectDir(r.work) {
		t.Errorf("its directory: %q", d.Project)
	}
	if err := r.x.Adopt(context.Background(), "m1", DesktopSession{ID: "none", Cwd: r.work}); err == nil || r.shown("none") {
		t.Errorf("one not there: %v", err)
	}
	// Archived here by the user: its activity there brings it back only with the sync on.
	if err := r.st.Hide("dsh", "s1", 0, 0); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Minute).UnixMilli()
	r.told(DesktopSession{ID: "s1", Last: later})
	if r.shown("s1") {
		t.Error("back with the sync off")
	}
	r.x.SetSync("m1", true)
	time.Sleep(5 * time.Millisecond) // after it was archived here
	r.told(DesktopSession{ID: "s1", Last: later + 1})
	if !r.shown("s1") {
		t.Error("not back with the sync on")
	}
}

// Without a registry dsh knows, the folders of its sessions stand in for its workspaces.
func TestRemoteDshWorkspacesFallback(t *testing.T) {
	r := newRemoteRig(t)
	other := filepath.Join(filepath.Dir(r.work), "other")
	r.session("--x-work--", "s1", zlines(t, `{"type":"session","cwd":`+quote(r.work)+`}`))
	r.session("--x-other--", "s2", zlines(t, `{"type":"session","cwd":`+quote(other)+`}`))
	r.session("--x-other--", "s3", zlines(t, `{"type":"session","cwd":`+quote(other)+`}`))
	r.session("--x-other--", "s3a", zlines(t, `{"type":"session","cwd":`+quote(other)+`,"origin":"subagent","parentSession":"s3"}`))
	r.write(filepath.Join(r.home, "storages", "workspace.json"), `{"unit":{"name":"workspace","version":9}}`)
	for _, s := range []DesktopSession{{ID: "s1", Cwd: r.work}, {ID: "s2", Cwd: other}, {ID: "s3", Cwd: other}, {ID: "s3a", Cwd: other}} {
		if err := r.x.Adopt(context.Background(), "m1", s); err != nil {
			t.Fatal(err)
		}
	}
	// Each with how many of its sessions are listed: subagents aside, archived ones aside.
	count := func() map[string]int {
		ws, err := r.x.DshWorkspaces(context.Background(), "m1")
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]int{}
		for _, w := range ws {
			out[w.Path] = w.Sessions
		}
		return out
	}
	if n := count(); len(n) != 2 || n[other] != 2 || n[r.work] != 1 {
		t.Errorf("sessions: %v", n)
	}
	if err := r.st.Hide(string(api.AgentDsh), "s2", 0, 0); err != nil {
		t.Fatal(err)
	}
	if n := count(); n[other] != 1 {
		t.Errorf("one archived: %v", n)
	}
}

// A session open in DSH Desktop is shown run elsewhere while its agent works there, held while it
// is only loaded, as the machine's dsh says (the event, taken off the hub); none once it is away.
func TestRemoteDshOpen(t *testing.T) {
	r := newRemoteRig(t)
	r.session("--x-work--", "s1", zlines(t, `{"type":"session","cwd":`+quote(r.work)+`}`))
	r.x.SetRemote(r.hub)             // the events' handlers
	say := func(p DesktopSessions) { // as the hub's handler does, then waited for
		t.Helper()
		done := make(chan struct{})
		r.x.enqueue("m1", func() { r.x.ToldSessions(context.Background(), "m1", p) })
		r.x.enqueue("m1", func() { close(done) })
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("not taken")
		}
	}
	say(DesktopSessions{Sessions: []DesktopSession{{ID: "s1", Cwd: r.work, Open: true, Running: true}}})
	// Shown as run elsewhere; not barred: dsh's writer lock there says whether it may be resumed.
	if s := r.sessions()["s1"]; s.Status != api.StatusElsewhere || !s.Live || r.x.Free("s1") != nil {
		t.Fatalf("at work in DSH Desktop: %+v", s)
	}
	// Still loaded there, its agent idle: not shown as elsewhere (the user, 2026-11-09), but held.
	say(DesktopSessions{Sessions: []DesktopSession{{ID: "s1", Open: true}}})
	if s := r.sessions()["s1"]; s.Status != api.StatusIdle || s.Live || !r.x.HeldElsewhere("s1") {
		t.Errorf("loaded, idle there: %+v, held %v", s, r.x.HeldElsewhere("s1"))
	}
	// All that is open named, s1 not among it: closed.
	say(DesktopSessions{Full: true})
	if s := r.sessions()["s1"]; s.Status != api.StatusIdle || s.Live || r.x.HeldElsewhere("s1") {
		t.Errorf("closed there: %+v", s)
	}
}

// The sessions in none of DSH Desktop's workspaces there are its loose ones, as it tells them.
func TestRemoteDshLoose(t *testing.T) {
	r := newRemoteRig(t)
	r.session("--x-work--", "s1", zlines(t, `{"type":"session","cwd":`+quote(r.work)+`}`))
	r.session("--x-work--", "s2", zlines(t, `{"type":"session","cwd":`+quote(r.work)+`}`))
	r.told(DesktopSession{ID: "s1", Cwd: r.work}, DesktopSession{ID: "s2", Cwd: r.work})
	if s := r.sessions(); s["s1"].Loose || s["s2"].Loose {
		t.Errorf("its workspaces not known: %+v", s)
	}
	var p DesktopWorkspaces
	json.Unmarshal([]byte(`{"workspaces":[{"id":"w0","title":"Work","path":`+quote(r.work)+`,"sessions":["s1"]}]}`), &p)
	r.x.ToldWorkspaces("m1", p)
	if s := r.sessions(); s["s1"].Loose || !s["s2"].Loose {
		t.Errorf("s2 in none: %+v", s)
	}
	ws, err := r.x.DshWorkspaces(context.Background(), "m1")
	if err != nil || len(ws) != 1 || ws[0].Title != "Work" {
		t.Errorf("workspaces as told: %+v %v", ws, err)
	}
}

// A machine whose Bot is gone (revoked, or paired again under a new one): what was found there is
// no more listed, nor read back at a restart.
func TestRemoteDshGone(t *testing.T) {
	r := newRemoteRig(t)
	r.session("--x-work--", "s1", zlines(t, `{"type":"session","cwd":`+quote(r.work)+`}`))
	r.told(DesktopSession{ID: "s1", Cwd: r.work})
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
	// Its row of machines gone while its app is still there: kept (what it found is not dropped
	// for a missing row alone).
	if err := r.st.Gate().Apply(gate.Change{Endpoints: []gate.Endpoint{{ID: "m1", Name: "pc", Kind: gate.KindChannel, TokenHash: "h"}}}); err != nil {
		t.Fatal(err)
	}
	r.st.ForgetMachine("m1")
	if n := later(); n != 1 {
		t.Fatalf("its app there, its machine's row not: %d sessions read back", n)
	}
	// Its app revoked: its connection ends with it.
	if err := r.st.Gate().Apply(gate.Change{DeleteEndpoints: []string{"m1"}}); err != nil {
		t.Fatal(err)
	}
	if n := later(); n != 0 {
		t.Errorf("its Bot gone: %d sessions read back", n)
	}
	if _, err := os.Stat(filepath.Join(r.state, "remote-dsh", "m1.json")); !os.IsNotExist(err) {
		t.Errorf("what was found there is kept: %v", err)
	}
}
