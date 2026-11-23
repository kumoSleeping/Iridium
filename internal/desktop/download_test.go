package desktop

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFetch(t *testing.T) {
	body := strings.Repeat("x", 600<<10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie("ir"); err != nil || c.Value != "s" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Length", "614400")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	dir := t.TempDir()

	path := filepath.Join(dir, "a.bin")
	var last, whole int64
	err := fetch(context.Background(), srv.URL, path, []*http.Cookie{{Name: "ir", Value: "s"}}, func(got, total int64) { last, whole = got, total })
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != body {
		t.Fatalf("saved %d bytes", len(b))
	}
	if last != int64(len(body)) || whole != int64(len(body)) {
		t.Fatalf("progress ended at %d of %d", last, whole)
	}
	if _, err := os.Stat(path + ".part"); !os.IsNotExist(err) {
		t.Fatal("part file left")
	}

	// No login: an error, nothing saved.
	other := filepath.Join(dir, "b.bin")
	if err := fetch(context.Background(), srv.URL, other, nil, func(int64, int64) {}); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(other); !os.IsNotExist(err) {
		t.Fatal("a file saved without the login")
	}
}
