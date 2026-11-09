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

	"github.com/kumoSleeping/iridium/internal/agents"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/files"
)

// A reply's files (plan "回复里交文件"): its lines [文件] `/absolute/path` (files.FileLines) that
// exist, and its file links (ir:<id>, files.Links) still given; they leave the reply's words, the
// files going as themselves. Each goes to the plugin as a ref good only for that plugin and session,
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

// reply is md as a plugin gets it, with the files it gives.
func (s *Service) reply(plugin, session, md string) api.ChatReply {
	var out []api.ChatFile
	given := map[string]bool{}
	for _, path := range files.FileLines(md, false) {
		if !filepath.IsAbs(path) || denied(path) {
			continue
		}
		st, err := os.Stat(path)
		if err != nil || !st.Mode().IsRegular() {
			continue
		}
		given[path] = true
		out = append(out, api.ChatFile{Name: filepath.Base(path), Size: st.Size(), Type: typeOf(path), Ref: s.ref(plugin, session, path)})
	}
	md = files.DropFileLines(md, given)
	linked := map[string]bool{}
	for _, id := range files.Links(md) {
		if s.o.Linked == nil {
			break
		}
		path, err := s.o.Linked(id)
		if err != nil {
			continue
		}
		st, err := os.Stat(path)
		if err != nil {
			continue
		}
		linked[id] = true
		out = append(out, api.ChatFile{Name: filepath.Base(path), Size: st.Size(), Type: typeOf(path), Ref: s.ref(plugin, session, linkPrefix+id)})
	}
	md = files.DropLinks(md, linked)
	return api.ChatReply{Markdown: md, Text: plain(md), Files: out}
}

func typeOf(path string) string {
	if typ := mime.TypeByExtension(filepath.Ext(path)); typ != "" {
		return typ
	}
	return "application/octet-stream"
}

// linkPrefix marks a ref's path as a file link's id: read where the files service keeps it.
const linkPrefix = "ir:"

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
	path := r.P
	if id, ok := strings.CutPrefix(path, linkPrefix); ok {
		if s.o.Linked == nil {
			return "", fmt.Errorf("%w: no such file", ErrNotFound)
		}
		if path, err = s.o.Linked(id); err != nil {
			return "", fmt.Errorf("%w: no such file", ErrNotFound)
		}
	} else if denied(path) {
		return "", fmt.Errorf("%w: no such file", ErrNotFound)
	}
	if err := s.guard().FileOut(c, r.S); err != nil {
		return "", err
	}
	return path, nil
}

// denied: paths never given out, whatever a reply says (plan "文件传输"): ~/.ssh, the agents'
// own directories, Iridium's state and config. Symlinks are resolved first.
func denied(path string) bool {
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return !errors.Is(err, os.ErrNotExist)
	}
	home, _ := os.UserHomeDir()
	dirs := []string{".ssh", ".config/iridium", ".local/state/iridium", ".gnupg"}
	for i, d := range dirs {
		dirs[i] = filepath.Join(home, d)
	}
	for _, dir := range append(dirs, agents.Homes(home, os.Getenv)...) {
		if real == dir || strings.HasPrefix(real, dir+string(filepath.Separator)) {
			return true
		}
	}
	return false
}
