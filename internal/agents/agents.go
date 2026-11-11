// Package agents is the registry of the agents Iridium hosts, one row each (Rhodium src/agents.ts):
// its driver, its approval modes, what it can report, and where its transcripts are. What
// differs by agent outside its driver is read from here.
package agents

import (
	"cmp"
	"log"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/kumoSleeping/iridium/internal/agent"
	"github.com/kumoSleeping/iridium/internal/agent/claude"
	"github.com/kumoSleeping/iridium/internal/agent/codex"
	"github.com/kumoSleeping/iridium/internal/agent/dsh"
	"github.com/kumoSleeping/iridium/internal/agent/pi"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/transcript"
)

// MaxLine is the longest output line a driver reads; out.log keeps longer ones whole.
const MaxLine = 64 << 20

// Where says where to look for a session's transcript.
type Where struct {
	ID         string   // the agent's own id for the session
	Cwd        string   // where it was started
	Env        []string // the environment agents start from (CODEX_HOME, PI_CODING_AGENT_SESSION_DIR...)
	ClaudeHome string   // Claude's directory
}

// RemoteWhere says where to look for a session's transcript at an executor (docs/exec.md).
type RemoteWhere struct {
	ID, Cwd string
	Home    string   // the agent's home there (dsh: DSH_HOME; Claude: its config dir), "" if not said
	User    string   // the user's home there
	Env     []string // the environment the agent starts with there (RemoteEnv)
	Join    func(elem ...string) string
	// List names the files in dir there, Dirs its directories; a dir not there has none.
	List func(dir string) ([]string, error)
	Dirs func(dir string) ([]string, error)
}

// Info is one agent's row.
type Info struct {
	Agent api.Agent
	Name  string
	// Modes are its approval modes, as SetMode and SpawnOptions.Mode take them; DefaultMode is
	// the one an empty mode means: for every agent the one that approves by itself (Claude auto,
	// Codex auto, pi auto without the approval extension).
	Modes       []string
	DefaultMode string
	// ModeSwitch: when a mode set on a running agent takes effect: "now", "next turn", or
	// "restart" (SetMode fails; the agent takes it at its next start).
	ModeSwitch string
	// Efforts are the thinking efforts SetEffort and SpawnOptions.Effort take (a model may take
	// fewer; the driver says so).
	Efforts []string
	// ContextBreakdown: Context gives categories, not only a total. Quota: Quota gives quota
	// windows (an account's limits).
	ContextBreakdown bool
	Quota            bool
	// Ultracode: its sessions can turn ultracode on (Claude; the driver is an agent.Ultracoder).
	Ultracode bool
	// Holdable: a holder at an executor's machine may have its sessions (DSH Desktop, dsh's): the
	// session layer asks it there about them (held.*), its model, level and features.
	Holdable bool
	// ModelEfforts: its efforts are each model's (dsh: as its catalog says; pi: as each model
	// takes them); Efforts, if any, all it has.
	ModelEfforts bool
	// Presets: it takes an agent preset (SpawnOptions.Preset), at launch only (dsh's tools and
	// prompt); which, those it registers where it runs (session.Manager.Presets).
	Presets bool
	// OwnID: the agent makes the session's id itself (Codex's thread) and reports it with
	// Started; the session keeps it as the agent's id, beside its own.
	OwnID bool
	New   func(emit func(agent.Event), logf func(string, ...any)) agent.Driver
	// Find is the transcript of the session, "" if there is none yet.
	Find   func(w Where) (string, error)
	Format transcript.Format
	// At an executor (docs/exec.md) an agent needs no more than this row and a driver that
	// launches through SpawnOptions.Command, LaunchFile and PromptFile. FindRemote is Find there:
	// the transcript's path there; nil: the agent does not run at one. HomeEnv is the variable the
	// agent's home there goes in when the executor says one (dsh: DSH_HOME). MinRemote is the
	// oldest version that runs there ("": any).
	FindRemote func(w RemoteWhere) (string, error)
	HomeEnv    string
	MinRemote  string
	// HomeDir is its directory in the user's home (".claude"), HomeVars the variables that move it
	// elsewhere: what holds its logins and keys, never served nor offered (Homes).
	HomeDir  string
	HomeVars []string
	// SessionEnv are the variables it sets in the processes it starts, naming its session
	// (SessionVars).
	SessionEnv []string
}

// RemoteEnv is the environment the agent starts with at an executor: the executor's own, its
// home in HomeEnv.
func (i Info) RemoteEnv(env []string, home string) []string {
	env = slices.Clone(env)
	if i.HomeEnv != "" && home != "" {
		env = agent.SetEnv(env, i.HomeEnv, home)
	}
	return env
}

// RemoteVersionOK: version v of the agent may run at an executor (MinRemote; a dotted version's
// first three numbers, "0.2.2-rc.1" as 0.2.2).
func (i Info) RemoteVersionOK(v string) bool {
	if i.MinRemote == "" {
		return true
	}
	have, least := dotted(v), dotted(i.MinRemote)
	return slices.Compare(have[:], least[:]) >= 0
}

func dotted(v string) [3]int {
	var out [3]int
	v, _, _ = strings.Cut(v, "-")
	for i, p := range strings.SplitN(v, ".", 3) {
		out[i], _ = strconv.Atoi(p)
	}
	return out
}

var all = []Info{
	{
		Agent: api.AgentClaude, Name: "Claude",
		// claude --help (2.1.318); "default" is still taken as manual's old name. New sessions
		// start in auto (the user's choice, 2026-11-05): Claude decides what to ask.
		Modes:       []string{"manual", "acceptEdits", "plan", "auto", "dontAsk", "bypassPermissions"},
		DefaultMode: "auto", ModeSwitch: "now", ContextBreakdown: true, Quota: true, Ultracode: true,
		Efforts: []string{"low", "medium", "high", "xhigh", "max"},
		New: func(emit func(agent.Event), logf func(string, ...any)) agent.Driver {
			return claude.New(claude.Options{Emit: emit, Logf: logf})
		},
		Find: func(w Where) (string, error) { return transcript.Find(w.ClaudeHome, w.ID) },
		// At an executor (docs/exec.md), under the owner's own login there: the session's project
		// directory, by its cwd. One cut short by Claude (a path past 200) is not found.
		FindRemote: func(w RemoteWhere) (string, error) {
			if !transcript.ValidID(w.ID) {
				return "", nil
			}
			dir, ok := transcript.ClaudeProjectDir(w.Cwd)
			if !ok {
				return "", nil
			}
			home := w.Home
			if home == "" {
				home = w.Join(w.User, ".claude")
			}
			dir = w.Join(home, "projects", dir)
			names, err := w.List(dir)
			if err != nil || !slices.Contains(names, w.ID+".jsonl") {
				return "", err
			}
			return w.Join(dir, w.ID+".jsonl"), nil
		},
		Format:  transcript.Claude,
		HomeDir: ".claude", HomeVars: []string{"CLAUDE_CONFIG_DIR"}, SessionEnv: []string{"CLAUDE_CODE_SESSION_ID"},
	},
	{
		Agent: api.AgentCodex, Name: "Codex",
		Modes: codex.Modes, DefaultMode: codex.DefaultMode, ModeSwitch: "next turn", Quota: true, OwnID: true,
		Efforts: []string{"minimal", "low", "medium", "high", "xhigh"},
		New: func(emit func(agent.Event), logf func(string, ...any)) agent.Driver {
			return codex.New(codex.Options{Emit: emit, Logf: logf})
		},
		Find: func(w Where) (string, error) {
			if w.ID == "" {
				return "", nil
			}
			return transcript.FindCodex(transcript.CodexHome(w.Env), w.ID)
		},
		Format: transcript.Codex,
		// Where CodexHome looks, at the executor: CODEX_HOME there, else ~/.codex there.
		HomeEnv: "CODEX_HOME", FindRemote: findCodexRemote,
		HomeDir: ".codex", HomeVars: []string{"CODEX_HOME"}, SessionEnv: []string{"CODEX_THREAD_ID", "CODEX_SESSION_ID"},
	},
	{
		Agent: api.AgentPi, Name: "pi",
		Modes: pi.Modes, DefaultMode: pi.DefaultMode, ModeSwitch: "restart", Efforts: pi.Levels, ModelEfforts: true,
		New: func(emit func(agent.Event), logf func(string, ...any)) agent.Driver {
			return pi.New(pi.Options{Emit: emit, Logf: logf})
		},
		Find:   func(w Where) (string, error) { return transcript.FindPi(transcript.PiSessions(w.Env, w.Cwd), w.ID) },
		Format: transcript.Pi,
		// Where PiSessions looks, at the executor: its variables, else ~/.pi/agent there.
		HomeEnv: "PI_CODING_AGENT_DIR",
		HomeDir: ".pi", HomeVars: []string{"PI_CODING_AGENT_DIR", "PI_CODING_AGENT_SESSION_DIR"}, SessionEnv: []string{"PI_SESSION_ID"},
		FindRemote: func(w RemoteWhere) (string, error) {
			if !transcript.ValidID(w.ID) {
				return "", nil
			}
			dir := agent.GetEnv(w.Env, "PI_CODING_AGENT_SESSION_DIR")
			if dir == "" {
				root := cmp.Or(agent.GetEnv(w.Env, "PI_CODING_AGENT_DIR"), w.Join(w.User, ".pi", "agent"))
				dir = w.Join(root, "sessions", transcript.PiFolder(w.Cwd))
			}
			names, err := w.List(dir)
			for _, n := range names {
				if strings.HasSuffix(n, "_"+w.ID+".jsonl") {
					return w.Join(dir, n), nil
				}
			}
			return "", err
		},
	},
	{
		// dsh --profile sdk (0.2.2-rc.1): mode at once (/permission, /plan); model, effort and
		// preset only at launch (the driver says agent.ErrRestart, the host restarts it).
		Agent: api.AgentDsh, Name: "DeepSeek Harness",
		Modes: dsh.Modes, DefaultMode: dsh.DefaultMode, ModeSwitch: "now", ModelEfforts: true,
		Presets: true, Holdable: true,
		New: func(emit func(agent.Event), logf func(string, ...any)) agent.Driver {
			return dsh.New(dsh.Options{Emit: emit, Logf: logf})
		},
		Find:   func(w Where) (string, error) { return transcript.FindDsh(transcript.DshHome(w.Env), w.ID) },
		Format: transcript.Dsh,
		// The first with the sdk profile.
		HomeEnv: "DSH_HOME", MinRemote: "0.2.2",
		HomeDir: ".dsh", HomeVars: []string{"DSH_HOME"}, SessionEnv: []string{"DSH_SESSION_ID"},
		FindRemote: func(w RemoteWhere) (string, error) {
			home := cmp.Or(agent.GetEnv(w.Env, "DSH_HOME"), w.Join(w.User, ".dsh"))
			dir := w.Join(home, "sessions", transcript.DshProjectDir(w.Cwd), w.ID)
			names, err := w.List(dir)
			if err != nil {
				return "", err
			}
			if best, _ := transcript.DshBest(names); best != "" {
				return w.Join(dir, best), nil
			}
			return "", nil
		},
	},
}

// codexDayDirs bounds the walk for a thread whose id has no time in it: the newest day folders.
const codexDayDirs = 400

// findCodexRemote is Codex's FindRemote: sessions/YYYY/MM/DD/rollout-<local time>-<thread>.jsonl,
// else archived_sessions/. A thread id is a UUIDv7, the folder its time's date where Codex runs:
// local there, within a day of the UTC date at any offset (UTC-12 to UTC+14), so three folders
// are listed, not the whole history. An id with no time (an older Codex): the newest
// codexDayDirs day folders, newest first. "" before the first turn (no thread, or no rollout).
func findCodexRemote(w RemoteWhere) (string, error) {
	if !transcript.ValidID(w.ID) {
		return "", nil
	}
	home := cmp.Or(w.Home, agent.GetEnv(w.Env, "CODEX_HOME"), w.Join(w.User, ".codex"))
	in := func(dir string) (string, error) {
		names, err := w.List(dir)
		for _, n := range names {
			if strings.HasPrefix(n, "rollout-") && strings.HasSuffix(n, "-"+w.ID+".jsonl") {
				return w.Join(dir, n), nil
			}
		}
		return "", err
	}
	sessions := w.Join(home, "sessions")
	day := func(t time.Time) string {
		return w.Join(sessions, t.Format("2006"), t.Format("01"), t.Format("02"))
	}
	if t, ok := transcript.CodexThreadTime(w.ID); ok {
		t = t.UTC()
		for _, d := range []time.Time{t, t.AddDate(0, 0, -1), t.AddDate(0, 0, 1)} {
			if p, err := in(day(d)); p != "" || err != nil {
				return p, err
			}
		}
	} else if w.Dirs != nil {
		// Newest first: years, months, days, each a number's folder.
		newest := func(dir string) ([]string, error) {
			names, err := w.Dirs(dir)
			names = slices.DeleteFunc(names, func(n string) bool { _, err := strconv.Atoi(n); return err != nil })
			slices.Sort(names)
			slices.Reverse(names)
			return names, err
		}
		left := codexDayDirs
		years, err := newest(sessions)
		if err != nil {
			return "", err
		}
	walk:
		for _, y := range years {
			months, err := newest(w.Join(sessions, y))
			if err != nil {
				return "", err
			}
			for _, m := range months {
				days, err := newest(w.Join(sessions, y, m))
				if err != nil {
					return "", err
				}
				for _, d := range days {
					if left == 0 {
						break walk
					}
					left--
					if p, err := in(w.Join(sessions, y, m, d)); p != "" || err != nil {
						return p, err
					}
				}
			}
		}
	}
	return in(w.Join(home, "archived_sessions"))
}

// Get is agent a's row.
func Get(a api.Agent) (Info, bool) {
	i := slices.IndexFunc(all, func(x Info) bool { return x.Agent == a })
	if i < 0 {
		return Info{}, false
	}
	return all[i], true
}

// Homes are the agents' own directories (Info.HomeDir under home, and where getenv says their
// HomeVars move them): never served nor offered, they hold logins and keys.
func Homes(home string, getenv func(string) string) []string {
	var out []string
	for _, i := range all {
		if home != "" && i.HomeDir != "" {
			out = append(out, filepath.Join(home, i.HomeDir))
		}
		for _, v := range i.HomeVars {
			if p := getenv(v); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

// SessionVars are the variables naming the session a process was started from, in the order
// they are believed (docs/api.md): Iridium's own first (it sets it for every agent it hosts),
// then each agent's (Info.SessionEnv), in sessionVarOrder.
func SessionVars() []string {
	out := []string{"IRIDIUM_SESSION"}
	for _, a := range sessionVarOrder {
		if i, ok := Get(a); ok {
			out = append(out, i.SessionEnv...)
		}
	}
	return out
}

// sessionVarOrder is the order agents' session variables are believed in: dsh's before pi's (a
// shell of dsh's model inside pi's names the nearer one).
var sessionVarOrder = []api.Agent{api.AgentClaude, api.AgentCodex, api.AgentDsh, api.AgentPi}

// All is every agent's row, Claude first.
func All() []Info { return slices.Clone(all) }

// Describe is the rows as the API gives them.
func Describe() []api.AgentInfo {
	out := make([]api.AgentInfo, 0, len(all))
	for _, i := range all {
		out = append(out, api.AgentInfo{Agent: i.Agent, Name: i.Name, Modes: slices.Clone(i.Modes), DefaultMode: i.DefaultMode, ModeSwitch: i.ModeSwitch, ContextBreakdown: i.ContextBreakdown, Quota: i.Quota, Ultracode: i.Ultracode, Presets: i.Presets})
	}
	return out
}

// NewDriver is agent a's driver; Claude's for one not registered.
func NewDriver(a api.Agent, emit func(agent.Event), logf func(string, ...any)) agent.Driver {
	if logf == nil {
		logf = log.Printf
	}
	i, ok := Get(a)
	if !ok {
		i, _ = Get(api.AgentClaude)
	}
	return i.New(emit, logf)
}
