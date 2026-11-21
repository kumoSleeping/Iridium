// Package deliver decides who a message said in a channel wakes (docs/channels.md "投递"): pure
// rules over the message's words, who said it and the channel's agents. It sends nothing.
package deliver

import (
	"strings"
	"unicode"
)

// MaxHops is how many times in a row agents (and the scripts they set off) may wake one another
// since a person last spoke, by naming or by a receive set alike (the user, 2026-11-17).
const MaxHops = 8

// Agent is an agent of the channel: the session it is, the nick it is called by, and its receive
// set (Recv): the senders whose messages that name nobody wake it (Receives).
type Agent struct {
	Session string
	Nick    string
	Recv    []string
}

// From is who said a message: Member, as a receive set names it ("agent:<session>", a login
// "<app>/<login>"); Session, an agent's. Quiet: it wakes only whom it names, nobody by a receive
// set (a script's words; an agent's words on its way, not its turn's last).
type From struct {
	Member  string
	Session string
	Quiet   bool
}

// Decision is what a message does. Wake: the sessions it is delivered to, in the channel's
// order; Named: the nicks it names (itself aside).
type Decision struct {
	Wake  []string
	Named []string
}

// The two names that call more than one (the user, 2026-11-13): "@全体成员", everyone in the
// channel, and "@全体Agent", its agents. For the agents both are the same: each is woken as if
// named. No nick may be one of them.
const (
	AllMembers = "全体成员"
	AllAgents  = "全体Agent"
)

// Reserved: n is one of the names that call more than one, whatever its case.
func Reserved(n string) bool {
	return strings.EqualFold(n, AllMembers) || strings.EqualFold(n, AllAgents)
}

// Route decides for a message with words text, said by from. One that names agents (@nick,
// @全体…) wakes them alone; one that names nobody wakes the agents whose receive set has its
// sender (Receives), unless it is quiet. The sender is never woken by its own words. How many in
// a row may pass a message on (MaxHops) is counted by the channel.
func Route(text string, from From, agents []Agent) Decision {
	var nicks []string
	for _, a := range agents {
		if a.Session != from.Session || from.Session == "" {
			nicks = append(nicks, a.Nick)
		}
	}
	var d Decision
	d.Named = Mentions(text, nicks)
	if all := Mentions(text, []string{AllMembers, AllAgents}); len(all) > 0 {
		d.Named = nicks // all of them, as if each were named
	}
	named := map[string]bool{}
	for _, n := range d.Named {
		named[strings.ToLower(n)] = true
	}
	for _, a := range agents {
		if from.Session != "" && a.Session == from.Session {
			continue
		}
		if len(d.Named) > 0 {
			if named[strings.ToLower(a.Nick)] {
				d.Wake = append(d.Wake, a.Session)
			}
		} else if !from.Quiet && from.Member != "" && Receives(a.Recv, from.Member) {
			d.Wake = append(d.Wake, a.Session)
		}
	}
	return d
}

// Reacts are the agents a reaction by from is given (docs/channels.md "表情"): those whose
// receive set has from, as for words that name nobody; never from itself. A reaction names
// nobody: @ in an emoji calls no one.
func Reacts(from From, agents []Agent) []string {
	var out []string
	for _, a := range agents {
		if from.Session != "" && a.Session == from.Session {
			continue
		}
		if from.Member != "" && Receives(a.Recv, from.Member) {
			out = append(out, a.Session)
		}
	}
	return out
}

// Receives: an agent with receive set is woken by a message of sender ("agent:<session>", a
// login "<app>/<login>") that names nobody.
func Receives(set []string, sender string) bool {
	agent := strings.HasPrefix(sender, "agent:")
	for _, e := range set {
		switch {
		case e == sender:
			return true
		case agent:
		case e == "*":
			return true
		case strings.HasSuffix(e, "/*") && strings.HasPrefix(sender, strings.TrimSuffix(e, "*")):
			return true
		}
	}
	return false
}

// ValidNick: a nick is letters, digits, "_" and "-", at most 32 of them, so that "@nick"
// stands out of any words; not one of the names that call more than one.
func ValidNick(n string) bool {
	if n == "" || len([]rune(n)) > 32 || Reserved(n) {
		return false
	}
	for _, r := range n {
		if !nickRune(r) {
			return false
		}
	}
	return true
}

// NickFrom is a nick made of name (a session's name): its letters, digits, _ and -, at most
// 32; "" if that is no nick.
func NickFrom(name string) string {
	var b strings.Builder
	n := 0
	for _, r := range name {
		if nickRune(r) && n < 32 {
			b.WriteRune(r)
			n++
		}
	}
	if !ValidNick(b.String()) {
		return ""
	}
	return b.String()
}

func nickRune(r rune) bool {
	return r == '_' || r == '-' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// Mentions are the nicks (of those given, as given, each once, in the order named) that text
// calls: "@nick" standing by itself (not inside a word, an address or a longer name), outside
// code (a fenced block, an inline span) and outside quoted lines ("> "). Case does not matter.
func Mentions(text string, nicks []string) []string {
	if len(nicks) == 0 || !strings.Contains(text, "@") {
		return nil
	}
	by := map[string]string{}
	for _, n := range nicks {
		by[strings.ToLower(n)] = n
	}
	var out []string
	seen := map[string]bool{}
	fenced := false
	for _, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			fenced = !fenced
			continue
		}
		if fenced || strings.HasPrefix(t, ">") {
			continue
		}
		rs := []rune(line)
		code := false
		for i := 0; i < len(rs); i++ {
			switch {
			case rs[i] == '`':
				code = !code
			case rs[i] == '@' && !code && (i == 0 || !nickRune(rs[i-1]) && rs[i-1] != '@' && rs[i-1] != '/' && rs[i-1] != '.'):
				j := i + 1
				for j < len(rs) && nickRune(rs[j]) {
					j++
				}
				if n, ok := by[strings.ToLower(string(rs[i+1:j]))]; ok && !seen[n] {
					seen[n] = true
					out = append(out, n)
				}
				i = j - 1
			}
		}
	}
	return out
}
