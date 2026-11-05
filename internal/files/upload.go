package files

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/pathsafe"
)

// MaxChunk is the most one PUT may carry. Clients pick their chunk size (8 to 50 MB by the speed
// they measure); this leaves room above.
const MaxChunk = 64 << 20

// upload is one upload, its record kept in the state directory so it can go on after a restart.
// The temporary file's length is how much has been received.
type upload struct {
	ID      string `json:"id"`
	Session string `json:"session"`
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	Dir     string `json:"dir"`
	Created int64  `json:"created"`
	Done    bool   `json:"done,omitempty"`
	Path    string `json:"path,omitempty"`
	Sha256  string `json:"sha256,omitempty"`
	Mtime   int64  `json:"mtime,omitempty"` // the finished file's, in ns: cleanup removes only that

	mu sync.Mutex
}

func (u *upload) temp() string { return filepath.Join(u.Dir, "."+u.ID+".part") }

func (s *Service) recordDir() string { return filepath.Join(s.o.State, "uploads", "records") }

func (s *Service) loadUploads() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loaded {
		return
	}
	s.loaded = true
	paths, _ := filepath.Glob(filepath.Join(s.recordDir(), "*.json"))
	for _, p := range paths {
		b, err := os.ReadFile(p)
		var u upload
		if err == nil {
			err = json.Unmarshal(b, &u)
		}
		if err != nil || u.ID+".json" != filepath.Base(p) {
			s.o.Logf("files: skipping %s: %v", p, err)
			continue
		}
		s.uploads[u.ID] = &u
	}
}

func (s *Service) save(u *upload) error {
	if err := os.MkdirAll(s.recordDir(), 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(u)
	if err != nil {
		return err
	}
	path := filepath.Join(s.recordDir(), u.ID+".json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (s *Service) upload(id string) (*upload, error) {
	s.loadUploads()
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.uploads[id]
	if u == nil {
		return nil, ErrNoUpload
	}
	return u, nil
}

// state is the upload as the API gives it. Done, Path and Sha256 change under s.mu (with u.mu
// held too), so they are read under it.
func (s *Service) state(u *upload) api.Upload {
	s.mu.Lock()
	done, path, sum := u.Done, u.Path, u.Sha256
	s.mu.Unlock()
	out := api.Upload{ID: u.ID, Session: u.Session, Name: u.Name, Size: u.Size, Done: done, Max: s.o.MaxFile}
	if done {
		out.Received = u.Size
		if info, err := s.Info(path); err == nil {
			info.Ref, info.Sha256 = s.ref(u.Session, path), sum
			out.File = &info
		}
	} else if st, err := os.Stat(u.temp()); err == nil {
		out.Received = st.Size()
	}
	return out
}

// UploadDir is where session's uploads go: its working directory's .iridium/uploads/ (with a
// .gitignore of *), or the state directory's when that cannot be written, is denied, or is $HOME.
func (s *Service) UploadDir(session string) (string, error) {
	cwd, err := s.o.Sessions.Cwd(session)
	if err != nil {
		return "", err
	}
	if d, ok := s.cwdUploads(cwd); ok {
		return d, nil
	}
	d := filepath.Join(UploadsFallback(s.o.State), session)
	if err := os.MkdirAll(d, 0o700); err != nil {
		return "", err
	}
	return d, nil
}

func (s *Service) cwdUploads(cwd string) (string, bool) {
	if cwd == "" || !filepath.IsAbs(cwd) {
		return "", false
	}
	home, _ := os.UserHomeDir()
	if rc, err := pathsafe.Resolve(cwd); err != nil || (home != "" && (filepath.Clean(cwd) == filepath.Clean(home) || rc == filepath.Clean(home))) {
		return "", false
	}
	d := filepath.Join(cwd, ".iridium", "uploads")
	if _, err := s.o.Guard.Check(d); err != nil {
		return "", false
	}
	if err := os.MkdirAll(d, 0o755); err != nil {
		return "", false
	}
	gi := filepath.Join(d, ".gitignore")
	if f, err := os.OpenFile(gi, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644); err == nil {
		_, err = f.WriteString("*\n")
		if cerr := f.Close(); err != nil || cerr != nil {
			return "", false
		}
	} else if !errors.Is(err, os.ErrExist) {
		return "", false
	}
	probe, err := os.CreateTemp(d, ".probe-*")
	if err != nil {
		return "", false
	}
	probe.Close()
	os.Remove(probe.Name())
	return d, true
}

// CleanName is name made safe as a file name: its last element, no control characters, at
// most 200 bytes; "file" if nothing is left.
func CleanName(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = filepath.Base(strings.TrimRight(name, "/"))
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '/' {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	for len(name) > 200 {
		ext := filepath.Ext(name)
		if len(ext) > 20 {
			ext = ""
		}
		r := []rune(strings.TrimSuffix(name, ext))
		name = string(r[:len(r)-1]) + ext
	}
	if name == "" || name == "." || name == ".." {
		return "file"
	}
	return name
}

// CreateUpload starts an upload of size bytes named name for session.
func (s *Service) CreateUpload(session string, req api.NewUpload) (api.Upload, error) {
	if req.Size < 0 {
		return api.Upload{}, fmt.Errorf("%w: a negative size", ErrBad)
	}
	if req.Size > s.o.MaxFile {
		return api.Upload{}, fmt.Errorf("%w: %d bytes, at most %d", ErrTooLarge, req.Size, s.o.MaxFile)
	}
	dir, err := s.UploadDir(session)
	if err != nil {
		return api.Upload{}, err
	}
	u := &upload{ID: randomID(), Session: session, Name: CleanName(req.Name), Size: req.Size, Dir: dir, Created: time.Now().UnixMilli()}
	f, err := os.OpenFile(u.temp(), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return api.Upload{}, err
	}
	f.Close()
	if err := s.save(u); err != nil {
		os.Remove(u.temp())
		return api.Upload{}, err
	}
	s.loadUploads()
	s.mu.Lock()
	s.uploads[u.ID] = u
	s.mu.Unlock()
	return s.state(u), nil
}

// UploadState is how far the upload is.
func (s *Service) UploadState(id string) (api.Upload, error) {
	u, err := s.upload(id)
	if err != nil {
		return api.Upload{}, err
	}
	return s.state(u), nil
}

// Put writes a chunk at offset, which must be what the upload has received. A chunk cut off
// midway keeps what arrived: ask the state and go on from there. length: the chunk's declared
// length, -1 if not known.
func (s *Service) Put(id string, offset int64, body io.Reader, length int64) (api.Upload, error) {
	u, err := s.upload(id)
	if err != nil {
		return api.Upload{}, err
	}
	if !u.mu.TryLock() {
		return s.state(u), ErrBusy
	}
	defer u.mu.Unlock()
	if u.Done {
		return s.state(u), ErrDone
	}
	f, err := os.OpenFile(u.temp(), os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		return api.Upload{}, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return api.Upload{}, err
	}
	if offset != st.Size() {
		return s.state(u), ErrOffset
	}
	left := u.Size - offset
	if length > left || length > MaxChunk {
		return s.state(u), ErrTooLong
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return api.Upload{}, err
	}
	limit := min(left, MaxChunk)
	n, err := io.Copy(f, io.LimitReader(body, limit))
	if err != nil {
		return s.state(u), err
	}
	if n == limit {
		var one [1]byte
		if k, _ := body.Read(one[:]); k > 0 {
			return s.state(u), ErrTooLong
		}
	}
	return s.state(u), nil
}

// Complete checks the whole file's sha256 and moves it to its name in the upload directory,
// never over a file there (a name taken gets " (2)" and so on). A mismatch discards it.
func (s *Service) Complete(id, sum string) (api.Upload, error) {
	u, err := s.upload(id)
	if err != nil {
		return api.Upload{}, err
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.Done {
		return s.state(u), nil
	}
	sum = strings.ToLower(strings.TrimSpace(sum))
	if len(sum) != 64 {
		return s.state(u), fmt.Errorf("%w: sha256 must be 64 hex digits", ErrBad)
	}
	f, err := os.Open(u.temp())
	if err != nil {
		return api.Upload{}, err
	}
	h := sha256.New()
	n, err := io.Copy(h, f)
	f.Close()
	if err != nil {
		return api.Upload{}, err
	}
	if n != u.Size {
		return s.state(u), ErrIncomplete
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != sum {
		os.Remove(u.temp())
		return s.state(u), ErrChecksum
	}
	path, err := place(u.temp(), filepath.Join(u.Dir, u.Name))
	if err != nil {
		return s.state(u), err
	}
	st, err := os.Stat(path)
	if err != nil {
		return api.Upload{}, err
	}
	s.mu.Lock()
	u.Done, u.Path, u.Sha256, u.Mtime = true, path, sum, st.ModTime().UnixNano()
	s.mu.Unlock()
	if err := s.save(u); err != nil {
		s.o.Logf("files: upload %s: %v", u.ID, err)
	}
	s.register(u.Session, path)
	s.remember(sumKey{path, st.Size(), st.ModTime().UnixNano()}, sum)
	return s.state(u), nil
}

// Cancel drops an upload not complete, and what it received.
func (s *Service) Cancel(id string) error {
	u, err := s.upload(id)
	if err != nil {
		return err
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.Done {
		return ErrDone
	}
	os.Remove(u.temp())
	os.Remove(filepath.Join(s.recordDir(), u.ID+".json"))
	s.mu.Lock()
	delete(s.uploads, id)
	s.mu.Unlock()
	return nil
}

// place moves tmp to want, or to want with " (2)", " (3)"… before its extension where taken.
// A hard link is atomic and never replaces a file; where links are not possible, the name is
// taken with O_EXCL first and the rename lands on that empty file.
func place(tmp, want string) (string, error) {
	dir, base := filepath.Dir(want), filepath.Base(want)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	for i := 1; i < 10000; i++ {
		p := want
		if i > 1 {
			p = filepath.Join(dir, fmt.Sprintf("%s (%d)%s", stem, i, ext))
		}
		err := os.Link(tmp, p)
		if err == nil {
			os.Remove(tmp)
			return p, nil
		}
		if errors.Is(err, os.ErrExist) {
			continue
		}
		f, cerr := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(cerr, os.ErrExist) {
			continue
		}
		if cerr != nil {
			return "", cerr
		}
		f.Close()
		if err := os.Rename(tmp, p); err != nil {
			os.Remove(p)
			return "", err
		}
		return p, nil
	}
	return "", ErrExists
}

// Janitor removes uploads older than Keep, and thumbnails not made for as long, every hour
// until ctx ends.
func (s *Service) Janitor(ctx context.Context) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		s.Sweep(time.Now())
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Sweep removes what is past keeping at now: uploads (the file too, unless it changed since)
// and their records, and old thumbnails.
func (s *Service) Sweep(now time.Time) {
	keep := s.o.Keep
	if keep <= 0 {
		return
	}
	s.loadUploads()
	s.mu.Lock()
	var old []*upload
	for _, u := range s.uploads {
		if now.Sub(time.UnixMilli(u.Created)) > keep {
			old = append(old, u)
		}
	}
	s.mu.Unlock()
	for _, u := range old {
		if !u.mu.TryLock() {
			continue
		}
		os.Remove(u.temp())
		if u.Done {
			if st, err := os.Stat(u.Path); err == nil && st.Size() == u.Size && st.ModTime().UnixNano() == u.Mtime {
				os.Remove(u.Path)
			}
		}
		os.Remove(filepath.Join(s.recordDir(), u.ID+".json"))
		s.mu.Lock()
		delete(s.uploads, u.ID)
		s.mu.Unlock()
		u.mu.Unlock()
	}
	thumbs, _ := os.ReadDir(filepath.Join(s.o.State, "thumbs"))
	for _, e := range thumbs {
		if info, err := e.Info(); err == nil && now.Sub(info.ModTime()) > keep {
			os.Remove(filepath.Join(s.o.State, "thumbs", e.Name()))
		}
	}
}
