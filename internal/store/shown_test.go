package store

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
)

func TestShown(t *testing.T) {
	s, _ := open(t)
	if _, ok, err := s.ShownRow("claude", "a"); ok || err != nil {
		t.Fatalf("a marker of nothing: %v %v", ok, err)
	}
	if c, err := s.Show("claude", "a"); !c || err != nil {
		t.Fatalf("show: %v %v", c, err)
	}
	if c, _ := s.Show("claude", "a"); c {
		t.Fatal("shown twice changed it")
	}
	must(t, s.Hide("claude", "a", 120, 7))
	r, ok, _ := s.ShownRow("claude", "a")
	if !ok || r.Shown || r.Size != 120 || r.QuietUntil != 7 {
		t.Fatalf("hidden: %+v", r)
	}
	if c, _ := s.Show("claude", "a"); !c {
		t.Fatal("shown again did not change it")
	}
	// Another agent's session of the same id is another marker.
	if v, _ := s.Visibility(); !v.Shown("claude", "a") || v.Shown("codex", "a") {
		t.Fatalf("visibility: %+v", v)
	}
	a, err := s.WatchingSince()
	b, _ := s.WatchingSince()
	if err != nil || a == 0 || a != b {
		t.Fatalf("watching since %d then %d: %v", a, b, err)
	}
}

// What the old archive flag hid stays hidden, under no agent until it is seen again.
func TestShownFromArchive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "iridium.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := db.Exec(schema[i]); err != nil {
			t.Fatal(err)
		}
	}
	db.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, 3))
	db.Exec(`INSERT INTO archive (session, at) VALUES ('old', 1)`)
	db.Close()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r, ok, _ := s.ShownRow("pi", "old")
	if !ok || r.Shown || !r.Legacy {
		t.Fatalf("old archived: %+v %v", r, ok)
	}
	must(t, s.Hide("pi", "old", 10, 0))
	v, _ := s.Visibility()
	if r, ok := v.Row("pi", "old"); !ok || r.Legacy || r.Size != 10 || len(v) != 1 {
		t.Fatalf("made exact: %+v", v)
	}
}
