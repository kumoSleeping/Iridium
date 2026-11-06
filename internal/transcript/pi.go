package transcript

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
)

// Pi is pi's session format (Rhodium §6.3, §7.7; pi 1.3.0–1.4.0, version 3): a session header,
// then entries. Kept: message entries of role user, assistant (text, thinking, toolCall),
// toolResult, bashExecution (a `!` command, as a local command's output); compaction. Skipped:
// the system prompt and every other kind.
var Pi Format = piBlocks

// piSource is the source prefix Iridium writes before a message ("[name] "; names are at most
// 12 characters). pi keeps no id of Iridium's with a message, so this is all that tells one: a
// message you typed in pi's terminal starting so would read as from that name too.
var piSource = regexp.MustCompile(`^\[([^\[\]\n]{1,12})\] `)

type piLine struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	Summary   string `json:"summary"`
	// TokensBefore: a compaction's context before it.
	TokensBefore int `json:"tokensBefore"`
	Message      *struct {
		Role       string          `json:"role"`
		Content    json.RawMessage `json:"content"`
		ToolCallID string          `json:"toolCallId"`
		IsError    bool            `json:"isError"`
		Command    string          `json:"command"`
		Output     string          `json:"output"`
		ExitCode   *int            `json:"exitCode"`
		Cancelled  bool            `json:"cancelled"`
	} `json:"message"`
}

type piContent struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// imageRef is a line an extension of pi's adds for each picture it sends along ("[Image #1]: path"),
// the path in your words become "[Image #1]".
var imageRef = regexp.MustCompile(`(?m)^\[Image #(\d+)\]: (\S.*)$\n?`)

// unImage gives your words back as you wrote them: each [Image #n] its path again, the lines that
// named them gone (the user's pi turns the paths Iridium writes into these; 2026-11-06).
func unImage(text string) string {
	refs := imageRef.FindAllStringSubmatch(text, -1)
	if refs == nil {
		return text
	}
	text = imageRef.ReplaceAllString(text, "")
	for _, r := range refs {
		text = strings.ReplaceAll(text, "[Image #"+r[1]+"]", strings.TrimSpace(r[2]))
	}
	return strings.TrimRight(text, "\n")
}

func piBlocks(off int64, raw []byte, max int, sources Sources) []api.Block {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil
	}
	var l piLine
	if trimmed[0] != '{' || json.Unmarshal(raw, &l) != nil {
		text, n := clip(string(raw), max)
		return []api.Block{{At: fmt.Sprintf("%d.0", off), Kind: api.BlockKindSystem, Event: api.SystemEventUnreadable, Text: text, Length: n}}
	}
	var blocks []api.Block
	var t int64
	if ts, err := time.Parse(time.RFC3339Nano, l.Timestamp); err == nil {
		t = ts.UnixMilli()
	}
	add := func(b api.Block) {
		b.At = fmt.Sprintf("%d.%d", off, len(blocks))
		b.Time = t
		blocks = append(blocks, b)
	}
	switch l.Type {
	case "compaction":
		text, n := clip(l.Summary, max)
		b := api.Block{Kind: api.BlockKindSystem, Event: api.SystemEventCompact, Text: text, Length: n}
		if l.TokensBefore > 0 {
			b.Compact = &api.Compaction{Before: l.TokensBefore}
		}
		add(b)
		return blocks
	case "message":
	default:
		return nil
	}
	m := l.Message
	if m == nil {
		return nil
	}
	switch m.Role {
	case "user":
		text := unImage(strings.Join(texts(m.Content), "\n\n"))
		if strings.TrimSpace(text) == "" {
			break
		}
		b := api.Block{Kind: api.BlockKindUser, Text: text}
		if sources != nil {
			if s := piSource.FindStringSubmatch(text); s != nil {
				b.From, b.Text = s[1], text[len(s[0]):]
			}
		}
		add(b)
	case "assistant":
		var items []piContent
		_ = json.Unmarshal(m.Content, &items)
		for _, i := range items {
			switch i.Type {
			case "text":
				if strings.TrimSpace(i.Text) != "" {
					add(api.Block{Kind: api.BlockKindAssistant, Text: i.Text})
				}
			case "thinking":
				if strings.TrimSpace(i.Thinking) != "" {
					text, n := clip(i.Thinking, max)
					add(api.Block{Kind: api.BlockKindThinking, Text: text, Length: n})
				}
			case "toolCall":
				input := "{}"
				var buf bytes.Buffer
				if len(i.Arguments) > 0 && json.Indent(&buf, i.Arguments, "", "  ") == nil {
					input = buf.String()
				}
				text, n := clip(input, max)
				add(api.Block{Kind: api.BlockKindTool, ID: i.ID, Name: i.Name, Input: text, Length: n})
			}
		}
	case "toolResult":
		text, n := clip(joined(m.Content), max)
		add(api.Block{Kind: api.BlockKindResult, ID: m.ToolCallID, Text: text, Length: n, Error: m.IsError})
	case "bashExecution":
		text, n := clip("$ "+m.Command+"\n"+m.Output, max)
		add(api.Block{Kind: api.BlockKindSystem, Event: api.SystemEventLocalCommand, Text: text, Length: n})
	}
	return blocks
}

// PiSessions is where pi keeps the sessions started in cwd: $PI_CODING_AGENT_SESSION_DIR in env
// as it is (no folder per directory), else <$PI_CODING_AGENT_DIR or ~/.pi/agent>/sessions/
// --<cwd, its leading / off, / \ : as ->--.
func PiSessions(env []string, cwd string) string {
	if d := getenv(env, "PI_CODING_AGENT_SESSION_DIR"); d != "" {
		return d
	}
	root := getenv(env, "PI_CODING_AGENT_DIR")
	if root == "" {
		home, _ := os.UserHomeDir()
		root = filepath.Join(home, ".pi", "agent")
	}
	folder := strings.NewReplacer("/", "-", `\`, "-", ":", "-").Replace(strings.TrimLeft(cwd, `/\`))
	return filepath.Join(root, "sessions", "--"+folder+"--")
}

// FindPi is pi session id's file in dir ("<created>_<id>.jsonl"); "" if none yet: pi writes it
// at the first message.
func FindPi(dir, id string) (string, error) {
	if !validID(id) {
		return "", fmt.Errorf("transcript: not a session id: %q", id)
	}
	m, err := filepath.Glob(filepath.Join(dir, "*_"+id+".jsonl"))
	if err != nil || len(m) == 0 {
		return "", err
	}
	return m[0], nil
}
