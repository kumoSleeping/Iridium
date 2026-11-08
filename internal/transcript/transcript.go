// Package transcript reads an agent's own transcript file as message blocks (Rhodium §7.7):
// Claude's, Codex's, pi's and dsh's JSONL (dsh's zstd-compressed), each by its Format. Read-only. It keeps a byte-offset index of the lines that give blocks,
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
	"unicode"
	"unicode/utf16"
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

// Sources names the user messages Iridium wrote: by the uuid it sent one with, the interactive
// terminal that sent it and the tag it was prefixed with ("[tag] "). ok false for one it did not
// write.
type Sources func(uuid string) (terminal, tag string, ok bool)

// Format gives the blocks of one line of an agent's transcript: max is the characters to cut
// texts to (-1: none); sources names the user messages Iridium wrote (may be nil).
type Format func(off int64, raw []byte, max int, sources Sources) []api.Block

// Claude is Claude Code's format; Codex (codex.go), Pi (pi.go) and Dsh (dsh.go) the others.
var Claude Format = blocksOf

// File is one transcript file and its index. Safe for concurrent use.
type File struct {
	path    string
	sources Sources
	format  Format

	mu    sync.Mutex
	ino   uint64
	size  int64 // indexed up to here: where the first line not yet read whole starts
	lines []entry
	// zst: a zstd file's frames (offsets are then in its decoded text); nil for a plain one.
	zst *frames
	// dsh: a dsh session file, whose title is its last session/title event, kept in title.
	dsh   bool
	title string
	// named: the title the agent's last title line gave (named.go), "" while none has.
	named string
}

// entry is a line that gives blocks.
type entry struct {
	off    int64
	n      int32 // its length, without the newline
	msgs   uint16
	blocks uint16
}

// Open is Claude's transcript at path; it need not exist yet. sources may be nil.
func Open(path string, sources Sources) *File { return OpenAs(path, Claude, sources) }

// OpenAs is the transcript at path, in format f. A path ending .zstd or .zst is read as zstd.
func OpenAs(path string, f Format, sources Sources) *File {
	file := &File{path: path, sources: sources, format: f, dsh: dshName.MatchString(filepath.Base(path))}
	if strings.HasSuffix(path, ".zstd") || strings.HasSuffix(path, ".zst") {
		file.zst = &frames{}
	}
	return file
}

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
	end := f.size // in the file
	if f.zst != nil {
		end = f.zst.end
	}
	if ino != f.ino || st.Size() < end {
		f.ino, f.size, f.lines, f.title, f.named, end = ino, 0, nil, "", "", 0
		if f.zst != nil {
			f.zst = &frames{}
		}
	}
	if st.Size() == end {
		return nil
	}
	r, err := os.Open(f.path)
	if err != nil {
		return err
	}
	defer r.Close()
	var src io.Reader = io.NewSectionReader(r, f.size, st.Size()-f.size)
	if f.zst != nil {
		if src, err = f.zst.from(r, st.Size(), f.size); err != nil {
			return err
		}
	}
	br := bufio.NewReaderSize(src, 256<<10)
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
		if f.dsh {
			if t, ok := dshTitle(line); ok {
				f.title = t
			}
		}
		if blocks := f.format(off, line, Clip, nil); len(blocks) > 0 {
			for _, b := range blocks {
				if b.Kind == api.BlockKindAssistant {
					if t := NameLine(b.Text); t != "" {
						f.named = t
					}
				}
			}
			f.lines = append(f.lines, entry{off: off, n: int32(len(line)), msgs: uint16(messages(blocks)), blocks: uint16(min(len(blocks), 65535))})
		}
		off += int64(len(line)) + 1
		f.size = off
	}
}

func messages(blocks []api.Block) int {
	n := 0
	for _, b := range blocks {
		if b.Kind == api.BlockKindUser || b.Kind == api.BlockKindAssistant {
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
	r, done, err := f.text()
	if err != nil {
		return out
	}
	defer done()
	for _, e := range lines {
		buf := make([]byte, e.n)
		if _, err := r.ReadAt(buf, e.off); err != nil {
			continue
		}
		out = append(out, f.format(e.off, buf, Clip, f.sources)...)
	}
	return paired(out)
}

// text opens what the lines are read from at their offsets: the file, or a zstd file's decoded
// text. f.mu is held.
func (f *File) text() (io.ReaderAt, func() error, error) {
	r, err := os.Open(f.path)
	if err != nil {
		return nil, nil, err
	}
	if f.zst == nil {
		return r, r.Close, nil
	}
	return &zReader{r: r, list: f.zst.list}, r.Close, nil
}

// paired puts each result into its call's block, where the call is on the page.
func paired(blocks []api.Block) []api.Block {
	calls := map[string]int{}
	out := blocks[:0]
	for _, b := range blocks {
		if b.Kind == api.BlockKindResult {
			if i, ok := calls[b.ID]; ok && out[i].Result == nil {
				out[i].Result = &api.Result{At: b.At, Time: b.Time, Text: b.Text, Length: b.Length, Error: b.Error}
				continue
			}
		}
		if b.Kind == api.BlockKindTool {
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
	r, done, err := f.text()
	f.mu.Unlock()
	if err != nil {
		return "", err
	}
	defer done()
	buf := make([]byte, e.n)
	if _, err := r.ReadAt(buf, e.off); err != nil {
		return "", err
	}
	blocks := f.format(e.off, buf, -1, f.sources)
	if idx < 0 || idx >= len(blocks) {
		return "", ErrNoBlock
	}
	if blocks[idx].Kind == api.BlockKindTool {
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
	b, err := f.walk(true, func(b api.Block) bool { return b.Kind == api.BlockKindUser || b.Kind == api.BlockKindAssistant })
	if b == nil || err != nil {
		return nil, err
	}
	text := heading.ReplaceAllString(WithoutNameLines(b.Text), "")
	text = strings.NewReplacer("**", "", "`", "").Replace(text)
	text = link.ReplaceAllString(text, "$1")
	return &api.LastSaid{Role: string(b.Kind), Text: cut(oneLine(text), LastLen), From: b.From, At: b.Time}, nil
}

// Title is the title the agent's last title line gave (named.go); else, a dsh session's, the
// title dsh gave it, where it did; else the opening of the first thing you said, on one line; ""
// if you said nothing.
func (f *File) Title() (string, error) {
	f.mu.Lock()
	err := f.refresh()
	named, t := f.named, f.title
	f.mu.Unlock()
	if named != "" {
		return named, err
	}
	if f.dsh && (err != nil || t != "") {
		return cut(oneLine(t), TitleLen), err
	}
	b, err := f.walk(false, func(b api.Block) bool { return b.Kind == api.BlockKindUser })
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
	r, done, err := f.text()
	f.mu.Unlock()
	if err != nil {
		return nil, err
	}
	defer done()
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
		blocks := f.format(e.off, buf, Clip, f.sources)
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

// ClaudeProjectDir is the directory under projects/ Claude keeps a session started in cwd in: each
// UTF-16 unit but an ASCII letter or digit made '-' (C:\a\b: C--a-b). Past 200 Claude cuts it
// and adds a hash of its own; ok is false then.
func ClaudeProjectDir(cwd string) (dir string, ok bool) {
	var b strings.Builder
	for _, c := range utf16.Encode([]rune(cwd)) {
		if c < 0x80 && (c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9') {
			b.WriteByte(byte(c))
		} else {
			b.WriteByte('-')
		}
	}
	return b.String(), b.Len() <= 200
}

// ValidID reports whether id may be a Claude session's (a UUID's characters).
func ValidID(id string) bool { return validID(id) }

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
	Type             string `json:"type"`
	Subtype          string `json:"subtype"`
	UUID             string `json:"uuid"`
	Timestamp        string `json:"timestamp"`
	IsMeta           bool   `json:"isMeta"`
	IsCompactSummary bool   `json:"isCompactSummary"`
	// MergedSendUuids: the sends after the first that Claude took in as this one line (queued
	// while a turn ran, all let in at its end), each a text block of its own after the first's.
	MergedSendUuids []string       `json:"mergedSendUuids"`
	Message         *claudeMessage `json:"message"`
	Attachment      *attachment    `json:"attachment"`
	// Origin: who wrote a user line, when not you (a task-notification, as one comes while idle).
	Origin *struct {
		Kind string `json:"kind"`
	} `json:"origin"`
	Error   json.RawMessage `json:"error"`
	Content json.RawMessage `json:"content"`
	// CompactMetadata: a compact_boundary's figures.
	CompactMetadata *struct {
		Trigger    string `json:"trigger"`
		PreTokens  int    `json:"preTokens"`
		PostTokens int    `json:"postTokens"`
		DurationMs int64  `json:"durationMs"`
	} `json:"compactMetadata"`
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
	commandLine = regexp.MustCompile(`^\s*<command-(?:name|message|args)>`)
	commandName = regexp.MustCompile(`<command-name>(/[^<\s]+)</command-name>`)
	commandArgs = regexp.MustCompile(`(?s)<command-args>(.*?)</command-args>`)
	commandOut  = regexp.MustCompile(`(?s)<local-command-std(?:out|err)>(.*?)</local-command-std(?:out|err)>`)
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
		return []api.Block{{At: fmt.Sprintf("%d.0", off), Kind: api.BlockKindSystem, Event: api.SystemEventUnreadable, Text: text, Length: n}}
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
		// A background task's end (a workflow, a background agent): a quiet line, not its XML.
		if a.Type == "queued_command" && a.Origin != nil && a.Origin.Kind == "task-notification" {
			if text := taskLine(texts(a.Prompt)); text != "" {
				add(api.Block{Kind: api.BlockKindSystem, Event: api.SystemEventTask, Text: text})
			}
			return blocks
		}
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
		// A background task's end that came while the session was idle: a user line of its own,
		// marked by its origin (mid-turn it is an attachment, above). The same quiet line.
		if l.Origin != nil && l.Origin.Kind == "task-notification" {
			if text := taskLine(texts(l.Message.Content)); text != "" {
				add(api.Block{Kind: api.BlockKindSystem, Event: api.SystemEventTask, Text: text})
			}
			break
		}
		for _, r := range items {
			if r.Type == "tool_result" {
				text, n := clip(joined(r.Content), max)
				add(api.Block{Kind: api.BlockKindResult, ID: r.ToolUseID, Text: text, Length: n, Error: r.IsError})
			}
		}
		all := texts(l.Message.Content)
		if len(all) == 1 && interrupted.MatchString(all[0]) {
			add(api.Block{Kind: api.BlockKindSystem, Event: api.SystemEventInterrupted})
			break
		}
		// A slash command as Claude records it, a line of its own: what was typed, under the
		// input's uuid.
		if len(all) == 1 && commandLine.MatchString(all[0]) {
			if c := commandName.FindStringSubmatch(all[0]); c != nil {
				line := c[1]
				if a := commandArgs.FindStringSubmatch(all[0]); a != nil && strings.TrimSpace(a[1]) != "" {
					line += " " + strings.TrimSpace(a[1])
				}
				all = []string{line}
				// /compact: the compaction's own line, just before, says it (the user, 2026-11-06).
				if c[1] == "/compact" {
					break
				}
				// /model, /effort not sent by Iridium: Claude's own record of a set_model (the
				// page's picker), no one's words. Without sources (the index's pass) it is kept.
				if (c[1] == "/model" || c[1] == "/effort") && sources != nil {
					if _, _, ok := sources(l.UUID); !ok {
						break
					}
				}
			}
		}
		said := func(all []string) string {
			var said []string
			for _, s := range all {
				if !tagged.MatchString(s) || pasted.MatchString(s) {
					said = append(said, s)
				}
			}
			return strings.Join(said, "\n\n")
		}
		say := func(text, uuid string) {
			if strings.TrimSpace(text) == "" {
				return
			}
			b := api.Block{Kind: api.BlockKindUser, Text: text, ID: uuid}
			if sources != nil && uuid != "" {
				if terminal, tag, ok := sources(uuid); ok {
					b.From, b.FromTerminal = tag, terminal
					b.Text = strings.TrimPrefix(text, "["+tag+"] ")
				}
			}
			if b.From == api.SourceIridium {
				// Iridium's own line (a recovery prompt), not your words.
				b = api.Block{Kind: api.BlockKindSystem, Event: api.SystemEventIridium, Text: b.Text, From: b.From, ID: b.ID}
			}
			add(b)
		}
		// Sends Claude merged into one line: each its own message again, under its own uuid (so
		// its own prefix goes, and the web finds it sent). Iridium sends one text block a message.
		if m := l.MergedSendUuids; len(m) > 0 && len(all) == len(m)+1 {
			for i, s := range all {
				uuid := l.UUID
				if i > 0 {
					uuid = m[i-1]
				}
				say(strings.TrimRightFunc(said([]string{s}), unicode.IsSpace), uuid)
			}
			break
		}
		say(said(all), l.UUID)
	case "assistant":
		for _, i := range items {
			switch i.Type {
			case "text":
				if strings.TrimSpace(i.Text) != "" {
					add(api.Block{Kind: api.BlockKindAssistant, Text: i.Text})
				}
			case "thinking": // Claude's are mostly empty
				if strings.TrimSpace(i.Thinking) != "" {
					text, n := clip(i.Thinking, max)
					add(api.Block{Kind: api.BlockKindThinking, Text: text, Length: n})
				}
			case "tool_use":
				input := "{}"
				var buf bytes.Buffer
				if len(i.Input) > 0 && json.Indent(&buf, i.Input, "", "  ") == nil {
					input = buf.String()
				}
				text, n := clip(input, max)
				add(api.Block{Kind: api.BlockKindTool, ID: i.ID, Name: i.Name, Input: text, Length: n})
			}
		}
	case "system":
		switch l.Subtype {
		case "compact_boundary":
			b := api.Block{Kind: api.BlockKindSystem, Event: api.SystemEventCompact}
			if m := l.CompactMetadata; m != nil {
				b.Compact = &api.Compaction{Auto: m.Trigger == "auto", Before: m.PreTokens, After: m.PostTokens, Ms: m.DurationMs}
			}
			add(b)
		case "api_error":
			var e struct {
				Formatted string `json:"formatted"`
				Message   string `json:"message"`
			}
			_ = json.Unmarshal(l.Error, &e)
			text, n := clip(cmpOr(e.Formatted, e.Message), max)
			add(api.Block{Kind: api.BlockKindSystem, Event: api.SystemEventAPIError, Text: text, Length: n})
		case "local_command":
			var s string
			_ = json.Unmarshal(l.Content, &s)
			// Its output in tags: the words only; none (/clear's), no line.
			if m := commandOut.FindAllStringSubmatch(s, -1); m != nil {
				var outs []string
				for _, o := range m {
					if o := strings.TrimSpace(o[1]); o != "" {
						outs = append(outs, o)
					}
				}
				s = strings.Join(outs, "\n\n")
			}
			if strings.TrimSpace(s) == "" {
				break
			}
			text, n := clip(s, max)
			add(api.Block{Kind: api.BlockKindSystem, Event: api.SystemEventLocalCommand, Text: text, Length: n})
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

var (
	taskTag   = regexp.MustCompile(`(?s)<(summary|status|agent_count|duration_ms)>(.*?)</`)
	workflowW = regexp.MustCompile(`^Dynamic workflow\b`)
)

// taskLine is a task-notification as one line: its summary ("Dynamic workflow" said Workflow),
// then how many agents it ran and for how long; "" when it says nothing.
func taskLine(parts []string) string {
	tags := map[string]string{}
	for _, m := range taskTag.FindAllStringSubmatch(strings.Join(parts, "\n"), -1) {
		if _, ok := tags[m[1]]; !ok {
			tags[m[1]] = strings.TrimSpace(m[2])
		}
	}
	line := workflowW.ReplaceAllString(oneLine(tags["summary"]), "Workflow")
	if line == "" {
		if tags["status"] == "" {
			return ""
		}
		line = "Task " + tags["status"]
	}
	if n, err := strconv.Atoi(tags["agent_count"]); err == nil && n > 0 {
		line += fmt.Sprintf(" · %d agent", n)
		if n > 1 {
			line += "s"
		}
	}
	if ms, err := strconv.ParseInt(tags["duration_ms"], 10, 64); err == nil && ms > 0 {
		line += " · " + took(time.Duration(ms)*time.Millisecond)
	}
	return line
}

// took is a span as people say it: 45s, 3m, 1h 12m.
func took(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()+0.5))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	}
}
