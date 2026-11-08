// Package agents is the registry of the agents Iridium hosts, one row each (Rhodium src/agents.ts):
// its driver, its approval modes, what it can report, and where its transcripts are. What
// differs by agent outside its driver is read from here.
package agents

import (
	"log"
	"slices"

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
	Home    string // the agent's home there (dsh: DSH_HOME; Claude: its config dir), "" if not said
	User    string // the user's home there
	Join    func(elem ...string) string
	// List names the files in dir there; a dir not there has none.
	List func(dir string) ([]string, error)
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
	// Presets are the agent presets SpawnOptions.Preset takes, at launch only (dsh's tools and
	// prompt); DefaultPreset is an empty preset's.
	Presets       []string
	DefaultPreset string
	// OwnID: the agent makes the session's id itself (Codex's thread) and reports it with
	// Started; the session keeps it as the agent's id, beside its own.
	OwnID bool
	New   func(emit func(agent.Event), logf func(string, ...any)) agent.Driver
	// Find is the transcript of the session, "" if there is none yet.
	Find   func(w Where) (string, error)
	Format transcript.Format
	// FindRemote is Find at an executor: the transcript's path there. nil: the agent does not run
	// at one.
	FindRemote func(w RemoteWhere) (string, error)
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
		Format: transcript.Claude,
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
	},
	{
		Agent: api.AgentPi, Name: "pi",
		Modes: pi.Modes, DefaultMode: pi.DefaultMode, ModeSwitch: "restart", Efforts: pi.Levels,
		New: func(emit func(agent.Event), logf func(string, ...any)) agent.Driver {
			return pi.New(pi.Options{Emit: emit, Logf: logf})
		},
		Find:   func(w Where) (string, error) { return transcript.FindPi(transcript.PiSessions(w.Env, w.Cwd), w.ID) },
		Format: transcript.Pi,
	},
	{
		// dsh --profile sdk (0.2.2-rc.1): mode at once (/permission, /plan); model, effort and
		// preset only at launch (the driver says agent.ErrRestart, the host restarts it).
		Agent: api.AgentDsh, Name: "DeepSeek Harness",
		Modes: dsh.Modes, DefaultMode: dsh.DefaultMode, ModeSwitch: "now", Efforts: dsh.Efforts,
		Presets: dsh.Presets, DefaultPreset: dsh.DefaultPreset,
		New: func(emit func(agent.Event), logf func(string, ...any)) agent.Driver {
			return dsh.New(dsh.Options{Emit: emit, Logf: logf})
		},
		Find:   func(w Where) (string, error) { return transcript.FindDsh(transcript.DshHome(w.Env), w.ID) },
		Format: transcript.Dsh,
		FindRemote: func(w RemoteWhere) (string, error) {
			dir := w.Join(w.Home, "sessions", transcript.DshProjectDir(w.Cwd), w.ID)
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

// Get is agent a's row.
func Get(a api.Agent) (Info, bool) {
	i := slices.IndexFunc(all, func(x Info) bool { return x.Agent == a })
	if i < 0 {
		return Info{}, false
	}
	return all[i], true
}

// All is every agent's row, Claude first.
func All() []Info { return slices.Clone(all) }

// Describe is the rows as the API gives them.
func Describe() []api.AgentInfo {
	out := make([]api.AgentInfo, 0, len(all))
	for _, i := range all {
		out = append(out, api.AgentInfo{Agent: i.Agent, Name: i.Name, Modes: slices.Clone(i.Modes), DefaultMode: i.DefaultMode, ModeSwitch: i.ModeSwitch, ContextBreakdown: i.ContextBreakdown, Quota: i.Quota, Ultracode: i.Ultracode, Presets: slices.Clone(i.Presets), DefaultPreset: i.DefaultPreset})
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
