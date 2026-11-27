package desktop

import (
	"slices"
	"testing"
)

func TestSetAway(t *testing.T) {
	var c Config
	c.setAway("a", true, Frame{})
	c.setAway("a", true, Frame{X: 1, Y: 2, W: 800, H: 600})
	c.setAway("b", true, Frame{})
	if len(c.Away) != 2 || c.away("a").Frame.W != 800 {
		t.Fatalf("away: %+v", c.Away)
	}
	c.remove("a")
	if c.away("a") != nil || c.away("b") == nil {
		t.Fatalf("removed: %+v", c.Away)
	}
	c.setAway("b", false, Frame{})
	if len(c.Away) != 0 {
		t.Fatalf("taken back: %+v", c.Away)
	}
}

func TestRailOrder(t *testing.T) {
	u := &ui{}
	c := Config{Servers: []Server{{URL: "r1"}, {URL: "l", Local: true}, {URL: "r2"}}}
	if got := u.railOrder(c); !slices.Equal(got, []string{"l", "r1", "r2"}) {
		t.Fatalf("this computer first: %v", got)
	}
	c.HideLocal = true
	if got := u.railOrder(c); !slices.Equal(got, []string{"r1", "r2"}) {
		t.Fatalf("hidden: %v", got)
	}
}

func TestLetterOf(t *testing.T) {
	for in, want := range map[string]string{"staging": "S", " 线上服": "线", "": "?", "ir.kumo.ltd": "I"} {
		if got := letterOf(in); got != want {
			t.Errorf("letterOf(%q) = %q, want %q", in, got, want)
		}
	}
}
