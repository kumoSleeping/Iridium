// Package server is Iridium's HTTP API (docs/api.md), served on a loopback TCP address for the
// web page and on a unix socket (0600) for the local ir, the same API on both.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kumoSleeping/iridium/internal/access"
	"github.com/kumoSleeping/iridium/internal/agents"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/channel"
	"github.com/kumoSleeping/iridium/internal/config"
	"github.com/kumoSleeping/iridium/internal/deliver"
	"github.com/kumoSleeping/iridium/internal/discover"
	"github.com/kumoSleeping/iridium/internal/files"
	"github.com/kumoSleeping/iridium/internal/gate"
	"github.com/kumoSleeping/iridium/internal/hub"
	"github.com/kumoSleeping/iridium/internal/joinscript"
	"github.com/kumoSleeping/iridium/internal/pricing"
	"github.com/kumoSleeping/iridium/internal/remote"
	"github.com/kumoSleeping/iridium/internal/satori"
	"github.com/kumoSleeping/iridium/internal/session"
	"github.com/kumoSleeping/iridium/internal/store"
	"github.com/kumoSleeping/iridium/internal/transcript"
	"github.com/kumoSleeping/iridium/internal/update"
	"github.com/kumoSleeping/iridium/internal/voice"
	"github.com/kumoSleeping/iridium/internal/webapp"
)

type Server struct {
	// personas: the agents' hosts' personas, kept a while (personas.go).
	personas      personasCache
	browserRanges browserRangeLog // recently logged 206 range reads (file_browser.go)
	// choosing: the kinds whose choices are being asked for behind a reply (agentChoices), by
	// choicesKey.
	choosing sync.Map

	cfg config.Config
	m   *session.Manager
	hub *hub.Hub

	turns        origins      // whose turn each session runs (origin.go)
	terminalsSeq atomic.Int64 // the "terminals" state's count (terminalsChanged)
	// CLI runs the ir of a session at another machine (POST /v1/internal/cli); nil: none.
	CLI CLIAt
	// tokenFor stands in for the session manager's SessionToken in tests.
	tokenFor func(tok string) (endpointID, session string, ok bool)
	st       *store.Store
	logf     func(string, ...any)
	// ConfigPath is where PUT /v1/internal/public saves public.origin; "" saves it nowhere.
	ConfigPath string
	// Updater, if set, serves /v1/internal/update and carries the state "update".
	Updater *update.Updater
	// Prices, if set, prices the usage the agents gave no cost (GET /v1/internal/usage).
	Prices *pricing.Catalog

	cmu      sync.Mutex // one config change at a time (SaveConfig); guards cfg's settings
	gmu      sync.Mutex // one change of ir_web at a time
	g        *gate.Gate // the core's door and registry (access.go)
	w        atomic.Pointer[webapp.Web]
	webToken string     // ir_web's own TOKEN, which it speaks to the core with
	initErr  error      // ir_web for cfg.Public.Origin would not open; Listen gives it
	asks     *needs     // what waits for a person (needs.go)
	more     *moreNeeds // asks' first sightings (needs_more.go)
	pairs    *pairs     // apps asking to pair (pairing.go)
	vault    *vault     // the credentials' masks and allowed uses (routes_secrets.go)

	// Discover and Folders, if set, serve the workspaces and the folder picker (routes_discover.go).
	Discover *discover.Index
	Folders  discover.Folders
	// App is the web app on the TCP address, past the API; nil is the embedded build (webui).
	App http.Handler
	// Remote, if set, takes executors' connections at /v1/internal/exec (docs/exec.md).
	Remote *remote.Hub

	// bin: ir-computer as served here (binaries()).
	bin     *joinscript.Binaries
	binOnce sync.Once

	files  *files.Service   // routes_files.go
	sat    *satori.Service  // /v1/: Satori with ir (satori.go)
	ch     *channel.Service // what is said in the channels (docs/channels.md)
	chain  chains           // each channel's run of agents passing messages on (channels.go)
	goneOn goOns            // the paused messages a person went on with (channels.go goOn)

	viewers viewers // who is looking at each channel now (channels.go)
	dms     dms     // the channels that are two people's (channels.go)
	calls   calls   // the channels each agent was called in by a subscription (channels.go)
	answers answers // what each agent writes, said in turn where it was called (channels.go)
	twins   twins   // the messages of other channels that are ones agents are saying in their own (liveSaid)
	recalls recalls // the channels a message was taken back in (reactions.go)
	// channelsSeq: the state "channels", up by one at each change of a channel's members.
	channelsSeq atomic.Int64
	// scripts: the channels' scripts running and waiting (scripts.go); scriptsSeq, the state
	// "scripts", up by one at each change of a script or a run of one.
	scripts    scriptRuns
	scriptsSeq atomic.Int64
	// avatarsSeq: the state "avatars", up by one at each change of a face.
	avatarsSeq atomic.Int64
	// faceSeen: the version of each session's face as last looked at (faceFollows); faceMu guards it.
	// faceDue: the state "faces" is to be worked out again (faces.go facesSoon).
	faceMu   sync.Mutex
	faceSeen map[string]string
	faceDue  bool
	faceIDs  string // the hosted channels in the list, as last seen (faces.go facesOnList)
	// cardsSeq: the state "cards", up by one at each change of a card (its name, face).
	cardsSeq atomic.Int64
	// listDue: the list of sessions is to be made again shortly (listSoon).
	listDue atomic.Bool
	// memorySeq: the state "memory", up by one when an agent's memory changes; rolesSeq: the
	// state "roles", when a role does.
	memorySeq atomic.Int64
	rolesSeq  atomic.Int64
	// favoritesSeq: the state "favorites", up by one when anyone's favorites change; each page
	// asks again for its own (favorites.go).
	favoritesSeq atomic.Int64
	// stopList stops giving Satori the session list (its channel events).
	stopList func()

	// The channels' voice (routes_voice.go); voiceNow is the state "voice": the members of each
	// room with someone in it.
	voice    *voice.Rooms
	voiceMu  sync.Mutex
	voiceNow map[string][]voice.Member
}

// New is the API over m. Apps and logins live in m's store; on the TCP address ir_web (package
// webapp: passkeys, cookies, the page) stands in front of the core's TOKEN gate (package gate);
// the unix socket is the host.
func New(cfg config.Config, m *session.Manager, h *hub.Hub, logf func(string, ...any)) *Server {
	if logf == nil {
		logf = log.Printf
	}
	s := &Server{cfg: cfg, m: m, hub: h, st: m.Store(), files: newFiles(cfg, m, logf), logf: logf}
	s.calls.st = s.st
	s.calls.open()
	s.voice = voice.New(logf)
	s.voice.Changed = s.carryVoice
	h.Set("voice", map[string][]voice.Member{})
	s.asks = newNeeds(h)
	s.pairs = newPairs()
	s.pairs.open = cfg.Pairing
	s.publishPairing()
	s.vault = newVault()
	s.reloadSecrets()
	m.SetAnnotate(s.files.AnnotateBlocks) // a page of messages and a state's tail name files
	s.ch = channel.New(s.st, m, logf)
	// Known to have a message taken back before it is written: an agent given something between
	// the two was told of none, then never (recalledFor; TestReactionsAndRecall, one run in twenty).
	s.ch.OnRecall = s.recalls.add
	s.sat = satori.New(satoriBackend{s}, logf)
	s.sat.Channels(s.ch)
	s.sat.OnSaid(s.routeSaid)
	s.sat.OnAsk(s.askCalls) // manual.go
	s.sat.OnLive(s.liveSaid)
	s.sat.OnLiveGone(s.liveGone)
	// A turn's start and end in order with what its agent wrote (answers), so that its words
	// are said where it was called before the call closes.
	s.sat.OnTaken(s.answers.took)
	s.sat.OnTurnStart(func(id string) { s.answers.add(id, func() { s.twins.drop(id); s.turnBegan(id) }) })
	s.sat.OnTurnEnd(func(id, _ string, _ []string, background bool) {
		taken, self := s.answers.ended(id)
		s.answers.add(id, func() { s.turnEnded(id, taken, self, background) })
	})
	m.SetInChannel(s.inSharedChannel)
	m.SetChannelOf(s.channelOf)
	m.SetSpawned(s.spawnedOf)
	m.SetBare(s.bareChannels, s.bareChannel)
	m.SetChannelLast(s.channelLast)
	s.ch.OnAdded(s.channelAdded)
	s.ch.OnList(s.channelListChanged)
	m.SetCleared(s.cleared)
	m.SetCreated(func(ctx context.Context, id string, req api.NewSession) error {
		c, _ := ctx.Value(cardCaller{}).(caller)
		return s.carded(c, id, req)
	})
	m.SetSelf(s.selfText)
	m.SetSpeaks(func(id, ch string) bool { return s.ch.Speaks(ch, id) })
	s.sat.Keep(satoriKeeper{s.st})
	m.Watch(s.sat.Notice)
	m.Watch(s.faceFollows)
	// Each agent turn's tools counted: turns of words alone passed on are paused (deliver.MaxIdle).
	m.Watch(s.chain.saw)
	// A call held for its agent's background tasks is let go once they are over (calls.settle).
	m.Watch(s.tasksChanged)
	lctx, lstop := context.WithCancel(context.Background())
	s.stopList = lstop
	go s.watchList(lctx)
	go s.cronLoop(lctx)
	go s.missedLoop(lctx)
	s.wireNeeds()
	if err := s.openGate(); err != nil {
		s.initErr = err
		return s
	}
	web, err := s.newWeb(cfg.Public.Origin)
	if err != nil {
		s.initErr = err
		web, _ = s.newWeb("")
	}
	s.w.Store(web)
	h.Set("update", s.updateState())
	h.Set("terminals", int64(0))
	s.atsChanged() // who was named and has not read it (ats.go)
	return s
}

// Close closes ir_web and the gate and stops Satori's events.
func (s *Server) Close() {
	if w := s.web(); w != nil {
		w.Close()
	}
	if s.g != nil {
		s.g.Close()
	}
	if s.stopList != nil {
		s.stopList()
	}
	s.answers.close()
	s.sat.Close()
}

// Handler is the API. local: requests come from the unix socket, from the local ir.
func (s *Server) Handler(local bool) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/internal/agents", func(w http.ResponseWriter, r *http.Request) {
		l := agents.Describe()
		for i := range l {
			l[i].Missing = s.m.Missing(l[i].Agent)
		}
		reply(w, http.StatusOK, l)
	})
	// What carries transcripts goes out with the credentials' values masked (routes_secrets.go).
	mux.HandleFunc("GET /v1/internal/sessions/{id}", s.masked(s.get))
	mux.HandleFunc("GET /v1/internal/sessions/{id}/first", s.first)
	mux.HandleFunc("GET /v1/internal/sessions/{id}/image", s.image)
	mux.HandleFunc("POST /v1/internal/sessions/{id}/interrupt", s.stopping(s.interrupt))
	mux.HandleFunc("POST /v1/internal/sessions/{id}/tasks/{task}/stop", s.use(s.stopTask))
	mux.HandleFunc("POST /v1/internal/sessions/{id}/stop", s.use(s.stop))
	mux.HandleFunc("POST /v1/internal/sessions/{id}/manual", s.use(s.askManual)) // manual.go
	mux.HandleFunc("GET /v1/internal/sessions/{id}/manual", s.use(s.manualState))
	// The session's agent's own commands (an app sets its menu from them).
	mux.HandleFunc("GET /v1/internal/sessions/{id}/commands", func(w http.ResponseWriter, r *http.Request) {
		st, err := s.m.State(r.PathValue("id"))
		if err != nil {
			s.error(w, err)
			return
		}
		reply(w, http.StatusOK, st.Commands)
	})
	s.appRoutes(mux)
	s.secretRoutes(mux)
	s.githubRoutes(mux)
	s.updateRoutes(mux)
	s.settingsRoutes(mux)
	s.sessionRoutes(mux)
	s.channelRoutes(mux) // channels.go
	s.voiceRoutes(mux)
	mux.HandleFunc("GET /v1/internal/exec", s.execConnect)
	s.pairRoutes(mux)
	s.serverFaceRoutes(mux)
	s.machineRoutes(mux)
	s.discoverRoutes(mux)
	s.fileRoutes(mux, local)
	if !local {
		s.cliRoutes(mux)
	}
	if local {
		// Satori too: ir speaks it (ir api), as the login it acts for (irCaller).
		return s.socketHandler(s.withSatori(s.sessionGuard(mux)))
	}
	return s.webHandler(s.sessionGuard(mux))
}

// Listen opens the TCP address and the unix socket. A socket file left by a daemon that is gone
// is replaced; one a running daemon answers on is an error.
func (s *Server) Listen() (tcp, unix net.Listener, err error) {
	if s.initErr != nil {
		return nil, nil, s.initErr
	}
	tcp, err = net.Listen("tcp", s.cfg.Listen)
	if err != nil {
		return nil, nil, err
	}
	path := s.cfg.SocketPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		tcp.Close()
		return nil, nil, err
	}
	if c, err := net.Dial("unix", path); err == nil {
		c.Close()
		tcp.Close()
		return nil, nil, fmt.Errorf("another daemon serves %s", path)
	}
	_ = os.Remove(path)
	unix, err = net.Listen("unix", path)
	if err == nil {
		err = os.Chmod(path, 0o600)
	}
	if err != nil {
		tcp.Close()
		return nil, nil, err
	}
	return tcp, unix, nil
}

// Serve serves both until ctx ends. Requests' contexts end with it, so event streams close at
// once rather than hold the shutdown.
func (s *Server) Serve(ctx context.Context, tcp, unix net.Listener) error {
	base := func(net.Listener) context.Context { return ctx }
	servers := []*http.Server{
		{Handler: s.Handler(false), ReadHeaderTimeout: 10 * time.Second, BaseContext: base},
		{Handler: s.Handler(true), ReadHeaderTimeout: 10 * time.Second, BaseContext: base},
	}
	go s.files.Janitor(ctx)
	errs := make(chan error, 2)
	for i, l := range []net.Listener{tcp, unix} {
		go func() { errs <- servers[i].Serve(l) }()
	}
	select {
	case <-ctx.Done():
	case err := <-errs:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}
	shut, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for _, srv := range servers {
		_ = srv.Shutdown(shut)
	}
	return nil
}

// createError is a session that did not start (the manager's refusal before it had an id).
type createError struct{ err error }

func (e *createError) Error() string { return e.err.Error() }
func (e *createError) Unwrap() error { return e.err }

// createAs starts session req as c (Satori's channel.create): its id. One an agent starts from
// inside its session (ir new) is in that agent's channel (launch).
func (s *Server) createAs(ctx context.Context, c caller, req api.NewSession) (string, error) {
	if req.Caller == "" {
		req.Caller = c.session
	}
	alone := req.Agent != "" && req.Caller != "" && req.Members == nil
	first := ""
	if alone {
		// Called by the name it was given (kumo, 2026-11-16: not claude2), unless it was given a
		// nick; its first words not its own session's but a call in the channel it joins, so
		// that what it answers is said there (launch).
		if req.Nick == "" && req.Role == "" {
			req.Nick = deliver.NickFrom(req.Name)
		}
		first, req.Text = req.Text, ""
	}
	id, err := s.create(ctx, c, req)
	if alone && id != "" && err == nil {
		err = s.launch(c, req.Caller, id, first)
	}
	return id, err
}

// create starts session req as c: its id.
func (s *Server) create(ctx context.Context, c caller, req api.NewSession) (string, error) {
	// Started from inside a session (ir's X-Iridium-Session, a session's own TOKEN): that session
	// is its launcher (who started it) unless the body names another.
	if req.Caller == "" {
		req.Caller = c.session
	}
	// Its folder as written: a place, ~, a path from the caller's own session's (places.go).
	if err := s.where(c, &req); err != nil {
		return "", err
	}
	// As a role: its agent, model and card, where the request says nothing (roles.go).
	if _, err := s.asRole(&req); err != nil {
		return "", err
	}
	ctx = context.WithValue(ctx, cardCaller{}, c)
	if req.Agent == "" {
		return s.createChannel(ctx, c, req)
	}
	if err := s.checkCreate(c, &req); err != nil {
		return "", err
	}
	// A channel of more than its own agent and everyone: made whole, then its first words said
	// in it, so that they reach whom they name.
	setup, first := req.Members, ""
	if setup != nil {
		first, req.Text, req.Members = req.Text, "", nil
		if len(setup.Agents) >= MaxAgents {
			return "", &createError{errFull}
		}
		for _, a := range append([]api.NewMember{{Nick: setup.Nick}}, setup.Agents...) {
			if a.Nick != "" && !deliver.ValidNick(a.Nick) {
				return "", &createError{fmt.Errorf("%w: %w", session.ErrBadRequest, channel.ErrNick)}
			}
		}
	}
	id, err := s.m.Create(ctx, req, s.fromOf(c, req.Caller))
	if id != "" && err == nil && setup != nil {
		err = s.setUp(ctx, c, id, *setup, first)
	} else if id != "" && err == nil {
		// Its people as any new channel's (DefaultPeople); its agent woken by its maker's words.
		if err := s.setNewPeople(c, id, nil); err != nil {
			s.logf("server: %s: its people: %v", id, err)
		}
		on := makerOn(c)
		if _, err := s.ch.Change(id, store.AgentMember(id), nil, &on, s.harnessOf); err != nil {
			s.logf("server: %s: its subscriptions: %v", id, err)
		}
	}
	if id != "" {
		via := c.session
		if via == "" && c.socket {
			via = req.Caller
		}
		s.noteOrigin(c, id, via)
		if err := s.st.SetOpener(id, c.ID()); err != nil {
			s.logf("server: opener of %s: %v", id, err)
		}
	}
	if err != nil && id == "" {
		return "", &createError{err}
	}
	return id, err
}

func (s *Server) get(w http.ResponseWriter, r *http.Request) {
	st, err := s.m.State(r.PathValue("id"))
	if err != nil {
		s.error(w, err)
		return
	}
	reply(w, http.StatusOK, st)
}

// errFiles wraps what the files service refused of a message's attachments.
var errFiles = errors.New("attachments")

type fileErr struct{ err error }

func (e fileErr) Error() string        { return e.err.Error() }
func (e fileErr) Unwrap() error        { return e.err }
func (e fileErr) Is(target error) bool { return target == errFiles }

// sendAs sends req into session id as c, from (the source tag): what Satori's message.create does. /clear starts the session anew.
func (s *Server) sendAs(ctx context.Context, c caller, from, id string, req api.Send) (api.SendReply, error) {
	// A script's ir speaks as the script; a script's command goes to the script, not to an agent
	// (scripts.go).
	if c.script != "" {
		return s.scriptSend(c, id, req)
	}
	if _, ok := s.scriptCommandOf(id, strings.TrimSpace(req.Text)); ok {
		return s.sayCommand(c, from, id, req)
	}
	// A channel of several agents: said there, given to those it wakes (channels.go). A command
	// is the sole agent's own when the channel has one agent.
	command := strings.HasPrefix(strings.TrimSpace(req.Text), "/")
	if _, own := s.ch.Own(id); own {
		// In a bare channel with exactly one agent, /clear replaces that agent session in place
		// (keeping the channel and its history) and agent slash commands route straight to it.
		if solo := s.soloAgent(id); command && solo != "" {
			if session.IsClear(req.Text) {
				if err := s.checkSend(c, solo); err != nil {
					return api.SendReply{}, err
				}
				if err := s.inChannel(c, id); err != nil {
					return api.SendReply{}, err
				}
				via := c.session
				if via == "" && c.socket {
					via = req.Caller
				}
				nid, err := s.m.Clear(ctx, solo, from)
				if nid != "" && nid != solo {
					if err := s.st.SetOpener(nid, c.ID()); err != nil {
						s.logf("server: opener of %s: %v", nid, err)
					}
					s.noteOrigin(c, nid, via)
				}
				return api.SendReply{Session: id, Command: true}, err
			}
			if s.m.IsCommand(solo, req.Text) {
				if err := s.checkSend(c, solo); err != nil {
					return api.SendReply{}, err
				}
				if err := s.inChannel(c, id); err != nil {
					return api.SendReply{}, err
				}
				via := c.session
				if via == "" && c.socket {
					via = req.Caller
				}
				s.noteOrigin(c, solo, via)
				text, images, err := s.files.Message(id, req.Text, req.Attachments)
				if err != nil {
					return api.SendReply{}, fileErr{err}
				}
				if req.ID == "" {
					req.ID = session.NewInputID()
				}
				input, queued, err := s.m.SendAs(ctx, solo, req.ID, satori.ForwardWords(text), from, true, images...)
				if err != nil {
					return api.SendReply{}, err
				}
				return api.SendReply{Input: input, Queued: queued, Command: s.m.RunsItself(solo, text)}, nil
			}
		}
		command = false // no sole agent to run it: words as any
	}
	// Among several, only a command of the channel's own agent is its alone; words that merely
	// begin with a slash ("/home/x is broken @codex") are said in the channel as any.
	if s.multi(id) && !(command && s.m.IsCommand(id, req.Text)) {
		return s.sendChannel(ctx, c, from, id, req)
	}
	// One agent: woken as among several (docs/channels.md "订阅事件"): by what names it or by one
	// of its subscriptions; else said in its channel and held for it, given before what next
	// wakes it. Its own commands, and another session's agent writing to it, reach it as ever.
	held, texts := "", []api.Trigger(nil)
	if !c.byAgent() && !(command && s.m.IsCommand(id, req.Text)) && !session.IsClear(req.Text) {
		woken, t := s.wakesOwn(id, c, req.Text)
		if !woken {
			return s.sendChannel(ctx, c, from, id, req)
		}
		held, texts = s.heldWords(id), t
	}
	// Keep the original words and quote ref for the channel log; prepend quoted lines only to
	// what is handed to the agent.
	rawText := req.Text
	var quote int64
	if req.Quote > 0 {
		if q, ok := s.ch.Get(id, req.Quote); ok {
			quote = q.Seq
			req.Text = quoted(satori.ForwardWords(q.Text)) + req.Text
		}
		req.Quote = 0
	}
	if err := s.checkSend(c, id); err != nil {
		return api.SendReply{}, err
	}
	if err := s.inChannel(c, id); err != nil {
		return api.SendReply{}, err
	}
	via := c.session
	if via == "" && c.socket {
		via = req.Caller
	}
	s.noteOrigin(c, id, via)
	// Attachments and all: Claude would clear itself just the same.
	if session.IsClear(req.Text) {
		nid, err := s.m.Clear(ctx, id, from)
		if nid != "" && nid != id {
			if err := s.st.SetOpener(nid, c.ID()); err != nil {
				s.logf("server: opener of %s: %v", nid, err)
			}
			s.noteOrigin(c, nid, via)
		}
		return api.SendReply{Session: nid}, err
	}
	text, images, err := s.files.Message(id, req.Text, req.Attachments)
	if err != nil {
		return api.SendReply{}, fileErr{err}
	}
	// Said in the channel first, under the input's id, then given to the agent: the others who
	// see it see what was said too (Satori's message-created), before what the agent answers.
	if req.ID == "" {
		req.ID = session.NewInputID()
	}
	var seq int64
	u := s.satoriUser(c)
	if st, err := s.m.State(id); err == nil && !command {
		if tag := s.m.SourceTag(from); tag != "" {
			u.Name = tag // the name the agent sees it under
		}
		seq = s.sat.Input(st.Session, req.ID, rawText, u, c.ID(), channel.Data{Device: s.deviceOfCaller(c), Quote: quote, Files: s.files.Refs(id, req.Attachments)})
	}
	named := s.names(id, seq, rawText, readerOf(c)) // the logins it names (ats.go)
	input, queued, err := s.m.SendAs(ctx, id, req.ID, held+satori.ForwardWords(text)+s.onText(texts), from, true, images...)
	if err != nil {
		if seq != 0 {
			s.ch.Unsay(id, seq) // not sent: not said
			s.unnames(id, seq, named)
		}
		return api.SendReply{}, err
	}
	if seq != 0 {
		go s.heard(id, seq, rawText, readerOf(c), u.Name, s.chain.of(id, "")) // its scripts
	}
	if input == session.HandedInput {
		return api.SendReply{Desktop: true, Seq: seq}, nil
	}
	return api.SendReply{Input: input, Queued: queued, Seq: seq, Command: s.m.RunsItself(id, text)}, nil
}

func (s *Server) interrupt(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	id := r.PathValue("id")
	// An agent ending its own turn (ir api POST internal/sessions/<its id>/interrupt; kumo,
	// 2026-11-16): called with nothing to say, it says nothing. The calls it was answering are
	// closed as answered: nothing is said for it, no line that it said nothing.
	if c := callerOf(r); c.session != "" && c.session == id {
		unlock := s.chain.lock(id)
		// Not one held for a background task: its report is yet to come (calls.settle).
		s.calls.take(id, func(c called) bool { return !c.Held })
		unlock()
	}
	// Who asked (kumo, 2026-11-26: a turn stopped and no one had pressed anything): a browser's
	// login, an agent's own ir (its session), an app, the host. No secret in it.
	c := callerOf(r)
	who := c.App
	if c.Name != "" {
		who += " " + c.Name
	}
	if c.session != "" {
		who += " agent " + c.session
	}
	s.logf("server: %s: interrupt asked by %s", id, who)
	s.done(w, s.m.Interrupt(ctx, id))
}

// stopTask stops one background task of a session's agent (api.Task.Stop).
func (s *Server) stopTask(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	id := r.PathValue("id")
	s.logf("server: %s: task %s stop asked", id, r.PathValue("task"))
	err := s.m.StopTask(ctx, id, r.PathValue("task"))
	if errors.Is(err, session.ErrBadRequest) {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	s.done(w, err)
}

func (s *Server) stop(w http.ResponseWriter, r *http.Request) {
	s.done(w, s.m.Stop(r.Context(), r.PathValue("id")))
}

var stateKey = regexp.MustCompile(`^(list|guilds|needs|pairing|update|terminals|machines|voice|channels|avatars|faces|ats|memory|roles|favorites|cards|scripts|(session|context|reads|viewers):[0-9a-zA-Z_-]{1,128})$`)

// stateKeys splits the states a WATCH asks for into those it carries and those it leaves
// out (unknown, or not this caller's: needs and pairing are a person's on ir_web or at the
// host).
func (s *Server) stateKeys(c caller, keys []string) (carried []string, ignored []api.StateEvent) {
	for _, k := range keys {
		switch {
		case !stateKey.MatchString(k):
			ignored = append(ignored, api.StateEvent{Key: k, Ignored: "unknown"})
		case (k == "needs" || k == "pairing") && !c.panel():
			ignored = append(ignored, api.StateEvent{Key: k, Ignored: "forbidden"})
		case !slices.Contains(carried, k):
			carried = append(carried, k)
		}
	}
	return carried, ignored
}

// MaxWatch is how many states one stream carries: the page's own, and a channel's agents'
// (their sessions and contexts, MaxAgents of them).
const MaxWatch = 40

// watchStates sends c the states keys name (at most MaxWatch) and the drafts,
// through emit ("state" or "draft", the JSON), till ctx ends: Satori's /v1/events for ir_web.
func (s *Server) watchStates(ctx context.Context, c caller, keys []string, emit func(kind string, data []byte) error) error {
	if len(keys) > MaxWatch {
		keys = keys[:MaxWatch]
	}
	carried, ignored := s.stateKeys(c, keys)
	for _, e := range ignored {
		b, _ := json.Marshal(e)
		if err := emit("state", b); err != nil {
			return err
		}
	}
	// Whoever watches who is looking at a channel is looking at it (channels.go "viewers").
	for _, k := range carried {
		if ch, ok := strings.CutPrefix(k, "viewers:"); ok && !c.byAgent() {
			defer s.viewing(c, ch)()
		}
	}
	sub := s.hub.Subscribe(carried)
	defer sub.Close()
	sub.Filter = s.streamFilter(c)
	// A session's state carries its transcript's tail: the credentials' values masked, as every
	// answer that carries a transcript is (routes_secrets.go).
	masked := func(kind string, data []byte) error {
		data = s.jsonMask().Mask(data)
		return emit(kind, data)
	}
	return sub.Run(ctx, masked, func() error { return nil }, func() {})
}

func (s *Server) done(w http.ResponseWriter, err error) {
	if err != nil {
		s.error(w, err)
		return
	}
	reply(w, http.StatusOK, map[string]any{"ok": true})
}

// fault answers with err, knowing the errors of the session settings and the queue; the
// rest as s.error does.
func (s *Server) fault(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, session.ErrBadRequest):
		fail(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, store.ErrNoInput):
		refuse(w, http.StatusUnprocessableEntity, "no_such_input", err.Error())
	case errors.Is(err, session.ErrGone):
		fail(w, http.StatusGone, err.Error())
	default:
		s.error(w, err)
	}
}

func (s *Server) error(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, session.ErrNotFound):
		refuse(w, http.StatusUnprocessableEntity, "no_such_session", err.Error())
	case errors.Is(err, transcript.ErrNoBlock):
		refuse(w, http.StatusUnprocessableEntity, "no_such_message", err.Error())
	case errors.Is(err, session.ErrNotLive), errors.Is(err, session.ErrElsewhere), errors.Is(err, session.ErrSubagent):
		fail(w, http.StatusConflict, err.Error())
	case errors.Is(err, context.DeadlineExceeded):
		fail(w, http.StatusGatewayTimeout, "the agent did not answer in time")
	case access.IsDenied(err):
		refuse(w, http.StatusForbidden, "no_permission", err.Error())
	case errors.Is(err, session.ErrBadRequest):
		refuse(w, http.StatusBadRequest, "bad_request", err.Error())
	default:
		s.logf("server: %v", err)
		fail(w, http.StatusInternalServerError, err.Error())
	}
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		fail(w, http.StatusBadRequest, "bad request body: "+err.Error())
		return false
	}
	return true
}

func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, status int, msg string) { reply(w, status, api.Error{Message: msg}) }

// refuse answers a refusal with its code (decision 8): 403 for what the caller may not do now,
// 422 for what is not there or not right (decision 28).
func refuse(w http.ResponseWriter, status int, code, msg string) {
	reply(w, status, api.Error{Code: code, Message: msg})
}

// execConnect takes an executor's WebSocket (docs/exec.md): a terminal with a TOKEN, as which the
// machine is known.
func (s *Server) execConnect(w http.ResponseWriter, r *http.Request) {
	if s.Remote == nil {
		refuse(w, http.StatusUnprocessableEntity, "not_here", "no executors here")
		return
	}
	c := callerOf(r)
	if c.Login != "" || c.Host() || c.socket || c.session != "" {
		refuse(w, http.StatusForbidden, "no_permission", "an executor connects with a TOKEN")
		return
	}
	s.Remote.Serve(w, r, c.ID(), c.Name)
}
