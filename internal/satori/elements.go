package satori

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// Message elements (Satori's protocol/message.md, protocol/elements.md): XHTML-like text with
// tags. Iridium sends text, <sharp> for a session, <a> for a place or a file link (never <at>: it
// would notify someone), <img>/<file> for files by internal: URL, and <button> for an ask's
// options; it reads all of them back, and anything else by its children.

// Element is one message element. Type "text" is plain text, in Attrs["content"]; any other is a
// tag with its attributes in order and its children.
type Element struct {
	Type     string
	Attrs    []Attr
	Children []Element
}

// Attr is an attribute; Bare is one written without a value (`<tag foo/>`).
type Attr struct {
	Key, Value string
	Bare       bool
}

// Text is a text element.
func Text(s string) Element { return Element{Type: "text", Attrs: []Attr{{Key: "content", Value: s}}} }

// Tag is an element with attributes given as key, value pairs.
func Tag(typ string, kv []string, children ...Element) Element {
	e := Element{Type: typ, Children: children}
	for i := 0; i+1 < len(kv); i += 2 {
		e.Attrs = append(e.Attrs, Attr{Key: kv[i], Value: kv[i+1]})
	}
	return e
}

// Attr is the value of attribute key, "" if it has none.
func (e Element) Attr(key string) string {
	for _, a := range e.Attrs {
		if a.Key == key {
			return a.Value
		}
	}
	return ""
}

// Has: e has attribute key.
func (e Element) Has(key string) bool {
	for _, a := range e.Attrs {
		if a.Key == key {
			return true
		}
	}
	return false
}

// Escape escapes text for a message: & < > and ".
func Escape(s string) string {
	if !strings.ContainsAny(s, `&<>"`) {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			b.WriteString("&quot;")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Unescape undoes Escape, and decimal and hexadecimal character references; an unknown or broken
// reference stays as written.
func Unescape(s string) string {
	if !strings.Contains(s, "&") {
		return s
	}
	var b strings.Builder
	for {
		i := strings.IndexByte(s, '&')
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:i])
		s = s[i:]
		j := strings.IndexByte(s, ';')
		if j < 0 || j > 12 {
			b.WriteByte('&')
			s = s[1:]
			continue
		}
		name := s[1:j]
		if r, ok := entity(name); ok {
			b.WriteRune(r)
			s = s[j+1:]
			continue
		}
		b.WriteByte('&')
		s = s[1:]
	}
}

func entity(name string) (rune, bool) {
	switch name {
	case "amp":
		return '&', true
	case "lt":
		return '<', true
	case "gt":
		return '>', true
	case "quot":
		return '"', true
	case "apos":
		return '\'', true
	}
	if num, ok := strings.CutPrefix(name, "#"); ok && num != "" {
		base := 10
		if h, ok := strings.CutPrefix(num, "x"); ok {
			num, base = h, 16
		} else if h, ok := strings.CutPrefix(num, "X"); ok {
			num, base = h, 16
		}
		n, err := strconv.ParseUint(num, base, 32)
		if err != nil || num == "" || !utf8.ValidRune(rune(n)) {
			return 0, false
		}
		return rune(n), true
	}
	return 0, false
}

// String encodes elements as a message.
func String(els []Element) string {
	var b strings.Builder
	for _, e := range els {
		write(&b, e)
	}
	return b.String()
}

func write(b *strings.Builder, e Element) {
	if e.Type == "text" {
		b.WriteString(Escape(e.Attr("content")))
		return
	}
	b.WriteByte('<')
	b.WriteString(e.Type)
	for _, a := range e.Attrs {
		b.WriteByte(' ')
		b.WriteString(a.Key)
		if !a.Bare {
			b.WriteString(`="`)
			b.WriteString(Escape(a.Value))
			b.WriteByte('"')
		}
	}
	if len(e.Children) == 0 {
		b.WriteString("/>")
		return
	}
	b.WriteByte('>')
	for _, c := range e.Children {
		write(b, c)
	}
	b.WriteString("</")
	b.WriteString(e.Type)
	b.WriteByte('>')
}

// token is a piece of a message as Parse reads it: text, or a tag.
type token struct {
	text  string // raw text (a tag that does not parse is text too)
	tag   bool
	name  string
	close bool // </name>
	empty bool // <name/>
	attrs []Attr
	raw   string // the tag as written, for when it is left unpaired
}

// Parse reads a message into elements. It never fails: what is not a well-formed tag, and a tag
// left without its pair, is text.
func Parse(s string) []Element {
	toks := lex(s)
	return build(toks, pairs(toks), 0, len(toks))
}

// pairs matches start tags with their end tags: match[i] is the end tag of start tag i, -1 for
// none. An end tag closes the latest start tag of its name still open, and leaves those opened
// after that one unpaired. Linear: each start tag is pushed and popped once.
func pairs(toks []token) []int {
	match := make([]int, len(toks))
	var open []int
	byName := map[string][]int{}
	for i, t := range toks {
		match[i] = -1
		if !t.tag || t.empty {
			continue
		}
		if !t.close {
			open = append(open, i)
			byName[t.name] = append(byName[t.name], i)
			continue
		}
		st := byName[t.name]
		if len(st) == 0 {
			continue
		}
		k := st[len(st)-1]
		for {
			top := open[len(open)-1]
			open = open[:len(open)-1]
			n := toks[top].name
			byName[n] = byName[n][:len(byName[n])-1]
			if top == k {
				break
			}
		}
		match[k] = i
	}
	return match
}

// build reads the elements of toks[lo:hi].
func build(toks []token, match []int, lo, hi int) []Element {
	var out []Element
	for i := lo; i < hi; i++ {
		t := toks[i]
		switch {
		case !t.tag:
			out = append(out, Text(t.text))
		case t.empty:
			out = append(out, Element{Type: t.name, Attrs: t.attrs})
		case !t.close && match[i] >= 0:
			out = append(out, Element{Type: t.name, Attrs: t.attrs, Children: build(toks, match, i+1, match[i])})
			i = match[i]
		default: // unpaired
			out = append(out, Text(Unescape(t.raw)))
		}
	}
	return merge(out)
}

// merge joins adjacent text elements, drops empty ones, and trims whitespace with a line break at
// either end of text (Satori: blank lines around text are layout, not content).
func merge(els []Element) []Element {
	var out []Element
	var text strings.Builder
	flush := func() {
		if text.Len() > 0 {
			if t := trimBreaks(text.String()); t != "" {
				out = append(out, Text(t))
			}
			text.Reset()
		}
	}
	for _, e := range els {
		if e.Type == "text" {
			text.WriteString(e.Attr("content"))
			continue
		}
		flush()
		out = append(out, e)
	}
	flush()
	return out
}

func trimBreaks(s string) string {
	if i := strings.IndexFunc(s, notSpace); i < 0 {
		if strings.Contains(s, "\n") {
			return ""
		}
		return s
	} else if strings.Contains(s[:i], "\n") {
		s = s[i:]
	}
	if i := strings.LastIndexFunc(s, notSpace); i >= 0 {
		_, n := utf8.DecodeRuneInString(s[i:])
		if strings.Contains(s[i+n:], "\n") {
			s = s[:i+n]
		}
	}
	return s
}

func notSpace(r rune) bool { return r != ' ' && r != '\t' && r != '\n' && r != '\r' }

// lex splits s into text and tags; comments are dropped.
func lex(s string) []token {
	var out []token
	var text strings.Builder
	var noQuote [2]bool // no " (') is left: a quoted value opened now never closes
	flush := func() {
		if text.Len() > 0 {
			out = append(out, token{text: Unescape(text.String())})
			text.Reset()
		}
	}
	for len(s) > 0 {
		i := strings.IndexByte(s, '<')
		if i < 0 {
			text.WriteString(s)
			break
		}
		text.WriteString(s[:i])
		s = s[i:]
		if strings.HasPrefix(s, "<!--") {
			end := strings.Index(s[4:], "-->")
			if end < 0 {
				s = "" // a comment never closed runs to the end
				break
			}
			s = s[4+end+3:]
			continue
		}
		t, n, ok := lexTag(s, &noQuote)
		if !ok {
			text.WriteByte('<')
			s = s[1:]
			continue
		}
		flush()
		out = append(out, t)
		s = s[n:]
	}
	flush()
	return out
}

// lexTag reads the tag at the start of s ('<' first): it, its length, ok.
func lexTag(s string, noQuote *[2]bool) (token, int, bool) {
	t := token{tag: true}
	i := 1
	if i < len(s) && s[i] == '/' {
		t.close = true
		i++
	}
	start := i
	for i < len(s) && (isLower(s[i]) || isDigit(s[i]) || s[i] == '-' || s[i] == ':' || s[i] == '_' || (i > start && isUpper(s[i]))) {
		i++
	}
	if i == start || !isLetter(s[start]) {
		return t, 0, false
	}
	t.name = s[start:i]
	for {
		for i < len(s) && isSpace(s[i]) {
			i++
		}
		if i >= len(s) {
			return t, 0, false
		}
		switch {
		case s[i] == '>':
			t.raw = s[:i+1]
			return t, i + 1, true
		case s[i] == '/' && i+1 < len(s) && s[i+1] == '>' && !t.close:
			t.empty = true
			t.raw = s[:i+2]
			return t, i + 2, true
		case t.close:
			return t, 0, false
		}
		ks := i
		for i < len(s) && !isSpace(s[i]) && s[i] != '=' && s[i] != '>' && s[i] != '/' && s[i] != '"' && s[i] != '\'' && s[i] != '<' {
			i++
		}
		if i == ks {
			return t, 0, false
		}
		a := Attr{Key: s[ks:i]}
		if i < len(s) && s[i] == '=' {
			i++
			if i >= len(s) || (s[i] != '"' && s[i] != '\'') {
				// key=value without quotes: up to a space or the tag's end
				vs := i
				for i < len(s) && !isSpace(s[i]) && s[i] != '>' && !(s[i] == '/' && i+1 < len(s) && s[i+1] == '>') && s[i] != '<' && s[i] != '"' && s[i] != '\'' {
					i++
				}
				if i == vs {
					return t, 0, false
				}
				a.Value = Unescape(s[vs:i])
			} else {
				q, k := s[i], 0
				if q == '\'' {
					k = 1
				}
				if noQuote[k] {
					return t, 0, false
				}
				end := strings.IndexByte(s[i+1:], q)
				if end < 0 {
					noQuote[k] = true
					return t, 0, false
				}
				a.Value = Unescape(s[i+1 : i+1+end])
				i += end + 2
			}
		} else {
			a.Bare = true
		}
		t.attrs = append(t.attrs, a)
	}
}

func isLower(c byte) bool  { return c >= 'a' && c <= 'z' }
func isUpper(c byte) bool  { return c >= 'A' && c <= 'Z' }
func isLetter(c byte) bool { return isLower(c) || isUpper(c) }
func isDigit(c byte) bool  { return c >= '0' && c <= '9' }
func isSpace(c byte) bool  { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

// Plain is what elements say as text, for an agent: tags by their meaning (a link with its
// address, a session by its name and id, a resource by its title or address), the rest by their
// children. ref, if not nil, takes a resource's src and gives what to write for it ("" drops it).
func Plain(els []Element, ref func(e Element) string) string {
	var b strings.Builder
	plain(&b, els, ref)
	return strings.TrimSpace(b.String())
}

func plain(b *strings.Builder, els []Element, ref func(e Element) string) {
	for _, e := range els {
		switch e.Type {
		case "text":
			b.WriteString(e.Attr("content"))
		case "br":
			b.WriteByte('\n')
		case "p", "message":
			if b.Len() > 0 && !strings.HasSuffix(b.String(), "\n") {
				b.WriteByte('\n')
			}
			plain(b, e.Children, ref)
			if !strings.HasSuffix(b.String(), "\n") {
				b.WriteByte('\n')
			}
		case "a":
			inner := Plain(e.Children, ref)
			href := e.Attr("href")
			switch {
			case inner == "" || inner == href:
				b.WriteString(href)
			case href == "":
				b.WriteString(inner)
			default:
				b.WriteString(inner + " (" + href + ")")
			}
		case "sharp":
			name := e.Attr("name")
			if name == "" {
				name = Plain(e.Children, ref)
			}
			if name != "" {
				b.WriteString("#" + name + " (" + e.Attr("id") + ")")
			} else {
				b.WriteString("#" + e.Attr("id"))
			}
		case "at":
			name := e.Attr("name")
			if name == "" {
				name = e.Attr("id")
			}
			if t := e.Attr("type"); name == "" && t != "" {
				name = t
			}
			b.WriteString("@" + name)
		case "img", "image", "file", "audio", "video":
			if ref != nil {
				b.WriteString(ref(e))
				continue
			}
			if t := e.Attr("title"); t != "" {
				b.WriteString(t)
			} else {
				b.WriteString(e.Attr("src"))
			}
		case "quote":
			q := Plain(e.Children, ref)
			for _, l := range strings.Split(q, "\n") {
				b.WriteString("> " + l + "\n")
			}
		case "author":
		case "button":
			b.WriteString(Plain(e.Children, ref))
		default:
			plain(b, e.Children, ref)
		}
	}
}
