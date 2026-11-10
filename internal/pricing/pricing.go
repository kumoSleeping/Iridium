// Package pricing prices tokens an agent did not price itself (the user, 2026-11-10): at the list
// price of the model's own maker, from models.dev, whoever served it. A DeepSeek model reached
// through another provider is priced as DeepSeek prices it.
package pricing

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// URL is models.dev's catalogue.
const URL = "https://models.dev/api.json"

// fresh is how long a catalogue fetched is used before it is fetched again.
const fresh = 24 * time.Hour

// Rates are a model's list prices, dollars per million tokens.
type Rates struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
}

// Cost is what the tokens cost at these rates.
func (r Rates) Cost(input, output, cacheRead, cacheWrite int64) float64 {
	return (float64(input)*r.Input + float64(output)*r.Output + float64(cacheRead)*r.CacheRead + float64(cacheWrite)*r.CacheWrite) / 1e6
}

// makers: a model's maker from the start of its bare name, as models.dev names the provider.
var makers = []struct{ prefix, provider string }{
	{"claude", "anthropic"},
	{"gpt", "openai"}, {"chatgpt", "openai"}, {"codex", "openai"}, {"o1", "openai"}, {"o3", "openai"}, {"o4", "openai"},
	{"deepseek", "deepseek"},
	{"gemini", "google"}, {"gemma", "google"},
	{"qwen", "alibaba"}, {"qwq", "alibaba"},
	{"kimi", "moonshotai"}, {"moonshot", "moonshotai"},
	{"glm", "zhipuai"},
	{"grok", "xai"},
	{"mistral", "mistral"}, {"codestral", "mistral"}, {"devstral", "mistral"}, {"magistral", "mistral"}, {"ministral", "mistral"}, {"pixtral", "mistral"},
	{"minimax", "minimax"},
}

var (
	nonWord = regexp.MustCompile(`[^a-z0-9]+`)
	dated   = regexp.MustCompile(`-(\d{8}|\d{4}-\d{2}-\d{2}|\d{4})$`)
)

// norm is a name for matching: lower case, words joined by "-" ("DeepSeek V4.1 Flash 1017":
// deepseek-v4-1-flash-1017).
func norm(s string) string {
	return strings.Trim(nonWord.ReplaceAllString(strings.ToLower(s), "-"), "-")
}

// bare is a model as its maker names it: no route before it, no variant after it
// ("mistral/deepseek-v4-1-flash-1017": deepseek-v4-1-flash-1017).
func bare(model string) string {
	if i := strings.LastIndexByte(model, '/'); i >= 0 {
		model = model[i+1:]
	}
	if i := strings.IndexAny(model, "@:"); i >= 0 {
		model = model[:i]
	}
	return norm(model)
}

func maker(b string) string {
	for _, m := range makers {
		if b == m.prefix || strings.HasPrefix(b, m.prefix+"-") || (len(m.prefix) > 2 && strings.HasPrefix(b, m.prefix)) {
			return m.provider
		}
	}
	return ""
}

// entry is one model in the catalogue kept: its ID, its name and its rates.
type entry struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Rates Rates  `json:"rates"`
}

// saved is the catalogue as kept on disk: only the makers' own models.
type saved struct {
	Fetched   int64              `json:"fetched"`
	Providers map[string][]entry `json:"providers"`
}

// Catalog is the makers' prices; the zero value prices nothing.
type Catalog struct {
	path   string
	client *http.Client
	url    string

	mu      sync.RWMutex
	fetched time.Time
	index   map[string]map[string]Rates // provider: name (norm, and without a date): rates
}

// Open is the catalogue kept at dir (as it was last fetched, if it was); Run keeps it fresh.
func Open(dir string) *Catalog {
	c := &Catalog{path: filepath.Join(dir, "models-dev.json"), client: &http.Client{Timeout: time.Minute}, url: URL}
	if b, err := os.ReadFile(c.path); err == nil {
		var s saved
		if json.Unmarshal(b, &s) == nil {
			c.set(s)
		}
	}
	return c
}

func (c *Catalog) set(s saved) {
	index := map[string]map[string]Rates{}
	for p, es := range s.Providers {
		m := map[string]Rates{}
		// Exact names first, so a dated name never takes the place of one that is exactly it.
		for _, e := range es {
			for _, k := range []string{norm(e.ID), norm(e.Name)} {
				if k != "" {
					m[k] = e.Rates
				}
			}
		}
		for _, e := range es {
			for _, k := range []string{norm(e.ID), norm(e.Name)} {
				if d := dated.ReplaceAllString(k, ""); d != k {
					if _, ok := m[d]; !ok {
						m[d] = e.Rates
					}
				}
			}
		}
		index[p] = m
	}
	c.mu.Lock()
	c.index, c.fetched = index, time.UnixMilli(s.Fetched)
	c.mu.Unlock()
}

// Rates is the model's maker's list price for it; false when the maker or the model is not known.
func (c *Catalog) Rates(model string) (Rates, bool) {
	if c == nil {
		return Rates{}, false
	}
	b := bare(model)
	p := maker(b)
	if p == "" {
		return Rates{}, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	m := c.index[p]
	if r, ok := m[b]; ok {
		return r, true
	}
	r, ok := m[dated.ReplaceAllString(b, "")]
	return r, ok
}

// Cost is the tokens' cost at the model's list price; false when it has none.
func (c *Catalog) Cost(model string, input, output, cacheRead, cacheWrite int64) (float64, bool) {
	r, ok := c.Rates(model)
	if !ok {
		return 0, false
	}
	return r.Cost(input, output, cacheRead, cacheWrite), true
}

// Run fetches the catalogue when it is older than a day, then once a day, till ctx ends. A
// failed fetch keeps the one there is and is tried again in an hour.
func (c *Catalog) Run(ctx context.Context) {
	for {
		c.mu.RLock()
		next := time.Until(c.fetched.Add(fresh))
		c.mu.RUnlock()
		if next <= 0 {
			if err := c.Fetch(ctx); err != nil {
				next = time.Hour
			} else {
				next = fresh
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(next):
		}
	}
}

// Fetch reads the catalogue from models.dev and keeps the makers' models.
func (c *Catalog) Fetch(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "iridium")
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("models.dev: %s", resp.Status)
	}
	s, err := parse(resp.Body)
	if err != nil {
		return err
	}
	s.Fetched = time.Now().UnixMilli()
	c.set(s)
	if b, err := json.Marshal(s); err == nil {
		tmp := c.path + ".tmp"
		if os.WriteFile(tmp, b, 0o600) == nil {
			_ = os.Rename(tmp, c.path)
		}
	}
	return nil
}

// parse reads models.dev's api.json: of each maker its models' base prices (a missing cache
// price is the input's: the maker bills those tokens as input).
func parse(r io.Reader) (saved, error) {
	var all map[string]struct {
		Models map[string]struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Cost *struct {
				Input      *float64 `json:"input"`
				Output     *float64 `json:"output"`
				CacheRead  *float64 `json:"cache_read"`
				CacheWrite *float64 `json:"cache_write"`
			} `json:"cost"`
		} `json:"models"`
	}
	if err := json.NewDecoder(r).Decode(&all); err != nil {
		return saved{}, fmt.Errorf("models.dev: %w", err)
	}
	wanted := map[string]bool{}
	for _, m := range makers {
		wanted[m.provider] = true
	}
	s := saved{Providers: map[string][]entry{}}
	for p, v := range all {
		if !wanted[p] {
			continue
		}
		for id, m := range v.Models {
			if m.Cost == nil || m.Cost.Input == nil || m.Cost.Output == nil {
				continue
			}
			r := Rates{Input: *m.Cost.Input, Output: *m.Cost.Output, CacheRead: *m.Cost.Input, CacheWrite: *m.Cost.Input}
			if m.Cost.CacheRead != nil {
				r.CacheRead = *m.Cost.CacheRead
			}
			if m.Cost.CacheWrite != nil {
				r.CacheWrite = *m.Cost.CacheWrite
			}
			if m.ID != "" {
				id = m.ID
			}
			s.Providers[p] = append(s.Providers[p], entry{ID: id, Name: m.Name, Rates: r})
		}
	}
	if len(s.Providers) == 0 {
		return saved{}, fmt.Errorf("models.dev: no known maker in the catalogue")
	}
	return s, nil
}

// Read is a catalogue read from a models.dev api.json (tests, and a copy at hand).
func Read(r io.Reader) (*Catalog, error) {
	s, err := parse(r)
	if err != nil {
		return nil, err
	}
	c := &Catalog{}
	c.set(s)
	return c, nil
}
