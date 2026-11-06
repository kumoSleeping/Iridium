package transcript

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/kumoSleeping/iridium/internal/api"
)

// How much of a file Glance reads: at most HeadBytes from the start, TailBytes from the end. A
// session list of a few gigabytes of transcripts is read in moments, never whole.
const (
	HeadBytes = 8 << 20
	TailBytes = 4 << 20
	maxGlance = 1 << 20 // longer lines are skipped
)

// Glimpse is what a session list needs of a Claude transcript.
type Glimpse struct {
	Cwd   string // where it was started: the first line's cwd
	Title string // the opening of the first thing you said
	Last  *api.LastSaid
	// Sidechain: an Agent tool's subtask, started by session Parent.
	Sidechain bool
	Parent    string
	// Ended: a sidechain whose last message is its final answer: an assistant reply that calls no
	// tool, or the tool result that ends the turn (toolEndsTurn, as a workflow agent's
	// StructuredOutput). One still at work ends on a tool call or its result.
	Ended bool
}

// Glance reads the start of the Claude transcript at path for where it was started and its
// title, and its end for what was said last, without indexing it.
func Glance(path string, sources Sources) (Glimpse, error) {
	var g Glimpse
	f, err := os.Open(path)
	if err != nil {
		return g, err
	}
	defer f.Close()
	var off int64
	first := true
	err = Lines(io.LimitReader(f, HeadBytes), maxGlance, func(raw []byte) bool {
		at := off
		off += int64(len(raw)) + 1
		if bytes.HasPrefix(raw, blobPrefix) {
			return true
		}
		var l struct {
			Cwd         string `json:"cwd"`
			IsSidechain bool   `json:"isSidechain"`
			SessionID   string `json:"sessionId"`
		}
		if json.Unmarshal(raw, &l) != nil {
			return true
		}
		if first && l.IsSidechain {
			g.Sidechain, g.Parent = true, l.SessionID
		}
		first = false
		if g.Cwd == "" {
			g.Cwd = l.Cwd
		}
		if g.Title == "" {
			for _, b := range blocksOf(at, raw, Clip, sources) {
				if b.Kind == api.BlockKindUser {
					g.Title = cut(oneLine(b.Text), TitleLen)
					break
				}
			}
		}
		return g.Cwd == "" || g.Title == ""
	})
	if err != nil {
		return g, err
	}
	if g.Sidechain {
		end, err := Tail(f, ended)
		if err != nil {
			return g, err
		}
		g.Ended = end != nil && *end
	}
	g.Last, err = Tail(f, func(off int64, raw []byte) *api.LastSaid {
		blocks := blocksOf(off, raw, Clip, sources)
		for j := len(blocks) - 1; j >= 0; j-- {
			if b := blocks[j]; b.Kind == api.BlockKindUser || b.Kind == api.BlockKindAssistant {
				return Said(b.Kind == api.BlockKindUser, b.Text, b.From, b.Time)
			}
		}
		return nil
	})
	return g, err
}

// ended reads a sidechain's line from the end: nil for one that is not a message (progress,
// attachments, API requests), else whether it ends the subagent's work.
func ended(_ int64, raw []byte) *bool {
	if bytes.HasPrefix(raw, blobPrefix) {
		return nil
	}
	var l struct {
		Type         string `json:"type"`
		ToolEndsTurn bool   `json:"toolEndsTurn"`
		Message      struct {
			StopReason string          `json:"stop_reason"`
			Content    json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(raw, &l) != nil {
		return nil
	}
	var done bool
	switch l.Type {
	case "user":
		done = l.ToolEndsTurn
	case "assistant":
		var blocks []struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(l.Message.Content, &blocks)
		done = l.Message.StopReason == "end_turn" || l.Message.StopReason == "stop_sequence"
		for _, b := range blocks {
			if b.Type == "tool_use" {
				done = false
			}
		}
	default:
		return nil
	}
	return &done
}

// Said is a LastSaid of text: on one line, Markdown's marks for headings, bold, code and links
// taken off, cut to LastLen.
func Said(user bool, text, from string, at int64) *api.LastSaid {
	text = heading.ReplaceAllString(text, "")
	text = strings.NewReplacer("**", "", "`", "").Replace(text)
	text = link.ReplaceAllString(text, "$1")
	role := "assistant"
	if user {
		role = "user"
	}
	return &api.LastSaid{Role: role, Text: cut(oneLine(text), LastLen), From: from, At: at}
}

// Lines calls f with each line of r, without its newline, until f says stop. Lines longer than
// max are skipped; a last line without a newline is given too.
func Lines(r io.Reader, max int, f func([]byte) bool) error {
	br := bufio.NewReaderSize(r, 64<<10)
	var long []byte
	skip := false
	for {
		chunk, err := br.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
			if !skip {
				long = append(long, chunk...)
				if len(long) > max {
					skip, long = true, long[:0]
				}
			}
			continue
		}
		line := chunk
		if len(long) > 0 {
			line = append(long, chunk...)
		}
		line = bytes.TrimSuffix(line, []byte("\n"))
		if !skip && len(line) > 0 && len(line) <= max {
			if !f(line) {
				return nil
			}
		}
		long, skip = long[:0], false
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// Tail reads f's lines from its end, at most TailBytes back, until pick gives something; off is
// each line's offset. Lines longer than a megabyte are skipped.
func Tail[T any](f *os.File, pick func(off int64, raw []byte) *T) (*T, error) {
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	end := st.Size()
	start := max(0, end-TailBytes)
	buf := make([]byte, end-start)
	if _, err := f.ReadAt(buf, start); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	// Lines from the last back; the first may be cut, unless it starts the file.
	for len(buf) > 0 {
		buf = bytes.TrimSuffix(buf, []byte("\n"))
		i := bytes.LastIndexByte(buf, '\n')
		if i < 0 && start > 0 {
			break
		}
		line := buf[i+1:]
		if len(line) > 0 && len(line) <= maxGlance {
			if v := pick(start+int64(i+1), line); v != nil {
				return v, nil
			}
		}
		buf = buf[:max(i, 0)]
	}
	return nil, nil
}
