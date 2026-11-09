package api

// Queued is a message waiting in a session's queue (docs/api.md "待发队列"). Paused: it waits for
// "continue" (POST continue) or its own steer, not for the turn to end.
type Queued struct {
	ID   string `json:"id"`
	Text string `json:"text"`
	From string `json:"from"` // the sender's name, as the agent saw it
	// FromTerminal is the sending terminal's ID.
	FromTerminal string `json:"fromTerminal,omitempty"`
	Paused       bool   `json:"paused,omitempty"`
	At           int64  `json:"at"`
}

// EditQueued changes a queued message's text.
type EditQueued struct {
	Text string `json:"text"`
}

// ContextSummary is how full a session's context window was, without the parts.
type ContextSummary struct {
	Used int   `json:"used"`
	Max  int   `json:"max,omitempty"`
	At   int64 `json:"at"`
}

// BlockTail is the blocks from Start to End of the transcript. Blocks nil: too many or too
// large to carry, GET messages?after=<your end>. A page whose end is in [Start, End] takes the
// blocks at or after its end, and replaces a block it has at the same At (a tool whose result
// came).
type BlockTail struct {
	Start  int64   `json:"start"`
	End    int64   `json:"end"`
	Blocks []Block `json:"blocks"`
}

// Model is a model the agent offers. Value is what SetModel takes. Resolved: the full model id
// this value runs (Claude's resolvedModel, Codex's id). Efforts: the thinking levels this model
// takes; NoEffort: it takes none; neither: the agent does not say (SessionState.efforts holds).
type Model struct {
	Value       string   `json:"value"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Resolved    string   `json:"resolved,omitempty"`
	Efforts     []string `json:"efforts,omitempty"`
	NoEffort    bool     `json:"noEffort,omitempty"`
	// EffortNames: what the agent calls its efforts (dsh: as a provider registers them, "ultra":
	// "Ultra"), for those the page has no name of its own for.
	EffortNames map[string]string `json:"effortNames,omitempty"`
	// Group: what it is listed under (dsh: its provider's name); the menu heads each group.
	Group string `json:"group,omitempty"`
}

// SetModel, SetMode and SetEffort change a session's model, approval mode and thinking effort,
// at once if its agent runs, and for every later start. Model "" is the agent's default.
type SetModel struct {
	Model string `json:"model"`
}

type SetMode struct {
	Mode string `json:"mode"`
}

type SetEffort struct {
	Effort string `json:"effort"`
}

// SetPreset sets a session's agent preset ("": the agent's default), for this and every later
// start: a running agent restarts with it, now if idle, else when its turn ends.
type SetPreset struct {
	Preset string `json:"preset"`
}

// SetUltracode turns a Claude session's ultracode on or off, now and for every later start.
type SetUltracode struct {
	Ultracode bool `json:"ultracode"`
}

// AllowRule is a session's "always allow": asks for Tool whose subject (a command, a file path)
// matches Pattern are allowed without asking; '*' is any run of characters, "" any call.
type AllowRule struct {
	Tool    string `json:"tool"`
	Pattern string `json:"pattern"`
}

// Card is a choice to make, as the web's confirmation panel and plugins show it: an ask
// (permission, question, plan) or a command's pick (/use, /model, /mode). Options with the same
// Group are one question's; pick one per group.
type Card struct {
	ID      string       `json:"id"`
	Kind    string       `json:"kind"` // permission | question | plan | pick
	Session string       `json:"session,omitempty"`
	Title   string       `json:"title"`
	Summary []string     `json:"summary"`
	Options []CardOption `json:"options"`
	// Multi: the groups that take more than one option (a multiSelect question).
	Multi []int `json:"multi,omitempty"`
}

type CardOption struct {
	ID          string `json:"id"`
	Text        string `json:"text"`
	Description string `json:"description,omitempty"`
	Primary     bool   `json:"primary,omitempty"`
	Group       int    `json:"group,omitempty"`
}

// CardAnswer answers a card: Option, or Choices (one option id per group, more for a Multi
// group); Message goes with a denial or rejection. Words: a question's own answers, by group
// ("" for none), each in place of that group's choice.
type CardAnswer struct {
	Card    string   `json:"card"`
	Option  string   `json:"option,omitempty"`
	Choices []string `json:"choices,omitempty"`
	Words   []string `json:"words,omitempty"`
	Message string   `json:"message,omitempty"`
}

// CardReply is what answering a card did: for a pick, the command's reply or a next card.
type CardReply struct {
	OK    bool       `json:"ok"`
	Reply *ChatReply `json:"reply,omitempty"`
	Card  *Card      `json:"card,omitempty"`
}
