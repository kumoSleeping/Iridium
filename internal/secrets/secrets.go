// Package secrets is what the daemon and ir share of the credentials (plan "凭据"): their names,
// the forms a value may take in output (as is, base64, URL encoded), and the masks that put
// [secret:NAME] in their place, on whole buffers and on streams (where a value may be split
// across writes).
package secrets

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// MaxValue is the longest value a secret may have (an ssh key, a kubeconfig fit).
const MaxValue = 64 << 10

var nameRE = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)

// ValidName reports whether name may name a secret: capitals, digits and _, a letter first, at
// most 64.
func ValidName(name string) bool { return nameRE.MatchString(name) }

// Placeholder is what a value of name is replaced with.
func Placeholder(name string) string { return "[secret:" + name + "]" }

// MinShown is the shortest value the daemon masks in transcripts: shorter ones would hit too much
// that is not them.
const MinShown = 6

// minEncoded is the shortest base64 run that counts as a value's (shorter ones are noise).
const minEncoded = 8

// Forms are the ways value may show in output: as is, URL encoded, and the base64 runs (standard
// and URL alphabets) that come of it wherever it starts in what was encoded (the characters that
// depend on its bytes alone).
func Forms(value string) []string {
	if value == "" {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	add(value)
	add(url.QueryEscape(value))
	add(url.PathEscape(value))
	b := []byte(value)
	for k := 0; k < 3; k++ {
		buf := append(make([]byte, k), b...)
		enc := base64.RawStdEncoding.EncodeToString(buf)
		from := (8*k + 5) / 6
		to := 8 * len(buf) / 6
		if to-from < minEncoded || to > len(enc) {
			continue
		}
		core := enc[from:to]
		add(core)
		add(strings.NewReplacer("+", "-", "/", "_").Replace(core))
	}
	return out
}

// JSONForms is s as it shows inside a JSON string, with and without HTML escaping.
func JSONForms(s string) []string {
	var out []string
	for _, html := range []bool{true, false} {
		var b bytes.Buffer
		enc := json.NewEncoder(&b)
		enc.SetEscapeHTML(html)
		_ = enc.Encode(s)
		j := strings.TrimSuffix(b.String(), "\n")
		j = j[1 : len(j)-1]
		if len(out) == 0 || out[0] != j {
			out = append(out, j)
		}
	}
	return out
}

type pattern struct {
	b    []byte
	with []byte
}

// Masker replaces known values in bytes. The zero Masker (and nil) replaces nothing.
type Masker struct {
	pats []pattern // longest first
	max  int
}

// NewMasker masks each name's value in all its Forms. min: values shorter than it are left alone.
// json: the forms as they show inside JSON strings.
func NewMasker(values map[string]string, min int, json bool) *Masker {
	m := &Masker{}
	seen := map[string]bool{}
	names := make([]string, 0, len(values))
	for n := range values {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		v := values[name]
		if len(v) < max(min, 1) {
			continue
		}
		with := []byte(Placeholder(name))
		for _, f := range Forms(v) {
			fs := []string{f}
			if json {
				fs = JSONForms(f)
			}
			for _, f := range fs {
				if f == "" || seen[f] {
					continue
				}
				seen[f] = true
				m.pats = append(m.pats, pattern{b: []byte(f), with: with})
				m.max = max(m.max, len(f))
			}
		}
	}
	sort.SliceStable(m.pats, func(i, j int) bool { return len(m.pats[i].b) > len(m.pats[j].b) })
	return m
}

// Empty reports whether m masks nothing.
func (m *Masker) Empty() bool { return m == nil || len(m.pats) == 0 }

// next is the first match in b at or after i: its start, its pattern; -1 if none. Of those that
// start together, the longest.
func (m *Masker) next(b []byte, i int) (int, *pattern) {
	best, bp := -1, (*pattern)(nil)
	for k := range m.pats {
		p := &m.pats[k]
		j := bytes.Index(b[i:], p.b)
		if j < 0 {
			continue
		}
		if j += i; best < 0 || j < best {
			best, bp = j, p
		}
	}
	return best, bp
}

// Mask is b with every known value replaced; b itself if there is none.
func (m *Masker) Mask(b []byte) []byte {
	if m.Empty() {
		return b
	}
	out, rest := m.mask(nil, b, true)
	return append(out, rest...)
}

// MaskString is Mask on a string.
func (m *Masker) MaskString(s string) string {
	if m.Empty() {
		return s
	}
	return string(m.Mask([]byte(s)))
}

// mask appends b, masked, to out, but for a tail that might be the start of a value cut off (when
// !final), which it gives back unmasked.
func (m *Masker) mask(out, b []byte, final bool) ([]byte, []byte) {
	i := 0
	for {
		j, p := m.next(b, i)
		if j < 0 {
			break
		}
		out = append(out, b[i:j]...)
		out = append(out, p.with...)
		i = j + len(p.b)
	}
	if final {
		return append(out, b[i:]...), nil
	}
	keep := m.held(b[i:])
	cut := len(b) - keep
	return append(out, b[i:cut]...), b[cut:]
}

// held is how long the longest end of b is that begins some value.
func (m *Masker) held(b []byte) int {
	from := max(0, len(b)-m.max+1)
	for j := from; j < len(b); j++ {
		for k := range m.pats {
			p := m.pats[k].b
			if len(p) > len(b)-j && bytes.HasPrefix(p, b[j:]) {
				return len(b) - j
			}
		}
	}
	return 0
}

// Writer masks what goes through it to w. A value split across writes is masked all the same: the
// end of a write that might begin one waits for the next (or Close). Masker gives the masks to use
// at each write (they may change).
type Writer struct {
	w      io.Writer
	masker func() *Masker
	mu     sync.Mutex
	held   []byte
}

func NewWriter(w io.Writer, masker func() *Masker) *Writer {
	return &Writer{w: w, masker: masker}
}

func (w *Writer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	m := w.masker()
	if m.Empty() && len(w.held) == 0 {
		return w.w.Write(p)
	}
	buf := append(w.held, p...)
	var out []byte
	if m.Empty() {
		out, w.held = buf, nil
	} else {
		var rest []byte
		out, rest = m.mask(make([]byte, 0, len(buf)), buf, false)
		w.held = append([]byte(nil), rest...)
	}
	if len(out) > 0 {
		if _, err := w.w.Write(out); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

// Close writes out what waited, masked.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.held) == 0 {
		return nil
	}
	out := w.masker().Mask(w.held)
	w.held = nil
	_, err := w.w.Write(out)
	return err
}

// Refs are the names a command line refers to: {{NAME}} and {{file:NAME}}.
var refRE = regexp.MustCompile(`\{\{(file:)?([A-Z][A-Z0-9_]{0,63})\}\}`)

// Refs is every name args refer to, in order, once each.
func Refs(args []string) []string {
	var out []string
	for _, a := range args {
		for _, m := range refRE.FindAllStringSubmatch(a, -1) {
			if !contains(out, m[2]) {
				out = append(out, m[2])
			}
		}
	}
	return out
}

// Expand is arg with {{NAME}} as value(NAME) and {{file:NAME}} as file(NAME).
func Expand(arg string, value, file func(string) string) string {
	return refRE.ReplaceAllStringFunc(arg, func(s string) string {
		m := refRE.FindStringSubmatch(s)
		if m[1] != "" {
			return file(m[2])
		}
		return value(m[2])
	})
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}
