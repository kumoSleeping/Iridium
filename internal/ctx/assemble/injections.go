package assemble

import "slices"

// What Iridium gives agents, each as an Injection. An error is a fragment missing or its slots
// not matching: a broken build, which the tests catch.

// System is what is appended to a hosted agent's system prompt (plan "ir"), made anew at each
// start of its process.
func System(w Where) (Injection, error) {
	return build(KindSystem, systemPieces(w)...)
}

// Input is words that are not Iridium's, as they are: a person's or an app's message, with
// what names it (the input's id). A driver gets them through Message; alone they are for a
// test of one.
func Input(s string, refs ...Ref) Injection {
	in, _ := build(KindMessage, words(s, refs...)) // words alone cannot fail
	return in
}

// Message is the line an agent gets for message input from a sender: "[tag] " and the words
// (the terminal's ID for a sender without a tag); a slash command alone, as typed.
func Message(tag, terminal, said, input string) (Injection, error) {
	var refs []Ref
	if input != "" {
		refs = []Ref{{Kind: "input", ID: input}}
	}
	return build(KindMessage, messagePieces(tag, terminal, words(said, refs...), isCommand(said))...)
}

// Attachment is the line a message gets for a file that came with it: its path where the
// agent runs, in inline code.
func Attachment(path string) (Injection, error) {
	return build(KindMessage, text(idMessageAttach, "path", path))
}

// Denied is what the agent's call is answered with after a no to ask: the words that came
// with it, or Iridium's for none.
func Denied(message, ask string) (Injection, error) {
	return build(KindToolResult, deniedPieces(message, ask)...)
}

// TitleNow is the line put after a message now and then (every few turns, and after a
// compaction): the session's title as it is, so that the agent sees it beside the work at hand
// and renames the session when the subject has moved on (kumo, 2026-11-26). "": none yet.
func TitleNow(title string) (Injection, error) {
	if title == "" {
		return build(KindMessage, text(idTitleNone))
	}
	return build(KindMessage, text(idTitleNow, "title", title))
}

// TitleNowOf is TitleNow for channel ch, one the agent is in beside its own session: which
// channel it is said.
func TitleNowOf(title, ch string) (Injection, error) {
	if title == "" {
		return build(KindMessage, text(idTitleNoneOf, "channel", ch))
	}
	return build(KindMessage, text(idTitleNowOf, "title", title, "channel", ch))
}

// Then is in with more after it, as one: in's kind.
func (in Injection) Then(more Injection) Injection {
	return Injection{kind: in.kind, parts: append(slices.Clone(in.parts), more.parts...)}
}
