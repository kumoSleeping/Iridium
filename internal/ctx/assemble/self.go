package assemble

import (
	"strconv"
	"strings"
	"time"
)

// Self is what a hosted agent is told of itself in its system prompt (docs/channels.md
// "资料", "模糊记忆", "记忆"): the nick it goes by, its intro and its persona, what it chose to
// remember, and what Iridium's records say of where it has been: the channels it is in, the
// contexts it had before it was cleared. All of it from the records, none of it a summary.
type Self struct {
	// Persona: its own words on how it works, as written, after its intro (otae, 2026-11-21).
	Nick, Intro, Persona string
	// Memory: the latest of what it chose to remember; More: how many more there are.
	Memory []SelfMemory
	More   int
	// Channels: those it is in, the latest spoken in first. Before: its earlier contexts.
	Channels []SelfChannel
	Before   []SelfBefore
	// IR: it has ir to look things up, remember and change profiles with.
	IR   bool
	Zone *time.Location
}

// SelfMemory is one thing remembered: when (ms), where it is from, the words.
type SelfMemory struct {
	At   int64
	From string
	Text string
}

// SelfChannel is a channel the agent is in: when it last spoke there (0: never), with whom,
// the head of what it said.
type SelfChannel struct {
	ID, Name string
	At       int64
	With     []string
	Last     string
}

// SelfBefore is a context it had before: the session, when it was cleared, its turns, the
// head of its first message.
type SelfBefore struct {
	Session string
	At      int64
	Turns   int
	First   string
}

// day is a time as the system prompt has it: to the day, so that the prompt changes only when
// something happened (an agent's cache holds over a restart).
func (s *Self) day(at int64) string {
	zone := s.Zone
	if zone == nil {
		zone = time.UTC
	}
	return time.UnixMilli(at).In(zone).Format("2006-01-02")
}

// selfPieces are what the system prompt says of the agent itself; none for nil.
func selfPieces(s *Self) []piece {
	if s == nil {
		return nil
	}
	var ps []piece
	if s.Nick != "" {
		ps = append(ps, text(idSelfWho, "nick", s.Nick))
	}
	if s.Intro != "" {
		ps = append(ps, text(idSelfIntro), words(s.Intro, Ref{Kind: "profile", ID: "intro"}), text(idNL))
	}
	if s.Persona != "" {
		ps = append(ps, words(s.Persona, Ref{Kind: "profile", ID: "persona"}), text(idNL))
	}
	if len(s.Memory) > 0 {
		ps = append(ps, text(idSelfMemory))
		for _, m := range s.Memory {
			from := ""
			if m.From != "" {
				from = ", " + m.From
			}
			ps = append(ps, text(idSelfMemoryLine, "date", s.day(m.At), "from", from), words(m.Text, Ref{Kind: "memory"}), text(idNL))
		}
		if s.More > 0 {
			ps = append(ps, text(idSelfMemoryMore, "count", strconv.Itoa(s.More)))
		}
	}
	if len(s.Channels) > 0 {
		ps = append(ps, text(idSelfChannels))
		for _, c := range s.Channels {
			if c.At == 0 {
				ps = append(ps, text(idSelfChannelQuiet, "name", c.Name, "channel", c.ID))
				continue
			}
			with := ""
			if len(c.With) > 0 {
				with = ", with " + strings.Join(c.With, ", ")
			}
			ps = append(ps, text(idSelfChannelLine, "name", c.Name, "channel", c.ID, "date", s.day(c.At), "with", with),
				words(c.Last, Ref{Kind: "channel", ID: c.ID}), text(idNL))
		}
	}
	if len(s.Before) > 0 {
		ps = append(ps, text(idSelfBefore))
		for _, b := range s.Before {
			ps = append(ps, text(idSelfBeforeLine, "date", s.day(b.At), "turns", strconv.Itoa(b.Turns), "session", b.Session),
				words(b.First, Ref{Kind: "session", ID: b.Session}), text(idNL))
		}
	}
	if s.IR {
		ps = append(ps, text(idSelfTools), text(idSelfRemember), text(idSelfProfile), text(idSelfHelpers))
	}
	return ps
}
