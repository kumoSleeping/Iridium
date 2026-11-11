package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode"

	"github.com/kumoSleeping/iridium/internal/access"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/discover"
	"github.com/kumoSleeping/iridium/internal/files"
	"github.com/kumoSleeping/iridium/internal/gate"
	"github.com/kumoSleeping/iridium/internal/pathsafe"
	"github.com/kumoSleeping/iridium/internal/satori"
	"github.com/kumoSleeping/iridium/internal/session"
	"github.com/kumoSleeping/iridium/internal/store"
	"github.com/kumoSleeping/iridium/internal/transcript"
)

// Satori with ir (package satori, docs/satori.md): /v1/ on the TCP address, behind the gate and the
// login check (withLogin), and on the socket and for an agent's ir at another machine (irCaller);
// this file is the server as its Backend.

// withSatori serves /v1/ by package satori, the rest by next.
func (s *Server) withSatori(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// /v1/internal/<path>: Iridium's own API (Satori's "internal" routes, spec advanced/internal),
		// served by next. Satori-Platform and Satori-User-ID, if given, must be the caller's.
		// (button is the Satori package's own.)
		if rest, ok := strings.CutPrefix(r.URL.Path, "/v1/internal/"); ok && rest != "button" {
			if c, ok := (satoriBackend{s}).Who(r); ok {
				if p := r.Header.Get(satori.HeaderPlatform); p != "" && p != c.App {
					refuse(w, http.StatusForbidden, "no_such_login", satori.HeaderPlatform+" is not this caller's")
					return
				}
				if u := r.Header.Get(satori.HeaderUser); u != "" && c.Login != "" && u != c.Login {
					refuse(w, http.StatusForbidden, "no_such_login", satori.HeaderUser+" is not this caller's")
					return
				}
			}
			next.ServeHTTP(w, r)
			return
		}
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
	if cl, ok := c.Self.(caller); ok {
		return cl
	}
	return caller{Identity: gate.Identity{App: c.App, Login: c.Login, Name: c.AppName}}
}

// irCaller is ir's caller in Satori: the host's ir on the socket, an agent's (as the login its
// session's turn is for, socketHandler), or an agent's at another machine (its session's TOKEN:
// as that turn's login too, within its machine's permissions). What it may do is cl's own
// (Self), the same as on /v1/internal; it speaks as that login. ok false: not ir.
func (b satoriBackend) irCaller(cl caller) (satori.Caller, bool) {
	if !cl.socket && cl.session == "" {
		return satori.Caller{}, false
	}
	id := cl.Identity
	if !cl.socket {
		o, ok := b.s.originOf(cl.session)
		if !ok {
			return satori.Caller{}, false
		}
		id = o
	}
	sc := satori.Caller{App: id.App, AppName: b.s.Tag(id.App), Login: id.Login, LoginName: id.Name, Only: true, Self: cl}
	switch {
	case id.Host():
		sc.AppName, sc.Login, sc.LoginName = b.s.Tag(gate.HostID), gate.HostID, b.s.Tag(gate.HostID)
	case id.Web():
		sc.AppName = gate.WebApp
	case id.Login == "":
		return satori.Caller{}, false
	}
	return sc, true
}

// Who: an app by its TOKEN (and the login it named); a login of ir_web likewise, by ir_web's
// TOKEN (package webapp forwards a browser so); the host (its own page, with no public origin) as
// the login "local" of the platform "local". Every app alike (draft 一): ir_web speaks Satori as
// any other, one login a connection.
func (b satoriBackend) Who(r *http.Request) (satori.Caller, bool) {
	if cl, ok := r.Context().Value(callerKey{}).(caller); ok && (cl.socket || cl.session != "") {
		return b.irCaller(cl)
	}
	id, ok := gate.FromContext(r.Context())
	switch {
	case !ok || id.Session != "":
		return satori.Caller{}, false
	case id.Host():
		return satori.Caller{App: gate.HostID, AppName: id.Name, Login: gate.HostID, LoginName: id.Name, Only: true}, true
	case id.Web():
		return satori.Caller{App: gate.WebApp, AppName: gate.WebApp, Login: id.Login, LoginName: id.Name, Only: true}, true
	case id.App == "" || id.App == gate.WebApp:
		return satori.Caller{}, false
	}
	return satori.Caller{App: id.App, AppName: id.Name, Login: id.Login}, true
}

func (b satoriBackend) AppByToken(r *http.Request, tok string) (satori.Caller, bool) {
	info, ok := b.s.gate().ByTokenFrom(r, tok)
	if !ok || info.Host() || info.Kind != gate.KindChannel {
		return satori.Caller{}, false
	}
	return satori.Caller{App: info.ID, AppName: info.Name}, true
}

func (b satoriBackend) Logins(app string) []satori.LoginInfo {
	switch app {
	case gate.HostID:
		return []satori.LoginInfo{{ID: gate.HostID, Name: b.s.Tag(gate.HostID)}}
	case gate.WebApp:
		var out []satori.LoginInfo
		for _, e := range b.s.gate().Access().Endpoints {
			if e.Kind == gate.KindPanel {
				out = append(out, satori.LoginInfo{ID: e.ID, Name: e.Name})
			}
		}
		return out
	}
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
	// A login's own connection ends as it is removed; a browser's also as it logs out (package
	// webapp ends the request it forwarded).
	if id, ok := gate.FromContext(r.Context()); ok && id.App == app {
		return b.s.gate().Track(r, closeFn)
	}
	return b.s.gate().Track(r.WithContext(gate.NewContext(r.Context(), gate.Identity{App: app})), closeFn)
}

// Watch sends the states keys name (and the drafts) to a connection of ir_web or the host, as
// the page's event stream had them (Iridium's own, op STATE).
func (b satoriBackend) Watch(ctx context.Context, r *http.Request, c satori.Caller, keys []string, emit func(kind string, data []byte) error) error {
	cl := callerOf(r)
	if cl.App != c.App {
		cl = callerAs(c)
	}
	return b.s.watchStates(ctx, cl, keys, emit)
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

func (b satoriBackend) Patch(ctx context.Context, c satori.Caller, id string, p api.SessionPatch) error {
	cl := callerAs(c)
	if _, err := b.s.using(cl, id); err != nil {
		return err
	}
	// An agent's name never covers one a person gave (as ir rename did).
	if p.Name != nil && (cl.agent || cl.session != "") {
		p.ByAgent = true
	}
	err := b.s.patch(ctx, id, p)
	if errors.Is(err, errPatchManager) {
		return errors.Unwrap(err)
	}
	return err
}

// satoriKeeper keeps Satori's events in the store's outbox (plugin "satori").
type satoriKeeper struct{ st *store.Store }

func (k satoriKeeper) Keep(session, typ string, data []byte) error {
	_, err := k.st.Post("satori", session, typ, string(data))
	return err
}

func (k satoriKeeper) Kept(since time.Time) ([][]byte, error) {
	l, err := k.st.OutboxAfter("satori", 0)
	var out [][]byte
	for _, o := range l {
		if o.At >= since.UnixMilli() {
			out = append(out, []byte(o.Data))
		}
	}
	return out, err
}

func (k satoriKeeper) Drop(before time.Time) error { return k.st.PruneOutbox(time.Since(before)) }

// watchList gives Satori the session list as it changes, for its channel events, till ctx ends.
func (s *Server) watchList(ctx context.Context) {
	sub := s.hub.Subscribe([]string{"list"})
	defer sub.Close()
	_ = sub.Run(ctx, func(event string, data []byte) error {
		var st struct {
			Key   string          `json:"key"`
			Value json.RawMessage `json:"value"`
		}
		if event != "state" || json.Unmarshal(data, &st) != nil || st.Key != "list" {
			return nil
		}
		var l []api.Session
		if json.Unmarshal(st.Value, &l) == nil {
			s.sat.Sessions(l)
		}
		return nil
	}, func() error { return nil }, func() {})
}

// satoriUser is the caller as Satori's user of a message it wrote.
func (s *Server) satoriUser(c caller) satori.User {
	switch {
	case c.Host():
		return satori.User{ID: gate.HostID, Name: s.Tag(gate.HostID)}
	case c.Login != "":
		return satori.User{ID: c.Login, Name: c.Name}
	}
	return satori.User{ID: c.App, Name: s.Tag(c.App)}
}

func (b satoriBackend) SeesGuild(c satori.Caller, w api.Workspace) bool {
	cl := callerAs(c)
	return cl.Host() || len(b.s.scopeWorkspaces(b.s.permsFor(cl), []api.Workspace{w})) == 1
}

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

func (b satoriBackend) Send(ctx context.Context, c satori.Caller, id, text string, refs []string, o satori.SendOpts) (satori.SendResult, error) {
	s, cl := b.s, callerAs(c)
	req := api.Send{Text: text, Attachments: refs, Caller: o.Caller}
	// ir: as POST /v1/internal/sessions/{id}/send has it. It steers unless told to queue; an
	// agent's always steers (queued, it would wait for a person).
	if _, ok := c.Self.(caller); ok {
		req.Steer = o.Steer == nil || *o.Steer || cl.agent || cl.socket && cl.session != "" || !cl.socket
		return s.sendAs(ctx, cl, s.fromOf(cl, o.Caller), id, req)
	}
	// An app's message joins a running turn, as the chat layer's did; ir_web's and the host's
	// steer or queue as the person chose.
	from := session.FromLogin(c.App, c.Login, sourceTag(c))
	if cl.Host() || cl.Web() {
		from = s.fromOf(cl, o.Caller)
	} else {
		req.Steer = true
	}
	if o.Steer != nil && (cl.Host() || cl.Web()) {
		req.Steer = *o.Steer
	}
	return s.sendAs(ctx, cl, from, id, req)
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
	case errors.Is(err, store.ErrUserNamed):
		return http.StatusConflict, "named" // an agent's name over one a person gave
	case errors.Is(err, discover.ErrNoName):
		return http.StatusBadRequest, "bad_request"
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
