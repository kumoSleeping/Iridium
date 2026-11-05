package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/chat"
	"github.com/kumoSleeping/iridium/internal/push"
	"github.com/kumoSleeping/iridium/internal/session"
	"github.com/kumoSleeping/iridium/internal/store"
)

// Identities says which interactive terminal a request comes from. The gate implements it
// (gate.FromContext: EndpointID and Name); ok false: none, the request is refused.
type Identities interface {
	Caller(r *http.Request) (chat.Caller, bool)
}

// IdentityFunc is a func as Identities.
type IdentityFunc func(r *http.Request) (chat.Caller, bool)

func (f IdentityFunc) Caller(r *http.Request) (chat.Caller, bool) { return f(r) }

// caller is the request's terminal: from Who if set, else the terminal name the handler gives
// every request (网页, 本机) as both its id and its name.
func (s *Server) caller(w http.ResponseWriter, r *http.Request) (chat.Caller, bool) {
	if s.Who == nil {
		n := terminal(r)
		return chat.Caller{ID: n, Name: n}, true
	}
	c, ok := s.Who.Caller(r)
	if !ok {
		fail(w, http.StatusUnauthorized, "no interactive terminal")
	}
	return c, ok
}

// pluginRoutes: the chat layer (docs/api.md "插件").
func (s *Server) pluginRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/answer", s.answerCard)
	if s.Chat == nil {
		return
	}
	mux.HandleFunc("GET /api/chat/{key}", s.chatPointer)
	mux.HandleFunc("POST /api/chat/{key}/input", s.chatInput)
	mux.HandleFunc("GET /api/chat/{key}/commands", s.chatCommands)
	mux.HandleFunc("GET /api/chat/{key}/files/{ref}", s.chatFile)
}

func (s *Server) chatPointer(w http.ResponseWriter, r *http.Request) {
	c, ok := s.caller(w, r)
	if !ok {
		return
	}
	p, err := s.Chat.Pointer(c, r.PathValue("key"))
	if err != nil {
		s.fault(w, err)
		return
	}
	reply(w, http.StatusOK, p)
}

func (s *Server) chatInput(w http.ResponseWriter, r *http.Request) {
	c, ok := s.caller(w, r)
	if !ok {
		return
	}
	var in api.ChatInput
	if !readJSON(w, r, &in) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	out, err := s.Chat.Input(ctx, c, r.PathValue("key"), in)
	if err != nil {
		s.fault(w, err)
		return
	}
	reply(w, http.StatusOK, out)
}

func (s *Server) chatCommands(w http.ResponseWriter, r *http.Request) {
	c, ok := s.caller(w, r)
	if !ok {
		return
	}
	l, err := s.Chat.Commands(c, r.PathValue("key"))
	if err != nil {
		s.fault(w, err)
		return
	}
	reply(w, http.StatusOK, l)
}

func (s *Server) chatFile(w http.ResponseWriter, r *http.Request) {
	c, ok := s.caller(w, r)
	if !ok {
		return
	}
	path, err := s.Chat.File(c, r.PathValue("ref"))
	if err != nil {
		s.fault(w, err)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		fail(w, http.StatusNotFound, "no such file")
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		fail(w, http.StatusNotFound, "no such file")
		return
	}
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(filepath.Base(path)))
	http.ServeContent(w, r, filepath.Base(path), st.ModTime(), f) // Range, If-Modified-Since
}

// answerCard answers a card: an ask's (the web's confirmation panel too), or a command's pick.
func (s *Server) answerCard(w http.ResponseWriter, r *http.Request) {
	c, ok := s.caller(w, r)
	if !ok {
		return
	}
	var a api.CardAnswer
	if !readJSON(w, r, &a) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	if s.Chat == nil {
		if err := s.m.AnswerCard(ctx, a); err != nil {
			s.fault(w, err)
			return
		}
		reply(w, http.StatusOK, api.CardReply{OK: true})
		return
	}
	out, err := s.Chat.Answer(ctx, c, a)
	if err != nil {
		s.fault(w, err)
		return
	}
	reply(w, http.StatusOK, out)
}

// pluginEvents is GET /api/events without sub=: the caller's plugin events, from the one after
// Last-Event-ID (or ?after=), else from now on.
func (s *Server) pluginEvents(w http.ResponseWriter, r *http.Request) {
	c, ok := s.caller(w, r)
	if !ok {
		return
	}
	last := int64(-1)
	for _, v := range []string{r.Header.Get("Last-Event-ID"), r.URL.Query().Get("after")} {
		if v == "" {
			continue
		}
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			fail(w, http.StatusBadRequest, "bad Last-Event-ID")
			return
		}
		last = n
	}
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{})
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	io.WriteString(w, "retry: 2000\n\n")
	_ = rc.Flush()
	if err := s.Chat.Serve(r.Context(), c, last, w, func() { _ = rc.Flush() }); err != nil {
		s.logf("server: plugin events: %v", err)
	}
}

// fault answers with err, knowing the errors of the session settings, the queue, push and chat;
// the rest as s.error does.
func (s *Server) fault(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, session.ErrBadRequest), errors.Is(err, chat.ErrBadRequest), errors.Is(err, push.ErrBadSubscription):
		fail(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, store.ErrNoInput), errors.Is(err, chat.ErrNotFound):
		fail(w, http.StatusNotFound, err.Error())
	case errors.Is(err, session.ErrGone):
		fail(w, http.StatusGone, err.Error())
	default:
		s.error(w, err)
	}
}
