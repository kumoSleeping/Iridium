package dsh

import (
	"reflect"
	"strings"
	"testing"

	"github.com/kumoSleeping/iridium/internal/agent"
)

// The probe's line, as dsh 0.2.2-rc.1 printed it with a DeepSeek key, among dsh's own output: a
// provider without models offers none; with one provider its name is not said.
func TestParseProbe(t *testing.T) {
	out := "[llm-deepseek-api-key] model listing loaded 2 model(s)\n" +
		`IRIDIUM-CATALOG {"providers":[{"id":"deepseek-official","name":"DeepSeek","models":[{"id":"deepseek-flash","name":"DeepSeek-V4.1-Flash","efforts":["off","low","high","max"],"defaultEffort":"high"},{"id":"deepseek-v4-pro","name":"DeepSeek-V4-Pro","description":"Stronger.","efforts":["off","low","high","max"]}]},{"id":"deepseek-account","name":"DeepSeek Account","models":[]}],"default":{"provider":"deepseek-official","model":"deepseek-flash"}}` + "\n"
	c, err := ParseProbe([]byte(out))
	if err != nil {
		t.Fatal(err)
	}
	want := agent.Catalog{Default: "deepseek-official/deepseek-flash", Models: []agent.Model{
		{Value: "deepseek-official/deepseek-flash", Name: "DeepSeek-V4.1-Flash", Efforts: []string{"off", "low", "high", "max"}},
		{Value: "deepseek-official/deepseek-v4-pro", Name: "DeepSeek-V4-Pro", Description: "Stronger.", Efforts: []string{"off", "low", "high", "max"}},
	}}
	if !reflect.DeepEqual(c, want) {
		t.Errorf("%+v", c)
	}
	// Two providers with models: each model says its provider; one without efforts takes none.
	two := `IRIDIUM-CATALOG {"providers":[{"id":"a","name":"A","models":[{"id":"m","name":"M"}]},{"id":"b","name":"B","models":[{"id":"x/y","name":"Y","description":"d"}]}],"default":null}`
	c, err = ParseProbe([]byte(two))
	if err != nil || len(c.Models) != 2 || c.Models[0].Description != "A" || !c.Models[0].NoEffort || c.Models[1].Value != "b/x/y" || c.Models[1].Description != "B · d" || c.Default != "" {
		t.Errorf("two: %+v %v", c, err)
	}
	if _, err := ParseProbe([]byte("dsh: warning\n")); err == nil {
		t.Error("no line")
	}
}

// A model value's route: the catalog's, a value set before models were dsh's (no provider) as the
// provider that has it, a provider's model id with a '/' of its own.
func TestRoute(t *testing.T) {
	c := agent.Catalog{Default: "b/x/y", Models: []agent.Model{{Value: "a/m"}, {Value: "b/x/y"}}}
	for _, k := range []struct{ value, p, id string }{
		{"a/m", "a", "m"},
		{"b/x/y", "b", "x/y"},
		{"m", "a", "m"},
		{"x/y", "b", "x/y"},
		{"other", "b", "other"},
		{"a/new", "a", "new"},
	} {
		if p, id := Route(k.value, c); p != k.p || id != k.id {
			t.Errorf("%s: %s %s", k.value, p, id)
		}
	}
	if p, id := Route("deepseek-flash", agent.Catalog{}); p != LegacyProvider || id != "deepseek-flash" {
		t.Errorf("legacy: %s %s", p, id)
	}
	if p, id := Route("p/m", agent.Catalog{}); p != "p" || id != "m" {
		t.Errorf("no catalog: %s %s", p, id)
	}
}

func TestFileURL(t *testing.T) {
	if u := FileURL(`C:\Users\o\x y\probe.mjs`); u != "file:///C:/Users/o/x%20y/probe.mjs" {
		t.Error(u)
	}
	if u := FileURL("/tmp/a/probe.mjs"); u != "file:///tmp/a/probe.mjs" {
		t.Error(u)
	}
	if p := string(ProbePatch("file:///tmp/a'b.mjs")); !strings.Contains(p, "name: 'file:///tmp/a''b.mjs'") {
		t.Error(p)
	}
}
