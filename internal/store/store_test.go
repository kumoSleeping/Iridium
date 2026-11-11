package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func open(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "iridium.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, path
}

func TestSchemaAndWAL(t *testing.T) {
	s, path := open(t)
	if v, err := s.Version(); err != nil || v != len(schema) {
		t.Fatalf("version %d %v", v, err)
	}
	var mode string
	if err := s.db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("journal mode %q %v", mode, err)
	}
	s.Close()
	// Opening again runs nothing twice.
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s2.Close()
	// A database from a newer binary is refused.
	db, _ := sql.Open("sqlite", path)
	db.Exec(`PRAGMA user_version = 99`)
	db.Close()
	if _, err := Open(path); err == nil {
		t.Fatal("opened a newer schema")
	}
}

// Before an upgrade the database is copied to <db>.v<N>.bak, whole and private; a new database,
// or one up to date, is not; a copy there already is kept, an upgrade that fails leaves one.
func TestBackupBeforeUpgrade(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "iridium.db")
	full := schema
	t.Cleanup(func() { schema = full })
	old := len(full) - 2
	schema = full[:old]
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetName("s1", "kept"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if left, _ := filepath.Glob(filepath.Join(dir, "*.bak*")); len(left) != 0 {
		t.Fatalf("a new database copied: %v", left)
	}

	schema = full
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.SetName("s1", "after")
	s.Close()
	bak := fmt.Sprintf("%s.v%d.bak", path, old)
	fi, err := os.Stat(bak)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("copy: %v %v", fi, err)
	}
	db, _ := sql.Open("sqlite", bak)
	var v int
	var name string
	db.QueryRow(`PRAGMA user_version`).Scan(&v)
	db.QueryRow(`SELECT name FROM names WHERE session = 's1'`).Scan(&name)
	db.Close()
	if v != old || name != "kept" {
		t.Fatalf("copy at version %d with %q", v, name)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "*.bak.*")); len(left) != 0 {
		t.Fatalf("left %v", left)
	}
	// Up to date: nothing more.
	s, _ = Open(path)
	s.Close()
	if all, _ := filepath.Glob(filepath.Join(dir, "*.bak")); len(all) != 1 {
		t.Fatalf("copies %v", all)
	}

	// An upgrade that fails: the copy made, the database left at its version, opened again the
	// copy kept as it is.
	schema = append(full[:len(full):len(full)], `CREATE TABLE broken (`)
	if _, err := Open(path); err == nil {
		t.Fatal("a broken migration opened")
	}
	bak = fmt.Sprintf("%s.v%d.bak", path, len(full))
	before, err := os.Stat(bak)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil {
		t.Fatal("a broken migration opened")
	}
	if after, err := os.Stat(bak); err != nil || !after.ModTime().Equal(before.ModTime()) {
		t.Fatalf("copy replaced: %v", err)
	}
	schema = full
	if s, err = Open(path); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if v, _ := s.Version(); v != len(full) {
		t.Fatalf("version %d", v)
	}
}

func TestOverlays(t *testing.T) {
	s, _ := open(t)
	ws, err := s.AddWorkspace("kumo", "/home/x/kumo")
	if err != nil {
		t.Fatal(err)
	}
	must(t, s.SetName("a", "first"))
	must(t, s.SetPinned("a", true))
	must(t, s.SetPinned("a", true))
	must(t, s.SetColor("a", "blue"))
	must(t, s.SetColor("a", "red"))
	must(t, s.Assign("b", ws, true))
	must(t, s.Assign("b", 0, false)) // discovery does not move what was dragged
	must(t, s.Assign("c", ws, false))
	o, err := s.Overlays()
	if err != nil {
		t.Fatal(err)
	}
	if o["a"] != (Overlay{Name: "first", Pinned: true, Color: "red"}) || o["b"] != (Overlay{Workspace: ws, Manual: true}) || o["c"].Workspace != ws {
		t.Fatalf("overlays: %+v", o)
	}
	must(t, s.SetName("a", ""))
	must(t, s.SetPinned("a", false))
	must(t, s.SetColor("a", ""))
	if o, _ := s.Overlays(); o["a"] != (Overlay{}) {
		t.Fatalf("a: %+v", o["a"])
	}
	reap := 10 * time.Minute
	must(t, s.SetSettings("a", Settings{IdleReap: &reap, Model: "haiku"}))
	if st, err := s.Settings("a"); err != nil || *st.IdleReap != reap || st.Model != "haiku" || st.Mode != "" {
		t.Fatalf("settings: %+v %v", st, err)
	}
	if st, err := s.Settings("none"); err != nil || st.IdleReap != nil {
		t.Fatalf("no settings: %+v %v", st, err)
	}
	// Ultracode: never set, then off (which is not never set).
	if st, _ := s.Settings("a"); st.Ultracode != nil {
		t.Fatalf("ultracode never set: %v", *st.Ultracode)
	}
	must(t, s.UpdateSettings("a", func(st *Settings) { off := false; st.Ultracode = &off }))
	if st, _ := s.Settings("a"); st.Ultracode == nil || *st.Ultracode || st.Model != "haiku" {
		t.Fatalf("ultracode off: %+v", st)
	}
}

func TestInputsUsageOutbox(t *testing.T) {
	s, _ := open(t)
	must(t, s.AddInput(Input{ID: "u1", Session: "a", Terminal: "abcdefghijklmnop", Tag: "mac", Text: "hi", State: "sent"}))
	must(t, s.AddInput(Input{ID: "u2", Session: "a", Terminal: "local", Text: "later", State: "queued"}))
	if id, tag, ok := s.Source("u1"); !ok || id != "abcdefghijklmnop" || tag != "mac" {
		t.Fatalf("source %q %q %v", id, tag, ok)
	}
	if id, tag, ok := s.Source("u2"); !ok || id != "local" || tag != "local" {
		t.Fatalf("source without a tag %q %q %v", id, tag, ok)
	}
	if _, _, ok := s.Source("nope"); ok {
		t.Fatal("source of an input never written")
	}
	q, _ := s.Queued("a")
	if len(q) != 1 || q[0].ID != "u2" {
		t.Fatalf("queued: %+v", q)
	}
	must(t, s.SetInputState("u2", "sent"))
	if q, _ := s.Queued("a"); len(q) != 0 {
		t.Fatalf("queued after sending: %+v", q)
	}

	cost := 0.25
	must(t, s.AddUsage(Usage{Terminal: "网页", Session: "a", Agent: "claude", Model: "claude-haiku-5-5", Input: 1, Output: 2, CostUSD: &cost}))
	must(t, s.AddUsage(Usage{Terminal: "网页", Session: "a", Agent: "pi", Model: "x"}))
	u, _ := s.UsageSince(0)
	if len(u) != 2 || *u[0].CostUSD != 0.25 || u[1].CostUSD != nil {
		t.Fatalf("usage: %+v", u)
	}
	must(t, s.AddUsage(Usage{Terminal: "网页", Session: "a", Agent: "claude", Model: "m", Input: 3, CacheRead: 90, CacheWrite: 6}))
	if in, cr, cw, err := s.SessionInput("a"); err != nil || in != 4 || cr != 90 || cw != 6 {
		t.Fatalf("session input: %d %d %d %v", in, cr, cw, err)
	}
	if in, cr, cw, _ := s.SessionInput("none"); in+cr+cw != 0 {
		t.Fatalf("no rows: %d %d %d", in, cr, cw)
	}

	a, _ := s.Post("bot", "qq:1", "reply", `{}`)
	b, _ := s.Post("bot", "qq:1", "status", `{}`)
	s.Post("other", "x", "reply", `{}`)
	if o, _ := s.OutboxAfter("bot", a); len(o) != 1 || o[0].Seq != b {
		t.Fatalf("outbox: %+v", o)
	}
	must(t, s.PruneOutbox(-time.Second))
	if o, _ := s.OutboxAfter("bot", 0); len(o) != 0 {
		t.Fatalf("pruned: %+v", o)
	}
}

// Writers on several connections do not fail with SQLITE_BUSY: BEGIN IMMEDIATE and the busy timeout.
func TestConcurrentWrites(t *testing.T) {
	s, _ := open(t)
	var wg sync.WaitGroup
	errs := make(chan error, 100)
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range 5 {
				errs <- s.Tx(func(tx *sql.Tx) error {
					var n int
					tx.QueryRow(`SELECT COUNT(*) FROM names`).Scan(&n)
					_, err := tx.Exec(`INSERT INTO names (session, name) VALUES (?, ?)`, string(rune('a'+i))+string(rune('a'+j)), "x")
					return err
				})
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
