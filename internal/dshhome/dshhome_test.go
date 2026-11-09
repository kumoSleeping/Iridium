package dshhome

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func TestParseRegistry(t *testing.T) {
	doc := `{
  "unit": {"name": "workspace", "version": 2},
  "global": {
    "initialized": true,
    "workspaceIds": ["w2", "w1", "gone"],
    "archivedSessionIds": ["s9"],
    "pinnedSessionIds": []
  },
  "tables": {"workspaces": {
    "w1": {"path": "C:\\Code\\CAD", "title": "CAD", "sessionIds": ["s1"], "createdAt": "2026-10-01T00:00:00.000Z", "updatedAt": "2026-11-01T00:00:00.000Z"},
    "w2": {"path": "C:\\Code\\DSH_Desktop", "title": "DSH_Desktop", "sessionIds": ["s2", "s9"], "createdAt": "2026-10-01T00:00:00.000Z", "updatedAt": "2026-11-02T00:00:00.000Z"},
    "w3": {"path": "D:\\x", "title": "x", "sessionIds": [], "createdAt": "2026-10-01T00:00:00.000Z", "updatedAt": "2026-11-03T00:00:00.000Z"}
  }}
}`
	r, err := ParseRegistry([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, w := range r.Workspaces {
		paths = append(paths, w.Path)
	}
	want := []string{`C:\Code\DSH_Desktop`, `C:\Code\CAD`, `D:\x`} // the order, then the rest
	if len(paths) != len(want) {
		t.Fatalf("paths %q", paths)
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Fatalf("paths %q, want %q", paths, want)
		}
	}
	if w := r.Workspaces[0]; w.Title != "DSH_Desktop" || len(w.Sessions) != 2 || w.UpdatedAt != 1793577600000 {
		t.Errorf("first %+v", w)
	}
	if !r.Archived["s9"] || r.Archived["s2"] {
		t.Errorf("archived %v", r.Archived)
	}
	for _, bad := range []string{`{}`, `{"unit":{"name":"workspace","version":3}}`, `not json`} {
		if _, err := ParseRegistry([]byte(bad)); !errors.Is(err, ErrFormat) {
			t.Errorf("%s: %v", bad, err)
		}
	}
}

func TestParseSummary(t *testing.T) {
	doc := `{"version": 7, "record": {
  "identity": {"formatVersion": 4, "createdAt": 1794142197234, "cwd": "/home/otae/ir", "isSeeded": false},
  "rows": {
    "title": {"ver": 1, "seq": 34, "val": "fix the\nbuild"},
    "timeContext": {"ver": 3, "seq": 34, "val": {"lastMessageTime": 1794142333807}},
    "turnBoundary": {"ver": 2, "seq": 34, "val": {"openTurnStartSeq": 30, "lastTurn": 2}}
  }}}`
	s, err := ParseSummary([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if s.Cwd != "/home/otae/ir" || s.Title != "fix the build" || s.Last != 1794142333807 || s.Ended {
		t.Errorf("%+v", s)
	}
	ended := strings.Replace(doc, `"openTurnStartSeq": 30`, `"openTurnStartSeq": null`, 1)
	if s, err := ParseSummary([]byte(ended)); err != nil || !s.Ended {
		t.Errorf("its turn over: %+v %v", s, err)
	}
	if _, err := ParseSummary([]byte(`{"version":7}`)); !errors.Is(err, ErrFormat) {
		t.Errorf("no record: %v", err)
	}
}

// frames is lines as dsh writes them: a zstd frame per batch.
func frames(t *testing.T, batches ...string) []byte {
	t.Helper()
	enc, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close()
	var out []byte
	for _, b := range batches {
		out = enc.EncodeAll([]byte(b), out)
	}
	return out
}

func TestParseHead(t *testing.T) {
	b := frames(t,
		`{"type":"session","cwd":"C:\\Code\\otc","origin":"subagent","parentSession":"p1"}`+"\n",
		`{"type":"event","seq":1}`+"\n")
	h, err := ParseHead(b)
	if err != nil {
		t.Fatal(err)
	}
	if h.Cwd != `C:\Code\otc` || !h.Sub || h.Parent != "p1" {
		t.Errorf("%+v", h)
	}
	// Only the first bytes, the second frame cut short: the header is still read.
	if h, err := ParseHead(b[:len(b)-3]); err != nil || h.Cwd != `C:\Code\otc` {
		t.Errorf("cut: %+v %v", h, err)
	}
	if _, err := ParseHead(frames(t, `{"type":"event"}`+"\n")); !errors.Is(err, ErrFormat) {
		t.Errorf("no header: %v", err)
	}
	if _, err := ParseHead(bytes.Repeat([]byte("x"), 10)); err == nil {
		t.Error("not zstd: no error")
	}
}
