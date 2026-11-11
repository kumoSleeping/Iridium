package irhelp

import (
	_ "embed"
	"strings"
)

// API is the reference ir api help prints (api.txt): a route a line, under "# " headings.
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
  ir api help <topic or word>: that topic's routes, or the routes the words are in; ir api help all: every route
`

// Overview is ir api help: Usage, API's notes (its lines before the first blank one), its topics
// and its common routes.
func Overview() string {
	pre, _, _ := strings.Cut(API, "\n\n")
	var b strings.Builder
	b.WriteString(Usage)
	b.WriteString(pre + "\n")
	b.WriteString("# topics: " + strings.Join(Topics(), ", ") + "\n")
	b.WriteString(Section("common"))
	return b.String()
}

// Topics are API's headings past its notes.
func Topics() []string {
	var out []string
	_, body, _ := strings.Cut(API, "\n\n")
	for _, l := range strings.Split(body, "\n") {
		if t, ok := strings.CutPrefix(l, "# "); ok {
			out = append(out, t)
		}
	}
	return out
}

// Section is the routes under heading topic (its start will do), the heading first; "" if none.
func Section(topic string) string {
	var b strings.Builder
	in := false
	for _, l := range strings.Split(API, "\n") {
		if t, ok := strings.CutPrefix(l, "# "); ok {
			in = strings.HasPrefix(strings.ToLower(t), strings.ToLower(topic))
			if in {
				b.WriteString(l + "\n")
			}
			continue
		}
		if in && strings.TrimSpace(l) != "" {
			b.WriteString(l + "\n")
		}
	}
	return b.String()
}

// Route is a line of API: its method and path (a Satori method: POST and the method's name),
// the fields its answer shows by default (view=), and the line as written.
type Route struct {
	Method, Path string
	Satori       bool
	View         []string
	Line         string
}

// Routes are API's routes, in order.
func Routes() []Route {
	var out []Route
	for _, l := range strings.Split(API, "\n") {
		f := strings.Fields(l)
		if len(f) == 0 || strings.HasPrefix(l, "#") {
			continue
		}
		r := Route{Line: l}
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

// Find is ir api help <words>: all of API for "all"; a topic's section when the words name one
// (sessions, secrets…); else the routes every word is in (case aside), each under its heading.
func Find(words []string) string {
	switch {
	case len(words) == 0:
		return Overview()
	case len(words) == 1 && words[0] == "all":
		return API
	case len(words) == 1 && words[0] != "":
		if sec := Section(words[0]); sec != "" {
			// And the routes elsewhere it is in (a common one, say).
			var more []string
			for _, l := range strings.Split(lines(words), "\n") {
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
	if l := lines(words); l != "" {
		return l
	}
	return "no route has " + strings.Join(words, " ") + "; topics: " + strings.Join(Topics(), ", ") + "\n"
}

// lines are the routes every word is in (case aside), each under its heading.
func lines(words []string) string {
	_, body, _ := strings.Cut(API, "\n\n")
	var b strings.Builder
	head := ""
	for _, l := range strings.Split(body, "\n") {
		if strings.HasPrefix(l, "# ") {
			head = l
			continue
		}
		if strings.TrimSpace(l) == "" {
			continue
		}
		low := strings.ToLower(l)
		hit := true
		for _, w := range words {
			if !strings.Contains(low, strings.ToLower(w)) {
				hit = false
				break
			}
		}
		if hit {
			if head != "" {
				b.WriteString(head + "\n")
				head = ""
			}
			b.WriteString(l + "\n")
		}
	}
	return b.String()
}
