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
	pre, _, _ := strings.Cut(API, "\n\n")
	f := Find([]string{"secrets"})
	if !strings.HasPrefix(f, pre+"\n# secrets\n") || strings.Contains(f, "channel.list {") {
		t.Errorf("Find: %q", f)
	}
	c := Common()
	if !strings.Contains(c, "message.create ") || strings.Contains(c, "internal/secrets") || !strings.HasPrefix(c, pre) {
		t.Errorf("Common: %q", c)
	}
	if n := strings.Count(Help, "\n"); n > 20 {
		t.Errorf("ir --help is %d lines", n)
	}
}
