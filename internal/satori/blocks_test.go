package satori

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"reflect"
	"testing"

	"github.com/kumoSleeping/iridium/internal/api"
)

var update = flag.Bool("update", false, "write testdata/blocks.json's content as encoded")

// blockTable is testdata/blocks.json: blocks and their content, the web's decoder tested on the
// same (web/test/satori.test.ts).
type blockTable struct {
	Session string `json:"session"`
	App     string `json:"app"`
	Login   string `json:"login"`
	Cases   []struct {
		Name    string          `json:"name"`
		Block   json.RawMessage `json:"block"`
		Content string          `json:"content"`
	} `json:"cases"`
}

func TestBlockContent(t *testing.T) {
	const path = "testdata/blocks.json"
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var tab blockTable
	if err := json.Unmarshal(raw, &tab); err != nil {
		t.Fatal(err)
	}
	same := func(s string) string { return s }
	for i, c := range tab.Cases {
		var b api.Block
		if err := json.Unmarshal(c.Block, &b); err != nil {
			t.Fatalf("%s: %v", c.Name, err)
		}
		// The table's blocks are as the server sends them, so the web compares like with like.
		again, _ := json.Marshal(b)
		var sent, want any
		_ = json.Unmarshal(again, &sent)
		_ = json.Unmarshal(c.Block, &want)
		if !reflect.DeepEqual(sent, want) {
			t.Errorf("%s: not as sent:\n%s\n%s", c.Name, again, c.Block)
		}
		got := blockContent(b, tab.App, tab.Login, tab.Session, same)
		if *update {
			tab.Cases[i].Content = got
		} else if got != c.Content {
			t.Errorf("%s:\n got %s\nwant %s", c.Name, got, c.Content)
		}
	}
	if *update {
		var out bytes.Buffer
		enc := json.NewEncoder(&out)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		_ = enc.Encode(tab)
		if err := os.WriteFile(path, out.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Masked: each text, never the markup.
	b := api.Block{At: "1.0", Kind: api.BlockKindTool, Name: "Bash", Input: `{"k":"SECRET"}`, Result: &api.Result{At: "2.0", Text: "SECRET"}}
	got := blockContent(b, "", "", "s", (&fake{}).Mask)
	if want := `<iridium:tool name="Bash"><iridium:input>{&quot;k&quot;:&quot;******&quot;}</iridium:input><iridium:result at="2.0">******</iridium:result></iridium:tool>`; got != want {
		t.Errorf("masked: %s", got)
	}
}
