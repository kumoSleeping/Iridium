package chat

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/kumoSleeping/iridium/internal/api"
)

// A reply's files (plan "回复里认…"): `![](path)`, `[name](path)`, and absolute paths in inline
// code that exist. Each goes to the plugin as a ref good only for that plugin and session,
// signed with a key kept in the state directory, so refs outlive a restart, and only paths a
// reply named ever get one.

const fileRefKey = "chat-ref.key"

func refKey(dir string) ([]byte, error) {
	path := filepath.Join(dir, fileRefKey)
	if b, err := os.ReadFile(path); err == nil && len(b) == 32 {
		return b, nil
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return b, os.WriteFile(path, b, 0o600)
}

var (
	mdLink     = regexp.MustCompile(`!?\[([^\]]*)\]\(([^)\s]+)\)`)
	inlineCode = regexp.MustCompile("`([^`\n]+)`")
	fence      = regexp.MustCompile("(?m)^```[^\n]*\n?")
	heading    = regexp.MustCompile(`(?m)^#{1,6}\s+`)
	emphasis   = regexp.MustCompile(`(\*\*|__)(.+?)(\*\*|__)`)
)

// plain is md with its marks taken off: fences, headings, emphasis, code ticks; a link becomes
// its text.
func plain(md string) string {
	s := fence.ReplaceAllString(md, "")
	s = heading.ReplaceAllString(s, "")
	s = mdLink.ReplaceAllString(s, "$1")
	s = emphasis.ReplaceAllString(s, "$2")
	s = inlineCode.ReplaceAllString(s, "$1")
	return strings.TrimSpace(s)
}

// reply is md as a plugin gets it, with the files it names.
func (s *Service) reply(plugin, session, md string) api.ChatReply {
	r := api.ChatReply{Markdown: md, Text: plain(md)}
	seen := map[string]bool{}
	add := func(name, path string) {
		if !filepath.IsAbs(path) || seen[path] || denied(path) {
			return
		}
		st, err := os.Stat(path)
		if err != nil || !st.Mode().IsRegular() {
			return
		}
		seen[path] = true
		if name == "" {
			name = filepath.Base(path)
		}
		typ := mime.TypeByExtension(filepath.Ext(path))
		if typ == "" {
			typ = "application/octet-stream"
		}
		r.Files = append(r.Files, api.ChatFile{Name: name, Size: st.Size(), Type: typ, Ref: s.ref(plugin, session, path)})
	}
	for _, m := range mdLink.FindAllStringSubmatch(md, -1) {
		add(m[1], m[2])
	}
	for _, m := range inlineCode.FindAllStringSubmatch(md, -1) {
		add("", m[1])
	}
	return r
}

type refBody struct {
	P string `json:"p"` // the path
	S string `json:"s"` // the session
	C string `json:"c"` // the plugin
}

func (s *Service) ref(plugin, session, path string) string {
	b, _ := json.Marshal(refBody{P: path, S: session, C: plugin})
	body := base64.RawURLEncoding.EncodeToString(b)
	return body + "." + s.sign(body)
}

func (s *Service) sign(body string) string {
	m := hmac.New(sha256.New, s.key)
	m.Write([]byte(body))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil)[:18])
}

// File is the path ref names, if it is the caller's and the file is still there.
func (s *Service) File(c Caller, ref string) (string, error) {
	body, sig, ok := strings.Cut(ref, ".")
	if !ok || !hmac.Equal([]byte(sig), []byte(s.sign(body))) {
		return "", fmt.Errorf("%w: no such file", ErrNotFound)
	}
	b, err := base64.RawURLEncoding.DecodeString(body)
	var r refBody
	if err != nil || json.Unmarshal(b, &r) != nil || r.C != c.ID {
		return "", fmt.Errorf("%w: no such file", ErrNotFound)
	}
	if denied(r.P) {
		return "", fmt.Errorf("%w: no such file", ErrNotFound)
	}
	if err := s.guard().FileOut(c, r.S); err != nil {
		return "", err
	}
	return r.P, nil
}

// denied: paths never given out, whatever a reply says (plan "文件传输"): ~/.ssh, the agents'
// own directories, Iridium's state and config. Symlinks are resolved first.
func denied(path string) bool {
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return !errors.Is(err, os.ErrNotExist)
	}
	home, _ := os.UserHomeDir()
	for _, d := range []string{".ssh", ".claude", ".codex", ".pi", ".config/iridium", ".local/state/iridium", ".gnupg"} {
		dir := filepath.Join(home, d)
		if real == dir || strings.HasPrefix(real, dir+string(filepath.Separator)) {
			return true
		}
	}
	return false
}
