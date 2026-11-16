package discover

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

// agy's sessions are its stores' names; what the list shows of each, its summary (agy 1.5.9's
// conversation_summaries, the columns read).
func TestAgy(t *testing.T) {
	home := t.TempDir()
	conv := filepath.Join(home, "conversations")
	os.MkdirAll(conv, 0o700)
	const id, sub = "fe193cc1-bf3e-4586-af14-c94e24349ee2", "a8a6b307-6d16-436e-8571-9ab42cb1603a"
	for _, n := range []string{id + ".db", sub + ".db", id + ".db-wal", "notes.txt"} {
		os.WriteFile(filepath.Join(conv, n), nil, 0o600)
	}
	db, err := sql.Open("sqlite", filepath.Join(home, "conversation_summaries.db"))
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		"CREATE TABLE `conversation_summaries` (`conversation_id` text,`title` text NOT NULL DEFAULT \"\",`preview` text NOT NULL DEFAULT \"\",`last_modified_time` datetime NOT NULL,`workspace_uris` text NOT NULL,`parent_conversation_id` text NOT NULL DEFAULT \"\")",
		`INSERT INTO conversation_summaries VALUES ('` + id + `', 'Environment  Variables', 'x', '2026-11-16 08:50:08+00:00', '["file:///tmp/a%20b/c"]', '')`,
		`INSERT INTO conversation_summaries VALUES ('` + sub + `', 'Helper', 'x', '2026-11-16 08:50:08+00:00', '[]', '` + id + `')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	x := &Index{o: Options{AgyHome: home}}
	found := x.agyIn(conv)
	if len(found) != 2 {
		t.Fatalf("found %+v", found)
	}
	g := x.glanceAgy(onDisk{id: id, path: filepath.Join(conv, id+".db")})
	if g.title != "Environment Variables" || g.cwd != "/tmp/a b/c" || g.sub {
		t.Errorf("glimpse %+v", g)
	}
	if g := x.glanceAgy(onDisk{id: sub}); !g.sub || g.parent != id || g.cwd != "" {
		t.Errorf("subagent's glimpse %+v", g)
	}
	if g := x.glanceAgy(onDisk{id: "00000000-0000-0000-0000-000000000000"}); g.title != "" {
		t.Errorf("unknown's glimpse %+v", g)
	}
}
