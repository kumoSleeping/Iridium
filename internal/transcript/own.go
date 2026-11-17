package transcript

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kumoSleeping/iridium/internal/api"
)

// OwnFile is the name of the record Iridium keeps itself, in the session's hold directory.
const OwnFile = "record.jsonl"

// Own is the record Iridium keeps itself of a session whose agent keeps none it can read (agy:
// a SQLite store of protobufs): one Entry a line, written by the session layer from its driver's
// agent.Record events as each piece is whole (a message, a thought, a call, its result).
var Own Format = ownBlocks

// Entry is one line of Iridium's own record.
type Entry struct {
	// Off is where in out.log the output that completed it ends: a daemon taken up after a crash
	// writes no line twice. Time is when it was written (ms).
	Off  int64  `json:"o"`
	Time int64  `json:"t"`
	Kind string `json:"kind"` // user, assistant, thinking, tool, result, title, system
	Text string `json:"text,omitempty"`
	// ID: user, the input it was (Iridium's id, what Sources knows it by); tool and result, the
	// call's. Name: a tool's.
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
	Error bool            `json:"error,omitempty"`
	Event string          `json:"event,omitempty"` // system: its api.SystemEvent
}

// Entry kinds besides api.BlockKind's: the agent's own title for the session.
const OwnTitle = "title"

func ownBlocks(off int64, raw []byte, max int, sources Sources) []api.Block {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil
	}
	var e Entry
	if trimmed[0] != '{' || json.Unmarshal(trimmed, &e) != nil {
		text, n := clip(string(raw), max)
		return []api.Block{{At: fmt.Sprintf("%d.0", off), Kind: api.BlockKindSystem, Event: api.SystemEventUnreadable, Text: text, Length: n}}
	}
	b := api.Block{At: fmt.Sprintf("%d.0", off), Time: e.Time}
	switch api.BlockKind(e.Kind) {
	case api.BlockKindUser:
		if strings.TrimSpace(e.Text) == "" {
			return nil
		}
		b.Kind, b.Text, b.ID = api.BlockKindUser, e.Text, e.ID
		if sources != nil && e.ID != "" {
			if terminal, tag, ok := sources(e.ID); ok {
				fromSource(&b, e.Text, terminal, tag)
			}
		}
		if b.FromTerminal == api.SourceIridium {
			b = api.Block{At: b.At, Time: b.Time, Kind: api.BlockKindSystem, Event: api.SystemEventIridium, Text: b.Text, From: b.From, ID: b.ID}
		}
	case api.BlockKindAssistant:
		if strings.TrimSpace(e.Text) == "" {
			return nil
		}
		b.Kind, b.Text = api.BlockKindAssistant, e.Text
	case api.BlockKindThinking:
		if strings.TrimSpace(e.Text) == "" {
			return nil
		}
		b.Kind = api.BlockKindThinking
		b.Text, b.Length = clip(e.Text, max)
	case api.BlockKindTool:
		input := "{}"
		var buf bytes.Buffer
		if len(e.Input) > 0 && json.Indent(&buf, e.Input, "", "  ") == nil {
			input = buf.String()
		}
		b.Kind, b.ID, b.Name = api.BlockKindTool, e.ID, e.Name
		b.Input, b.Length = clip(input, max)
	case api.BlockKindResult:
		b.Kind, b.ID, b.Error = api.BlockKindResult, e.ID, e.Error
		b.Text, b.Length = clip(e.Text, max)
	case api.BlockKindSystem:
		if strings.TrimSpace(e.Text) == "" {
			return nil
		}
		b.Kind, b.Event = api.BlockKindSystem, api.SystemEvent(e.Event)
		b.Text, b.Length = clip(e.Text, max)
	default:
		return nil // a title, or a kind of a later Iridium
	}
	return []api.Block{b}
}

// ownTitle is the title an entry gives (the agent's own name for the session); false for
// another line.
func ownTitle(raw []byte) (string, bool) {
	if !bytes.Contains(raw, []byte(`"kind":"`+OwnTitle+`"`)) {
		return "", false
	}
	var e Entry
	if json.Unmarshal(raw, &e) != nil || e.Kind != OwnTitle || strings.TrimSpace(e.Text) == "" {
		return "", false
	}
	return e.Text, true
}
