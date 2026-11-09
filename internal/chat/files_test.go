package chat

import (
	"os"
	"path/filepath"
	"testing"
)

// The agents' own directories are never given out, dsh's among them.
func TestDeniedAgentHomes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, d := range []string{".claude", ".codex", ".pi", ".dsh", "work"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(home, d, "f"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, d := range []string{".claude", ".codex", ".pi", ".dsh"} {
		if !denied(filepath.Join(home, d, "f")) {
			t.Errorf("~/%s/f not denied", d)
		}
	}
	if denied(filepath.Join(home, "work", "f")) {
		t.Error("~/work/f denied")
	}
}

// A reply's file links go as files, in the order written; a picture's link leaves the words, a
// file's leaves its text; an id not given stays as written.
func TestReplyLinks(t *testing.T) {
	dir := t.TempDir()
	png, pdf := filepath.Join(dir, "chart.png"), filepath.Join(dir, "report.pdf")
	os.WriteFile(png, []byte("p"), 0o600)
	os.WriteFile(pdf, []byte("pdf"), 0o600)
	given := map[string]string{"abcdefghij": png, "bcdefghijk": pdf}
	s := &Service{key: []byte("k"), o: Options{Linked: func(id string) (string, error) {
		if p, ok := given[id]; ok {
			return p, nil
		}
		return "", os.ErrNotExist
	}}}
	r := s.reply("qq", "s1", "See:\n\n![a chart](ir:abcdefghij)\n\nand [the report](ir:bcdefghijk), [gone](ir:cdefghijkl).")
	if r.Markdown != "See:\n\nand the report, [gone](ir:cdefghijkl)." {
		t.Errorf("markdown: %q", r.Markdown)
	}
	if len(r.Files) != 2 || r.Files[0].Name != "chart.png" || r.Files[1].Name != "report.pdf" || r.Files[1].Size != 3 {
		t.Fatalf("files: %+v", r.Files)
	}
	if p, err := s.File(Caller{ID: "qq"}, r.Files[0].Ref); err != nil || p != png {
		t.Errorf("file: %q %v", p, err)
	}
	if _, err := s.File(Caller{ID: "other"}, r.Files[0].Ref); err == nil {
		t.Error("another plugin took the file")
	}
}
