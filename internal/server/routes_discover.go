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
	mux.HandleFunc("GET /v1/internal/folders", s.folders)
	mux.HandleFunc("POST /v1/internal/folders", s.makeFolder)
	if s.Discover == nil {
		return
	}
	// Folder groups are made as sessions go in them, never by hand (the user, 2026-11-08): their
	// names, order, splitting the panels' and 本机's. A session's place (Satori's channel.update),
	// a folder's marks (channel.update of folder:<path>) are Satori's.
	mux.HandleFunc("PATCH /v1/internal/guilds/{id}", s.manages(s.patchGuild))
}

func workspaceID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		fail(w, http.StatusBadRequest, "bad workspace id")
		return 0, false
	}
	return id, true
}

// patchGuild renames a folder group, moves it among the groups, or splits it at its folder.
func (s *Server) patchGuild(w http.ResponseWriter, r *http.Request) {
	id, ok := workspaceID(w, r)
	if !ok {
		return
	}
	var req api.GuildPatch
	if !readJSON(w, r, &req) {
		return
	}
	var err error
	if req.Name != nil {
		err = s.Discover.RenameWorkspace(id, *req.Name)
	}
	if err == nil && req.Position != nil {
		err = s.Discover.MoveGroup(id, *req.Position)
	}
	if err == nil && req.Split {
		err = s.m.SplitWorkspace(r.Context(), id)
	}
	s.discoverDone(w, err)
}

// errNotIndexed: no discovery index here (the session manager alone, in tests).
var errNotIndexed = errors.New("sessions are not indexed here")

// errPatchManager wraps an error of the session manager's in patch (the rest are Discover's).
var errPatchManager = errors.New("session")

type managerErr struct{ err error }

func (e managerErr) Error() string        { return e.err.Error() }
func (e managerErr) Unwrap() error        { return e.err }
func (e managerErr) Is(target error) bool { return target == errPatchManager }

// patch changes session id as req says (Satori's channel.update and channel.delete).
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
		return managerErr{errNotIndexed}
	}
	return s.Discover.Patch(id, req)
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
