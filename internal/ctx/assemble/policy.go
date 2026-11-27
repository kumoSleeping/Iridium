package assemble

import (
	"cmp"
	"strings"

	"github.com/kumoSleeping/iridium/internal/irhelp"
)

// Policy: which pieces go when. Pure functions of what is known, apart from the text they
// choose (internal/prompts) and from how it is delivered.

// Where is where a hosted session's agent runs, as its system prompt depends on it.
type Where struct {
	// Remote: at another machine than Iridium's own (an executor's). IR: that machine has ir
	// (wire.Hello.IRClient); here there always is one.
	Remote, IR bool
	// Lang is the user's language as the web settings set it ("zh", "en"); "": not known.
	Lang string
	// Self: what the agent is told of itself (self.go); nil: nothing.
	Self *Self
	// Auto: its agent has Claude's auto mode, whose classifier may deny a step: it is told how to
	// ask a person for manual approval with ir (idHostManual), where it has ir.
	Auto bool
	// Folder: the folder it runs in, as written there; "": not said. OS: that computer's system
	// ("linux", "darwin", "windows"). Computer: its name, at another machine (Remote).
	Folder, OS, Computer string
	// Session is the session's id, which the commands it is told name it by: ir takes no "self"
	// (kumo, 2026-11-27).
	Session string
}

// systems are the systems' names as an agent is told them.
var systems = map[string]string{"linux": "Linux", "darwin": "macOS", "windows": "Windows"}

// wherePieces say where the agent runs (the user, 2026-11-18): the computer, its system, the
// folder; at another machine, how a path is written from there and how another is reached.
func wherePieces(w Where) []piece {
	if w.Folder == "" {
		return nil
	}
	os := cmp.Or(systems[w.OS], w.OS, "unknown system")
	if w.Remote {
		return []piece{text(idWhereRemote, "computer", cmp.Or(w.Computer, "another computer"), "os", os, "folder", w.Folder)}
	}
	return []piece{text(idWhereHost, "os", os, "folder", w.Folder)}
}

// langs are the languages that have a name to tell the agent.
var langs = map[string]string{"zh": idLangZh, "en": idLangEn}

// systemPieces is the system prompt of a session at w, in sections, what changes least first
// (kumo, 2026-11-26): what hosts it; CLI (with ir: its commands, how to read its help, secrets,
// wake, that a command line at a computer reaches the Iridium there, Claude's manual approval);
// Links (at the host, how to hand over a file; how words name a session or a folder); Title,
// for every session, a channel's too; Collaboration & Memory (with ir); Runtime: where it
// runs, the user's language, what it is told of itself.
func systemPieces(w Where) []piece {
	ir := !w.Remote || w.IR
	ps := []piece{text(idIridiumOpen)}
	switch {
	case !w.Remote:
		ps = append(ps, text(idHostIntro))
	case w.IR:
		ps = append(ps, text(idRemoteIntro))
	default:
		ps = append(ps, text(idRemoteIntroBare))
	}
	if ir {
		ps = append(ps, text(idCLI))
		if w.Remote {
			ps = append(ps, text(idCLIRemote))
		} else {
			ps = append(ps, text(idCLIHost, "secrets", "`{{name}}`, `{{file:name}}`"))
		}
		ps = append(ps, text(idCLIDiscover, "topics", topics()))
		if !w.Remote {
			ps = append(ps, text(idHostSecretsFiles))
		}
		ps = append(ps, text(idShellIR))
		if w.Auto {
			ps = append(ps, text(idHostManual, "session", w.Session))
		}
	}
	ps = append(ps, text(idLinks))
	if !w.Remote {
		ps = append(ps, text(idLinksFiles))
	}
	ps = append(ps, text(idNames), text(idTitle, "session", w.Session))
	if ir {
		ps = append(ps, collabPieces(w.Self)...)
	}
	var runtime []piece
	runtime = append(runtime, wherePieces(w)...)
	if name := langs[w.Lang]; name != "" {
		runtime = append(runtime, text(idLang).filled("language", name))
	}
	if w.Session != "" {
		runtime = append(runtime, text(idSelfSession, "session", w.Session))
	}
	runtime = append(runtime, selfPieces(w.Self)...)
	if len(runtime) > 0 {
		ps = append(append(ps, text(idRuntime)), runtime...)
	}
	return append(ps, text(idIridiumClose))
}

// topics are ir api help's topics by name, as the CLI section lists them.
func topics() string {
	var ts []string
	for _, t := range irhelp.Topics() {
		t, _, _ = strings.Cut(t, " (")
		ts = append(ts, t)
	}
	return strings.Join(ts, ", ")
}

// messagePieces is a message's line: the words after "[tag] ", the tag the sender's (its
// terminal's ID when it has none); a slash command as typed, which must reach the agent so.
func messagePieces(tag, terminal string, body piece, command bool) []piece {
	if command {
		return []piece{body}
	}
	if tag == "" {
		tag = terminal
	}
	return []piece{text(idMessageTag, "tag", tag), body}
}

// deniedPieces is what a call the user said no to is answered with: the words that came with
// the no, else Iridium's.
func deniedPieces(message, ask string) []piece {
	if message != "" {
		return []piece{words(message, Ref{Kind: "ask", ID: ask})}
	}
	return []piece{text(idAskDenied)}
}

// isCommand: the words are a slash command.
func isCommand(s string) bool { return strings.HasPrefix(s, "/") }
