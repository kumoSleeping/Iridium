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
		`{"o":80,"t":1007,"kind":"later-kind","text":"?"}`,
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
	if got := strings.Join(kinds, " "); got != "user thinking tool assistant system" {
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
	if title, _ := f.Title(); title != "Fixing It" {
		t.Errorf("title %q", title)
	}
	if last, _ := f.LastSaid(); last == nil || last.Text != "Done." {
		t.Errorf("last %+v", last)
	}
}
