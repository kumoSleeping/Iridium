package transcript

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"sort"
	"strconv"
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
	call := func(name string, input any, result string, failed bool, images ...api.Image) {
		b, _ := json.MarshalIndent(input, "", "  ")
		in, n := clip(string(b), max)
		add(api.Block{Kind: api.BlockKindTool, ID: it.ID, Name: name, Input: in, Length: n})
		text, m := clip(result, max)
		add(api.Block{Kind: api.BlockKindResult, ID: it.ID, Text: text, Length: m, Error: failed, Images: images})
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
			if terminal, tag, ok := sources(it.ClientID); ok {
				b.From, b.FromTerminal = tag, terminal
				b.Text = strings.TrimPrefix(text, "["+tag+"] ")
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
		var images []api.Image
		if it.Result != nil {
			out, isErr, images = joined(it.Result.Content), it.Result.IsError, pictures(it.Result.Content, max < 0)
		}
		if out == "" && len(it.Error) > 0 && string(it.Error) != "null" {
			out = string(it.Error)
		}
		call("mcp__"+it.Server+"__"+it.Tool, args, out, failed || isErr, images...)
	case "ImageView":
		// The picture is not on this line: the output of the script that viewed it has it
		// (codexShown). Its type by its name, till it is read.
		path := strings.TrimPrefix(it.Path, "file://")
		call("view_image", map[string]any{"path": path}, "", false, api.Image{Mime: mime.TypeByExtension(strings.ToLower(filepath.Ext(path))), Later: true})
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

// CodexThreadTime is when thread id was made, for a UUIDv7 (Codex's thread ids): its first 48
// bits are Unix milliseconds. ok false for any other id (an older Codex's).
func CodexThreadTime(id string) (t time.Time, ok bool) {
	if len(id) != 36 || id[8] != '-' || id[13] != '-' || id[18] != '-' || id[23] != '-' || id[14] != '7' || !validID(id) {
		return time.Time{}, false
	}
	ms, err := strconv.ParseInt(id[:8]+id[9:13], 16, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.UnixMilli(ms), true
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

// codexShown is the picture the ImageView on the line at off (n bytes) showed the model. In code
// mode the model does not call view_image itself: a script it runs does (exec, a custom tool
// call), and the model sees what the script hands on with image(), in the script's output
// (custom_tool_call_output: input_image items, data URLs). Codex records an ImageView for each
// view_image between the call and its output, in the order they end, not the order the script
// hands their pictures on: only a script that viewed one picture and showed one, the only call
// open as it did, tells which is which (the user's rollouts, 2026-11-09: two viewed at once came
// in the other order). Before the output is written, while its turn goes on, ErrNotYet. size:
// where the record's whole lines end.
func codexShown(r io.ReaderAt, size, off, n int64) (api.Image, error) {
	// Back to the call open as the view ended, the only one: back from it, an output or a turn
	// comes before any other call.
	call, pos := "", off
	for k := 0; ; k++ {
		if pos <= 0 {
			if call == "" {
				return api.Image{}, ErrNoImage
			}
			break
		}
		if k > codexWindow {
			return api.Image{}, ErrNoImage
		}
		start, line, err := lineBefore(r, pos)
		if err != nil {
			return api.Image{}, err
		}
		pos = start
		kind, id, _ := codexStep(line)
		if kind == codexView && call == "" || kind == codexCall && call != "" || (kind == codexOutput || kind == codexEnd) && call == "" {
			return api.Image{}, ErrNoImage
		}
		if kind == codexCall {
			call = id
		} else if call != "" && (kind == codexOutput || kind == codexEnd) {
			break
		}
	}
	// On to its output: no other view or call first, and one picture.
	from := off + n + 1
	br := bufio.NewReaderSize(io.NewSectionReader(r, from, max(size-from, 0)), 64<<10)
	for k := 0; k <= codexWindow; k++ {
		line, err := longLine(br)
		if err == io.EOF {
			return api.Image{}, ErrNotYet
		}
		if err != nil {
			return api.Image{}, err
		}
		switch kind, id, output := codexStep(line); kind {
		case codexView, codexCall, codexEnd:
			return api.Image{}, ErrNoImage
		case codexOutput:
			images := pictures(output, true)
			if id != call || len(images) != 1 {
				return api.Image{}, ErrNoImage
			}
			return images[0], nil
		}
	}
	return api.Image{}, ErrNoImage
}

// codexWindow is the lines codexShown reads each way from an ImageView, at most.
const codexWindow = 10000

// What a rollout line is to codexShown.
const (
	codexOther  = iota
	codexView   // an ImageView completed
	codexCall   // a tool call the model made (a script's, or another's)
	codexOutput // a call's output, as the model was given it
	codexEnd    // a turn began or ended: no call of the one before is open
)

// codexStep is what line is to codexShown: its call's id for a call or an output, and an
// output's content.
func codexStep(line []byte) (kind int, call string, output json.RawMessage) {
	var l struct {
		Type    string `json:"type"`
		Payload struct {
			Type   string `json:"type"`
			CallID string `json:"call_id"`
			Item   struct {
				Type string `json:"type"`
			} `json:"item"`
			Output json.RawMessage `json:"output"`
		} `json:"payload"`
	}
	if json.Unmarshal(line, &l) != nil {
		return codexOther, "", nil
	}
	p := l.Payload
	switch {
	case l.Type == "event_msg" && p.Type == "item_completed" && p.Item.Type == "ImageView":
		return codexView, "", nil
	case l.Type == "response_item" && (p.Type == "custom_tool_call" || p.Type == "function_call"):
		return codexCall, p.CallID, nil
	case l.Type == "response_item" && (p.Type == "custom_tool_call_output" || p.Type == "function_call_output"):
		return codexOutput, p.CallID, p.Output
	case l.Type == "event_msg" && (p.Type == "task_started" || p.Type == "task_complete" || p.Type == "turn_aborted"):
		return codexEnd, "", nil
	}
	return codexOther, "", nil
}

// maxLine is the longest line read whole outside the index (a script's output with its
// pictures).
const maxLine = 64 << 20

// lineBefore is the line that ends just before pos (a line's start) and where it starts: its
// start found first, then the line read once.
func lineBefore(r io.ReaderAt, pos int64) (int64, []byte, error) {
	end := pos - 1 // its newline
	start := int64(0)
	buf := make([]byte, 64<<10)
	for at := end; at > 0; {
		from := max(at-int64(len(buf)), 0)
		chunk := buf[:at-from]
		if _, err := r.ReadAt(chunk, from); err != nil {
			return 0, nil, err
		}
		if i := bytes.LastIndexByte(chunk, '\n'); i >= 0 {
			start = from + int64(i) + 1
			break
		}
		if end-from > maxLine {
			return 0, nil, ErrNoImage
		}
		at = from
	}
	line := make([]byte, end-start)
	if _, err := r.ReadAt(line, start); err != nil {
		return 0, nil, err
	}
	return start, line, nil
}

// longLine is br's next whole line, without its newline.
func longLine(br *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		chunk, err := br.ReadSlice('\n')
		if err == bufio.ErrBufferFull {
			if line = append(line, chunk...); len(line) > maxLine {
				return nil, ErrNoImage
			}
			continue
		}
		if err != nil {
			return nil, err
		}
		return append(line, chunk[:len(chunk)-1]...), nil
	}
}
