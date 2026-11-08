package files

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"

	"github.com/kumoSleeping/iridium/internal/api"
)

// copyJob is a copy Iridium runs, not the ir that asked for it: it goes on when ir is
// interrupted, and the same copy asked again joins it, or picks up its temporary file.
type copyJob struct {
	done atomic.Int64
	size int64
	fin  chan struct{}
	res  api.CopyProgress
	err  error
}

// Progress is how far the copy is.
func (j *copyJob) Progress() api.CopyProgress {
	return api.CopyProgress{Done: j.done.Load(), Size: j.size}
}

// Done is closed when the copy has ended; Result then says how.
func (j *copyJob) Done() <-chan struct{} { return j.fin }

func (j *copyJob) Result() (api.CopyProgress, error) { return j.res, j.err }

// CopyJob is what Copy gives: progress, then the result.
type CopyJob interface {
	Progress() api.CopyProgress
	Done() <-chan struct{}
	Result() (api.CopyProgress, error)
}

// Copy starts copying the regular file from to to (both absolute), or joins the same copy
// running. to may be a directory, which takes from's name; it must not exist, and its directory
// must. The file is written beside it under a temporary name, then linked into place: never over
// a file. A copy cut off is picked up again from its temporary file while from is unchanged.
func (s *Service) Copy(from, to string) (CopyJob, error) {
	if !filepath.IsAbs(from) || !filepath.IsAbs(to) {
		return nil, fmt.Errorf("%w: paths must be absolute", ErrBad)
	}
	src, err := s.o.Guard.Check(filepath.Clean(from))
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(src)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: %s (directories come later)", ErrNotFile, from)
	}
	to = filepath.Clean(to)
	if dt, err := os.Stat(to); err == nil && dt.IsDir() {
		to = filepath.Join(to, filepath.Base(src))
	}
	if _, err := s.o.Guard.Check(to); err != nil {
		return nil, err
	}
	if _, err := os.Lstat(to); err == nil {
		return nil, fmt.Errorf("%w: %s", ErrExists, to)
	}
	if dt, err := os.Stat(filepath.Dir(to)); err != nil || !dt.IsDir() {
		return nil, fmt.Errorf("%w: no directory %s", ErrBad, filepath.Dir(to))
	}
	k := sha256.Sum256(fmt.Appendf(nil, "%s\x00%s\x00%d\x00%d", src, to, st.Size(), st.ModTime().UnixNano()))
	key := hex.EncodeToString(k[:8])
	s.mu.Lock()
	if j := s.copies[key]; j != nil {
		s.mu.Unlock()
		return j, nil
	}
	j := &copyJob{size: st.Size(), fin: make(chan struct{})}
	s.copies[key] = j
	s.mu.Unlock()
	tmp := filepath.Join(filepath.Dir(to), "."+filepath.Base(to)+"."+key+".part")
	go func() {
		j.res, j.err = s.runCopy(j, src, tmp, to, st)
		s.mu.Lock()
		delete(s.copies, key)
		s.mu.Unlock()
		close(j.fin)
	}()
	return j, nil
}

func (s *Service) runCopy(j *copyJob, src, tmp, to string, st os.FileInfo) (api.CopyProgress, error) {
	in, err := os.Open(src)
	if err != nil {
		return api.CopyProgress{}, err
	}
	defer in.Close()
	out, err := os.OpenFile(tmp, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return api.CopyProgress{}, err
	}
	defer out.Close()
	have, err := out.Seek(0, io.SeekEnd)
	if err != nil {
		return api.CopyProgress{}, err
	}
	if have > st.Size() {
		if err := out.Truncate(0); err != nil {
			return api.CopyProgress{}, err
		}
		have = 0
	}
	h := sha256.New()
	// What an earlier run wrote counts towards the sum.
	if _, err := io.Copy(h, io.NewSectionReader(out, 0, have)); err != nil {
		return api.CopyProgress{}, err
	}
	j.done.Store(have)
	if _, err := in.Seek(have, io.SeekStart); err != nil {
		return api.CopyProgress{}, err
	}
	if _, err := out.Seek(have, io.SeekStart); err != nil {
		return api.CopyProgress{}, err
	}
	if _, err := io.Copy(io.MultiWriter(out, h), &counted{r: io.LimitReader(in, st.Size()-have), n: &j.done}); err != nil {
		return api.CopyProgress{}, err
	}
	if j.done.Load() != st.Size() {
		return api.CopyProgress{}, fmt.Errorf("%s changed while it was copied", src)
	}
	if err := out.Sync(); err != nil {
		return api.CopyProgress{}, err
	}
	_ = out.Chmod(st.Mode().Perm())
	if err := os.Link(tmp, to); err != nil {
		return api.CopyProgress{}, fmt.Errorf("%w: %v", ErrExists, err)
	}
	os.Remove(tmp)
	sum := sumOf(h)
	if ft, err := os.Stat(to); err == nil {
		s.remember(sumKey{to, ft.Size(), ft.ModTime().UnixNano()}, sum)
	}
	return api.CopyProgress{Done: st.Size(), Size: st.Size(), Path: to, Sha256: sum}, nil
}

func sumOf(h hash.Hash) string { return hex.EncodeToString(h.Sum(nil)) }

type counted struct {
	r io.Reader
	n *atomic.Int64
}

func (c *counted) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n.Add(int64(n))
	return n, err
}

// List is one level of the directory at path: name, size, time and type of each entry.
func (s *Service) List(path string) (api.DirListing, error) {
	if !filepath.IsAbs(path) {
		return api.DirListing{}, fmt.Errorf("%w: the path must be absolute", ErrBad)
	}
	path = filepath.Clean(path)
	real, err := s.o.Guard.Check(path)
	if err != nil {
		return api.DirListing{}, err
	}
	ents, err := os.ReadDir(real)
	if err != nil {
		return api.DirListing{}, err
	}
	out := api.DirListing{Path: path, Entries: []api.DirEntry{}}
	for _, e := range ents {
		info, err := e.Info()
		if err != nil {
			continue
		}
		t := "other"
		switch m := info.Mode(); {
		case m.IsRegular():
			t = "file"
		case m.IsDir():
			t = "dir"
		case m&os.ModeSymlink != 0:
			t = "link"
		}
		size := info.Size()
		if t == "dir" {
			size = 0
		}
		out.Entries = append(out.Entries, api.DirEntry{Name: e.Name(), Size: size, Mtime: info.ModTime().UnixMilli(), Type: t})
	}
	return out, nil
}

// Guarded is path, absolute, as the guard lets it be read or written here (the copies across
// machines' side on this one); an error if it may not.
func (s *Service) Guarded(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("%w: paths must be absolute", ErrBad)
	}
	return s.o.Guard.Check(filepath.Clean(path))
}
