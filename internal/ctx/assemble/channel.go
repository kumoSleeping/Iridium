package assemble

import (
	"cmp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/kumoSleeping/iridium/internal/activity"
	"github.com/kumoSleeping/iridium/internal/ctx/strategy"
	"github.com/kumoSleeping/iridium/internal/prompts"
)

// A channel of several agents (docs/channels.md "上下文"): what an agent called there is given.
// The words that call it come first, under their number; then a block of what the channel said
// since the agent last read it, as internal/ctx/strategy planned it.

// ChannelAgent is an agent of the channel: the nick it is called by, the name it goes by, its
// intro (the head of it), whom its receive set names (Recv, in words; "": only what names it).
// At: where it works, when not in the channel's folder (a place as written: mac:/Users/k/x).
type ChannelAgent struct{ Nick, Name, Intro, Recv, At string }

// ChannelScript is a script the channel's IR runs: its name, its intro, its triggers in words.
type ChannelScript struct{ Name, Intro, On string }

// ChannelPerson is one of the channel's people that has an intro: the name, the intro.
type ChannelPerson struct{ Name, Intro string }

// ProfileChange is a card changed since the agent was last told: Who ("": the channel), What
// ("name", "intro", "avatar", "background"), To: the new words (none for an image).
type ProfileChange struct{ Who, What, To string }

// Channel is what the agent called is told of the channel. You: its own nick; Agents: all of
// them, itself too; Plan: what it has not seen; Pull: it has ir to read more with; Zone: the
// zone times are written in (nil: UTC). ID, Name: the channel, for an agent that is in it by a
// subscription (docs/channels.md "订阅"): what it says in its turn stays in its own session, and
// it speaks in the channel with ir; none: the channel is the agent's own.
type Channel struct {
	// Rename: You is a nick Iridium gave it for now (channel.IsDefaultNick): it is asked to give
	// itself one first (kumo, 2026-11-16). Only to one with ir.
	Rename bool
	ID     string
	Name   string
	You    string
	Agents []ChannelAgent
	Plan   strategy.Plan
	Pull   bool
	Zone   *time.Location
	// Did: what an agent did before saying a message, by the message's number. Work: what each
	// agent did in all since this one last read the channel, what it has not spoken of yet too.
	Did  map[int64]activity.Activity
	Work []Work
	// Quote: the message the one given answers, if any.
	Quote *Quote
	// Brief: the channel's standing words (who it is there, the agents, how to answer and read
	// more) were given before and hold still (docs/channels.md "增量"): not again, a line in
	// their place.
	Brief bool
	// Key: what the standing words are of beside their text: the channel's agents (their
	// sessions). The names and intros in them are not part of it: when those change, the agent is
	// told what changed (Changes), not everything again.
	Key string
	// People: the channel's people that have an intro. Changes: the cards changed since the
	// agent was last told.
	People  []ChannelPerson
	Changes []ProfileChange
	// Scripts: the scripts the channel's IR runs (docs/channels.md "脚本").
	Scripts []ChannelScript
	// Place: the channel's folder, as a place is written (host:/home/k/ir). At: where the agent
	// called works, when that is not the channel's folder (the user, 2026-11-18). Both are said
	// only when some agent works elsewhere.
	Place, At string
	// Recalled: the numbers of messages it was given that were taken back since it was last
	// given something here (docs/channels.md "撤回").
	Recalled []int64
}

// ChannelStaticKey names the standing words as they are for c, apart from the names and intros
// in them: the text of the fragments, which channel, which agents (c.Key), whether it reads
// more with ir. What a channel's injection record keeps; changed, the words are given again.
func ChannelStaticKey(c Channel) string {
	c.You, c.Name, c.People, c.Changes, c.Scripts = "", "", nil, nil, nil
	c.Agents = []ChannelAgent{{Nick: c.Key}}
	return prompts.Hash(ChannelStatic(c))
}

// ChannelStatic is the channel's standing words as c would give them: what Brief leaves out.
// Their hash tells whether they changed since they were last given.
func ChannelStatic(c Channel) string {
	in, err := build(KindTurnPrefix, staticPieces(c)...)
	if err != nil {
		return ""
	}
	return in.String()
}

// staticPieces are the standing words: who the agent is there and the agents, how it answers;
// how it reads more.
func staticPieces(c Channel) []piece {
	self := cmp.Or(c.ID, "self")
	var agents []string
	for _, a := range c.Agents {
		s := a.Nick // without @: the agent echoing the list calls nobody
		var about []string
		for _, v := range []string{a.Name, a.Intro} {
			if v != "" {
				about = append(about, v)
			}
		}
		if a.Recv != "" {
			about = append(about, "woken by "+a.Recv)
		}
		if a.At != "" {
			about = append(about, "at "+a.At)
		}
		if len(about) > 0 {
			s += " (" + strings.Join(about, "; ") + ")"
		}
		agents = append(agents, s)
	}
	var ps []piece
	switch {
	case c.ID != "" && !c.Pull:
		ps = append(ps, text(idChannelGuestBare, "you", c.You, "name", cmp.Or(c.Name, c.ID), "agents", strings.Join(agents, ", ")))
	case c.ID != "":
		ps = append(ps, text(idChannelGuest, "you", c.You, "channel", c.ID, "name", cmp.Or(c.Name, c.ID), "agents", strings.Join(agents, ", ")))
	default:
		ps = append(ps, text(idChannelMembers, "you", c.You, "agents", strings.Join(agents, ", ")))
	}
	if len(c.People) > 0 {
		var people []string
		for _, p := range c.People {
			people = append(people, p.Name+" ("+p.Intro+")")
		}
		ps[0] = piece{parts: mustParts(ps[0], text(idChannelPeople, "people", strings.Join(people, ", ")))}
	}
	// Where the channel is and who works elsewhere, the agent called among them or not.
	if c.Place != "" && (c.At != "" || slices.ContainsFunc(c.Agents, func(a ChannelAgent) bool { return a.At != "" })) {
		ps[0] = piece{parts: mustParts(ps[0], text(idChannelPlace, "place", c.Place))}
		if c.At != "" {
			ps[0] = piece{parts: mustParts(ps[0], text(idChannelAway, "at", c.At, "example", awayExample(c.At)))}
		}
	}
	// The scripts IR runs there, and how receive sets go where an agent has one: with the words
	// about the agents (static[0]), the way to read more after the rest (static[1]).
	if len(c.Scripts) > 0 {
		var scripts []string
		for _, x := range c.Scripts {
			s := x.Name
			var about []string
			for _, v := range []string{x.Intro, x.On} {
				if v != "" {
					about = append(about, v)
				}
			}
			if len(about) > 0 {
				s += " (" + strings.Join(about, "; ") + ")"
			}
			scripts = append(scripts, s)
		}
		ps[0] = piece{parts: mustParts(ps[0], text(idChannelScripts, "scripts", strings.Join(scripts, ", ")))}
	}
	if c.Pull && slices.ContainsFunc(c.Agents, func(a ChannelAgent) bool { return a.Recv != "" }) {
		ps[0] = piece{parts: mustParts(ps[0], text(idChannelRecv))}
	}
	if c.Pull {
		ps = append(ps, piece{parts: mustParts(text(idChannelPull, "channel", self), text(idChannelReact, "channel", self))})
	}
	return ps
}

// awayExample is how an agent at place at writes a file of its own for the channel: its place
// with a file under it, in that computer's separator.
func awayExample(at string) string {
	sep := "/"
	if strings.Contains(at, `\`) {
		sep = `\`
	}
	return strings.TrimRight(at, `/\`) + sep + "notes.md"
}

// mustParts are pieces resolved into one piece's parts (a fragment missing: none).
func mustParts(ps ...piece) []Part {
	in, err := build(KindTurnPrefix, ps...)
	if err != nil {
		return []Part{}
	}
	return in.parts
}

// changePieces say which cards changed since the agent was last told.
func changePieces(c Channel) []piece {
	if len(c.Changes) == 0 {
		return nil
	}
	ps := []piece{text(idChannelProfiles)}
	for _, ch := range c.Changes {
		who := ch.Who
		if who == "" {
			who = "the channel"
		}
		switch {
		case ch.What == "name":
			ps = append(ps, text(idChannelProfileName, "who", who, "to", ch.To))
		case ch.What == "intro" && ch.To == "":
			ps = append(ps, text(idChannelProfileNoIntro, "who", who))
		case ch.What == "intro":
			ps = append(ps, text(idChannelProfileIntro, "who", who), words(ch.To, Ref{Kind: "profile", ID: "intro"}), text(idNL))
		case ch.What == "persona" && ch.To == "":
			ps = append(ps, text(idChannelProfileNoPers))
		case ch.What == "persona":
			ps = append(ps, text(idChannelProfilePersona), words(ch.To, Ref{Kind: "profile", ID: "persona"}), text(idNL))
		case ch.What == "avatar":
			ps = append(ps, text(idChannelProfileImage, "who", who, "what", "face"))
		default:
			ps = append(ps, text(idChannelProfileImage, "who", who, "what", "background"))
		}
	}
	return ps
}

// Quote is a message answered: its number, who said it, its words (cut short here).
type Quote struct {
	Seq  int64
	Who  string
	Text string
}

// QuoteCut is how many characters of a message answered are given.
const QuoteCut = 200

// Work is what the agent Who did over a stretch.
type Work struct {
	Who string
	Did activity.Activity
}

// WorkFiles is how many changed files a line of work names.
const WorkFiles = 12

// didPieces say a in few words: "bash 7 (go 4, git 3), 1 failed · 3 files +120 −14 · read 2".
func didPieces(a activity.Activity) []piece {
	var groups [][]piece
	if a.Bash > 0 {
		g := []piece{text(idDidBash, "n", strconv.Itoa(a.Bash))}
		if top := a.Top(3); len(top) > 0 {
			g = append(g, text(idDidCmds, "cmds", strings.Join(top, ", ")))
		}
		if a.Failed > 0 {
			g = append(g, text(idDidFailed, "n", strconv.Itoa(a.Failed)))
		}
		groups = append(groups, g)
	}
	if n := len(a.Files); n > 0 {
		add, del := a.Lines()
		groups = append(groups, []piece{text(idDidFiles, "n", strconv.Itoa(n), "add", strconv.Itoa(add), "del", strconv.Itoa(del))})
	}
	for _, x := range []struct {
		id string
		n  int
	}{{idDidRead, a.Read}, {idDidWeb, a.Search + a.Fetch}, {idDidAgents, a.Agents}, {idDidOther, a.Other}} {
		if x.n > 0 {
			groups = append(groups, []piece{text(x.id, "n", strconv.Itoa(x.n))})
		}
	}
	var ps []piece
	for i, g := range groups {
		if i > 0 {
			ps = append(ps, text(idDidSep))
		}
		ps = append(ps, g...)
	}
	return ps
}

// ChannelTurn is what an agent of a channel gets for message seq (said, the input it is):
// "#seq: " and the words, then the channel's context.
func ChannelTurn(c Channel, seq int64, said, input string) (Injection, error) {
	return build(KindTurnPrefix, channelPieces(c, seq, said, input)...)
}

func n(i int64) string { return strconv.FormatInt(i, 10) }

func channelPieces(c Channel, seq int64, said, input string) []piece {
	var refs []Ref
	if input != "" {
		refs = []Ref{{Kind: "input", ID: input}}
	}
	ps := []piece{text(idChannelMessage, "seq", n(seq))}
	if q := c.Quote; q != nil {
		ps = append(ps, text(idChannelQuote, "seq", n(q.Seq), "who", q.Who), words(cut(q.Text, QuoteCut), Ref{Kind: "message", ID: n(q.Seq)}), text(idChannelQuoteEnd))
	}
	ps = append(ps, words(said, append(refs, Ref{Kind: "message", ID: n(seq)})...))
	return append(ps, contextPieces(c)...)
}

// cut is words on one line, at most max characters of them.
func cut(words string, max int) string {
	w := strings.Join(strings.Fields(words), " ")
	if r := []rune(w); len(r) > max {
		w = string(r[:max]) + "…"
	}
	return w
}

// Reaction is an emoji put on message Seq (its words Text) by Who: what an agent that receives
// Who is given (docs/channels.md "表情").
type Reaction struct {
	Seq        int64
	Who, Emoji string
	Text       string
}

// reactionPieces are the line of a reaction: "#seq [who reacted 👍 to: words]".
func reactionPieces(r Reaction) []piece {
	return []piece{text(idChannelReaction, "seq", n(r.Seq), "who", r.Who, "emoji", r.Emoji), words(cut(r.Text, QuoteCut), Ref{Kind: "message", ID: n(r.Seq)}), text(idChannelReactionEnd)}
}

// ChannelReaction is what an agent of a channel gets for a reaction: its line, then the
// channel's context, as for a message.
func ChannelReaction(c Channel, r Reaction) (Injection, error) {
	return build(KindTurnPrefix, append(reactionPieces(r), contextPieces(c)...)...)
}

// Reacted is the line of a reaction alone: for the agent of a session that is its channel's only
// one, which is given no channel's context.
func Reacted(r Reaction) (Injection, error) {
	return build(KindMessage, reactionPieces(r)...)
}

// contextPieces are the channel's context after the words that call the agent.
func contextPieces(c Channel) []piece {
	// The channel as ir takes it: the agent's own is "self".
	self := cmp.Or(c.ID, "self")
	ps := []piece{text(idChannelOpen)}
	if c.Rename {
		ps = append(ps, text(idChannelRename, "you", c.You))
	}
	static := staticPieces(c)
	switch {
	case !c.Brief:
		ps = append(ps, static[0])
	case c.ID != "":
		ps = append(ps, text(idChannelAgain, "name", cmp.Or(c.Name, c.ID), "channel", c.ID))
	}
	ps = append(ps, changePieces(c)...)
	if len(c.Recalled) > 0 {
		var seqs []string
		for _, r := range c.Recalled {
			seqs = append(seqs, "#"+n(r))
		}
		ps = append(ps, text(idChannelRecalled, "seqs", strings.Join(seqs, ", ")))
	}
	ps = append(ps, unseenPieces(c, self)...)
	work := false
	for _, w := range c.Work {
		if w.Did.Empty() {
			continue
		}
		if !work {
			ps, work = append(ps, text(idChannelWork)), true
		}
		ps = append(append(ps, text(idChannelWorkLine, "who", w.Who)), didPieces(w.Did)...)
		if len(w.Did.Files) > 0 {
			var paths []string
			for i, f := range w.Did.Files {
				if i >= WorkFiles {
					paths = append(paths, "…")
					break
				}
				paths = append(paths, f.Path)
			}
			ps = append(ps, text(idChannelWorkFile, "files", strings.Join(paths, ", ")))
		}
		ps = append(ps, text(idNL))
	}
	if !c.Brief && len(static) > 1 {
		ps = append(ps, static[1])
	}
	return append(ps, text(idChannelClose))
}

// unseenPieces are what the agent has not seen of the channel, as c.Plan has it: how many early
// ones are not shown, turns in a line each, then the latest in full. self: the channel as ir
// takes it.
func unseenPieces(c Channel, self string) []piece {
	var ps []piece
	p := c.Plan
	if k := p.Skipped; k.Count > 0 {
		sk := text(idChannelSkipped, "from", n(k.From), "to", n(k.To), "count", strconv.Itoa(k.Count))
		sk.refs = []Ref{{Kind: "messages", From: n(k.From), To: n(k.To)}}
		ps = append(ps, sk)
	}
	if len(p.Turns) > 0 {
		ps = append(ps, text(idChannelListed))
		for _, t := range p.Turns {
			line := text(idChannelTurn, "from", n(t.From), "to", n(t.To), "who", t.Who, "head", t.Head)
			line.refs = []Ref{{Kind: "messages", From: n(t.From), To: n(t.To)}}
			ps = append(ps, line)
			if len(t.Answered) > 0 {
				ps = append(ps, text(idChannelAnswered, "who", strings.Join(t.Answered, ", ")))
			}
			ps = append(ps, text(idNL))
		}
	}
	if len(p.Full) > 0 {
		ps = append(ps, text(idChannelFull))
		zone := c.Zone
		if zone == nil {
			zone = time.UTC
		}
		for _, m := range p.Full {
			ps = append(ps, text(idChannelLine, "seq", n(m.Seq), "who", m.Who, "time", time.UnixMilli(m.At).In(zone).Format("01-02 15:04")))
			if did, ok := c.Did[m.Seq]; ok && !did.Empty() {
				ps = append(append(ps, text(idChannelLineDid)), didPieces(did)...)
			}
			ps = append(ps, text(idChannelLineEnd), words(m.Text, Ref{Kind: "message", ID: n(m.Seq)}))
			if m.Left > 0 {
				ps = append(ps, text(idChannelCut, "left", strconv.Itoa(m.Left), "seq", n(m.Seq), "channel", self))
			}
			ps = append(ps, text(idNL))
		}
	}
	return ps
}

// ChannelUnseen is what comes back to an agent that said something in a channel with ir
// (docs/channels.md "发送检查"): what was said there since it last read it. Empty when c.Plan is.
func ChannelUnseen(c Channel) (Injection, error) {
	if c.Plan.Skipped.Count == 0 && len(c.Plan.Turns) == 0 && len(c.Plan.Full) == 0 {
		return Injection{}, nil
	}
	return build(KindToolResult, append([]piece{text(idChannelSendUnseen)}, unseenPieces(c, cmp.Or(c.ID, "self"))...)...)
}

// ChannelBehind is what an agent's ir is refused with when it is too far behind the channel, or
// something it has not seen names it: count messages, and what they are; named: one names it.
func ChannelBehind(c Channel, count int, named bool) (Injection, error) {
	id := idChannelSendBehind
	if named {
		id = idChannelSendNamed
	}
	return build(KindToolResult, append([]piece{text(id, "count", strconv.Itoa(count))}, unseenPieces(c, cmp.Or(c.ID, "self"))...)...)
}

// ChannelSilent tells the agent that started another (nick) that it was called in channel name
// for message seq and its turn ended with nothing said there.
func ChannelSilent(nick, name string, seq int64) (Injection, error) {
	return build(KindMessage, text(idChannelSilent, "nick", nick, "name", name, "seq", n(seq)))
}
