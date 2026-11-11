package irhelp

import (
	_ "embed"
	"strings"
)

// API is the reference ir api help prints (api.txt): a route a line, under "# " headings.
//
//go:embed api.txt
var API string

// Common is API's preamble and its common routes: what every hosted agent's system prompt
// carries (session.SystemPrompt), the rest left to ir api help <word>.
func Common() string {
	c, _, _ := strings.Cut(API, "\n# more")
	return strings.TrimRight(c, "\n") + "\n"
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

// Find is API's preamble (its lines before the first blank one) and the lines every word is in
// (case aside), each under its heading; no words, all of it.
func Find(words []string) string {
	if len(words) == 0 {
		return API
	}
	pre, body, _ := strings.Cut(API, "\n\n")
	var b strings.Builder
	b.WriteString(pre + "\n")
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
