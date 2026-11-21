package channel

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/kumoSleeping/iridium/internal/store"
)

// Taking a message back, and reactions (docs/channels.md "撤回" "表情"): any member who may speak
// in a channel may take back any message said there (a person's, an agent's words), and put an
// emoji on any. Asks and Iridium's own lines keep their own course.

// EventRecall is the type of the line a message taken back leaves (Event: Seq, its number;
// Member, who took it back; By, by name).
const EventRecall = "recall"

var (
	// ErrNoMessage: no such message in the channel (or it is gone).
	ErrNoMessage = errors.New("no such message")
	// ErrNotWords: the message is an ask or a line of Iridium's own: not taken back.
	ErrNotWords = errors.New("only what a member said is taken back: an ask or a line of Iridium's own is not")
)

// Recallable: m is what a member said (a person's words, an agent's, a script's), which may be
// taken back.
func Recallable(m Message) bool { return m.Kind == KindUser || m.Kind == KindSaid }

// Recall takes message seq of channel back for everyone: its words, its reactions, the marks on
// it go; its number stays. by: who took it back, by name. It gives the message as it was.
func (s *Service) Recall(channel string, seq int64, by string) (Message, error) {
	m, ok := s.Get(channel, seq)
	if !ok {
		return Message{}, ErrNoMessage
	}
	if !Recallable(m) {
		return Message{}, ErrNotWords
	}
	done, err := s.st.RecallChannelMessage(channel, seq, by)
	if err != nil {
		return Message{}, err
	}
	if !done {
		return Message{}, ErrNoMessage
	}
	return m, nil
}

// Recalls are the messages of channel taken back after at (ms).
func (s *Service) Recalls(channel string, after int64) []store.Recall {
	rs, err := s.st.Recalls(channel, after)
	if err != nil {
		s.logf("channel %s: what was taken back: %v", channel, err)
	}
	return rs
}

// RecalledRefs are what channel's messages taken back were from (store.RecalledRefs).
func (s *Service) RecalledRefs(channel string) (map[string]bool, int64) {
	refs, latest, err := s.st.RecalledRefs(channel)
	if err != nil {
		s.logf("channel %s: what was taken back: %v", channel, err)
	}
	return refs, latest
}

// ---- reactions ----

// ErrEmoji: not one emoji.
var ErrEmoji = errors.New("emoji: one Unicode emoji")

// Emoji is e if it is one Unicode emoji (one grapheme: a pictograph with its modifiers,
// variation selectors and joined parts, a flag, a keycap), trimmed; ErrEmoji if not.
func Emoji(e string) (string, error) {
	e = strings.TrimSpace(e)
	if e == "" || len(e) > 64 || !utf8.ValidString(e) {
		return "", ErrEmoji
	}
	rs := []rune(e)
	// A flag: two regional indicators.
	if len(rs) == 2 && regional(rs[0]) && regional(rs[1]) {
		return e, nil
	}
	// A keycap: a digit, # or *, maybe U+FE0F, then U+20E3.
	if n := len(rs); n >= 2 && rs[n-1] == 0x20E3 && strings.ContainsRune("0123456789#*", rs[0]) && (n == 2 || n == 3 && rs[1] == 0xFE0F) {
		return e, nil
	}
	pict := false
	joined := true // the next rune may begin a pictograph: at the start, after a ZWJ
	for i, r := range rs {
		switch {
		case r == 0x200D: // zero width joiner
			if joined || i == len(rs)-1 {
				return "", ErrEmoji
			}
			joined = true
		case r == 0xFE0F || r == 0xFE0E, r >= 0x1F3FB && r <= 0x1F3FF, r >= 0xE0020 && r <= 0xE007F:
			// variation selectors, skin tones, a tag sequence's tags
			if i == 0 {
				return "", ErrEmoji
			}
		case pictograph(r):
			if !joined {
				return "", ErrEmoji
			}
			pict, joined = true, false
		default:
			return "", ErrEmoji
		}
	}
	if !pict {
		return "", ErrEmoji
	}
	return e, nil
}

func regional(r rune) bool { return r >= 0x1F1E6 && r <= 0x1F1FF }

// pictograph: r is one of Unicode's emoji pictographs (roughly Extended_Pictographic, and the
// older symbols that are emoji with U+FE0F).
func pictograph(r rune) bool {
	switch {
	case regional(r):
		return false
	case r >= 0x1F000 && r <= 0x1FAFF, r >= 0x2600 && r <= 0x27BF, r >= 0x2B00 && r <= 0x2BFF, r >= 0x2300 && r <= 0x23FF,
		r >= 0x2190 && r <= 0x21FF, r >= 0x25A0 && r <= 0x25FF, r >= 0x2934 && r <= 0x2935, r >= 0x3030 && r <= 0x303D,
		r == 0x00A9, r == 0x00AE, r == 0x203C, r == 0x2049, r == 0x2122, r == 0x2139, r == 0x24C2, r == 0x3297, r == 0x3299:
		return !unicode.IsSpace(r)
	}
	return false
}

// React puts member's emoji (by name) on message seq of channel; added false if it was there.
func (s *Service) React(channel string, seq int64, emoji, member, name string) (bool, error) {
	if _, ok := s.Get(channel, seq); !ok {
		return false, ErrNoMessage
	}
	return s.st.AddReaction(store.Reaction{Channel: channel, Seq: seq, Emoji: emoji, Member: member, Name: name})
}

// Unreact takes member's emoji off message seq of channel; false if it had none.
func (s *Service) Unreact(channel string, seq int64, emoji, member string) (bool, error) {
	return s.st.RemoveReaction(channel, seq, emoji, member)
}

// ClearReactions takes every reaction (of emoji; "": of every one) off message seq: those it took.
func (s *Service) ClearReactions(channel string, seq int64, emoji string) ([]store.Reaction, error) {
	return s.st.ClearReactions(channel, seq, emoji)
}

// Reactions are the reactions on the messages seqs of channel, by message.
func (s *Service) Reactions(channel string, seqs []int64) map[int64][]store.Reaction {
	by, err := s.st.Reactions(channel, seqs)
	if err != nil {
		s.logf("channel %s: reactions: %v", channel, err)
	}
	return by
}

// Numbers are the numbers in channel of the messages that are from refs (as far as it has read
// its record in), by ref.
func (s *Service) Numbers(channel string, refs []string) map[string]int64 {
	out, err := s.st.SeqsByRef(channel, refs)
	if err != nil {
		s.logf("channel %s: numbers: %v", channel, err)
	}
	return out
}
