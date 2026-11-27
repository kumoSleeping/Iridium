package discover

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/dshhome"
	"github.com/kumoSleeping/iridium/internal/remote"
	"github.com/kumoSleeping/iridium/internal/remote/wire"
	"github.com/kumoSleeping/iridium/internal/store"
	"github.com/kumoSleeping/iridium/internal/transcript"
)

// dsh's sessions at other machines (docs/exec.md "远端 DSH"). Nothing there is looked for: the
// dsh plugin in DSH Desktop tells of its sessions as they change (ToldSessions: the ones followed,
// and with its "sync new sessions" on the ones active since), of its workspaces (ToldWorkspaces)
// and of that switch (SetSync); one picked in a search is taken in (Adopt). A session told of is
// looked at once there (one directory: its transcript's name and size), its transcript fetched
// only when it is opened (Fetch). What is known is kept in the state directory, so a restart or a
// machine away lists them still.

// The executor's events discovery takes itself (SetRemote), and what they carry.
const (
	EventSessions   = "desktop.sessions.changed"
	EventWorkspaces = "desktop.workspaces.changed"
	EventConfig     = "desktop.config"
)

// DesktopSession is one session as its machine's dsh tells of it (unified schema with harness/dsh).
type DesktopSession struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspaceId,omitempty"`
	Cwd         string `json:"cwd,omitempty"`
	Title       string `json:"title,omitempty"`
	Last        int64  `json:"last,omitempty"` // ms, its last message
	UpdatedAt   int64  `json:"updatedAt,omitempty"`
	Sub         bool   `json:"sub,omitempty"`
	Parent      string `json:"parent,omitempty"`
	Ended       bool   `json:"ended,omitempty"` // its last turn ended
	// Open: loaded in DSH Desktop; Running: its agent at work.
	Open     bool `json:"open,omitempty"`
	Running  bool `json:"running,omitempty"`
	Archived bool `json:"archived,omitempty"`
}

// DesktopSessions is EventSessions: the sessions that changed, and those deleted there. Full: it
// names every session open there, so one it does not name is open no more.
type DesktopSessions struct {
	Full     bool             `json:"full,omitempty"`
	Sessions []DesktopSession `json:"sessions"`
	Gone     []string         `json:"gone,omitempty"`
}

// DesktopWorkspaces is EventWorkspaces: DSH Desktop's workspaces in its order, and the sessions
// archived there.
type DesktopWorkspaces struct {
	Workspaces []struct {
		ID        string   `json:"id"`
		Title     string   `json:"title"`
		Path      string   `json:"path"`
		Sessions  []string `json:"sessions"`
		UpdatedAt int64    `json:"updatedAt,omitempty"`
	} `json:"workspaces"`
	Archived []string `json:"archived,omitempty"`
}

// DesktopConfig is EventConfig, what of it is discovery's: the "sync new sessions" switch.
type DesktopConfig struct {
	Sync *bool `json:"syncNewSessions,omitempty"`
}

// dshAt is what is known of dsh at one machine; kept as JSON (State/remote-dsh/<id>.json).
type dshAt struct {
	Home     string                    `json:"home"` // its DSH_HOME there
	Sep      string                    `json:"sep"`
	Sessions map[string]*remoteSession `json:"sessions"`
	// The workspace registry, as last told or read (nil if not known); RegistryAt: the file's
	// mtime when read, 0 if told. Gone: its workspaces whose folder is not there.
	Registry   *dshhome.Registry `json:"registry,omitempty"`
	RegistryAt int64             `json:"registryAt,omitempty"`
	Gone       map[string]bool   `json:"gone,omitempty"`
	// Sync: its "sync new sessions" is on, since SyncAt (ms): a session active after that is shown.
	Sync   bool  `json:"sync,omitempty"`
	SyncAt int64 `json:"syncAt,omitempty"`
}

// remoteSession is one session's transcript there and what is known of it.
type remoteSession struct {
	Project  string `json:"project"` // its directory under sessions/
	File     string `json:"file"`    // the transcript's name in <project>/<id>/
	Size     int64  `json:"size"`    // its size there
	Mtime    int64  `json:"mtime"`   // ms
	Cwd      string `json:"cwd"`
	Title    string `json:"title,omitempty"`
	Last     int64  `json:"last,omitempty"`
	Headed   bool   `json:"headed,omitempty"` // its header was read: Sub and Parent are known
	Sub      bool   `json:"sub,omitempty"`
	Parent   string `json:"parent,omitempty"`
	Ended    bool   `json:"ended,omitempty"`
	Archived bool   `json:"archived,omitempty"` // as its machine said
	looked   time.Time
}

const (
	remoteCalls   = 8               // file requests at once, at one machine
	remoteFetchIn = 2 * time.Second // a session is looked at there, its transcript mirrored, at most this often
	remoteAway    = 5 * time.Second // how often machines gone away are noticed (nothing is asked of them)
)

// SetRemote gives the executors whose dsh sessions are listed, and takes their events of them;
// before Start, and before they connect.
func (x *Index) SetRemote(h *remote.Hub) {
	x.hub = h
	h.Handle(EventSessions, func(machine string, params json.RawMessage) {
		var p DesktopSessions
		if json.Unmarshal(params, &p) == nil && !x.here(machine) {
			x.enqueue(machine, func() { x.ToldSessions(x.ctx, machine, p) })
		}
	})
	h.Handle(EventWorkspaces, func(machine string, params json.RawMessage) {
		var p DesktopWorkspaces
		if json.Unmarshal(params, &p) == nil && !x.here(machine) {
			x.enqueue(machine, func() { x.ToldWorkspaces(machine, p) })
		}
	})
	h.Handle(EventConfig, func(machine string, params json.RawMessage) {
		var p DesktopConfig
		if json.Unmarshal(params, &p) != nil || p.Sync == nil {
			return
		}
		if x.here(machine) {
			machine = ""
		}
		x.enqueue(machine, func() { x.SetSync(machine, *p.Sync) })
	})
}

// here: machine is an executor on this computer (DSH Desktop here): its sessions are this disk's.
func (x *Index) here(machine string) bool {
	return x.hub != nil && x.hub.Machine(machine).IsLocal()
}

// enqueue runs f after what machine's events before it asked for, off the connection's reader.
func (x *Index) enqueue(machine string, f func()) {
	x.remoteMu.Lock()
	x.queue[machine] = append(x.queue[machine], f)
	run := !x.working[machine] && x.ctx.Err() == nil
	if run {
		x.working[machine] = true
		x.wg.Add(1)
	}
	x.remoteMu.Unlock()
	if !run {
		return
	}
	go func() {
		defer x.wg.Done()
		for {
			x.remoteMu.Lock()
			q := x.queue[machine]
			if len(q) == 0 || x.ctx.Err() != nil {
				delete(x.working, machine)
				delete(x.queue, machine)
				x.remoteMu.Unlock()
				return
			}
			f := q[0]
			x.queue[machine] = q[1:]
			x.remoteMu.Unlock()
			f()
		}
	}()
}

func (x *Index) remoteDir() string { return filepath.Join(x.o.State, "remote-dsh") }

// mirrorPath is where session id's transcript at machine is mirrored here.
func (x *Index) mirrorPath(machine, id, file string) string {
	return filepath.Join(x.remoteDir(), machine, id, file)
}

// loadRemote lists what was found at each machine before, as the state directory has it.
func (x *Index) loadRemote() {
	known, _ := x.knownMachines()
	ents, _ := os.ReadDir(x.remoteDir())
	for _, e := range ents {
		id, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || e.IsDir() || !validID(id) {
			continue
		}
		if known != nil && !known[id] {
			x.forgetRemote(id) // its Bot gone (revoked, or the machine paired again)
			continue
		}
		var d dshAt
		b, err := os.ReadFile(filepath.Join(x.remoteDir(), e.Name()))
		if err != nil || json.Unmarshal(b, &d) != nil || d.Sessions == nil {
			continue
		}
		x.remoteMu.Lock()
		x.remotes[id] = &d
		x.remoteMu.Unlock()
		x.putRemote(id, &d)
	}
}

// knownMachines is the machines that have connected and whose app is still there (a revoked one's
// is forgotten, store.ForgetMachine), and every app there is: what was found at a machine is
// dropped only once its app is gone, never for a row of machines missing alone; nil if they cannot
// be had.
func (x *Index) knownMachines() (map[string]bool, error) {
	if x.o.Store == nil {
		return nil, nil
	}
	ms, err := x.o.Store.Machines()
	if err != nil {
		return nil, err
	}
	known, err := x.o.Store.AppIDs()
	if err != nil {
		return nil, err
	}
	for _, m := range ms {
		known[m.Terminal] = true
	}
	return known, nil
}

// dropGone forgets what was found at machines no longer there: their sessions are not listed, as
// their groups are not (the user, 2026-11-08: an old machine's group offered a "+" that failed).
func (x *Index) dropGone() {
	known, err := x.knownMachines()
	if known == nil || err != nil {
		return
	}
	if x.hub != nil {
		for _, id := range x.hub.Online() { // connected now: its Bot is there (noted a moment later)
			known[id] = true
		}
	}
	x.remoteMu.Lock()
	var gone []string
	for id := range x.remotes {
		if !known[id] {
			gone = append(gone, id)
		}
	}
	x.remoteMu.Unlock()
	for _, id := range gone {
		x.forgetRemote(id)
	}
}

// forgetRemote drops machine's sessions from the list, and what was kept of them here.
func (x *Index) forgetRemote(machine string) {
	x.remoteMu.Lock()
	delete(x.remotes, machine)
	x.remoteMu.Unlock()
	x.mu.Lock()
	for id, e := range x.entries {
		if e.machine == machine {
			delete(x.entries, id)
			delete(x.files, id)
			x.changed[id] = true
		}
	}
	delete(x.open, machine)
	delete(x.filed, machine)
	x.mu.Unlock()
	os.Remove(filepath.Join(x.remoteDir(), machine+".json"))
	os.RemoveAll(filepath.Join(x.remoteDir(), machine))
}

func (x *Index) saveRemote(machine string, d *dshAt) {
	b, err := json.Marshal(d)
	if err == nil {
		err = os.MkdirAll(x.remoteDir(), 0o700)
	}
	if err == nil {
		tmp := filepath.Join(x.remoteDir(), machine+".json.tmp")
		if err = os.WriteFile(tmp, b, 0o600); err == nil {
			err = os.Rename(tmp, filepath.Join(x.remoteDir(), machine+".json"))
		}
	}
	if err != nil {
		x.o.Logf("discover: keeping what %s has: %v", machine, err)
	}
}

// remoteLoop notices machines gone: one away has no session open, one whose app is gone is
// forgotten. Nothing is asked of them.
func (x *Index) remoteLoop() {
	defer x.wg.Done()
	tick := time.NewTicker(remoteAway)
	defer tick.Stop()
	var dropped time.Time
	for {
		if time.Since(dropped) >= x.o.RemoteScan {
			dropped = time.Now()
			x.dropGone()
		}
		x.mu.Lock()
		var with []string
		for id := range x.open {
			with = append(with, id)
		}
		x.mu.Unlock()
		for _, id := range with {
			if _, ok := x.hub.Machine(id).Hello(); !ok {
				x.setOpen(id, nil, nil)
			}
		}
		select {
		case <-x.ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// setOpen notes the sessions open in machine's dsh, and of those the running ones (busy; nil: not
// said); its sessions whose state that changes are told.
func (x *Index) setOpen(machine string, open, busy map[string]bool) {
	x.mu.Lock()
	defer x.mu.Unlock()
	was, wasBusy := x.open[machine], x.busy[machine]
	running := func(o, b map[string]bool, id string) bool { return o[id] && (b == nil || b[id]) }
	for _, set := range []map[string]bool{was, open} {
		for id := range set {
			if was[id] != open[id] || running(was, wasBusy, id) != running(open, busy, id) {
				x.changed[id] = true
			}
		}
	}
	if len(open) == 0 {
		delete(x.open, machine)
		delete(x.busy, machine)
		return
	}
	x.open[machine] = open
	if busy == nil {
		delete(x.busy, machine)
	} else {
		x.busy[machine] = busy
	}
}

// HeldElsewhere: session id at another machine is open in DSH Desktop there, running or not:
// Desktop holds its writer lock, so its agent there takes a message, not a resume here.
func (x *Index) HeldElsewhere(id string) bool {
	x.mu.Lock()
	defer x.mu.Unlock()
	e := x.entries[id]
	return e != nil && e.machine != "" && x.open[e.machine][id]
}

// dshHome is where dsh keeps its sessions at mc ("" if it has no dsh), as the agent finds its
// transcripts there (agents' FindRemote): the home it said, else DSH_HOME, else ~/.dsh.
func dshHome(mc *remote.Machine) (home, sep string, ok bool) {
	hi, online := mc.Hello()
	ai, has := mc.Agent(string(api.AgentDsh))
	if !online || !has {
		return "", "", false
	}
	return cmp.Or(dshhome.Home(ai.Home, hi.Env), remote.Join(hi.Sep, hi.Home, ".dsh")), hi.Sep, true
}

// at is what is known of dsh at machine, made if it is connected and nothing is known yet; nil if
// it has no dsh, or is away and unknown. A home that moved starts anew. machine's lock held.
func (x *Index) at(machine string) *dshAt {
	home, sep, ok := dshHome(x.hub.Machine(machine))
	x.remoteMu.Lock()
	defer x.remoteMu.Unlock()
	d := x.remotes[machine]
	switch {
	case d == nil && !ok:
		return nil
	case d == nil, ok && (d.Home != home || d.Sep != sep):
		was := d
		d = &dshAt{Home: home, Sep: sep, Sessions: map[string]*remoteSession{}}
		if was != nil {
			d.Sync, d.SyncAt = was.Sync, was.SyncAt
		}
		x.remotes[machine] = d
	}
	return d
}

// ToldSessions takes what machine's dsh said of its sessions: each is looked at there (its
// transcript's name and size, at most every remoteFetchIn), listed, and shown if it may be
// (remoteShown).
func (x *Index) ToldSessions(ctx context.Context, machine string, p DesktopSessions) {
	if x.hub == nil {
		return
	}
	defer x.lockMachine(machine)()
	x.told(ctx, machine, p, false)
}

// Adopt takes in a session of machine's ("": this computer's) picked in a search: told of, and
// shown whatever the switch says. An error if its transcript is not found there.
func (x *Index) Adopt(ctx context.Context, machine string, s DesktopSession) error {
	if machine == "" {
		return x.adoptLocal(s.ID)
	}
	if x.hub == nil {
		return fmt.Errorf("no machine %q", machine)
	}
	mc := x.hub.Find(machine)
	if mc == nil {
		return fmt.Errorf("no machine %q", machine)
	}
	if _, online := mc.Hello(); !online {
		return fmt.Errorf("%w: %s", wire.ErrOffline, machine)
	}
	defer x.lockMachine(mc.ID())()
	d := x.told(ctx, mc.ID(), DesktopSessions{Sessions: []DesktopSession{s}}, true)
	if d == nil || d.Sessions[s.ID] == nil || d.Sessions[s.ID].File == "" {
		return fmt.Errorf("%w: session %s at %s", os.ErrNotExist, s.ID, machine)
	}
	return nil
}

// told is ToldSessions and Adopt (adopt: its sessions are shown whatever the switch says);
// machine's lock held.
func (x *Index) told(ctx context.Context, machine string, p DesktopSessions, adopt bool) *dshAt {
	d := x.at(machine)
	if d == nil {
		return nil
	}
	mc := x.hub.Machine(machine)
	x.mu.Lock()
	open, busy := map[string]bool{}, map[string]bool{}
	if !p.Full {
		for id := range x.open[machine] {
			open[id] = true
		}
		for id := range x.busy[machine] {
			busy[id] = true
		}
	}
	x.mu.Unlock()
	for _, id := range p.Gone {
		delete(d.Sessions, id)
		delete(open, id)
		delete(busy, id)
	}
	type one struct {
		id       string
		s        *remoteSession
		archived bool // before
		size     int64
	}
	var all []one
	run := limiter(remoteCalls)
	for _, t := range p.Sessions {
		if !validID(t.ID) {
			continue
		}
		s := d.Sessions[t.ID]
		if s == nil {
			s = &remoteSession{}
			d.Sessions[t.ID] = s
		}
		all = append(all, one{t.ID, s, s.Archived, s.Size})
		if t.Cwd != "" && t.Cwd != s.Cwd {
			s.Cwd, s.Project, s.File, s.Headed = t.Cwd, "", "", false
		}
		s.Title, s.Last = cmp.Or(t.Title, s.Title), max(s.Last, t.Last)
		s.Sub, s.Parent = s.Sub || t.Sub, cmp.Or(t.Parent, s.Parent)
		s.Ended, s.Archived = t.Ended, t.Archived
		delete(open, t.ID)
		delete(busy, t.ID)
		if t.Open || t.Running {
			open[t.ID] = true
		}
		if t.Running {
			busy[t.ID] = true
		}
		if s.File == "" || time.Since(s.looked) >= remoteFetchIn {
			run(func() { x.lookRemote(ctx, mc, d, t.ID, s) })
		}
	}
	run(nil)
	x.putRemote(machine, d)
	x.setOpen(machine, open, busy)
	x.saveRemote(machine, d)
	for _, o := range all {
		x.remoteShown(d, o.id, o.s, o.archived, adopt)
		x.follow(ctx, mc, machine, d, o.id, o.s, o.size)
	}
	return d
}

// follow brings the mirror of a session read here before up to its transcript there, if that
// grew past was.
func (x *Index) follow(ctx context.Context, mc *remote.Machine, machine string, d *dshAt, id string, s *remoteSession, was int64) {
	if s.File == "" || s.Size == was {
		return
	}
	local := x.mirrorPath(machine, id, s.File)
	if _, err := os.Stat(local); err != nil {
		return
	}
	if err := mc.Mirror(ctx, d.there(id, s), local); err != nil {
		x.o.Logf("discover: mirroring %s from %s: %v", id, machine, err)
	}
	x.Changed(id)
}

// there is session id's transcript at its machine.
func (d *dshAt) there(id string, s *remoteSession) string {
	return remote.Join(d.Sep, append(append([]string{d.Home}, dshhome.SessionsDir...), s.Project, id, s.File)...)
}

// ToldWorkspaces takes DSH Desktop's workspaces at machine as it said them: a session archived
// there since is archived here, the ones in none of them are its loose ones.
func (x *Index) ToldWorkspaces(machine string, p DesktopWorkspaces) {
	if x.hub == nil {
		return
	}
	defer x.lockMachine(machine)()
	d := x.at(machine)
	if d == nil {
		return
	}
	r := dshhome.Registry{Archived: map[string]bool{}}
	for _, w := range p.Workspaces {
		if w.ID != "" && w.Path != "" {
			r.Workspaces = append(r.Workspaces, dshhome.Workspace{ID: w.ID, Title: w.Title, Path: w.Path, Sessions: w.Sessions, UpdatedAt: w.UpdatedAt})
		}
	}
	for _, id := range p.Archived {
		r.Archived[id] = true
	}
	x.setRegistry(machine, d, &r, 0)
	x.saveRemote(machine, d)
}

// setRegistry makes r machine's registry (at: its file's mtime, 0 if told): the list follows, and
// the sessions archived there since are archived here.
func (x *Index) setRegistry(machine string, d *dshAt, r *dshhome.Registry, at int64) {
	was := d.Registry
	d.Registry, d.RegistryAt, d.Gone = r, at, nil
	x.putRemote(machine, d)
	if r == nil {
		return
	}
	for id := range r.Archived {
		if s := d.Sessions[id]; s != nil && (was == nil || !was.Archived[id]) {
			x.remoteShown(d, id, s, false, false)
		}
	}
}

// SetSync sets machine's "sync new sessions" ("": this computer's, dshsync.go): on, its dsh's
// sessions active from now are shown as they are told of; off, only those taken in (Adopt) and
// Iridium's own.
func (x *Index) SetSync(machine string, on bool) {
	if machine == "" {
		x.setLocalSync(on)
		return
	}
	if x.hub == nil {
		return
	}
	defer x.lockMachine(machine)()
	d := x.at(machine)
	if d == nil || d.Sync == on {
		return
	}
	d.Sync, d.SyncAt = on, 0
	if on {
		d.SyncAt = time.Now().UnixMilli()
	}
	x.saveRemote(machine, d)
}

// Sync is machine's "sync new sessions" ("": this computer's).
func (x *Index) Sync(machine string) bool {
	if machine == "" {
		on, _ := x.localSync()
		return on
	}
	x.remoteMu.Lock()
	defer x.remoteMu.Unlock()
	d := x.remotes[machine]
	return d != nil && d.Sync
}

// ScanMachine looks again now at what is known of dsh at machine id (asked for: a "sync now"):
// its registry, and its sessions that are listed. It finds no session: those are told of.
func (x *Index) ScanMachine(ctx context.Context, id string) error {
	if x.hub == nil {
		return nil
	}
	mc := x.hub.Machine(id)
	if _, _, ok := dshHome(mc); !ok {
		return nil
	}
	defer x.lockMachine(id)()
	d := x.at(id)
	if d == nil {
		return nil
	}
	x.scanRegistry(ctx, id, mc, d, false)
	var vis store.Visibility
	if x.o.Store != nil {
		var err error
		if vis, err = x.o.Store.Visibility(); err != nil {
			return err
		}
	}
	sizes := map[string]int64{}
	run := limiter(remoteCalls)
	for sid, s := range d.Sessions {
		if vis != nil && !vis.Shown(string(api.AgentDsh), sid) {
			continue
		}
		sizes[sid] = s.Size
		run(func() { x.lookRemote(ctx, mc, d, sid, s) })
	}
	run(nil)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	x.putRemote(id, d)
	x.saveRemote(id, d)
	for sid, was := range sizes {
		x.follow(ctx, mc, id, d, sid, d.Sessions[sid], was)
	}
	return nil
}

// lookRemote looks at one session there: its transcript's name, size and time, and once its
// header (a subagent's parent; its folder, if none was told). Its directory is the one dsh keeps
// its folder's sessions in; a session whose folder is not known is looked for under each.
func (x *Index) lookRemote(ctx context.Context, mc *remote.Machine, d *dshAt, id string, s *remoteSession) {
	sessions := remote.Join(d.Sep, append([]string{d.Home}, dshhome.SessionsDir...)...)
	list := func(project string) ([]wire.Entry, error) {
		return mc.List(ctx, remote.Join(d.Sep, sessions, project, id))
	}
	project := s.Project
	if project == "" && s.Cwd != "" {
		project = transcript.DshProjectDir(s.Cwd)
	}
	var ents []wire.Entry
	err := error(os.ErrNotExist)
	if project != "" {
		ents, err = list(project)
	}
	if err != nil && (project == "" || wire.IsCode(err, wire.CodeNotFound)) {
		ps, perr := mc.List(ctx, sessions)
		if perr != nil {
			return
		}
		for _, p := range ps {
			if p.Type != "dir" || p.Name == project {
				continue
			}
			if ents, err = list(p.Name); err == nil {
				project = p.Name
				break
			}
			if ctx.Err() != nil {
				return
			}
		}
	}
	if err != nil {
		return
	}
	var names []string
	sizes := map[string]wire.Entry{}
	for _, e := range ents {
		if e.Type == "file" {
			names = append(names, e.Name)
			sizes[e.Name] = e
		}
	}
	best, _ := transcript.DshBest(names)
	if best == "" {
		return
	}
	if best != s.File || project != s.Project {
		s.Headed = false
	}
	s.Project, s.File, s.Size, s.Mtime, s.looked = project, best, sizes[best].Size, sizes[best].Mtime, time.Now()
	if !s.Headed {
		path := d.there(id, s)
		for _, n := range []int64{8 << 10, dshhome.HeadBytes} {
			b, err := mc.ReadAt(ctx, path, 0, n)
			if err != nil {
				return
			}
			h, err := dshhome.ParseHead(b)
			if err == nil {
				s.Headed, s.Sub, s.Parent = true, h.Sub, h.Parent
				s.Cwd = cmp.Or(s.Cwd, h.Cwd)
				break
			}
			if int64(len(b)) < n {
				break // the whole file, and no header in it yet
			}
		}
	}
}

// scanRegistry reads the workspace registry there again if its file changed, and then (or if
// folders, as for a new session's choice) which of its workspaces' folders are gone.
func (x *Index) scanRegistry(ctx context.Context, machine string, mc *remote.Machine, d *dshAt, folders bool) {
	path := remote.Join(d.Sep, append([]string{d.Home}, dshhome.RegistryPath...)...)
	st, err := mc.Stat(ctx, path)
	if err != nil {
		if wire.IsCode(err, wire.CodeNotFound) && d.RegistryAt != 0 {
			x.setRegistry(machine, d, nil, 0)
		}
		return
	}
	if st.Mtime != d.RegistryAt {
		b, err := mc.ReadFile(ctx, path, 16<<20)
		if err != nil {
			return
		}
		r, err := dshhome.ParseRegistry(b)
		if err != nil {
			x.setRegistry(machine, d, nil, st.Mtime) // another dsh's: the sessions' folders stand in
			return
		}
		x.setRegistry(machine, d, &r, st.Mtime)
		folders = true
	}
	if d.Registry == nil || !folders {
		return
	}
	gone := map[string]bool{}
	var mu sync.Mutex
	run := limiter(remoteCalls)
	for _, w := range d.Registry.Workspaces {
		run(func() {
			st, err := mc.Stat(ctx, w.Path)
			if err == nil && st.Type == "dir" || err != nil && !wire.IsCode(err, wire.CodeNotFound) {
				return
			}
			mu.Lock()
			gone[w.ID] = true
			mu.Unlock()
		})
	}
	run(nil)
	d.Gone = gone
}

// limiter runs functions n at a time; run(nil) waits for all.
func limiter(n int) func(f func()) {
	sem := make(chan struct{}, n)
	var wg sync.WaitGroup
	return func(f func()) {
		if f == nil {
			wg.Wait()
			return
		}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			f()
		}()
	}
}

// lockMachine takes machine's scan lock (one look at a time there); the result gives it back.
func (x *Index) lockMachine(machine string) func() {
	x.remoteMu.Lock()
	l := x.scanLocks[machine]
	if l == nil {
		l = &sync.Mutex{}
		x.scanLocks[machine] = l
	}
	x.remoteMu.Unlock()
	l.Lock()
	return l.Unlock
}

// putRemote makes the index's entries of machine what d has.
func (x *Index) putRemote(machine string, d *dshAt) {
	var filed map[string]bool
	if d.Registry != nil {
		filed = map[string]bool{}
		for _, w := range d.Registry.Workspaces {
			for _, id := range w.Sessions {
				filed[id] = true
			}
		}
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	was0, known0 := x.filed[machine]
	if filed == nil {
		delete(x.filed, machine)
	} else {
		x.filed[machine] = filed
	}
	for id, e := range x.entries {
		if s := d.Sessions[id]; e.machine == machine && (s == nil || s.File == "" || s.Cwd == "") {
			delete(x.entries, id)
			delete(x.files, id)
			x.changed[id] = true
		}
	}
	for id, s := range d.Sessions {
		if s.File == "" || s.Cwd == "" {
			continue // not written yet, or not found there (looked at again when told of)
		}
		e := &entry{
			onDisk:  onDisk{id: id, agent: api.AgentDsh, path: x.mirrorPath(machine, id, s.File)},
			glimpse: glimpse{cwd: s.Cwd, title: s.Title, sub: s.Sub, parent: s.Parent, done: s.Sub && s.Ended},
			size:    s.Size, mtime: time.UnixMilli(max(s.Mtime, s.Last)),
			machine: machine, there: d.there(id, s),
		}
		if o := x.entries[id]; o == nil || o.machine != machine || o.size != e.size || o.mtime != e.mtime || o.title != e.title || o.cwd != e.cwd || o.path != e.path || o.done != e.done || (known0 && !e.sub && !was0[id]) != x.loose(e) {
			x.changed[id] = true
		}
		x.entries[id] = e
	}
}

// remoteShown sets or takes off the marker of session id at a machine. Archived in DSH Desktop
// since it was last told of (wasArchived: it was before): archived here. Else shown if taken in
// (adopt); or, with the machine's sync on, if it was active after that was set, and after it was
// archived here if it was. A subagent goes as its parent does.
func (x *Index) remoteShown(d *dshAt, id string, s *remoteSession, wasArchived, adopt bool) {
	st := x.o.Store
	if st == nil || s.File == "" {
		return
	}
	agent := string(api.AgentDsh)
	r, ok, err := st.ShownRow(agent, id)
	if err != nil {
		x.o.Logf("discover: %v", err)
		return
	}
	archived := s.Archived || d.Registry != nil && d.Registry.Archived[id]
	active := max(s.Mtime, s.Last)
	synced := d.Sync && active > d.SyncAt
	if s.Sub && s.Parent != "" && !adopt {
		p, pok, _ := st.ShownRow(agent, s.Parent)
		synced = pok && p.Shown
	}
	changed := false
	switch {
	case ok && r.Legacy:
		err = st.Hide(agent, id, s.Size, 0)
		changed = r.Shown
	case archived && !adopt:
		if ok && r.Shown && !wasArchived {
			err = st.Hide(agent, id, s.Size, 0)
			changed = true
		}
	case ok && r.Shown:
	case adopt:
		changed, err = st.Show(agent, id)
	case !synced:
	case !ok, active > r.At && time.Now().UnixMilli() >= r.QuietUntil:
		changed, err = st.Show(agent, id)
	}
	if err != nil {
		x.o.Logf("discover: %v", err)
	}
	if changed {
		x.Changed(id)
	}
}

// Fetch brings the mirror of session id's transcript up to date, if it is one at another machine
// (at most every remoteFetchIn); one not connected keeps what was mirrored.
func (x *Index) Fetch(ctx context.Context, id string) error {
	x.mu.Lock()
	e := x.entries[id]
	var machine, there, local string
	if e != nil {
		machine, there, local = e.machine, e.there, e.path
	}
	x.mu.Unlock()
	if machine == "" || x.hub == nil {
		return nil
	}
	x.fetchMu.Lock()
	if time.Since(x.fetched[id]) < remoteFetchIn {
		x.fetchMu.Unlock()
		return nil
	}
	x.fetched[id] = time.Now()
	x.fetchMu.Unlock()
	mc := x.hub.Find(machine)
	if mc == nil {
		mc = x.hub.Machine(machine)
	}
	online := false
	if mc != nil {
		_, online = mc.Hello()
	}
	if !online {
		if _, err := os.Stat(local); err == nil {
			return nil
		}
		return fmt.Errorf("%w: %s", wire.ErrOffline, machine)
	}
	// As it is there now (it may have grown, or moved to a newer format's file, since told of).
	unlock := x.lockMachine(mc.ID())
	x.remoteMu.Lock()
	d := x.remotes[mc.ID()]
	x.remoteMu.Unlock()
	if d != nil && d.Sessions[id] != nil {
		s := d.Sessions[id]
		x.lookRemote(ctx, mc, d, id, s)
		if s.File != "" {
			x.putRemote(mc.ID(), d)
			there, local = d.there(id, s), x.mirrorPath(mc.ID(), id, s.File)
		}
	}
	unlock()
	before, _ := os.Stat(local)
	if err := mc.Mirror(ctx, there, local); err != nil {
		if _, serr := os.Stat(local); serr == nil {
			return nil
		}
		return err
	}
	if after, err := os.Stat(local); err == nil && (before == nil || after.Size() != before.Size()) {
		x.Changed(id)
	}
	return nil
}

// DshWorkspaces is what a new session at machine (a terminal ID or name) may open in: dsh's
// workspaces there, DSH Desktop's order (their folders gone left out); else, when its registry
// is not known, the folders of its sessions, the latest first. Paths are the machine's own.
func (x *Index) DshWorkspaces(ctx context.Context, machine string) ([]api.DshWorkspace, error) {
	if x.hub == nil {
		return nil, fmt.Errorf("no machine %q", machine)
	}
	mc := x.hub.Find(machine)
	if mc == nil {
		return nil, fmt.Errorf("no machine %q", machine)
	}
	if _, _, ok := dshHome(mc); !ok {
		if _, online := mc.Hello(); !online {
			return nil, fmt.Errorf("%w: %s", wire.ErrOffline, machine)
		}
		return []api.DshWorkspace{}, nil // no dsh there
	}
	defer x.lockMachine(mc.ID())()
	d := x.at(mc.ID())
	if d == nil {
		return []api.DshWorkspace{}, nil
	}
	x.scanRegistry(ctx, mc.ID(), mc, d, true) // as it is now, its folders looked at
	x.saveRemote(mc.ID(), d)
	// Listed: a session of its there, not a subagent, not archived here.
	vis, err := x.o.Store.Visibility()
	if err != nil {
		return nil, err
	}
	listed := func(id string) bool {
		s := d.Sessions[id]
		return s != nil && !s.Sub && vis.Shown(string(api.AgentDsh), id)
	}
	out := []api.DshWorkspace{}
	if d.Registry != nil {
		for _, w := range d.Registry.Workspaces {
			if !d.Gone[w.ID] {
				n := 0
				for _, id := range w.Sessions {
					if listed(id) {
						n++
					}
				}
				out = append(out, api.DshWorkspace{Title: cmp.Or(w.Title, remote.Base(d.Sep, w.Path)), Path: w.Path, UpdatedAt: w.UpdatedAt, Sessions: n})
			}
		}
		return out, nil
	}
	latest, count := map[string]int64{}, map[string]int{}
	for id, s := range d.Sessions {
		if !s.Sub && s.Cwd != "" {
			latest[s.Cwd] = max(latest[s.Cwd], s.Mtime, s.Last)
			if listed(id) {
				count[s.Cwd]++
			}
		}
	}
	for cwd, at := range latest {
		out = append(out, api.DshWorkspace{Title: remote.Base(d.Sep, cwd), Path: cwd, UpdatedAt: at, Sessions: count[cwd]})
	}
	slices.SortFunc(out, func(a, b api.DshWorkspace) int { return cmp.Compare(b.UpdatedAt, a.UpdatedAt) })
	return out, nil
}
