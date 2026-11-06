package discover

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/hub"
	"github.com/kumoSleeping/iridium/internal/store"
)

const (
	idA     = "11111111-1111-4111-8111-111111111111" // Claude, /w/a
	idB     = "22222222-2222-4222-8222-222222222222" // Claude, /w/a/b
	idC     = "33333333-3333-4333-8333-333333333333" // Claude, /w/c
	idCodex = "00000000-0000-7000-8000-8d5c554c3185" // /tmp/codex-probe/work/project
	idSub   = "00000000-0000-7000-8000-00000000aaaa" // its subagent's thread
	idTask  = "agent-a0be53fc45406abf7"              // an Agent tool subtask of idA, in idA/subagents/
	idOld   = "agent-ab6082737f86b4771"              // an older one, beside idA
	idStep  = "agent-a5f00000000000001"              // a Workflow run's agent, in idA/subagents/workflows/<run>/
	idPi    = "00000000-0000-4000-8000-4c4a2a950dc8"
	idDsh   = "session-0123abcd"
	me      = 1000
)

// fakeProcs is /proc as a test says.
type fakeProcs struct {
	mu sync.Mutex
	p  map[int]*fakeProc
}

type fakeProc struct {
	Stat
	argv  []string
	exe   string
	cwd   string
	env   []string
	files []string
}

func (f *fakeProcs) set(pid int, p *fakeProc) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.p == nil {
		f.p = map[int]*fakeProc{}
	}
	if p == nil {
		delete(f.p, pid)
		return
	}
	if p.UID == 0 {
		p.UID = me
	}
	if p.Start == 0 {
		p.Start = uint64(1000 + pid)
	}
	f.p[pid] = p
}

func (f *fakeProcs) get(pid int) *fakeProc {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.p[pid]
}

func (f *fakeProcs) PIDs() ([]int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []int
	for pid := range f.p {
		out = append(out, pid)
	}
	return out, nil
}

func (f *fakeProcs) Stat(pid int) (Stat, error) {
	if p := f.get(pid); p != nil {
		return p.Stat, nil
	}
	return Stat{}, fs.ErrNotExist
}

func (f *fakeProcs) Cmdline(pid int) []string {
	if p := f.get(pid); p != nil {
		return p.argv
	}
	return nil
}

func (f *fakeProcs) Exe(pid int) string {
	if p := f.get(pid); p != nil {
		return p.exe
	}
	return ""
}

func (f *fakeProcs) Cwd(pid int) string {
	if p := f.get(pid); p != nil {
		return p.cwd
	}
	return ""
}

func (f *fakeProcs) Environ(pid int) []string {
	p := f.get(pid)
	if p == nil {
		return nil
	}
	if p.UID != me {
		panic("read the environment of another user's process")
	}
	return p.env
}

func (f *fakeProcs) Files(pid int) []string {
	if p := f.get(pid); p != nil {
		return p.files
	}
	return nil
}

type rig struct {
	t     *testing.T
	home  string
	x     *Index
	store *store.Store
	procs *fakeProcs
	hub   *hub.Hub

	mu      sync.Mutex
	changed []string
}

// wait waits up to five seconds for ok.
func (r *rig) wait(what string, ok func() bool) {
	r.t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if ok() {
			return
		}
	}
	r.t.Fatalf("timed out: %s", what)
}

// newRig copies testdata/home into a temporary home and makes an index of it.
func newRig(t *testing.T) *rig {
	t.Helper()
	home := filepath.Join(t.TempDir(), "home")
	if err := os.CopyFS(home, os.DirFS("testdata/home")); err != nil {
		t.Fatal(err)
	}
	// There before Iridium watched: the archive, until they show activity.
	past := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	filepath.WalkDir(home, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			os.Chtimes(p, past, past)
		}
		return nil
	})
	os.MkdirAll(filepath.Join(home, ".claude", "sessions"), 0o700)
	st, err := store.Open(filepath.Join(t.TempDir(), "iridium.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	r := &rig{t: t, home: home, store: st, procs: &fakeProcs{}, hub: hub.New()}
	r.x = New(Options{
		ClaudeDir:  filepath.Join(home, ".claude"),
		CodexHome:  filepath.Join(home, ".codex"),
		PiSessions: filepath.Join(home, ".pi", "agent", "sessions"),
		DshHome:    filepath.Join(home, ".dsh"),
		Store:      st, Hub: r.hub, Procs: r.procs, UID: me, Logf: t.Logf,
		Settle: 10 * time.Millisecond, ProcScan: time.Hour, Rescan: time.Hour,
		OnChange: func(ids []string) {
			r.mu.Lock()
			r.changed = append(r.changed, ids...)
			r.mu.Unlock()
		},
	})
	return r
}

func (r *rig) sessions() map[string]api.Session {
	r.t.Helper()
	l, err := r.x.Sessions(nil)
	if err != nil {
		r.t.Fatal(err)
	}
	out := map[string]api.Session{}
	for _, s := range l {
		out[s.ID] = s
	}
	return out
}

// register writes Claude's registry file of a live claude process; state: its status and
// waitingFor, idle if none.
func (r *rig) register(pid int, session, cwd string, state ...string) {
	r.t.Helper()
	m := map[string]any{"pid": pid, "sessionId": session, "cwd": cwd, "procStart": strconv.Itoa(1000 + pid), "kind": "interactive", "status": "idle", "name": "made-up", "nameSource": "auto"}
	if len(state) > 0 {
		m["status"] = state[0]
	}
	if len(state) > 1 {
		m["waitingFor"] = state[1]
	}
	b, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(r.home, ".claude", "sessions", strconv.Itoa(pid)+".json"), b, 0o600); err != nil {
		r.t.Fatal(err)
	}
}

// shown says whether session id of agent carries the shown marker.
func (r *rig) shown(agent api.Agent, id string) bool {
	r.t.Helper()
	v, err := r.store.Visibility()
	if err != nil {
		r.t.Fatal(err)
	}
	return v.Shown(string(agent), id)
}

// grow appends a line to session id's transcript, as its agent goes on.
func (r *rig) grow(id string) {
	r.t.Helper()
	_, f, ok := r.x.Found(id)
	if !ok || f == nil {
		r.t.Fatalf("no transcript of %s", id)
	}
	w, err := os.OpenFile(f.Path(), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		r.t.Fatal(err)
	}
	w.WriteString(`{"type":"user","message":{"role":"user","content":"more"}}` + "\n")
	w.Close()
}

func (r *rig) assignment(id string) (store.Assignment, bool) {
	r.t.Helper()
	a, err := r.store.Assignments()
	if err != nil {
		r.t.Fatal(err)
	}
	v, ok := a[id]
	return v, ok
}

func TestListsEveryAgent(t *testing.T) {
	r := newRig(t)
	r.x.Scan()
	got := r.sessions()
	var ids []string
	for id := range got {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	want := []string{idCodex, idSub, idPi, idA, idB, idC, idDsh, idTask, idOld, idStep}
	slices.Sort(want)
	if !slices.Equal(ids, want) {
		t.Fatalf("sessions %v, want %v (subagents too)", ids, want)
	}
	// Subagents are sessions of their own, read-only, started by their parent.
	if s := got[idTask]; !s.Subagent || s.Parent != idA || s.Launcher != idA || s.Agent != api.AgentClaude || s.Cwd != "/w/a" || s.Title != "Look up the weekday" {
		t.Errorf("claude subagent: %+v", s)
	}
	if s := got[idStep]; !s.Subagent || s.Parent != idA || s.Launcher != idA || s.Cwd != "/w/a" || s.Title != "review:caret" {
		t.Errorf("workflow agent: %+v", s)
	}
	// Its run, by its folder; the workflow's name, by its script's; its phase, by its meta.
	if w := got[idStep].Workflow; w == nil || *w != (api.SessionWorkflow{Run: "wf_0123abcd-ef0", Name: "fix-caret", Phase: "Review"}) {
		t.Errorf("workflow agent's run: %+v", w)
	}
	if w := got[idOld].Workflow; w != nil {
		t.Errorf("a plain subagent in a run: %+v", w)
	}
	if s := got[idOld]; !s.Subagent || s.Parent != idA || s.Title != "an old subtask" {
		t.Errorf("older claude subagent: %+v", s)
	}
	if s := got[idSub]; !s.Subagent || s.Parent != idCodex || s.Launcher != idCodex || s.Agent != api.AgentCodex || s.Title != "/root/weekday" {
		t.Errorf("codex subagent: %+v", s)
	}
	if s := got[idA]; s.Subagent || s.Parent != "" {
		t.Errorf("a session is no subagent: %+v", s)
	}
	a := got[idA]
	if a.Agent != api.AgentClaude || a.Cwd != "/w/a" || a.Title != "Fix the build in a" || a.Status != api.StatusIdle || a.Live || a.Hosted {
		t.Errorf("claude: %+v", a)
	}
	if a.Last == nil || a.Last.Role != "assistant" || a.Last.Text != "Done Fixed it in main.go, see the log." {
		t.Errorf("claude last: %+v", a.Last)
	}
	if b := got[idB]; b.Title != "Write the docs" || b.Last == nil || b.Last.Role != "user" || b.Last.Text != "and the tests" {
		t.Errorf("claude b: %+v %+v", b, b.Last)
	}
	c := got[idCodex]
	if c.Agent != api.AgentCodex || c.Cwd != "/tmp/codex-probe/work/project" || c.Title != "Probe thread" || c.Last == nil {
		t.Errorf("codex: %+v %+v", c, c.Last)
	}
	p := got[idPi]
	if p.Agent != api.AgentPi || p.Cwd != "/tmp/scratchpad/pi-probe-9/project" || p.Title == "" || p.Last == nil {
		t.Errorf("pi: %+v %+v", p, p.Last)
	}
	d := got[idDsh]
	if d.Agent != api.AgentDsh || d.UpdatedAt == 0 {
		t.Errorf("dsh: %+v", d)
	}
	if _, f, ok := r.x.Found(idA); !ok || f == nil || filepath.Base(f.Path()) != idA+".jsonl" {
		t.Errorf("found: %v %v", ok, f)
	}
	if _, f, ok := r.x.Found(idDsh); !ok || f != nil {
		t.Errorf("dsh is listed, not read: %v %v", ok, f)
	}
}

func TestRunningAndLaunchers(t *testing.T) {
	r := newRig(t)
	codexRollout := filepath.Join(r.home, ".codex", "sessions", "2026", "01", "01", "rollout-2026-01-01T00-00-00-"+idCodex+".jsonl")
	// Claude A in a terminal; under its Bash tool a shell, a codex wrapper and the codex it runs.
	r.register(100, idA, "/w/a")
	r.procs.set(100, &fakeProc{Stat: Stat{PPid: 1}, argv: []string{"claude"}, exe: "/home/u/.local/share/claude/versions/2.1.318"})
	r.procs.set(101, &fakeProc{Stat: Stat{PPid: 100}, argv: []string{"/usr/bin/zsh", "-c", "codex"}, exe: "/usr/bin/zsh"})
	r.procs.set(102, &fakeProc{Stat: Stat{PPid: 101}, argv: []string{"node", "/usr/local/bin/codex"}, exe: "/usr/bin/node"})
	r.procs.set(103, &fakeProc{Stat: Stat{PPid: 102}, argv: []string{"/opt/codex/bin/codex"}, exe: "/opt/codex/bin/codex", files: []string{codexRollout, "/dev/null"}})
	// pi, by its title alone, started from Claude B's tool: by the variable.
	r.procs.set(200, &fakeProc{Stat: Stat{PPid: 1, Started: time.Unix(0, 0)}, argv: []string{"pi "}, exe: "/usr/bin/node", cwd: "/tmp/scratchpad/pi-probe-9/project",
		env: []string{"PATH=/bin", "CLAUDE_CODE_SESSION_ID=" + idB, "AI_AGENT=claude"}})
	// Claude C hosted by Iridium: its own IRIDIUM_SESSION, so not elsewhere.
	r.register(300, idC, "/w/c")
	r.procs.set(300, &fakeProc{Stat: Stat{PPid: 1}, argv: []string{"claude", "-p"}, env: []string{"IRIDIUM_SESSION=" + idC}})
	// dsh, by its lock, started inside hosted C's agent and from a Claude B shell: Iridium's first.
	lock := filepath.Join(r.home, ".dsh", "sessions", "--w-d--", idDsh, "session.lock")
	r.procs.set(400, &fakeProc{Stat: Stat{PPid: 300}, argv: []string{"dsh", "--profile", "sdk"}, files: []string{lock},
		env: []string{"CLAUDE_CODE_SESSION_ID=" + idB, "IRIDIUM_SESSION=" + idC}})
	// Another user's claude of B: never read.
	r.procs.set(500, &fakeProc{Stat: Stat{PPid: 1, UID: 1001}, argv: []string{"claude", "--resume", idB}})
	r.x.Scan()

	got := r.sessions()
	if a := got[idA]; a.Status != api.StatusElsewhere || !a.Live || a.Launcher != "" || a.Title != "Fix the build in a" {
		t.Errorf("A: %+v", a)
	}
	if c := got[idCodex]; c.Status != api.StatusElsewhere || c.Launcher != idA {
		t.Errorf("codex: launcher %q status %s, want %s by the process tree", c.Launcher, c.Status, idA)
	}
	if p := got[idPi]; p.Status != api.StatusElsewhere || p.Launcher != idB {
		t.Errorf("pi: launcher %q status %s", p.Launcher, p.Status)
	}
	if c := got[idC]; c.Status != api.StatusIdle || c.Live || c.Launcher != "" {
		t.Errorf("hosted C: %+v", c)
	}
	if d := got[idDsh]; d.Launcher != idC || d.Status != api.StatusElsewhere {
		t.Errorf("dsh: launcher %q status %s", d.Launcher, d.Status)
	}
	if b := got[idB]; b.Live {
		t.Errorf("B runs only as another user's: %+v", b)
	}

	// A registry file whose process is another one now (the pid used again) is stale.
	r.procs.set(100, &fakeProc{Stat: Stat{PPid: 1, Start: 99}, argv: []string{"vim"}})
	r.x.Scan()
	if a := r.sessions()[idA]; a.Live {
		t.Errorf("A after its process went: %+v", a)
	}
	// The launcher is remembered after the process exits.
	r.procs.set(103, nil)
	r.x.Scan()
	if c := r.sessions()[idCodex]; c.Live || c.Launcher != idA {
		t.Errorf("codex after exit: %+v", c)
	}
}

func TestClassification(t *testing.T) {
	r := newRig(t)
	// What was there before Iridium watched is the archive: not shown, and not classified.
	r.x.Scan()
	for _, id := range []string{idA, idB, idC, idCodex, idPi, idDsh} {
		if a, ok := r.assignment(id); ok {
			t.Fatalf("%s classified while hidden: %+v", id, a)
		}
	}
	// A new workspace takes no session by itself.
	wsA, _ := r.x.CreateWorkspace("/w/a", "")
	wsB, _ := r.x.CreateWorkspace("/w/a/b", "B")
	wsC, _ := r.x.CreateWorkspace("/w/c", "")
	if _, ok := r.assignment(idA); ok {
		t.Fatal("a new workspace took a session")
	}
	if wsA.Name != "a" || wsB.Name != "B" {
		t.Errorf("names: %q %q", wsA.Name, wsB.Name)
	}
	// Activity shows a session and classifies it once: A in /w/a, the folder of a workspace.
	r.grow(idA)
	r.grow(idC)
	r.x.Scan()
	if a, _ := r.assignment(idA); a.Workspace != wsA.ID || a.Manual {
		t.Fatalf("A in /w/a: %+v, want the workspace of that folder %d", a, wsA.ID)
	}
	if c, _ := r.assignment(idC); c.Workspace != wsC.ID {
		t.Fatalf("C in /w/c: %+v", c)
	}
	if _, ok := r.assignment(idB); ok {
		t.Fatal("B, still hidden, classified")
	}

	// New sessions: one in /w/a/b/c (the nearest folder above is B's workspace's), one in /w
	// (none above), one started by C's session wherever it is.
	newClaude := func(id, cwd string, at time.Time) {
		dir := filepath.Join(r.home, ".claude", "projects", filepath.Base(cwd)+"-x")
		os.MkdirAll(dir, 0o700)
		line, _ := json.Marshal(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": "hi"}, "cwd": cwd, "uuid": id, "timestamp": at.UTC().Format(time.RFC3339Nano)})
		p := filepath.Join(dir, id+".jsonl")
		os.WriteFile(p, append(line, '\n'), 0o600)
		os.Chtimes(p, at, at)
	}
	idD := "44444444-4444-4444-8444-444444444444"
	idE := "55555555-5555-4555-8555-555555555555"
	idF := "66666666-6666-4666-8666-666666666666"
	now := time.Now().Add(time.Second)
	newClaude(idD, "/w/a/b/c", now)
	newClaude(idE, "/w", now)
	newClaude(idF, "/somewhere/else", now)
	r.register(600, idF, "/somewhere/else")
	r.procs.set(600, &fakeProc{Stat: Stat{PPid: 1}, argv: []string{"claude"}, env: []string{"CLAUDE_CODE_SESSION_ID=" + idC}})
	r.x.Scan()
	if d, _ := r.assignment(idD); d.Workspace != wsB.ID || d.Manual {
		t.Errorf("D in /w/a/b/c: %+v, want the nearest folder's workspace %d", d, wsB.ID)
	}
	if e, ok := r.assignment(idE); !ok || e.Workspace != 0 {
		t.Errorf("E in /w: %+v %v, want none", e, ok)
	}
	if f, _ := r.assignment(idF); f.Workspace != wsC.ID {
		t.Errorf("F started by C: %+v, want C's workspace %d", f, wsC.ID)
	}

	// Only once: with A moved to C's workspace, a later look leaves D where it went.
	r.x.Patch(idA, api.SessionPatch{Workspace: &wsC.ID})
	r.grow(idD)
	r.x.Scan()
	if d, _ := r.assignment(idD); d.Workspace != wsB.ID {
		t.Errorf("D moved: %+v", d)
	}
	if ws, _ := r.x.Workspaces(); len(ws) != 3 {
		t.Errorf("workspaces: %+v", ws)
	}
	r.x.publishWorkspaces()
	var carried []api.Workspace
	json.Unmarshal(r.hub.Get("workspaces"), &carried)
	if len(carried) != 3 || carried[1].Name != "B" || carried[1].Path != "/w/a/b" {
		t.Errorf("carried workspaces: %s", r.hub.Get("workspaces"))
	}
}

// The shown marker: activity after Iridium began watching sets it; hiding notes the size, and
// only growth past it shows the session again; a restart keeps when watching began.
func TestShownMarker(t *testing.T) {
	r := newRig(t)
	r.x.Scan()
	for _, id := range []string{idA, idB, idC} {
		if r.shown(api.AgentClaude, id) {
			t.Fatalf("%s shown with no activity", id)
		}
	}
	if r.shown(api.AgentCodex, idCodex) || r.shown(api.AgentPi, idPi) {
		t.Fatal("codex or pi shown with no activity")
	}
	r.grow(idA)
	r.x.Scan()
	if !r.shown(api.AgentClaude, idA) || r.shown(api.AgentClaude, idB) {
		t.Fatal("A grew: shown; B did not")
	}
	// Archived: hidden at its size; looking again does not bring it back, growing does.
	size, _ := r.x.Size(idA)
	if err := r.store.Hide(string(api.AgentClaude), idA, size, 0); err != nil {
		t.Fatal(err)
	}
	r.x.Scan()
	if r.shown(api.AgentClaude, idA) {
		t.Fatal("A came back with no growth")
	}
	// Growth while it is quiet (its agent ending) does not count.
	r.store.Hide(string(api.AgentClaude), idA, size, time.Now().Add(time.Hour).UnixMilli())
	r.grow(idA)
	r.x.Scan()
	if r.shown(api.AgentClaude, idA) {
		t.Fatal("A came back while quiet")
	}
	size, _ = r.x.Size(idA)
	r.store.Hide(string(api.AgentClaude), idA, size, 0)
	r.grow(idA)
	r.x.Scan()
	if !r.shown(api.AgentClaude, idA) {
		t.Fatal("A grew after it was archived: shown again")
	}

	// A restart: a new index over the same store. What is there stays as it was; a transcript
	// written meanwhile, never seen, is activity.
	_, fb, _ := r.x.Found(idB)
	later := time.Now().Add(time.Second)
	os.Chtimes(fb.Path(), later, later)
	x := New(Options{
		ClaudeDir: filepath.Join(r.home, ".claude"), CodexHome: filepath.Join(r.home, ".codex"),
		PiSessions: filepath.Join(r.home, ".pi", "agent", "sessions"), DshHome: filepath.Join(r.home, ".dsh"),
		Store: r.store, Procs: r.procs, UID: me, Logf: t.Logf, Settle: 10 * time.Millisecond, ProcScan: time.Hour, Rescan: time.Hour,
	})
	x.Scan()
	if !r.shown(api.AgentClaude, idB) {
		t.Error("B written while no daemon looked: shown")
	}
	if r.shown(api.AgentClaude, idC) {
		t.Error("C untouched: still hidden after a restart")
	}
}

func TestMoveSticks(t *testing.T) {
	r := newRig(t)
	r.x.Scan()
	r.grow(idA)
	r.grow(idB)
	r.x.Scan()
	wsA, _ := r.x.CreateWorkspace("/w/a", "")
	wsC, _ := r.x.CreateWorkspace("/w/c", "")
	// The user moves A to C's workspace, and B out of any.
	if err := r.x.Patch(idA, api.SessionPatch{Workspace: &wsC.ID}); err != nil {
		t.Fatal(err)
	}
	none := int64(0)
	r.x.Patch(idB, api.SessionPatch{Workspace: &none})
	r.x.Scan()
	if a, _ := r.assignment(idA); a.Workspace != wsC.ID || !a.Manual {
		t.Fatalf("A: %+v", a)
	}
	r.x.DeleteWorkspace(wsA.ID)
	// Deleting a workspace leaves its sessions in none, and they are not classified again.
	if err := r.x.DeleteWorkspace(wsC.ID); err != nil {
		t.Fatal(err)
	}
	r.grow(idA)
	r.x.Scan()
	if a, ok := r.assignment(idA); !ok || a.Workspace != 0 {
		t.Errorf("A after its workspace went: %+v %v", a, ok)
	}
	if b, _ := r.assignment(idB); b.Workspace != 0 {
		t.Errorf("B: %+v", b)
	}
	// Moving to a workspace that is not there fails.
	bad := int64(999)
	if err := r.x.Patch(idA, api.SessionPatch{Workspace: &bad}); !errors.Is(err, store.ErrNoWorkspace) {
		t.Errorf("move to none: %v", err)
	}
	if _, err := r.x.CreateWorkspace("/w", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := r.x.CreateWorkspace("/w", ""); !errors.Is(err, store.ErrWorkspacePath) {
		t.Errorf("a second workspace of /w: %v", err)
	}
	// The overlay: name, pin.
	name, yes := "mine", true
	r.x.Patch(idA, api.SessionPatch{Name: &name, Pinned: &yes})
	o, _ := r.store.Overlays()
	if o[idA].Name != "mine" || !o[idA].Pinned {
		t.Errorf("overlay: %+v", o[idA])
	}
}

func TestFree(t *testing.T) {
	r := newRig(t)
	r.x.Scan()
	if err := r.x.Free(idA); err != nil {
		t.Fatalf("free: %v", err)
	}
	r.register(100, idA, "/w/a")
	r.procs.set(100, &fakeProc{Stat: Stat{PPid: 1}, argv: []string{"claude"}})
	if err := r.x.Free(idA); !errors.Is(err, ErrBusy) {
		t.Fatalf("A while it runs: %v", err)
	}
	// It exited: its registry file is gone.
	os.Remove(filepath.Join(r.home, ".claude", "sessions", "100.json"))
	r.procs.set(100, nil)
	if err := r.x.Free(idA); err != nil {
		t.Fatalf("A after exit: %v", err)
	}
	// dsh: its session lock.
	f, _ := os.Open(filepath.Join(r.home, ".dsh", "sessions", "--w-d--", idDsh, "session.lock"))
	defer f.Close()
	if err := r.x.Free(idDsh); err != nil {
		t.Fatalf("dsh: %v", err)
	}
	unix.Flock(int(f.Fd()), unix.LOCK_EX)
	if err := r.x.Free(idDsh); !errors.Is(err, ErrBusy) {
		t.Fatalf("dsh while locked: %v", err)
	}
	unix.Flock(int(f.Fd()), unix.LOCK_UN)
	if err := r.x.Free(idDsh); err != nil {
		t.Fatalf("dsh unlocked: %v", err)
	}
}

func TestWatches(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	if err := r.x.Start(); err != nil {
		t.Fatal(err)
	}
	defer r.x.Close()
	wait := r.wait
	told := func(id string) bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		return slices.Contains(r.changed, id)
	}
	wait("the first list", func() bool { return told(idA) })

	// A new project directory and a session in it.
	id := "77777777-7777-4777-8777-777777777777"
	dir := filepath.Join(r.home, ".claude", "projects", "-w-new")
	os.MkdirAll(dir, 0o700)
	os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte(`{"type":"user","message":{"role":"user","content":"new one"},"cwd":"/w/new"}`+"\n"), 0o600)
	wait("a new session", func() bool { return r.sessions()[id].Title == "new one" && told(id) })

	// It grows.
	f, _ := os.OpenFile(filepath.Join(dir, id+".jsonl"), os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"hello"}]},"cwd":"/w/new"}` + "\n")
	f.Close()
	wait("what it said", func() bool { l := r.sessions()[id].Last; return l != nil && l.Text == "hello" })

	// A new day of Codex's.
	day := filepath.Join(r.home, ".codex", "sessions", "2026", "02", "03")
	os.MkdirAll(day, 0o700)
	cid := "00000000-0000-7000-8000-0000000bbbbb"
	os.WriteFile(filepath.Join(day, "rollout-2026-02-03T00-00-00-"+cid+".jsonl"), []byte(`{"type":"session_meta","payload":{"id":"`+cid+`","cwd":"/w","source":"cli"}}`+"\n"), 0o600)
	wait("a new Codex day", func() bool { return r.sessions()[cid].Cwd == "/w" })

	// Claude's registry: a process starts.
	r.procs.set(700, &fakeProc{Stat: Stat{PPid: 1}, argv: []string{"claude"}})
	r.register(700, id, "/w/new")
	wait("it runs", func() bool { return r.sessions()[id].Status == api.StatusElsewhere })

	// It asks in its terminal, and is answered.
	r.register(700, id, "/w/new", "waiting", "approve Bash")
	wait("it needs you", func() bool {
		s := r.sessions()[id]
		return s.Status == api.StatusElsewhere && s.Needs == "approve Bash"
	})
	r.register(700, id, "/w/new", "waiting")
	wait("a word for it", func() bool { return r.sessions()[id].Needs == "input needed" })
	r.register(700, id, "/w/new", "busy")
	wait("answered", func() bool { s := r.sessions()[id]; return s.Status == api.StatusElsewhere && s.Needs == "" })

	// Deleted.
	os.Remove(filepath.Join(dir, id+".jsonl"))
	wait("it went", func() bool { _, ok := r.sessions()[id]; return !ok })
}

func TestDshFolder(t *testing.T) {
	root := t.TempDir()
	want := filepath.Join(root, "my-proj", "a:b")
	os.MkdirAll(want, 0o700)
	os.MkdirAll(filepath.Join(root, "my", "proj"), 0o700) // a decoy: my/proj/a:b is not there
	x := New(Options{Store: nil})
	if got := x.dshCwd("--" + projectKey(want) + "--"); got != want {
		t.Errorf("dshCwd: %q, want %q", got, want)
	}
	if got := x.dshCwd("--" + projectKey(filepath.Join(root, "gone")) + "--"); got != "" {
		t.Errorf("a folder gone: %q", got)
	}
}

func TestAgentOf(t *testing.T) {
	for _, c := range []struct {
		argv []string
		exe  string
		want string
	}{
		{[]string{"claude", "-p"}, "/x/claude/versions/2.1.318", "claude"},
		{[]string{"claude bg-pty-host --bg-pty-host /tmp/x"}, "", "claude"},
		{[]string{"pi "}, "/usr/bin/node", "pi"},
		{[]string{"node", "--no-warnings", "/usr/local/bin/codex", "resume"}, "/usr/bin/node", "codex"},
		{[]string{"/usr/bin/zsh", "-c", "claude"}, "/usr/bin/zsh", ""},
		{[]string{"dsh", "--profile", "sdk"}, "/x/dsh", "dsh"},
		{[]string{"vim", "pi.go"}, "/usr/bin/vim", ""},
	} {
		if got := agentOf(c.argv, c.exe); got != c.want {
			t.Errorf("agentOf(%q, %q) = %q, want %q", c.argv, c.exe, got, c.want)
		}
	}
}
