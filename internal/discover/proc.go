package discover

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Procs is what discovery reads of the processes: /proc on Linux, a fake in tests.
type Procs interface {
	PIDs() ([]int, error)
	Stat(pid int) (Stat, error)
	Cmdline(pid int) []string
	Exe(pid int) string
	Cwd(pid int) string
	// Environ is the process's environment. Only ever asked of the user's own processes.
	Environ(pid int) []string
	// Files are the paths of the files it has open.
	Files(pid int) []string
}

// Stat: the parent, the start time in clock ticks since boot (/proc/<pid>/stat's 22nd field,
// which Claude's sessions/<pid>.json records as procStart) and as a time, and the owner.
type Stat struct {
	PPid    int
	Start   uint64
	Started time.Time
	UID     int
}

// ProcFS is the real /proc (Root "" is /proc).
type ProcFS struct{ Root string }

func (p ProcFS) path(pid int, name string) string {
	root := p.Root
	if root == "" {
		root = "/proc"
	}
	return filepath.Join(root, strconv.Itoa(pid), name)
}

func (p ProcFS) PIDs() ([]int, error) {
	root := p.Root
	if root == "" {
		root = "/proc"
	}
	ents, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	out := make([]int, 0, len(ents))
	for _, e := range ents {
		if n, err := strconv.Atoi(e.Name()); err == nil && n > 0 {
			out = append(out, n)
		}
	}
	return out, nil
}

func (p ProcFS) Stat(pid int) (Stat, error) {
	var s Stat
	dir, err := os.Stat(filepath.Dir(p.path(pid, "stat")))
	if err != nil {
		return s, err
	}
	if st, ok := dir.Sys().(*syscall.Stat_t); ok {
		s.UID = int(st.Uid)
	}
	b, err := os.ReadFile(p.path(pid, "stat"))
	if err != nil {
		return s, err
	}
	// pid (comm) state ppid ...: comm may hold spaces and parentheses, so from the last ")".
	i := bytes.LastIndexByte(b, ')')
	if i < 0 {
		return s, errors.New("bad stat")
	}
	f := strings.Fields(string(b[i+1:]))
	if len(f) < 20 {
		return s, errors.New("bad stat")
	}
	s.PPid, _ = strconv.Atoi(f[1])
	s.Start, _ = strconv.ParseUint(f[19], 10, 64)
	// Clock ticks are 100 a second on every Linux Go runs on (USER_HZ).
	s.Started = p.boot().Add(time.Duration(s.Start) * 10 * time.Millisecond)
	return s, nil
}

var (
	bootOnce sync.Once
	bootAt   time.Time
)

// boot is when the machine started: /proc/stat's btime.
func (p ProcFS) boot() time.Time {
	bootOnce.Do(func() {
		root := p.Root
		if root == "" {
			root = "/proc"
		}
		b, _ := os.ReadFile(filepath.Join(root, "stat"))
		for _, l := range strings.Split(string(b), "\n") {
			if v, ok := strings.CutPrefix(l, "btime "); ok {
				n, _ := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
				bootAt = time.Unix(n, 0)
			}
		}
	})
	return bootAt
}

func (p ProcFS) Cmdline(pid int) []string {
	b, _ := os.ReadFile(p.path(pid, "cmdline"))
	b = bytes.TrimRight(b, "\x00")
	if len(b) == 0 {
		return nil
	}
	return strings.Split(string(b), "\x00")
}

func (p ProcFS) Exe(pid int) string {
	s, _ := os.Readlink(p.path(pid, "exe"))
	return s
}

func (p ProcFS) Cwd(pid int) string {
	s, _ := os.Readlink(p.path(pid, "cwd"))
	return s
}

func (p ProcFS) Environ(pid int) []string {
	b, _ := os.ReadFile(p.path(pid, "environ"))
	b = bytes.TrimRight(b, "\x00")
	if len(b) == 0 {
		return nil
	}
	return strings.Split(string(b), "\x00")
}

func (p ProcFS) Files(pid int) []string {
	dir := p.path(pid, "fd")
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		if t, err := os.Readlink(filepath.Join(dir, e.Name())); err == nil && strings.HasPrefix(t, "/") {
			out = append(out, t)
		}
	}
	return out
}

// launchVars are the variables that name the session a process was started from, in the order
// they are believed: Iridium's own first (it sets it for every agent it hosts), then the agents'.
// Only these are ever taken from an environment, and their values are never logged.
var launchVars = []string{"IRIDIUM_SESSION", "CLAUDE_CODE_SESSION_ID", "CODEX_THREAD_ID", "CODEX_SESSION_ID", "DSH_SESSION_ID", "PI_SESSION_ID"}

// launchEnv picks launchVars out of env.
func launchEnv(env []string) map[string]string {
	out := map[string]string{}
	for _, kv := range env {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || v == "" {
			continue
		}
		for _, want := range launchVars {
			if k == want {
				out[k] = v
			}
		}
	}
	return out
}

// agentOf is the kind of agent a process runs, by its command line or program; "" if none. pi
// sets its process title to "pi", Claude's background hosts to "claude bg-…", so the first word
// of the first argument counts; a script under node or bun counts by the script's name.
func agentOf(argv []string, exe string) string {
	names := []string{filepath.Base(exe)}
	if len(argv) > 0 {
		first, _, _ := strings.Cut(strings.TrimSpace(argv[0]), " ")
		names = append(names, filepath.Base(first))
		switch filepath.Base(first) {
		case "node", "bun", "deno":
			for _, a := range argv[1:] {
				if !strings.HasPrefix(a, "-") {
					names = append(names, strings.TrimSuffix(filepath.Base(a), ".js"))
					break
				}
			}
		}
	}
	for _, n := range names {
		switch n {
		case "claude":
			return "claude"
		case "codex":
			return "codex"
		case "pi":
			return "pi"
		case "dsh":
			return "dsh"
		}
	}
	if strings.Contains(exe, "/claude/versions/") {
		return "claude"
	}
	return ""
}

// flagValue is what follows one of the flags in argv ("--resume id" or "--resume=id").
func flagValue(argv []string, flags ...string) string {
	for i, a := range argv {
		for _, f := range flags {
			if a == f && i+1 < len(argv) {
				return argv[i+1]
			}
			if v, ok := strings.CutPrefix(a, f+"="); ok {
				return v
			}
		}
	}
	return ""
}

// codexResumed is the thread of `codex resume <id>`.
func codexResumed(argv []string) string {
	for i, a := range argv {
		if a == "resume" && i+1 < len(argv) && validID(argv[i+1]) {
			return argv[i+1]
		}
	}
	return ""
}
