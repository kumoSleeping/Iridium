package store

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSecrets(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "iridium.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	key := filepath.Join(dir, "secrets.key")
	if _, err := os.Stat(key); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("key made before any secret: %v", err)
	}
	if err := s.PutSecret(Secret{Name: "GITHUB_TOKEN", Note: "push", By: "local"}, "ghp_abcdefgh"); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(key)
	if err != nil || fi.Mode().Perm() != 0o600 || fi.Size() != 32 {
		t.Fatalf("key file: %v %v", fi, err)
	}
	// The value is sealed: not in the database file as is.
	db, _ := os.ReadFile(filepath.Join(dir, "iridium.db"))
	wal, _ := os.ReadFile(filepath.Join(dir, "iridium.db-wal"))
	if bytes.Contains(append(db, wal...), []byte("ghp_abcdefgh")) {
		t.Fatal("the value is in the database as is")
	}
	if v, err := s.SecretValue("GITHUB_TOKEN"); err != nil || v != "ghp_abcdefgh" {
		t.Fatalf("value %q %v", v, err)
	}
	first, _ := s.Secret("GITHUB_TOKEN")
	if err := s.PutSecret(Secret{Name: "GITHUB_TOKEN", Note: "push 2", Scope: "owner", By: "p1"}, "ghp_zzzzzzzz"); err != nil {
		t.Fatal(err)
	}
	sec, err := s.Secret("GITHUB_TOKEN")
	if err != nil || sec.Note != "push 2" || sec.Scope != "owner" || sec.By != "p1" || sec.CreatedAt != first.CreatedAt {
		t.Fatalf("replaced: %+v %v", sec, err)
	}
	if err := s.SetSecretMeta(Secret{Name: "GITHUB_TOKEN", Note: "n", Scope: "workspaces", Workspaces: []int64{3}}); err != nil {
		t.Fatal(err)
	}
	if sec, _ = s.Secret("GITHUB_TOKEN"); sec.Scope != "workspaces" || len(sec.Workspaces) != 1 || sec.Workspaces[0] != 3 {
		t.Fatalf("meta: %+v", sec)
	}
	if v, _ := s.SecretValue("GITHUB_TOKEN"); v != "ghp_zzzzzzzz" {
		t.Fatalf("meta changed the value: %q", v)
	}
	if err := s.SetSecretMeta(Secret{Name: "NOPE"}); !errors.Is(err, ErrNoSecret) {
		t.Fatalf("meta of none: %v", err)
	}

	// A value moved to another row does not open.
	if _, err := s.db.Exec(`INSERT INTO secrets (name, value, by, created_at, updated_at) SELECT 'OTHER', value, by, 0, 0 FROM secrets WHERE name = 'GITHUB_TOKEN'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SecretValue("OTHER"); err == nil {
		t.Fatal("a moved value opened")
	}
	vals, err := s.SecretValues()
	if err == nil || len(vals) != 1 || vals["GITHUB_TOKEN"] != "ghp_zzzzzzzz" {
		t.Fatalf("values %v %v", vals, err)
	}
	_ = s.DeleteSecret("OTHER")

	for i := 0; i < 3; i++ {
		if err := s.AddSecretUse(SecretUse{Secret: "GITHUB_TOKEN", Session: "s1", Terminal: "local", Command: "git push"}); err != nil {
			t.Fatal(err)
		}
	}
	counts, last, err := s.SecretUseCounts()
	if err != nil || counts["GITHUB_TOKEN"] != 3 || last["GITHUB_TOKEN"].Command != "git push" {
		t.Fatalf("counts %v %v %v", counts, last, err)
	}
	if uses, _ := s.SecretUses("", 2); len(uses) != 2 {
		t.Fatalf("uses %v", uses)
	}
	if err := s.DeleteSecret("GITHUB_TOKEN"); err != nil {
		t.Fatal(err)
	}
	if uses, _ := s.SecretUses("GITHUB_TOKEN", 10); len(uses) != 0 {
		t.Fatalf("uses after delete %v", uses)
	}
	if err := s.DeleteSecret("GITHUB_TOKEN"); !errors.Is(err, ErrNoSecret) {
		t.Fatalf("delete twice: %v", err)
	}

	// Another store over the same files reads the same key.
	if err := s.PutSecret(Secret{Name: "A", By: "local"}, "value-a"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s2, err := Open(filepath.Join(dir, "iridium.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if v, err := s2.SecretValue("A"); err != nil || v != "value-a" {
		t.Fatalf("reopened %q %v", v, err)
	}
}
