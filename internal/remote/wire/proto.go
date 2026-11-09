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
	// NoShell: it starts nothing but its agents (each hold.start's argv begins with one of
	// theirs), so no command line runs there; its owner's choice.
	NoShell bool `json:"noShell,omitempty"`
	// AgentShell: agents of that Iridium may run command lines here (ir sh), not only a person
	// on its page; its owner's choice, off unless said.
	AgentShell bool `json:"agentShell,omitempty"`
	// IRClient: it puts an ir on the PATH of the agents it starts, which reaches the Iridium over
	// HTTPS with the session's own TOKEN (hold.start's env IRIDIUM_TOKEN; docs/exec.md "远端的
	// ir"): IRIDIUM_URL is set there to the address it connected to, unless the env has one.
	IRClient bool `json:"irClient,omitempty"`
	// Caps: the optional parts of the protocol it speaks (CapHeld…); none from an older one.
	Caps []string `json:"caps,omitempty"`
}

// Caps an executor may say in Hello. CapHeld: held.list and held.send (dsh.open and dsh.prompt
// otherwise). CapHeldFeatures: held.get, held.set and the event held.changed.
const (
	CapHeld         = "held"
	CapHeldFeatures = "held.features"
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

// Optional, the dsh plugin's (docs/exec.md "远端 DSH"): the dsh it runs in (DSH Desktop). Another
// executor answers CodeUnsupported.

// HeldListReply answers held.list (dsh.open of an older plugin): the sessions open in that dsh
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

// DshOpenReply is HeldListReply by its older name.
type DshOpenReply = HeldListReply

// HeldSendParams is held.send (dsh.prompt of an older plugin): a message for a session the dsh
// there holds (open in DSH Desktop), which the Iridium cannot resume: its agent there takes it, a
// next turn or (Steer) into the running one. CodeNotFound: no agent of it lives there.
type HeldSendParams struct {
	Session string `json:"session"`
	Text    string `json:"text"`
	Steer   bool   `json:"steer,omitempty"`
}

// HeldGetParams is held.get: session's live state there, in Lang ("zh", "en"; "" the plugin's
// own) for the features' texts. Any session: one not held has its features unavailable, with
// why.
type HeldGetParams struct {
	Session string `json:"session"`
	Lang    string `json:"lang,omitempty"`
}

// HeldSetParams is held.set: one of Model ("<provider>/<model>"), Effort or Feature changed on a
// held session; the reply is HeldLive after it. CodeNotFound: not held there.
type HeldSetParams struct {
	Session string          `json:"session"`
	Lang    string          `json:"lang,omitempty"`
	Model   string          `json:"model,omitempty"`
	Effort  string          `json:"effort,omitempty"`
	Feature *HeldSetFeature `json:"feature,omitempty"`
}

// HeldSetFeature is feature ID switched On, maybe at Option (one of its options' IDs).
type HeldSetFeature struct {
	ID     string `json:"id"`
	On     bool   `json:"on"`
	Option string `json:"option,omitempty"`
}

// HeldLive is a session as the holder there runs it now. Held: it has the session loaded; Model
// ("<provider>/<model>") and Effort, the level it runs at (a feature's own while that feature
// owns the effort), only then. Features: the switches plugins there add to it, held or not.
type HeldLive struct {
	Held     bool      `json:"held"`
	Model    string    `json:"model,omitempty"`
	Effort   string    `json:"effort,omitempty"`
	Features []Feature `json:"features,omitempty"`
}

// HeldChanged is the event held.changed: session's live state changed there (a feature switched
// in DSH Desktop, its model or effort changed); at most two a second per session.
type HeldChanged struct {
	Session string   `json:"session"`
	Live    HeldLive `json:"live"`
}

// Feature is an extra switch on a session from a plugin there, as api.Feature: ID
// "<plugin>.<name>"; Available, it can be switched now (Reason, why not); Active, on and in
// effect; Options, what it runs as while on (Option, the one picked); Place, where the page puts
// it ("effort": at the effort track's right); Tone "warm" or none; V, its shape's version (1).
type Feature struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	On        bool            `json:"on"`
	Active    bool            `json:"active,omitempty"`
	Available bool            `json:"available"`
	Reason    string          `json:"reason,omitempty"`
	Hint      string          `json:"hint,omitempty"`
	Options   []FeatureOption `json:"options,omitempty"`
	Option    string          `json:"option,omitempty"`
	Place     string          `json:"place"`
	Tone      string          `json:"tone,omitempty"`
	V         int             `json:"v,omitempty"`
}

// FeatureOption is one of a feature's options.
type FeatureOption struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// DshPresetsReply answers dsh.presets: the agent presets the dsh there registers (its
// agentPresets registry: shipped, a bundle's, the profile's own edits), by its order, and its
// default (the user's pick in its settings, else the deployment's).
type DshPresetsReply struct {
	Presets []DshPreset `json:"presets"`
	Default string      `json:"default"`
}

// DshPreset is one registered preset: Plugins, its child plugin rows as YAML (the registry's
// read); Broken, why it cannot compose a session there, if it cannot.
type DshPreset struct {
	ID          string `json:"id"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Plugins     string `json:"plugins"`
	Broken      string `json:"broken,omitempty"`
}

// DshLLMReply answers dsh.llm: a patch there (Patch, its path) that gives an sdk profile the
// models and providers the dsh there has in its settings (DSH Desktop's): the llm adapters' rows
// and the default model, as configured there. Keys may be in it: it stays there, served to no one.
type DshLLMReply struct {
	Patch string `json:"patch"`
}

// DshAttachParams is dsh.attach: session filed under the workspace of its folder cwd there.
type DshAttachParams struct {
	Session string `json:"session"`
	Cwd     string `json:"cwd"`
}
