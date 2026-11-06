// Package access is the interactive terminals' permission table (plan "交互终端"): what each
// kind gets by default, what counts as widening one, and the checks the API makes with it.
package access

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kumoSleeping/iridium/internal/api"
)

// Denied is what the permission table refuses; the server answers 403 (429 for ErrCap).
type Denied struct{ Reason string }

func (d *Denied) Error() string { return d.Reason }

// IsDenied reports whether err is (or wraps) a Denied.
func IsDenied(err error) bool {
	var d *Denied
	return errors.As(err, &d)
}

// Errors the checks give.
var (
	ErrAgent       = &Denied{"this terminal may not use that agent"}
	ErrDir         = &Denied{"this terminal may not use that directory"}
	ErrCommand     = &Denied{"this terminal may not send that command"}
	ErrMustApprove = &Denied{"this terminal must run with approvals"}
	ErrAnswer      = &Denied{"this terminal may not answer questions or approvals"}
	ErrFilesIn     = &Denied{"this terminal may not copy files in"}
	ErrFilesOut    = &Denied{"this terminal may not copy files out"}
	ErrCap         = &Denied{"this terminal used its tokens for today"}
	ErrOwner       = &Denied{"claude is only for the owner's own terminals, and only a person at the machine or on a panel that has Claude turns it on (ir access <name> --claude on)"}
	ErrWiden       = &Denied{"only a web panel may widen a terminal's permissions"}
	ErrManage      = &Denied{"only a web panel or 本机 manages this"}
	ErrLocal       = errors.New("本机's permissions are all of them and do not change")
)

// Full is 本机's table: everything. 本机 is the owner's machine, so Claude needs no confirmation.
func Full() api.Rules {
	return api.Rules{
		Agents:      []api.Agent{api.AgentClaude, api.AgentCodex, api.AgentDsh, api.AgentPi},
		ClaudeOwner: &api.OwnerConfirm{By: api.LocalTerminal},
		AllDirs:     true, SeeOthers: true, FilesIn: true, FilesOut: true, Answer: true, AllCommands: true, SecretsSet: true,
		Dirs: []string{}, Workspaces: []int64{}, Commands: []string{},
	}
}

// Default is a new terminal's table. Claude is off: only the first panel, an ir invite --owner or
// ir token --owner, or ir access --claude on, each from a person at the machine or on a panel that
// has Claude (an agent's only once such a panel confirms it), give it (WithClaude). A channel
// (a plugin or a peer) sees only what it opened, runs with approvals and stores no credentials.
func Default(kind api.TerminalKind) api.Rules {
	r := Full()
	r.Agents, r.ClaudeOwner = []api.Agent{api.AgentCodex, api.AgentDsh, api.AgentPi}, nil
	if kind == api.TerminalChannel {
		r.SeeOthers = false
		r.MustApprove = true
		r.SecretsSet = false
	}
	return r
}

// Ways a terminal was found the owner's own (api.OwnerConfirm.By).
const (
	ByLocal = api.LocalTerminal // a person's ir on the machine
	ByFirst = "first"           // the first panel registered
	ByPanel = "panel"           // a person on a panel that has Claude (or that confirmed an agent's ask)
)

// HasClaude reports whether r gives its terminal Claude.
func HasClaude(r api.Rules) bool {
	return r.ClaudeOwner != nil && slices.Contains(r.Agents, api.AgentClaude)
}

// WithClaude is r with Claude on, found the owner's own by (ByLocal, ByFirst, ByPanel) at at (ms). One that
// has it already keeps how it got it.
func WithClaude(r api.Rules, by string, at int64) api.Rules {
	if HasClaude(r) {
		return r
	}
	r.Agents = append(slices.Clone(r.Agents), api.AgentClaude)
	r.ClaudeOwner = &api.OwnerConfirm{By: by, At: at}
	return Clean(r)
}

// WithoutClaude is r with Claude off.
func WithoutClaude(r api.Rules) api.Rules {
	r.Agents = slices.DeleteFunc(slices.Clone(r.Agents), func(a api.Agent) bool { return a == api.AgentClaude })
	r.ClaudeOwner = nil
	return Clean(r)
}

// Clean is r with its lists made non-nil, sorted and without repeats, and its directories clean.
// Claude stays only with ClaudeOwner, and ClaudeOwner only with Claude.
func Clean(r api.Rules) api.Rules {
	agents := []api.Agent{}
	for _, a := range r.Agents {
		if a == api.AgentClaude && r.ClaudeOwner == nil {
			continue
		}
		if !slices.Contains(agents, a) {
			agents = append(agents, a)
		}
	}
	slices.Sort(agents)
	r.Agents = agents
	dirs := []string{}
	for _, d := range r.Dirs {
		if d = strings.TrimSpace(d); d != "" {
			d = filepath.Clean(d)
			if !slices.Contains(dirs, d) {
				dirs = append(dirs, d)
			}
		}
	}
	slices.Sort(dirs)
	r.Dirs = dirs
	ws := []int64{}
	for _, w := range r.Workspaces {
		if !slices.Contains(ws, w) {
			ws = append(ws, w)
		}
	}
	slices.Sort(ws)
	r.Workspaces = ws
	cmds := []string{}
	for _, c := range r.Commands {
		c = strings.TrimSpace(c)
		if c != "" && !strings.HasPrefix(c, "/") {
			c = "/" + c
		}
		if c != "" && !slices.Contains(cmds, c) {
			cmds = append(cmds, c)
		}
	}
	slices.Sort(cmds)
	r.Commands = cmds
	if r.DailyTokens < 0 {
		r.DailyTokens = 0
	}
	if !slices.Contains(r.Agents, api.AgentClaude) {
		r.ClaudeOwner = nil
	}
	return r
}

// Validate says what is wrong with r, if anything.
func Validate(r api.Rules) error {
	for _, a := range r.Agents {
		switch a {
		case api.AgentClaude, api.AgentCodex, api.AgentPi, api.AgentDsh:
		default:
			return errors.New("unknown agent " + string(a))
		}
	}
	for _, d := range r.Dirs {
		if !filepath.IsAbs(d) {
			return errors.New("not an absolute directory: " + d)
		}
	}
	for _, c := range r.Commands {
		if strings.ContainsAny(c, " \t\n") {
			return errors.New("a command is one word: " + c)
		}
	}
	return nil
}

// Widens reports whether next allows anything old did not, and so needs a panel.
func Widens(old, next api.Rules) bool {
	for _, a := range next.Agents {
		if !slices.Contains(old.Agents, a) {
			return true
		}
	}
	if next.AllDirs && !old.AllDirs {
		return true
	}
	if !next.AllDirs {
		for _, d := range next.Dirs {
			if !old.AllDirs && !under(d, old.Dirs) {
				return true
			}
		}
		for _, w := range next.Workspaces {
			if !old.AllDirs && !slices.Contains(old.Workspaces, w) {
				return true
			}
		}
	}
	if (next.SeeOthers && !old.SeeOthers) || (next.FilesIn && !old.FilesIn) || (next.FilesOut && !old.FilesOut) ||
		(next.Answer && !old.Answer) || (!next.MustApprove && old.MustApprove) || (next.SecretsSet && !old.SecretsSet) {
		return true
	}
	if old.DailyTokens > 0 && (next.DailyTokens == 0 || next.DailyTokens > old.DailyTokens) {
		return true
	}
	if next.AllCommands && !old.AllCommands {
		return true
	}
	if !next.AllCommands && !old.AllCommands {
		for _, c := range next.Commands {
			if !slices.Contains(old.Commands, c) {
				return true
			}
		}
	}
	return false
}

// under reports whether dir is one of dirs or below one.
func under(dir string, dirs []string) bool {
	for _, d := range dirs {
		if rel, err := filepath.Rel(d, dir); err == nil && rel != ".." && !strings.HasPrefix(rel, "../") {
			return true
		}
	}
	return false
}

// Agent checks that r lets its terminal use agent a (claude also needs the owner's confirmation).
func Agent(r api.Rules, a api.Agent) error {
	if a == "" {
		a = api.AgentClaude
	}
	if !slices.Contains(r.Agents, a) || (a == api.AgentClaude && r.ClaudeOwner == nil) {
		return ErrAgent
	}
	return nil
}

// Dir checks that r lets its terminal work in cwd. workspace is the session's (0 none); paths
// are the workspaces' paths, by ID, for a new session that has none yet.
func Dir(r api.Rules, cwd string, workspace int64, paths map[int64]string) error {
	if r.AllDirs {
		return nil
	}
	if workspace != 0 && slices.Contains(r.Workspaces, workspace) {
		return nil
	}
	if cwd != "" {
		cwd = filepath.Clean(cwd)
		if under(cwd, r.Dirs) {
			return nil
		}
		for _, w := range r.Workspaces {
			if p := paths[w]; p != "" && under(cwd, []string{p}) {
				return nil
			}
		}
	}
	return ErrDir
}

// Command checks a message: a slash command must be one r allows.
func Command(r api.Rules, text string) error {
	t := strings.TrimSpace(text)
	if !strings.HasPrefix(t, "/") || r.AllCommands {
		return nil
	}
	word, _, _ := strings.Cut(t, " ")
	word, _, _ = strings.Cut(word, "\n")
	if slices.Contains(r.Commands, word) {
		return nil
	}
	return ErrCommand
}

// skipping are the approval modes that let tools run without asking anyone: Claude's
// bypassPermissions, acceptEdits and auto, Codex's auto (its policy never) and on-request (the model decides,
// in a full-access sandbox), pi's auto, dsh's danger-full-access.
var skipping = []string{"bypassPermissions", "acceptEdits", "auto", "never", "on-request", "danger-full-access"}

// SkipsApproval reports whether mode lets tools run unasked.
func SkipsApproval(mode string) bool { return slices.Contains(skipping, mode) }

// ApprovingMode is the mode a must-approve terminal's new session of agent a starts in, when it
// names none: one that asks, whatever the agent's own default is. "" leaves the agent's.
func ApprovingMode(a api.Agent) string {
	switch a {
	case api.AgentClaude, "":
		return "manual"
	case api.AgentCodex:
		return "untrusted"
	case api.AgentPi:
		return "ask"
	}
	return ""
}

// Mode checks the approval mode a must-approve terminal would run a session in.
func Mode(r api.Rules, mode string) error {
	if r.MustApprove && SkipsApproval(mode) {
		return ErrMustApprove
	}
	return nil
}

// Visible reports whether r lets its terminal (id) see a session of agent a in cwd and workspace,
// opened by opener ("" when not through Iridium: 本机's).
func Visible(r api.Rules, id string, a api.Agent, cwd string, workspace int64, opener string) bool {
	if opener == "" {
		opener = api.LocalTerminal
	}
	if !r.SeeOthers && opener != id {
		return false
	}
	if Agent(r, a) != nil {
		return false
	}
	return Dir(r, cwd, workspace, nil) == nil
}
