package desktop

import (
	"encoding/json"
	"slices"
	"testing"
)

func TestFavMerge(t *testing.T) {
	order := []string{"a#1", "b#9", "a#2", "b#8"}
	// a's own put in another order on its page: its places in the order kept, filled anew.
	if got := favMerge(order, "a", []string{"2", "1"}); !slices.Equal(got, []string{"a#2", "b#9", "a#1", "b#8"}) {
		t.Fatalf("moved: %v", got)
	}
	// One taken off, one new: the new last.
	if got := favMerge(order, "a", []string{"2", "3"}); !slices.Equal(got, []string{"a#2", "b#9", "b#8", "a#3"}) {
		t.Fatalf("off and new: %v", got)
	}
	// None left.
	if got := favMerge(order, "b", nil); !slices.Equal(got, []string{"a#1", "a#2"}) {
		t.Fatalf("none: %v", got)
	}
}

func TestFavOrderOf(t *testing.T) {
	if got := favOrderOf([]string{"a#1", "b#9", "a#2"}, "a"); !slices.Equal(got, []string{"1", "2"}) {
		t.Fatalf("%v", got)
	}
	// An address with a port, an id after the last #.
	if got := favOrderOf([]string{"http://h:1#x", "http://h:2#y"}, "http://h:1"); !slices.Equal(got, []string{"x"}) {
		t.Fatalf("%v", got)
	}
}

func TestFavIDs(t *testing.T) {
	items := []json.RawMessage{json.RawMessage(`{"id":"1","title":"x"}`), json.RawMessage(`{"title":"no id"}`), json.RawMessage(`{"id":"2"}`)}
	if got := favIDs(items); !slices.Equal(got, []string{"1", "2"}) {
		t.Fatalf("%v", got)
	}
}
