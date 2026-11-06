package store

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/access"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/gate"
)

// downTo4 makes the database at path one from before migration 5, with what is in it now.
func downTo4(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{`ALTER TABLE invites DROP COLUMN owner`, `ALTER TABLE session_settings DROP COLUMN ultracode`, `DROP TABLE devices`, `DROP TABLE session_colors`, `DROP TABLE folder_colors`, `PRAGMA user_version = 4`} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
}

func rulesOf(t *testing.T, s *Store, id string) (api.Rules, bool) {
	t.Helper()
	b, ok, err := s.TerminalRules(id)
	if err != nil {
		t.Fatal(err)
	}
	var r api.Rules
	if ok {
		must(t, json.Unmarshal(b, &r))
	}
	return r, ok
}

func reopen(t *testing.T, path string) *Store {
	t.Helper()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// Migration 5 gives Claude to the earliest panel when no panel has it, once; when a panel has it
// already, nothing changes.
func TestClaudeForFirstPanel(t *testing.T) {
	t0 := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	const first, second, bot = "pppppppppppppp01", "pppppppppppppp02", "cccccccccccccc01"
	panels := []gate.Endpoint{
		{ID: second, Name: "pad", Kind: gate.KindPanel, CreatedAt: t0.Add(time.Hour)},
		{ID: first, Name: "kumo-mac-m2", Kind: gate.KindPanel, CreatedAt: t0},
		{ID: bot, Name: "bot", Kind: gate.KindChannel, CreatedAt: t0.Add(-time.Hour)},
	}

	// None has it: the earliest panel (not the older channel) gets it.
	s, path := open(t)
	must(t, s.Gate().Apply(gate.Change{Endpoints: panels}))
	s.Close()
	downTo4(t, path)
	s = reopen(t, path)
	r, _ := rulesOf(t, s, first)
	if !access.HasClaude(r) || r.ClaudeOwner.By != access.ByFirst || !r.AllDirs || !r.SeeOthers {
		t.Fatalf("the first panel: %+v", r)
	}
	if _, ok := rulesOf(t, s, second); ok {
		t.Error("the second panel got rules")
	}
	if _, ok := rulesOf(t, s, bot); ok {
		t.Error("the channel got rules")
	}
	// Once only: turned off again, it stays off.
	b, _ := json.Marshal(access.WithoutClaude(r))
	must(t, s.SetTerminalRules(first, b))
	s.Close()
	s = reopen(t, path)
	if r, _ := rulesOf(t, s, first); access.HasClaude(r) {
		t.Error("migration 5 ran twice")
	}

	// One has it (a panel turned it on before the rule): nothing changes.
	b, _ = json.Marshal(access.WithClaude(access.Default(api.TerminalPanel), first, 1))
	must(t, s.SetTerminalRules(second, b))
	s.Close()
	downTo4(t, path)
	s = reopen(t, path)
	defer s.Close()
	if r, _ := rulesOf(t, s, first); access.HasClaude(r) {
		t.Error("the first panel got Claude though another had it")
	}
	if r, _ := rulesOf(t, s, second); !access.HasClaude(r) || r.ClaudeOwner.By != first {
		t.Errorf("the panel with Claude: %+v", r)
	}
}

// The database and its -wal and -shm are 0600 whatever the umask; an older one is brought down
// to 0600.
func TestDatabaseMode(t *testing.T) {
	old := syscall.Umask(0o022)
	defer syscall.Umask(old)
	s, path := open(t)
	files := []string{path, path + "-wal", path + "-shm"}
	check := func(s *Store) {
		t.Helper()
		must(t, s.SetTerminalRules("x", []byte("{}"))) // something in the WAL
		for _, p := range files {
			fi, err := os.Stat(p)
			if err != nil {
				t.Fatal(err)
			}
			if m := fi.Mode().Perm(); m != 0o600 {
				t.Errorf("%s: %v", filepath.Base(p), m)
			}
		}
	}
	check(s)
	for _, p := range files {
		os.Chmod(p, 0o644)
	}
	s.Close()
	os.Chmod(path, 0o644)
	s = reopen(t, path)
	defer s.Close()
	check(s)
}
