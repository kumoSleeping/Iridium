package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/remote"
	"github.com/kumoSleeping/iridium/internal/remote/wire"
)

// remoteWriteMax is the most a copy writes at another machine: in one fs.write, its frame within
// wire.MaxFrame in base64.
const remoteWriteMax = 4 << 20

// listAt is GET /api/files?machine=: one level of a folder at another machine, its own path.
func (s *Server) listAt(w http.ResponseWriter, r *http.Request, machine, path string) {
	mc, hi := s.machineAt(w, machine)
	if mc == nil {
		return
	}
	if path == "" {
		path = hi.Home
	}
	ents, err := mc.List(r.Context(), path)
	if err != nil {
		s.remoteFileError(w, err)
		return
	}
	l := api.DirListing{Path: path, Entries: []api.DirEntry{}}
	for _, e := range ents {
		l.Entries = append(l.Entries, api.DirEntry{Name: e.Name, Size: e.Size, Mtime: e.Mtime, Type: e.Type})
	}
	reply(w, http.StatusOK, l)
}

func (s *Server) remoteFileError(w http.ResponseWriter, err error) {
	switch {
	case wire.IsCode(err, wire.CodeNotFound):
		refuse(w, http.StatusUnprocessableEntity, "no_such_path", err.Error())
	case wire.IsCode(err, wire.CodeDenied):
		refuse(w, http.StatusForbidden, "denied", err.Error())
	case errors.Is(err, wire.ErrOffline):
		fail(w, http.StatusConflict, err.Error())
	default:
		fail(w, http.StatusBadGateway, err.Error())
	}
}

// side is one end of a copy across machines.
type side struct {
	mc   *remote.Machine // nil: this machine
	sep  string
	path string
}

func (sd side) join(dir, name string) string {
	if sd.mc == nil {
		return filepath.Join(dir, name)
	}
	return remote.Join(sd.sep, dir, name)
}

func (sd side) base(p string) string {
	if sd.mc == nil {
		return filepath.Base(p)
	}
	return remote.Base(sd.sep, p)
}

// copyAcross is POST /api/files/copy with a side at another machine: the file read in pieces,
// written beside its place and linked in here, or whole there (at most remoteWriteMax); never over
// a file; to may be a directory, which takes from's name. The same lines as copyFile.
func (s *Server) copyAcross(w http.ResponseWriter, r *http.Request, req api.Copy) {
	ctx := r.Context()
	from, to := side{path: req.From}, side{path: req.To}
	for _, sd := range []struct {
		machine string
		s       *side
	}{{req.FromMachine, &from}, {req.ToMachine, &to}} {
		if sd.machine == "" {
			p, err := s.files.Guarded(sd.s.path)
			if err != nil {
				s.fileError(w, err)
				return
			}
			sd.s.path = p
			continue
		}
		mc, hi := s.machineAt(w, sd.machine)
		if mc == nil {
			return
		}
		if !remote.IsAbs(hi.Sep, sd.s.path) {
			fail(w, http.StatusBadRequest, "a path there is a whole path: "+sd.s.path)
			return
		}
		sd.s.mc, sd.s.sep = mc, hi.Sep
	}
	// The source: a file.
	var size int64
	if from.mc == nil {
		st, err := os.Stat(from.path)
		if err == nil && !st.Mode().IsRegular() {
			err = fmt.Errorf("not a file: %s (directories come later)", from.path)
		}
		if err != nil {
			s.fileError(w, err)
			return
		}
		size = st.Size()
	} else {
		st, err := from.mc.Stat(ctx, from.path)
		if err == nil && st.Type != "file" {
			err = &wire.Error{Code: wire.CodeBadRequest, Message: "not a file: " + from.path + " (directories come later)"}
		}
		if err != nil {
			s.remoteFileError(w, err)
			return
		}
		size = st.Size
	}
	if to.mc != nil && size > remoteWriteMax {
		fail(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("%d bytes: to another machine at most %d for now", size, remoteWriteMax))
		return
	}
	// The target: a new file, in a folder that is there (a folder given takes the source's name).
	name := from.base(from.path)
	if to.mc == nil {
		if st, err := os.Stat(to.path); err == nil && st.IsDir() {
			to.path = filepath.Join(to.path, name)
			if _, err := s.files.Guarded(to.path); err != nil {
				s.fileError(w, err)
				return
			}
		}
		if _, err := os.Lstat(to.path); err == nil {
			fail(w, http.StatusConflict, "there already: "+to.path)
			return
		}
	} else {
		if st, err := to.mc.Stat(ctx, to.path); err == nil && st.Type == "dir" {
			to.path = to.join(to.path, name)
		}
		if _, err := to.mc.Stat(ctx, to.path); err == nil {
			fail(w, http.StatusConflict, "there already: "+to.path)
			return
		} else if !wire.IsCode(err, wire.CodeNotFound) {
			s.remoteFileError(w, err)
			return
		}
	}
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{})
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	enc := json.NewEncoder(w)
	res, err := copyPieces(ctx, from, to, size, func(done int64) {
		_ = enc.Encode(api.CopyProgress{Done: done, Size: size})
		_ = rc.Flush()
	})
	if err != nil {
		res = api.CopyProgress{Error: err.Error()}
	}
	_ = enc.Encode(res)
}

// copyPieces reads from in wire.MaxRead pieces into to, telling progress at most four times a
// second; the last line's path, size and sha256.
func copyPieces(ctx context.Context, from, to side, size int64, progress func(int64)) (api.CopyProgress, error) {
	var src io.ReaderAt
	if from.mc == nil {
		f, err := os.Open(from.path)
		if err != nil {
			return api.CopyProgress{}, err
		}
		defer f.Close()
		src = f
	}
	var dst io.Writer
	var buf []byte // for a target there: written whole at the end
	var tmp *os.File
	if to.mc == nil {
		f, err := os.CreateTemp(filepath.Dir(to.path), ".ir-copy-*")
		if err != nil {
			return api.CopyProgress{}, err
		}
		tmp = f
		defer func() { tmp.Close(); os.Remove(tmp.Name()) }()
		dst = f
	} else {
		dst = writerFunc(func(p []byte) (int, error) { buf = append(buf, p...); return len(p), nil })
	}
	h := sha256.New()
	last := time.Now()
	var done int64
	for done < size {
		n := min(size-done, int64(wire.MaxRead))
		var b []byte
		if src != nil {
			b = make([]byte, n)
			k, err := src.ReadAt(b, done)
			if err != nil && !errors.Is(err, io.EOF) {
				return api.CopyProgress{}, err
			}
			b = b[:k]
		} else {
			var err error
			if b, err = from.mc.ReadAt(ctx, from.path, done, n); err != nil {
				return api.CopyProgress{}, err
			}
		}
		if len(b) == 0 {
			return api.CopyProgress{}, fmt.Errorf("%s ended at %d of %d bytes", from.path, done, size)
		}
		if _, err := dst.Write(b); err != nil {
			return api.CopyProgress{}, err
		}
		h.Write(b)
		done += int64(len(b))
		if time.Since(last) >= 250*time.Millisecond {
			progress(done)
			last = time.Now()
		}
	}
	if to.mc == nil {
		if err := tmp.Close(); err != nil {
			return api.CopyProgress{}, err
		}
		if err := os.Link(tmp.Name(), to.path); err != nil { // never over a file
			return api.CopyProgress{}, err
		}
	} else if err := to.mc.WriteFile(ctx, to.path, buf, false); err != nil {
		return api.CopyProgress{}, err
	}
	return api.CopyProgress{Done: done, Size: done, Path: to.path, Sha256: hex.EncodeToString(h.Sum(nil))}, nil
}
