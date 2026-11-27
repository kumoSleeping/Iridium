package transcript

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kumoSleeping/iridium/internal/api"
)

// Iridium's own record (agy's): blocks by entry, your words by the input they were, the agent's
// title for the session.
func TestOwn(t *testing.T) {
	path := filepath.Join(t.TempDir(), OwnFile)
	lines := []string{
		`{"o":10,"t":1000,"kind":"user","text":"[web] fix it","id":"in-1"}`,
		`{"o":20,"t":1001,"kind":"title","text":"Fixing It"}`,
		`{"o":30,"t":1002,"kind":"thinking","text":"look first"}`,
		`{"o":40,"t":1003,"kind":"tool","id":"c1","name":"bash","input":{"command":"ls"}}`,
		`{"o":50,"t":1004,"kind":"result","id":"c1","text":"a.txt\n"}`,
		`{"o":60,"t":1005,"kind":"assistant","text":"Done."}`,
		`{"o":70,"t":1006,"kind":"system","event":"interrupted","text":"Interrupted"}`,
		`{"o":75,"t":1007,"kind":"system","event":"compact","compact":{"before":332394,"ms":15000}}`,
		`{"o":80,"t":1008,"kind":"later-kind","text":"?"}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sources := func(uuid string) (string, string, bool) { return "ir_web", "web", uuid == "in-1" }
	f := OpenAs(path, Own, sources)
	page, err := f.Latest()
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, b := range page.Blocks {
		kinds = append(kinds, string(b.Kind))
	}
	// The tool's result goes with its call on the same page.
	if got := strings.Join(kinds, " "); got != "user thinking tool assistant system system" {
		t.Fatalf("blocks %s: %+v", got, page.Blocks)
	}
	u := page.Blocks[0]
	if u.Text != "fix it" || u.From != "web" || u.Time != 1000 {
		t.Errorf("user %+v", u)
	}
	if tool := page.Blocks[2]; tool.Name != "bash" || !strings.Contains(tool.Input, `"command": "ls"`) || tool.Result == nil || tool.Result.Text != "a.txt\n" {
		t.Errorf("tool %+v", tool)
	}
	if s := page.Blocks[4]; s.Event != api.SystemEventInterrupted {
		t.Errorf("system %+v", s)
	}
	if c := page.Blocks[5]; c.Event != api.SystemEventCompact || c.Compact == nil || c.Compact.Before != 332394 || c.Compact.Ms != 15000 {
		t.Errorf("compaction %+v", c) // no words of its own
	}
	if title, _ := f.Title(); title != "Fixing It" {
		t.Errorf("title %q", title)
	}
	if last, _ := f.LastSaid(); last == nil || last.Text != "Done." {
		t.Errorf("last %+v", last)
	}
}

// A result kept with the picture it gave the model (agy's read of one): on the page with its
// size, read again whole by Image.
func TestOwnPicture(t *testing.T) {
	path := filepath.Join(t.TempDir(), OwnFile)
	// A 1×1 PNG.
	png := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII="
	lines := []string{
		`{"o":10,"t":1000,"kind":"tool","id":"c1","name":"read","input":{"file_path":"/w/a.png"}}`,
		`{"o":20,"t":1001,"kind":"result","id":"c1","content":[{"type":"image","data":"` + png + `","mimeType":"image/png"}]}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := OpenAs(path, Own, nil)
	page, err := f.Latest()
	if err != nil || len(page.Blocks) != 1 || page.Blocks[0].Result == nil {
		t.Fatalf("%v %+v", err, page.Blocks)
	}
	r := page.Blocks[0].Result
	if len(r.Images) != 1 || r.Images[0].Mime != "image/png" || r.Images[0].W != 1 || r.Images[0].Data != "" {
		t.Fatalf("its picture on the page: %+v", r.Images)
	}
	img, data, err := f.Image(r.At, 0)
	if err != nil || img.Mime != "image/png" || len(data) == 0 {
		t.Errorf("read again: %+v %d %v", img, len(data), err)
	}
}
