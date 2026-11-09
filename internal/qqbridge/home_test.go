package qqbridge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/kumoSleeping/iridium/internal/api"
)

// A terminal's name from a bot's name on QQ: what a name may not hold dropped, cut to 12, made to
// differ from the other bots' and Iridium's.
func TestFreeName(t *testing.T) {
	root := t.TempDir()
	if err := Write(BotHome(root, "1"), ConfigFile, &Config{AppID: "1", Name: "小铱"}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		id, base string
		taken    []string
		want     string
	}{
		{"2", "", nil, "QQ"},
		{"2", "", []string{"qq"}, "QQ2"},
		{"2", "Iridium 助手", nil, "Iridium 助手"},
		{"2", "小铱", nil, "小铱2"},
		{"1", "小铱", nil, "小铱"}, // its own name is no other's
		{"2", "[Bot]·名字/测试", nil, "Bot 名字 测试"},
		{"2", "🤖", nil, "QQ"},
		{"2", "Ir", nil, "QQ"}, // reserved
		{"2", "abcdefghijklmnop", nil, "abcdefghijkl"},
		{"2", "abcdefghijkl", []string{"abcdefghijkl"}, "abcdefghijk2"},
	} {
		if got := FreeName(root, c.id, c.base, c.taken...); got != c.want {
			t.Errorf("FreeName(%q, %q, %v) = %q, want %q", c.id, c.base, c.taken, got, c.want)
		}
	}
}

// The one bot of before, its files right in the root, moves into its own directory.
func TestMoveLegacy(t *testing.T) {
	root := t.TempDir()
	if MoveLegacy(root) != "" || len(Bots(root)) != 0 {
		t.Fatal("nothing to move")
	}
	if err := Write(root, ConfigFile, &Config{AppID: "1024", Secret: "s", Owners: []string{"o"}}); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(root, TokenFile), []byte("ir_x\n"), 0o600)
	Write(root, StateFile, &State{AppID: "1024"})
	if id := MoveLegacy(root); id != "1024" {
		t.Fatalf("moved: %q", id)
	}
	home := BotHome(root, "1024")
	c, ok, _ := LoadConfig(home)
	if !ok || c.Secret != "s" || c.Name != "QQ" || Token(home) != "ir_x" {
		t.Fatalf("its config: %+v %v %q", c, ok, Token(home))
	}
	if have, _ := Read(home, StateFile, &State{}); !have {
		t.Fatal("its state stayed behind")
	}
	if _, err := os.Stat(filepath.Join(root, ConfigFile)); !os.IsNotExist(err) {
		t.Fatal("the old config is still there")
	}
	// Another bot beside it; a stray directory is none.
	Write(BotHome(root, "77"), ConfigFile, &Config{AppID: "77"})
	os.MkdirAll(filepath.Join(root, BotsDir, "x-y"), 0o700)
	os.MkdirAll(filepath.Join(root, BotsDir, "88"), 0o700)
	if got := Bots(root); !slices.Equal(got, []string{"1024", "77"}) {
		t.Fatalf("bots: %v", got)
	}
}

// A rename on the page reaches the config as the plugin asks Iridium who it is.
func TestSyncName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/me" || r.Header.Get("Authorization") != "Bearer ir_t" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(api.Me{Terminal: api.Terminal{Name: "新名字"}})
	}))
	defer srv.Close()
	home := t.TempDir()
	Write(home, ConfigFile, &Config{AppID: "1", Name: "QQ", Owners: []string{"o"}})
	ir := &Iridium{URL: srv.URL, Token: func() string { return "ir_t" }}
	if n := SyncName(context.Background(), ir, home); n != "新名字" {
		t.Fatalf("name: %q", n)
	}
	if c, _, _ := LoadConfig(home); c.Name != "新名字" || len(c.Owners) != 1 {
		t.Fatalf("config: %+v", c)
	}
	// Iridium refusing it: the config's stays.
	ir.Token = func() string { return "ir_x" }
	if n := SyncName(context.Background(), ir, home); n != "新名字" {
		t.Fatalf("refused: %q", n)
	}
}
