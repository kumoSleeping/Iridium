package files

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/kumoSleeping/iridium/internal/api"
)

// A Mention is a file a block names, as written: Raw before it is resolved.
type Mention struct {
	Raw  string
	Kind MentionKind
}

type MentionKind int

const (
	MentionImage MentionKind = iota // ![](path)
	MentionLink                     // [name](path)
	MentionCode                     // `/absolute/path`
	MentionWrite                    // a Write call's file_path
)

var (
	// Fenced code is not prose: links and paths inside it are examples, not files.
	fence = regexp.MustCompile("(?ms)^ {0,3}(```|~~~).*?(^ {0,3}(```|~~~)[^\\n]*$|\\z)")
	// [text](target "title"), the target bare or in <>.
	mdLink   = regexp.MustCompile(`(!?)\[((?:[^\[\]\n]|\[[^\[\]\n]*\])*)\]\(\s*(<[^>\n]+>|[^()\s]+)(?:\s+(?:"[^"\n]*"|'[^'\n]*'))?\s*\)`)
	code     = regexp.MustCompile("`([^`\n]+)`")
	scheme   = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*:`)
	lineSuf  = regexp.MustCompile(`:\d+(?::\d+)?$`)
	filePath = regexp.MustCompile(`"file_path"\s*:\s*("(?:[^"\\]|\\.)*")`)
)

// Detect is the files a block names, in order, each once: in what you or the agent said,
// Markdown images and links and inline code that is an absolute path (outside fenced code); in a
// Write call, its file_path. Nothing is looked up here.
func Detect(b api.Block) []Mention {
	var out []Mention
	seen := map[string]bool{}
	add := func(raw string, k MentionKind) {
		if raw != "" && !seen[raw] {
			seen[raw] = true
			out = append(out, Mention{Raw: raw, Kind: k})
		}
	}
	switch b.Kind {
	case api.BlockKindUser, api.BlockKindAssistant:
		text := fence.ReplaceAllString(b.Text, "")
		for _, m := range mdLink.FindAllStringSubmatch(text, -1) {
			k := MentionLink
			if m[1] == "!" {
				k = MentionImage
			}
			add(strings.TrimSuffix(strings.TrimPrefix(m[3], "<"), ">"), k)
		}
		for _, m := range code.FindAllStringSubmatch(text, -1) {
			p := strings.TrimSpace(m[1])
			if strings.HasPrefix(p, "/") || strings.HasPrefix(p, "~/") {
				add(p, MentionCode)
			}
		}
	case api.BlockKindTool:
		if b.Name != "Write" {
			break
		}
		// The input may be cut short; file_path comes first.
		if m := filePath.FindStringSubmatch(b.Input); m != nil {
			var p string
			if json.Unmarshal([]byte(m[1]), &p) == nil {
				add(p, MentionWrite)
			}
		}
	}
	return out
}

// Locate is the absolute path a mention means, if it is a regular file that exists: links may
// be relative to cwd (and have a #fragment), file:// URLs are paths, other URLs are not files;
// inline code must be absolute (or ~/), and may end in :line[:column].
func Locate(m Mention, cwd string) (string, bool) {
	home, _ := os.UserHomeDir()
	abs := func(p string) string {
		if rest, ok := strings.CutPrefix(p, "~/"); ok && home != "" {
			p = filepath.Join(home, rest)
		}
		if !filepath.IsAbs(p) {
			if cwd == "" {
				return ""
			}
			p = filepath.Join(cwd, p)
		}
		return filepath.Clean(p)
	}
	var tries []string
	switch m.Kind {
	case MentionImage, MentionLink:
		raw := m.Raw
		if rest, ok := strings.CutPrefix(raw, "file://"); ok {
			raw = rest
		} else if scheme.MatchString(raw) || strings.HasPrefix(raw, "#") || strings.HasPrefix(raw, "//") {
			return "", false
		}
		if i := strings.IndexAny(raw, "#?"); i >= 0 {
			tries = append(tries, raw)
			raw = raw[:i]
		}
		tries = append(tries, raw)
		if strings.Contains(raw, "%") {
			if u, err := url.PathUnescape(raw); err == nil {
				tries = append(tries, u)
			}
		}
	case MentionCode:
		tries = append(tries, m.Raw)
		if lineSuf.MatchString(m.Raw) {
			tries = append(tries, lineSuf.ReplaceAllString(m.Raw, ""))
		}
	case MentionWrite:
		if !filepath.IsAbs(m.Raw) {
			return "", false
		}
		tries = append(tries, m.Raw)
	}
	for _, t := range tries {
		if t == "" {
			continue
		}
		p := abs(t)
		if p == "" {
			continue
		}
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
			return p, true
		}
	}
	return "", false
}
