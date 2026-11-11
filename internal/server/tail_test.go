package server

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
)

// The tail a session's state carries names files as a page of messages does: the reply's file
// has a ref, and the files route serves it.
func TestTailFiles(t *testing.T) {
	t.Parallel()
	l := start(t)
	note := filepath.Join(l.work, "note.txt")
	if err := os.WriteFile(note, []byte("a note\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	created := l.create(200, "web", api.NewSession{Agent: api.AgentClaude, Cwd: l.work, Text: "see\n[文件] `" + note + "`"})
	var ref string
	for deadline := time.Now().Add(10 * time.Second); ref == ""; time.Sleep(20 * time.Millisecond) {
		var st api.SessionState
		l.want(200, "web", "GET", "/v1/internal/sessions/"+created, nil, &st)
		if st.Tail != nil {
			for _, b := range st.Tail.Blocks {
				if b.Kind == api.BlockKindAssistant && len(b.Files) == 1 && b.Files[0].Name == "note.txt" {
					ref = b.Files[0].Ref
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no file in the tail: %+v", st.Tail)
		}
	}
	var body string
	if code := l.do("web", "GET", "/v1/internal/sessions/"+created+"/files/"+ref, nil, &body); code != 200 || body != "a note\n" {
		t.Fatalf("file: %d %q", code, body)
	}
}
