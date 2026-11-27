//go:build !windows

package store

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// The database and its -wal and -shm are 0600 whatever the umask; an older one is brought down
// to 0600.
func TestDatabaseMode(t *testing.T) {
	old := syscall.Umask(0o022)
	defer syscall.Umask(old)
	s, path := open(t)
	files := []string{path, path + "-wal", path + "-shm"}
	check := func(s *Store) {
		t.Helper()
		must(t, s.SetDevice("local", "phone")) // something in the WAL
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
