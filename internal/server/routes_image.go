package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/kumoSleeping/iridium/internal/session"
)

// image is a picture a tool's result gave the model: GET /api/sessions/{id}/image?at=&n=. Its
// ETag is its content's, so a page shown again asks only whether it changed. 425: not in the
// record yet, to ask again.
func (s *Server) image(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	n, err := strconv.Atoi(q.Get("n"))
	if err != nil {
		fail(w, http.StatusBadRequest, "n: a picture's place among its result's")
		return
	}
	mime, data, err := s.m.Image(r.Context(), r.PathValue("id"), q.Get("at"), n)
	switch {
	case errors.Is(err, session.ErrNoImage):
		refuse(w, http.StatusUnprocessableEntity, "no_such_image", err.Error())
		return
	case errors.Is(err, session.ErrGone):
		fail(w, http.StatusGone, err.Error())
		return
	case errors.Is(err, session.ErrNotYet):
		w.Header().Set("Retry-After", "1")
		fail(w, http.StatusTooEarly, err.Error())
		return
	case errors.Is(err, session.ErrOffline):
		fail(w, http.StatusConflict, err.Error())
		return
	case err != nil:
		s.error(w, err)
		return
	}
	sum := sha256.Sum256(data)
	h := w.Header()
	h.Set("Content-Type", mime)
	h.Set("ETag", `"`+hex.EncodeToString(sum[:16])+`"`)
	h.Set("Cache-Control", "private, no-cache")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	h.Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(data))
}
