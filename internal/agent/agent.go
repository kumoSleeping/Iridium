// Package agent is the agent-agnostic face of a driver (Rhodium §6.1, trimmed to the plan): the
// operations the session host calls and the events a driver reports. A driver speaks one agent's
// protocol over the holder's files and knows nothing of sessions, stores or pages.
package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/hold"
)

// State is where the agent's turn is. A turn begins when it leaves idle and ends when it is back.
type State string

const (
	Idle       State = "idle"
	Running    State = "running"
	Waiting    State = "waiting" // on an ask
	Compacting State = "compacting"
)

// Message is one message for the agent. ID is what the agent's record names it by, where it
// keeps one (Claude: the user frame's uuid), so the host can tell its own inputs in transcripts.
// Human: from a person, not from another agent.
type Message struct {
	ID    string
	Text  string
	Human bool
	// Images go with the text to an agent that takes them (Claude); the text names their paths.
	Images []Image
}

// Image is an image the agent sees: its bytes within the agent's limits, and their media type
// (image/png, image/jpeg, image/gif, image/webp).
type Image struct {
	MediaType string
	Data      []byte
}

// SpawnOptions say how to launch the agent for a session.
type SpawnOptions struct {
	Session string // the session's id
	Resume  bool   // the agent has the session on record: resume it, else start it under this id
	// AgentSession is the agent's own id for the session where the agent makes it (Codex's
	// thread, which Started reports); empty: none yet.
	AgentSession string
	Cwd          string
	Env          []string // the environment to start from; the driver drops what a parent session hands down
	Program      string   // absolute, or a bare name looked up on Env's PATH
	// SystemPromptFile is appended to the agent's system prompt, re-read at every resume.
	SystemPromptFile string
	Model            string
	Mode             string // the approval mode; empty: the agent's default
	Effort           string // the thinking effort; empty: the agent's default
	Ultracode        *bool  // Claude's ultracode; nil: never set, the user's own settings hold
	Preset           string // the agent preset (dsh's tools and prompt); empty: the agent's default
	// CrashResume: the last process died mid-turn; an agent that can run that turn again by itself
	// is told to.
	CrashResume bool
	// Remote: the agent runs at an executor (docs/exec.md), not here: Cwd and Env are its, and
	// nothing of the agent is looked up on this machine. nil: here.
	Remote *Remote
}

// Remote is the executor an agent runs at, as the driver needs it.
type Remote struct {
	Argv    []string // the agent's program part, as the executor gave it
	Version string
	Scope   string // dsh: the directory of its @deepseek-ai packages there
	Scratch string // a directory there for launch files
	Join    func(elem ...string) string
	// ReadFile and WriteFile read and write a file there (small ones).
	ReadFile  func(path string) ([]byte, error)
	WriteFile func(path string, data []byte) error
}

// Writer writes one line to the agent's stdin (through the holder's pipe).
type Writer func(line []byte) error

// Driver is one session's connection to its agent processes, one at a time. Feed is called from
// one goroutine; the rest may be called from any.
type Driver interface {
	Spawn(o SpawnOptions) (hold.Spec, error)
	// Attach connects the driver to agent process proc (up by one each start) and does the
	// handshake. replay: the process was started by an earlier daemon, whose output is fed again
	// with Feed(…, true) first.
	Attach(w Writer, proc int)
	// Feed is one whole line of the attached process's output. replay: an earlier daemon saw it;
	// it only brings state back and is never answered.
	Feed(line []byte, replay bool)
	// Detach: the process ended. Requests waiting on it fail.
	Detach()
	Send(ctx context.Context, m Message) error
	Interrupt(ctx context.Context) error
	Answer(ctx context.Context, a api.Answer) error
	SetModel(ctx context.Context, model string) error
	SetMode(ctx context.Context, mode string) error
	Context(ctx context.Context) (*api.Context, error)
	Quota(ctx context.Context) (*Usage, error)
	State() State
}

// Balancer is a driver whose account has money rather than quota windows (dsh: the DeepSeek
// account's balance).
type Balancer interface {
	Balance(ctx context.Context) ([]api.Balance, error)
}

// Event is what a driver reports, through the emit func it was made with. Emit is never called
// with the driver's lock held, but must not block for long.
type Event interface{ event() }

// AutoDenied begins the tool result Claude writes for a call its auto mode classifier denied: the
// CLI answers the model itself, nothing is asked (session/release.go).
const AutoDenied = "Permission for this action was denied by the Claude Code auto mode classifier"

type (
	// TurnStart: the agent left idle. Input: the message that began the turn, if the driver can tell.
	TurnStart struct{ Input string }
	// TurnEnd: back to idle. Reply: its last assistant message. Background: tasks still run, and
	// one ending wakes the agent for another turn. CompactOnly: the turn only compacted.
	TurnEnd struct {
		Reply       string
		Background  bool
		CompactOnly bool
	}
	StateChange struct{ State State }
	// TextDelta is text the agent is writing; MessageDone seals the draft with the whole message.
	TextDelta   struct{ Text string }
	MessageDone struct{ Text string }
	// ToolDraft: the agent began to write tool call ID (Name); ToolDraftDelta is its input's JSON
	// as it comes, in pieces. ToolStart follows with the whole call.
	ToolDraft      struct{ ID, Name string }
	ToolDraftDelta struct{ JSON string }
	ToolStart      struct {
		ID, Name string
		Input    any
	}
	// ToolDone: call ID has its result. Denied: Claude's auto mode classifier turned it down (the
	// result is AutoDenied's), its reason as the classifier gave it, or "denied" for none.
	ToolDone    struct{ ID, Name, Denied string }
	AskOpen     struct{ Ask api.Ask }
	AskCancel   struct{ RequestID string }
	PendingAsks struct {
		Asks  []api.Ask
		Known bool // false: the agent cannot say, so any ask not answered yet is stale
	}
	Tasks struct{ Tasks []api.Task }
	// Taken: the agent took the input into its conversation (or turned it down for good). Runs:
	// it runs on its own within the turn, after the last one's result (Claude's message queued
	// mid-turn): the use from here on is its.
	Taken struct {
		InputID string
		Runs    bool
	}
	// Started: the agent named its session and model (Claude: system/init, each turn), and the
	// effort level it runs at, if it says (Codex, pi; "" for Claude, read with Applier).
	Started struct{ Session, Model, Effort string }
	// Cleared: the agent went on in a conversation of a new id, Session, in the same process
	// (Claude's own /clear, /reset, /new): what it says from then on is not the session's.
	Cleared struct{ Session string }
	// Hello: the handshake's answer.
	Hello struct {
		Version  string
		Mode     string
		Models   []Model
		Commands []Command
		// Account, Plan: the login the agent runs under (Claude: e-mail, subscriptionType).
		Account, Plan string
	}
	// TurnUsage is one turn's tokens, and the session's running totals as the agent counts them.
	// CostUSD: the turn's own cost, where the agent prices each answer but keeps no totals by
	// model (pi); nil if not known.
	TurnUsage struct {
		Tokens  Tokens
		Context int // tokens the last request carried
		Total   *Usage
		CostUSD *float64
	}
	// QuotaHit: the agent was refused for its quota until ResetsAt (ms), or told it may go on.
	QuotaHit struct {
		Exhausted bool
		ResetsAt  int64
	}
	Compacted struct {
		Before, After int
		Failed        bool
	}
	ProtocolError struct{ Reason, Detail string }
	// GiveUp: the process can do nothing for the session (Codex could not open its thread): the host
	// stops it, and Why is the session's error.
	GiveUp struct{ Why string }
)

func (TurnStart) event()      {}
func (TurnEnd) event()        {}
func (StateChange) event()    {}
func (TextDelta) event()      {}
func (MessageDone) event()    {}
func (ToolDraft) event()      {}
func (ToolDraftDelta) event() {}
func (ToolStart) event()      {}
func (ToolDone) event()       {}
func (AskOpen) event()        {}
func (AskCancel) event()      {}
func (PendingAsks) event()    {}
func (Tasks) event()          {}
func (Taken) event()          {}
func (Started) event()        {}
func (Cleared) event()        {}
func (Hello) event()          {}
func (TurnUsage) event()      {}
func (QuotaHit) event()       {}
func (Compacted) event()      {}
func (ProtocolError) event()  {}
func (GiveUp) event()         {}

// Model is a model the agent offers. Resolved: the full id Value runs (Claude's resolvedModel);
// Efforts: the effort levels it takes; NoEffort: it takes none. Neither: the agent does not say.
type Model struct {
	Value       string   `json:"value"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Resolved    string   `json:"resolved,omitempty"`
	Efforts     []string `json:"efforts,omitempty"`
	NoEffort    bool     `json:"noEffort,omitempty"`
}

type Command struct {
	Name         string `json:"name"`
	Description  string `json:"description,omitempty"`
	ArgumentHint string `json:"argumentHint,omitempty"`
}

type Tokens struct {
	Input      int `json:"input"`
	Output     int `json:"output"`
	CacheRead  int `json:"cacheRead"`
	CacheWrite int `json:"cacheWrite"`
}

func (t Tokens) Sub(o Tokens) Tokens {
	return Tokens{t.Input - o.Input, t.Output - o.Output, t.CacheRead - o.CacheRead, t.CacheWrite - o.CacheWrite}
}

// ModelUsage is one model's share of a session's use.
type ModelUsage struct {
	Tokens
	CostUSD float64 `json:"costUsd"`
}

// Usage is a session's use so far, by model, as the agent counts it (Claude: across resumes),
// and its quota windows where the agent reads them.
type Usage struct {
	CostUSD float64               `json:"costUsd"`
	Models  map[string]ModelUsage `json:"models"`
	Windows []api.QuotaWindow     `json:"windows,omitempty"`
}

// Since is what was used between before and u, by model; before may be nil (nothing yet).
func (u *Usage) Since(before *Usage) map[string]ModelUsage {
	out := map[string]ModelUsage{}
	for m, now := range u.Models {
		var was ModelUsage
		if before != nil {
			was = before.Models[m]
		}
		d := ModelUsage{Tokens: now.Tokens.Sub(was.Tokens), CostUSD: now.CostUSD - was.CostUSD}
		if d != (ModelUsage{}) {
			out[m] = d
		}
	}
	return out
}

// What a parent Claude or Codex session or a Herdr pane hands down (Rhodium src/agents.ts). Left
// in, the agent would think it runs inside that session, and hooks would report it as that pane.
// Every CLAUDE_CODE_ variable goes but the crash-resume switch, which the driver sets itself;
// CLAUDE_JOB_DIR too, the parent's job directory (Claude 2.1.318 hands it down; Rhodium's list
// predates it). IRIDIUM_SESSION and RHODIUM_SLOT go too: Iridium sets its own.
var parentSession = regexp.MustCompile(`^(CLAUDECODE|CLAUDE_CODE_.*|CLAUDE_PID|CLAUDE_EFFORT|CLAUDE_JOB_DIR|CODEX_(THREAD_ID|SESSION_ID|CI|VERSION|APPLY_PATCH_PRESERVE_LINE_ENDINGS)|AI_AGENT|HERDR_.*|IRIDIUM_SESSION|RHODIUM_SLOT)$`)

// CleanEnv is env without what a parent session hands down.
func CleanEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if k == "CLAUDE_CODE_RESUME_INTERRUPTED_TURN" || !parentSession.MatchString(k) {
			out = append(out, kv)
		}
	}
	return out
}

// claudeKeys: Claude's keys, subscription and settings (ANTHROPIC_API_KEY, ANTHROPIC_AUTH_TOKEN,
// ANTHROPIC_BASE_URL, CLAUDE_CODE_OAUTH_TOKEN, CLAUDE_CONFIG_DIR...), which no agent but Claude
// may see (Rhodium src/agents.ts): pi's bundled Anthropic SDK would read them.
var claudeKeys = regexp.MustCompile(`^(ANTHROPIC_|CLAUDE)`)

// ForeignEnv is CleanEnv for an agent other than Claude: Claude's keys go too.
func ForeignEnv(env []string) []string {
	out := CleanEnv(env)[:0:0]
	for _, kv := range CleanEnv(env) {
		if k, _, _ := strings.Cut(kv, "="); !claudeKeys.MatchString(k) {
			out = append(out, kv)
		}
	}
	return out
}

// SetEnv sets k=v in env, replacing any k there.
func SetEnv(env []string, k, v string) []string {
	out := env[:0:0]
	for _, kv := range env {
		if !strings.HasPrefix(kv, k+"=") {
			out = append(out, kv)
		}
	}
	return append(out, k+"="+v)
}

// GetEnv is k's value in env.
func GetEnv(env []string, k string) string {
	v := ""
	for _, kv := range env {
		if s, ok := strings.CutPrefix(kv, k+"="); ok {
			v = s
		}
	}
	return v
}

// ErrNoProgram: the agent's program is not there, as for a few seconds while it updates itself.
var ErrNoProgram = errors.New("agent program not found")

// LookPath finds the program: absolute as given, a bare name on env's PATH.
func LookPath(program string, env []string) (string, error) {
	if strings.Contains(program, "/") {
		if _, err := os.Stat(program); err != nil {
			return "", fmt.Errorf("%w: %v", ErrNoProgram, err)
		}
		return program, nil
	}
	path := GetEnv(env, "PATH")
	for _, d := range filepath.SplitList(path) {
		if d == "" {
			continue
		}
		p := filepath.Join(d, program)
		if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return p, nil
		}
	}
	return "", fmt.Errorf("%w: %s on PATH %s", ErrNoProgram, program, path)
}
