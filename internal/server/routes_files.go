package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/config"
	"github.com/kumoSleeping/iridium/internal/files"
	"github.com/kumoSleeping/iridium/internal/pathsafe"
	"github.com/kumoSleeping/iridium/internal/session"
)

// newFiles is the file service over the manager's sessions, its guard denying the state and
// config directories and the agents' homes, all but the state directory's uploads.
func newFiles(cfg config.Config, m *session.Manager, logf func(string, ...any)) *files.Service {
	state := cfg.StatePath()
	deny := pathsafe.Deny(state, filepath.Dir(config.DefaultPath()), cfg.ClaudeDir())
	return files.New(files.Options{
		State:    state,
		Guard:    pathsafe.New(nil, deny, []string{files.UploadsFallback(state)}),
		Sessions: sessionsOf{m},
		Keep:     cfg.Uploads.Keep.D(),
		MaxFile:  cfg.Uploads.MaxFile,
		Logf:     logf,
	})
}

type sessionsOf struct{ m *session.Manager }

func (a sessionsOf) Cwd(id string) (string, error) {
	st, err := a.m.State(id)
	if err != nil {
		return "", err
	}
	return st.Cwd, nil
}

func (a sessionsOf) After(id string, at int64) (api.MessagePage, error) {
	return a.m.Messages(id, -1, at)
}

// fileRoutes are the files a session names, uploads, and (local: the unix socket only)
// attaching, listing and copying by path.
func (s *Server) fileRoutes(mux *http.ServeMux, local bool) {
	// Every terminal reads every session's files; one goes into a session only from a terminal
	// that may use it (access.go: Claude's, the owner's own). The files service guards the paths.
	mux.HandleFunc("GET /api/sessions/{id}/files/{ref}", s.fileGet)
	mux.HandleFunc("GET /api/sessions/{id}/files/{ref}/thumb", s.fileThumb)
	mux.HandleFunc("GET /api/sessions/{id}/files/{ref}/info", s.fileInfo)
	mux.HandleFunc("POST /api/sessions/{id}/uploads", s.use(s.uploadCreate))
	mux.HandleFunc("GET /api/uploads/{upload}", s.uploadGet)
	mux.HandleFunc("PUT /api/uploads/{upload}", s.uploadPut)
	mux.HandleFunc("POST /api/uploads/{upload}/complete", s.uploadComplete)
	mux.HandleFunc("DELETE /api/uploads/{upload}", s.uploadCancel)
	if local {
		mux.HandleFunc("POST /api/sessions/{id}/attach", s.use(s.attachPath))
		mux.HandleFunc("GET /api/files", s.listDir)
		mux.HandleFunc("POST /api/files/copy", s.copyFile)
	}
}

func (s *Server) lookup(w http.ResponseWriter, r *http.Request) (string, bool) {
	p, err := s.files.Lookup(r.PathValue("id"), r.PathValue("ref"))
	if err != nil {
		s.fileError(w, err)
		return "", false
	}
	return p, true
}

func (s *Server) fileGet(w http.ResponseWriter, r *http.Request) {
	if p, ok := s.lookup(w, r); ok {
		s.files.ServeFile(w, r, p)
	}
}

func (s *Server) fileThumb(w http.ResponseWriter, r *http.Request) {
	side := files.ThumbSide
	if v := r.URL.Query().Get("size"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 32 || n > 4096 {
			fail(w, http.StatusBadRequest, "size is 32 to 4096")
			return
		}
		side = n
	}
	if p, ok := s.lookup(w, r); ok {
		s.files.Thumb(w, r, p, side)
	}
}

func (s *Server) fileInfo(w http.ResponseWriter, r *http.Request) {
	p, ok := s.lookup(w, r)
	if !ok {
		return
	}
	info, err := s.files.Info(p)
	if err == nil {
		info.Sha256, err = s.files.Sum(p)
	}
	if err != nil {
		s.fileError(w, err)
		return
	}
	info.Ref = r.PathValue("ref")
	reply(w, http.StatusOK, info)
}

func (s *Server) uploadCreate(w http.ResponseWriter, r *http.Request) {
	var req api.NewUpload
	if !readJSON(w, r, &req) {
		return
	}
	u, err := s.files.CreateUpload(r.PathValue("id"), req)
	if err != nil {
		s.fileError(w, err)
		return
	}
	reply(w, http.StatusOK, u)
}

func (s *Server) uploadGet(w http.ResponseWriter, r *http.Request) {
	u, err := s.files.UploadState(r.PathValue("upload"))
	if err != nil {
		s.fileError(w, err)
		return
	}
	reply(w, http.StatusOK, u)
}

// uploadPut takes a chunk at ?offset=. A 409 carries the Upload, to go on from its received.
func (s *Server) uploadPut(w http.ResponseWriter, r *http.Request) {
	off, err := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
	if err != nil || off < 0 {
		fail(w, http.StatusBadRequest, "bad offset")
		return
	}
	// A big chunk on a slow link takes a while; the server's own timeouts are for headers.
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Now().Add(10 * time.Minute))
	u, err := s.files.Put(r.PathValue("upload"), off, r.Body, r.ContentLength)
	if err != nil {
		if errors.Is(err, files.ErrOffset) || errors.Is(err, files.ErrBusy) || errors.Is(err, files.ErrDone) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(struct {
				api.Upload
				Error string `json:"error"`
			}{u, err.Error()})
			return
		}
		s.fileError(w, err)
		return
	}
	reply(w, http.StatusOK, u)
}

func (s *Server) uploadComplete(w http.ResponseWriter, r *http.Request) {
	var req api.CompleteUpload
	if !readJSON(w, r, &req) {
		return
	}
	u, err := s.files.Complete(r.PathValue("upload"), req.Sha256)
	if err != nil {
		s.fileError(w, err)
		return
	}
	reply(w, http.StatusOK, u)
}

func (s *Server) uploadCancel(w http.ResponseWriter, r *http.Request) {
	if err := s.files.Cancel(r.PathValue("upload")); err != nil {
		s.fileError(w, err)
		return
	}
	reply(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) attachPath(w http.ResponseWriter, r *http.Request) {
	var req api.AttachPath
	if !readJSON(w, r, &req) {
		return
	}
	info, err := s.files.Attach(r.PathValue("id"), req.Path)
	if err != nil {
		s.fileError(w, err)
		return
	}
	reply(w, http.StatusOK, info)
}

func (s *Server) listDir(w http.ResponseWriter, r *http.Request) {
	if m := r.URL.Query().Get("machine"); m != "" {
		s.listAt(w, r, m, r.URL.Query().Get("path"))
		return
	}
	l, err := s.files.List(r.URL.Query().Get("path"))
	if err != nil {
		s.fileError(w, err)
		return
	}
	reply(w, http.StatusOK, l)
}

// copyFile streams the copy's progress, a CopyProgress a line, four a second; the last line has
// the path and sha256, or the error. The copy goes on if the asker goes away.
func (s *Server) copyFile(w http.ResponseWriter, r *http.Request) {
	var req api.Copy
	if !readJSON(w, r, &req) {
		return
	}
	if req.FromMachine != "" || req.ToMachine != "" {
		s.copyAcross(w, r, req)
		return
	}
	job, err := s.files.Copy(req.From, req.To)
	if err != nil {
		s.fileError(w, err)
		return
	}
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{})
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	enc := json.NewEncoder(w)
	t := time.NewTicker(250 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-job.Done():
			res, err := job.Result()
			if err != nil {
				res = api.CopyProgress{Error: err.Error()}
			}
			_ = enc.Encode(res)
			return
		case <-r.Context().Done():
			return
		case <-t.C:
			_ = enc.Encode(job.Progress())
			_ = rc.Flush()
		}
	}
}

func (s *Server) fileError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, files.ErrNoRef), errors.Is(err, files.ErrNoUpload), errors.Is(err, os.ErrNotExist), errors.Is(err, session.ErrNotFound):
		fail(w, http.StatusNotFound, err.Error())
	case errors.Is(err, pathsafe.ErrDenied), errors.Is(err, pathsafe.ErrOutside):
		fail(w, http.StatusForbidden, err.Error())
	case errors.Is(err, files.ErrExists), errors.Is(err, files.ErrOffset), errors.Is(err, files.ErrBusy),
		errors.Is(err, files.ErrIncomplete), errors.Is(err, files.ErrDone):
		fail(w, http.StatusConflict, err.Error())
	case errors.Is(err, files.ErrChecksum):
		fail(w, http.StatusUnprocessableEntity, err.Error())
	case errors.Is(err, files.ErrTooLarge), errors.Is(err, files.ErrTooLong):
		fail(w, http.StatusRequestEntityTooLarge, err.Error())
	case errors.Is(err, files.ErrNotImage):
		fail(w, http.StatusUnsupportedMediaType, err.Error())
	case errors.Is(err, files.ErrBad), errors.Is(err, files.ErrNotFile), errors.Is(err, pathsafe.ErrRelative):
		fail(w, http.StatusBadRequest, err.Error())
	default:
		s.error(w, err)
	}
}
