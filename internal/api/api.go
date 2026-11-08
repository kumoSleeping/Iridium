// Package api holds the types of Iridium's HTTP API (docs/api.md): requests, replies, the carried
// states of the event stream and the message blocks. The web app's TypeScript types are generated
// from this package alone, so it imports nothing of Iridium's own.
package api

//go:generate go run github.com/gzuidhof/tygo@v0.2.21 generate --config ../../tygo.yaml

// Agent is the kind of agent: claude, codex, pi, dsh.
type Agent string

const (
	AgentClaude Agent = "claude"
	AgentCodex  Agent = "codex"
	AgentPi     Agent = "pi"
	AgentDsh    Agent = "dsh"
)

// AgentInfo is what an agent Iridium hosts can do (GET /api/agents).
type AgentInfo struct {
	Agent Agent  `json:"agent"`
	Name  string `json:"name"`
	// Modes: its approval modes, in the order the mode key cycles them; DefaultMode is an empty
	// mode's. ModeSwitch: when a switch takes effect: now | next turn | restart.
	Modes       []string `json:"modes"`
	DefaultMode string   `json:"defaultMode"`
	ModeSwitch  string   `json:"modeSwitch"`
	// ContextBreakdown: its context comes in categories. Quota: it reports quota windows.
	ContextBreakdown bool `json:"contextBreakdown"`
	Quota            bool `json:"quota"`
	// Ultracode: its sessions can turn ultracode on (Claude).
	Ultracode bool `json:"ultracode"`
	// Presets: its agent presets (dsh: the tools and prompt a session runs with), taken at launch:
	// a switch restarts the agent. DefaultPreset is an empty preset's. None: it has no presets.
	Presets       []string `json:"presets,omitempty"`
	DefaultPreset string   `json:"defaultPreset,omitempty"`
	// Missing: its program is not on the daemon's PATH (not installed): a new session is refused.
	Missing bool `json:"missing,omitempty"`
}

// Color is a colour tag a session or a folder is given from its menu (the user, 2026-11-06).
type Color string

const (
	ColorRed    Color = "red"
	ColorOrange Color = "orange"
	ColorYellow Color = "yellow"
	ColorGreen  Color = "green"
	ColorBlue   Color = "blue"
	ColorPurple Color = "purple"
)

// Colors is every colour tag, in the menu's order.
var Colors = []Color{ColorRed, ColorOrange, ColorYellow, ColorGreen, ColorBlue, ColorPurple}

// Status is a session's lamp (plan "风格"). idle shows none.
type Status string

const (
	StatusIdle       Status = "idle"
	StatusStarting   Status = "starting"
	StatusRunning    Status = "running"
	StatusCompacting Status = "compacting"
	StatusWaiting    Status = "waiting" // needs you: an ask is open
	StatusError      Status = "error"   // its last process ended badly
	StatusElsewhere  Status = "elsewhere"
)

// Session is one row of the session list.
type Session struct {
	ID    string `json:"id"`
	Agent Agent  `json:"agent"`
	Cwd   string `json:"cwd"`
	// Name: what it was renamed to, else empty. Title: the opening of its first message.
	Name   string `json:"name,omitempty"`
	Title  string `json:"title,omitempty"`
	Status Status `json:"status"`
	// Hosted: Iridium runs (or ran) it through a holder. Live: an agent process runs now.
	Hosted    bool  `json:"hosted"`
	Live      bool  `json:"live"`
	Pinned    bool  `json:"pinned,omitempty"`
	Archived  bool  `json:"archived,omitempty"`
	Workspace int64 `json:"workspace,omitempty"` // 0: none
	// Color: the colour tag it was given in its menu, if any.
	Color Color `json:"color,omitempty"`
	// Launcher: the session that started this one, if discovery saw it.
	Launcher string `json:"launcher,omitempty"`
	// Subagent: an Agent tool's subtask (Claude) or a subagent's thread (Codex), listed as a
	// session of its own, read-only; Parent: the session that started it.
	Subagent bool   `json:"subagent,omitempty"`
	Parent   string `json:"parent,omitempty"`
	// Done: a subagent that has finished (Claude: its transcript ends with its final answer, or it was stopped); the
	// sidebar shows a subagent only while it works.
	Done bool `json:"done,omitempty"`
	// Needs: elsewhere, and its agent waits on you there (Claude: an ask or a question open in its
	// terminal), what for in the agent's words; the lamp is amber.
	Needs string `json:"needs,omitempty"`
	// Workflow: the run a workflow's agent is of (Claude's subagents/workflows/<run>/), the
	// workflow's name and the agent's phase in it, as far as they are known.
	Workflow  *SessionWorkflow `json:"workflow,omitempty"`
	Last      *LastSaid        `json:"last,omitempty"`
	UpdatedAt int64            `json:"updatedAt"` // ms since the epoch
	// Bots: the plugins (terminal IDs) whose chat is in this session now: the list shows their
	// avatar and a Bot tag.
	Bots []string `json:"bots,omitempty"`
	// Machine: the executor it runs at (its terminal ID; docs/exec.md), empty for this machine.
	// Offline: that executor is not connected now.
	Machine string `json:"machine,omitempty"`
	Offline bool   `json:"offline,omitempty"`
}

// LastSaid is the last thing said in a session, on one line, at most 200 characters.
type LastSaid struct {
	Role string `json:"role"` // user | assistant
	Text string `json:"text"`
	From string `json:"from,omitempty"` // the user message's source, as Iridium wrote it
	At   int64  `json:"at,omitempty"`
}

// SessionState is the carried state "session:<id>": the open session.
type SessionState struct {
	Session `tstype:",extends"`
	Asks    []Ask  `json:"asks"`
	Tasks   []Task `json:"tasks"`
	// Denials: steps Claude's auto mode classifier turned down since the daemon started, waiting
	// for you to switch to manual for them or let them be (docs/api.md "临时切换至 manual"); the oldest first.
	Denials []Denial `json:"denials"`
	Model   string   `json:"model,omitempty"`
	Mode    string   `json:"mode,omitempty"` // the approval (permission) mode
	// End: how far the transcript has been read; GET messages?after=<your end> for new blocks.
	End int64 `json:"end"`
	// Proc: the agent process's number, up by one at each start; drafts of another are stale.
	Proc  int    `json:"proc"`
	Error string `json:"error,omitempty"` // why its last process ended badly
	// Pick: the model set for it ("" or "default": the agent's own); Model is the one in use, often
	// a full id (Claude: as get_settings or the turn's init name it).
	Pick string `json:"pick,omitempty"`
	// Effort: the thinking effort set for it; EffortNow: the level the agent says it runs at (set
	// or not). Models, Modes, Efforts: what the agent offers.
	Effort    string   `json:"effort,omitempty"`
	EffortNow string   `json:"effortNow,omitempty"`
	Models    []Model  `json:"models,omitempty"`
	Modes     []string `json:"modes,omitempty"`
	Efforts   []string `json:"efforts,omitempty"`
	// Preset: the agent preset set for it ("": the agent's default; see AgentInfo.Presets).
	Preset string `json:"preset,omitempty"`
	// Queue: messages waiting for the turn to end, oldest first (docs/api.md "待发队列").
	Queue []Queued `json:"queue"`
	// Context: how full its context window was when last read; context:<id> has the parts.
	Context *ContextSummary `json:"context,omitempty"`
	// QuotaUntil: the agent's quota ran out; Iridium goes on at this time (ms).
	QuotaUntil int64 `json:"quotaUntil,omitempty"`
	// Tail: the newest blocks, when they are few and small; else the page fetches after its end.
	Tail *BlockTail `json:"tail,omitempty"`
	// Commands: the slash commands its agent offers, as its last handshake named them.
	Commands []Command `json:"commands,omitempty"`
	// Ultracode: Claude's ultracode; absent for an agent without it.
	Ultracode *Ultracode `json:"ultracode,omitempty"`
	// Next: its agent cleared itself (Claude's own /clear) and the conversation goes on in this
	// session; this one is archived, as it was before. The page open on it follows.
	Next string `json:"next,omitempty"`
}

// Ultracode is a Claude session's ultracode. On: asked for (what the switch shows); Active: asked
// for and in effect; Available: its model can take it (true till the process says).
type Ultracode struct {
	On        bool `json:"on"`
	Active    bool `json:"active"`
	Available bool `json:"available"`
}

// Command is a slash command an agent offers (the composer's palette), without its slash.
type Command struct {
	Name         string `json:"name"`
	Description  string `json:"description,omitempty"`
	ArgumentHint string `json:"argumentHint,omitempty"`
}

// Ask is a question, a plan to confirm or a tool to approve, open until answered or withdrawn.
type Ask struct {
	RequestID   string `json:"requestId"`
	Kind        string `json:"kind"` // question | plan | permission
	Tool        string `json:"tool"`
	Input       any    `json:"input" tstype:"unknown"`
	ToolUseID   string `json:"toolUseId,omitempty"`
	Description string `json:"description,omitempty"`
	// Pattern: what "always allow for this session" allows (Claude: its own rule suggestion,
	// such as "git status *"); empty: every call of Tool.
	Pattern string `json:"pattern,omitempty"`
	// Card: the ask as the confirmation panel and plugins show it.
	Card *Card `json:"card,omitempty"`
}

// Denial is a step Claude's auto mode classifier turned down: the call (ToolUseID), its tool and
// input, the classifier's reason, when (ms).
type Denial struct {
	ToolUseID string `json:"toolUseId"`
	Tool      string `json:"tool"`
	Input     any    `json:"input" tstype:"unknown"`
	Reason    string `json:"reason,omitempty"`
	At        int64  `json:"at"`
}

// Task is a background task of the agent's. While one runs the session is not reaped.
type Task struct {
	ID          string `json:"id"`
	Type        string `json:"type,omitempty"` // Claude: local_bash, local_agent...
	Description string `json:"description,omitempty"`
}

// Context is the carried state "context:<id>": how full the context window is.
type Context struct {
	Used       int               `json:"used"`
	Max        int               `json:"max,omitempty"`
	Categories []ContextCategory `json:"categories,omitempty"`
	At         int64             `json:"at"`
}

type ContextCategory struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Tokens int    `json:"tokens"`
	Kind   string `json:"kind"` // used | buffer | free | deferred
}

// Quota is the carried state "quota": the agents' quota windows, as last read.
type Quota struct {
	Agents []AgentQuota `json:"agents"`
}

// AgentQuota is one agent and account's windows. Live: a process of that agent and account runs,
// so they are kept fresh; else they are the last read, and At says how old.
type AgentQuota struct {
	Agent   Agent         `json:"agent"`
	Account string        `json:"account,omitempty"` // the login the agent reports (Claude: e-mail)
	Plan    string        `json:"plan,omitempty"`    // Claude: subscriptionType
	Windows []QuotaWindow `json:"windows"`
	Balance []Balance     `json:"balance,omitempty"` // dsh: the DeepSeek account's money, by currency
	At      int64         `json:"at"`                // when they were read
	Live    bool          `json:"live"`
}

// Balance is an account's money in one currency (DeepSeek's /user/balance): Total is Granted (given)
// and ToppedUp (paid) together.
type Balance struct {
	Currency string  `json:"currency"` // CNY | USD
	Total    float64 `json:"total"`
	Granted  float64 `json:"granted"`
	ToppedUp float64 `json:"toppedUp"`
}

type QuotaWindow struct {
	Group    string  `json:"group"`           // Claude: session (5 hours) | weekly (7 days)
	Model    string  `json:"model,omitempty"` // the one model it is for, if any
	Percent  float64 `json:"percent"`
	ResetsAt int64   `json:"resetsAt,omitempty"`
}

// Block is a piece of a conversation as the page shows it (Rhodium §7.7): the server reads each
// agent's transcript and gives blocks, so the page never reads an agent's own format.
//
// At is where it is: the byte offset of its transcript line, a dot, its place among that line's
// blocks. Text, Input and a result's Text are cut to 2000 characters, with Length the whole's;
// GET block?at= gives the whole.
type Block struct {
	At   string    `json:"at"`
	Time int64     `json:"time,omitempty"`
	Kind BlockKind `json:"kind"`
	Text string    `json:"text,omitempty"`
	// Length: the whole text's length in characters, where Text (or Input) is cut.
	Length int `json:"length,omitempty"`
	// user: From is the source Iridium wrote the message with, its prefix taken off; ID its uuid.
	// FromTerminal is the ID of the interactive terminal that sent it (its avatar, its kind).
	From         string `json:"from,omitempty"`
	FromTerminal string `json:"fromTerminal,omitempty"`
	ID           string `json:"id,omitempty"` // tool, result: the call's id
	// tool: Name, Input as JSON, and Result when it is on the same page.
	Name   string  `json:"name,omitempty"`
	Input  string  `json:"input,omitempty"`
	Result *Result `json:"result,omitempty"`
	Error  bool    `json:"error,omitempty"` // result
	// system: what happened. Compact: a compaction's figures, as far as the agent records them.
	Event   SystemEvent `json:"event,omitempty"`
	Compact *Compaction `json:"compact,omitempty"`
	// Files the block names that exist: user and assistant text, a Write call's file.
	Files []FileRef `json:"files,omitempty"`
}

// Compaction is what a compaction did: Auto, the agent's own (the context ran full) rather than
// a /compact; the context before and after it in tokens, and how long it took; 0 is not recorded.
type Compaction struct {
	Auto   bool  `json:"auto,omitempty"`
	Before int   `json:"before,omitempty"`
	After  int   `json:"after,omitempty"`
	Ms     int64 `json:"ms,omitempty"`
}

type BlockKind string

const (
	BlockKindUser      BlockKind = "user"
	BlockKindAssistant BlockKind = "assistant"
	BlockKindThinking  BlockKind = "thinking"
	BlockKindTool      BlockKind = "tool"
	BlockKindResult    BlockKind = "result" // a result whose call is on another page
	BlockKindSystem    BlockKind = "system"
)

type SystemEvent string

const (
	SystemEventCompact      SystemEvent = "compact"
	SystemEventAPIError     SystemEvent = "api_error"
	SystemEventLocalCommand SystemEvent = "local_command"
	SystemEventInterrupted  SystemEvent = "interrupted" // you stopped the turn
	SystemEventUnreadable   SystemEvent = "unreadable"  // a line that is not a JSON object
	SystemEventIridium      SystemEvent = "iridium"     // Iridium's own line, such as a recovery prompt
	// SystemEventTask: a background task told the session it ended (Claude's task-notification:
	// a workflow, a background agent); Text is its summary, with how many agents and how long.
	SystemEventTask SystemEvent = "task"
)

// SessionWorkflow is the workflow run a subagent works in.
type SessionWorkflow struct {
	Run   string `json:"run"`
	Name  string `json:"name,omitempty"`
	Phase string `json:"phase,omitempty"`
}

// SourceIridium is the source of what Iridium itself writes to an agent (recovery prompts,
// "继续"): the page shows those as a faint line of its own, not as your words.
const SourceIridium = "Iridium"

// Result is a tool call's result.
type Result struct {
	At     string `json:"at"`
	Time   int64  `json:"time,omitempty"`
	Text   string `json:"text"`
	Length int    `json:"length,omitempty"`
	Error  bool   `json:"error,omitempty"`
}

// MessagePage is up to 20 messages (things you or the agent said) with the blocks between them.
// Start: before=<start> for the page before; End: after=<end> for what came after. More: there
// is more before Start.
type MessagePage struct {
	Blocks []Block `json:"blocks"`
	Start  int64   `json:"start"`
	End    int64   `json:"end"`
	More   bool    `json:"more"`
}

// FullText is a block's whole text.
type FullText struct {
	Text string `json:"text"`
}

// NewSession starts a hosted session. Cwd must exist; Text, if any, is its first message.
type NewSession struct {
	Agent  Agent  `json:"agent"`
	Cwd    string `json:"cwd"`
	Text   string `json:"text,omitempty"`
	Model  string `json:"model,omitempty"`
	Mode   string `json:"mode,omitempty"`
	Effort string `json:"effort,omitempty"`
	Preset string `json:"preset,omitempty"`
	// Workspace: the workspace it goes in (0: the caller's, else as discovery classifies it).
	Workspace int64 `json:"workspace,omitempty"`
	// Caller is the session that ran ir new (from IRIDIUM_SESSION, CLAUDE_CODE_SESSION_ID,
	// CODEX_THREAD_ID…): the new session goes into its workspace.
	Caller string `json:"caller,omitempty"`
	// Machine: the executor to run it at (a terminal ID or name; docs/exec.md); empty: here. Cwd
	// is then a path there.
	Machine string `json:"machine,omitempty"`
}

type NewSessionReply struct {
	ID string `json:"id"`
}

// Send gives the session a message: a new turn when idle; while a turn runs it waits in the
// queue for the turn to end, unless Steer, which joins the running turn.
type Send struct {
	Text  string `json:"text"`
	Steer bool   `json:"steer,omitempty"`
	// Attachments are file refs of the session (uploads, attached paths): their paths are written
	// into the message, and images go to the agent as images too.
	Attachments []string `json:"attachments,omitempty"`
	Caller      string   `json:"caller,omitempty"` // as NewSession's
}

type SendReply struct {
	Input  string `json:"input"`            // the message's id (Claude: its uuid)
	Queued bool   `json:"queued,omitempty"` // it waits in the queue
	// Session: /clear went on in this session, the old one left as it was (no input then).
	Session string `json:"session,omitempty"`
	// Command: the agent runs it as a command of its own (Codex's, pi's /compact): no words of
	// it come in the transcript.
	Command bool `json:"command,omitempty"`
}

// Release puts the process in manual mode for call ToolUseID, which Claude's auto mode classifier
// denied, till the step is run again or the turn ends; nothing is said to Claude (docs/api.md
// "临时切换至 manual").
type Release struct {
	ToolUseID string `json:"toolUseId"`
	// Dismiss: let it be, not run: only its card goes.
	Dismiss bool `json:"dismiss,omitempty"`
}

// Answer answers an ask. permission: Allow (and Message if not); question: Answers, by question
// text, the label chosen; plan: Allow (approve), Message if not.
type Answer struct {
	RequestID string            `json:"requestId"`
	Kind      string            `json:"kind"`
	Allow     bool              `json:"allow,omitempty"`
	Message   string            `json:"message,omitempty"`
	Answers   map[string]string `json:"answers,omitempty"`
	// Always: allow, and from now on allow the ask's Pattern of its tool in this session.
	Always bool `json:"always,omitempty"`
}

// Error is every failed request's body.
type Error struct {
	Error string `json:"error"`
}

// StateEvent is the event stream's "state" event: a carried state, whole. Value is null for one
// that does not exist (a session gone).
type StateEvent struct {
	Key   string `json:"key"`
	Value any    `json:"value" tstype:"unknown"`
	// Ignored: the stream does not carry Key, and says why once, as it opens: "unknown" (no such
	// state) or "forbidden" (not for this terminal). The rest of the stream goes on.
	Ignored string `json:"ignored,omitempty"`
}

// DraftEvent is the event stream's "draft" event: text the agent is writing, appended to draft N
// of process Proc. A new N starts a new draft; the one before is sealed. Not stored anywhere: the
// transcript has the finished text.
type DraftEvent struct {
	Session string `json:"session"`
	Proc    int    `json:"proc"`
	N       int    `json:"n"`
	Text    string `json:"text"`
	// Done: the draft is whole, no more text comes to it; its words come in the transcript.
	Done bool `json:"done,omitempty"`
	// Tool: a tool call being written, not words (user 2026-11-06: no blank while a long command
	// is written): Text is its input's JSON so far, cut anywhere. Sent with every piece, so a
	// stream opened mid-call knows it; sealed as the call is whole, the transcript then has it by its ID.
	Tool *DraftTool `json:"tool,omitempty"`
}

// DraftTool is the call a tool draft is: its ID, as the transcript's tool block has it, and the
// tool's name.
type DraftTool struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
