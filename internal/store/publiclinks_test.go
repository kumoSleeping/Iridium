package store

import (
	"path/filepath"
	"testing"
)

// One link per session: a new one replaces the old, found by its hash only while it is there.
func TestPublicLinks(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, ok, err := s.PublicLinkOf("s1"); ok || err != nil {
		t.Fatal(ok, err)
	}
	if err := s.SetPublicLink(PublicLink{Session: "s1", Hash: "h1", Creator: "local", CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPublicLink(PublicLink{Session: "s1", Hash: "h2", Creator: "pad", CreatedAt: 2}); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.PublicLinkByHash("h1"); ok {
		t.Error("the old link still found")
	}
	if l, ok, err := s.PublicLinkByHash("h2"); !ok || err != nil || l.Session != "s1" || l.Creator != "pad" || l.CreatedAt != 2 {
		t.Errorf("by hash: %+v %v %v", l, ok, err)
	}
	if ok, err := s.DeletePublicLink("s1"); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if _, ok, _ := s.PublicLinkByHash("h2"); ok {
		t.Error("revoked, still found")
	}
	if ok, _ := s.DeletePublicLink("s1"); ok {
		t.Error("deleted twice")
	}
}
