package server

import (
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/kumoSleeping/iridium/internal/api"
)

func TestNotes(t *testing.T) {
	s, _, _ := setup(t)
	srv := httptest.NewServer(s.Handler(false))
	defer srv.Close()
	c, base := srv.Client(), srv.URL
	carried := func() api.Notes {
		var n api.Notes
		if err := json.Unmarshal(s.hub.Get("notes"), &n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := carried(); len(n.Notes) != 0 || n.Folders == nil {
		t.Fatalf("at first: %+v", n)
	}

	var a, b api.Note
	if code := call(t, c, "POST", base+"/v1/internal/notes", api.NewNote{Body: "first"}, &a); code != 200 || a.ID == "" {
		t.Fatalf("new: %d %+v", code, a)
	}
	// A new note goes above; one sent again with its own ID is left as it was.
	if code := call(t, c, "POST", base+"/v1/internal/notes", api.NewNote{ID: "abc", Body: "second"}, &b); code != 200 || b.Pos >= a.Pos {
		t.Fatalf("second: %d %+v", code, b)
	}
	call(t, c, "POST", base+"/v1/internal/notes", api.NewNote{ID: "abc", Body: "again"}, &b)
	if n := carried(); len(n.Notes) != 2 || n.Notes[0].ID != "abc" || n.Notes[0].Body != "second" {
		t.Fatalf("carried: %+v", n.Notes)
	}

	var f api.NoteFolder
	name := "Ideas"
	if code := call(t, c, "POST", base+"/v1/internal/note-folders", api.NoteFolderPatch{Name: &name}, &f); code != 200 || f.ID == 0 {
		t.Fatalf("folder: %d %+v", code, f)
	}
	body, done := "first, edited", true
	var got api.Note
	call(t, c, "PATCH", base+"/v1/internal/notes/"+a.ID, api.NotePatch{Body: &body, Folder: &f.ID, Done: &done, Session: "s1"}, &got)
	call(t, c, "PATCH", base+"/v1/internal/notes/"+a.ID, api.NotePatch{Session: "s2"}, &got)
	call(t, c, "PATCH", base+"/v1/internal/notes/"+a.ID, api.NotePatch{Session: "s1"}, &got)
	if got.Body != body || got.Folder != f.ID || got.Done == 0 || len(got.Sessions) != 2 || got.Sessions[1] != "s1" {
		t.Fatalf("patched: %+v", got)
	}
	if code := call(t, c, "PATCH", base+"/v1/internal/notes/none", api.NotePatch{Body: &body}, nil); code != 422 {
		t.Fatalf("no such note: %d", code)
	}

	// A folder deleted leaves its notes in none.
	call(t, c, "DELETE", base+"/v1/internal/note-folders/"+strconv.FormatInt(f.ID, 10), nil, nil)
	n := carried()
	if len(n.Folders) != 0 {
		t.Fatalf("folders: %+v", n.Folders)
	}
	for _, x := range n.Notes {
		if x.Folder != 0 {
			t.Fatalf("still in a folder: %+v", x)
		}
	}
	call(t, c, "DELETE", base+"/v1/internal/notes/abc", nil, nil)
	if n := carried(); len(n.Notes) != 1 {
		t.Fatalf("deleted: %+v", n.Notes)
	}
}
