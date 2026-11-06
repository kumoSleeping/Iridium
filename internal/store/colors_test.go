package store

import "testing"

// A colour no longer offered (gray) is taken off sessions and folders; the others stay.
func TestKeepColors(t *testing.T) {
	s, _ := open(t)
	for _, err := range []error{
		s.SetColor("a", "gray"), s.SetColor("b", "red"),
		s.SetFolderColor("/x", "gray"), s.SetFolderColor("/y", "blue"),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := s.KeepColors([]string{"red", "blue"}); err != nil {
		t.Fatal(err)
	}
	o, err := s.Overlays()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := o["a"]; ok || o["b"].Color != "red" {
		t.Fatalf("sessions %+v", o)
	}
	f, err := s.FolderColors()
	if err != nil || len(f) != 1 || f["/y"] != "blue" {
		t.Fatalf("folders %v %v", f, err)
	}
}
