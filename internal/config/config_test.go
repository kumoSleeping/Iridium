package config

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"
)

func TestLoadSave(t *testing.T) {
	path := filepath.Join(t.TempDir(), "iridium", "config.json")
	c, err := Load(path)
	if err != nil || !reflect.DeepEqual(c, Default()) {
		t.Fatalf("no file: %+v %v", c, err)
	}
	os.MkdirAll(filepath.Dir(path), 0o700)
	os.WriteFile(path, []byte(`{"listen":"127.0.0.1:9000","idleReap":"0","programs":{"claude":"/opt/claude"}}`), 0o600)
	c, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != "127.0.0.1:9000" || c.IdleReap != 0 || c.StopGrace.D() != 5*time.Second || c.Programs["claude"] != "/opt/claude" || c.Programs["pi"] != "pi" {
		t.Fatalf("loaded: %+v", c)
	}
	c.IdleReap = Duration(90 * time.Minute)
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	back, err := Load(path)
	if err != nil || !reflect.DeepEqual(back, c) {
		t.Fatalf("round trip: %+v %v", back, err)
	}
	os.WriteFile(path, []byte(`{"listne":"x"}`), 0o600)
	if _, err := Load(path); err == nil {
		t.Fatal("took an unknown field")
	}
}

// A file an older version wrote still loads: the fields 0.2 drops (pairClosed, chatDir) and those
// gone already (update.manual) are taken while Config has them, skipped once it has not, and the
// rest stays strict.
func TestLoadLegacy(t *testing.T) {
	c, err := Load(filepath.Join("testdata", "v0.1.41.json"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != "127.0.0.1:7811" || !c.PairClosed || c.ChatDir != "/home/someone/chats" || c.Update.URL != "github:someone/iridium" ||
		c.Public.Origin != "https://ir-test.example.com" || c.EnterInTurn != "queue" || !c.Push || c.Lang != "zh" {
		t.Fatalf("loaded: %+v", c)
	}

	// Once a field is gone from the struct, the file's is dropped, at any depth; the rest decodes.
	type update struct {
		URL string `json:"url"`
	}
	type next struct {
		Listen string `json:"listen"`
		Update update `json:"update"`
	}
	b, _ := os.ReadFile(filepath.Join("testdata", "v0.1.41.json"))
	var all map[string]any
	if err := json.Unmarshal(b, &all); err != nil {
		t.Fatal(err)
	}
	keep := []string{"listen", "update", "pairClosed", "chatDir"}
	for k := range all {
		if !slices.Contains(keep, k) {
			delete(all, k)
		}
	}
	b, _ = json.Marshal(all)
	var n next
	dec := json.NewDecoder(bytes.NewReader(dropLegacy(b, reflect.TypeFor[next](), legacy)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&n); err != nil || n.Listen != "127.0.0.1:7811" || n.Update.URL != "github:someone/iridium" {
		t.Fatalf("without the fields: %+v %v", n, err)
	}
	// Nothing to drop: the bytes as they were. Not an object: left to the decoder.
	for _, in := range []string{`{"listen":"x"}`, `[1]`, `nul`, `{"update":"x","pairClosed":1}`} {
		got := string(dropLegacy([]byte(in), reflect.TypeFor[next](), []string{"update.manual"}))
		if got != in {
			t.Errorf("%s became %s", in, got)
		}
	}

	// Any other unknown field is still an error, legacy ones beside it or not.
	path := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(path, []byte(`{"pairClosed":true,"update":{"manual":true,"manaul":true}}`), 0o600)
	if _, err := Load(path); err == nil {
		t.Fatal("took a misspelt field beside legacy ones")
	}
	os.WriteFile(path, []byte(`{"update":{"manual":true},"chatDir":"/x"}`), 0o600)
	if c, err := Load(path); err != nil || c.ChatDir != "/x" {
		t.Fatalf("legacy only: %+v %v", c, err)
	}
}
