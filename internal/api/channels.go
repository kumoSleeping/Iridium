package api

import "encoding/json"

// ChannelMember is a member of a channel (docs/channels.md). Member: "agent:<session>", an
// app's login "<app>/<login>", or all of an app's "<app>/*". An agent's: Session, the session
// it is; Agent, its harness; Name, what it runs as now ("Claude Opus 5.5 High"), for people; Model,
// the model's own id (claude-opus-5-5, mistral/deepseek-v4-1-flash-1017), Effort, its thinking
// level, for agents; Nick, what it is
// called by, in every channel (@nick); Status, its lamp; Cwd, Machine, where it runs. On: an
// agent's subscriptions (Trigger), what wakes it beside what names it. What an agent writes in a turn
// a channel called is said in that channel. Seen: the number of the last message the agent was
// given.
type ChannelMember struct {
	Member  string    `json:"member"`
	Session string    `json:"session,omitempty"`
	Agent   Agent     `json:"agent,omitempty"`
	Name    string    `json:"name,omitempty"`
	Model   string    `json:"model,omitempty"`
	Effort  string    `json:"effort,omitempty"`
	Nick    string    `json:"nick,omitempty"`
	On      []Trigger `json:"on,omitempty"`
	Seen    int64     `json:"seen,omitempty"`
	Status  Status    `json:"status,omitempty"`
	Cwd     string    `json:"cwd,omitempty"`
	Machine string    `json:"machine,omitempty"`
	// Its card (Profile): Avatar where it is an emoji or a colour; Role, the role its session was
	// started as.
	Avatar string `json:"avatar,omitempty"`
	Role   string `json:"role,omitempty"`
}

// ChannelMembers are a channel's members: Channel, its id; Multi, it has more than one agent or
// is no session's (people call its agents by @nick; an agent then gets only what it is given).
type ChannelMembers struct {
	Channel string          `json:"channel"`
	Multi   bool            `json:"multi"`
	Members []ChannelMember `json:"members"`
	// Joined: the caller is one of its people (may speak, answer, change the members); not
	// joined, it only reads. Me: the member the caller is as a login ("<app>/<login>").
	Joined bool   `json:"joined"`
	Me     string `json:"me,omitempty"`
	// Warn: what a change just made may lead to (a loop of subscriptions), a line each.
	Warn []string `json:"warn,omitempty"`
	// Archived: its agents taken out and archived (移出并归档) whose sessions are still in the
	// archive, newest first; POST …/archived/{member} brings one back (kumo, 2026-11-27).
	Archived []ChannelMember `json:"archived,omitempty"`
}

// NewMember adds an agent to a channel: Session, a session there is; else a new session of
// Agent (Model, Mode, Effort, Preset as NewSession's) started in Cwd at Machine (none: the
// channel's folder), listed there as any session. Nick: what it is called by ("": the one it
// has, else one made); On: what wakes it beside what names it (none: nothing).
type NewMember struct {
	Session string    `json:"session,omitempty"`
	Agent   Agent     `json:"agent,omitempty"`
	Cwd     string    `json:"cwd,omitempty"`
	Machine string    `json:"machine,omitempty"`
	Model   string    `json:"model,omitempty"`
	Mode    string    `json:"mode,omitempty"`
	Effort  string    `json:"effort,omitempty"`
	Preset  string    `json:"preset,omitempty"`
	Attach  string    `json:"attach,omitempty"`
	Nick    string    `json:"nick,omitempty"`
	On      []Trigger `json:"on,omitempty"`
	// Role: start it as this role (its id or name): the role's agent, model, preset and card,
	// where the fields above say nothing; its effort and mode are these fields' or the agent's
	// own. Persona, Avatar: its card (ProfilePatch).
	Role    string `json:"role,omitempty"`
	Persona string `json:"persona,omitempty"`
	Avatar  string `json:"avatar,omitempty"`
	// Text: its first words, said in the channel by the one inviting it as "@nick text" once it
	// is in (one call: no @ before it has joined). Given, it does not greet.
	Text string `json:"text,omitempty"`
	// Greet: it is asked to say a line as it joins, hello and who it is (the page's invites,
	// kumo, 2026-11-16).
	Greet bool `json:"greet,omitempty"`
}

// MemberPatch changes a member: its nick, an agent's subscriptions (On; [] only what names it).
type MemberPatch struct {
	Nick *string    `json:"nick,omitempty"`
	On   *[]Trigger `json:"on,omitempty"`
}

// Injection is what an agent was given once (docs/channels.md "可追溯"): for message Seq of
// its channel, as input Input, at At (ms); Record says where each part of it came from
// (assemble.Record: the fragment and its hash, the values its slots took, the messages cited).
type Injection struct {
	ID int64 `json:"id"`
	// Channel: the channel the message is of (the session's own, or one it is in).
	Channel string          `json:"channel,omitempty"`
	Seq     int64           `json:"seq,omitempty"`
	Input   string          `json:"input,omitempty"`
	At      int64           `json:"at"`
	Record  json.RawMessage `json:"record"`
}

// ChannelAgent is an agent of a channel as the list of sessions has it: its session, its
// harness, the nick it is called by.
type ChannelAgent struct {
	Session string `json:"session"`
	Agent   Agent  `json:"agent,omitempty"`
	Nick    string `json:"nick,omitempty"`
}

// Spawned is where a session was started for a channel: Channel, the channel; By, the session
// whose agent started it ("": a person did).
type Spawned struct {
	By      string `json:"by,omitempty"`
	Channel string `json:"channel"`
}

// ChannelSetup is who a new session's channel has beside its own agent. Agents: more agents,
// each a session of its own (NewMember), in it by a subscription: called by what names it, what
// it writes there said there. People: who may speak in it ("*" everyone, "<app>/*",
// "<app>/<login>"); none: the panel's logins and the host's. The one who makes it is always
// one of them.
type ChannelSetup struct {
	Agents []NewMember `json:"agents,omitempty"`
	People []string    `json:"people,omitempty"`
	// Nick, On: the first agent's own (the session's), as NewMember's; On none: the messages of
	// whoever makes it (docs/channels.md "订阅事件").
	Nick string    `json:"nick,omitempty"`
	On   []Trigger `json:"on,omitempty"`
}

// ChannelReads is how far each member has read a channel (GET …/reads, the carried state
// "reads:<channel>"): End, the number of its last message; Readers, a person a login that
// opened it, an agent each one in it. Who has read message n: those whose Seq is n or more.
type ChannelReads struct {
	Channel string   `json:"channel"`
	End     int64    `json:"end"`
	Readers []Reader `json:"readers"`
}

// Reader is how far Member (a login "<app>/<login>", an agent "agent:<session>") has read: Seq,
// the number of the last message it read (an agent: was given), at At (ms; a person's). Name:
// what it goes by; Nick, Agent: an agent's.
type Reader struct {
	Member string `json:"member"`
	Name   string `json:"name,omitempty"`
	Nick   string `json:"nick,omitempty"`
	Agent  Agent  `json:"agent,omitempty"`
	Seq    int64  `json:"seq"`
	At     int64  `json:"at,omitempty"`
}

// Viewer is someone looking at a channel now (the carried state "viewers:<channel>", a list of
// them): Member, the login ("<app>/<login>"); Name, what it goes by; Pages, how many of its
// pages have the channel open; Since, when the first did (ms).
type Viewer struct {
	Member string `json:"member"`
	Name   string `json:"name,omitempty"`
	Pages  int    `json:"pages"`
	Since  int64  `json:"since"`
}

// Read moves the caller's place in a channel up to message Seq (POST …/read).
type Read struct {
	Seq int64 `json:"seq"`
}

// ChannelAt is the first message of a channel that names the caller and that it has not read
// (docs/channels.md "有人 @ 我"): Seq, its number; Everyone: it names all the members
// ("@全体成员"), not the caller alone.
type ChannelAt struct {
	Seq      int64 `json:"seq"`
	Everyone bool  `json:"everyone,omitempty"`
}

// ChannelAts are the caller's own, by channel: the carried state "ats", GET /v1/internal/ats.
type ChannelAts map[string]ChannelAt

// Outline is a session's record by turn (POST internal/block.outline): between what an agent
// did in counts and the record whole. Start, End: the lines of the record it spans; More: there
// are earlier turns.
type Outline struct {
	Turns []OutlineTurn `json:"turns"`
	Start int64         `json:"start"`
	End   int64         `json:"end"`
	More  bool          `json:"more,omitempty"`
}

// OutlineTurn is one turn of a record in a line: At, End, the blocks it begins and ends with
// (block.list reads between them); Time, when it began (ms); From, who asked; Asked, their first
// words; Did, what the agent did in it (activity.Activity: bash, failed, cmds, read, search,
// fetch, agents, other, files, ms); Said, the first words of the last thing it said; Words,
// how many times it spoke.
type OutlineTurn struct {
	At    string          `json:"at"`
	End   string          `json:"end"`
	Time  int64           `json:"time,omitempty"`
	From  string          `json:"from,omitempty"`
	Asked string          `json:"asked,omitempty"`
	Did   json.RawMessage `json:"did,omitempty"`
	Said  string          `json:"said,omitempty"`
	Words int             `json:"words,omitempty"`
}

// People sets a channel's people (PUT …/people).
type People struct {
	People []string `json:"people"`
}

// AgentChoices is what a new agent of a kind may be started with at a machine, as the latest
// session of that kind there said it: its models (each with its own efforts) and efforts. None
// known (no session of the kind ran there yet): empty, and the agent's own defaults go.
// Presets: an agent with presets (dsh): those a session of it there listed last.
// A new one given no model runs the one of Models marked Default; an agent that names none (pi):
// Ran, the model its kind last ran there, one of Models. Neither: not known till it starts.
// Effort: the level one given none last ran at there with that model.
type AgentChoices struct {
	Models  []Model  `json:"models"`
	Efforts []string `json:"efforts"`
	Presets *Presets `json:"presets,omitempty"`
	Ran     string   `json:"ran,omitempty"`
	Effort  string   `json:"effort,omitempty"`
	// Attached: the keys a new session may start with, as a running one's (SessionState.Attached),
	// all off (Claude's UltraCode, OMD's Ultra): a pick is one button's id (NewSession.Attach).
	Attached []Attached `json:"attached,omitempty"`
}
