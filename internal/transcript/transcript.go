// Package transcript reads an agent's own transcript file as message blocks (Rhodium §7.7):
// Claude's JSONL for now. Read-only. It keeps a byte-offset index of the lines that give blocks,
// read once and then only the new bytes, so a page of a 100 MB file reads only that page.
package transcript

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/kumoSleeping/iridium/internal/api"
)

const (
	Clip         = 2000 // characters of a tool's input or result, a thought or a system line a page gives
	PageMessages = 20   // things you or the agent said on a page
	PageBlocks   = 200  // blocks on a page, at most, unless one line has more
	LastLen      = 200  // characters of LastSaid
	TitleLen     = 100  // characters of Title
)

// Sources names the user messages Iridium wrote: by the uuid it sent one with, the source it
// prefixed it with ("[name] "). ok false for one it did not write.
type Sources func(uuid string) (from string, ok bool)

// File is one transcript file and its index. Safe for concurrent use.
type File struct {
	path    string
	sources Sources

	mu    sync.Mutex
	ino   uint64
	size  int64 // indexed up to here: where the first line not yet read whole starts
	lines []entry
}

// entry is a line that gives blocks.
type entry struct {
	off    int64
	n      int32 // its length, without the newline
	msgs   uint16
	blocks uint16
}

// Open is the file at path; it need not exist yet. sources may be nil.
func Open(path string, sources Sources) *File { return &File{path: path, sources: sources} }

func (f *File) Path() string { return f.path }

// Size is how far the file has been read: the offset just past its last whole line. It reads on
// first.
func (f *File) Size() (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	err := f.refresh()
	return f.size, err
}

// refresh indexes what was written since the last call. A file replaced or cut short is read anew.
func (f *File) refresh() error {
	st, err := os.Stat(f.path)
	if err != nil {
		return err
	}
	ino := uint64(0)
	if s, ok := st.Sys().(*syscall.Stat_t); ok {
		ino = s.Ino
	}
	if ino != f.ino || st.Size() < f.size {
		f.ino, f.size, f.lines = ino, 0, nil
	}
	if st.Size() == f.size {
		return nil
	}
	r, err := os.Open(f.path)
	if err != nil {
		return err
	}
	defer r.Close()
	br := bufio.NewReaderSize(io.NewSectionReader(r, f.size, st.Size()-f.size), 256<<10)
	off := f.size
	var long []byte
	for {
		chunk, err := br.ReadSlice('\n')
		if err == bufio.ErrBufferFull {
			long = append(long, chunk...)
			continue
		}
		if err != nil {
			return nil // EOF: a last line without its newline is still being written
		}
		line := chunk[:len(chunk)-1]
		if long != nil {
			line = append(long, line...)
			long = nil
		}
		if blocks := blocksOf(off, line, Clip, nil); len(blocks) > 0 {
			f.lines = append(f.lines, entry{off: off, n: int32(len(line)), msgs: uint16(messages(blocks)), blocks: uint16(min(len(blocks), 65535))})
		}
		off += int64(len(line)) + 1
		f.size = off
	}
}

func messages(blocks []api.Block) int {
	n := 0
	for _, b := range blocks {
		if b.Kind == api.BlockUser || b.Kind == api.BlockAssistant {
			n++
		}
	}
	return n
}

// Latest is the last page.
func (f *File) Latest() (api.MessagePage, error) { return f.Before(-1) }

// Before is the page that ends where at begins (-1: the end of the file).
func (f *File) Before(at int64) (api.MessagePage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.refresh(); err != nil {
		return api.MessagePage{}, err
	}
	if at < 0 || at > f.size {
		at = f.size
	}
	hi := sort.Search(len(f.lines), func(i int) bool { return f.lines[i].off >= at })
	lo := hi
	msgs, blocks := 0, 0
	for lo > 0 {
		e := f.lines[lo-1]
		if blocks > 0 && (msgs+int(e.msgs) > PageMessages || blocks+int(e.blocks) > PageBlocks) {
			break
		}
		msgs, blocks = msgs+int(e.msgs), blocks+int(e.blocks)
		lo--
	}
	page := api.MessagePage{Start: at, End: at, More: lo > 0}
	if lo < hi {
		page.Start = f.lines[lo].off
	}
	page.Blocks = f.read(f.lines[lo:hi])
	return page, nil
}

// After is the page that begins at at: what was written since a page that ended there.
func (f *File) After(at int64) (api.MessagePage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.refresh(); err != nil {
		return api.MessagePage{}, err
	}
	at = max(0, min(at, f.size))
	lo := sort.Search(len(f.lines), func(i int) bool { return f.lines[i].off >= at })
	hi := lo
	msgs, blocks := 0, 0
	for hi < len(f.lines) {
		e := f.lines[hi]
		if blocks > 0 && (msgs+int(e.msgs) > PageMessages || blocks+int(e.blocks) > PageBlocks) {
			break
		}
		msgs, blocks = msgs+int(e.msgs), blocks+int(e.blocks)
		hi++
	}
	page := api.MessagePage{Start: at, End: f.size, More: lo > 0}
	if hi < len(f.lines) {
		page.End = f.lines[hi].off
	}
	page.Blocks = f.read(f.lines[lo:hi])
	return page, nil
}

// read gives the blocks of lines, each result with its call where both are there.
func (f *File) read(lines []entry) []api.Block {
	out := []api.Block{}
	if len(lines) == 0 {
		return out
	}
	r, err := os.Open(f.path)
	if err != nil {
		return out
	}
	defer r.Close()
	for _, e := range lines {
		buf := make([]byte, e.n)
		if _, err := r.ReadAt(buf, e.off); err != nil {
			continue
		}
		out = append(out, blocksOf(e.off, buf, Clip, f.sources)...)
	}
	return paired(out)
}

// paired puts each result into its call's block, where the call is on the page.
func paired(blocks []api.Block) []api.Block {
	calls := map[string]int{}
	out := blocks[:0]
	for _, b := range blocks {
		if b.Kind == api.BlockResult {
			if i, ok := calls[b.ID]; ok && out[i].Result == nil {
				out[i].Result = &api.Result{At: b.At, Time: b.Time, Text: b.Text, Length: b.Length, Error: b.Error}
				continue
			}
		}
		if b.Kind == api.BlockTool {
			calls[b.ID] = len(out)
		}
		out = append(out, b)
	}
	return out
}

// ErrNoBlock: nothing is at that position.
var ErrNoBlock = errors.New("transcript: no block there")

// Full is the whole text of the block at at ("offset.index"): a tool call's input, a result, a
// thought, a system line, a message.
func (f *File) Full(at string) (string, error) {
	o, i, ok := strings.Cut(at, ".")
	off, err1 := strconv.ParseInt(o, 10, 64)
	idx, err2 := strconv.Atoi(i)
	if !ok || err1 != nil || err2 != nil {
		return "", ErrNoBlock
	}
	f.mu.Lock()
	if err := f.refresh(); err != nil {
		f.mu.Unlock()
		return "", err
	}
	k := sort.Search(len(f.lines), func(j int) bool { return f.lines[j].off >= off })
	if k == len(f.lines) || f.lines[k].off != off {
		f.mu.Unlock()
		return "", ErrNoBlock
	}
	e := f.lines[k]
	f.mu.Unlock()
	r, err := os.Open(f.path)
	if err != nil {
		return "", err
	}
	defer r.Close()
	buf := make([]byte, e.n)
	if _, err := r.ReadAt(buf, e.off); err != nil {
		return "", err
	}
	blocks := blocksOf(e.off, buf, -1, f.sources)
	if idx < 0 || idx >= len(blocks) {
		return "", ErrNoBlock
	}
	if blocks[idx].Kind == api.BlockTool {
		return blocks[idx].Input, nil
	}
	return blocks[idx].Text, nil
}

var (
	heading = regexp.MustCompile(`(?m)^ {0,3}#{1,6}\s+`)
	link    = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	space   = regexp.MustCompile(`\s+`)
)

// LastSaid is the last thing you or the agent said, on one line, Markdown's marks for headings,
// bold, code and links taken off; nil if nothing was said.
func (f *File) LastSaid() (*api.LastSaid, error) {
	b, err := f.walk(true, func(b api.Block) bool { return b.Kind == api.BlockUser || b.Kind == api.BlockAssistant })
	if b == nil || err != nil {
		return nil, err
	}
	text := heading.ReplaceAllString(b.Text, "")
	text = strings.NewReplacer("**", "", "`", "").Replace(text)
	text = link.ReplaceAllString(text, "$1")
	return &api.LastSaid{Role: string(b.Kind), Text: cut(oneLine(text), LastLen), From: b.From, At: b.Time}, nil
}

// Title is the opening of the first thing you said, on one line; "" if you said nothing.
func (f *File) Title() (string, error) {
	b, err := f.walk(false, func(b api.Block) bool { return b.Kind == api.BlockUser })
	if b == nil || err != nil {
		return "", err
	}
	return cut(oneLine(b.Text), TitleLen), nil
}

// walk finds the first block (or the last, back) that ok takes, among lines that give messages.
func (f *File) walk(back bool, ok func(api.Block) bool) (*api.Block, error) {
	f.mu.Lock()
	if err := f.refresh(); err != nil {
		f.mu.Unlock()
		return nil, err
	}
	lines := f.lines
	f.mu.Unlock()
	r, err := os.Open(f.path)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	for k := range lines {
		if back {
			k = len(lines) - 1 - k
		}
		e := lines[k]
		if e.msgs == 0 {
			continue
		}
		buf := make([]byte, e.n)
		if _, err := r.ReadAt(buf, e.off); err != nil {
			return nil, err
		}
		blocks := blocksOf(e.off, buf, Clip, f.sources)
		for j := range blocks {
			if back {
				j = len(blocks) - 1 - j
			}
			if ok(blocks[j]) {
				return &blocks[j], nil
			}
		}
	}
	return nil, nil
}

// Find is the transcript of Claude session id under home's projects (home: ~/.claude), in
// whatever directory it was started; "" if there is none yet. Claude writes it at the first message.
func Find(home, id string) (string, error) {
	if !validID(id) {
		return "", fmt.Errorf("transcript: not a session id: %q", id)
	}
	m, err := filepath.Glob(filepath.Join(home, "projects", "*", id+".jsonl"))
	if err != nil || len(m) == 0 {
		return "", err
	}
	return m[0], nil
}

func validID(id string) bool {
	if id == "" {
		return false
	}
	for _, c := range id {
		if !(c == '-' || c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

// ---- Claude's lines as blocks ----

type claudeLine struct {
	Type             string          `json:"type"`
	Subtype          string          `json:"subtype"`
	UUID             string          `json:"uuid"`
	Timestamp        string          `json:"timestamp"`
	IsMeta           bool            `json:"isMeta"`
	IsCompactSummary bool            `json:"isCompactSummary"`
	Message          *claudeMessage  `json:"message"`
	Attachment       *attachment     `json:"attachment"`
	Error            json.RawMessage `json:"error"`
	Content          json.RawMessage `json:"content"`
}

type claudeMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type attachment struct {
	Type       string          `json:"type"`
	Prompt     json.RawMessage `json:"prompt"`
	SourceUUID string          `json:"source_uuid"`
	IsMeta     bool            `json:"isMeta"`
	Origin     *struct {
		Kind string `json:"kind"`
	} `json:"origin"`
}

type contentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

var (
	blobPrefix  = []byte(`{"type":"api-request-blob"`)
	interrupted = regexp.MustCompile(`^\[Request interrupted by user( for tool use)?\]$`)
	tagged      = regexp.MustCompile(`^\s*<`)
	pasted      = regexp.MustCompile(`^\s*<pasted_content\b`)
)

// blocksOf is the blocks of one transcript line (Rhodium §7.7's table). Kept: what you said
// (user text; a queued_command attachment, what you sent mid-turn), what the agent said and
// thought, tool calls and results, compaction, API errors, local command output, your stopping
// a turn. Skipped: api-request-blob (the whole API request, your CLAUDE.md and email in it), meta
// lines and compaction summaries, text Claude wraps in tags (task notices, slash commands, IDE
// context; but not what you pasted), every other kind. A line that is not a JSON object is a
// system line. max: characters to cut texts to; -1 for none.
func blocksOf(off int64, raw []byte, max int, sources Sources) []api.Block {
	if bytes.HasPrefix(raw, blobPrefix) {
		return nil
	}
	var blocks []api.Block
	var l claudeLine
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil
	}
	if trimmed[0] != '{' || json.Unmarshal(raw, &l) != nil {
		text, n := clip(string(raw), max)
		return []api.Block{{At: fmt.Sprintf("%d.0", off), Kind: api.BlockSystem, Event: api.EventUnreadable, Text: text, Length: n}}
	}
	var t int64
	if ts, err := time.Parse(time.RFC3339Nano, l.Timestamp); err == nil {
		t = ts.UnixMilli()
	}
	add := func(b api.Block) {
		b.At = fmt.Sprintf("%d.%d", off, len(blocks))
		b.Time = t
		blocks = append(blocks, b)
	}
	// What you send mid-turn Claude records as an attachment once it takes it in. Yours: origin
	// human, or none on a line an older CLI wrote; a task's notice or another session's message is not.
	if a := l.Attachment; l.Type == "attachment" && a != nil {
		if a.Type != "queued_command" || a.IsMeta || l.IsMeta || (a.Origin != nil && a.Origin.Kind != "human") {
			return nil
		}
		l.Type, l.UUID, l.Message = "user", a.SourceUUID, &claudeMessage{Role: "user", Content: a.Prompt}
	}
	var items []contentBlock
	if l.Message != nil {
		_ = json.Unmarshal(l.Message.Content, &items)
	}
	switch l.Type {
	case "user":
		if l.IsMeta || l.IsCompactSummary || l.Message == nil {
			break
		}
		for _, r := range items {
			if r.Type == "tool_result" {
				text, n := clip(joined(r.Content), max)
				add(api.Block{Kind: api.BlockResult, ID: r.ToolUseID, Text: text, Length: n, Error: r.IsError})
			}
		}
		all := texts(l.Message.Content)
		if len(all) == 1 && interrupted.MatchString(all[0]) {
			add(api.Block{Kind: api.BlockSystem, Event: api.EventInterrupted})
			break
		}
		var said []string
		for _, s := range all {
			if !tagged.MatchString(s) || pasted.MatchString(s) {
				said = append(said, s)
			}
		}
		text := strings.Join(said, "\n\n")
		if strings.TrimSpace(text) == "" {
			break
		}
		b := api.Block{Kind: api.BlockUser, Text: text, ID: l.UUID}
		if sources != nil && l.UUID != "" {
			if from, ok := sources(l.UUID); ok {
				b.From = from
				b.Text = strings.TrimPrefix(text, "["+from+"] ")
			}
		}
		add(b)
	case "assistant":
		for _, i := range items {
			switch i.Type {
			case "text":
				if strings.TrimSpace(i.Text) != "" {
					add(api.Block{Kind: api.BlockAssistant, Text: i.Text})
				}
			case "thinking": // Claude's are mostly empty
				if strings.TrimSpace(i.Thinking) != "" {
					text, n := clip(i.Thinking, max)
					add(api.Block{Kind: api.BlockThinking, Text: text, Length: n})
				}
			case "tool_use":
				input := "{}"
				var buf bytes.Buffer
				if len(i.Input) > 0 && json.Indent(&buf, i.Input, "", "  ") == nil {
					input = buf.String()
				}
				text, n := clip(input, max)
				add(api.Block{Kind: api.BlockTool, ID: i.ID, Name: i.Name, Input: text, Length: n})
			}
		}
	case "system":
		switch l.Subtype {
		case "compact_boundary":
			add(api.Block{Kind: api.BlockSystem, Event: api.EventCompact})
		case "api_error":
			var e struct {
				Formatted string `json:"formatted"`
				Message   string `json:"message"`
			}
			_ = json.Unmarshal(l.Error, &e)
			text, n := clip(cmpOr(e.Formatted, e.Message), max)
			add(api.Block{Kind: api.BlockSystem, Event: api.EventAPIError, Text: text, Length: n})
		case "local_command":
			var s string
			_ = json.Unmarshal(l.Content, &s)
			text, n := clip(s, max)
			add(api.Block{Kind: api.BlockSystem, Event: api.EventLocalCommand, Text: text, Length: n})
		}
	}
	return blocks
}

// texts is a content's text blocks, or the content itself if it is a string.
func texts(content json.RawMessage) []string {
	var s string
	if json.Unmarshal(content, &s) == nil {
		return []string{s}
	}
	var items []contentBlock
	_ = json.Unmarshal(content, &items)
	var out []string
	for _, i := range items {
		if i.Type == "text" {
			out = append(out, i.Text)
		}
	}
	return out
}

func joined(content json.RawMessage) string { return strings.Join(texts(content), "\n\n") }

// clip cuts s to max characters, giving the whole length if it did; max -1 cuts nothing.
func clip(s string, max int) (string, int) {
	if max < 0 || utf8.RuneCountInString(s) <= max {
		return s, 0
	}
	n := utf8.RuneCountInString(s)
	i, k := 0, 0
	for i = range s {
		if k == max {
			break
		}
		k++
	}
	return s[:i], n
}

// cut is clip with an ellipsis, for one-line texts.
func cut(s string, max int) string {
	if c, n := clip(s, max-1); n > 0 {
		return c + "…"
	}
	return s
}

func oneLine(s string) string { return strings.TrimSpace(space.ReplaceAllString(s, " ")) }

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
