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
	"regexp"
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

// A Codex view_image's picture is not on its ImageView's line. In code mode the model does not
// call view_image itself: a script it runs does (exec, a custom tool call), and the model sees
// what the script hands on with image(), in the script's output (custom_tool_call_output:
// input_image items, data URLs). Codex records an ImageView for each view_image as it ends, not
// in the order the script hands the pictures on, and a script may go on in the background after
// its call's output (yielded: "Script running with cell ID n", taken up by wait). So a view is
// told only where nothing else could have made it: one call open, no other script running, no
// other view since that call and no other call before its output, which shows one picture (the
// user's rollouts, 2026-11-09). codexViews reads the rollout once, on as it grows, and keeps
// what it found of each view.
type codexViews struct {
	end     int64             // read up to here
	open    map[string]string // calls without their output: the cell each waits for ("" none)
	running map[string]bool   // cells going on after their call's output
	last    string            // the call made last
	since   int               // views since it
	pending map[string]int64  // a call's one view, by its line, till its output
	shots   map[int64]codexShot
	// lost: a long line of the turn was not told by its head: a call or an output may have gone
	// by unseen, so no view of the turn is told.
	lost bool
}

// codexShot is where a view's picture is: the output line (at, n bytes); n 0: nowhere.
type codexShot struct {
	at int64
	n  int
}

// shot is where the picture of the view on the line at off is, read on in r up to size first.
// ErrNoImage: it is not told; ErrNotYet: its script's output is not written yet.
func (c *codexViews) shot(r io.ReaderAt, size, off int64) (codexShot, error) {
	if err := c.read(r, size); err != nil {
		return codexShot{}, err
	}
	if s, ok := c.shots[off]; ok {
		if s.n > 0 {
			return s, nil
		}
		return codexShot{}, ErrNoImage
	}
	for _, v := range c.pending {
		if v == off {
			return codexShot{}, ErrNotYet
		}
	}
	return codexShot{}, ErrNoImage
}

// read reads the rollout on from c.end to size.
func (c *codexViews) read(r io.ReaderAt, size int64) error {
	if c.end >= size {
		return nil
	}
	if c.shots == nil {
		c.shots, c.open, c.running, c.pending = map[int64]codexShot{}, map[string]string{}, map[string]bool{}, map[string]int64{}
	}
	br := bufio.NewReaderSize(io.NewSectionReader(r, c.end, size-c.end), 64<<10)
	for c.end < size {
		head, n, err := lineHead(br)
		if err != nil {
			return err
		}
		c.step(codexStep(head, len(head) == n), c.end, n)
		c.end += int64(n) + 1
	}
	return nil
}

// none: the views waiting for their outputs will not be told.
func (c *codexViews) none() {
	for call, v := range c.pending {
		c.shots[v] = codexShot{}
		delete(c.pending, call)
	}
}

// step takes in line l, at (n bytes long).
func (c *codexViews) step(l codexStepLine, at int64, n int) {
	switch l.kind {
	case codexLost:
		c.none()
		c.lost = true
	case codexEnd:
		c.none()
		clear(c.open)
		clear(c.running)
		c.last, c.since, c.lost = "", 0, false
	case codexCall:
		c.none() // a call began before their outputs
		c.open[l.call], c.last, c.since = l.cell, l.call, 0
	case codexView:
		c.since++
		waits, open := c.open[c.last]
		alone := !c.lost && len(c.open) == 1 && open && c.since == 1 &&
			(waits == "" && len(c.running) == 0 || waits != "" && len(c.running) == 1 && c.running[waits])
		if !alone {
			c.none()
			c.shots[at] = codexShot{}
			return
		}
		c.pending[c.last] = at
	case codexOutput:
		if v, ok := c.pending[l.call]; ok {
			delete(c.pending, l.call)
			c.shots[v] = codexShot{at: at, n: n}
		}
		if waits := c.open[l.call]; waits != "" && l.yielded == "" {
			delete(c.running, waits)
		}
		delete(c.open, l.call)
		if l.yielded != "" {
			c.running[l.yielded] = true
		}
	}
}

// codexShot reads the picture the view on line e showed the model: keep, with its bytes; else
// only what a page shows of it. f.mu is held; r reads the record.
func (f *File) codexShot(r io.ReaderAt, e entry, keep bool) (api.Image, error) {
	s, err := f.codexWhere(r, e)
	if err != nil {
		return api.Image{}, err
	}
	return s.image(r, keep)
}

// codexWhere is where the picture of the view on line e is, the rollout read on first. f.mu is
// held.
func (f *File) codexWhere(r io.ReaderAt, e entry) (codexShot, error) {
	if f.views == nil {
		f.views = &codexViews{}
	}
	return f.views.shot(r, f.size, e.off)
}

// image is the picture on s's output line, read from r: keep, with its bytes.
func (s codexShot) image(r io.ReaderAt, keep bool) (api.Image, error) {
	line := make([]byte, s.n)
	if _, err := r.ReadAt(line, s.at); err != nil {
		return api.Image{}, err
	}
	images := pictures(outputOf(line), keep)
	if len(images) != 1 {
		return api.Image{}, ErrNoImage
	}
	return images[0], nil
}

// What a rollout line is to codexViews.
const (
	codexOther  = iota
	codexView   // an ImageView completed
	codexCall   // a tool call the model made (a script's, or another's)
	codexOutput // a call's output, as the model was given it
	codexEnd    // a turn began or ended: no call of the one before is open
	codexLost   // a long line not told by its head
)

// codexStepLine is a rollout line as codexViews reads it.
type codexStepLine struct {
	kind int
	call string // a call's or an output's call id
	// cell: the script cell a wait call takes up; yielded: the cell an output says goes on.
	cell, yielded string
}

// payloadType is how a rollout line begins its payload: its type is read off the line's start,
// a line of no other kind passed over without reading it whole (a script's output may be tens of
// MB of pictures).
var payloadType = regexp.MustCompile(`"payload":\{"type":"([a-z_]+)"`)

// codexKinds are the payload types codexViews reads.
var codexKinds = map[string]bool{"item_completed": true, "task_started": true, "task_complete": true, "turn_aborted": true,
	"custom_tool_call": true, "function_call": true, "custom_tool_call_output": true, "function_call_output": true}

// codexStep is what line is to codexViews; whole false: line is only a long line's head (a
// script's output with its pictures, a long script), read off its start.
func codexStep(line []byte, whole bool) codexStepLine {
	m := payloadType.FindSubmatch(line[:min(len(line), 1024)])
	if m != nil && !codexKinds[string(m[1])] {
		return codexStepLine{}
	}
	if !whole {
		return codexHead(line, m)
	}
	var l struct {
		Type    string `json:"type"`
		Payload struct {
			Type      string                `json:"type"`
			Name      string                `json:"name"`
			CallID    string                `json:"call_id"`
			Arguments string                `json:"arguments"`
			Item      struct{ Type string } `json:"item"`
			Output    outputHead            `json:"output"`
		} `json:"payload"`
	}
	if json.Unmarshal(line, &l) != nil {
		return codexStepLine{}
	}
	p := l.Payload
	switch {
	case l.Type == "event_msg" && p.Type == "item_completed" && p.Item.Type == "ImageView":
		return codexStepLine{kind: codexView}
	case l.Type == "event_msg" && (p.Type == "task_started" || p.Type == "task_complete" || p.Type == "turn_aborted"):
		return codexStepLine{kind: codexEnd}
	case l.Type == "response_item" && (p.Type == "custom_tool_call" || p.Type == "function_call"):
		c := codexStepLine{kind: codexCall, call: p.CallID}
		if p.Name == "wait" {
			var a struct {
				Cell json.RawMessage `json:"cell_id"`
			}
			_ = json.Unmarshal([]byte(p.Arguments), &a)
			c.cell = strings.Trim(string(a.Cell), `"`)
		}
		return c
	case l.Type == "response_item" && (p.Type == "custom_tool_call_output" || p.Type == "function_call_output"):
		return codexStepLine{kind: codexOutput, call: p.CallID, yielded: string(p.Output)}
	}
	return codexStepLine{}
}

// codexHead is what a long line is to codexViews, read off its head: its payload's type (m), its
// call's id, and an output's start. An ImageView or a turn's event is never long. One whose kind
// is not told, a call or an output it may be, is codexLost.
func codexHead(head []byte, m [][]byte) codexStepLine {
	if m == nil {
		if t := topType.FindSubmatch(head); t != nil && string(t[1]) != "response_item" && string(t[1]) != "event_msg" {
			return codexStepLine{} // compacted history, a turn's context, …
		}
		return codexStepLine{kind: codexLost}
	}
	kind := string(m[1])
	if kind != "custom_tool_call" && kind != "function_call" && kind != "custom_tool_call_output" && kind != "function_call_output" {
		return codexStepLine{}
	}
	id := callID.FindSubmatch(head[:min(len(head), 1024)])
	if id == nil {
		return codexStepLine{kind: codexLost}
	}
	if kind == "custom_tool_call" || kind == "function_call" {
		c := codexStepLine{kind: codexCall, call: string(id[1])}
		if bytes.Contains(head[:min(len(head), 1024)], []byte(`"name":"wait"`)) {
			if a := waitCell.FindSubmatch(head); a != nil {
				c.cell = string(a[1])
			}
		}
		return c
	}
	c := codexStepLine{kind: codexOutput, call: string(id[1])}
	if i := bytes.Index(head, []byte(`"output":`)); i >= 0 {
		var o outputHead
		_ = o.UnmarshalJSON(head[i+len(`"output":`):])
		c.yielded = string(o)
	}
	return c
}

// topType is a rollout line's own type, as Codex writes it first.
var topType = regexp.MustCompile(`^\{"timestamp":"[^"]*",(?:"ordinal":\d+,)?"type":"([a-z_]+)"`)

// callID is a call's id on a rollout line; waitCell the cell a wait call takes up, in its
// arguments (a string of JSON).
var (
	callID   = regexp.MustCompile(`"call_id":"([^"\\]+)"`)
	waitCell = regexp.MustCompile(`\\"cell_id\\":(?:\\")?([^"\\,}\s]+)`)
)

// outputHead is, of an output, the cell a script that goes on after it names ("Script running
// with cell ID n", its text's start); "" for any other. The output is not copied.
type outputHead string

// yieldedOutput is the start of the output of a script that goes on after it, as JSON writes it.
var yieldedOutput = regexp.MustCompile(`^(?:\[\{[^{}]*?"text":)?"Script running with cell ID ([^\s\\"]+)`)

func (o *outputHead) UnmarshalJSON(b []byte) error {
	*o = ""
	if m := yieldedOutput.FindSubmatch(b[:min(len(b), 256)]); m != nil {
		*o = outputHead(m[1])
	}
	return nil
}

// outputOf is the output on a rollout line, whole.
func outputOf(line []byte) json.RawMessage {
	var l struct {
		Payload struct {
			Output json.RawMessage `json:"output"`
		} `json:"payload"`
	}
	_ = json.Unmarshal(line, &l)
	return l.Payload.Output
}

// headLine is the most of a line codexViews reads: a longer one is told by its head.
const headLine = 64 << 10

// lineHead is br's next line, without its newline, up to headLine bytes of it, and its whole
// length.
func lineHead(br *bufio.Reader) ([]byte, int, error) {
	var head []byte
	n := 0
	for {
		chunk, err := br.ReadSlice('\n')
		if err != nil && err != bufio.ErrBufferFull {
			return nil, 0, err
		}
		n += len(chunk)
		if err == nil {
			chunk = chunk[:len(chunk)-1]
		}
		if room := headLine - len(head); room > 0 {
			head = append(head, chunk[:min(len(chunk), room)]...)
		}
		if err == nil {
			return head, n - 1, nil
		}
	}
}
