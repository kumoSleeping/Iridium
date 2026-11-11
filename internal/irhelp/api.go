package irhelp

import (
	_ "embed"
	"strings"
)

// API is the reference ir api help prints (api.txt): a route a line, under "# " headings; lines
// indented under a route are its notes, which only ir api help <that route> prints.
//
//go:embed api.txt
var API string

// Usage is how ir api is written, what it shows, how --wait waits: the head of ir api help.
const Usage = `ir api [METHOD] <path|satori.method> [k=v | k:=json | k==query]... [@file|@-] [--pick a,b] [--grep word] [--all] [--clip N] [--json] [--wait]
  path: internal/x is /v1/internal/x (GET without a body, POST with one, else as METHOD says); a bare name is a Satori method, POST /v1/<name>
  k=v a string, k:=json any JSON, k==v the query; a.b=... sets a's b; @file, @- the body as it is
  shown: a line an item, the route's view= fields, values cut to 200 (--clip 0: whole); --pick other fields, --grep words, --all every page, --json as it came
  --wait: message.create, POST internal/sessions and GET internal/sessions/{id} wait for the turn to end and print its reply; a command line for its output; a 202 {need} for a person to decide
  exit: 0 done, 2 needs the user (a question, an approval), 1 error
`

// Layers is how ir api help is read, a layer at a time; the system prompt carries it, so an agent
// goes to the layer it needs at once.
func Layers() string {
	var ts []string
	for _, t := range Topics() {
		t, _, _ = strings.Cut(t, " (")
		ts = append(ts, t)
	}
	return `ir api help, a layer at a time: read only what the next call needs, no more.
  1. The route known: ir api help <METHOD path | satori.method> (POST internal/sessions, channel.list): its line and notes; one marked ` + More + ` in a list has notes on its fields: read them before its first call.
  2. Not known: ir api help <topic> (` + strings.Join(ts, ", ") + `), a line a route; or ir api help <words>, the routes and notes they are in.
  Sessions: start POST internal/sessions, message message.create, wait GET internal/sessions/{id} --wait, list channel.list. ir api help alone: how ir api is written, the common routes; all: every route.
`
}

// More marks a route line that has notes.
const More = "[more]"

// entry is a route of API: its heading, its line, its notes (unindented).
type entry struct {
	head, line string
	notes      []string
}

// shown is e's line as listed: marked More when it has notes.
func (e entry) shown() string {
	if len(e.notes) > 0 {
		return e.line + "  " + More
	}
	return e.line
}

// full is e's line and its notes, as ir api help <route> prints them.
func (e entry) full() string {
	var b strings.Builder
	b.WriteString(e.line + "\n")
	for _, n := range e.notes {
		b.WriteString("  " + n + "\n")
	}
	return b.String()
}

// entries are API's routes past its preamble, in order; heads, its headings in order.
func entries() (out []entry, heads []string) {
	_, body, _ := strings.Cut(API, "\n\n")
	head := ""
	for _, l := range strings.Split(body, "\n") {
		switch {
		case strings.HasPrefix(l, "# "):
			head = l
			heads = append(heads, strings.TrimPrefix(l, "# "))
		case strings.HasPrefix(l, " "):
			if n := strings.TrimSpace(l); n != "" && len(out) > 0 {
				out[len(out)-1].notes = append(out[len(out)-1].notes, n)
			}
		case strings.TrimSpace(l) != "":
			out = append(out, entry{head: head, line: l})
		}
	}
	return out, heads
}

// preamble is API's notes: its lines before the first blank one.
func preamble() string {
	pre, _, _ := strings.Cut(API, "\n\n")
	return pre + "\n"
}

// Overview is ir api help: Usage, Layers, API's notes, its common routes.
func Overview() string {
	return Usage + Layers() + preamble() + Section("common")
}

// Topics are API's headings past its notes.
func Topics() []string {
	_, heads := entries()
	return heads
}

// Section is the routes under heading topic (its start will do), the heading first; "" if none.
func Section(topic string) string {
	var b strings.Builder
	es, heads := entries()
	for _, h := range heads {
		if !strings.HasPrefix(strings.ToLower(h), strings.ToLower(topic)) {
			continue
		}
		b.WriteString("# " + h + "\n")
		for _, e := range es {
			if e.head == "# "+h {
				b.WriteString(e.shown() + "\n")
			}
		}
	}
	return b.String()
}

// all is every route, under its heading, without notes.
func all() string {
	var b strings.Builder
	b.WriteString(preamble())
	head := ""
	es, _ := entries()
	for _, e := range es {
		if e.head != head {
			head = e.head
			b.WriteString("\n" + head + "\n")
		}
		b.WriteString(e.shown() + "\n")
	}
	return b.String()
}

// Route is a line of API: its method and path (a Satori method: POST and the method's name),
// the fields its answer shows by default (view=), the line as written and its notes.
type Route struct {
	Method, Path string
	Satori       bool
	View         []string
	Line         string
	Notes        []string
}

// Routes are API's routes, in order.
func Routes() []Route {
	es, _ := entries()
	out := make([]Route, 0, len(es))
	for _, e := range es {
		l := e.line
		f := strings.Fields(l)
		r := Route{Line: l, Notes: e.notes}
		if isMethod(f[0]) && len(f) > 1 {
			r.Method, r.Path = f[0], f[1]
		} else {
			r.Method, r.Path, r.Satori = "POST", f[0], true
		}
		r.Path, _, _ = strings.Cut(r.Path, "?")
		if i := strings.LastIndex(l, " view="); i >= 0 {
			r.View = strings.Split(strings.Fields(l[i+len(" view="):])[0], ",")
		}
		out = append(out, r)
	}
	return out
}

func isMethod(s string) bool {
	switch s {
	case "GET", "POST", "PUT", "PATCH", "DELETE":
		return true
	}
	return false
}

// Match is the route of API that method and path (as sent, without /v1/) fall under, if any:
// "{x}" in a route's path takes any one segment.
func Match(method, path string) (Route, bool) {
	segs := strings.Split(strings.Trim(path, "/"), "/")
	for _, r := range Routes() {
		if r.Method != method {
			continue
		}
		rs := strings.Split(r.Path, "/")
		if len(rs) != len(segs) {
			continue
		}
		ok := true
		for i := range rs {
			if !strings.HasPrefix(rs[i], "{") && rs[i] != segs[i] {
				ok = false
				break
			}
		}
		if ok {
			return r, true
		}
	}
	return Route{}, false
}

// Find is ir api help <words>: the overview for none; every route for "all"; one route, with its
// notes, for a route (POST internal/sessions, internal/sessions/abc, channel.list); a topic's
// section when the words name one (sessions, secrets…); else the routes every word is in (case
// aside), each under its heading, with the notes a word is in.
func Find(words []string) string {
	switch {
	case len(words) == 0:
		return Overview()
	case len(words) == 1 && words[0] == "all":
		return all()
	}
	if r := route(words); r != "" {
		return r
	}
	if len(words) == 1 && words[0] != "" {
		if sec := Section(words[0]); sec != "" {
			// And the routes elsewhere it is in (a common one, say).
			var more []string
			for _, l := range strings.Split(lines(words, false), "\n") {
				if l != "" && !strings.HasPrefix(l, "# ") && !strings.Contains(sec, l+"\n") {
					more = append(more, l)
				}
			}
			if len(more) > 0 {
				sec += "# also\n" + strings.Join(more, "\n") + "\n"
			}
			return sec
		}
	}
	if l := lines(words, true); l != "" {
		return l
	}
	return "no route has " + strings.Join(words, " ") + "; topics: " + strings.Join(Topics(), ", ") + "\n"
}

// route is the routes words name, each with its notes: [METHOD] path, the path as written in API
// or as sent (internal/sessions/abc/stop), /v1/ or not; a Satori method by its name. "" if none.
func route(words []string) string {
	method, path := "", ""
	switch {
	case len(words) == 2 && isMethod(strings.ToUpper(words[0])):
		method, path = strings.ToUpper(words[0]), words[1]
	case len(words) == 1 && (strings.Contains(words[0], "/") || strings.Contains(words[0], ".")):
		path = words[0]
	default:
		return ""
	}
	path = strings.TrimPrefix(strings.TrimPrefix(path, "/"), "v1/")
	path, _, _ = strings.Cut(path, "?")
	var b strings.Builder
	es, _ := entries()
	rs := Routes()
	for i, r := range rs {
		if r.Path == path && (method == "" || r.Method == method) {
			b.WriteString(es[i].full())
		}
	}
	if b.Len() == 0 {
		for _, m := range []string{"GET", "POST", "PUT", "PATCH", "DELETE"} {
			if method != "" && m != method {
				continue
			}
			if r, ok := Match(m, path); ok {
				for i := range rs {
					if rs[i].Line == r.Line {
						b.WriteString(es[i].full())
					}
				}
			}
		}
	}
	return b.String()
}

// lines are the routes every word is in (case aside), line or notes, each under its heading; with
// notes, the notes a word is in under it.
func lines(words []string, notes bool) string {
	var b strings.Builder
	head := ""
	es, _ := entries()
	for _, e := range es {
		all := strings.ToLower(e.line + "\n" + strings.Join(e.notes, "\n"))
		hit := true
		for _, w := range words {
			if !strings.Contains(all, strings.ToLower(w)) {
				hit = false
				break
			}
		}
		if !hit {
			continue
		}
		if e.head != head {
			head = e.head
			b.WriteString(head + "\n")
		}
		b.WriteString(e.shown() + "\n")
		if !notes {
			continue
		}
		for _, n := range e.notes {
			low := strings.ToLower(n)
			for _, w := range words {
				if strings.Contains(low, strings.ToLower(w)) {
					b.WriteString("  " + n + "\n")
					break
				}
			}
		}
	}
	return b.String()
}
