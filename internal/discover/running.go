package discover

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/kumoSleeping/iridium/internal/api"
)

// claudeReg is one of Claude's ~/.claude/sessions/<pid>.json: a live claude process, written
// as it starts and deleted as it exits; one left by a crash names a process that is gone.
type claudeReg struct {
	PID        int    `json:"pid"`
	SessionID  string `json:"sessionId"`
	Cwd        string `json:"cwd"`
	ProcStart  string `json:"procStart"`
	Name       string `json:"name"`
	NameSource string `json:"nameSource"`
	// Status is idle, busy or waiting (an ask, a dialog, a question open in its terminal);
	// WaitingFor says what for ("input needed", ...). Claude 2.1.318 writes them as they change.
	Status     string `json:"status"`
	WaitingFor string `json:"waitingFor"`
}

func (x *Index) claudeRegistry() []claudeReg {
	files, _ := filepath.Glob(filepath.Join(x.o.ClaudeDir, "sessions", "*.json"))
	var out []claudeReg
	for _, p := range files {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var c claudeReg
		if json.Unmarshal(b, &c) != nil || c.PID <= 0 || c.SessionID == "" {
			continue
		}
		out = append(out, c)
	}
	return out
}

// alive: the process c names runs, the same one (not a pid used again), and is the user's.
func (x *Index) alive(c claudeReg) bool {
	st, err := x.o.Procs.Stat(c.PID)
	if err != nil || st.UID != x.o.UID {
		return false
	}
	return c.ProcStart == "" || c.ProcStart == st.procStart()
}

// userNamed is the name the user gave a Claude session; "" for one Claude made up.
func userNamed(c claudeReg) string {
	switch c.NameSource {
	case "", "auto", "derived":
		return ""
	}
	return oneLine(c.Name)
}

// needsOf is what a Claude process waits on you for, "" if it does not: its registry's
// waitingFor, else a word for it.
func needsOf(c claudeReg) string {
	if c.Status != "waiting" {
		return ""
	}
	if w := oneLine(c.WaitingFor); w != "" {
		return w
	}
	return "input needed"
}

type proc struct {
	st       Stat
	agent    string
	sessions []string // the sessions it runs: Codex's app-server can run several
	name     string
	needs    string // Claude: what it waits on you for, as its registry says
}

// scanProcs looks at the user's processes: which run an agent, of which session, and which
// session started each.
func (x *Index) scanProcs() {
	reg := map[int]claudeReg{}
	for _, c := range x.claudeRegistry() {
		reg[c.PID] = c
	}
	pids, err := x.o.Procs.PIDs()
	if err != nil {
		x.o.Logf("discover: %v", err)
		return
	}
	all := map[int]*proc{}
	for _, pid := range pids {
		st, err := x.o.Procs.Stat(pid)
		if err != nil || st.UID != x.o.UID {
			continue
		}
		p := &proc{st: st}
		all[pid] = p
		if c, ok := reg[pid]; ok && (c.ProcStart == "" || c.ProcStart == st.procStart()) {
			p.agent, p.sessions, p.name, p.needs = "claude", []string{c.SessionID}, userNamed(c), needsOf(c)
			continue
		}
		argv := x.o.Procs.Cmdline(pid)
		if p.agent = agentOf(argv, x.o.Procs.Exe(pid)); p.agent != "" {
			p.sessions = x.sessionsOf(pid, p, argv)
		}
	}
	found := map[string]running{}
	for pid, p := range all {
		for _, s := range p.sessions {
			r := running{pid: pid, start: p.st.Start, agent: api.Agent(p.agent), name: p.name, needs: p.needs}
			env := launchEnv(x.o.Procs.Environ(pid))
			own := env["IRIDIUM_SESSION"]
			// A hosted Codex runs its thread under Iridium's id for the session.
			mine := own != "" && own != s && x.aliasOf(own) == s
			r.ours = own == s || mine
			for _, k := range launchVars {
				if v := env[k]; v != "" && v != s && !(mine && v == own) {
					r.launcher = v
					break
				}
			}
			// Else the first agent above it that runs one session, and not this one.
			for up, n := p.st.PPid, 0; r.launcher == "" && up > 1 && n < 64; n++ {
				a := all[up]
				if a == nil {
					break
				}
				if len(a.sessions) == 1 && a.sessions[0] != s {
					r.launcher = a.sessions[0]
				}
				up = a.st.PPid
			}
			if old, ok := found[s]; !ok || old.ours && !r.ours {
				found[s] = r // a process elsewhere wins over Iridium's own
			}
		}
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	for s, r := range found {
		if old, ok := x.running[s]; !ok || old != r {
			x.changed[s] = true
		}
		if _, ok := x.launchers[s]; !ok && r.launcher != "" {
			x.launchers[s] = r.launcher
		}
	}
	for s := range x.running {
		if _, ok := found[s]; !ok {
			x.changed[s] = true
		}
	}
	x.running = found
}

// sessionsOf is the sessions an agent process runs, by the files it has open, else its command
// line. pi has neither (it sets its process title and opens its file only to write a line): its
// session is the newest in its folder, written since it started.
func (x *Index) sessionsOf(pid int, p *proc, argv []string) []string {
	var out []string
	add := func(id string) {
		if id != "" && validID(id) && !contains(out, id) {
			out = append(out, id)
		}
	}
	switch p.agent {
	case "claude":
		add(flagValue(argv, "--resume", "-r", "--session-id"))
	case "codex":
		root := filepath.Join(x.o.CodexHome, "sessions") + "/"
		for _, f := range x.o.Procs.Files(pid) {
			if strings.HasPrefix(f, root) && strings.HasPrefix(filepath.Base(f), "rollout-") {
				add(idPattern.FindString(filepath.Base(f)))
			}
		}
		if len(out) == 0 {
			add(codexResumed(argv))
		}
	case "dsh":
		root := filepath.Join(x.o.DshHome, "sessions") + "/"
		for _, f := range x.o.Procs.Files(pid) {
			if rest, ok := strings.CutPrefix(f, root); ok {
				if parts := strings.Split(rest, "/"); len(parts) == 3 {
					add(parts[1])
				}
			}
		}
		if len(out) == 0 {
			add(flagValue(argv, "--session", "--resume"))
		}
	case "pi":
		root := x.o.PiSessions + "/"
		for _, f := range x.o.Procs.Files(pid) {
			if strings.HasPrefix(f, root) && strings.HasSuffix(f, ".jsonl") {
				if _, id, ok := strings.Cut(strings.TrimSuffix(filepath.Base(f), ".jsonl"), "_"); ok {
					add(id)
				}
			}
		}
		if len(out) == 0 {
			add(flagValue(argv, "--session-id", "--session"))
		}
		if len(out) == 0 {
			add(x.newestPi(x.o.Procs.Cwd(pid), p.st.Started))
		}
	}
	return out
}

// newestPi is the pi session of folder cwd last written, if written since the process started.
func (x *Index) newestPi(cwd string, since time.Time) string {
	if cwd == "" || since.IsZero() {
		return ""
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	best, at := "", since
	for id, e := range x.entries {
		if e.agent == api.AgentPi && e.cwd == cwd && e.mtime.After(at) {
			best, at = id, e.mtime
		}
	}
	return best
}

func contains(l []string, s string) bool {
	for _, v := range l {
		if v == s {
			return true
		}
	}
	return false
}

// locked: another process holds the flock on path (dsh's session.lock, Codex's writer lock).
// Tried and let go at once; a file that is not there is no lock.
func locked(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) {
		return true
	}
	if err == nil {
		_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
	}
	return false
}
