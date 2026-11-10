package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/access"
	"github.com/kumoSleeping/iridium/internal/api"
)

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
	// at4 makes a database from before migration 5 with these terminals, and rules (by ID).
	at4 := func(rules map[string]string) string {
		path := filepath.Join(t.TempDir(), "iridium.db")
		db := openAt(t, path, 4)
		for _, e := range []struct {
			id, name, kind string
			at             time.Time
		}{{second, "pad", "panel", t0.Add(time.Hour)}, {first, "kumo-mac-m2", "panel", t0}, {bot, "bot", "channel", t0.Add(-time.Hour)}} {
			if _, err := db.Exec(`INSERT INTO terminals (id, name, kind, created_at) VALUES (?, ?, ?, ?)`, e.id, e.name, e.kind, e.at.UnixNano()); err != nil {
				t.Fatal(err)
			}
		}
		for id, r := range rules {
			if _, err := db.Exec(`INSERT INTO permissions (terminal, rules) VALUES (?, ?)`, id, r); err != nil {
				t.Fatal(err)
			}
		}
		db.Close()
		return path
	}

	// None has it: the earliest panel (not the older channel) gets it.
	path := at4(nil)
	s := reopen(t, path)
	r, _ := rulesOf(t, s, first)
	if !access.HasClaude(r) || r.ClaudeOwner.By != access.ByFirst {
		t.Fatalf("the first panel: %+v", r)
	}
	if _, ok := rulesOf(t, s, second); ok {
		t.Error("the second panel got rules")
	}
	if _, ok := rulesOf(t, s, bot); ok {
		t.Error("the channel got rules")
	}
	// Once only: without it again (an older table), it stays without.
	must(t, s.SetTerminalRules(first, []byte(`{}`)))
	s.Close()
	s = reopen(t, path)
	if r, _ := rulesOf(t, s, first); access.HasClaude(r) {
		t.Error("migration 5 ran twice")
	}
	s.Close()

	// One has it (a panel turned it on before the rule): nothing changes.
	b, _ := json.Marshal(access.WithClaude(access.Default(), first, 1))
	s = reopen(t, at4(map[string]string{second: string(b)}))
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
