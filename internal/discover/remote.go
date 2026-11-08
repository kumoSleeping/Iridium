package discover

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/kumoSleeping/iridium/internal/agent"
	"github.com/kumoSleeping/iridium/internal/agents"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/dshhome"
	"github.com/kumoSleeping/iridium/internal/remote"
	"github.com/kumoSleeping/iridium/internal/remote/wire"
	"github.com/kumoSleeping/iridium/internal/transcript"
)

// dsh's sessions at other machines (docs/exec.md "远端 DSH"): each connected executor with dsh
// is looked at through its file requests every RemoteScan, what changed only: its sessions/
// project directories (by their mtime), the projection cache (one small document per session:
// folder, title, last message) and the workspace registry. A transcript is fetched only when
// its session is opened (Fetch), then kept up as it grows. All of them are listed, the ones
// archived in DSH Desktop in the archive; what was found is kept in the state directory, so a
// restart or a machine away lists them still.

// dshAt is what was found of dsh at one machine; kept as JSON (State/remote-dsh/<id>.json).
type dshAt struct {
	Home     string                    `json:"home"` // its DSH_HOME there
	Sep      string                    `json:"sep"`
	Projects map[string]int64          `json:"projects"` // sessions/ project directory → its mtime when listed
	Sessions map[string]*remoteSession `json:"sessions"`
	// The workspace registry, as last read (nil if it is not there or not known); RegistryAt:
	// its mtime then. Gone: its workspaces whose folder is not there.
	Registry   *dshhome.Registry `json:"registry,omitempty"`
	RegistryAt int64             `json:"registryAt,omitempty"`
	Gone       map[string]bool   `json:"gone,omitempty"`
	// Deep: when sessions without a summary were last looked at one by one.
	Deep int64 `json:"deep,omitempty"`
	// Unsynced: the workspaces the dsh plugin's settings keep from Iridium (names or folders, as
	// its dsh.open last said): their sessions are not listed, nor offered for a new one.
	Unsynced []string `json:"unsynced,omitempty"`
	// NoLoose: the sessions in none of its workspaces (DSH's 未分组) are kept from Iridium too.
	NoLoose bool `json:"noLoose,omitempty"`
}

// unsynced: workspace w is one of those d.Unsynced names, by its title or its folder (the
// folder's whole path or its name; case and separators aside).
func (d *dshAt) unsynced(w dshhome.Workspace) bool {
	norm := func(s string) string {
		return strings.TrimRight(strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s), `\`, "/")), "/")
	}
	for _, u := range d.Unsynced {
		if u := norm(u); u != "" && (u == norm(w.Title) || u == norm(w.Path) || u == norm(remote.Base(d.Sep, w.Path))) {
			return true
		}
	}
	return false
}

// unsyncedSessions is the sessions kept from Iridium: those in the workspaces kept, and with
// NoLoose those in none (and their subagents, which no workspace lists).
func (d *dshAt) unsyncedSessions() map[string]bool {
	if d.Registry == nil || len(d.Unsynced) == 0 && !d.NoLoose {
		return nil
	}
	out := map[string]bool{}
	filed := map[string]bool{}
	for _, w := range d.Registry.Workspaces {
		kept := d.unsynced(w)
		for _, id := range w.Sessions {
			filed[id] = true
			if kept {
				out[id] = true
			}
		}
	}
	if d.NoLoose {
		for id, s := range d.Sessions {
			if !s.Sub && !filed[id] {
				out[id] = true
			}
		}
	}
	for id, s := range d.Sessions {
		if s.Sub && out[s.Parent] {
			out[id] = true
		}
	}
	return out
}

// remoteSession is one session's transcript there and what is known of it.
type remoteSession struct {
	Project string `json:"project"` // its directory under sessions/
	File    string `json:"file"`    // the transcript's name in <project>/<id>/
	Size    int64  `json:"size"`    // its size there
	Mtime   int64  `json:"mtime"`   // ms
	Summary int64  `json:"summary"` // the summary's mtime when read; 0: none
	Cwd     string `json:"cwd"`
	Title   string `json:"title,omitempty"`
	Last    int64  `json:"last,omitempty"`
	Headed  bool   `json:"headed,omitempty"` // its header was read: Sub and Parent are known
	Sub     bool   `json:"sub,omitempty"`
	Parent  string `json:"parent,omitempty"`
}

const (
	remoteDeep    = 10 * time.Minute // sessions without a summary looked at one by one
	remoteCalls   = 8                // file requests at once, at one machine
	remoteFetchIn = 2 * time.Second  // a session's transcript is mirrored at most this often when read
)

// SetRemote gives the executors whose dsh sessions are listed; before Start.
func (x *Index) SetRemote(h *remote.Hub) { x.hub = h }

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
		x.putRemote(id, &d, nil)
	}
}

// knownMachines is the machines that have connected and whose Bot is still there (a revoked one's
// is forgotten, store.ForgetMachine); nil if they cannot be had.
func (x *Index) knownMachines() (map[string]bool, error) {
	if x.o.Store == nil {
		return nil, nil
	}
	ms, err := x.o.Store.Machines()
	if err != nil {
		return nil, err
	}
	known := make(map[string]bool, len(ms))
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

// remoteLoop looks at each connected machine every RemoteScan, and at once at one just come.
func (x *Index) remoteLoop() {
	defer x.wg.Done()
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	last := map[string]time.Time{}
	for {
		x.dropGone()
		for _, id := range x.hub.Online() {
			x.pollOpen(id)
			if time.Since(last[id]) < x.o.RemoteScan {
				continue
			}
			last[id] = time.Now()
			ctx, cancel := context.WithTimeout(x.ctx, 5*time.Minute)
			if err := x.ScanMachine(ctx, id); err != nil && !errors.Is(err, wire.ErrOffline) && x.ctx.Err() == nil {
				x.o.Logf("discover: dsh at %s: %v", id, err)
			}
			cancel()
		}
		for id := range last {
			if mc := x.hub.Machine(id); mc != nil {
				if _, ok := mc.Hello(); !ok {
					delete(last, id) // looked at again as soon as it is back
					x.setOpen(id, nil)
					x.remoteMu.Lock()
					delete(x.noOpen, id) // asked again: it may be another version then
					x.remoteMu.Unlock()
				}
			}
		}
		select {
		case <-x.ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// pollOpen asks machine id's dsh which sessions are open in it now (dsh.open, the dsh plugin's):
// those are shown run elsewhere, not to be resumed here. One that cannot say is not asked again
// until it connects anew.
func (x *Index) pollOpen(id string) {
	mc := x.hub.Machine(id)
	x.remoteMu.Lock()
	skip := x.noOpen[id]
	x.remoteMu.Unlock()
	if _, _, ok := dshHome(mc); skip || !ok {
		return
	}
	ctx, cancel := context.WithTimeout(x.ctx, 10*time.Second)
	defer cancel()
	r, err := mc.DshOpen(ctx)
	if wire.IsCode(err, wire.CodeUnsupported) {
		x.remoteMu.Lock()
		x.noOpen[id] = true
		x.remoteMu.Unlock()
		return
	}
	if err != nil {
		return // as it was: the next poll
	}
	open := map[string]bool{}
	for _, s := range r.Sessions {
		open[s] = true
	}
	x.setOpen(id, open)
	x.setUnsynced(id, r.Unsynced, r.UnsyncedUngrouped)
}

// setUnsynced notes the workspaces machine's dsh plugin keeps from Iridium; if they changed, its
// sessions are listed again as they say.
func (x *Index) setUnsynced(machine string, names []string, noLoose bool) {
	x.remoteMu.Lock()
	d := x.remotes[machine]
	same := d == nil || slices.Equal(d.Unsynced, names) && d.NoLoose == noLoose
	x.remoteMu.Unlock()
	if same {
		return
	}
	defer x.lockMachine(machine)()
	x.remoteMu.Lock()
	d = x.remotes[machine]
	x.remoteMu.Unlock()
	if d == nil {
		return
	}
	d.Unsynced, d.NoLoose = names, noLoose
	x.putRemote(machine, d, d.Registry)
	x.saveRemote(machine, d)
}

// setOpen notes the sessions open in machine's dsh; its sessions whose state that changes are told.
func (x *Index) setOpen(machine string, open map[string]bool) {
	x.mu.Lock()
	defer x.mu.Unlock()
	was := x.open[machine]
	for id := range was {
		if !open[id] {
			x.changed[id] = true
		}
	}
	for id := range open {
		if !was[id] {
			x.changed[id] = true
		}
	}
	if len(open) == 0 {
		delete(x.open, machine)
	} else {
		x.open[machine] = open
	}
}

// dshHome is where dsh keeps its sessions at mc ("" if it has no dsh), as the agent finds its
// transcripts there (agents' FindRemote): the home it said, else DSH_HOME, else ~/.dsh.
func dshHome(mc *remote.Machine) (home, sep string, ok bool) {
	hi, online := mc.Hello()
	ai, has := mc.Agent(string(api.AgentDsh))
	if !online || !has {
		return "", "", false
	}
	info, _ := agents.Get(api.AgentDsh)
	return cmp.Or(agent.GetEnv(info.RemoteEnv(hi.Env, ai.Home), "DSH_HOME"), remote.Join(hi.Sep, hi.Home, ".dsh")), hi.Sep, true
}

// ScanMachine looks at dsh's sessions at machine id now: what changed since the last look.
func (x *Index) ScanMachine(ctx context.Context, id string) error {
	if x.hub == nil {
		return nil
	}
	mc := x.hub.Machine(id)
	home, sep, ok := dshHome(mc)
	if !ok {
		return nil
	}
	defer x.lockMachine(id)()
	x.remoteMu.Lock()
	old := x.remotes[id]
	x.remoteMu.Unlock()
	d := &dshAt{Home: home, Sep: sep, Projects: map[string]int64{}, Sessions: map[string]*remoteSession{}}
	if old != nil && old.Home == home && old.Sep == sep {
		d.Registry, d.RegistryAt, d.Gone, d.Deep, d.Unsynced, d.NoLoose = old.Registry, old.RegistryAt, old.Gone, old.Deep, old.Unsynced, old.NoLoose
	} else {
		old = &dshAt{Projects: map[string]int64{}, Sessions: map[string]*remoteSession{}}
	}
	join := func(elem ...string) string { return remote.Join(sep, elem...) }
	sessionsDir := join(append([]string{home}, dshhome.SessionsDir...)...)
	projects, err := mc.List(ctx, sessionsDir)
	if wire.IsCode(err, wire.CodeNotFound) {
		projects, err = nil, nil // no session yet
	}
	if err != nil {
		return err
	}
	summaries := map[string]int64{}
	if ents, err := mc.List(ctx, join(append([]string{home}, dshhome.SummaryDir...)...)); err == nil {
		for _, e := range ents {
			if sid, ok := strings.CutSuffix(e.Name, ".json"); ok && e.Type == "file" {
				summaries[sid] = e.Mtime
			}
		}
	} else if !wire.IsCode(err, wire.CodeNotFound) {
		return err
	}
	deep := time.Since(time.UnixMilli(d.Deep)) >= remoteDeep
	if deep {
		d.Deep = time.Now().UnixMilli()
	}
	// Each project directory: listed again only if its mtime moved (a session made or gone in it).
	type look struct {
		id string
		s  *remoteSession
	}
	var looks []look
	var mu sync.Mutex
	run := limiter(remoteCalls)
	for _, p := range projects {
		if p.Type != "dir" || !strings.HasPrefix(p.Name, "--") {
			continue
		}
		d.Projects[p.Name] = p.Mtime
		kept, listed := old.Projects[p.Name]
		if listed && kept == p.Mtime {
			for sid, s := range old.Sessions {
				if s.Project == p.Name {
					c := *s
					d.Sessions[sid] = &c
				}
			}
			continue
		}
		run(func() {
			ents, err := mc.List(ctx, join(sessionsDir, p.Name))
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				delete(d.Projects, p.Name) // listed again next time
				for sid, s := range old.Sessions {
					if s.Project == p.Name {
						c := *s
						d.Sessions[sid] = &c
					}
				}
				return
			}
			for _, e := range ents {
				if e.Type != "dir" || !validID(e.Name) {
					continue
				}
				if s := old.Sessions[e.Name]; s != nil && s.Project == p.Name {
					c := *s
					d.Sessions[e.Name] = &c
				} else {
					d.Sessions[e.Name] = &remoteSession{Project: p.Name}
				}
			}
		})
	}
	run(nil)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	// Each session whose summary changed, new ones, and (now and then) those with no summary:
	// its transcript's name and size, its summary, its header once.
	for sid, s := range d.Sessions {
		sum, has := summaries[sid]
		if s.File != "" && (has && sum == s.Summary || !has && !deep) {
			continue
		}
		looks = append(looks, look{sid, s})
	}
	for _, l := range looks {
		run(func() { x.lookRemote(ctx, mc, join, home, l.id, l.s, summaries[l.id]) })
	}
	run(nil)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	x.scanRegistry(ctx, mc, d, false)
	x.remoteMu.Lock()
	x.remotes[id] = d
	x.remoteMu.Unlock()
	x.putRemote(id, d, old.Registry)
	x.saveRemote(id, d)
	// Mirrors of sessions read here follow them.
	for sid, s := range d.Sessions {
		if o := old.Sessions[sid]; s.File == "" || o != nil && o.Size == s.Size && o.Mtime == s.Mtime {
			continue
		}
		local := x.mirrorPath(id, sid, s.File)
		if _, err := os.Stat(local); err == nil {
			if err := mc.Mirror(ctx, join(sessionsDir, s.Project, sid, s.File), local); err != nil {
				x.o.Logf("discover: mirroring %s from %s: %v", sid, id, err)
			}
			x.Changed(sid)
		}
	}
	return nil
}

// lookRemote reads what changed of one session there into s.
func (x *Index) lookRemote(ctx context.Context, mc *remote.Machine, join func(...string) string, home, id string, s *remoteSession, summary int64) {
	dir := join(append(append([]string{home}, dshhome.SessionsDir...), s.Project, id)...)
	ents, err := mc.List(ctx, dir)
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
	if best != s.File {
		s.Headed = false
	}
	s.File, s.Size, s.Mtime = best, sizes[best].Size, sizes[best].Mtime
	if summary != 0 && summary != s.Summary {
		b, err := mc.ReadFile(ctx, join(append(append([]string{home}, dshhome.SummaryDir...), id+".json")...), 1<<20)
		if sum, perr := dshhome.ParseSummary(b); err == nil && perr == nil {
			s.Cwd, s.Title, s.Last, s.Summary = sum.Cwd, sum.Title, sum.Last, summary
		}
	}
	if !s.Headed {
		path := join(dir, best)
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

// scanRegistry reads the workspace registry again if it changed, and then (or if folders, as
// for a new session's choice) which of its workspaces' folders are gone.
func (x *Index) scanRegistry(ctx context.Context, mc *remote.Machine, d *dshAt, folders bool) {
	path := remote.Join(d.Sep, append([]string{d.Home}, dshhome.RegistryPath...)...)
	st, err := mc.Stat(ctx, path)
	if err != nil {
		if wire.IsCode(err, wire.CodeNotFound) {
			d.Registry, d.RegistryAt, d.Gone = nil, 0, nil
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
			d.Registry, d.RegistryAt, d.Gone = nil, st.Mtime, nil // another dsh's: the sessions' folders stand in
			return
		}
		d.Registry, d.RegistryAt, folders = &r, st.Mtime, true
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

// putRemote makes the index's entries of machine what d has, and their shown markers follow:
// a session first seen is shown unless DSH Desktop archived it; one archived there since the
// last look (was: the registry then) is archived here too; one archived that grew comes back.
func (x *Index) putRemote(machine string, d *dshAt, was *dshhome.Registry) {
	archived := func(r *dshhome.Registry, id string) bool { return r != nil && r.Archived[id] }
	kept := d.unsyncedSessions() // kept from Iridium: not listed
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
	was0, known0 := x.filed[machine]
	if filed == nil {
		delete(x.filed, machine)
	} else {
		x.filed[machine] = filed
	}
	for id, e := range x.entries {
		if s := d.Sessions[id]; e.machine == machine && (s == nil || s.File == "" || s.Cwd == "" || kept[id]) {
			delete(x.entries, id)
			delete(x.files, id)
			x.changed[id] = true
		}
	}
	var marks []func()
	for id, s := range d.Sessions {
		if s.File == "" || s.Cwd == "" || kept[id] {
			continue // not written yet, or unreadable (looked at again next time); or kept from Iridium
		}
		e := &entry{
			onDisk:  onDisk{id: id, agent: api.AgentDsh, path: x.mirrorPath(machine, id, s.File)},
			glimpse: glimpse{cwd: s.Cwd, title: s.Title, sub: s.Sub, parent: s.Parent},
			size:    s.Size, mtime: time.UnixMilli(max(s.Mtime, s.Last)),
			machine: machine, there: remote.Join(d.Sep, append(append([]string{d.Home}, dshhome.SessionsDir...), s.Project, id, s.File)...),
		}
		if o := x.entries[id]; o == nil || o.machine != machine || o.size != e.size || o.mtime != e.mtime || o.title != e.title || o.cwd != e.cwd || o.path != e.path || (known0 && !e.sub && !was0[id]) != x.loose(e) {
			x.changed[id] = true
		}
		x.entries[id] = e
		gone, wasGone := archived(d.Registry, id), archived(was, id)
		marks = append(marks, func() { x.remoteShown(id, s.Size, gone, wasGone) })
	}
	x.mu.Unlock()
	for _, f := range marks {
		f()
	}
}

// remoteShown sets or takes off a remote session's marker (see putRemote).
func (x *Index) remoteShown(id string, size int64, archived, wasArchived bool) {
	st := x.o.Store
	if st == nil {
		return
	}
	agent := string(api.AgentDsh)
	r, ok, err := st.ShownRow(agent, id)
	if err != nil {
		x.o.Logf("discover: %v", err)
		return
	}
	changed := false
	switch {
	case !ok && archived, ok && r.Shown && archived && !wasArchived, ok && r.Legacy:
		err = st.Hide(agent, id, size, 0)
		changed = !ok || r.Shown
	case !ok:
		changed, err = st.Show(agent, id)
	case !r.Shown && !archived && size > r.Size && time.Now().UnixMilli() >= r.QuietUntil:
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
	x.remoteMu.Lock()
	d := x.remotes[mc.ID()]
	x.remoteMu.Unlock()
	if d == nil {
		if err := x.ScanMachine(ctx, mc.ID()); err != nil {
			return nil, err
		}
		x.remoteMu.Lock()
		d = x.remotes[mc.ID()]
		x.remoteMu.Unlock()
		if d == nil {
			return []api.DshWorkspace{}, nil
		}
	}
	defer x.lockMachine(mc.ID())()
	x.remoteMu.Lock()
	d = x.remotes[mc.ID()]
	x.remoteMu.Unlock()
	x.scanRegistry(ctx, mc, d, true) // as it is now, its folders looked at
	out := []api.DshWorkspace{}
	if d.Registry != nil {
		for _, w := range d.Registry.Workspaces {
			if !d.Gone[w.ID] && !d.unsynced(w) {
				out = append(out, api.DshWorkspace{Title: cmp.Or(w.Title, remote.Base(d.Sep, w.Path)), Path: w.Path, UpdatedAt: w.UpdatedAt})
			}
		}
		return out, nil
	}
	latest := map[string]int64{}
	for _, s := range d.Sessions {
		if !s.Sub && s.Cwd != "" {
			latest[s.Cwd] = max(latest[s.Cwd], s.Mtime, s.Last)
		}
	}
	for cwd, at := range latest {
		if d.unsynced(dshhome.Workspace{Title: remote.Base(d.Sep, cwd), Path: cwd}) {
			continue
		}
		out = append(out, api.DshWorkspace{Title: remote.Base(d.Sep, cwd), Path: cwd, UpdatedAt: at})
	}
	slices.SortFunc(out, func(a, b api.DshWorkspace) int { return cmp.Compare(b.UpdatedAt, a.UpdatedAt) })
	return out, nil
}
