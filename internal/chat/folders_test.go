package chat

import (
	"slices"
	"strconv"
	"testing"

	"github.com/kumoSleeping/iridium/internal/api"
)

// The directory 未归类's folders are under, as the web's tree.ts finds it.
func TestCommonDir(t *testing.T) {
	for _, c := range []struct {
		in   []string
		want string
	}{
		{[]string{"/home/a/x", "/home/a/y/z"}, "/home/a"},
		{[]string{"/home/a/x", "/home/a/x"}, "/home/a"},
		{[]string{"/home/a/x", "/home/a/x/y"}, "/home/a/x"},
		{[]string{"/a", "/b"}, ""},
		{nil, ""},
	} {
		if got := commonDir(c.in); got != c.want {
			t.Errorf("commonDir(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// A long list goes in pages of pageSize: the extra entries on each, then 上一页 and 下一页.
func TestPaged(t *testing.T) {
	var items []api.CardOption
	for i := range 16 {
		items = append(items, api.CardOption{ID: strconv.Itoa(i)})
	}
	extra := []api.CardOption{{ID: "n:w1", Text: newText}}
	ids := func(c api.Card) []string {
		var out []string
		for _, o := range c.Options {
			out = append(out, o.ID)
		}
		return out
	}
	for _, c := range []struct {
		n    int
		want []string
		sum  string
	}{
		{0, []string{"0", "1", "2", "3", "4", "5", "6", "n:w1", "p:1:g:w1"}, "第 1/3 页"},
		{1, []string{"7", "8", "9", "10", "11", "12", "13", "n:w1", "p:0:g:w1", "p:2:g:w1"}, "第 2/3 页"},
		{9, []string{"14", "15", "n:w1", "p:1:g:w1"}, "第 3/3 页"},
	} {
		card := api.Card{}
		paged(&card, items, extra, c.n, "g:w1")
		if got := ids(card); !slices.Equal(got, c.want) || card.Summary[0] != c.sum {
			t.Errorf("page %d: %v %v", c.n, got, card.Summary)
		}
		if len(card.Options) > 10 {
			t.Errorf("page %d: %d options, more than QQ's buttons", c.n, len(card.Options))
		}
	}
	card := api.Card{}
	paged(&card, items[:3], extra, 0, "use")
	if len(card.Options) != 4 || len(card.Summary) != 0 {
		t.Errorf("one page: %+v", card)
	}
}
