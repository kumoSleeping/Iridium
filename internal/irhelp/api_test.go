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
	want["POST internal/button"] = true // the satori package's own
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
	if !strings.HasPrefix(o, Usage) || !strings.Contains(o, "(common, satori, sessions") || !strings.Contains(o, "# common\nchannel.create {") || strings.Contains(o, "internal/secrets/use") {
		t.Errorf("overview: %q", o)
	}
	if f := Find([]string{"secrets"}); !strings.HasPrefix(f, "# secrets\nGET internal/secrets ") || !strings.Contains(f, "# also\nPUT internal/secrets/{name}") || strings.Contains(f, "# common") {
		t.Errorf("a topic: %q", f)
	}
	if f := Find([]string{"queue", "resume"}); f != "# queue\nPOST internal/sessions/{id}/continue - -> {ok}  resume a paused queue\n" {
		t.Errorf("words: %q", f)
	}
	if f := Find([]string{"no-such-word"}); !strings.Contains(f, "topics: ") {
		t.Errorf("nothing found: %q", f)
	}
	// all: every route, notes left out, marked where there are some.
	if a := Find([]string{"all"}); !strings.Contains(a, "# settings (admin)\n") || strings.Contains(a, "\n  data.parent_id:") || !strings.Contains(a, "guild_id?} -> Channel") || strings.Count(a, "  [more]") < 5 {
		t.Errorf("all: %q", a)
	}
	// A route, by method and path, by path alone (as sent too), or a Satori method's name: its
	// line and notes.
	full := Find([]string{"GET", "internal/sessions/{id}"})
	if !strings.HasPrefix(full, "GET internal/sessions/{id} -") || !strings.Contains(full, "\n  --wait: until its turn ends") || strings.Contains(full, "\nPOST internal/sessions") {
		t.Errorf("a route: %q", full)
	}
	if f := Find([]string{"/v1/internal/sessions/abc/rules"}); !strings.Contains(f, "GET internal/sessions/{id}/rules - ") || !strings.Contains(f, "DELETE internal/sessions/{id}/rules - ") {
		t.Errorf("a path, both methods: %q", f)
	}
	if f := Find([]string{"patch", "internal/guilds/7"}); !strings.HasPrefix(f, "PATCH internal/guilds/{id} ") {
		t.Errorf("a path as sent: %q", f)
	}
	if f := Find([]string{"channel.delete"}); !strings.HasPrefix(f, "channel.delete ") || !strings.Contains(f, "\n  The sessions hung under it") {
		t.Errorf("a Satori method: %q", f)
	}
	// Notes stay out of lists; a word found in one brings that note along.
	if f := Find([]string{"common"}); strings.Contains(f, "\n  ") || !strings.Contains(f, "guild_id?} -> Channel  start a session, a child of yours; _data.cwd=. your folder; --wait for its reply  [more]\n") {
		t.Errorf("a topic with notes: %q", f)
	}
	if f := Find([]string{"launcher"}); !strings.Contains(f, "\n  data.parent_id: the session it hangs under") || !strings.Contains(f, "\n  data.parent_id:=null") || strings.Contains(f, "\n  _data.cwd:") {
		t.Errorf("a word in notes: %q", f)
	}
	if n := strings.Count(Help, "\n"); n > 8 {
		t.Errorf("ir --help is %d lines", n)
	}
}
