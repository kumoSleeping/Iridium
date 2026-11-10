package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode"

	"github.com/kumoSleeping/iridium/internal/access"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/files"
	"github.com/kumoSleeping/iridium/internal/gate"
	"github.com/kumoSleeping/iridium/internal/pathsafe"
	"github.com/kumoSleeping/iridium/internal/satori"
	"github.com/kumoSleeping/iridium/internal/session"
	"github.com/kumoSleeping/iridium/internal/transcript"
)

// Satori with ir (package satori, docs/satori.md): /v1/ on the TCP address, behind the gate and the
// login check (withLogin); this file is the server as its Backend.

// withSatori serves /v1/ by package satori, the rest by next.
func (s *Server) withSatori(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.sat != nil && (r.URL.Path == "/v1" || strings.HasPrefix(r.URL.Path, "/v1/")) {
			s.sat.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type satoriBackend struct{ s *Server }

// callerAs is c as the server's caller: its app, speaking as its login. Until S2 a login counts as
// its app (gate.Identity.ID): its permissions, its usage.
func callerAs(c satori.Caller) caller {
	return caller{Identity: gate.Identity{App: c.App, Login: c.Login, Name: c.AppName}}
}

func (b satoriBackend) Who(r *http.Request) (satori.Caller, bool) {
	id, ok := gate.FromContext(r.Context())
	if !ok || id.App == "" || id.Host() || id.App == gate.WebApp || id.Session != "" {
		return satori.Caller{}, false
	}
	return satori.Caller{App: id.App, AppName: id.Name, Login: id.Login}, true
}

func (b satoriBackend) AppByToken(tok string) (satori.Caller, bool) {
	info, ok := b.s.gate().ByToken(tok)
	if !ok || info.Host() || info.Kind != gate.KindChannel {
		return satori.Caller{}, false
	}
	return satori.Caller{App: info.ID, AppName: info.Name}, true
}

func (b satoriBackend) Logins(app string) []satori.LoginInfo {
	l, err := b.s.st.Logins()
	if err != nil {
		b.s.logf("server: logins: %v", err)
		return nil
	}
	var out []satori.LoginInfo
	for _, x := range l {
		if x.App == app && !x.ApprovedAt.IsZero() {
			out = append(out, satori.LoginInfo{ID: x.ID, Name: x.Name})
		}
	}
	return out
}

func (b satoriBackend) Track(r *http.Request, app string, closeFn func()) func() {
	return b.s.gate().Track(r.WithContext(gate.NewContext(r.Context(), gate.Identity{App: app})), closeFn)
}

func (b satoriBackend) User(id string) (satori.User, bool) {
	info, ok := b.s.gate().Endpoint(id)
	if !ok {
		return satori.User{}, false
	}
	return satori.User{ID: info.ID, Name: info.Name, Avatar: info.Avatar}, true
}

func (b satoriBackend) Sessions(ctx context.Context) ([]api.Session, error) { return b.s.m.List(ctx) }

func (b satoriBackend) Session(id string) (*api.SessionState, error) { return b.s.m.State(id) }

func (b satoriBackend) Workspaces() ([]api.Workspace, error) {
	if b.s.Discover == nil {
		return nil, nil
	}
	return b.s.Discover.Workspaces()
}

// Sees: every session, until S2's scopes (decision 17: the login's within its app's).
// Sees: the session is in the login's scope within its app's (S2).
func (b satoriBackend) Sees(c satori.Caller, id string) bool {
	st, err := b.s.m.State(id)
	if err != nil {
		return false
	}
	return b.s.sees(b.s.permsFor(callerAs(c)), st.Session)
}

func (b satoriBackend) Messages(id string, before, after int64) (api.MessagePage, error) {
	p, err := b.s.m.Messages(id, before, after)
	if err == nil {
		b.s.files.Annotate(id, &p)
	}
	return p, err
}

func (b satoriBackend) Full(id, at string) (string, error) { return b.s.m.Full(id, at) }

// sourceTag is what the agent sees a login's message as from: "<app>·<login>".
func sourceTag(c satori.Caller) string {
	clean := func(t string) string {
		t = strings.Map(func(r rune) rune {
			if strings.ContainsRune("[]/:·", r) || !unicode.IsPrint(r) {
				return -1
			}
			return r
		}, t)
		t = strings.Join(strings.Fields(t), " ")
		if r := []rune(t); len(r) > 24 {
			t = string(r[:24])
		}
		return t
	}
	if l := clean(c.LoginName); l != "" {
		return clean(c.AppName) + "·" + l
	}
	return clean(c.AppName)
}

func (b satoriBackend) Send(ctx context.Context, c satori.Caller, id, text string, refs []string) (satori.SendResult, error) {
	s, cl := b.s, callerAs(c)
	if err := s.checkSend(cl, id); err != nil {
		return satori.SendResult{}, err
	}
	from := session.From(c.App, sourceTag(c))
	if session.IsClear(text) && len(refs) == 0 {
		nid, err := s.m.Clear(ctx, id, from)
		if nid != "" && nid != id {
			if err := s.st.SetOpener(nid, cl.ID()); err != nil {
				s.logf("server: opener of %s: %v", nid, err)
			}
		}
		return satori.SendResult{Session: nid}, err
	}
	text, images, err := s.files.Message(id, text, refs)
	if err != nil {
		return satori.SendResult{}, err
	}
	// As the chat layer did: it joins a running turn.
	input, _, err := s.m.SendWith(ctx, id, text, from, true, images...)
	if err != nil {
		return satori.SendResult{}, err
	}
	if input == session.HandedInput {
		input = ""
	}
	return satori.SendResult{Input: input, Session: id}, nil
}

func (b satoriBackend) Answer(ctx context.Context, c satori.Caller, a api.CardAnswer) error {
	rest, _ := strings.CutPrefix(a.Card, "ask:")
	id, _, _ := strings.Cut(rest, ":")
	if _, err := b.s.using(callerAs(c), id); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	return b.s.m.AnswerCard(ctx, a)
}

func (b satoriBackend) Upload(c satori.Caller, id, name string, size int64, body io.Reader) (string, error) {
	s := b.s
	if _, err := s.using(callerAs(c), id); err != nil {
		return "", err
	}
	u, err := s.files.CreateUpload(id, api.NewUpload{Name: name, Size: size})
	if err != nil {
		return "", err
	}
	h := sha256.New()
	if _, err := s.files.Put(u.ID, 0, io.TeeReader(body, h), size); err != nil {
		_ = s.files.Cancel(u.ID)
		return "", err
	}
	u, err = s.files.Complete(u.ID, hex.EncodeToString(h.Sum(nil)))
	if err != nil {
		return "", err
	}
	if u.File == nil {
		return "", errors.New("the upload gave no file")
	}
	return u.File.Ref, nil
}

func (b satoriBackend) File(c satori.Caller, id, ref string) (string, error) {
	return b.s.files.Lookup(id, ref)
}

func (b satoriBackend) ServeFile(w http.ResponseWriter, r *http.Request, path string) {
	b.s.files.ServeFile(w, r, path)
}

func (b satoriBackend) Mask(text string) string { return b.s.MaskText(text) }

// Fault: decision 28, what is not there is 422 with its code; 404 is the routes'.
func (b satoriBackend) Fault(err error) (int, string) {
	switch {
	case errors.Is(err, session.ErrNotFound):
		return http.StatusUnprocessableEntity, "no_such_session"
	case errors.Is(err, transcript.ErrNoBlock):
		return http.StatusUnprocessableEntity, "no_such_message"
	case errors.Is(err, session.ErrGone):
		return http.StatusUnprocessableEntity, "no_such_ask"
	case errors.Is(err, files.ErrNoRef), errors.Is(err, files.ErrNoUpload), errors.Is(err, os.ErrNotExist), errors.Is(err, files.ErrChanged):
		return http.StatusUnprocessableEntity, "no_such_file"
	case errors.Is(err, session.ErrNotLive), errors.Is(err, session.ErrElsewhere), errors.Is(err, session.ErrSubagent):
		return http.StatusConflict, "not_live"
	case errors.Is(err, context.DeadlineExceeded):
		return http.StatusGatewayTimeout, "timeout"
	case access.IsDenied(err):
		return http.StatusForbidden, "no_permission"
	case errors.Is(err, pathsafe.ErrDenied), errors.Is(err, pathsafe.ErrOutside):
		return http.StatusForbidden, "outside"
	case errors.Is(err, files.ErrTooLarge), errors.Is(err, files.ErrTooLong):
		return http.StatusRequestEntityTooLarge, "too_large"
	case errors.Is(err, session.ErrBadRequest), errors.Is(err, files.ErrBad), errors.Is(err, files.ErrNotFile),
		errors.Is(err, pathsafe.ErrRelative), errors.Is(err, files.ErrChecksum):
		return http.StatusBadRequest, "bad_request"
	}
	return http.StatusInternalServerError, "internal"
}
