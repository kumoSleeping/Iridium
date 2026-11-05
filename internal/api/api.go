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
	// Launcher: the session that started this one, if discovery saw it.
	Launcher  string    `json:"launcher,omitempty"`
	Last      *LastSaid `json:"last,omitempty"`
	UpdatedAt int64     `json:"updatedAt"` // ms since the epoch
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
	Model   string `json:"model,omitempty"`
	Mode    string `json:"mode,omitempty"` // the approval (permission) mode
	// End: how far the transcript has been read; GET messages?after=<your end> for new blocks.
	End int64 `json:"end"`
	// Proc: the agent process's number, up by one at each start; drafts of another are stale.
	Proc  int    `json:"proc"`
	Error string `json:"error,omitempty"` // why its last process ended badly
}

// Ask is a question, a plan to confirm or a tool to approve, open until answered or withdrawn.
type Ask struct {
	RequestID   string `json:"requestId"`
	Kind        string `json:"kind"` // question | plan | permission
	Tool        string `json:"tool"`
	Input       any    `json:"input" tstype:"unknown"`
	ToolUseID   string `json:"toolUseId,omitempty"`
	Description string `json:"description,omitempty"`
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

type AgentQuota struct {
	Agent   Agent         `json:"agent"`
	Windows []QuotaWindow `json:"windows"`
	At      int64         `json:"at"` // when they were read
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
	From string `json:"from,omitempty"`
	ID   string `json:"id,omitempty"` // tool, result: the call's id
	// tool: Name, Input as JSON, and Result when it is on the same page.
	Name   string  `json:"name,omitempty"`
	Input  string  `json:"input,omitempty"`
	Result *Result `json:"result,omitempty"`
	Error  bool    `json:"error,omitempty"` // result
	// system: what happened.
	Event SystemEvent `json:"event,omitempty"`
	// Files the block names that exist: user and assistant text, a Write call's file.
	Files []FileRef `json:"files,omitempty"`
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
)

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
	Agent Agent  `json:"agent"`
	Cwd   string `json:"cwd"`
	Text  string `json:"text,omitempty"`
	Model string `json:"model,omitempty"`
	Mode  string `json:"mode,omitempty"`
	// Workspace: the workspace it goes in (0: the caller's, else as discovery classifies it).
	Workspace int64 `json:"workspace,omitempty"`
	// Caller is the session that ran ir new (from IRIDIUM_SESSION, CLAUDE_CODE_SESSION_ID,
	// CODEX_THREAD_ID…): the new session goes into its workspace.
	Caller string `json:"caller,omitempty"`
}

type NewSessionReply struct {
	ID string `json:"id"`
}

// Send gives the session a message: a new turn when idle, else it joins the running one.
type Send struct {
	Text string `json:"text"`
	// Attachments are file refs of the session (uploads, attached paths): their paths are written
	// into the message, and images go to the agent as images too.
	Attachments []string `json:"attachments,omitempty"`
	Caller      string   `json:"caller,omitempty"` // as NewSession's
}

type SendReply struct {
	Input string `json:"input"` // the message's id (Claude: its uuid)
}

// Answer answers an ask. permission: Allow (and Message if not); question: Answers, by question
// text, the label chosen; plan: Allow (approve), Message if not.
type Answer struct {
	RequestID string            `json:"requestId"`
	Kind      string            `json:"kind"`
	Allow     bool              `json:"allow,omitempty"`
	Message   string            `json:"message,omitempty"`
	Answers   map[string]string `json:"answers,omitempty"`
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
}

// DraftEvent is the event stream's "draft" event: text the agent is writing, appended to draft N
// of process Proc. A new N starts a new draft; the one before is sealed. Not stored anywhere: the
// transcript has the finished text.
type DraftEvent struct {
	Session string `json:"session"`
	Proc    int    `json:"proc"`
	N       int    `json:"n"`
	Text    string `json:"text"`
}
