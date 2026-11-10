// Package satori is Satori with ir (docs/satori.md, docs/next/draft.md 三): apps talk to Iridium
// in the Satori protocol. An app (platform: its ID) speaks as one of its logins (user.id) to the
// sessions (channels), grouped by the web's folder groups (guilds). HTTP POST /v1/{resource}.{method}
// forwards to Iridium's own logic through Backend; the WebSocket /v1/events carries what the
// sessions say as events, kept in memory 24 hours for a resume by sn.
package satori

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/session"
)

// Request headers naming the login a request speaks as.
const (
	HeaderPlatform = "Satori-Platform"
	HeaderUser     = "Satori-User-ID"
)

// Adapter is login.adapter.
const Adapter = "iridium"

// Features are login.features: the APIs served.
var Features = []string{"message.create", "message.get", "message.list", "channel.get", "channel.list",
	"guild.get", "guild.list", "login.get", "user.get", "upload.create"}

// Caller is who a request comes from: an app (by its TOKEN) and, past the headers, its login.
type Caller struct {
	App, AppName     string
	Login, LoginName string
	// Only: the connection is this login's alone (a browser of ir_web, the host's page): its
	// event stream carries this login's events only.
	Only bool
}

// LoginInfo is an approved login of an app.
type LoginInfo struct {
	ID, Name string
}

// SendResult is what a message sent did, as POST /api/sessions/{id}/send answers it: its input's
// id, whether it waits in the queue, and the session it went to (another one after /clear).
type SendResult = api.SendReply

// SendOpts are what Iridium's own extension of message.create (its "_data") may add: whether it
// joins a running turn (apps' always do), and attachments by ref.
type SendOpts struct {
	Steer       *bool    `json:"steer,omitempty"`
	Attachments []string `json:"attachments,omitempty"`
	Caller      string   `json:"caller,omitempty"`
}

// Backend is what the protocol needs of Iridium; the server implements it. Errors go out as
// Fault says.
type Backend interface {
	// Who is the app a request comes with (its TOKEN, through the gate) and the login it named, if
	// approved (Satori-User-ID, checked before); ok false: not an app's TOKEN.
	Who(r *http.Request) (c Caller, ok bool)
	// AppByToken is the app whose TOKEN tok is (IDENTIFY's token).
	AppByToken(r *http.Request, tok string) (c Caller, ok bool)
	// Logins are app's approved logins, oldest first.
	Logins(app string) []LoginInfo
	// Track ends a connection of app (closeFn) when the app is removed or its TOKEN replaced.
	Track(r *http.Request, app string, closeFn func()) (untrack func())
	// User is someone by ID: an app, a login of ir_web, the host.
	User(id string) (User, bool)

	// Sessions lists the sessions; Session is one (an error Fault calls not there, if not).
	Sessions(ctx context.Context) ([]api.Session, error)
	Session(id string) (*api.SessionState, error)
	// Workspaces are the web's folder groups.
	Workspaces() ([]api.Workspace, error)
	// Sees: c may see session id. What it may not see is not there for it.
	Sees(c Caller, id string) bool
	// Messages is a page of session id's transcript (Manager.Messages), its blocks' files named.
	Messages(id string, before, after int64) (api.MessagePage, error)
	// Full is the whole text of the block at at.
	Full(id, at string) (string, error)
	// Send sends text with attachments (file refs of the session) to session id as c.
	Send(ctx context.Context, c Caller, id, text string, refs []string, o SendOpts) (SendResult, error)
	// Watch sends the states keys name to an event stream (Iridium's own, op STATE): what ir_web
	// shows beyond chat (the session list, a session's whole state, drafts), till ctx ends.
	Watch(ctx context.Context, r *http.Request, c Caller, keys []string, emit func(kind string, data []byte) error) error
	// Answer answers an ask's card as c.
	Answer(ctx context.Context, c Caller, a api.CardAnswer) error
	// Upload stores a file for session id: its ref.
	Upload(c Caller, id, name string, size int64, body io.Reader) (string, error)
	// File is the path of file ref of session id, if c may take it.
	File(c Caller, id, ref string) (string, error)
	// ServeFile serves the file at path.
	ServeFile(w http.ResponseWriter, r *http.Request, path string)
	// Mask masks the credentials' values in text.
	Mask(text string) string
	// Fault is how an error from the above goes out: its status and code.
	Fault(err error) (status int, code string)
}

// Keep is how long events wait in the outbox for a resume; KeepMax how many at most.
var (
	Keep    = 24 * time.Hour
	KeepMax = 10000
)

// Service serves /v1/ and turns the sessions' notices into events.
type Service struct {
	b    Backend
	logf func(string, ...any)
	out  *outbox

	qmu   sync.Mutex
	queue []session.Notice
	qwake chan struct{}
	stop  chan struct{}
	done  chan struct{}
	once  sync.Once

	mu      sync.Mutex
	held    map[string]said             // session: the agent's last message this turn, not sent yet
	origin  map[string]string           // session: who began its turn
	started map[string]time.Time        // session: when its turn began
	picks   map[string]map[int][]string // an ask's card: the options pressed so far, by group
	words   map[string]map[int]string   // an ask's card: own words so far, by group
}

type said struct {
	text  string
	msg   *Message // its block's, if found
	files []api.FileRef
}

// New starts the service; feed it the sessions' notices with Notice, Close it at the end.
func New(b Backend, logf func(string, ...any)) *Service {
	if logf == nil {
		logf = log.Printf
	}
	s := &Service{b: b, logf: logf, out: newOutbox(), qwake: make(chan struct{}, 1), stop: make(chan struct{}), done: make(chan struct{}),
		held: map[string]said{}, origin: map[string]string{}, started: map[string]time.Time{}, picks: map[string]map[int][]string{}, words: map[string]map[int]string{}}
	go s.work()
	return s
}

// Close stops the service.
func (s *Service) Close() {
	s.once.Do(func() { close(s.stop) })
	<-s.done
}

// Error is a refusal: its status, code and why.
type Error struct {
	Status        int
	Code, Message string
}

func (e *Error) Error() string { return e.Message }

func refuse(status int, code, msg string) error {
	return &Error{Status: status, Code: code, Message: msg}
}

func bad(msg string) error { return refuse(http.StatusBadRequest, "bad_request", msg) }

// ErrNoSession is a channel not there (or not to be seen).
var errNoSession = refuse(http.StatusUnprocessableEntity, "no_such_session", "no such session")

type errorBody struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
}

func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Service) fail(w http.ResponseWriter, err error) {
	var e *Error
	if !errors.As(err, &e) {
		status, code := s.b.Fault(err)
		e = &Error{Status: status, Code: code, Message: err.Error()}
		if status >= 500 {
			s.logf("satori: %v", err)
		}
	}
	reply(w, e.Status, errorBody{Code: e.Code, Message: e.Message})
}

type method func(s *Service, ctx context.Context, c Caller, body []byte) (any, error)

var methods = map[string]method{
	"message.create": (*Service).messageCreate,
	"message.get":    (*Service).messageGet,
	"message.list":   (*Service).messageList,
	"channel.get":    (*Service).channelGet,
	"channel.list":   (*Service).channelList,
	"guild.get":      (*Service).guildGet,
	"guild.list":     (*Service).guildList,
	"login.get":      (*Service).loginGet,
	"user.get":       (*Service).userGet,
	"upload.create":  nil, // multipart: served on its own
}

// ServeHTTP serves /v1/: the API, the event stream, meta, the proxy and the internal routes.
func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/")
	switch {
	case path == "events":
		s.serveEvents(w, r)
		return
	case strings.HasPrefix(path, "proxy/"):
		s.serveProxy(w, r, strings.TrimPrefix(r.URL.Path, "/v1/proxy/"))
		return
	case path == "meta":
		if r.Method != http.MethodPost {
			s.fail(w, refuse(http.StatusMethodNotAllowed, "method", "use POST"))
			return
		}
		c, ok := s.b.Who(r)
		if !ok {
			s.fail(w, notApp)
			return
		}
		ls := s.logins(c.App, nil)
		if c.Only {
			ls = slices.DeleteFunc(ls, func(l Login) bool { return l.User == nil || l.User.ID != c.Login })
		}
		reply(w, http.StatusOK, Meta{Logins: ls, ProxyURLs: []string{}})
		return
	case strings.HasPrefix(path, "internal/"):
		s.serveInternal(w, r, strings.TrimPrefix(path, "internal/"))
		return
	}
	m, known := methods[path]
	if !known {
		s.fail(w, refuse(http.StatusNotFound, "no_such_api", "no API "+path))
		return
	}
	if r.Method != http.MethodPost {
		s.fail(w, refuse(http.StatusMethodNotAllowed, "method", "use POST"))
		return
	}
	c, err := s.speaker(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	if path == "upload.create" {
		out, err := s.uploadCreate(r, c)
		if err != nil {
			s.fail(w, err)
			return
		}
		reply(w, http.StatusOK, out)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		s.fail(w, bad("the body: "+err.Error()))
		return
	}
	out, err := m(s, r.Context(), c, body)
	if err != nil {
		s.fail(w, err)
		return
	}
	reply(w, http.StatusOK, out)
}

var notApp = refuse(http.StatusForbidden, "no_login", "an app's TOKEN, or a login of ir_web")

// speaker is the login a request speaks as: Satori-Platform the app's ID, Satori-User-ID one of
// its logins (approved: the server refused it before, if not).
func (s *Service) speaker(r *http.Request) (Caller, error) {
	c, ok := s.b.Who(r)
	if !ok {
		return c, notApp
	}
	if p := r.Header.Get(HeaderPlatform); p != c.App {
		return c, refuse(http.StatusForbidden, "no_such_login", fmt.Sprintf("%s is this app's ID (%s)", HeaderPlatform, c.App))
	}
	if c.Login == "" || r.Header.Get(HeaderUser) != c.Login {
		return c, refuse(http.StatusForbidden, "no_such_login", HeaderUser+" names the login")
	}
	if c.LoginName == "" {
		c.LoginName = c.Login
		for _, l := range s.b.Logins(c.App) {
			if l.ID == c.Login {
				c.LoginName = l.Name
			}
		}
	}
	return c, nil
}

func decode(body []byte, v any) error {
	if len(strings.TrimSpace(string(body))) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, v); err != nil {
		return bad("the body: " + err.Error())
	}
	return nil
}

// logins are app's approved logins as Satori's Login, sn by place unless sns gives one.
func (s *Service) logins(app string, sns map[string]int64) []Login {
	out := []Login{}
	for i, l := range s.b.Logins(app) {
		sn := int64(i + 1)
		if v, ok := sns[l.ID]; ok {
			sn = v
		}
		out = append(out, loginOf(app, l, sn, true))
	}
	return out
}

func loginOf(app string, l LoginInfo, sn int64, full bool) Login {
	out := Login{SN: sn, Platform: app, User: &User{ID: l.ID, Name: l.Name}}
	if full {
		on := StatusOnline
		out.Status, out.Adapter, out.Features = &on, Adapter, slices.Clone(Features)
	}
	return out
}

func (s *Service) loginGet(_ context.Context, c Caller, _ []byte) (any, error) {
	sn := int64(1)
	for i, l := range s.b.Logins(c.App) {
		if l.ID == c.Login {
			sn = int64(i + 1)
		}
	}
	return loginOf(c.App, LoginInfo{ID: c.Login, Name: c.LoginName}, sn, true), nil
}

func (s *Service) userGet(_ context.Context, c Caller, body []byte) (any, error) {
	var req struct {
		UserID string `json:"user_id"`
	}
	if err := decode(body, &req); err != nil {
		return nil, err
	}
	if req.UserID == "" {
		return nil, bad("user_id")
	}
	if u, ok := agentUser(api.Agent(req.UserID)); ok {
		return u, nil
	}
	for _, l := range s.b.Logins(c.App) {
		if l.ID == req.UserID {
			return User{ID: l.ID, Name: l.Name}, nil
		}
	}
	if u, ok := s.b.User(req.UserID); ok {
		return u, nil
	}
	return nil, refuse(http.StatusUnprocessableEntity, "no_such_user", "no user "+req.UserID)
}
