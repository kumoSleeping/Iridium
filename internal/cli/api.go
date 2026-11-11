package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/irhelp"
	"github.com/kumoSleeping/iridium/internal/satori"
)

// ir api [METHOD] <path> [k=v | k:=json | k==query]... [@file|@-] [--pick a,b.c] [--grep word]...
// [--all] [--clip N] [--json] [--wait]: a request to the daemon as it is given (plan "ir"). A path
// with a slash is /v1/<path> (GET without a body, else POST, unless METHOD says); without one it
// is a Satori method, POST /v1/<method> as the login ir acts for (POST /v1/meta names it). ir adds
// only the headers (its session, the login) and changes nothing else: a session's id's start and
// self are the server's to take (docs/api.md "会话 ID"). The answer is what ir shapes: a line an
// item, the fields the reference's view= names (--pick others), each value on one line and cut
// short (--clip), a Satori message's content as plain words; --json shows it as it came.
// ir api help [word]... prints the reference (irhelp.API).

const apiUsage = `usage: ir api [METHOD] <path|satori.method> [k=v | k:=json | k==query]... [@file|@-] [--pick a,b] [--grep word] [--all] [--clip N] [--json] [--wait]
       ir api help [word]...   the routes, their bodies and answers`

// clipLen is how much of a value ir api shows by default.
const clipLen = 200

type apiCall struct {
	method, path string
	satori       bool
	body         map[string]any
	raw          []byte // @file, @-: the body as it is
	query        url.Values
	pick         []string
	grep         []string
	all, json    bool
	wait         bool
	clip         int
}

func cmdAPI(e *env, args []string) int {
	if len(args) > 0 && args[0] == "help" {
		fmt.Fprint(e.out, irhelp.Find(args[1:]))
		return exitDone
	}
	c, err := parseAPI(args)
	if err != nil {
		return e.failf("%v\n%s", err, apiUsage)
	}
	if c.path == "/v1/message.create" && !c.json {
		return e.apiSend(c)
	}
	raw, err := e.call(c)
	if err != nil {
		return e.fail(err)
	}
	if c.all {
		if raw, err = e.rest(c, raw); err != nil {
			return e.fail(err)
		}
	}
	if c.json {
		fmt.Fprintln(e.out, strings.TrimSpace(string(raw)))
		return exitDone
	}
	e.render(c, raw)
	return exitDone
}

func parseAPI(args []string) (*apiCall, error) {
	c := &apiCall{body: map[string]any{}, query: url.Values{}, clip: clipLen}
	val := func(i *int, name string) (string, error) {
		if _, v, ok := strings.Cut(args[*i], "="); ok {
			return v, nil
		}
		if *i+1 >= len(args) {
			return "", fmt.Errorf("%s needs a value", name)
		}
		*i++
		return args[*i], nil
	}
	method := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		name, _, _ := strings.Cut(a, "=")
		switch {
		case a == "--json":
			c.json = true
		case a == "--all":
			c.all = true
		case a == "--wait":
			c.wait = true
		case name == "--pick":
			v, err := val(&i, name)
			if err != nil {
				return nil, err
			}
			c.pick = append(c.pick, strings.Split(v, ",")...)
		case name == "--grep":
			v, err := val(&i, name)
			if err != nil {
				return nil, err
			}
			c.grep = append(c.grep, strings.Fields(strings.ToLower(v))...)
		case name == "--clip":
			v, err := val(&i, name)
			if err != nil {
				return nil, err
			}
			if c.clip, err = strconv.Atoi(v); err != nil || c.clip < 0 {
				return nil, fmt.Errorf("--clip takes a count, not %q", v)
			}
		case strings.HasPrefix(a, "--"):
			return nil, fmt.Errorf("no flag %s here", a)
		case method == "" && c.path == "" && isVerb(a):
			method = a
		case c.path == "":
			c.path = a
		case strings.HasPrefix(a, "@"):
			if c.raw != nil {
				return nil, errors.New("one @body only")
			}
			var err error
			if a == "@-" {
				c.raw, err = io.ReadAll(os.Stdin)
			} else {
				c.raw, err = os.ReadFile(a[1:])
			}
			if err != nil {
				return nil, err
			}
		default:
			if err := c.item(a); err != nil {
				return nil, err
			}
		}
	}
	if c.path == "" {
		return nil, errors.New("no path")
	}
	if c.raw != nil && len(c.body) > 0 {
		return nil, errors.New("@body or k=v fields, not both")
	}
	hasBody := c.raw != nil || len(c.body) > 0
	if strings.Contains(c.path, "/") {
		c.path = "/v1/" + strings.TrimPrefix(strings.TrimPrefix(c.path, "/"), "v1/")
		c.method = method
		if c.method == "" {
			c.method = "GET"
			if hasBody {
				c.method = "POST"
			}
		}
		if hasBody && c.method == "GET" {
			return nil, errors.New("GET takes no body; query fields are k==v")
		}
	} else {
		if method != "" && method != "POST" {
			return nil, fmt.Errorf("%s is a Satori method: POST only", c.path)
		}
		c.method, c.satori = "POST", true
		c.path = "/v1/" + c.path
	}
	if len(c.query) > 0 {
		c.path += "?" + c.query.Encode()
	}
	if c.wait && c.path != "/v1/message.create" {
		return nil, errors.New("--wait is for message.create")
	}
	if c.all && !c.satori {
		return nil, errors.New("--all is for Satori's lists (their next)")
	}
	return c, nil
}

func isVerb(s string) bool {
	switch s {
	case "GET", "POST", "PUT", "PATCH", "DELETE":
		return true
	}
	return false
}

// item takes k==v (query), k:=json (any JSON), or k=v (a string); a dotted key goes into objects.
func (c *apiCall) item(a string) error {
	i := strings.IndexAny(a, ":=")
	if i <= 0 {
		return fmt.Errorf("%q: k=v, k:=json or k==v", a)
	}
	k, rest := a[:i], a[i:]
	switch {
	case strings.HasPrefix(rest, "=="):
		c.query.Add(k, rest[2:])
		return nil
	case strings.HasPrefix(rest, ":="):
		var x any
		if err := json.Unmarshal([]byte(rest[2:]), &x); err != nil {
			return fmt.Errorf("%s: not JSON: %v", k, err)
		}
		return setPath(c.body, strings.Split(k, "."), x)
	case strings.HasPrefix(rest, "="):
		return setPath(c.body, strings.Split(k, "."), rest[1:])
	}
	return fmt.Errorf("%q: k=v, k:=json or k==v", a)
}

// setPath puts v at path in m, making the objects on the way.
func setPath(m map[string]any, path []string, v any) error {
	for _, k := range path[:len(path)-1] {
		if k == "" {
			return fmt.Errorf("an empty key in %s", strings.Join(path, "."))
		}
		next, ok := m[k].(map[string]any)
		if !ok {
			if _, there := m[k]; there {
				return fmt.Errorf("%s is set and is not an object", k)
			}
			next = map[string]any{}
			m[k] = next
		}
		m = next
	}
	m[path[len(path)-1]] = v
	return nil
}

// call sends c: the answer as it came.
func (e *env) call(c *apiCall) ([]byte, error) {
	var body any
	switch {
	case c.raw != nil:
		body = json.RawMessage(c.raw)
	case len(c.body) > 0 || c.satori:
		body = c.body
	}
	var hdr []string
	if c.satori && c.path != "/v1/meta" {
		login, err := e.login()
		if err != nil {
			return nil, err
		}
		hdr = []string{satori.HeaderPlatform, login.Platform, satori.HeaderUser, login.User.ID}
	}
	resp, err := e.request(c.method, c.path, body, hdr...)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

// satori calls method with body as the login ir acts for; the answer as it came.
func (e *env) satori(method string, body map[string]any) ([]byte, error) {
	if body == nil {
		body = map[string]any{}
	}
	return e.call(&apiCall{method: "POST", path: "/v1/" + method, satori: true, body: body})
}

// login is the login ir acts for: the one meta gives it (the host's ir, local; an agent's, the
// login its session's turn is for).
func (e *env) login() (satori.Login, error) {
	if e.as != nil {
		return *e.as, nil
	}
	resp, err := e.request("POST", "/v1/meta", map[string]any{})
	if err != nil {
		return satori.Login{}, err
	}
	defer resp.Body.Close()
	var m satori.Meta
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		return satori.Login{}, err
	}
	if len(m.Logins) != 1 || m.Logins[0].User == nil {
		return satori.Login{}, fmt.Errorf("ir speaks as one login, meta gives %d", len(m.Logins))
	}
	e.as = &m.Logins[0]
	return *e.as, nil
}

// rest is a Satori list with the items of its later pages (next) in it, as one answer.
func (e *env) rest(c *apiCall, first []byte) ([]byte, error) {
	var l struct {
		Data []json.RawMessage `json:"data"`
		Next string            `json:"next"`
	}
	if json.Unmarshal(first, &l) != nil || l.Next == "" {
		return first, nil
	}
	items := l.Data
	for range 100 {
		if l.Next == "" {
			break
		}
		c.body["next"] = l.Next
		raw, err := e.call(c)
		if err != nil {
			return nil, err
		}
		l.Data, l.Next = nil, ""
		if err := json.Unmarshal(raw, &l); err != nil {
			return nil, err
		}
		items = append(items, l.Data...)
	}
	return json.Marshal(map[string]any{"data": items})
}

// apiSend is message.create: sent, then waited on until the agent took it (so ir wait right
// after waits for its turn), or with --wait until its turn ends, the reply printed.
func (e *env) apiSend(c *apiCall) int {
	arg, _ := c.body["channel_id"].(string)
	if c.raw != nil {
		var b struct {
			ChannelID string `json:"channel_id"`
		}
		_ = json.Unmarshal(c.raw, &b)
		arg = b.ChannelID
	}
	id := e.caller()
	if arg != "self" {
		var err error
		if id, err = e.resolve(arg); err != nil {
			return e.fail(err)
		}
	}
	before, _ := e.latest(id)
	raw, err := e.call(c)
	if err != nil {
		return e.fail(err)
	}
	var ms []struct {
		Data api.SendReply `json:"_data"`
	}
	if err := json.Unmarshal(raw, &ms); err != nil || len(ms) == 0 {
		return e.failf("message.create: %s", strings.TrimSpace(string(raw)))
	}
	return e.afterSend(id, ms[len(ms)-1].Data, c.wait, before)
}

// afterSend waits on a message sent to session id as wait asks.
func (e *env) afterSend(id string, sent api.SendReply, wait bool, before api.MessagePage) int {
	if sent.Desktop {
		if !wait {
			return exitDone
		}
		return e.waitMirrored(id, before)
	}
	if n := len(before.Blocks); n > 0 {
		e.after = before.Blocks[n-1].At
	}
	if sent.Session != "" { // /clear: it goes on in a new session
		fmt.Fprintln(e.out, short(sent.Session, e.ids()))
		return exitDone
	}
	if sent.Queued {
		if !wait {
			return exitDone // it goes when the running turn ends
		}
		if code := e.waitQueued(id, sent.Input); code >= 0 {
			return code
		}
	}
	if wait {
		return e.waitTurn(id, sent.Input)
	}
	return e.waitTaken(id, sent.Input)
}

// ---- the answer, shaped ----

// render writes the answer: a list (an array, or Satori's {data, next}) a line an item, as a
// table when fields are named (--pick, else the route's view=); an object a field a line, an
// array of objects in it that fields go into (apps.id) a table of its own; the rest as it is.
func (e *env) render(c *apiCall, raw []byte) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if len(bytes.TrimSpace(raw)) == 0 || dec.Decode(&v) != nil {
		if s := strings.TrimSpace(string(raw)); s != "" {
			fmt.Fprintln(e.out, s)
		}
		return
	}
	fields := c.pick
	if len(fields) == 0 {
		route, _, _ := strings.Cut(strings.TrimPrefix(c.path, "/v1/"), "?")
		if r, ok := irhelp.Match(c.method, route); ok {
			fields = r.View
		}
	}
	if items, next, ok := listOf(v); ok {
		e.table(c, items, fields)
		if next != "" {
			fmt.Fprintf(e.out, "(next=%s; --all takes every page)\n", next)
		}
		return
	}
	m, ok := v.(map[string]any)
	if !ok {
		fmt.Fprintln(e.out, c.show(kv{v: v}))
		return
	}
	// Fields into an array of objects: a table each, after the object's own fields.
	var own, order []string
	sub := map[string][]string{}
	for _, f := range fields {
		head, rest, _ := strings.Cut(f, ".")
		if arr, ok := m[head].([]any); ok && rest != "" && slices.ContainsFunc(arr, isObject) {
			if sub[head] == nil {
				order = append(order, head)
			}
			sub[head] = append(sub[head], rest)
			continue
		}
		own = append(own, f)
	}
	if len(own) > 0 || len(order) == 0 {
		for _, x := range pickOf(flat(m), own) {
			if x.v != nil {
				fmt.Fprintf(e.out, "%s: %s\n", x.k, c.show(x))
			}
		}
	}
	for _, head := range order {
		fmt.Fprintf(e.out, "%s:\n", head)
		e.table(c, m[head].([]any), sub[head])
	}
}

func isObject(v any) bool { _, ok := v.(map[string]any); return ok }

// table writes items a line each: the fields named, tab-separated under a header, else every
// field as k=v; with --grep only the items every word is in.
func (e *env) table(c *apiCall, items []any, fields []string) {
	head := len(fields) > 0
	row := func(l string) {
		if head {
			fmt.Fprintln(e.out, strings.Join(fields, "\t"))
			head = false
		}
		fmt.Fprintln(e.out, l)
	}
	for _, it := range items {
		m, ok := it.(map[string]any)
		if !ok {
			if c.matches(fmt.Sprint(it)) {
				row(c.show(kv{v: it}))
			}
			continue
		}
		f := flat(m)
		hay := make([]string, len(f))
		for i, x := range f {
			hay[i] = x.text()
		}
		if !c.matches(hay...) {
			continue
		}
		var cells []string
		if len(fields) > 0 {
			for _, x := range pickOf(f, fields) {
				s := c.show(x)
				if s == "" {
					s = "-"
				}
				cells = append(cells, s)
			}
			row(strings.Join(cells, "\t"))
			continue
		}
		for _, x := range f {
			cells = append(cells, x.k+"="+c.show(x))
		}
		row(strings.Join(cells, "  "))
	}
}

// listOf: v's items if it is a list (an array, or an object with a data array), and its next.
func listOf(v any) (items []any, next string, ok bool) {
	switch x := v.(type) {
	case []any:
		return x, "", true
	case map[string]any:
		if d, ok := x["data"].([]any); ok {
			n, _ := x["next"].(string)
			return d, n, true
		}
	}
	return nil, "", false
}

// kv is a field of an object, its key dotted from the top.
type kv struct {
	k string
	v any
}

func (x kv) text() string {
	if s, ok := x.v.(string); ok {
		if isContent(x.k) {
			return plainContent(s)
		}
		return s
	}
	return fmt.Sprint(x.v)
}

// flat is m's fields, nested objects' with dotted keys (keys sorted), arrays of plain values
// joined with commas; nothing empty (null, "", false, [], {}).
func flat(m map[string]any) []kv {
	var out []kv
	var walk func(prefix string, v any)
	walk = func(prefix string, v any) {
		switch x := v.(type) {
		case nil:
		case bool:
			if x {
				out = append(out, kv{prefix, x})
			}
		case string:
			if x != "" {
				out = append(out, kv{prefix, x})
			}
		case map[string]any:
			keys := make([]string, 0, len(x))
			for k := range x {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				walk(join(prefix, k), x[k])
			}
		case []any:
			if len(x) == 0 {
				return
			}
			if slices.ContainsFunc(x, func(y any) bool { _, ok := y.(map[string]any); return ok }) {
				for i, y := range x {
					walk(join(prefix, strconv.Itoa(i)), y)
				}
				return
			}
			parts := make([]string, len(x))
			for i, y := range x {
				parts[i] = fmt.Sprint(y)
			}
			out = append(out, kv{prefix, strings.Join(parts, ",")})
		default:
			out = append(out, kv{prefix, x})
		}
	}
	walk("", m)
	return out
}

func join(prefix, k string) string {
	if prefix == "" {
		return k
	}
	return prefix + "." + k
}

// pickOf is the fields of f fields names, in that order, or f itself when none are named. A name
// matches through arrays (logins.id: logins.0.id, logins.1.id…, joined with commas) and may
// stand for an object (its fields on one line, k=v). One not there is an empty kv.
func pickOf(f []kv, fields []string) []kv {
	if len(fields) == 0 {
		return f
	}
	var out []kv
	for _, name := range fields {
		var same, parts []string
		for _, x := range f {
			k := noIndex(x.k)
			switch {
			case k == name:
				same = append(same, x.text())
			case strings.HasPrefix(k, name+"."):
				parts = append(parts, strings.TrimPrefix(k, name+".")+"="+x.text())
			}
		}
		switch {
		case len(same) == 1:
			out = append(out, kv{name, same[0]})
		case len(same) > 1:
			out = append(out, kv{name, strings.Join(same, ",")})
		case len(parts) > 0:
			out = append(out, kv{name, strings.Join(parts, " ")})
		default:
			out = append(out, kv{k: name})
		}
	}
	return out
}

// noIndex is a dotted key without its arrays' indexes (logins.0.id: logins.id).
func noIndex(k string) string {
	segs := strings.Split(k, ".")
	out := segs[:0]
	for _, s := range segs {
		if _, err := strconv.Atoi(s); err != nil {
			out = append(out, s)
		}
	}
	return strings.Join(out, ".")
}

// show is a value on one line, cut to c.clip runes (0: whole).
func (c *apiCall) show(x kv) string {
	if x.v == nil {
		return ""
	}
	t := strings.Join(strings.Fields(x.text()), " ")
	if r := []rune(t); c.clip > 0 && len(r) > c.clip {
		return string(r[:c.clip]) + fmt.Sprintf("…(+%d)", len(r)-c.clip)
	}
	return t
}

// matches: every --grep word is in one of fields, case aside.
func (c *apiCall) matches(fields ...string) bool {
	hay := strings.ToLower(strings.Join(fields, "\n"))
	return !slices.ContainsFunc(c.grep, func(w string) bool { return !strings.Contains(hay, w) })
}

// isContent: the field is a Satori message's content (a list's data.N.content too).
func isContent(k string) bool { return k == "content" || strings.HasSuffix(k, ".content") }

// plainContent is a message's content (docs/satori.md "记录") as words: a person's after "> " and
// who; a tool call, thinking, a system line marked as such; the agent's as they are.
func plainContent(content string) string {
	s := readMessage(satori.Message{Content: content})
	switch s.kind {
	case string(api.BlockKindUser):
		return "> " + fromTag(s.from) + s.text
	case string(api.BlockKindAssistant):
		return s.text
	}
	return "[" + strings.TrimSpace(s.kind+" "+s.from) + "] " + s.text
}

// said is a message of the transcript as ir shows it: who, from where, what.
type said struct {
	kind, from, text string
}

// readMessage reads a message's content (docs/satori.md "记录"): a person's words (iridium:user),
// the agent's (bare words, or iridium:assistant), a tool call, a system line, thinking.
func readMessage(m satori.Message) said {
	els := satori.Parse(m.Content)
	out := said{kind: string(api.BlockKindAssistant)}
	var rest []satori.Element
	for i, el := range els {
		kind, ok := strings.CutPrefix(el.Type, satori.Adapter+":")
		if !ok {
			rest = append(rest, el)
			continue
		}
		if i == 0 {
			switch kind {
			case "user":
				out.kind, out.from = string(api.BlockKindUser), el.Attr("from")
			case "assistant":
			default:
				out.kind = kind
				out.from = el.Attr("name")
				if out.from == "" {
					out.from = el.Attr("event")
				}
			}
		}
		// A tool call's input (its command, its path…), before what it wrote.
		for _, ch := range el.Children {
			if ch.Type == satori.Adapter+":input" {
				rest = append(rest, ch.Children...)
			}
		}
	}
	out.text = satori.Plain(rest, nil)
	return out
}

// fromTag is who a person's message came from, as the agent saw it: "[who] ".
func fromTag(from string) string {
	if from == "" {
		return ""
	}
	return "[" + from + "] "
}
