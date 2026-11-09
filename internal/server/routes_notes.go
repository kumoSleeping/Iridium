package server

import (
	"crypto/rand"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/store"
)

// noteRoutes are the notes (docs/api.md "便签"): the panels' and 本机's, as the colour tags are.
// Each change carries every note again (the state "notes").
func (s *Server) noteRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/notes", s.manages(s.notes))
	mux.HandleFunc("POST /api/notes", s.manages(s.newNote))
	mux.HandleFunc("PATCH /api/notes/{id}", s.manages(s.patchNote))
	mux.HandleFunc("DELETE /api/notes/{id}", s.manages(s.deleteNote))
	mux.HandleFunc("POST /api/note-folders", s.manages(s.newNoteFolder))
	mux.HandleFunc("PATCH /api/note-folders/{id}", s.manages(s.patchNoteFolder))
	mux.HandleFunc("DELETE /api/note-folders/{id}", s.manages(s.deleteNoteFolder))
}

// noteMax is the longest a note's words may be, in bytes.
const noteMax = 64 << 10

var noteID = regexp.MustCompile(`^[0-9a-zA-Z_-]{1,64}$`)

// carryNotes sets the state "notes" to every note as it is now.
func (s *Server) carryNotes() {
	n, err := s.st.Notes()
	if err != nil {
		s.logf("notes: %v", err)
		return
	}
	s.hub.Set("notes", n)
}

func (s *Server) notes(w http.ResponseWriter, r *http.Request) {
	n, err := s.st.Notes()
	if err != nil {
		s.error(w, err)
		return
	}
	reply(w, http.StatusOK, n)
}

func (s *Server) noteError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNoNote) {
		fail(w, http.StatusNotFound, err.Error())
		return
	}
	s.error(w, err)
}

func (s *Server) newNote(w http.ResponseWriter, r *http.Request) {
	var req api.NewNote
	if !readJSON(w, r, &req) {
		return
	}
	if req.ID == "" {
		req.ID = strings.ToLower(rand.Text())
	}
	if !noteID.MatchString(req.ID) {
		fail(w, http.StatusBadRequest, "bad note id")
		return
	}
	if len(req.Body) > noteMax {
		fail(w, http.StatusRequestEntityTooLarge, "the note is too long")
		return
	}
	n, err := s.st.AddNote(req)
	if err != nil {
		s.noteError(w, err)
		return
	}
	s.carryNotes()
	reply(w, http.StatusOK, n)
}

func (s *Server) patchNote(w http.ResponseWriter, r *http.Request) {
	var req api.NotePatch
	if !readJSON(w, r, &req) {
		return
	}
	if req.Body != nil && len(*req.Body) > noteMax {
		fail(w, http.StatusRequestEntityTooLarge, "the note is too long")
		return
	}
	n, err := s.st.PatchNote(r.PathValue("id"), req)
	if err != nil {
		s.noteError(w, err)
		return
	}
	s.carryNotes()
	reply(w, http.StatusOK, n)
}

func (s *Server) deleteNote(w http.ResponseWriter, r *http.Request) {
	if err := s.st.DeleteNote(r.PathValue("id")); err != nil {
		s.noteError(w, err)
		return
	}
	s.carryNotes()
	s.done(w, nil)
}

func (s *Server) newNoteFolder(w http.ResponseWriter, r *http.Request) {
	var req api.NoteFolderPatch
	if !readJSON(w, r, &req) {
		return
	}
	if req.Name == nil || strings.TrimSpace(*req.Name) == "" {
		fail(w, http.StatusBadRequest, "name the folder")
		return
	}
	f, err := s.st.AddNoteFolder(strings.TrimSpace(*req.Name))
	if err != nil {
		s.noteError(w, err)
		return
	}
	s.carryNotes()
	reply(w, http.StatusOK, f)
}

func (s *Server) patchNoteFolder(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		fail(w, http.StatusBadRequest, "bad folder id")
		return
	}
	var req api.NoteFolderPatch
	if !readJSON(w, r, &req) {
		return
	}
	if req.Name != nil {
		if *req.Name = strings.TrimSpace(*req.Name); *req.Name == "" {
			fail(w, http.StatusBadRequest, "name the folder")
			return
		}
	}
	if err := s.st.PatchNoteFolder(id, req); err != nil {
		s.noteError(w, err)
		return
	}
	s.carryNotes()
	s.done(w, nil)
}

func (s *Server) deleteNoteFolder(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		fail(w, http.StatusBadRequest, "bad folder id")
		return
	}
	if err := s.st.DeleteNoteFolder(id); err != nil {
		s.noteError(w, err)
		return
	}
	s.carryNotes()
	s.done(w, nil)
}
