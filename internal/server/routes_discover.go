package server

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"path/filepath"
	"strconv"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/discover"
	"github.com/kumoSleeping/iridium/internal/gate"
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
	mux.HandleFunc("POST /api/workspaces/{id}/split", s.manages(s.splitWorkspace))
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
	if c := callerOf(r); !c.Host() {
		ws = s.scopeWorkspaces(s.permsFor(c), ws)
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
	s.discoverDone(w, err)
}

func (s *Server) splitWorkspace(w http.ResponseWriter, r *http.Request) {
	id, ok := workspaceID(w, r)
	if !ok {
		return
	}
	s.discoverDone(w, s.m.SplitWorkspace(r.Context(), id))
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
		refuse(w, http.StatusUnprocessableEntity, "no_such_session", "no such session")
		return
	}
	if req.Workspace != nil && *req.Workspace < 0 {
		fail(w, http.StatusBadRequest, "bad workspace")
		return
	}
	if err := s.patch(r.Context(), id, req); err != nil {
		if errors.Is(err, errPatchManager) {
			s.error(w, errors.Unwrap(err))
			return
		}
		s.discoverError(w, err)
		return
	}
	reply(w, http.StatusOK, map[string]any{"ok": true})
}

// errPatchManager wraps an error of the session manager's in patch (the rest are Discover's).
var errPatchManager = errors.New("session")

type managerErr struct{ err error }

func (e managerErr) Error() string        { return e.err.Error() }
func (e managerErr) Unwrap() error        { return e.err }
func (e managerErr) Is(target error) bool { return target == errPatchManager }

// patch changes session id as req says (PATCH /api/sessions/{id}, Satori's channel.update and
// channel.delete).
func (s *Server) patch(ctx context.Context, id string, req api.SessionPatch) error {
	// Archiving is the shown marker's, and stops a hosted agent: the session manager's; the
	// sessions hung under it go with it (ir archive too).
	if req.Archived != nil {
		if err := s.m.ArchiveTree(ctx, id, *req.Archived); err != nil {
			return managerErr{err}
		}
		req.Archived = nil
	}
	if req.Detach != nil && *req.Detach {
		if err := s.m.Detach(ctx, id); err != nil {
			return managerErr{err}
		}
	}
	req.Detach = nil
	if s.Discover == nil {
		return managerErr{errors.New("sessions are not indexed here")}
	}
	return s.Discover.Patch(id, req)
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
		if err := s.remoteScope(callerOf(r), mid, r.URL.Query().Get("path")); err != nil {
			s.error(w, err)
			return
		}
		s.remoteFolders(w, r, mid, r.URL.Query().Get("path"))
		return
	}
	if s.Discover == nil {
		refuse(w, http.StatusUnprocessableEntity, "not_here", "no folders here")
		return
	}
	l, err := s.Folders.List(r.URL.Query().Get("path"))
	if err != nil {
		s.discoverError(w, err)
		return
	}
	if err := s.scopeFolders(callerOf(r), gate.HostID, &l); err != nil {
		s.error(w, err)
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
		if err := s.remoteScope(callerOf(r), req.Machine, req.Path); err != nil {
			s.error(w, err)
			return
		}
		s.remoteMkdir(w, r, req)
		return
	}
	if s.Discover == nil {
		refuse(w, http.StatusUnprocessableEntity, "not_here", "no folders here")
		return
	}
	if dir, err := s.Folders.Resolve(req.Path); err == nil {
		if err := s.inScope(callerOf(r), gate.HostID, filepath.Join(dir, req.Name)); err != nil {
			s.error(w, err)
			return
		}
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
	case errors.Is(err, store.ErrNoWorkspace):
		refuse(w, http.StatusUnprocessableEntity, "no_such_workspace", err.Error())
	case errors.Is(err, fs.ErrNotExist):
		refuse(w, http.StatusUnprocessableEntity, "no_such_path", err.Error())
	case errors.Is(err, store.ErrWorkspacePath), errors.Is(err, fs.ErrExist), errors.Is(err, store.ErrUserNamed):
		fail(w, http.StatusConflict, err.Error())
	case errors.Is(err, discover.ErrOutside), errors.Is(err, fs.ErrPermission):
		refuse(w, http.StatusForbidden, "outside", err.Error())
	default:
		// The rest are what was asked: not a folder, not a folder name, no name.
		fail(w, http.StatusBadRequest, err.Error())
	}
}
