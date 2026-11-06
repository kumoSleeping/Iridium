package transcript

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/kumoSleeping/iridium/internal/api"
)

// Dsh is DeepSeek Harness's session format (dsh 0.2.2, version 4): a session header, then one
// event {type, seq, time, data} per line, the file zstd-compressed (session.vN.jsonl.zstd). Kept:
// what you said (user/message of source "user"; a slash command you ran, command/run, with its
// answer, command/done, as a local command's output), what the model said and thought and its
// tool calls (assistant/message), tool results (tool/result), compaction (compaction/summary),
// a turn stopped (turn/end aborted or interrupted) or failed (error, max-tokens). Skipped: the
// user/messages dsh writes for the model (runtime and time snapshots, notices of a changed
// permission or plan mode, the compaction's checkpoint), the system prompt, requests, steps, jobs
// and every other kind. The title is the last session/title event (File.Title).
var Dsh Format = dshBlocks

// dshName is a dsh session file's name.
var dshName = regexp.MustCompile(`^session(\.v(\d+))?\.jsonl(\.zstd?)?$`)

// dshKept is the events that give blocks; others are not parsed.
var dshKept = map[string]bool{
	"user/message": true, "assistant/message": true, "tool/result": true, "turn/end": true,
	"compaction/summary": true, "command/run": true, "command/done": true,
}

var dshPrefix = []byte(`{"type":"`)

// dshType is the type of an event line, read off its start; ok false if it does not start so.
func dshType(raw []byte) (string, bool) {
	rest, ok := bytes.CutPrefix(raw, dshPrefix)
	if !ok {
		return "", false
	}
	i := bytes.IndexByte(rest, '"')
	if i < 0 {
		return "", false
	}
	return string(rest[:i]), true
}

type dshEvent struct {
	Type string  `json:"type"`
	Time int64   `json:"time"`
	Data dshData `json:"data"`
}

type dshData struct {
	// user/message
	ID      string          `json:"id"`
	Content json.RawMessage `json:"content"`
	Source  struct {
		Kind string `json:"kind"`
	} `json:"source"`
	// assistant/message, tool/result
	Message *struct {
		Content    json.RawMessage `json:"content"`
		ToolCallID string          `json:"toolCallId"`
		IsError    bool            `json:"isError"`
	} `json:"message"`
	// turn/end
	Reason *struct {
		Kind  string `json:"kind"`
		Error *struct {
			Message string `json:"message"`
			Code    string `json:"code"`
		} `json:"error"`
	} `json:"reason"`
	// compaction/summary: its summary; none from a command, the agent's own
	Summary         json.RawMessage `json:"summary"`
	SourceCommandID string          `json:"sourceCommandId"`
	// command/run, command/done
	CommandID string `json:"commandId"`
	Name      string `json:"name"`
	Args      string `json:"args"`
	Kind      string `json:"kind"`
	Text      string `json:"text"`
}

type dshContent struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

func dshBlocks(off int64, raw []byte, max int, sources Sources) []api.Block {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil
	}
	if t, ok := dshType(trimmed); ok && !dshKept[t] {
		return nil
	}
	var l dshEvent
	if trimmed[0] != '{' || json.Unmarshal(raw, &l) != nil {
		text, n := clip(string(raw), max)
		return []api.Block{{At: fmt.Sprintf("%d.0", off), Kind: api.BlockKindSystem, Event: api.SystemEventUnreadable, Text: text, Length: n}}
	}
	var blocks []api.Block
	add := func(b api.Block) {
		b.At = fmt.Sprintf("%d.%d", off, len(blocks))
		b.Time = l.Time
		blocks = append(blocks, b)
	}
	user := func(text, id string) {
		if strings.TrimSpace(text) == "" {
			return
		}
		b := api.Block{Kind: api.BlockKindUser, Text: text, ID: id}
		if sources != nil {
			if terminal, tag, ok := sources(id); ok && id != "" {
				b.From, b.FromTerminal = tag, terminal
				b.Text = strings.TrimPrefix(text, "["+tag+"] ")
			} else if s := piSource.FindStringSubmatch(text); s != nil {
				// dsh gives a prompt an id of its own (session/prompt takes none), so an id of
				// Iridium's is seldom here: the tag Iridium wrote before it tells, as with pi.
				b.From, b.Text = s[1], text[len(s[0]):]
			}
		}
		if b.From == api.SourceIridium {
			b = api.Block{Kind: api.BlockKindSystem, Event: api.SystemEventIridium, Text: b.Text, From: b.From, ID: b.ID}
		}
		add(b)
	}
	d := &l.Data
	switch l.Type {
	case "user/message":
		// Yours only: dsh writes the model's context as user messages of other sources.
		if d.Source.Kind == "user" {
			user(joined(d.Content), d.ID)
		}
	case "command/run":
		// /compact: the compaction says it. /permission and /plan are the approval mode, which the
		// driver sets at every start and the page shows as a setting, not as words; /plan's message
		// dsh sends on as a user/message of its own.
		if d.Source.Kind != "user" || d.Name == "" || d.Name == "compact" || d.Name == "permission" || d.Name == "plan" {
			break
		}
		line := "/" + d.Name
		if args := strings.TrimSpace(d.Args); args != "" {
			line += " " + args
		}
		user(line, d.CommandID)
	case "command/done":
		// The answers of the mode commands left out above (modeAnswer).
		if modeAnswer.MatchString(d.Text) {
			break
		}
		if strings.TrimSpace(d.Text) != "" {
			text, n := clip(d.Text, max)
			add(api.Block{Kind: api.BlockKindSystem, Event: api.SystemEventLocalCommand, Text: text, Length: n})
		}
	case "assistant/message":
		if d.Message == nil {
			break
		}
		var items []dshContent
		_ = json.Unmarshal(d.Message.Content, &items)
		for _, i := range items {
			switch i.Type {
			case "text":
				if strings.TrimSpace(i.Text) != "" {
					add(api.Block{Kind: api.BlockKindAssistant, Text: i.Text})
				}
			case "reasoning", "thinking":
				if strings.TrimSpace(i.Text) != "" {
					text, n := clip(i.Text, max)
					add(api.Block{Kind: api.BlockKindThinking, Text: text, Length: n})
				}
			case "tool-call":
				text, n := clip(dshArguments(i.Arguments), max)
				add(api.Block{Kind: api.BlockKindTool, ID: i.ID, Name: i.Name, Input: text, Length: n})
			}
		}
	case "tool/result":
		if d.Message == nil {
			break
		}
		text, n := clip(joined(d.Message.Content), max)
		add(api.Block{Kind: api.BlockKindResult, ID: d.Message.ToolCallID, Text: text, Length: n, Error: d.Message.IsError})
	case "compaction/summary":
		text, n := clip(joined(d.Summary), max)
		b := api.Block{Kind: api.BlockKindSystem, Event: api.SystemEventCompact, Text: text, Length: n}
		if d.SourceCommandID == "" {
			b.Compact = &api.Compaction{Auto: true}
		}
		add(b)
	case "turn/end":
		if d.Reason == nil {
			break
		}
		switch d.Reason.Kind {
		case "aborted", "interrupted":
			add(api.Block{Kind: api.BlockKindSystem, Event: api.SystemEventInterrupted})
		case "error":
			msg := "error"
			if e := d.Reason.Error; e != nil {
				msg = cmpOr(e.Message, cmpOr(e.Code, msg))
			}
			text, n := clip(msg, max)
			add(api.Block{Kind: api.BlockKindSystem, Event: api.SystemEventAPIError, Text: text, Length: n})
		case "max-tokens":
			add(api.Block{Kind: api.BlockKindSystem, Event: api.SystemEventAPIError, Text: "The reply reached the maximum output tokens."})
		}
	}
	return blocks
}

// dshArguments is a tool call's arguments, a JSON text in a string, indented.
func dshArguments(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) != nil {
		s = string(raw)
	}
	if strings.TrimSpace(s) == "" {
		return "{}"
	}
	var buf bytes.Buffer
	if json.Indent(&buf, []byte(s), "", "  ") == nil {
		return buf.String()
	}
	return s
}

// modeAnswer: what dsh answers /permission and /plan with (dsh 0.2.2: "preset <name>", "Plan mode
// on. ...", "Plan mode off.", "Plan mode is already inactive.", "Plan mode entry cancelled.",
// "Entering plan mode ...", "Leaving plan mode ...").
var modeAnswer = regexp.MustCompile(`^(preset (read-only|workspace-write|danger-full-access)|(Plan mode|Entering plan mode|Leaving plan mode)\b.*)$`)

// leadTag: Iridium's "[tag] " at the head of a message, which dsh's fallback title (the first
// message as written) keeps.
var leadTag = regexp.MustCompile(`^\[[^\]\n]{1,80}\] +`)

var titlePrefix = []byte(`{"type":"session/title"`)

// dshTitle is the title a session/title event gives; false for another line.
func dshTitle(raw []byte) (string, bool) {
	if !bytes.HasPrefix(raw, titlePrefix) {
		return "", false
	}
	var l struct {
		Data struct {
			Title  string `json:"title"`
			Source struct {
				Kind string `json:"kind"`
			} `json:"source"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &l) != nil {
		return "", false
	}
	t := l.Data.Title
	if l.Data.Source.Kind == "fallback" {
		t = leadTag.ReplaceAllString(t, "")
	}
	if strings.TrimSpace(t) == "" {
		return "", false
	}
	return t, true
}

// DshHome is $DSH_HOME in env, else ~/.dsh.
func DshHome(env []string) string {
	if d := getenv(env, "DSH_HOME"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".dsh")
}

// FindDsh is the file of dsh session id under home (home/sessions/--<project>--/<id>/), its
// session.vN.jsonl.zstd of the highest N (an older dsh's session.jsonl counting as 0); "" if
// there is none yet.
func FindDsh(home, id string) (string, error) {
	if !dshID(id) {
		return "", fmt.Errorf("transcript: not a dsh session id: %q", id)
	}
	dirs, err := filepath.Glob(filepath.Join(home, "sessions", "*", id))
	if err != nil {
		return "", err
	}
	best, bestN := "", -1
	for _, dir := range dirs {
		ents, _ := os.ReadDir(dir)
		for _, e := range ents {
			m := dshName.FindStringSubmatch(e.Name())
			if m == nil || !e.Type().IsRegular() {
				continue
			}
			n, _ := strconv.Atoi(m[2])
			if n > bestN {
				best, bestN = filepath.Join(dir, e.Name()), n
			}
		}
	}
	return best, nil
}

// dshID: dsh takes any string as a session id; one that is a single path element, of letters,
// digits, '-' and '_', is taken here.
func dshID(id string) bool {
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

// GlanceDsh is what a session list needs of the dsh session file at path: its header's cwd, a
// subagent's parent, the title dsh gave it (else the first thing you said) and what was said
// last.
func GlanceDsh(path string, sources Sources) (Glimpse, error) {
	var g Glimpse
	f := OpenAs(path, Dsh, sources)
	title, err := f.Title()
	if err != nil {
		return g, err
	}
	g.Title = title
	if g.Last, err = f.LastSaid(); err != nil {
		return g, err
	}
	f.mu.Lock()
	r, done, err := f.text()
	f.mu.Unlock()
	if err != nil {
		return g, err
	}
	defer done()
	head := make([]byte, 64<<10)
	n, err := r.ReadAt(head, 0)
	if err != nil && err != io.EOF {
		return g, err
	}
	head, _, _ = bytes.Cut(head[:n], []byte("\n"))
	var h struct {
		Type          string `json:"type"`
		Cwd           string `json:"cwd"`
		Origin        string `json:"origin"`
		ParentSession string `json:"parentSession"`
	}
	if json.Unmarshal(head, &h) == nil && h.Type == "session" {
		g.Cwd = h.Cwd
		g.Sidechain = h.Origin == "subagent"
		g.Parent = h.ParentSession
	}
	return g, nil
}
