// Package discover finds the sessions on disk (plan "会话", "数据"): every agent's transcripts,
// Claude's registry of live processes (~/.claude/sessions/<pid>.json) and the agents running
// now (/proc). The list is the disk's; the index of it is kept in memory, built at start, kept
// up by inotify and a light periodic look. Read-only: it never writes an agent's files.
//
// A session is in the list only while it carries a shown marker (shown.go): activity sets it.
// Each session shown for the first time is put in a workspace, once (the store remembers): the
// workspace of the session that started it; else the one that holds a session of the same
// folder, or of the nearest folder above, or is that folder; else none (未归类). One the user
// moved stays.
package discover

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/kumoSleeping/iridium/internal/agents"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/config"
	"github.com/kumoSleeping/iridium/internal/hub"
	"github.com/kumoSleeping/iridium/internal/store"
	"github.com/kumoSleeping/iridium/internal/transcript"
)

// ErrBusy: another process uses the session, so Iridium must not resume it.
var ErrBusy = errors.New("the session runs elsewhere")

type Options struct {
	ClaudeDir  string // ~/.claude
	CodexHome  string // $CODEX_HOME, else ~/.codex
	PiSessions string // ~/.pi/agent/sessions
	DshHome    string // $DSH_HOME, else ~/.dsh
	Store      *store.Store
	Hub        *hub.Hub // given the carried state "workspaces"; may be nil
	Procs      Procs    // nil: /proc
	UID        int      // whose processes count; -1: this process's
	Logf       func(string, ...any)
	// OnChange is told which sessions' rows may have changed, nil for the list as a whole
	// (workspaces). Called at most once per Settle.
	OnChange func(ids []string)
	Rescan   time.Duration // how often the directories are looked at whole; 0 is a minute
	ProcScan time.Duration // how often /proc is looked at; 0 is 3s
	Settle   time.Duration // changes are told together after this; 0 is 200ms
}

// Defaults are the agents' own places, as the config and the environment say.
func Defaults(cfg config.Config) Options {
	home, _ := os.UserHomeDir()
	o := Options{
		ClaudeDir:  cfg.ClaudeDir(),
		CodexHome:  filepath.Join(home, ".codex"),
		PiSessions: filepath.Join(home, ".pi", "agent", "sessions"),
		DshHome:    filepath.Join(home, ".dsh"),
		UID:        -1,
	}
	if d := os.Getenv("CODEX_HOME"); d != "" {
		o.CodexHome = d
	}
	if d := os.Getenv("PI_CODING_AGENT_DIR"); d != "" {
		o.PiSessions = filepath.Join(d, "sessions")
	}
	if d := os.Getenv("PI_CODING_AGENT_SESSION_DIR"); d != "" {
		o.PiSessions = d
	}
	if d := os.Getenv("DSH_HOME"); d != "" {
		o.DshHome = d
	}
	return o
}

// entry is one transcript in the index.
type entry struct {
	onDisk
	glimpse
	size  int64
	mtime time.Time
}

// running is an agent process of a session.
type running struct {
	pid      int
	start    uint64
	agent    api.Agent
	launcher string // the session that started it, if known
	ours     bool   // Iridium hosts it: IRIDIUM_SESSION is its own id
	name     string // Claude: the name the user gave it
}

type Index struct {
	o      Options
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	w      *fsnotify.Watcher
	kick   chan struct{} // look at /proc now

	scanMu sync.Mutex // one scan at a time

	mu        sync.Mutex
	entries   map[string]*entry  // by session id
	running   map[string]running // by session id
	launchers map[string]string  // every launcher seen, by session id, kept after it exits
	files     map[string]*transcript.File
	changed   map[string]bool
	whole     bool // the list as a whole changed

	namesMu sync.Mutex
	names   map[string]string // Codex's thread names
	namesAt time.Time
	dshKeys map[string]string

	aliasMu sync.Mutex
	alias   func(hosted string) string

	sinceOnce sync.Once
	since     int64 // when Iridium began watching (ms)
}

// SetAlias tells the index the agent's own id of a session Iridium hosts under another (Codex's
// thread: Iridium's id is made before Codex makes the thread), "" for none: a process whose
// IRIDIUM_SESSION is the hosted id runs that agent session as Iridium's own. f must not call the
// index.
func (x *Index) SetAlias(f func(hosted string) string) {
	x.aliasMu.Lock()
	x.alias = f
	x.aliasMu.Unlock()
}

func (x *Index) aliasOf(hosted string) string {
	x.aliasMu.Lock()
	f := x.alias
	x.aliasMu.Unlock()
	if f == nil || hosted == "" {
		return ""
	}
	return f(hosted)
}

func New(o Options) *Index {
	if o.Procs == nil {
		o.Procs = ProcFS{}
	}
	if o.UID < 0 {
		o.UID = os.Getuid()
	}
	if o.Logf == nil {
		o.Logf = log.Printf
	}
	if o.Rescan == 0 {
		o.Rescan = time.Minute
	}
	if o.ProcScan == 0 {
		o.ProcScan = 3 * time.Second
	}
	if o.Settle == 0 {
		o.Settle = 200 * time.Millisecond
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Index{
		o: o, ctx: ctx, cancel: cancel, kick: make(chan struct{}, 1),
		entries: map[string]*entry{}, running: map[string]running{}, launchers: map[string]string{},
		files: map[string]*transcript.File{}, changed: map[string]bool{},
		dshKeys: map[string]string{},
	}
}

// SetOnChange sets Options.OnChange; before Start.
func (x *Index) SetOnChange(f func(ids []string)) { x.o.OnChange = f }

// Start builds the index, then keeps it up until Close.
func (x *Index) Start() error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		x.o.Logf("discover: no inotify, looking every %s: %v", x.o.Rescan, err)
	}
	x.w = w
	x.Scan()
	x.publishWorkspaces()
	x.wg.Add(2)
	go x.loop()
	go x.settle()
	return nil
}

func (x *Index) Close() {
	x.cancel()
	if x.w != nil {
		x.w.Close()
	}
	x.wg.Wait()
}

// Scan looks at every directory and /proc now, and classifies what is new.
func (x *Index) Scan() {
	x.scanMu.Lock()
	defer x.scanMu.Unlock()
	// Watching begins with the first look, whether or not it finds anything: a transcript
	// written after it is activity, however long until a look finds it.
	x.watchingSince()
	x.scanDisk()
	x.scanProcs()
	x.classify()
}

// watch adds dir to inotify's, quietly: a directory gone or past the limit is still looked at
// by the periodic scan.
func (x *Index) watch(dir string) {
	if x.w != nil {
		_ = x.w.Add(dir)
	}
}

func (x *Index) loop() {
	defer x.wg.Done()
	rescan := time.NewTicker(x.o.Rescan)
	defer rescan.Stop()
	procs := time.NewTicker(x.o.ProcScan)
	defer procs.Stop()
	var events <-chan fsnotify.Event
	var errs <-chan error
	if x.w != nil {
		events, errs = x.w.Events, x.w.Errors
	}
	// Changed files and directories, looked at together a moment later: a turn writes many lines.
	dirty := map[string]bool{}
	var soon <-chan time.Time
	for {
		select {
		case <-x.ctx.Done():
			return
		case <-rescan.C:
			x.Scan()
		case <-procs.C:
			x.procsOnly()
		case <-x.kick:
			x.procsOnly()
		case err := <-errs:
			x.o.Logf("discover: inotify: %v", err)
		case ev := <-events:
			dirty[ev.Name] = true
			if soon == nil {
				soon = time.After(100 * time.Millisecond)
			}
		case <-soon:
			soon = nil
			paths := dirty
			dirty = map[string]bool{}
			x.touched(paths)
		}
	}
}

func (x *Index) procsOnly() {
	x.scanMu.Lock()
	defer x.scanMu.Unlock()
	x.scanProcs()
	x.classify()
}

// touched looks again at what inotify said changed: Claude's registry means /proc; a transcript,
// that file; a new directory, its files.
func (x *Index) touched(paths map[string]bool) {
	x.scanMu.Lock()
	defer x.scanMu.Unlock()
	procs := false
	dirs := map[string]bool{}
	for p := range paths {
		if filepath.Dir(p) == filepath.Join(x.o.ClaudeDir, "sessions") {
			procs = true
			continue
		}
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			dirs[p] = true
		} else {
			dirs[filepath.Dir(p)] = true
		}
	}
	fresh := false
	for d := range dirs {
		fresh = x.scanDir(d) || fresh
	}
	// A new session's launcher is known from its process: look at /proc before classifying it.
	if procs || fresh {
		x.scanProcs()
	}
	x.classify()
}

// ---- the directories ----

type source struct {
	dirs  func() []string
	files func(dir string) []onDisk
	roots func() []string // the directories above dirs, watched for new ones
}

func (x *Index) sources() []source {
	return []source{
		{x.claudeDirs, x.claudeIn, func() []string { return []string{filepath.Join(x.o.ClaudeDir, "projects")} }},
		{x.codexDirs, x.codexIn, func() []string {
			root := filepath.Join(x.o.CodexHome, "sessions")
			out := []string{root}
			for _, y := range subdirs(root) {
				out = append(out, y)
				out = append(out, subdirs(y)...)
			}
			return out
		}},
		{x.piDirs, x.piIn, func() []string { return []string{x.o.PiSessions} }},
		{x.dshDirs, x.dshIn, func() []string { return []string{filepath.Join(x.o.DshHome, "sessions")} }},
	}
}

// scanDisk looks at every directory: transcripts new or changed are read again, gone ones go.
func (x *Index) scanDisk() {
	x.watch(filepath.Join(x.o.ClaudeDir, "sessions"))
	seen := map[string]bool{}
	for _, s := range x.sources() {
		for _, r := range s.roots() {
			x.watch(r)
		}
		for _, d := range s.dirs() {
			x.watch(d)
			for _, f := range s.files(d) {
				seen[f.id] = true
				x.look(f)
			}
			// dsh keeps each session in a directory of its own.
			if strings.HasPrefix(d, filepath.Join(x.o.DshHome, "sessions")+"/") {
				for _, sd := range subdirs(d) {
					x.watch(sd)
				}
			}
		}
	}
	x.mu.Lock()
	for id := range x.entries {
		if !seen[id] {
			delete(x.entries, id)
			delete(x.files, id)
			x.changed[id] = true
		}
	}
	x.mu.Unlock()
}

// scanDir looks at one directory inotify named: one of the agents' session directories, or one
// above them (a new day of Codex's, a new project). True if a session is new.
func (x *Index) scanDir(dir string) (fresh bool) {
	for _, s := range x.sources() {
		if roots := s.roots(); slices.Contains(roots, dir) {
			for _, r := range roots {
				x.watch(r)
			}
			for _, d := range s.dirs() {
				x.watch(d)
				for _, f := range s.files(d) {
					fresh = x.look(f) || fresh
				}
			}
			return fresh
		}
		for _, d := range s.dirs() {
			// dsh: a file in <project>/<session>/ is that project's.
			if d == dir || filepath.Dir(dir) == d && strings.HasPrefix(d, filepath.Join(x.o.DshHome, "sessions")+"/") {
				x.watch(dir)
				seen := map[string]bool{}
				for _, f := range s.files(d) {
					seen[f.path] = true
					fresh = x.look(f) || fresh
				}
				x.mu.Lock()
				for id, e := range x.entries {
					if (filepath.Dir(e.path) == d || filepath.Dir(filepath.Dir(e.path)) == d) && !seen[e.path] {
						if _, err := os.Stat(e.path); err != nil {
							delete(x.entries, id)
							delete(x.files, id)
							x.changed[id] = true
						}
					}
				}
				x.mu.Unlock()
				return fresh
			}
		}
	}
	return false
}

// look reads a transcript again if it changed since the last look; true if it is new.
func (x *Index) look(f onDisk) bool {
	st, err := os.Stat(f.path)
	if err != nil {
		return false
	}
	x.mu.Lock()
	e := x.entries[f.id]
	same := e != nil && e.path == f.path && e.size == st.Size() && e.mtime.Equal(st.ModTime())
	x.mu.Unlock()
	if same {
		return false
	}
	g := x.glance(f)
	if e != nil && e.path == f.path && g.cwd == "" {
		g.cwd = e.cwd // read before; the head does not change
	}
	x.mu.Lock()
	if g.skip {
		delete(x.entries, f.id)
		x.mu.Unlock()
		return false
	}
	x.entries[f.id] = &entry{onDisk: f, glimpse: g, size: st.Size(), mtime: st.ModTime()}
	x.changed[f.id] = true
	x.mu.Unlock()
	x.activity(f.agent, f.id, st.Size(), st.ModTime(), e != nil && e.path == f.path && st.Size() > e.size)
	return e == nil
}

// ---- what is told ----

func (x *Index) settle() {
	defer x.wg.Done()
	t := time.NewTicker(x.o.Settle)
	defer t.Stop()
	for {
		select {
		case <-x.ctx.Done():
			return
		case <-t.C:
		}
		x.mu.Lock()
		whole := x.whole
		ids := make([]string, 0, len(x.changed))
		for id := range x.changed {
			ids = append(ids, id)
		}
		x.changed, x.whole = map[string]bool{}, false
		x.mu.Unlock()
		if whole {
			x.publishWorkspaces()
		}
		if (len(ids) > 0 || whole) && x.o.OnChange != nil {
			slices.Sort(ids)
			x.o.OnChange(ids)
		}
	}
}

// Changed says the list as a whole changed (workspaces): it is told at the next settle.
func (x *Index) Changed(ids ...string) {
	x.mu.Lock()
	for _, id := range ids {
		x.changed[id] = true
	}
	if len(ids) == 0 {
		x.whole = true
	}
	x.mu.Unlock()
}

func (x *Index) publishWorkspaces() {
	if x.o.Hub == nil {
		return
	}
	ws, err := x.Workspaces()
	if err != nil {
		x.o.Logf("discover: workspaces: %v", err)
		return
	}
	x.o.Hub.Set("workspaces", ws)
}

// Sessions is every session found on disk, as the list has it; the overlay (names, pins,
// workspaces) is the session manager's to add.
func (x *Index) Sessions(context.Context) ([]api.Session, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	out := make([]api.Session, 0, len(x.entries))
	for _, e := range x.entries {
		out = append(out, x.row(e))
	}
	return out, nil
}

func (x *Index) row(e *entry) api.Session {
	s := api.Session{
		ID: e.id, Agent: e.agent, Cwd: e.cwd, Title: e.title, Status: api.StatusIdle,
		Last: e.last, UpdatedAt: e.mtime.UnixMilli(), Launcher: x.launchers[e.id],
	}
	if r, ok := x.running[e.id]; ok && !r.ours {
		s.Status, s.Live = api.StatusElsewhere, true
		if r.name != "" {
			s.Title = r.name
		}
	}
	return s
}

// Found is session id as the list has it, and its transcript, read through the index in its
// agent's format (none for dsh's, for now); false if it is not on disk.
func (x *Index) Found(id string) (api.Session, *transcript.File, bool) {
	x.mu.Lock()
	defer x.mu.Unlock()
	e := x.entries[id]
	if e == nil {
		return api.Session{}, nil, false
	}
	var f *transcript.File
	if info, ok := agents.Get(e.agent); ok {
		if f = x.files[id]; f == nil || f.Path() != e.path {
			f = transcript.OpenAs(e.path, info.Format, x.o.Store.Source)
			x.files[id] = f
		}
	}
	return x.row(e), f, true
}

// Free is nil when no process uses session id but Iridium's own, so Iridium may resume it:
// /proc is looked at afresh; Claude's registry must not have a live process of it; dsh's
// session lock and Codex's writer lock must be free.
func (x *Index) Free(id string) error {
	x.scanMu.Lock()
	x.scanProcs()
	x.scanMu.Unlock()
	x.mu.Lock()
	r, live := x.running[id]
	e := x.entries[id]
	x.mu.Unlock()
	if live && !r.ours {
		return fmt.Errorf("%w (process %d)", ErrBusy, r.pid)
	}
	for _, c := range x.claudeRegistry() {
		if c.SessionID == id && x.alive(c) {
			return fmt.Errorf("%w (process %d)", ErrBusy, c.PID)
		}
	}
	var lock string
	switch {
	case e != nil && e.agent == api.AgentDsh:
		lock = filepath.Join(filepath.Dir(e.path), "session.lock")
	case e != nil && e.agent == api.AgentCodex:
		lock = filepath.Join(x.o.CodexHome, "thread-writer-locks", id+".lock")
	}
	if lock != "" && locked(lock) {
		return fmt.Errorf("%w (its lock is held)", ErrBusy)
	}
	return nil
}
