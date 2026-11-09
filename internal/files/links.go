package files

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
)

// File links (docs/api.md "文件链接"): ir attach <path> gives a file an id, and a reply puts it in
// its words where it belongs, as ![caption](ir:id) (a picture, a video or a sound shows there) or
// [text](ir:id) (a file to download). A file of at most linkCopyMax is copied when attached
// (links/<id>/<name> in the state directory): what the reply showed stays as it was, the original
// changed or gone. A larger one is given where it is, and no longer once it changed. An id is
// random: knowing it is what lets a page show the file, in any session.

// linkCopyMax is the largest file a link copies.
const linkCopyMax = 64 << 20

var (
	ErrChanged = errors.New("the file changed or went away since it was attached")

	// linkRef is an id as a reply writes it.
	linkRef = regexp.MustCompile(`\bir:([a-z2-7]{10})\b`)
	linkID  = regexp.MustCompile(`^[a-z2-7]{10}$`)
)

// link is a file link's record, links/<id>.json.
type link struct {
	ID      string `json:"id"`
	Session string `json:"session,omitempty"` // the session ir attach ran in
	Path    string `json:"path"`              // the file attached
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	Mtime   int64  `json:"mtime"` // ns: the file's when attached
	Sha256  string `json:"sha256,omitempty"`
	Copied  bool   `json:"copied,omitempty"` // links/<id>/<name> is what is given
	At      int64  `json:"at"`               // ms
}

func (s *Service) linkDir() string { return filepath.Join(s.o.State, "links") }

// given is where link l's file is read: its copy, or the file attached if it has not changed.
func (s *Service) given(l link) (string, error) {
	if l.Copied {
		return filepath.Join(s.linkDir(), l.ID, l.Name), nil
	}
	if _, err := s.o.Guard.Check(l.Path); err != nil {
		return "", err
	}
	st, err := os.Stat(l.Path)
	if err != nil || !st.Mode().IsRegular() || st.Size() != l.Size || st.ModTime().UnixNano() != l.Mtime {
		return "", ErrChanged
	}
	return l.Path, nil
}

// linkOf is the record of id.
func (s *Service) linkOf(id string) (link, error) {
	if !linkID.MatchString(id) {
		return link{}, ErrNoRef
	}
	s.mu.Lock()
	l, ok := s.links[id]
	s.mu.Unlock()
	if ok {
		return l, nil
	}
	b, err := os.ReadFile(filepath.Join(s.linkDir(), id+".json"))
	if err != nil || json.Unmarshal(b, &l) != nil || l.ID != id {
		return link{}, ErrNoRef
	}
	s.mu.Lock()
	s.links[id] = l
	s.mu.Unlock()
	return l, nil
}

// Linked is the path file link id is read from, if it is still given.
func (s *Service) Linked(id string) (string, error) {
	l, err := s.linkOf(id)
	if err != nil {
		return "", err
	}
	return s.given(l)
}

// Link gives the file at path (absolute) a link, attached in session ("" if none): the same one
// again while the file is the same.
func (s *Service) Link(session, path string) (api.FileLink, error) {
	if !filepath.IsAbs(path) {
		return api.FileLink{}, fmt.Errorf("%w: the path must be absolute", ErrBad)
	}
	path = filepath.Clean(path)
	if _, err := s.o.Guard.Check(path); err != nil {
		return api.FileLink{}, err
	}
	st, err := os.Stat(path)
	if err != nil {
		return api.FileLink{}, err
	}
	if !st.Mode().IsRegular() {
		return api.FileLink{}, ErrNotFile
	}
	l, ok := s.sameLink(session, path, st)
	if !ok {
		if l, err = s.newLink(session, path, st); err != nil {
			return api.FileLink{}, err
		}
	}
	return s.linkInfo(l)
}

// sameLink is the link session already gave the file at path, as it is now.
func (s *Service) sameLink(session, path string, st os.FileInfo) (link, bool) {
	k := sumKey{path, st.Size(), st.ModTime().UnixNano()}
	s.mu.Lock()
	id, ok := s.linked[session+"\x00"+k.path]
	s.mu.Unlock()
	if !ok {
		return link{}, false
	}
	l, err := s.linkOf(id)
	if err != nil || l.Size != k.size || l.Mtime != k.mtime {
		return link{}, false
	}
	if _, err := s.given(l); err != nil {
		return link{}, false
	}
	return l, true
}

func (s *Service) newLink(session, path string, st os.FileInfo) (link, error) {
	b := make([]byte, 7)
	if _, err := rand.Read(b); err != nil {
		return link{}, err
	}
	l := link{
		ID: refEncoding.EncodeToString(b)[:10], Session: session, Path: path, Name: filepath.Base(path),
		Size: st.Size(), Mtime: st.ModTime().UnixNano(), At: time.Now().UnixMilli(),
	}
	if err := os.MkdirAll(s.linkDir(), 0o700); err != nil {
		return link{}, err
	}
	if st.Size() <= linkCopyMax {
		sum, err := s.copyLink(l)
		if err != nil {
			return link{}, err
		}
		l.Copied, l.Sha256 = true, sum
	}
	j, err := json.Marshal(l)
	if err != nil {
		return link{}, err
	}
	rec := filepath.Join(s.linkDir(), l.ID+".json")
	tmp := rec + ".part"
	if err := os.WriteFile(tmp, j, 0o600); err != nil {
		return link{}, err
	}
	if err := os.Rename(tmp, rec); err != nil {
		os.Remove(tmp)
		return link{}, err
	}
	s.mu.Lock()
	s.links[l.ID] = l
	s.linked[session+"\x00"+path] = l.ID
	s.mu.Unlock()
	return l, nil
}

// copyLink copies l's file to links/<id>/<name>, giving its sha256.
func (s *Service) copyLink(l link) (string, error) {
	dir := filepath.Join(s.linkDir(), l.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	src, err := os.Open(l.Path)
	if err != nil {
		return "", err
	}
	defer src.Close()
	tmp, err := os.CreateTemp(dir, ".part-*")
	if err != nil {
		return "", err
	}
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(tmp, h), io.LimitReader(src, linkCopyMax+1))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), filepath.Join(dir, l.Name))
	}
	if err != nil {
		os.Remove(tmp.Name())
		os.Remove(dir)
		return "", err
	}
	return sumOf(h), nil
}

// linkInfo is link l as ir attach prints it.
func (s *Service) linkInfo(l link) (api.FileLink, error) {
	f, err := s.linkFile(l)
	if err != nil {
		return api.FileLink{}, err
	}
	md := "[" + l.Name + "](ir:" + l.ID + ")"
	if f.Kind != api.FileKindFile {
		md = "!" + md
	}
	return api.FileLink{File: f, Link: "ir:" + l.ID, Markdown: md}, nil
}

// linkFile is link l as a block's Files have it.
func (s *Service) linkFile(l link) (api.FileRef, error) {
	p, err := s.given(l)
	if err != nil {
		return api.FileRef{}, err
	}
	f, err := s.Info(p)
	if err != nil {
		return api.FileRef{}, err
	}
	f.Ref, f.Path, f.Name, f.Sha256 = l.ID, "ir:"+l.ID, l.Name, l.Sha256
	return f, nil
}

// Links is the ids of the file links text has (ir:<id>), in order, each once; fenced code is not
// looked in.
func Links(text string) []string {
	if !strings.Contains(text, "ir:") {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, m := range linkRef.FindAllStringSubmatch(fence.ReplaceAllString(text, ""), -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	return out
}

var (
	linkImage = regexp.MustCompile(`!\[[^\]\n]*\]\(\s*<?ir:([a-z2-7]{10})>?\s*\)`)
	linkText  = regexp.MustCompile(`\[([^\]\n]*)\]\(\s*<?ir:([a-z2-7]{10})>?\s*\)`)
)

// DropLinks is text with the file links of ids taken out, outside fenced code, for where the files
// go as themselves (a chat's reply): a picture's goes, a link leaves its words.
func DropLinks(text string, ids map[string]bool) string {
	if len(ids) == 0 {
		return text
	}
	drop := func(s string) string {
		s = linkImage.ReplaceAllStringFunc(s, func(m string) string {
			if ids[linkImage.FindStringSubmatch(m)[1]] {
				return ""
			}
			return m
		})
		return linkText.ReplaceAllStringFunc(s, func(m string) string {
			if sm := linkText.FindStringSubmatch(m); ids[sm[2]] {
				return sm[1]
			}
			return m
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

// LinkInfo is file link id as a block's Files have it, if it is still given.
func (s *Service) LinkInfo(id string) (api.FileRef, error) {
	l, err := s.linkOf(id)
	if err != nil {
		return api.FileRef{}, err
	}
	return s.linkFile(l)
}

// IsLink: ref is a file link's id.
func IsLink(ref string) bool { return linkID.MatchString(ref) }
