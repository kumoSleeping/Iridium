package session

import (
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/kumoSleeping/iridium/internal/api"
)

// Sessions alike in pin and time come in one order whatever order they are found in: the list
// is the same, and not sent to every page again.
func TestListOrderTotal(t *testing.T) {
	base := []api.Session{
		{ID: "p", Pinned: true, UpdatedAt: 1},
		{ID: "agent-b", UpdatedAt: 5},
		{ID: "agent-a", UpdatedAt: 5},
		{ID: "agent-c", UpdatedAt: 5},
		{ID: "o", Order: 9},
		{ID: "x", UpdatedAt: 3},
	}
	want := []string{"p", "o", "agent-a", "agent-b", "agent-c", "x"}
	for range 50 {
		l := slices.Clone(base)
		rand.Shuffle(len(l), func(i, j int) { l[i], l[j] = l[j], l[i] })
		slices.SortFunc(l, listOrder)
		var got []string
		for _, s := range l {
			got = append(got, s.ID)
		}
		if !slices.Equal(got, want) {
			t.Fatalf("order %v, want %v", got, want)
		}
	}
}
