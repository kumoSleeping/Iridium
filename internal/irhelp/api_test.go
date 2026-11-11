package irhelp

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The reference has every route the server registers under /v1/internal and every Satori method,
// once each, and nothing else: ir api help is all an agent learns the API from.
func TestAPICoversRoutes(t *testing.T) {
	files, _ := filepath.Glob("../server/*.go")
	re := regexp.MustCompile(`HandleFunc\("(GET|POST|PUT|PATCH|DELETE) /v1/(internal/[^"]+)"`)
	want := map[string]bool{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range re.FindAllStringSubmatch(string(b), -1) {
			want[m[1]+" "+m[2]] = true
		}
	}
	// satori.Features (read, not imported: satori imports this package by way of session).
	b, err := os.ReadFile("../satori/satori.go")
	if err != nil {
		t.Fatal(err)
	}
	feats := regexp.MustCompile(`(?s)var Features = \[\]string\{(.*?)\}`).FindSubmatch(b)
	if feats == nil {
		t.Fatal("no satori.Features")
	}
	for _, m := range regexp.MustCompile(`"([a-z.]+)"`).FindAllSubmatch(feats[1], -1) {
		want["POST "+string(m[1])] = true
	}
	want["POST meta"] = true
	if len(want) < 100 {
		t.Fatalf("only %d routes found in ../server", len(want))
	}
	got := map[string]bool{}
	for _, r := range Routes() {
		k := r.Method + " " + r.Path
		if got[k] {
			t.Errorf("%s twice", k)
		}
		got[k] = true
		if !want[k] {
			t.Errorf("%s: not a route", k)
		}
	}
	for k := range want {
		if !got[k] {
			t.Errorf("%s: not in api.txt", k)
		}
	}
}

func TestFindAndCommon(t *testing.T) {
	if r, ok := Match("POST", "internal/sessions/abc/stop"); !ok || r.Path != "internal/sessions/{id}/stop" {
		t.Errorf("Match: %+v %v", r, ok)
	}
	if r, ok := Match("POST", "channel.list"); !ok || !r.Satori || len(r.View) == 0 {
		t.Errorf("Match channel.list: %+v %v", r, ok)
	}
	o := Find(nil)
	if !strings.HasPrefix(o, Usage) || !strings.Contains(o, "# topics: common, satori, sessions") || !strings.Contains(o, "# common\nPOST internal/sessions") || strings.Contains(o, "internal/secrets/use") {
		t.Errorf("overview: %q", o)
	}
	if f := Find([]string{"secrets"}); !strings.HasPrefix(f, "# secrets\nGET internal/secrets ") || !strings.Contains(f, "# also\nPUT internal/secrets/{name}") || strings.Contains(f, "# common") {
		t.Errorf("a topic: %q", f)
	}
	if f := Find([]string{"queue", "drop"}); f != "# queue\nDELETE internal/sessions/{id}/queue/{input} - -> {ok}  drop a queued input\n" {
		t.Errorf("words: %q", f)
	}
	if f := Find([]string{"no-such-word"}); !strings.Contains(f, "topics: ") {
		t.Errorf("nothing found: %q", f)
	}
	if Find([]string{"all"}) != API {
		t.Error("all")
	}
	if n := strings.Count(Help, "\n"); n > 8 {
		t.Errorf("ir --help is %d lines", n)
	}
}
