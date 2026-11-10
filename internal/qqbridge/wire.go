package qqbridge

import "github.com/kumoSleeping/iridium/internal/api"

// The 0.1 chat layer's wire types (/api/chat/*, the plugin event stream), kept here alone since
// Iridium 0.2 removed that layer: the bridge does not work against 0.2 until it is rewritten as
// a Satori client (docs/next/decisions.md, scope).

// ChatInput is a message from a chat window. Sender, if given, is who in the chat sent it.
type ChatInput struct {
	Text        string   `json:"text"`
	Sender      string   `json:"sender,omitempty"`
	Attachments []string `json:"attachments,omitempty"`
}

// ChatInputReply: where the message went, or an Iridium command's answer.
type ChatInputReply struct {
	Session string     `json:"session,omitempty"`
	Queued  bool       `json:"queued,omitempty"`
	Reply   *ChatReply `json:"reply,omitempty"`
	Card    *api.Card  `json:"card,omitempty"`
}

// ChatCommand is a command a chat window can send.
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

// ChatFile is a file a reply names.
type ChatFile struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	Type string `json:"type"`
	Ref  string `json:"ref"`
}

// ChatEvent is the data of a plugin event (progress, reply, card, card_done, error, status).
type ChatEvent struct {
	Chat    string     `json:"chat"`
	Session string     `json:"session,omitempty"`
	Text    string     `json:"text,omitempty"`
	Reply   *ChatReply `json:"reply,omitempty"`
	Card    *api.Card  `json:"card,omitempty"`
	CardID  string     `json:"cardId,omitempty"`
	Status  api.Status `json:"status,omitempty"`
}

// CardReply is what answering a card did: for a command's pick, its reply or a next card.
type CardReply struct {
	OK    bool       `json:"ok"`
	Reply *ChatReply `json:"reply,omitempty"`
	Card  *api.Card  `json:"card,omitempty"`
}
