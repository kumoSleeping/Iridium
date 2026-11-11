// Package wire is the executor protocol (docs/exec.md) both sides speak: its frames, its types,
// and a connection over a WebSocket. It builds on every system, the executor's too.
package wire

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Protocol is the version this side speaks.
const Protocol = 1

// Frame is one message either way: a request (Method), its answer (Result or Error, by ID), or an
// event (Event, from the executor only).
type Frame struct {
	ID     int64           `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Event  string          `json:"event,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *Error          `json:"error,omitempty"`
}

// Error codes.
const (
	CodeNotFound    = "not_found"
	CodeExists      = "exists"
	CodeDenied      = "denied"
	CodeBadRequest  = "bad_request"
	CodeUnsupported = "unsupported"
	CodeFailed      = "failed"
)

// Error is a request's failure as the other side gave it.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return fmt.Sprintf("executor: %s: %s", e.Code, e.Message) }

// IsCode reports whether err is an executor's Error of code.
func IsCode(err error, code string) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == code
}

// ErrOffline: the machine's executor is not connected.
var ErrOffline = errors.New("executor not connected")

// Hello is what an executor says first.
type Hello struct {
	Protocol int    `json:"protocol"`
	Program  string `json:"program"`
	OS       string `json:"os"` // windows, darwin, linux
	Arch     string `json:"arch"`
	Home     string `json:"home"`
	Sep      string `json:"sep"` // its path separator
	// Scratch is a directory of its own this side may write in (agents' launch files).
	Scratch string      `json:"scratch"`
	Env     []string    `json:"env"`
	Agents  []AgentInfo `json:"agents"`
	Holds   []HoldState `json:"holds"`
	// Shell: command lines run there, a person's on the page ($) and agents' (ir sh); else it
	// starts nothing but its agents (each hold.start's argv begins with one of theirs). Its
	// owner's choice, off unless said (ir join --shell). nil from an older executor: NoShell and
	// AgentShell say it (ShellOn).
	Shell *bool `json:"shell,omitempty"`
	// NoShell and AgentShell are the older two switches, still said for an older Iridium:
	// NoShell, only its agents start there; AgentShell, agents may run command lines there too.
	NoShell    bool `json:"noShell,omitempty"`
	AgentShell bool `json:"agentShell,omitempty"`
	// MachineID is a hash of the computer's own ID (MachineHash): one equal to the Iridium's own
	// is an app on the host, whose agents are not offered (they are the host's).
	MachineID string `json:"machineId,omitempty"`
	// IRClient: it puts an ir on the PATH of the agents it starts, which reaches the Iridium over
	// HTTPS with the session's own TOKEN (hold.start's env IRIDIUM_TOKEN; docs/exec.md "远端的
	// ir"): IRIDIUM_URL is set there to the address it connected to, unless the env has one.
	IRClient bool `json:"irClient,omitempty"`
	// Caps: the optional parts of the protocol it speaks (CapHeld…); none from an older one.
	Caps []string `json:"caps,omitempty"`
}

// ShellOn: command lines run there (Shell; from an older executor, AgentShell without NoShell).
func (h *Hello) ShellOn() bool {
	if h.Shell != nil {
		return *h.Shell
	}
	return h.AgentShell && !h.NoShell
}

// Caps an executor may say in Hello. CapHeld: held.list, held.send, held.get, held.set and the
// event held.changed. CapRun: run, run.stop and the events run.out, run.exit (else ir sh runs as a
// holder). CapPrepare: agent.presets and agent.prepare (it composes an agent's launch from its
// host's own settings). CapAttach: it sends session.attach and takes the event session.button.
const (
	CapHeld    = "held"
	CapRun     = "run"
	CapPrepare = "agent.prepare"
	CapAttach  = "session.attach"
)

// AgentInfo is an agent installed there and how to start it. Argv is the program part, to which
// the driver adds its arguments.
type AgentInfo struct {
	Agent   string   `json:"agent"`
	Argv    []string `json:"argv"`
	Package string   `json:"package,omitempty"` // the agent's package root
	Scope   string   `json:"scope,omitempty"`   // dsh: the directory of its @deepseek-ai packages
	Version string   `json:"version"`
	Home    string   `json:"home,omitempty"` // dsh: its DSH_HOME
}

// HoldState is a holder the executor has: how much output it wrote, the last input written (its
// seq), and how it ended, if it did.
type HoldState struct {
	Hold    string `json:"hold"`
	OutSize int64  `json:"outSize"`
	InSeq   int64  `json:"inSeq"`
	Exit    *Exit  `json:"exit"`
}

type HelloReply struct {
	Name string `json:"name"`
}

type StartParams struct {
	Hold        string   `json:"hold"`
	Argv        []string `json:"argv"`
	Env         []string `json:"env"`
	Cwd         string   `json:"cwd"`
	StopGraceMS int      `json:"stopGraceMs,omitempty"`
}

type WriteParams struct {
	Hold string `json:"hold"`
	Seq  int64  `json:"seq"`
	Line string `json:"line"`
}

type TailParams struct {
	Hold string `json:"hold"`
	From int64  `json:"from"`
}

type HoldParams struct {
	Hold string `json:"hold"`
}

// OutEvent: Data is whole lines of the holder's out.log from byte offset Off.
type OutEvent struct {
	Hold string `json:"hold"`
	Off  int64  `json:"off"`
	Data string `json:"data"`
}

// ExitEvent: the holder ended; ErrTail is the end of its stderr, for the error shown.
type ExitEvent struct {
	Hold string `json:"hold"`
	Exit
	ErrTail string `json:"errTail,omitempty"`
}

// Exit is how a holder ended, as hold.Exit: From "agent" (Code, or Signal without "SIG") or
// "holder" (it went without seeing its agent end).
type Exit struct {
	From   string  `json:"from"`
	Code   *int    `json:"code"`
	Signal *string `json:"signal"`
}

// RunParams is run: command line Line run there now, in Cwd ("" its home), by its shell (sh -c,
// cmd.exe /c on Windows), with Env (as hold.start's; none: its own). Run is this side's ID for it,
// in the events. Its output comes as run.out events, then one run.exit; run.stop ends it, and so
// does the connection's end.
type RunParams struct {
	Run  string   `json:"run"`
	Line string   `json:"line"`
	Cwd  string   `json:"cwd,omitempty"`
	Env  []string `json:"env,omitempty"`
}

// RunStopParams is run.stop: run ended, its whole tree.
type RunStopParams struct {
	Run string `json:"run"`
}

// RunOut is the event run.out: what the run wrote, as it came (UTF-8 text, whole characters);
// Err: to its stderr.
type RunOut struct {
	Run  string `json:"run"`
	Data string `json:"data"`
	Err  bool   `json:"err,omitempty"`
}

// RunExit is the event run.exit, the run's last: its exit code, or the signal (without "SIG")
// that ended it; Error, why it could not start (Code 127 then).
type RunExit struct {
	Run    string  `json:"run"`
	Code   *int    `json:"code"`
	Signal *string `json:"signal,omitempty"`
	Error  string  `json:"error,omitempty"`
}

// Stat is a file there.
type Stat struct {
	Size  int64  `json:"size"`
	Mtime int64  `json:"mtime"` // ms
	Type  string `json:"type"`  // file, dir, link, other
	Head  string `json:"head,omitempty"`
}

// Entry is one name in a directory there.
type Entry struct {
	Name  string `json:"name"`
	Size  int64  `json:"size"`
	Mtime int64  `json:"mtime"`
	Type  string `json:"type"`
}

type StatParams struct {
	Path string `json:"path"`
	Head int    `json:"head,omitempty"`
}

type PathParams struct {
	Path string `json:"path"`
}

type ListReply struct {
	Entries []Entry `json:"entries"`
}

type ReadParams struct {
	Path   string `json:"path"`
	Offset int64  `json:"offset"`
	Length int64  `json:"length"`
}

type ReadReply struct {
	Data []byte `json:"data"` // base64 in JSON
}

type WriteFileParams struct {
	Path    string `json:"path"`
	Data    []byte `json:"data"`
	SHA256  string `json:"sha256"`
	Replace bool   `json:"replace"`
}

// MaxRead is the most one fs.read returns.
const MaxRead = 4 << 20

// Optional (docs/exec.md "远端 DSH"): an agent's host there (the dsh plugin: the dsh it runs in, DSH
// Desktop). Another executor answers CodeUnsupported.

// HeldListReply answers held.list: the sessions open in that dsh
// process now (held: DSH Desktop has them loaded, its writer lock); Unsynced, the workspaces the
// plugin's settings keep from the Iridium (their folders or names); UnsyncedUngrouped, the
// sessions in none of them (dsh's 未分组) kept too.
type HeldListReply struct {
	Sessions []string `json:"sessions"`
	// Running: those of Sessions whose agent there works now (a turn, or one waiting on an
	// approval or an answer); nil from a plugin that does not say (all of them, then).
	Running           []string `json:"running,omitempty"`
	Unsynced          []string `json:"unsynced,omitempty"`
	UnsyncedUngrouped bool     `json:"unsyncedUngrouped,omitempty"`
}

// HeldSendParams is held.send: a message for a session the dsh
// there holds (open in DSH Desktop), which the Iridium cannot resume: its agent there takes it, a
// next turn or (Steer) into the running one. CodeNotFound: no agent of it lives there.
type HeldSendParams struct {
	Session string `json:"session"`
	Text    string `json:"text"`
	Steer   bool   `json:"steer,omitempty"`
}

// HeldGetParams is held.get: session's live state there.
type HeldGetParams struct {
	Session string `json:"session"`
}

// HeldSetParams is held.set: one of Model ("<provider>/<model>") or Effort changed on a held
// session; the reply is HeldLive after it. CodeNotFound: not held there.
type HeldSetParams struct {
	Session string `json:"session"`
	Model   string `json:"model,omitempty"`
	Effort  string `json:"effort,omitempty"`
}

// HeldLive is a session as the holder there runs it now. Held: it has the session loaded; Model
// ("<provider>/<model>") and Effort, the level it runs at, only then.
type HeldLive struct {
	Held   bool   `json:"held"`
	Model  string `json:"model,omitempty"`
	Effort string `json:"effort,omitempty"`
}

// HeldChanged is the event held.changed: session's live state changed there (its model or effort
// changed in DSH Desktop); at most two a second per session.
type HeldChanged struct {
	Session string   `json:"session"`
	Live    HeldLive `json:"live"`
}

// PresetsParams is agent.presets: the agent presets Agent has there.
type PresetsParams struct {
	Agent string `json:"agent"`
}

// PresetsReply answers agent.presets: the agent presets its host registers (dsh: DSH Desktop's
// agentPresets registry: shipped, a bundle's, the profile's own edits), by its order, and its
// default (the user's pick in its settings, else the deployment's).
type PresetsReply struct {
	Presets []Preset `json:"presets"`
	Default string   `json:"default"`
}

// Preset is one registered preset: Plugins, its child plugin rows as YAML (the registry's read);
// Broken, why it cannot compose a session there, if it cannot.
type Preset struct {
	ID          string `json:"id"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Plugins     string `json:"plugins"`
	Broken      string `json:"broken,omitempty"`
}

// PrepareParams is agent.prepare: what a launch of Agent there takes from its host's settings.
// Session (the agent's own id), Cwd, Preset and System (Iridium's system prompt) for a session;
// no Session: for a look at its models only.
type PrepareParams struct {
	Agent   string `json:"agent"`
	Session string `json:"session,omitempty"`
	Cwd     string `json:"cwd,omitempty"`
	Preset  string `json:"preset,omitempty"`
	System  string `json:"system,omitempty"`
}

// PrepareReply answers agent.prepare: Patches, files there the launch takes in order (dsh: each a
// --patch after --profile sdk); Preset, the preset (System with it) is in them, so Iridium adds
// none of its own. Keys may be in them: they stay there, served to no one.
type PrepareReply struct {
	Patches []string `json:"patches"`
	Preset  bool     `json:"preset,omitempty"`
}

// AttachParams is session.attach, from the executor: Content (Satori elements; a string, or one
// a language: {"zh": …, "en": …}) at Place on session Session (the agent's own id there); empty
// or null takes it away.
type AttachParams struct {
	Session string          `json:"session"`
	Place   string          `json:"place"`
	Content json.RawMessage `json:"content,omitempty"`
}

// ButtonEvent is the event session.button, to the executor: a button it attached pressed (ID, its
// own id) on session Session, by User.
type ButtonEvent struct {
	Session string     `json:"session"`
	ID      string     `json:"id"`
	User    ButtonUser `json:"user"`
}

// ButtonUser is who pressed a button.
type ButtonUser struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}
