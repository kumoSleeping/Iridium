package dsh

import (
	"strings"
	"testing"

	"github.com/kumoSleeping/iridium/internal/remote/wire"
)

// What a plugin says of its features is checked: IDs, the first of each, a known shape and place,
// at most four; texts trimmed, without control characters, cut short; options checked.
func TestFeatures(t *testing.T) {
	c := New(Options{Logf: t.Logf})
	long := strings.Repeat("長", 200)
	in := []wire.Feature{
		{ID: "a.one", Name: " One\x07 ", Place: "effort", Tone: "hot", Hint: long, Reason: "r\n",
			Options: []wire.FeatureOption{{ID: "o1", Name: long}, {ID: "Bad", Name: "x"}, {ID: "o1", Name: "dup"}}, Option: "Bad"},
		{ID: "a.one", Name: "second", Place: "effort"},
		{ID: "noplugin", Name: "x", Place: "effort"},
		{ID: "a.two", Name: "x", Place: "elsewhere"},
		{ID: "a.three", Name: "x", Place: "effort", V: 2},
		{ID: "a.b", Name: "b", Place: "effort", Tone: "warm", V: 1},
		{ID: "a.c", Name: "c", Place: "effort"},
		{ID: "a.d", Name: "d", Place: "effort"},
		{ID: "a.e", Name: "e", Place: "effort"},
	}
	for i := range 10 {
		in[0].Options = append(in[0].Options, wire.FeatureOption{ID: "n" + string(rune('a'+i)), Name: "n"})
	}
	out := c.features("m1", in)
	var ids []string
	for _, f := range out {
		ids = append(ids, f.ID)
	}
	if strings.Join(ids, " ") != "a.one a.b a.c a.d" {
		t.Fatalf("ids: %v", ids)
	}
	one := out[0]
	if one.Name != "One" || one.Tone != "" || len([]rune(one.Hint)) != 120 || one.Reason != "r" || one.Option != "" ||
		len(one.Options) != 8 || one.Options[0].ID != "o1" || len([]rune(one.Options[0].Name)) != 24 || one.Options[1].ID != "na" {
		t.Errorf("checked: %+v", one)
	}
	if out[1].Tone != "warm" {
		t.Errorf("tone: %+v", out[1])
	}
	if c.features("m1", []wire.Feature{{ID: "a.b", Place: "effort", Options: []wire.FeatureOption{{ID: "x", Name: "X"}}, Option: "x"}})[0].Option != "x" {
		t.Error("an option of its own dropped")
	}
}
