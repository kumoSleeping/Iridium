// Package satori is Satori with ir (docs/satori.md, docs/next/draft.md 三): apps talk to Iridium
// in the Satori protocol. An app (platform: its ID) speaks as one of its logins (user.id) to the
// sessions (channels), grouped by the web's folder groups (guilds). HTTP POST /v1/{resource}.{method}
// forwards to Iridium's own logic through Backend; the WebSocket /v1/events carries what the
// sessions say as events, kept in memory 24 hours for a resume by sn.
package satori

import (
	"context"
	"encoding/base64"
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
	"github.com/kumoSleeping/iridium/internal/channel"
	"github.com/kumoSleeping/iridium/internal/store"
)

// Request headers naming the login a request speaks as.
const (
	HeaderPlatform = "Satori-Platform"
	HeaderUser     = "Satori-User-ID"
)

// Adapter is login.adapter.
const Adapter = "iridium"

// Features are login.features: the APIs served.
var Features = []string{"message.create", "message.get", "message.list", "message.update", "message.delete", "channel.get", "channel.list", "channel.create", "channel.update", "channel.delete",
	"guild.get", "guild.list", "guild.member.get", "guild.member.list", "login.get", "user.get", "upload.create",
	"reaction.create", "reaction.delete", "reaction.clear", "reaction.list"}

// Caller is who a request comes from: an app (by its TOKEN) and, past the headers, its login.
type Caller struct {
	App, AppName     string
	Login, LoginName string
	// Only: the connection is this login's alone (a browser of ir_web, the host's page): its
	// event stream carries this login's events only.
	Only bool
	// Self: the backend's own caller the request came as, when it knows more than the above (the
	// host's ir on the socket, an agent's ir); opaque here.
	Self any
}

// LoginInfo is an approved login of an app.
type LoginInfo struct {
	ID, Name string
}

// SendResult is what a message sent did (message.create's _data): its input's
// id, whether it waits in the queue, and the session it went to (another one after /clear).
type SendResult = api.SendReply

// SendOpts are what Iridium's own extension of message.create (its "_data") may add: attachments
// by ref, the words as plain text (Text, in place of content, escaped here), and files by their
// path at the host (Paths, the backend's to take or refuse: ir on the socket).
type SendOpts struct {
	Attachments []string `json:"attachments,omitempty"`
	Caller      string   `json:"caller,omitempty"`
	Text        *string  `json:"text,omitempty"`
	Paths       []string `json:"paths,omitempty"`
	// ID: the input id the sender chose (api.Send's), for a send it may try again.
	ID string `json:"id,omitempty"`
	// Quote: the message it answers, from its <quote id="…"/> (api.Send's).
	Quote int64 `json:"-"`
}

// Resolver is a Backend that takes a session's id as a caller may write it: whole or the start
// of one (docs/api.md "会话 ID"). Without it ids are taken as they are.
type Resolver interface {
	Resolve(ctx context.Context, c Caller, id string) (string, error)
}

// AskSeer is a Backend that lets c answer session's asks where it does not see the session
// itself: in a channel the session's agent is in, which c sees and may act in (docs/channels.md
// "提问").
type AskSeer interface {
	SeesAsk(c Caller, session string) bool
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
	// SeesGuild: c may see folder group w (its scope has the folder, or leads there).
	SeesGuild(c Caller, w api.Workspace) bool
	// Sees: c may see session id. What it may not see is not there for it.
	Sees(c Caller, id string) bool
	// SeesListed is Sees of a session of the list, by its row: a list asks it of every row, and
	// Sees makes each one's whole state for it (otae, 2026-11-25: a page of the list in 10 s).
	SeesListed(c Caller, ss api.Session) bool
	// Messages is a page of session id's transcript (Manager.Messages), its blocks' files named.
	Messages(id string, before, after int64) (api.MessagePage, error)
	// Full is the whole text of the block at at.
	Full(id, at string) (string, error)
	// Send sends text with attachments (file refs of the session) to session id as c.
	Send(ctx context.Context, c Caller, id, text string, refs []string, o SendOpts) (SendResult, error)
	// Watch sends the states keys name to an event stream (Iridium's own, op STATE): what ir_web
	// shows beyond chat (the session list, a session's whole state, drafts), till ctx ends.
	Watch(ctx context.Context, r *http.Request, c Caller, keys []string, emit func(kind string, data []byte) error) error
	// Create starts session req as c (channel.create): its id.
	Create(ctx context.Context, c Caller, req api.NewSession) (string, error)
	// Patch changes session id as c may (a rename, archiving it, its place in the list).
	Patch(ctx context.Context, c Caller, id string, p api.SessionPatch) error
	// Marks are the folders marked by hand; MarkFolder marks the folder of path (as grouped) as c.
	Marks() []api.FolderMark
	MarkFolder(c Caller, path string, p api.FolderPatch) error
	// EditQueued changes the text of input of session id while it waits in the queue; nil drops it.
	EditQueued(c Caller, id, input string, text *string) error
	// EditGiven changes message seq of channel ch (its words were old) where it waits in the
	// queues of the agents it was given to, each under an input of its own (a channel of several
	// agents, or of none of its own); nil takes it back. How many queues had it.
	EditGiven(c Caller, ch string, seq int64, old string, text *string) (int, error)
	// Answer answers an ask's card as c.
	Answer(ctx context.Context, c Caller, a api.CardAnswer) error
	// Press gives button id, attached to session sid's page from source from, pressed by c, back
	// to where it came from; app: an app's, for its interaction/button.
	Press(ctx context.Context, c Caller, sid, from, id string) (app string, err error)
	// Upload stores a file for session id: its ref.
	Upload(c Caller, id, name string, size int64, body io.Reader) (string, error)
	// File is the path of file ref of session id, if c may take it.
	File(c Caller, id, ref string) (string, error)
	// ServeFile serves the file at path.
	ServeFile(w http.ResponseWriter, r *http.Request, path string)
	// Mask masks the credentials' values in text.
	Mask(text string) string
	// LinkFiles is the files text links (ir:<id>) that are still given.
	LinkFiles(text string) []api.FileRef
	// Fault is how an error from the above goes out: its status and code.
	Fault(err error) (status int, code string)
}

// Keep is how long events wait in the outbox for a resume; KeepMax how many at most.
var (
	Keep    = 24 * time.Hour
	KeepMax = 10000
)

// RecordGuard is a Backend that keeps some records from some callers: an agent's ir does not
// read the record of another agent it shares a channel with (docs/channels.md "隔离").
type RecordGuard interface {
	MayReadRecord(c Caller, session string) bool
}

// Service serves /v1/ and turns the sessions' notices into events.
type Service struct {
	b    Backend
	logf func(string, ...any)
	out  *outbox

	qmu   sync.Mutex
	queue []noticed
	qwake chan struct{}
	stop  chan struct{}
	done  chan struct{}
	once  sync.Once

	ch  channels
	log *channel.Service // what was said in the channels (Channels)
	// onSaid is told each message an agent said, as it is sent out (OnSaid).
	onSaid    func(channel.Message)
	onTurnEnd func(session, reply string, waiting []string, background bool)
	onTaken   func(session, input string)
	onBegan   func(session string)
	onAsk     func(ch string, seq int64, session string, a api.Ask)
	// onLive is told each message of an agent's own channel as it is told live (OnLive).
	onLive func(m channel.Message, passing bool)
	// onLiveGone is told a message told so that is taken away again (one made as a turn began,
	// of a turn that came to nothing).
	onLiveGone func(m channel.Message)

	mu      sync.Mutex
	held    map[string]channel.Message  // session: the agent's last message this turn, not sent yet
	heard   map[string]int64            // session: the number of the last message heard from it
	origin  map[string]string           // session: who began its turn
	started map[string]time.Time        // session: when its turn began
	picks   map[string]map[int][]string // an ask's card: the options pressed so far, by group
	words   map[string]map[int]string   // an ask's card: own words so far, by group
	live    map[string]*saying          // session: what its agent is at in its turn (live.go); the worker's
}

// OnSaid has f told each thing an agent says in a channel, after the event of it is kept; f
// must not wait.
func (s *Service) OnSaid(f func(channel.Message)) { s.onSaid = f }

// OnLive has f told each message an agent says in its turn, in its own channel, as those who
// take messages live are told of it: as it begins, as it grows (passing: more follows soon), as
// its work goes on. f must not wait.
func (s *Service) OnLive(f func(m channel.Message, passing bool)) { s.onLive = f }

// OnLiveGone has f told each message told by OnLive that is no more. f must not wait.
func (s *Service) OnLiveGone(f func(m channel.Message)) { s.onLiveGone = f }

// OnTaken has f told each input given to a session's agent, in order with what the agent says
// (session.NoticeTaken): what f is told before a thing said is told were given before it.
func (s *Service) OnTaken(f func(session, input string)) { s.onTaken = f }

// OnTurnStart has f told each turn of a session that begins, in order with what its agent says.
func (s *Service) OnTurnStart(f func(session string)) { s.onBegan = f }

// OnAsk has f told each ask of a session's agent as its message is made in a channel it is told
// in (its own, or one it is in beside): the channel, the message's number. f must not wait.
func (s *Service) OnAsk(f func(ch string, seq int64, session string, a api.Ask)) { s.onAsk = f }

// OnTurnEnd has f told each turn of a session that ends, after its reply was said: the
// session, the reply's words ("" if it said none), and the inputs that still wait in its queue
// (session.Notice.Waiting), and whether background tasks of its agent went on as it ended
// (session.Notice.Background). A turn cut short by its process ending is told too, with no reply.
func (s *Service) OnTurnEnd(f func(session, reply string, waiting []string, background bool)) {
	s.onTurnEnd = f
}

// Note writes a line of Iridium's own into channel ch and tells those who see it (_data.kind
// "note"). ev: what it tells of, for a page to say in its own words (the message's _data.event).
func (s *Service) Note(ch, text, origin string, ev *channel.Event) int64 {
	m, isNew, err := s.log.Say(channel.Message{ChannelMessage: store.ChannelMessage{Channel: ch, Author: NoteUser, Kind: channel.KindNote, Text: text, Origin: origin}, D: channel.Data{Event: ev}})
	if err != nil || !isNew {
		return 0
	}
	s.tell(m, "note", origin, 0)
	return m.Seq
}

// NoteUser is the author of Iridium's own lines in a channel.
const NoteUser = "iridium"

// Channels gives the service the channels' log: what message.list reads and the events are of.
func (s *Service) Channels(l *channel.Service) {
	s.log = l
	l.SettleLive()
	// Found in its agent's record after it was said live: its block, for those who have it.
	// Not relayed via onLive: it runs out of order with turns and only anchors the record block.
	l.OnChanged(func(m channel.Message) { s.tellLiveLocal("message-updated", m, false, "") })
}

// New starts the service; feed it the sessions' notices with Notice, Close it at the end.
func New(b Backend, logf func(string, ...any)) *Service {
	if logf == nil {
		logf = log.Printf
	}
	s := &Service{b: b, logf: logf, out: newOutbox(logf), qwake: make(chan struct{}, 1), stop: make(chan struct{}), done: make(chan struct{}),
		held: map[string]channel.Message{}, heard: map[string]int64{}, origin: map[string]string{}, started: map[string]time.Time{}, picks: map[string]map[int][]string{}, words: map[string]map[int]string{}, live: map[string]*saying{}}
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
	"message.create":    (*Service).messageCreate,
	"message.get":       (*Service).messageGet,
	"message.list":      (*Service).messageList,
	"message.update":    (*Service).messageUpdate,
	"message.delete":    (*Service).messageDelete,
	"channel.get":       (*Service).channelGet,
	"channel.list":      (*Service).channelList,
	"channel.create":    (*Service).channelCreate,
	"channel.update":    (*Service).channelUpdate,
	"channel.delete":    (*Service).channelDelete,
	"guild.get":         (*Service).guildGet,
	"guild.list":        (*Service).guildList,
	"guild.member.get":  (*Service).guildMemberGet,
	"guild.member.list": (*Service).guildMemberList,
	"login.get":         (*Service).loginGet,
	"user.get":          (*Service).userGet,
	"upload.create":     nil, // multipart: served on its own
	"reaction.create":   (*Service).reactionCreate,
	"reaction.delete":   (*Service).reactionDelete,
	"reaction.clear":    (*Service).reactionClear,
	"reaction.list":     (*Service).reactionList,
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
	if p := r.Header.Get(HeaderPlatform); !samePlatform(p, c.App) {
		return c, refuse(http.StatusForbidden, "no_such_login", fmt.Sprintf("%s is %s or this app's ID (%s)", HeaderPlatform, Platform, c.App))
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

// Platform is what every login says it is on (kumo, 2026-11-16: as QQ's say qq), not the app's
// ID; Satori-Platform may say either.
const Platform = "iridium"

// irName and irAvatar are an app's login as it sees itself (a bot: Koishi's status shows it):
// Iridium, with Iridium's mark, in place (a data: URL needs no address of Iridium's nor its
// TOKEN). The page's logins and the host's are people: their own names.
const irName = "Iridium"

var irAvatar = "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64"><rect width="64" height="64" rx="14" fill="#2b2b2b"/><path d="M20 18h6v28h-6zM32 28h5.6v3.2c1.4-2.4 3.6-3.6 6.4-3.6v5.8c-3.8 0-6.2 1.6-6.2 6V46H32z" fill="#f4f4f4"/></svg>`))

// person: app's logins are people (the page's, the host's), not a bot of an app.
func person(app string) bool { return app == "ir_web" || app == "local" }

// samePlatform: what Satori-Platform says names app.
func samePlatform(p, app string) bool { return p == app || p == Platform }

func loginOf(app string, l LoginInfo, sn int64, full bool) Login {
	u := &User{ID: l.ID, Name: l.Name}
	if !person(app) {
		u = &User{ID: l.ID, Name: irName, Nick: l.Name, Avatar: irAvatar, IsBot: true}
	}
	out := Login{SN: sn, Platform: Platform, User: u}
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
	// An agent as a channel's member: its session, if c sees it.
	if id, ok := store.SessionOfMember(req.UserID); ok {
		if _, err := s.channel(c, id); err != nil {
			return nil, refuse(http.StatusUnprocessableEntity, "no_such_user", "no user "+req.UserID)
		}
		return *s.agentOf(id), nil
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
