package satori

import (
	"cmp"
	"regexp"
	"strings"
)

// A merged forward (Satori's <message forward>, docs/channels.md "元素"): <message>s, each with
// its <author>, its words, perhaps a forward of its own. Kept as written in what the channel
// keeps, for a page to draw as a card; an agent reads it as lines (forwardText).

// forwardHead is the line a forward begins with as an agent reads it.
const forwardHead = "[聊天记录]"

// forwardText is forward e as an agent reads it: forwardHead, then a line per message, "who:
// words", the words' further lines (a forward inside, too) set in by two spaces.
func forwardText(e Element, ref func(e Element) string) string {
	var b strings.Builder
	b.WriteString(forwardHead + "\n")
	for _, m := range e.Children {
		if m.Type != "message" {
			continue
		}
		who := ""
		var rest []Element
		for _, c := range m.Children {
			if c.Type == "author" {
				who = cmp.Or(c.Attr("name"), c.Attr("nick"), c.Attr("id"))
				continue
			}
			rest = append(rest, c)
		}
		words := Plain(rest, ref)
		if who == "" && words == "" {
			continue
		}
		lines := strings.Split(words, "\n")
		b.WriteString(cmp.Or(who, "?") + ": " + lines[0] + "\n")
		for _, l := range lines[1:] {
			b.WriteString("  " + l + "\n")
		}
	}
	return b.String()
}

var forwardOpen = regexp.MustCompile(`<message\b[^<>]*?\sforward\b[^<>]*>`)
var messageTag = regexp.MustCompile(`<(/?)message\b[^<>]*?(/?)>`)

// ForwardWords is text with each forward written in it (outside code) as an agent reads it
// (forwardText). What keeps a forward as written (a channel's log, from which a page draws it)
// gives an agent this.
func ForwardWords(text string) string {
	if !strings.Contains(text, "forward") {
		return text
	}
	var b strings.Builder
	at := 0
	for at < len(text) {
		loc := forwardOpen.FindStringIndex(text[at:])
		if loc == nil {
			break
		}
		from := at + loc[0]
		end := forwardEnd(text, at+loc[1])
		if end < 0 || inCode(text, from) {
			b.WriteString(text[at : at+loc[1]])
			at += loc[1]
			continue
		}
		b.WriteString(text[at:from])
		if b.Len() > 0 && !strings.HasSuffix(b.String(), "\n") {
			b.WriteByte('\n')
		}
		b.WriteString(Plain(Parse(text[from:end]), nil))
		at = end
		if at < len(text) && text[at] != '\n' {
			b.WriteByte('\n')
		}
	}
	b.WriteString(text[at:])
	return b.String()
}

// forwardEnd is where the <message> opened before from closes, past its </message>; -1 if it
// does not.
func forwardEnd(text string, from int) int {
	depth := 1
	for _, m := range messageTag.FindAllStringSubmatchIndex(text[from:], -1) {
		switch {
		case m[3] > m[2]: // </message>
			depth--
		case m[5] > m[4]: // <message …/>
		default:
			depth++
		}
		if depth == 0 {
			return from + m[1]
		}
	}
	return -1
}

// inCode: text's position at is in Markdown code (a fenced block, an inline span).
func inCode(text string, at int) bool {
	fenced := false
	for _, line := range strings.SplitAfter(text[:at], "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			fenced = !fenced
		}
	}
	if fenced {
		return true
	}
	last := strings.LastIndexByte(text[:at], '\n') + 1
	return strings.Count(text[last:at], "`")%2 == 1
}
