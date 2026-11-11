package files

import (
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/pathsafe"
)

// A link copies the file: the reply shows it as it was, the original changed or gone; any session
// may show it; a page's blocks get it among their files.
func TestLinkCopies(t *testing.T) {
	r := newRig(t)
	src := r.file("out/chart.png", "picture one")
	l, err := r.s.Link("s1", src)
	if err != nil {
		t.Fatal(err)
	}
	id := strings.TrimPrefix(l.Link, "ir:")
	if !IsLink(id) || l.Markdown != "![chart.png](ir:"+id+")" || l.File.Kind != api.FileKindImage || l.File.Path != l.Link || l.File.Ref != id || l.File.Sha256 == "" {
		t.Fatalf("link: %+v", l)
	}
	// The same file again: the same link; changed: a new one.
	if again, _ := r.s.Link("s1", src); again.Link != l.Link {
		t.Errorf("again: %s, was %s", again.Link, l.Link)
	}
	os.WriteFile(src, []byte("picture two"), 0o600)
	os.Chtimes(src, time.Now(), time.Now().Add(time.Second))
	if next, _ := r.s.Link("s1", src); next.Link == l.Link {
		t.Error("a changed file kept its link")
	}
	os.Remove(src)
	p, err := r.s.Lookup("s2", id)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "picture one" {
		t.Errorf("copy: %q", b)
	}
	w := httptest.NewRecorder()
	r.s.ServeFile(w, httptest.NewRequest("GET", "/x?download", nil), p)
	if d := w.Header().Get("Content-Disposition"); !strings.Contains(d, "chart.png") || w.Body.String() != "picture one" {
		t.Errorf("served: %q %q", d, w.Body.String())
	}
	// Annotate: what the agent said links it; an id nobody gave is not a file.
	blocks := []api.Block{{Kind: api.BlockKindAssistant, Text: "Here:\n\n![a chart](ir:" + id + ") and [x](ir:aaaaaaaaaa)\n```\nir:" + id + "\n```"}}
	r.s.AnnotateBlocks("s2", "", blocks)
	if f := blocks[0].Files; len(f) != 1 || f[0].Ref != id || f[0].Name != "chart.png" || f[0].Path != "ir:"+id {
		t.Errorf("annotated: %+v", f)
	}
	// Records outlive a restart.
	again := New(Options{State: r.state, Guard: r.s.o.Guard, Sessions: r.fs})
	if p2, err := again.Linked(id); err != nil || p2 != p {
		t.Errorf("after a restart: %q %v", p2, err)
	}
	if _, err := again.Linked("aaaaaaaaaa"); !errors.Is(err, ErrNoRef) {
		t.Errorf("unknown: %v", err)
	}
}

// A file too large to copy is given where it is, until it changes; the guard holds.
func TestLinkLargeAndDenied(t *testing.T) {
	r := newRig(t)
	big := r.file("big.bin", "")
	if err := os.Truncate(big, linkCopyMax+1); err != nil {
		t.Fatal(err)
	}
	l, err := r.s.Link("", big)
	if err != nil {
		t.Fatal(err)
	}
	id := strings.TrimPrefix(l.Link, "ir:")
	if p, err := r.s.Linked(id); err != nil || p != big || l.Markdown != "[big.bin](ir:"+id+")" {
		t.Fatalf("large: %q %v %s", p, err, l.Markdown)
	}
	if _, err := os.Stat(filepath.Join(r.state, "links", id)); !os.IsNotExist(err) {
		t.Error("a large file was copied")
	}
	os.Truncate(big, 3)
	if _, err := r.s.Linked(id); !errors.Is(err, ErrChanged) {
		t.Errorf("changed: %v", err)
	}
	os.MkdirAll(filepath.Join(r.root, "secret"), 0o700)
	os.WriteFile(filepath.Join(r.root, "secret", "key"), []byte("k"), 0o600)
	if _, err := r.s.Link("s1", filepath.Join(r.root, "secret", "key")); !errors.Is(err, pathsafe.ErrDenied) {
		t.Errorf("denied: %v", err)
	}
	if _, err := r.s.Link("s1", filepath.Join(r.work, "gone.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing: %v", err)
	}
	if _, err := r.s.Link("s1", r.work); !errors.Is(err, ErrNotFile) {
		t.Errorf("a directory: %v", err)
	}
}

func TestLinksAndDropLinks(t *testing.T) {
	text := "A ![shot](ir:abcdefghij) then [the log](ir:bcdefghijk), ir:abcdefghij again, ir:short1 no.\n```\n[x](ir:cdefghijkl)\n```"
	if got := Links(text); !reflect.DeepEqual(got, []string{"abcdefghij", "bcdefghijk"}) {
		t.Errorf("links: %v", got)
	}
	got := DropLinks(text, map[string]bool{"abcdefghij": true, "bcdefghijk": true, "cdefghijkl": true})
	want := "A  then the log, ir:abcdefghij again, ir:short1 no.\n```\n[x](ir:cdefghijkl)\n```"
	if got != want {
		t.Errorf("drop:\n got %q\nwant %q", got, want)
	}
}
