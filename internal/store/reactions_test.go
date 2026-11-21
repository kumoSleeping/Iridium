package store

import "testing"

// Reactions: one of each emoji a member, listed by message in the order put on; cleared one emoji
// or all.
func TestReactions(t *testing.T) {
	s, _ := open(t)
	for _, r := range []Reaction{{Channel: "c", Seq: 1, Emoji: "👍", Member: "kumo", Name: "kumo"}, {Channel: "c", Seq: 1, Emoji: "👍", Member: "agent:s1"}, {Channel: "c", Seq: 2, Emoji: "🎉", Member: "kumo"}, {Channel: "d", Seq: 1, Emoji: "👍", Member: "kumo"}} {
		if added, err := s.AddReaction(r); err != nil || !added {
			t.Fatalf("add %+v: %v %v", r, added, err)
		}
	}
	if added, err := s.AddReaction(Reaction{Channel: "c", Seq: 1, Emoji: "👍", Member: "kumo"}); err != nil || added {
		t.Errorf("the same twice: %v %v", added, err)
	}
	by, err := s.Reactions("c", []int64{1, 2, 3})
	if err != nil || len(by[1]) != 2 || by[1][0].Member != "kumo" || len(by[2]) != 1 || len(by[3]) != 0 {
		t.Fatalf("reactions: %+v %v", by, err)
	}
	if had, err := s.RemoveReaction("c", 1, "👍", "kumo"); err != nil || !had {
		t.Errorf("remove: %v %v", had, err)
	}
	if had, _ := s.RemoveReaction("c", 1, "👍", "kumo"); had {
		t.Error("removed twice")
	}
	if gone, err := s.ClearReactions("c", 2, "🎉"); err != nil || len(gone) != 1 {
		t.Errorf("clear: %+v %v", gone, err)
	}
	if by, _ := s.Reactions("c", []int64{1, 2}); len(by[1]) != 1 || len(by[2]) != 0 {
		t.Errorf("after: %+v", by)
	}
}

// A message taken back: no words, not found, its reactions and the marks on it gone, its ref
// kept (not read in again) and listed as recalled.
func TestRecallChannelMessage(t *testing.T) {
	s, _ := open(t)
	seq, _, err := s.AddChannelMessage(ChannelMessage{Channel: "c", Author: "kumo", Kind: "user", Text: "secret words", Ref: "in-1"})
	if err != nil {
		t.Fatal(err)
	}
	other, _, _ := s.AddChannelMessage(ChannelMessage{Channel: "c", Author: "kumo", Kind: "user", Text: "secret too", Ref: "in-2"})
	_, _ = s.AddReaction(Reaction{Channel: "c", Seq: seq, Emoji: "👍", Member: "bob"})
	if err := s.AddChannelAt("c", "ir_web/bob", seq, false); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.RecallChannelMessage("c", seq, "kumo"); err != nil || !ok {
		t.Fatalf("recall: %v %v", ok, err)
	}
	if ok, _ := s.RecallChannelMessage("c", seq, "kumo"); ok {
		t.Error("recalled twice")
	}
	m, ok, _ := s.ChannelMessage("c", seq)
	if !ok || !m.Gone || m.Text != "" || m.Ref != "in-1" {
		t.Errorf("the row: %+v", m)
	}
	if found, _ := s.SearchChannelMessages("c", "secret", 10); len(found) != 1 || found[0].Seq != other {
		t.Errorf("found: %+v", found)
	}
	if by, _ := s.Reactions("c", []int64{seq}); len(by[seq]) != 0 {
		t.Errorf("reactions stayed: %+v", by)
	}
	if ats, _ := s.ChannelAts("ir_web/bob"); len(ats) != 0 {
		t.Errorf("the mark stayed: %+v", ats)
	}
	refs, latest, err := s.RecalledRefs("c")
	if err != nil || !refs["in-1"] || refs["in-2"] || latest == 0 {
		t.Errorf("refs: %v %d %v", refs, latest, err)
	}
	if rs, _ := s.Recalls("c", 0); len(rs) != 1 || rs[0].Seq != seq || rs[0].By != "kumo" {
		t.Errorf("recalls: %+v", rs)
	}
	if rs, _ := s.Recalls("c", rs0At(s)); len(rs) != 0 {
		t.Errorf("recalls after: %+v", rs)
	}
	if chs, _ := s.RecalledChannels(); !chs["c"] || len(chs) != 1 {
		t.Errorf("channels: %v", chs)
	}
	// Read in again from its ref: not a new message.
	if n, added, _ := s.AddChannelMessage(ChannelMessage{Channel: "c", Author: "kumo", Kind: "user", Text: "secret words", Ref: "in-1"}); added || n != seq {
		t.Errorf("again: %d %v", n, added)
	}
}

func rs0At(s *Store) int64 {
	rs, _ := s.Recalls("c", 0)
	return rs[0].At
}
