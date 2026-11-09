package chat

import (
	"path/filepath"
	"testing"

	"github.com/kumoSleeping/iridium/internal/store"
)

// An ask of a session no chat points at goes to the Bots that follow the panel that began its turn
// (through the agents that passed it on), and to the Bot whose chat began it; to none else.
func TestFollowing(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "iridium.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	s := &Service{o: Options{Store: st, Logf: t.Logf}, online: map[string]int{}, origin: map[string]string{}}
	for _, x := range [][3]string{{"qq1", "c2c:a", "s0"}, {"qq2", "c2c:b", "s0"}, {"qq3", "c2c:c", "s0"}} {
		if err := st.Seen(x[0], x[1], x[2]); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.SetFollows("qq1", []string{"pad", "mac"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetFollows("qq2", []string{"mac"}); err != nil {
		t.Fatal(err)
	}
	bots := func(origin string) []string {
		var out []string
		for _, c := range s.following("s1", origin) {
			out = append(out, c.Plugin+"/"+c.Chat)
		}
		return out
	}
	for origin, want := range map[string][]string{
		"pad":   {"qq1/c2c:a"},
		"mac":   {"qq1/c2c:a", "qq2/c2c:b"},
		"phone": nil,           // no Bot follows it
		"local": nil,           // 本机: none may follow it
		"qq3":   {"qq3/c2c:c"}, // a Bot's own chat began the turn
	} {
		if got := bots(origin); len(got) != len(want) || (len(got) > 0 && got[len(got)-1] != want[len(want)-1]) {
			t.Errorf("%s: %v, want %v", origin, got, want)
		}
	}
	// Passed on by an agent: where that session's turn began.
	s.origin["parent"] = "pad"
	if o := s.originLocked("local", "parent"); o != "pad" {
		t.Errorf("through the parent: %q", o)
	}
	if o := s.originLocked("mac", "unknown"); o != "mac" {
		t.Errorf("an unknown parent: %q", o)
	}
	if f, _ := st.Follows(); len(f["qq1"]) != 2 || len(f["qq2"]) != 1 {
		t.Errorf("follows: %v", f)
	}
}
