// Package dshhome reads what DeepSeek Harness keeps in its home ($DSH_HOME, ~/.dsh) besides the
// transcripts themselves (docs/exec.md "远端 DSH"): its workspace registry, the one DSH Desktop's
// sidebar shows (storages/workspace.json); its sessions' projection cache, one small document per
// session with its folder and title (storages/session_projcache/sessions/<id>.json); and a
// transcript's header line, from the first bytes of its zstd file. Formats as of dsh 0.2.2;
// another reads as ErrFormat, never as a guess. Paths are the caller's: nothing here touches a
// disk, so it reads another machine's home as well as this one's.
package dshhome

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
)

// ErrFormat: the document is not one this package knows (another dsh version, or cut short).
var ErrFormat = errors.New("dshhome: unknown format")

// Where things are, below the home: path elements, joined by the caller's separator.
var (
	RegistryPath = []string{"storages", "workspace.json"}
	SummaryDir   = []string{"storages", "session_projcache", "sessions"}
	SessionsDir  = []string{"sessions"}
)

// Workspace is one of the registry's: a folder (the machine's own path), its title in DSH
// Desktop, the sessions filed under it.
type Workspace struct {
	ID        string
	Title     string
	Path      string
	Sessions  []string
	UpdatedAt int64 // ms since the epoch; 0 if not known
}

// Registry is the workspace registry: its workspaces in DSH Desktop's order, and the sessions
// archived there.
type Registry struct {
	Workspaces []Workspace
	Archived   map[string]bool
}

// ParseRegistry reads storages/workspace.json (the "workspace" unit, version 2: a single JSON
// document whose tables.workspaces holds each record as it is).
func ParseRegistry(b []byte) (Registry, error) {
	var doc struct {
		Unit struct {
			Name    string `json:"name"`
			Version int    `json:"version"`
		} `json:"unit"`
		Global struct {
			WorkspaceIDs []string `json:"workspaceIds"`
			Archived     []string `json:"archivedSessionIds"`
		} `json:"global"`
		Tables struct {
			Workspaces map[string]struct {
				Path       string   `json:"path"`
				Title      string   `json:"title"`
				SessionIDs []string `json:"sessionIds"`
				UpdatedAt  string   `json:"updatedAt"`
			} `json:"workspaces"`
		} `json:"tables"`
	}
	if err := json.Unmarshal(b, &doc); err != nil || doc.Unit.Name != "workspace" || doc.Unit.Version != 2 {
		return Registry{}, ErrFormat
	}
	r := Registry{Archived: map[string]bool{}}
	for _, id := range doc.Global.Archived {
		r.Archived[id] = true
	}
	seen := map[string]bool{}
	add := func(id string) {
		w, ok := doc.Tables.Workspaces[id]
		if !ok || seen[id] || w.Path == "" {
			return
		}
		seen[id] = true
		r.Workspaces = append(r.Workspaces, Workspace{ID: id, Title: w.Title, Path: w.Path, Sessions: w.SessionIDs, UpdatedAt: millis(w.UpdatedAt)})
	}
	for _, id := range doc.Global.WorkspaceIDs {
		add(id)
	}
	// Records the order does not name (a registration under way): after, the latest first.
	var rest []string
	for id := range doc.Tables.Workspaces {
		if !seen[id] {
			rest = append(rest, id)
		}
	}
	slices.SortFunc(rest, func(a, b string) int {
		return int(millis(doc.Tables.Workspaces[b].UpdatedAt) - millis(doc.Tables.Workspaces[a].UpdatedAt))
	})
	for _, id := range rest {
		add(id)
	}
	return r, nil
}

func millis(s string) int64 {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return 0
	}
	return t.UnixMilli()
}

// Summary is what a session's projection cache says of it.
type Summary struct {
	Cwd   string
	Title string
	Last  int64 // ms since the epoch of the last message; 0 if not known
}

// ParseSummary reads storages/session_projcache/sessions/<id>.json: a per-record document,
// {version, record: {identity, rows}}, each row {ver, seq, val}.
func ParseSummary(b []byte) (Summary, error) {
	var doc struct {
		Record *struct {
			Identity struct {
				Cwd string `json:"cwd"`
			} `json:"identity"`
			Rows struct {
				Title struct {
					Val string `json:"val"`
				} `json:"title"`
				Time struct {
					Val struct {
						LastMessage int64 `json:"lastMessageTime"`
					} `json:"val"`
				} `json:"timeContext"`
			} `json:"rows"`
		} `json:"record"`
	}
	if err := json.Unmarshal(b, &doc); err != nil || doc.Record == nil || doc.Record.Identity.Cwd == "" {
		return Summary{}, ErrFormat
	}
	r := doc.Record
	return Summary{Cwd: r.Identity.Cwd, Title: oneLine(r.Rows.Title.Val), Last: r.Rows.Time.Val.LastMessage}, nil
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// HeadBytes is how much of a transcript ParseHead wants: its first frame holds the header.
const HeadBytes = 64 << 10

// Head is a transcript's header line.
type Head struct {
	Cwd    string
	Parent string // the session that started it, for a subagent's
	Sub    bool   // a subagent's
}

// ParseHead reads the header line of a session.vN.jsonl.zstd from its first bytes (HeadBytes or
// the whole file): a run of zstd frames, the first one's first line {"type":"session", ...}.
func ParseHead(b []byte) (Head, error) {
	d, err := zstd.NewReader(bytes.NewReader(b), zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(64<<20))
	if err != nil {
		return Head{}, err
	}
	defer d.Close()
	line, err := bufio.NewReaderSize(d, 64<<10).ReadSlice('\n')
	if err != nil {
		return Head{}, ErrFormat // no whole line: cut short, or not zstd
	}
	var h struct {
		Type          string `json:"type"`
		Cwd           string `json:"cwd"`
		Origin        string `json:"origin"`
		ParentSession string `json:"parentSession"`
	}
	if json.Unmarshal(line, &h) != nil || h.Type != "session" {
		return Head{}, ErrFormat
	}
	return Head{Cwd: h.Cwd, Parent: h.ParentSession, Sub: h.Origin == "subagent"}, nil
}
