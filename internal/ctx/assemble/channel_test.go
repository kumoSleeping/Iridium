package assemble

import (
	"strings"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/activity"
	"github.com/kumoSleeping/iridium/internal/ctx/strategy"
)

func TestChannelTurn(t *testing.T) {
	var p strategy.Plan
	p.Skipped.From, p.Skipped.To, p.Skipped.Count = 1, 4, 4
	p.Turns = []strategy.Turn{{From: 5, To: 8, Who: "kumo", Head: "fix the build", Answered: []string{"claude"}}}
	p.Full = []strategy.Full{{Msg: strategy.Msg{Seq: 9, Who: "claude", Text: "done", At: 3600_000}}, {Msg: strategy.Msg{Seq: 10, Who: "kumo", Text: "long", At: 3660_000}, Left: 7}}
	did := activity.Activity{Bash: 7, Failed: 1, Cmds: map[string]int{"go": 4, "git": 3}, Read: 2, Search: 1, Files: []activity.File{{Path: "/w/a.go", Add: 120, Del: 14}, {Path: "/w/b.go", Add: 3}}}
	in, err := ChannelTurn(Channel{Session: "s-1", You: "codex", Agents: []ChannelAgent{{Nick: "claude", Agent: "claude", Model: "claude-opus-5-5", Effort: "high"}, {Nick: "codex"}}, Plan: p, Pull: true,
		Did: map[int64]activity.Activity{9: did}, Work: []Work{{"claude", did}, {"pi", activity.Activity{}}}}, 11, "@codex look", "in1")
	if err != nil {
		t.Fatal(err)
	}
	want := `#11: @codex look

<channel-context>
Channel: this session's own (s-1), shared with people and other agents | You: codex
Agents:
- claude (claude, claude-opus-5-5, effort high)
- codex

Rules:
- Reply directly: only what you write reaches the channel; your thinking and tool calls stay yours, and you do not see theirs.
- Before tools: one short line of what you're about to do (work folds under it).
- Mentions: ` + "`@Nick` calls an agent; `Nick` without `@` only names it. Between agents, `@` only when you need an answer — drop `@` once done, don't bounce acks or nag while waiting, and end your turn after `@`ing someone: its answer comes here, and one that answers you calls you." + `
- Keep it tight: don't restate visible context, IDs, paths or states.
- Interrupt: nothing to add (thanks/ack)? Write nothing and run ` + "`ir api POST internal/sessions/s-1/interrupt`" + `. If another agent here is clearly looping, off track or about to do harm (never one doing valid work), stop it with ` + "`ir api POST internal/sessions/<nick>/interrupt`" + ` and say why in one line.

Commands:
- History: ` + "`ir api message.list channel_id=s-1 [next=<n> limit=<n>]` (search: `_data.q=\"<words>\"`)" + `
- Full message: ` + "`ir api message.get channel_id=s-1 message_id=<n>`" + `
- React: ` + "`ir api reaction.create channel_id=s-1 message_id=<n> emoji=👍` (shown as `#<n> [who reacted 👍 to: …]`)" + `

#1–#4: 4 earlier messages, not shown.
Listed by turn (who asked, their first words, who answered):
#5–#8 kumo: fix the build (answered by claude)
In full, what was said since you last read the channel:
#9 [claude · 01-01 01:00 · bash 7 (go 4, git 3), 1 failed · 2 files +123 −14 · read 2 · web 1]: done
#10 [kumo · 01-01 01:01]: long […7 more characters: ir api message.get channel_id=s-1 message_id=10]
Work since you last read the channel (tool calls counted; files as changed by editing tools, not by shell commands):
claude: bash 7 (go 4, git 3), 1 failed · 2 files +123 −14 · read 2 · web 1 — /w/a.go, /w/b.go
</channel-context>`
	if got := in.String(); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	if in.Kind() != KindTurnPrefix {
		t.Error(in.Kind())
	}
	// Each part says where it is from; the words of others are cited, not kept.
	var cited int
	for _, part := range in.Record().Parts {
		if part.Source == SourceInput {
			cited++
			if len(part.Refs) == 0 || part.Values != nil {
				t.Errorf("words not cited: %+v", part)
			}
		}
	}
	if cited != 3 {
		t.Errorf("%d parts of words", cited)
	}
	// Nothing unseen: who is there, nothing else.
	in, _ = ChannelTurn(Channel{Session: "s-1", You: "a", Agents: []ChannelAgent{{Nick: "a"}}}, 1, "hi", "")
	if got := in.String(); got != "#1: hi\n\n<channel-context>\nChannel: this session's own, shared with people and other agents | You: a\nAgents:\n- a\n\nRules:\n- Reply directly: only what you write reaches the channel; your thinking and tool calls stay yours, and you do not see theirs.\n- Before tools: one short line of what you're about to do (work folds under it).\n- Mentions: `@Nick` calls an agent; `Nick` without `@` only names it. Between agents, `@` only when you need an answer — drop `@` once done, don't bounce acks or nag while waiting, and end your turn after `@`ing someone: its answer comes here, and one that answers you calls you.\n- Keep it tight: don't restate visible context, IDs, paths or states.\n</channel-context>" {
		t.Errorf("bare: %q", got)
	}
}

func TestChannelBriefSameChannel(t *testing.T) {
	// Consecutive turn in the same channel with nothing new: omit <channel-context> completely.
	in, err := ChannelTurn(Channel{Session: "s-1", ID: "ch-1", Name: "Room", You: "a", Brief: true, SameChannel: true}, 4, "hello", "in4")
	if err != nil {
		t.Fatal(err)
	}
	if got := in.String(); got != "#4: hello" {
		t.Errorf("same channel nothing new: got %q, want %q", got, "#4: hello")
	}

	// Same channel with something new (recalled): keep <channel-context> without repeating "In the channel ...".
	in, err = ChannelTurn(Channel{Session: "s-1", ID: "ch-1", Name: "Room", You: "a", Brief: true, SameChannel: true, Recalled: []int64{2}}, 5, "next", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := in.String(); strings.Contains(got, "In the channel") || strings.Contains(got, "In channel") || !strings.Contains(got, "<channel-context>\nTaken back") {
		t.Errorf("same channel with recalled: %q", got)
	}

	// Switching back to a named channel: include "In the channel \"Room\" (ch-1), as before."
	in, err = ChannelTurn(Channel{Session: "s-1", ID: "ch-1", Name: "Room", You: "a", Brief: true, SameChannel: false}, 6, "back", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := in.String(); !strings.Contains(got, `In the channel "Room" (ch-1), as before.`) {
		t.Errorf("switched named channel: %q", got)
	}

	// Switching back to an unnamed channel (Name empty or == ID): do not repeat the UUID twice.
	for _, name := range []string{"", "ch-1"} {
		in, err = ChannelTurn(Channel{Session: "s-1", ID: "ch-1", Name: name, You: "a", Brief: true, SameChannel: false}, 7, "back", "")
		if err != nil {
			t.Fatal(err)
		}
		if got := in.String(); !strings.Contains(got, "In channel ch-1, as before.") || strings.Contains(got, "(ch-1)") {
			t.Errorf("switched unnamed channel (%q): %q", name, got)
		}
	}
}

// A reaction (docs/channels.md "表情"): its line, the message's words cut to a line, then the
// channel's context, the messages taken back since in it; alone, the line only.
func TestChannelReaction(t *testing.T) {
	r := Reaction{Seq: 7, Who: "kumo", Emoji: "👍", Text: "the tests\npass " + strings.Repeat("x", 300)}
	in, err := ChannelReaction(Channel{Session: "s-1", You: "a", Agents: []ChannelAgent{{Nick: "a"}}, Brief: true, Recalled: []int64{3, 5}}, r)
	if err != nil {
		t.Fatal(err)
	}
	got := in.String()
	if !strings.HasPrefix(got, "#7 [kumo reacted 👍 to: the tests pass xxx") || !strings.Contains(got, "…]\n\n<channel-context>\nTaken back since you last read the channel, gone for everyone: #3, #5.\n") {
		t.Errorf("got %q", got)
	}
	alone, _ := Reacted(Reaction{Seq: 2, Who: "kumo", Emoji: "🎉", Text: "done"})
	if alone.String() != "#2 [kumo reacted 🎉 to: done]" {
		t.Errorf("alone: %q", alone.String())
	}
}

func TestChannelOverhear(t *testing.T) {
	at := time.Date(2026, 1, 1, 1, 2, 0, 0, time.UTC).UnixMilli()
	in, err := ChannelOverhear(Overhear{Channel: "ch-1", Session: "s-1", Seq: 12, Who: "kumo", At: at, Text: "hover also", Pull: true})
	if err != nil {
		t.Fatal(err)
	}
	got := in.String()
	for _, want := range []string{"<channel-overhear>", "did not @ you", "internal/sessions/s-1/interrupt", "#12 [kumo · 01-01 01:02]: hover also", "</channel-overhear>"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}

	// Long message clipped to OverhearCut with message.get pointer.
	long := strings.Repeat("a", OverhearCut+25)
	in, err = ChannelOverhear(Overhear{Channel: "ch-1", Session: "s-1", Seq: 13, Who: "kumo", At: at, Text: long, Pull: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := in.String(); !strings.Contains(got, "[…25 more characters: ir api message.get channel_id=ch-1 message_id=13]") {
		t.Errorf("clipped overhear: %q", got)
	}

	// Bare agent (no ir): no interrupt/message.get hint, ellipsis on clip.
	in, err = ChannelOverhear(Overhear{Channel: "s-1", Session: "s-1", Seq: 14, Who: "kumo", At: at, Text: long, Pull: false})
	if err != nil {
		t.Fatal(err)
	}
	if got := in.String(); strings.Contains(got, "ir api") || !strings.Contains(got, "…\n</channel-overhear>") {
		t.Errorf("bare overhear: %q", got)
	}
}

// Where agents work (the user, 2026-11-18): said only when one is not in the channel's folder.
func TestChannelPlace(t *testing.T) {
	has := func(c Channel, want string, in bool) {
		t.Helper()
		if got := ChannelStatic(c); strings.Contains(got, want) != in {
			t.Errorf("%q in it: %v, want %v:\n%s", want, !in, in, got)
		}
	}
	home := Channel{Session: "s-1", You: "a", Place: "host:/w/ir", Agents: []ChannelAgent{{Nick: "a"}, {Nick: "b"}}}
	has(home, "Folder:", false)
	// Another works elsewhere: the one at home is told where the channel is and where that one is.
	other := home
	other.Agents = []ChannelAgent{{Nick: "a"}, {Nick: "b", At: `win:C:\Code\x`}}
	has(other, "Agents:\n- a\n- b (at win:C:\\Code\\x)\n", true)
	has(other, "Folder: host:/w/ir;", true)
	has(other, "You work at", false)
	// The one called works elsewhere itself: how it writes a file of its own.
	away := other
	away.You, away.At = "b", `win:C:\Code\x`
	has(away, `You work at win:C:\Code\x:`, true)
	has(away, `(win:C:\Code\x\notes.md)`, true)
	away.At = "host:/w/other/"
	has(away, "(host:/w/other/notes.md)", true)
	// Where an agent works is part of what the standing words are of.
	if ChannelStaticKey(home) == ChannelStaticKey(away) {
		t.Error("the key does not see the place")
	}
}

func TestWhere(t *testing.T) {
	sys := func(w Where) string {
		in, err := System(w)
		if err != nil {
			t.Fatal(err)
		}
		return in.String()
	}
	if got := sys(Where{Folder: "/w/ir", OS: "linux"}); !strings.Contains(got, "## Runtime\n- Host: Linux, `/w/ir`\n") {
		t.Error(got)
	}
	got := sys(Where{Remote: true, IR: true, Folder: `C:\Code\x`, OS: "windows", Computer: "otae-win"})
	if !strings.Contains(got, "- Computer: otae-win (Windows), `C:\\Code\\x` — not the host.") {
		t.Error(got)
	}
	if got := sys(Where{}); strings.Contains(got, "## Runtime") {
		t.Error("said without a folder")
	}
}
