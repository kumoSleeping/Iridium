package files

import (
	"encoding/json"
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
	MentionFile  MentionKind = iota // a line [文件] `path` (or, in what you sent, [附件] `path`)
	MentionWrite                    // a Write call's file_path
)

var (
	// Fenced code is not prose: what is inside it is an example, not a file.
	fence = regexp.MustCompile("(?ms)^ {0,3}(```|~~~).*?(^ {0,3}(```|~~~)[^\\n]*$|\\z)")
	// A line that gives a file, and nothing else: its word and the path in inline code.
	fileLine = regexp.MustCompile("(?m)^[ \\t]*\\[(文件|附件)\\] `([^`\\n]+)`[ \\t]*$")
	filePath = regexp.MustCompile(`"file_path"\s*:\s*("(?:[^"\\]|\\.)*")`)
)

// FileLines is the paths text gives as files, in order, each once (plan "回复里交文件"): the lines
// [文件] `path` outside fenced code, which the agent writes to hand you a file; in what you sent
// (user), also [附件] `path`, which Iridium writes for an attachment (attach.go). Anything else
// that looks like a path, a link or a picture is only words.
func FileLines(text string, user bool) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range fileLine.FindAllStringSubmatch(fence.ReplaceAllString(text, ""), -1) {
		p := strings.TrimSpace(m[2])
		if (m[1] == "附件" && !user) || p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// Detect is the files a block names, in order, each once: in what you or the agent said, its
// FileLines; in a Write call, its file_path (a ref only, not shown). Nothing is looked up here.
func Detect(b api.Block) []Mention {
	var out []Mention
	switch b.Kind {
	case api.BlockKindUser, api.BlockKindAssistant:
		for _, p := range FileLines(b.Text, b.Kind == api.BlockKindUser) {
			out = append(out, Mention{Raw: p, Kind: MentionFile})
		}
	case api.BlockKindTool:
		if b.Name != "Write" {
			break
		}
		// The input may be cut short; file_path comes first.
		if m := filePath.FindStringSubmatch(b.Input); m != nil {
			var p string
			if json.Unmarshal([]byte(m[1]), &p) == nil && p != "" {
				out = append(out, Mention{Raw: p, Kind: MentionWrite})
			}
		}
	}
	return out
}

// Locate is the absolute path a mention means, if it is a regular file that exists: a file line's
// path is absolute or ~/, a Write's absolute.
func Locate(m Mention) (string, bool) {
	p := m.Raw
	if rest, ok := strings.CutPrefix(p, "~/"); ok && m.Kind == MentionFile {
		home, _ := os.UserHomeDir()
		if home == "" {
			return "", false
		}
		p = filepath.Join(home, rest)
	}
	if !filepath.IsAbs(p) {
		return "", false
	}
	p = filepath.Clean(p)
	if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
		return p, true
	}
	return "", false
}

// DropFileLines is text without the file lines given (by their path as written), outside fenced
// code: what shows beside the files, which show as themselves.
func DropFileLines(text string, paths map[string]bool) string {
	if len(paths) == 0 {
		return text
	}
	drop := func(s string) string {
		return fileLine.ReplaceAllStringFunc(s, func(line string) string {
			if m := fileLine.FindStringSubmatch(line); m != nil && paths[strings.TrimSpace(m[2])] {
				return ""
			}
			return line
		})
	}
	var b strings.Builder
	at := 0
	for _, f := range fence.FindAllStringIndex(text, -1) {
		b.WriteString(drop(text[at:f[0]]))
		b.WriteString(text[f[0]:f[1]])
		at = f[1]
	}
	b.WriteString(drop(text[at:]))
	return strings.TrimSpace(blankRun.ReplaceAllString(b.String(), "\n\n"))
}

var blankRun = regexp.MustCompile(`\n{3,}`)
