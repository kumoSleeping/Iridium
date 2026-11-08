package api

// ChatInput is a message from a plugin's chat window (plan "插件"). Sender, if given, is who in
// the chat sent it: the agent sees "[plugin·sender] ".
type ChatInput struct {
	Text   string `json:"text"`
	Sender string `json:"sender,omitempty"`
	// Attachments: refs of uploads into the chat's session (POST /api/chat/{key}/uploads): their
	// paths go into the message, images to the agent as images too, as the web's.
	Attachments []string `json:"attachments,omitempty"`
}

// ChatInputReply: Session is where the message went (or the command ran). An Iridium command
// answers here, with Reply or Card; what the agent says comes on the plugin's event stream.
type ChatInputReply struct {
	Session string     `json:"session,omitempty"`
	Queued  bool       `json:"queued,omitempty"`
	Reply   *ChatReply `json:"reply,omitempty"`
	Card    *Card      `json:"card,omitempty"`
}

// ChatPointer is what a chat window points at: its session, and what /new opens.
type ChatPointer struct {
	Chat    string `json:"chat"`
	Session string `json:"session,omitempty"`
	Agent   Agent  `json:"agent,omitempty"`
	Cwd     string `json:"cwd,omitempty"`
}

// ChatCommand is a command a chat window can send. Iridium: Iridium runs it; else it goes to the
// agent as typed.
type ChatCommand struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Iridium     bool   `json:"iridium,omitempty"`
}

// ChatReply is a reply in both forms: Markdown as written, Text with the marks taken off.
type ChatReply struct {
	Markdown string     `json:"markdown"`
	Text     string     `json:"text"`
	Files    []ChatFile `json:"files,omitempty"`
}

// ChatFile is a file a reply names. GET /api/chat/files/<ref> gives it (Range works); the ref is
// good only for the plugin it was given to.
type ChatFile struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	Type string `json:"type"` // a MIME type
	Ref  string `json:"ref"`
}

// ChatEvent is the data of a plugin event (SSE event types progress, reply, card, card_done,
// error, status). Every event is for one chat window.
type ChatEvent struct {
	Chat    string `json:"chat"`
	Session string `json:"session,omitempty"`
	// progress, error: Text.
	Text string `json:"text,omitempty"`
	// reply
	Reply *ChatReply `json:"reply,omitempty"`
	// card; card_done: CardID.
	Card   *Card  `json:"card,omitempty"`
	CardID string `json:"cardId,omitempty"`
	// status
	Status Status `json:"status,omitempty"`
}

// BotLetter is what a session says to the user through a bot (ir bot): To, a bot's name (its
// terminal's), else the one Iridium picks; Caller, the session (ir's own, as Send's).
type BotLetter struct {
	Text   string `json:"text"`
	To     string `json:"to,omitempty"`
	Caller string `json:"caller,omitempty"`
}

// BringBot is POST /api/sessions/{id}/bot: Bot's chat (its terminal ID) points at the session.
type BringBot struct {
	Bot string `json:"bot"`
}

// BotReply is where a letter went: the bot (its terminal ID, and name) and its chat; Letter, as a
// letter (the chat pointed elsewhere) rather than the session's reply.
type BotReply struct {
	Bot    string `json:"bot"`
	Name   string `json:"name,omitempty"`
	Chat   string `json:"chat"`
	Letter bool   `json:"letter,omitempty"`
}
