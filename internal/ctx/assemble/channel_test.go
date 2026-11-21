package assemble

import (
	"strings"
	"testing"

	"github.com/kumoSleeping/iridium/internal/activity"
	"github.com/kumoSleeping/iridium/internal/ctx/strategy"
)

func TestChannelTurn(t *testing.T) {
	var p strategy.Plan
	p.Skipped.From, p.Skipped.To, p.Skipped.Count = 1, 4, 4
	p.Turns = []strategy.Turn{{From: 5, To: 8, Who: "kumo", Head: "fix the build", Answered: []string{"claude"}}}
	p.Full = []strategy.Full{{Msg: strategy.Msg{Seq: 9, Who: "claude", Text: "done", At: 3600_000}}, {Msg: strategy.Msg{Seq: 10, Who: "kumo", Text: "long", At: 3660_000}, Left: 7}}
	did := activity.Activity{Bash: 7, Failed: 1, Cmds: map[string]int{"go": 4, "git": 3}, Read: 2, Search: 1, Files: []activity.File{{Path: "/w/a.go", Add: 120, Del: 14}, {Path: "/w/b.go", Add: 3}}}
	in, err := ChannelTurn(Channel{You: "codex", Agents: []ChannelAgent{{Nick: "claude", Name: "Claude Opus"}, {Nick: "codex"}}, Plan: p, Pull: true,
		Did: map[int64]activity.Activity{9: did}, Work: []Work{{"claude", did}, {"pi", activity.Activity{}}}}, 11, "@codex look", "in1")
	if err != nil {
		t.Fatal(err)
	}
	want := `#11: @codex look

<channel-context>
You are codex in a channel shared with people and other agents. Its agents: claude (Claude Opus), codex. Only what you say reaches the channel: your thinking and tool calls stay yours, and you do not see theirs. To call another agent, write @ and its name in what you say; without that no agent is called. Between agents, @ another only when you need its answer; when the exchange has said what it needs to, reply without @ and it ends there (agents passing it on without a person stop after a few turns anyway). After you @ an agent, end your turn: its answer comes in the channel, and one that answers you calls you. Do not restate what the channel already shows, nor ids, folders or states. To name one without calling it, write its name without @. Called with nothing to say (thanks, an acknowledgement): end your turn with ir api POST internal/sessions/self/interrupt; nothing is said for you.
#1–#4: 4 earlier messages, not shown.
Listed by turn (who asked, their first words, who answered):
#5–#8 kumo: fix the build (answered by claude)
In full, what was said since you last read the channel:
#9 [claude · 01-01 01:00 · bash 7 (go 4, git 3), 1 failed · 2 files +123 −14 · read 2 · web 1]: done
#10 [kumo · 01-01 01:01]: long […7 more characters: ir api message.get channel_id=self message_id=10]
Work since you last read the channel (tool calls counted; files as changed by editing tools, not by shell commands):
claude: bash 7 (go 4, git 3), 1 failed · 2 files +123 −14 · read 2 · web 1 — /w/a.go, /w/b.go
More of the channel: ir api message.list channel_id=self [next=<number> limit=<n>]; by words: ir api message.list channel_id=self _data.q="<words>"; one message whole: ir api message.get channel_id=self message_id=<number>.
React to a message rather than answer it in words: ir api reaction.create channel_id=self message_id=<number> emoji=👍. A line "#<number> [who reacted 👍 to: …]" is someone's reaction to that message.
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
	in, _ = ChannelTurn(Channel{You: "a", Agents: []ChannelAgent{{Nick: "a"}}}, 1, "hi", "")
	if got := in.String(); got != "#1: hi\n\n<channel-context>\nYou are a in a channel shared with people and other agents. Its agents: a. Only what you say reaches the channel: your thinking and tool calls stay yours, and you do not see theirs. To call another agent, write @ and its name in what you say; without that no agent is called. Between agents, @ another only when you need its answer; when the exchange has said what it needs to, reply without @ and it ends there (agents passing it on without a person stop after a few turns anyway). After you @ an agent, end your turn: its answer comes in the channel, and one that answers you calls you. Do not restate what the channel already shows, nor ids, folders or states. To name one without calling it, write its name without @. Called with nothing to say (thanks, an acknowledgement): end your turn with ir api POST internal/sessions/self/interrupt; nothing is said for you.\n</channel-context>" {
		t.Errorf("bare: %q", got)
	}
}

// A reaction (docs/channels.md "表情"): its line, the message's words cut to a line, then the
// channel's context, the messages taken back since in it; alone, the line only.
func TestChannelReaction(t *testing.T) {
	r := Reaction{Seq: 7, Who: "kumo", Emoji: "👍", Text: "the tests\npass " + strings.Repeat("x", 300)}
	in, err := ChannelReaction(Channel{You: "a", Agents: []ChannelAgent{{Nick: "a"}}, Brief: true, Recalled: []int64{3, 5}}, r)
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

// Where agents work (the user, 2026-11-18): said only when one is not in the channel's folder.
func TestChannelPlace(t *testing.T) {
	has := func(c Channel, want string, in bool) {
		t.Helper()
		if got := ChannelStatic(c); strings.Contains(got, want) != in {
			t.Errorf("%q in it: %v, want %v:\n%s", want, !in, in, got)
		}
	}
	home := Channel{You: "a", Place: "host:/w/ir", Agents: []ChannelAgent{{Nick: "a"}, {Nick: "b"}}}
	has(home, "The channel's folder", false)
	// Another works elsewhere: the one at home is told where the channel is and where that one is.
	other := home
	other.Agents = []ChannelAgent{{Nick: "a"}, {Nick: "b", At: `win:C:\Code\x`}}
	has(other, `Its agents: a, b (at win:C:\Code\x).`, true)
	has(other, "The channel's folder is host:/w/ir;", true)
	has(other, "You are one of those", false)
	// The one called works elsewhere itself: how it writes a file of its own.
	away := other
	away.You, away.At = "b", `win:C:\Code\x`
	has(away, `you work at win:C:\Code\x.`, true)
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
	if got := sys(Where{Folder: "/w/ir", OS: "linux"}); !strings.Contains(got, "Where: you run at the host (Linux), in /w/ir.\n") {
		t.Error(got)
	}
	got := sys(Where{Remote: true, IR: true, Folder: `C:\Code\x`, OS: "windows", Computer: "otae-win"})
	if !strings.Contains(got, `Where: you run at otae-win (Windows), in C:\Code\x: another computer than the host.`) {
		t.Error(got)
	}
	if got := sys(Where{}); strings.Contains(got, "Where:") {
		t.Error("said without a folder")
	}
}
