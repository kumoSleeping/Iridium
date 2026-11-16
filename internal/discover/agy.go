package discover

import (
	"database/sql"
	"encoding/json"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/transcript"

	_ "modernc.org/sqlite"
)

// agy: <home>/conversations/<id>.db, a SQLite store of protobufs Iridium does not read; what the
// list needs of each is in <home>/conversation_summaries.db (agy 1.5.9). A session of agy's own
// shows no record here until Iridium takes it up, when agy gives its history again
// (transcript.Own).
func (x *Index) agyDirs() []string {
	if x.o.AgyHome == "" {
		return nil
	}
	return []string{filepath.Join(x.o.AgyHome, "conversations")}
}

func (x *Index) agyIn(dir string) []onDisk {
	var out []onDisk
	for _, p := range filesIn(dir, ".db") {
		if id := strings.TrimSuffix(filepath.Base(p), ".db"); validID(id) {
			out = append(out, onDisk{id: id, agent: api.AgentAgy, path: p})
		}
	}
	return out
}

// glanceAgy is the summary agy keeps of the session: its title, its first folder, and its parent
// if a subagent's.
func (x *Index) glanceAgy(d onDisk) glimpse {
	var g glimpse
	db, err := sql.Open("sqlite", "file:"+filepath.Join(x.o.AgyHome, "conversation_summaries.db")+"?mode=ro&_pragma=busy_timeout(1000)")
	if err != nil {
		return g
	}
	defer db.Close()
	var title, uris, parent string
	err = db.QueryRow(`SELECT title, workspace_uris, parent_conversation_id FROM conversation_summaries WHERE conversation_id = ?`, d.id).Scan(&title, &uris, &parent)
	if err != nil {
		return g
	}
	g.title = cut(oneLine(title), transcript.TitleLen)
	var list []string
	if json.Unmarshal([]byte(uris), &list) == nil && len(list) > 0 {
		if u, err := url.Parse(list[0]); err == nil && u.Scheme == "file" {
			g.cwd = u.Path
		}
	}
	if parent != "" {
		g.sub, g.parent = true, parent
	}
	return g
}
