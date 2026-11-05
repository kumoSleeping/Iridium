// Package files is Iridium's file transfer (plan "文件传输"): the files a session's transcript
// names, each given a reference good only for that session; serving them with Range, ETag and
// sha256, and thumbnails; resumable uploads into the session's working directory; attachments for
// messages; and, for the local ir, copying and listing by path. Every path goes through a
// pathsafe.Guard.
package files

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/pathsafe"
)

var (
	ErrNoRef      = errors.New("no such file reference")
	ErrNotFile    = errors.New("not a regular file")
	ErrExists     = errors.New("the target exists; files are never overwritten")
	ErrNoUpload   = errors.New("no such upload")
	ErrOffset     = errors.New("the chunk's offset is not where the upload is")
	ErrBusy       = errors.New("another chunk of this upload is being written")
	ErrIncomplete = errors.New("the upload has not received every byte")
	ErrChecksum   = errors.New("sha256 mismatch; the upload was discarded, start it again")
	ErrTooLarge   = errors.New("file too large")
	ErrTooLong    = errors.New("the chunk goes past the upload's size")
	ErrDone       = errors.New("the upload is complete")
	ErrNotImage   = errors.New("not an image that can be thumbnailed")
	ErrBad        = errors.New("bad request")
)

// Sessions is what the service needs of the session manager.
type Sessions interface {
	// Cwd is the session's working directory.
	Cwd(id string) (string, error)
	// After is the page of the session's transcript that begins at at (MessagePage.End: where
	// the next begins).
	After(id string, at int64) (api.MessagePage, error)
}

type Options struct {
	State    string // Iridium's state directory: the key, upload records, thumbnails, the uploads fallback
	Guard    *pathsafe.Guard
	Sessions Sessions
	Keep     time.Duration // uploads older are removed; 0 keeps them
	MaxFile  int64         // the largest upload; 0 is 2 GB
	Logf     func(string, ...any)
}

type Service struct {
	o   Options
	key []byte

	mu      sync.Mutex
	refs    map[string]map[string]string // session → ref → path
	scanned map[string]int64             // session → how far its transcript was scanned
	scanMu  map[string]*sync.Mutex
	sums    map[sumKey]string
	uploads map[string]*upload
	loaded  bool
	copies  map[string]*copyJob
}

type sumKey struct {
	path  string
	size  int64
	mtime int64
}

// UploadsFallback is where uploads go, under the state directory, when the session's working
// directory cannot take them. The Guard must except it from the state directory's deny.
func UploadsFallback(state string) string { return filepath.Join(state, "uploads", "files") }

func New(o Options) *Service {
	if o.MaxFile <= 0 {
		o.MaxFile = 2 << 30
	}
	if o.Logf == nil {
		o.Logf = log.Printf
	}
	if o.Guard == nil {
		o.Guard = pathsafe.New(nil, pathsafe.Deny(o.State, ""), []string{UploadsFallback(o.State)})
	}
	s := &Service{o: o, refs: map[string]map[string]string{}, scanned: map[string]int64{}, scanMu: map[string]*sync.Mutex{}, sums: map[sumKey]string{}, uploads: map[string]*upload{}, copies: map[string]*copyJob{}}
	s.key = s.loadKey()
	return s
}

// loadKey is the key refs are made with, kept in the state directory so refs outlive a restart.
func (s *Service) loadKey() []byte {
	path := filepath.Join(s.o.State, "files.key")
	if b, err := os.ReadFile(path); err == nil && len(b) == 32 {
		return b
	}
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	if err := os.MkdirAll(s.o.State, 0o700); err == nil {
		err = os.WriteFile(path, key, 0o600)
		if err != nil {
			s.o.Logf("files: %v; references will not outlive a restart", err)
		}
	}
	return key
}

var refEncoding = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

// ref is path's reference in session: the same each time, and telling nothing of the path.
func (s *Service) ref(session, path string) string {
	m := hmac.New(sha256.New, s.key)
	m.Write([]byte(session))
	m.Write([]byte{0})
	m.Write([]byte(path))
	return refEncoding.EncodeToString(m.Sum(nil)[:10])
}

// register gives path a reference in session.
func (s *Service) register(session, path string) string {
	r := s.ref(session, path)
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.refs[session]
	if m == nil {
		m = map[string]string{}
		s.refs[session] = m
	}
	m[r] = path
	return r
}

func (s *Service) cached(session, ref string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.refs[session][ref]
	return p, ok
}

// Lookup is the path of a reference of session: one given before, an upload's, or one the
// transcript names (read on from where it was last read). The path is checked again.
func (s *Service) Lookup(session, ref string) (string, error) {
	p, ok := s.cached(session, ref)
	if !ok {
		s.loadUploads()
		s.mu.Lock()
		for _, u := range s.uploads {
			if u.Session == session && u.Done && s.ref(session, u.Path) == ref {
				p, ok = u.Path, true
			}
		}
		s.mu.Unlock()
		if ok {
			s.register(session, p)
		}
	}
	if !ok {
		if err := s.scan(session); err != nil {
			return "", err
		}
		p, ok = s.cached(session, ref)
	}
	if !ok {
		return "", ErrNoRef
	}
	if _, err := s.o.Guard.Check(p); err != nil {
		return "", err
	}
	return p, nil
}

// scan reads the session's transcript from where it was last read, giving every file it names a
// reference.
func (s *Service) scan(session string) error {
	s.mu.Lock()
	mu := s.scanMu[session]
	if mu == nil {
		mu = &sync.Mutex{}
		s.scanMu[session] = mu
	}
	s.mu.Unlock()
	mu.Lock()
	defer mu.Unlock()
	cwd, err := s.o.Sessions.Cwd(session)
	if err != nil {
		return err
	}
	s.mu.Lock()
	at := s.scanned[session]
	s.mu.Unlock()
	for {
		p, err := s.o.Sessions.After(session, at)
		if err != nil {
			return err
		}
		for _, b := range p.Blocks {
			s.files(session, cwd, b)
		}
		if p.End <= at {
			break
		}
		at = p.End
	}
	s.mu.Lock()
	s.scanned[session] = at
	s.mu.Unlock()
	return nil
}

// files is the files block b names that exist and may be served, each given a reference.
func (s *Service) files(session, cwd string, b api.Block) []api.FileRef {
	var out []api.FileRef
	for _, m := range Detect(b) {
		p, ok := Locate(m, cwd)
		if !ok {
			continue
		}
		if _, err := s.o.Guard.Check(p); err != nil {
			continue
		}
		info, err := s.Info(p)
		if err != nil {
			continue
		}
		info.Ref = s.register(session, p)
		out = append(out, info)
	}
	return out
}

// Annotate fills in each block's Files: what it names that exists, with references.
func (s *Service) Annotate(session string, page *api.MessagePage) {
	cwd, _ := s.o.Sessions.Cwd(session)
	for i := range page.Blocks {
		page.Blocks[i].Files = s.files(session, cwd, page.Blocks[i])
	}
}

// Attach makes a local file (the local ir's -a) an attachment of session.
func (s *Service) Attach(session, path string) (api.FileRef, error) {
	if _, err := s.o.Sessions.Cwd(session); err != nil {
		return api.FileRef{}, err
	}
	if !filepath.IsAbs(path) {
		return api.FileRef{}, fmt.Errorf("%w: the path must be absolute", ErrBad)
	}
	path = filepath.Clean(path)
	if _, err := s.o.Guard.Check(path); err != nil {
		return api.FileRef{}, err
	}
	info, err := s.Info(path)
	if err != nil {
		return api.FileRef{}, err
	}
	info.Ref = s.register(session, path)
	return info, nil
}

// Info is what is known of the file at path without reading it whole: no Ref, no Sha256.
func (s *Service) Info(path string) (api.FileRef, error) {
	st, err := os.Stat(path)
	if err != nil {
		return api.FileRef{}, err
	}
	if !st.Mode().IsRegular() {
		return api.FileRef{}, ErrNotFile
	}
	typ := sniff(path)
	k := kind(typ)
	return api.FileRef{
		Name: filepath.Base(path), Path: path, Size: st.Size(), Mime: typ, Kind: k,
		Thumb: thumbable(typ), Mtime: st.ModTime().UnixMilli(),
	}, nil
}

// sniff is the file's media type: by its extension, else by its first 512 bytes.
func sniff(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	t := extTypes[ext]
	if t == "" {
		t = mime.TypeByExtension(ext)
	}
	if t != "" && t != "application/octet-stream" {
		return t
	}
	f, err := os.Open(path)
	if err != nil {
		return "application/octet-stream"
	}
	defer f.Close()
	buf := make([]byte, 512)
	n, _ := io.ReadFull(f, buf)
	return http.DetectContentType(buf[:n])
}

// extTypes are what mime.TypeByExtension may not know on a bare system.
var extTypes = map[string]string{
	".md": "text/markdown; charset=utf-8", ".go": "text/plain; charset=utf-8", ".txt": "text/plain; charset=utf-8",
	".log": "text/plain; charset=utf-8", ".py": "text/plain; charset=utf-8", ".ts": "text/plain; charset=utf-8",
	".tsx": "text/plain; charset=utf-8", ".rs": "text/plain; charset=utf-8", ".sh": "text/plain; charset=utf-8",
	".yaml": "text/plain; charset=utf-8", ".yml": "text/plain; charset=utf-8", ".toml": "text/plain; charset=utf-8",
	".json": "application/json", ".csv": "text/csv; charset=utf-8",
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif", ".webp": "image/webp",
	".svg": "image/svg+xml", ".avif": "image/avif", ".bmp": "image/bmp", ".ico": "image/x-icon",
	".mp4": "video/mp4", ".webm": "video/webm", ".mov": "video/quicktime", ".mkv": "video/x-matroska",
	".mp3": "audio/mpeg", ".wav": "audio/wav", ".ogg": "audio/ogg", ".m4a": "audio/mp4", ".flac": "audio/flac", ".opus": "audio/ogg",
	".pdf": "application/pdf", ".zip": "application/zip", ".gz": "application/gzip", ".tar": "application/x-tar",
}

func baseType(t string) string {
	b, _, _ := strings.Cut(t, ";")
	return strings.TrimSpace(strings.ToLower(b))
}

func kind(t string) api.FileKind {
	switch b := baseType(t); {
	case strings.HasPrefix(b, "image/"):
		return api.FileKindImage
	case strings.HasPrefix(b, "video/"):
		return api.FileKindVideo
	case strings.HasPrefix(b, "audio/"):
		return api.FileKindAudio
	}
	return api.FileKindFile
}

func thumbable(t string) bool {
	switch baseType(t) {
	case "image/png", "image/jpeg", "image/gif", "image/webp", "image/svg+xml":
		return true
	}
	return false
}

// Sum is the file's sha256 (hex), remembered by path, size and modification time.
func (s *Service) Sum(path string) (string, error) {
	st, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	k := sumKey{path, st.Size(), st.ModTime().UnixNano()}
	s.mu.Lock()
	sum, ok := s.sums[k]
	s.mu.Unlock()
	if ok {
		return sum, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	sum = hex.EncodeToString(h.Sum(nil))
	s.remember(k, sum)
	return sum, nil
}

func (s *Service) knownSum(path string, st os.FileInfo) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sum, ok := s.sums[sumKey{path, st.Size(), st.ModTime().UnixNano()}]
	return sum, ok
}

func (s *Service) remember(k sumKey, sum string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.sums) > 10000 {
		clear(s.sums)
	}
	s.sums[k] = sum
}

func randomID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
