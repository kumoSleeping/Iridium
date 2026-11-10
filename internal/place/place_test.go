package place

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// The cases web/src/places.ts is tested by too.
type cases struct {
	Apps  map[string]string `json:"apps"`
	Split []struct {
		In, Name, Path string
	} `json:"split"`
	Parse []struct {
		In, Self, App, Path string
		Error               bool
	} `json:"parse"`
	Format []struct {
		App, Path, Out string
	} `json:"format"`
	Key []struct {
		App, Path, Key, Back string
	} `json:"key"`
}

func TestCases(t *testing.T) {
	b, err := os.ReadFile("testdata/places.json")
	if err != nil {
		t.Fatal(err)
	}
	var c cases
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	lookup := func(name string) (string, bool) {
		for id, n := range c.Apps {
			if strings.EqualFold(n, name) {
				return id, true
			}
		}
		return "", false
	}
	for _, x := range c.Split {
		name, path, ok := Split(x.In)
		if name != x.Name || path != x.Path || ok != (x.Name != "") {
			t.Errorf("Split(%q) = %q %q %v", x.In, name, path, ok)
		}
	}
	for _, x := range c.Parse {
		p, err := Parse(x.In, x.Self, lookup)
		if x.Error {
			if err == nil {
				t.Errorf("Parse(%q): %+v, no error", x.In, p)
			}
			continue
		}
		if err != nil || p.App != x.App || p.Path != x.Path {
			t.Errorf("Parse(%q, %q) = %+v %v", x.In, x.Self, p, err)
		}
	}
	for _, x := range c.Format {
		if got := Format(Place{App: x.App, Path: x.Path}, func(id string) string { return c.Apps[id] }); got != x.Out {
			t.Errorf("Format(%q %q) = %q", x.App, x.Path, got)
		}
	}
	for _, x := range c.Key {
		p := Place{App: x.App, Path: x.Path}
		if got := Key(p); got != x.Key {
			t.Errorf("Key(%+v) = %q", p, got)
		}
		back := x.Back
		if back == "" {
			back = x.Path
		}
		if got := FromKey(x.Key); got.App != x.App || got.Path != back {
			t.Errorf("FromKey(%q) = %+v", x.Key, got)
		}
	}
}
