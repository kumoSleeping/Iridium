package pricing

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func serve(t *testing.T) *httptest.Server {
	b, err := os.ReadFile("testdata/api.json")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(b) }))
	t.Cleanup(srv.Close)
	return srv
}

func TestRatesByMaker(t *testing.T) {
	dir := t.TempDir()
	c := Open(dir)
	if _, ok := c.Rates("claude-opus-5-5"); ok {
		t.Fatal("priced before any catalogue")
	}
	c.url = serve(t).URL
	if err := c.Fetch(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		model string
		in    float64
		ok    bool
	}{
		{"claude-opus-5-5", 4, true},
		// Another provider's route: the maker's own price (DeepSeek's, by its model's name), not Venice's.
		{"mistral/deepseek-v4-1-flash-1017", 0.15, true},
		{"deepseek-official/deepseek-flash", 0.15, true},
		{"opencode-go/claude-haiku-5-5", 0.1, true},
		{"gpt-6-luna", 0.1, true},
		{"gpt-6.1-sol", 2, true},
		{"claude-opus-5-5-20261001", 4, true},
		{"some-unknown-model", 0, false},
		{"deepseek-v9-ultra", 0, false},
	} {
		r, ok := c.Rates(tc.model)
		if ok != tc.ok || r.Input != tc.in {
			t.Errorf("%s: %+v %v, want input %v %v", tc.model, r, ok, tc.in, tc.ok)
		}
	}
	// DeepSeek gives no cache-write price: those tokens are input.
	if r, _ := c.Rates("deepseek-flash"); r.CacheWrite != r.Input || r.CacheRead != 0.003 {
		t.Errorf("deepseek rates %+v", r)
	}
	got, _ := c.Cost("claude-opus-5-5", 1_000_000, 1_000_000, 10_000_000, 0)
	if math.Abs(got-(4+20+2)) > 1e-9 {
		t.Errorf("cost %v", got)
	}
	// Kept on disk: the next start prices at once.
	if _, ok := Open(dir).Rates("gpt-6-luna"); !ok {
		t.Error("catalogue not kept")
	}
}

func TestNilCatalog(t *testing.T) {
	var c *Catalog
	if _, ok := c.Cost("claude-opus-5-5", 1, 1, 1, 1); ok {
		t.Fatal("nil catalogue priced")
	}
}
