package server

import (
	"errors"
	"io/fs"
	"net/http"
	"strconv"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/discover"
	"github.com/kumoSleeping/iridium/internal/store"
)

// discoverRoutes are the workspaces, what lies over a session, and the folder picker
// (docs/api.md "工作区和目录"). Only when the server has a discovery index.
func (s *Server) discoverRoutes(mux *http.ServeMux) {
	if s.Discover == nil {
		return
	}
	mux.HandleFunc("GET /api/workspaces", s.workspaces)
	// The permission table (access.go): workspaces are the panels' and 本机's to change; a session
	// the terminal's to use; the folder picker only where its directories are.
	mux.HandleFunc("POST /api/workspaces", s.manages(s.createWorkspace))
	mux.HandleFunc("PATCH /api/workspaces/{id}", s.manages(s.renameWorkspace))
	mux.HandleFunc("DELETE /api/workspaces/{id}", s.manages(s.deleteWorkspace))
	mux.HandleFunc("PATCH /api/sessions/{id}", s.use(s.patchSession))
	mux.HandleFunc("PUT /api/colors", s.manages(s.folderColor))
	mux.HandleFunc("GET /api/folders", s.pathRoute("path", nil, false, false, s.folders))
	mux.HandleFunc("POST /api/folders", s.pathRoute("", []string{"path"}, false, false, s.makeFolder))
}

func (s *Server) workspaces(w http.ResponseWriter, r *http.Request) {
	ws, err := s.Discover.Workspaces()
	if err != nil {
		s.discoverError(w, err)
		return
	}
	reply(w, http.StatusOK, ws)
}

func (s *Server) createWorkspace(w http.ResponseWriter, r *http.Request) {
	var req api.NewWorkspace
	if !readJSON(w, r, &req) {
		return
	}
	path, err := s.Folders.Resolve(req.Path)
	if err != nil {
		s.discoverError(w, err)
		return
	}
	ws, err := s.Discover.CreateWorkspace(path, req.Name)
	if err != nil {
		s.discoverError(w, err)
		return
	}
	reply(w, http.StatusOK, ws)
}

func workspaceID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		fail(w, http.StatusBadRequest, "bad workspace id")
		return 0, false
	}
	return id, true
}

func (s *Server) renameWorkspace(w http.ResponseWriter, r *http.Request) {
	id, ok := workspaceID(w, r)
	if !ok {
		return
	}
	var req api.WorkspacePatch
	if !readJSON(w, r, &req) {
		return
	}
	s.discoverDone(w, s.Discover.RenameWorkspace(id, req.Name))
}

func (s *Server) deleteWorkspace(w http.ResponseWriter, r *http.Request) {
	id, ok := workspaceID(w, r)
	if !ok {
		return
	}
	s.discoverDone(w, s.Discover.DeleteWorkspace(id))
}

func (s *Server) patchSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req api.SessionPatch
	if !readJSON(w, r, &req) {
		return
	}
	if !s.m.Known(id) {
		fail(w, http.StatusNotFound, "no such session")
		return
	}
	if req.Workspace != nil && *req.Workspace < 0 {
		fail(w, http.StatusBadRequest, "bad workspace")
		return
	}
	// Archiving is the shown marker's, and stops a hosted agent: the session manager's.
	if req.Archived != nil {
		if err := s.m.SetArchived(r.Context(), id, *req.Archived); err != nil {
			s.error(w, err)
			return
		}
		req.Archived = nil
	}
	s.discoverDone(w, s.Discover.Patch(id, req))
}

// folderColor tags a folder with a colour from its menu, or takes the tag off.
func (s *Server) folderColor(w http.ResponseWriter, r *http.Request) {
	var req api.FolderColor
	if !readJSON(w, r, &req) {
		return
	}
	s.discoverDone(w, s.Discover.SetFolderColor(req.Path, req.Color))
}

func (s *Server) folders(w http.ResponseWriter, r *http.Request) {
	l, err := s.Folders.List(r.URL.Query().Get("path"))
	if err != nil {
		s.discoverError(w, err)
		return
	}
	reply(w, http.StatusOK, l)
}

func (s *Server) makeFolder(w http.ResponseWriter, r *http.Request) {
	var req api.NewFolder
	if !readJSON(w, r, &req) {
		return
	}
	p, err := s.Folders.Make(req.Path, req.Name)
	if err != nil {
		s.discoverError(w, err)
		return
	}
	reply(w, http.StatusOK, api.FolderReply{Path: p})
}

func (s *Server) discoverDone(w http.ResponseWriter, err error) {
	if err != nil {
		s.discoverError(w, err)
		return
	}
	reply(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) discoverError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNoWorkspace), errors.Is(err, fs.ErrNotExist):
		fail(w, http.StatusNotFound, err.Error())
	case errors.Is(err, store.ErrWorkspacePath), errors.Is(err, fs.ErrExist):
		fail(w, http.StatusConflict, err.Error())
	case errors.Is(err, discover.ErrOutside), errors.Is(err, fs.ErrPermission):
		fail(w, http.StatusForbidden, err.Error())
	default:
		// The rest are what was asked: not a folder, not a folder name, no name.
		fail(w, http.StatusBadRequest, err.Error())
	}
}
