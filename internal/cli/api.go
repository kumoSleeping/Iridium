package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/place"
	"github.com/kumoSleeping/iridium/internal/satori"
)

// ir api <method> [key=value | key:=json]... [--text words|@-] [-a file]... [--grep word]...
// [--json] [--wait]: a method of Satori with ir (docs/satori.md), as the login ir acts for (POST
// /v1/meta names it). key=value is a string, key:=json any JSON; a dotted key goes into an object
// (data.name, _data.archived). channel_id may be a session's id's start, or self (the session ir
// runs in); guild_id self is its group. --text is the content as plain words, escaped; -a attaches
// a file to message.create (_data.attachments). The answer is shown a line an item for the lists,
// the turns for message.list, as JSON otherwise; --json shows it as it is.

const apiUsage = `usage: ir api <方法> [键=值 | 键:=JSON]... [--text 文字|@-] [-a 文件]... [--grep 词]... [--json] [--wait]`

type apiCall struct {
	method string
	body   map[string]any
	text   *string
	attach []string
	grep   []string
	json   bool
	wait   bool
}

func cmdAPI(e *env, args []string) int {
	c, err := parseAPI(args)
	if err != nil {
		return e.failf("%v\n%s", err, apiUsage)
	}
	if err := e.fill(c); err != nil {
		return e.fail(err)
	}
	if c.method == "message.create" {
		return e.apiSend(c)
	}
	raw, err := e.satori(c.method, c.body)
	if err != nil {
		return e.fail(err)
	}
	if c.json {
		fmt.Fprintln(e.out, strings.TrimSpace(string(raw)))
		return exitDone
	}
	return e.show(c, raw)
}

func parseAPI(args []string) (*apiCall, error) {
	c := &apiCall{body: map[string]any{}}
	val := func(i *int, name string) (string, error) {
		a := args[*i]
		if _, v, ok := strings.Cut(a, "="); ok && strings.HasPrefix(a, "--") {
			return v, nil
		}
		if *i+1 >= len(args) {
			return "", fmt.Errorf("%s needs a value", name)
		}
		*i++
		return args[*i], nil
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		name, _, _ := strings.Cut(a, "=")
		switch {
		case a == "--json":
			c.json = true
		case a == "--wait":
			c.wait = true
		case name == "--text":
			v, err := val(&i, "--text")
			if err != nil {
				return nil, err
			}
			c.text = &v
		case a == "-a" || name == "--attach":
			v, err := val(&i, "-a")
			if err != nil {
				return nil, err
			}
			c.attach = append(c.attach, v)
		case name == "--grep":
			v, err := val(&i, "--grep")
			if err != nil {
				return nil, err
			}
			c.grep = append(c.grep, strings.Fields(strings.ToLower(v))...)
		case strings.HasPrefix(a, "-"):
			return nil, fmt.Errorf("no flag %s here", a)
		case c.method == "":
			c.method = a
		default:
			k, v, raw := a, "", false
			if i := strings.Index(a, ":="); i > 0 && !strings.Contains(a[:i], "=") {
				k, v, raw = a[:i], a[i+2:], true
			} else if i := strings.Index(a, "="); i > 0 {
				k, v = a[:i], a[i+1:]
			} else {
				return nil, fmt.Errorf("%q: key=value or key:=json", a)
			}
			var x any = v
			if raw {
				if err := json.Unmarshal([]byte(v), &x); err != nil {
					return nil, fmt.Errorf("%s: not JSON: %v", k, err)
				}
			}
			if err := setPath(c.body, strings.Split(k, "."), x); err != nil {
				return nil, err
			}
		}
	}
	if c.method == "" {
		return nil, errors.New("no method")
	}
	if c.wait && c.method != "message.create" {
		return nil, errors.New("--wait is for message.create")
	}
	if (c.text != nil || len(c.attach) > 0) && c.method != "message.create" {
		return nil, errors.New("--text and -a are for message.create")
	}
	return c, nil
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

// fill makes c's body what the daemon takes: a session by its id's start or self, a group by
// self, the text as content, the files attached, the session ir runs in as the caller.
func (e *env) fill(c *apiCall) error {
	if v, ok := c.body["channel_id"].(string); ok {
		id := e.caller()
		if v != "self" {
			var err error
			if id, err = e.resolve(v); err != nil {
				return err
			}
		} else if id == "" {
			return errors.New("channel_id=self is for ir run in a session")
		}
		c.body["channel_id"] = id
	}
	if v, ok := c.body["guild_id"].(string); ok && v == "self" {
		w, err := e.groupOf(e.caller())
		if err != nil {
			return errors.New("guild_id=self is for ir run in a session")
		}
		c.body["guild_id"] = satori.Loose
		if w != 0 {
			c.body["guild_id"] = strconv.FormatInt(w, 10)
		}
	}
	if c.method != "message.create" {
		return nil
	}
	if c.text != nil {
		if _, ok := c.body["content"]; ok {
			return errors.New("--text or content, not both")
		}
		t := *c.text
		if t == "@-" {
			b, err := io.ReadAll(os.Stdin)
			if err != nil {
				return err
			}
			t = strings.TrimRight(string(b), "\n")
		}
		c.body["content"] = satori.Escape(t)
	}
	id, _ := c.body["channel_id"].(string)
	if id == "" {
		return errors.New("message.create needs channel_id")
	}
	if _, ok := c.body["content"]; !ok && len(c.attach) == 0 {
		return errors.New(`message.create needs --text "…", content=… or -a file`)
	}
	data, _ := c.body["_data"].(map[string]any)
	if data == nil {
		data = map[string]any{}
	}
	for _, a := range c.attach {
		p, err := localPath(a)
		if err != nil {
			return err
		}
		var f api.FileRef
		if err := e.do("POST", "/v1/internal/sessions/"+id+"/attach", api.AttachPath{Path: p}, &f); err != nil {
			return fmt.Errorf("%s: %v", a, err)
		}
		refs, _ := data["attachments"].([]any)
		data["attachments"] = append(refs, f.Ref)
	}
	if _, ok := data["caller"]; !ok && e.caller() != "" {
		data["caller"] = e.caller()
	}
	if len(data) > 0 {
		c.body["_data"] = data
	}
	return nil
}

// satori calls method with body as the login ir acts for; the answer as it came.
func (e *env) satori(method string, body any) ([]byte, error) {
	var hdr []string
	if method != "meta" {
		login, err := e.login()
		if err != nil {
			return nil, err
		}
		hdr = []string{satori.HeaderPlatform, login.Platform, satori.HeaderUser, login.User.ID}
	}
	if body == nil {
		body = map[string]any{}
	}
	resp, err := e.request("POST", "/v1/"+method, body, hdr...)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
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

// apiSend is message.create: sent, then (as ir send did) waited on until the agent took it, or
// with --wait until its turn ends, its reply printed.
func (e *env) apiSend(c *apiCall) int {
	id := c.body["channel_id"].(string)
	var before api.MessagePage
	if c.wait {
		before, _ = e.latest(id)
	}
	raw, err := e.satori("message.create", c.body)
	if err != nil {
		return e.fail(err)
	}
	var ms []struct {
		Data api.SendReply `json:"_data"`
	}
	if err := json.Unmarshal(raw, &ms); err != nil || len(ms) == 0 {
		return e.failf("message.create: %s", strings.TrimSpace(string(raw)))
	}
	if c.json {
		fmt.Fprintln(e.out, strings.TrimSpace(string(raw)))
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

// show writes the answer of c's method: the lists a line an item (every page), the turns of
// message.list, JSON for the rest.
func (e *env) show(c *apiCall, raw []byte) int {
	switch c.method {
	case "channel.list", "channel.get":
		chans, err := allPages(e, c, raw, func(b []byte) ([]satori.Channel, string, error) {
			if c.method == "channel.get" {
				var ch satori.Channel
				return []satori.Channel{ch}, "", json.Unmarshal(b, &ch)
			}
			var l satori.List[satori.Channel]
			return l.Data, l.Next, json.Unmarshal(b, &l)
		})
		if err != nil {
			return e.fail(err)
		}
		return e.showChannels(c, chans)
	case "guild.list", "guild.get":
		gs, err := allPages(e, c, raw, func(b []byte) ([]satori.Guild, string, error) {
			if c.method == "guild.get" {
				var g satori.Guild
				return []satori.Guild{g}, "", json.Unmarshal(b, &g)
			}
			var l satori.List[satori.Guild]
			return l.Data, l.Next, json.Unmarshal(b, &l)
		})
		if err != nil {
			return e.fail(err)
		}
		home, names := e.home(), e.machineNames()
		for _, g := range gs {
			where := ""
			if g.Data != nil {
				p := place.FromKey(g.Data.Path)
				if p.App == "" {
					p.Path = tildeOf(p.Path, home)
				}
				where = place.Format(p, func(id string) string { return names[id] })
			}
			if matches(c.grep, g.ID, g.Name, where) {
				fmt.Fprintf(e.out, "%s\t%s\t%s\n", g.ID, g.Name, where)
			}
		}
		return exitDone
	case "message.list":
		var l satori.List[satori.Message]
		if err := json.Unmarshal(raw, &l); err != nil {
			return e.fail(err)
		}
		e.showTurns(c, l.Data)
		return exitDone
	case "message.get":
		var m satori.Message
		if err := json.Unmarshal(raw, &m); err != nil {
			return e.fail(err)
		}
		fmt.Fprintln(e.out, readMessage(m).text)
		return exitDone
	case "channel.update", "channel.delete":
		return exitDone
	}
	var v any
	if json.Unmarshal(raw, &v) == nil {
		b, _ := json.MarshalIndent(v, "", "  ")
		raw = b
	}
	fmt.Fprintln(e.out, strings.TrimSpace(string(raw)))
	return exitDone
}

// allPages is the items of the answer first and of the pages after it (next), as decode reads each.
func allPages[T any](e *env, c *apiCall, first []byte, decode func([]byte) ([]T, string, error)) ([]T, error) {
	var out []T
	raw := first
	for range 100 {
		items, next, err := decode(raw)
		if err != nil {
			return nil, err
		}
		out = append(out, items...)
		if next == "" {
			return out, nil
		}
		c.body["next"] = next
		if raw, err = e.satori(c.method, c.body); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// showChannels writes the sessions as ir ls did (a subagent under its parent), with --grep the
// ones every word is in (id, name, title, folder, last words), their last words below.
func (e *env) showChannels(c *apiCall, chans []satori.Channel) int {
	var list []api.Session
	for _, ch := range chans {
		if ch.Data != nil {
			list = append(list, *ch.Data)
		}
	}
	ids := make([]string, len(list))
	for i, s := range list {
		ids[i] = s.ID
	}
	home, names := e.home(), e.machineNames()
	found := 0
	for _, s := range underParents(list) {
		if !matches(c.grep, s.ID, s.Name, s.Title, s.Cwd, lastText(s)) {
			continue
		}
		found++
		fmt.Fprintln(e.out, line(s, ids, home, names))
		if t := lastText(s); t != "" && (len(c.grep) > 0 || c.method == "channel.get") {
			fmt.Fprintf(e.out, "\t%s\n", clip(t, askLen))
		}
	}
	if found == 0 && len(c.grep) > 0 {
		return e.failf("没找到：%s", strings.Join(c.grep, " "))
	}
	return exitDone
}

// matches: every word is in one of fields, case aside.
func matches(words []string, fields ...string) bool {
	hay := strings.ToLower(strings.Join(fields, "\n"))
	return !slices.ContainsFunc(words, func(w string) bool { return !strings.Contains(hay, w) })
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
	}
	out.text = satori.Plain(rest, nil)
	return out
}

// showTurns writes message.list's page as ir read did: each message of a person and the agent's
// last words after it, cut short (with --grep, the turns every word is in).
func (e *env) showTurns(c *apiCall, ms []satori.Message) {
	var blocks []api.Block
	for _, m := range ms {
		s := readMessage(m)
		blocks = append(blocks, api.Block{Kind: api.BlockKind(s.kind), Text: s.text, From: s.from})
	}
	ts := turns(blocks)
	ts = slices.DeleteFunc(ts, func(t turn) bool { return !matches(c.grep, t.asked, t.reply) })
	for i, t := range ts {
		if t.asked != "" {
			fmt.Fprintf(e.out, "> %s%s\n", fromTag(t.from), clip(t.asked, askLen))
		}
		if t.reply != "" {
			fmt.Fprintf(e.out, "  %s\n", clip(t.reply, replyLen))
		} else {
			fmt.Fprintln(e.out, "  （没有回复）")
		}
		if i < len(ts)-1 {
			fmt.Fprintln(e.out)
		}
	}
}
