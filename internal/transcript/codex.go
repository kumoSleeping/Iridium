package transcript

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
)

// Codex is codex-cli's rollout format (Rhodium §6.4, §7.7; 0.162.1–0.165.0): one
// {timestamp, type, payload} per line. Only event_msg lines are read: item_completed's items
// (what you and it said, its thought summaries, its tool calls each with its result, its
// compaction) and turn_aborted. response_item lines are what Codex sends the model (your skills,
// its system prompt, the tool calls' raw code) and are skipped, as is every other kind.
var Codex Format = codexBlocks

type codexLine struct {
	Timestamp string `json:"timestamp"`
	Type      string `json:"type"`
	Payload   struct {
		Type string          `json:"type"`
		Item json.RawMessage `json:"item"`
	} `json:"payload"`
}

type codexItem struct {
	Type     string          `json:"type"`
	ID       string          `json:"id"`
	ClientID string          `json:"client_id"`
	Content  json.RawMessage `json:"content"`
	Summary  []string        `json:"summary_text"`
	Status   string          `json:"status"`
	// CommandExecution
	Command []string `json:"command"`
	Output  string   `json:"aggregated_output"`
	// FileChange: by path
	Changes map[string]json.RawMessage `json:"changes"`
	Stdout  string                     `json:"stdout"`
	Stderr  string                     `json:"stderr"`
	// McpToolCall
	Server    string          `json:"server"`
	Tool      string          `json:"tool"`
	Arguments json.RawMessage `json:"arguments"`
	Result    *struct {
		Content json.RawMessage `json:"content"`
		IsError bool            `json:"isError"`
	} `json:"result"`
	Error json.RawMessage `json:"error"`
	// ImageView
	Path string `json:"path"`
	// Extension
	Kind    string `json:"kind"`
	Query   string `json:"query"`
	Results []struct {
		Title string `json:"title"`
		URL   string `json:"url"`
	} `json:"results"`
	// SubAgentActivity
	AgentPath string `json:"agent_path"`
}

func codexBlocks(off int64, raw []byte, max int, sources Sources) []api.Block {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil
	}
	var l codexLine
	if trimmed[0] != '{' || json.Unmarshal(raw, &l) != nil {
		text, n := clip(string(raw), max)
		return []api.Block{{At: fmt.Sprintf("%d.0", off), Kind: api.BlockKindSystem, Event: api.SystemEventUnreadable, Text: text, Length: n}}
	}
	if l.Type != "event_msg" {
		return nil
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
	switch l.Payload.Type {
	case "turn_aborted":
		add(api.Block{Kind: api.BlockKindSystem, Event: api.SystemEventInterrupted})
		return blocks
	case "item_completed":
	default:
		return nil
	}
	var it codexItem
	if json.Unmarshal(l.Payload.Item, &it) != nil {
		return nil
	}
	call := func(name string, input any, result string, failed bool) {
		b, _ := json.MarshalIndent(input, "", "  ")
		in, n := clip(string(b), max)
		add(api.Block{Kind: api.BlockKindTool, ID: it.ID, Name: name, Input: in, Length: n})
		text, m := clip(result, max)
		add(api.Block{Kind: api.BlockKindResult, ID: it.ID, Text: text, Length: m, Error: failed})
	}
	failed := it.Status != "" && it.Status != "completed"
	switch it.Type {
	case "UserMessage":
		text := strings.Join(texts(it.Content), "\n\n")
		if strings.TrimSpace(text) == "" {
			break
		}
		b := api.Block{Kind: api.BlockKindUser, Text: text, ID: it.ClientID}
		if sources != nil && it.ClientID != "" {
			if from, ok := sources(it.ClientID); ok {
				b.From = from
				b.Text = strings.TrimPrefix(text, "["+from+"] ")
			}
		}
		add(b)
	case "AgentMessage":
		// Its text blocks are of type "Text".
		var items []struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal(it.Content, &items)
		var said []string
		for _, i := range items {
			said = append(said, i.Text)
		}
		if text := strings.Join(said, ""); strings.TrimSpace(text) != "" {
			add(api.Block{Kind: api.BlockKindAssistant, Text: text})
		}
	case "Reasoning":
		if text := strings.Join(it.Summary, "\n\n"); strings.TrimSpace(text) != "" {
			c, n := clip(text, max)
			add(api.Block{Kind: api.BlockKindThinking, Text: c, Length: n})
		}
	case "ContextCompaction":
		add(api.Block{Kind: api.BlockKindSystem, Event: api.SystemEventCompact})
	case "CommandExecution":
		// What ran: the last of `bash -lc`'s words.
		cmd := ""
		if len(it.Command) > 0 {
			cmd = it.Command[len(it.Command)-1]
		}
		call("exec_command", map[string]any{"command": cmd}, it.Output, failed)
	case "FileChange":
		paths := make([]string, 0, len(it.Changes))
		for p := range it.Changes {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		changes := []map[string]any{}
		for _, p := range paths {
			c := map[string]any{}
			_ = json.Unmarshal(it.Changes[p], &c)
			c["path"] = p
			changes = append(changes, c)
		}
		call("apply_patch", map[string]any{"changes": changes}, it.Stdout+it.Stderr, failed)
	case "McpToolCall":
		args := any(map[string]any{})
		if len(it.Arguments) > 0 && string(it.Arguments) != "null" {
			args = it.Arguments
		}
		out, isErr := "", false
		if it.Result != nil {
			out, isErr = joined(it.Result.Content), it.Result.IsError
		}
		if out == "" && len(it.Error) > 0 && string(it.Error) != "null" {
			out = string(it.Error)
		}
		call("mcp__"+it.Server+"__"+it.Tool, args, out, failed || isErr)
	case "ImageView":
		call("view_image", map[string]any{"path": strings.TrimPrefix(it.Path, "file://")}, "", false)
	case "Extension":
		// Its web search; other kinds (clock.sleep...) are not the conversation.
		if it.Kind != "web.search" {
			break
		}
		var found []string
		for _, r := range it.Results {
			found = append(found, strings.TrimSpace(r.Title+" "+r.URL))
		}
		call("web_search", map[string]any{"query": it.Query}, strings.Join(found, "\n"), false)
	case "SubAgentActivity":
		// A subagent it started is its spawn_agent; their later back and forth is no call.
		if it.Kind == "started" {
			call("spawn_agent", map[string]any{"agent_path": it.AgentPath}, "", false)
		}
	}
	return blocks
}

// CodexHome is $CODEX_HOME in env, else ~/.codex.
func CodexHome(env []string) string {
	if d := getenv(env, "CODEX_HOME"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".codex")
}

// FindCodex is the rollout of thread id under home's sessions (or archived_sessions); "" if none
// yet: Codex writes it at the first turn.
func FindCodex(home, id string) (string, error) {
	if !validID(id) {
		return "", fmt.Errorf("transcript: not a thread id: %q", id)
	}
	for _, dir := range []string{"sessions", "archived_sessions"} {
		m, err := filepath.Glob(filepath.Join(home, dir, "*", "*", "*", "rollout-*-"+id+".jsonl"))
		if err != nil {
			return "", err
		}
		if len(m) > 0 {
			return m[0], nil
		}
		if m, _ = filepath.Glob(filepath.Join(home, dir, "rollout-*-"+id+".jsonl")); len(m) > 0 {
			return m[0], nil
		}
	}
	return "", nil
}

func getenv(env []string, k string) string {
	v := ""
	for _, kv := range env {
		if s, ok := strings.CutPrefix(kv, k+"="); ok {
			v = s
		}
	}
	return v
}
