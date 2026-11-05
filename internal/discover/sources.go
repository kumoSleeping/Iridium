package discover

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/transcript"
)

// A transcript on disk: the session it is, by the agent's own id.
type onDisk struct {
	id    string
	agent api.Agent
	path  string
}

// glimpse is what the list needs of one transcript. skip: not a session of its own (an Agent
// tool's subtask, a Codex subagent's thread).
type glimpse struct {
	cwd   string
	title string
	last  *api.LastSaid
	skip  bool
}

var (
	idPattern = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
	dshFile   = regexp.MustCompile(`^session(\.v(\d+))?\.jsonl(\.zstd)?$`)
)

func validID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, c := range id {
		if !(c == '-' || c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
			return false
		}
	}
	return true
}

func subdirs(dir string) []string {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		if e.IsDir() {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	return out
}

func filesIn(dir, suffix string) []string {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		if e.Type().IsRegular() && strings.HasSuffix(e.Name(), suffix) {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	return out
}

// Claude: projects/<folder>/<id>.jsonl. Agent tool subtasks are in <id>/subagents/, never
// listed; older ones were agent-<id>.jsonl beside the sessions.
func (x *Index) claudeDirs() []string { return subdirs(filepath.Join(x.o.ClaudeDir, "projects")) }

func (x *Index) claudeIn(dir string) []onDisk {
	var out []onDisk
	for _, p := range filesIn(dir, ".jsonl") {
		id := strings.TrimSuffix(filepath.Base(p), ".jsonl")
		if strings.HasPrefix(id, "agent-") || !validID(id) {
			continue
		}
		out = append(out, onDisk{id: id, agent: api.AgentClaude, path: p})
	}
	return out
}

// Codex: sessions/YYYY/MM/DD/rollout-<time>-<thread id>.jsonl.
func (x *Index) codexDirs() []string {
	var out []string
	for _, y := range subdirs(filepath.Join(x.o.CodexHome, "sessions")) {
		for _, m := range subdirs(y) {
			out = append(out, subdirs(m)...)
		}
	}
	return out
}

func (x *Index) codexIn(dir string) []onDisk {
	var out []onDisk
	for _, p := range filesIn(dir, ".jsonl") {
		name := filepath.Base(p)
		if !strings.HasPrefix(name, "rollout-") {
			continue
		}
		id := idPattern.FindString(name[max(0, len(name)-6-36):])
		if id == "" {
			continue
		}
		out = append(out, onDisk{id: id, agent: api.AgentCodex, path: p})
	}
	return out
}

// pi: <sessions>/--<cwd>--/<time>_<id>.jsonl.
func (x *Index) piDirs() []string { return subdirs(x.o.PiSessions) }

func (x *Index) piIn(dir string) []onDisk {
	var out []onDisk
	for _, p := range filesIn(dir, ".jsonl") {
		name := strings.TrimSuffix(filepath.Base(p), ".jsonl")
		_, id, ok := strings.Cut(name, "_")
		if !ok || !validID(id) {
			continue
		}
		out = append(out, onDisk{id: id, agent: api.AgentPi, path: p})
	}
	return out
}

// dsh: sessions/--<projectKey>--/<id>/session.v<N>.jsonl.zstd, the highest N counting. zstd: for
// now only its path and time are read.
func (x *Index) dshDirs() []string { return subdirs(filepath.Join(x.o.DshHome, "sessions")) }

func (x *Index) dshIn(dir string) []onDisk {
	var out []onDisk
	for _, s := range subdirs(dir) {
		id := filepath.Base(s)
		if !validID(id) {
			continue
		}
		best, bestN := "", -1
		ents, _ := os.ReadDir(s)
		for _, e := range ents {
			m := dshFile.FindStringSubmatch(e.Name())
			if m == nil || !e.Type().IsRegular() {
				continue
			}
			n, _ := strconv.Atoi(m[2])
			if n > bestN {
				best, bestN = filepath.Join(s, e.Name()), n
			}
		}
		if best != "" {
			out = append(out, onDisk{id: id, agent: api.AgentDsh, path: best})
		}
	}
	return out
}

// ---- reading them ----

func (x *Index) glance(d onDisk) glimpse {
	switch d.agent {
	case api.AgentClaude:
		g, err := transcript.Glance(d.path, x.o.Store.Source)
		if err != nil {
			return glimpse{}
		}
		return glimpse{cwd: g.Cwd, title: g.Title, last: g.Last, skip: g.Sidechain}
	case api.AgentCodex:
		return x.glanceCodex(d)
	case api.AgentPi:
		return x.glancePi(d)
	case api.AgentDsh:
		return glimpse{cwd: x.dshCwd(filepath.Base(filepath.Dir(filepath.Dir(d.path))))}
	}
	return glimpse{}
}

type codexLine struct {
	Timestamp string `json:"timestamp"`
	Type      string `json:"type"`
	Payload   struct {
		Type   string          `json:"type"`
		ID     string          `json:"id"`
		Cwd    string          `json:"cwd"`
		Source json.RawMessage `json:"source"`
		Item   *struct {
			Type     string          `json:"type"`
			ClientID string          `json:"client_id"`
			Content  json.RawMessage `json:"content"`
		} `json:"item"`
	} `json:"payload"`
}

// said is what a Codex line says, if you or it said something: its item_completed events'
// UserMessage and AgentMessage.
func (x *Index) codexSaid(l *codexLine) *api.LastSaid {
	it := l.Payload.Item
	if l.Type != "event_msg" || l.Payload.Type != "item_completed" || it == nil {
		return nil
	}
	user := it.Type == "UserMessage"
	if !user && it.Type != "AgentMessage" {
		return nil
	}
	text := contentText(it.Content)
	if text == "" {
		return nil
	}
	from := ""
	if user {
		text, from = x.unprefix(text, it.ClientID)
	}
	return transcript.Said(user, text, from, millis(l.Timestamp))
}

func (x *Index) glanceCodex(d onDisk) glimpse {
	var g glimpse
	f, err := os.Open(d.path)
	if err != nil {
		return g
	}
	defer f.Close()
	first := true
	transcript.Lines(io.LimitReader(f, transcript.HeadBytes), 1<<20, func(raw []byte) bool {
		var l codexLine
		if json.Unmarshal(raw, &l) != nil {
			return !first
		}
		if first {
			first = false
			if l.Type != "session_meta" {
				return false
			}
			g.cwd = l.Payload.Cwd
			// A subagent's thread is its parent's: {"subagent": …}.
			var src map[string]json.RawMessage
			if json.Unmarshal(l.Payload.Source, &src) == nil && src["subagent"] != nil {
				g.skip = true
				return false
			}
			return true
		}
		if s := x.codexSaid(&l); s != nil && s.Role == "user" {
			g.title = cut(s.Text, transcript.TitleLen)
			return false
		}
		return true
	})
	if g.skip {
		return g
	}
	if name := x.codexNames()[d.id]; name != "" {
		g.title = name
	}
	g.last, _ = transcript.Tail(f, func(_ int64, raw []byte) *api.LastSaid {
		var l codexLine
		if json.Unmarshal(raw, &l) != nil {
			return nil
		}
		return x.codexSaid(&l)
	})
	return g
}

// codexNames is Codex's thread names ($CODEX_HOME/session_index.jsonl, the last line of a thread
// counting), read again when the file changed.
func (x *Index) codexNames() map[string]string {
	p := filepath.Join(x.o.CodexHome, "session_index.jsonl")
	st, err := os.Stat(p)
	if err != nil {
		return nil
	}
	x.namesMu.Lock()
	defer x.namesMu.Unlock()
	if st.ModTime().Equal(x.namesAt) && x.names != nil {
		return x.names
	}
	names := map[string]string{}
	if f, err := os.Open(p); err == nil {
		transcript.Lines(f, 1<<20, func(raw []byte) bool {
			var l struct {
				ID   string `json:"id"`
				Name string `json:"thread_name"`
			}
			if json.Unmarshal(raw, &l) == nil && l.ID != "" {
				names[l.ID] = oneLine(l.Name)
			}
			return true
		})
		f.Close()
	}
	x.names, x.namesAt = names, st.ModTime()
	return names
}

type piLine struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	Cwd       string `json:"cwd"`
	Name      string `json:"name"`
	Timestamp string `json:"timestamp"`
	Message   *struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

func piSaid(l *piLine) *api.LastSaid {
	if l.Type != "message" || l.Message == nil || (l.Message.Role != "user" && l.Message.Role != "assistant") {
		return nil
	}
	text := contentText(l.Message.Content)
	if text == "" {
		return nil
	}
	return transcript.Said(l.Message.Role == "user", text, "", millis(l.Timestamp))
}

func (x *Index) glancePi(d onDisk) glimpse {
	var g glimpse
	f, err := os.Open(d.path)
	if err != nil {
		return g
	}
	defer f.Close()
	first := true
	name := ""
	transcript.Lines(io.LimitReader(f, transcript.HeadBytes), 1<<20, func(raw []byte) bool {
		var l piLine
		if json.Unmarshal(raw, &l) != nil {
			return !first
		}
		if first {
			first = false
			g.cwd = l.Cwd
			return l.Type == "session"
		}
		if l.Type == "session_info" {
			name = oneLine(l.Name)
		}
		if s := piSaid(&l); s != nil && s.Role == "user" && g.title == "" {
			g.title = cut(s.Text, transcript.TitleLen)
		}
		return true
	})
	// The name, set with /name, the last one counting: the end has the newest, so the tail's
	// is the one, read from the end until both are found.
	var last *api.LastSaid
	tailName := ""
	transcript.Tail(f, func(_ int64, raw []byte) *struct{} {
		var l piLine
		if json.Unmarshal(raw, &l) != nil {
			return nil
		}
		if l.Type == "session_info" && tailName == "" {
			tailName = oneLine(l.Name)
		}
		if last == nil {
			last = piSaid(&l)
		}
		if last != nil && (tailName != "" || name == "") {
			return &struct{}{}
		}
		return nil
	})
	if tailName != "" {
		name = tailName
	}
	g.last = last
	if name != "" {
		g.title = name
	}
	return g
}

// contentText is the text of a message's content: a string, or its text blocks (Codex's are
// "Text"), those wrapped in tags (what the agent wrote itself) left out.
func contentText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if (b.Type == "text" || b.Type == "Text") && b.Text != "" && !strings.HasPrefix(strings.TrimSpace(b.Text), "<") {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n\n")
}

// unprefix takes the "[source] " Iridium put before a message it sent as input id off.
func (x *Index) unprefix(text, id string) (string, string) {
	if id == "" {
		return text, ""
	}
	_, from, ok := x.o.Store.Source(id) // the terminal, and the tag it was sent with
	if !ok {
		return text, ""
	}
	return strings.TrimPrefix(text, "["+from+"] "), from
}

func millis(ts string) int64 {
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return 0
	}
	return t.UnixMilli()
}

var space = regexp.MustCompile(`\s+`)

func oneLine(s string) string { return strings.TrimSpace(space.ReplaceAllString(s, " ")) }

func cut(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// dshCwd is the folder of a dsh project directory "--<key>--", whose key is the path with "/",
// "\" and ":" made "-" and runs of "-" made one, so it can stand for several paths: the one that
// exists is found by walking down from /. "" if none does.
func (x *Index) dshCwd(dir string) string {
	key := strings.TrimSuffix(strings.TrimPrefix(dir, "--"), "--")
	if key == "" || key == "_no-cwd" {
		return ""
	}
	x.namesMu.Lock()
	defer x.namesMu.Unlock()
	if p, ok := x.dshKeys[key]; ok {
		return p
	}
	budget := 200 // directories read at most
	var walk func(dir, rest string) string
	walk = func(dir, rest string) string {
		if budget--; budget < 0 {
			return ""
		}
		ents, _ := os.ReadDir(dir)
		for _, e := range ents {
			if !e.IsDir() {
				continue
			}
			k := projectKey(e.Name())
			if k == rest {
				return filepath.Join(dir, e.Name())
			}
			if strings.HasPrefix(rest, k+"-") {
				if p := walk(filepath.Join(dir, e.Name()), rest[len(k)+1:]); p != "" {
					return p
				}
			}
		}
		return ""
	}
	p := walk("/", key)
	x.dshKeys[key] = p
	return p
}

var keySep = regexp.MustCompile(`[/\\:]+`)
var dashes = regexp.MustCompile(`-+`)

func projectKey(s string) string {
	return strings.TrimLeft(dashes.ReplaceAllString(keySep.ReplaceAllString(s, "-"), "-"), "-")
}
