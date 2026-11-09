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
// (docs/api.md "工作区和目录"). Only when the server has a discovery index; the folder picker always
// (another machine's folders need none).
func (s *Server) discoverRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/folders", s.folders)
	mux.HandleFunc("POST /api/folders", s.makeFolder)
	if s.Discover == nil {
		return
	}
	mux.HandleFunc("GET /api/workspaces", s.workspaces)
	// Folder groups are made as sessions go in them, never by hand (the user, 2026-11-08); a name is
	// the panels' and 本机's to change; a session anyone's to name, colour, archive.
	mux.HandleFunc("PATCH /api/workspaces/{id}", s.manages(s.renameWorkspace))
	mux.HandleFunc("PUT /api/groups/order", s.manages(s.orderGroups))
	mux.HandleFunc("PATCH /api/folders/place", s.manages(s.placeFolder))
	mux.HandleFunc("PATCH /api/sessions/{id}", s.patchSession)
	mux.HandleFunc("PUT /api/colors", s.manages(s.folderColor))
}

func (s *Server) workspaces(w http.ResponseWriter, r *http.Request) {
	ws, err := s.Discover.Workspaces()
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
	var err error
	if req.Name != nil {
		err = s.Discover.RenameWorkspace(id, *req.Name)
	}
	if err == nil && req.Hidden != nil {
		err = s.Discover.HideWorkspace(id, *req.Hidden)
	}
	s.discoverDone(w, err)
}

func (s *Server) orderGroups(w http.ResponseWriter, r *http.Request) {
	var req api.GroupOrder
	if !readJSON(w, r, &req) {
		return
	}
	s.discoverDone(w, s.Discover.OrderGroups(req.Keys))
}

func (s *Server) placeFolder(w http.ResponseWriter, r *http.Request) {
	var req api.FolderPlace
	if !readJSON(w, r, &req) {
		return
	}
	s.discoverDone(w, s.Discover.PlaceFolder(req))
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
	if mid := r.URL.Query().Get("machine"); mid != "" {
		s.remoteFolders(w, r, mid, r.URL.Query().Get("path"))
		return
	}
	if s.Discover == nil {
		fail(w, http.StatusNotFound, "no folders here")
		return
	}
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
	if req.Machine != "" {
		s.remoteMkdir(w, r, req)
		return
	}
	if s.Discover == nil {
		fail(w, http.StatusNotFound, "no folders here")
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
	case errors.Is(err, store.ErrWorkspacePath), errors.Is(err, fs.ErrExist), errors.Is(err, store.ErrUserNamed):
		fail(w, http.StatusConflict, err.Error())
	case errors.Is(err, discover.ErrOutside), errors.Is(err, fs.ErrPermission):
		fail(w, http.StatusForbidden, err.Error())
	default:
		// The rest are what was asked: not a folder, not a folder name, no name.
		fail(w, http.StatusBadRequest, err.Error())
	}
}
