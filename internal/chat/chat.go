// Package chat is the plugins' chat layer (plan "插件"): a plugin names each of its chat windows
// with a key of its own, and Iridium keeps what the window points at (its session, and what /new
// opens), runs Iridium's commands, and passes everything else to the session. What the session
// does comes back as plugin events, kept 24 hours in the store's outbox so a plugin reads on
// after it reconnects (Last-Event-ID).
package chat

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/session"
	"github.com/kumoSleeping/iridium/internal/store"
)

// Keep is how long plugin events wait to be read.
const Keep = 24 * time.Hour

// Caller is the interactive terminal a request comes from: a plugin's channel terminal, as the
// gate names it. ID keys its chats and events; Name is its source mark.
type Caller struct {
	ID   string
	Name string
}

type Options struct {
	Manager  *session.Manager
	Store    *store.Store
	StateDir string // the file refs' key is kept here
	ChatDir  string // where a chat opens a session before /new set one; "" is home
	Logf     func(string, ...any)
}

var (
	ErrBadRequest = errors.New("bad request")
	ErrNotFound   = errors.New("not found")
)

type Service struct {
	o      Options
	key    []byte
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu    sync.Mutex
	held  map[string]string        // session: the agent's last message this turn, not sent yet
	wake  map[string]chan struct{} // plugin: closed when an event is posted
	picks map[string]pick          // a command's card
}

// pick is a command's card: answering it runs the command with the option chosen.
type pick struct {
	plugin, chat, command string
	at                    time.Time
}

func New(o Options) (*Service, error) {
	if o.Logf == nil {
		o.Logf = log.Printf
	}
	if o.ChatDir == "" {
		o.ChatDir, _ = os.UserHomeDir()
	}
	key, err := refKey(o.StateDir)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{o: o, key: key, ctx: ctx, cancel: cancel, held: map[string]string{}, wake: map[string]chan struct{}{}, picks: map[string]pick{}}
	o.Manager.Watch(s.onNotice)
	s.wg.Add(1)
	go s.prune()
	return s, nil
}

func (s *Service) Close() {
	s.cancel()
	s.wg.Wait()
}

func (s *Service) prune() {
	defer s.wg.Done()
	t := time.NewTicker(10 * time.Minute)
	defer t.Stop()
	for {
		if err := s.o.Store.PruneOutbox(Keep); err != nil {
			s.o.Logf("chat: %v", err)
		}
		s.mu.Lock()
		for id, p := range s.picks {
			if time.Since(p.at) > Keep {
				delete(s.picks, id)
			}
		}
		s.mu.Unlock()
		select {
		case <-s.ctx.Done():
			return
		case <-t.C:
		}
	}
}

// ValidKey: a chat key is 1 to 200 bytes of printable text.
func ValidKey(k string) bool {
	if k == "" || len(k) > 200 {
		return false
	}
	for _, r := range k {
		if !unicode.IsPrint(r) {
			return false
		}
	}
	return true
}

// source is the mark the agent sees: the plugin's name, and the sender in the chat if given.
func source(c Caller, sender string) string {
	sender = strings.Map(func(r rune) rune {
		if strings.ContainsRune("[]/:·", r) || !unicode.IsPrint(r) {
			return -1
		}
		return r
	}, sender)
	sender = strings.Join(strings.Fields(sender), " ")
	if r := []rune(sender); len(r) > 24 {
		sender = string(r[:24])
	}
	if sender == "" {
		return c.Name
	}
	return c.Name + "·" + sender
}

// Pointer is what the chat points at.
func (s *Service) Pointer(c Caller, chat string) (api.ChatPointer, error) {
	p, err := s.o.Store.Pointer(c.ID, chat)
	return api.ChatPointer{Chat: chat, Session: p.Session, Agent: api.Agent(p.Agent), Cwd: p.Cwd}, err
}

// Input takes a message from a chat window: an Iridium command runs here; anything else goes to
// the session the chat points at (a new one, if none), joining a running turn as a steer.
func (s *Service) Input(ctx context.Context, c Caller, chat string, in api.ChatInput) (api.ChatInputReply, error) {
	if !ValidKey(chat) {
		return api.ChatInputReply{}, fmt.Errorf("%w: bad chat key", ErrBadRequest)
	}
	text := strings.TrimSpace(in.Text)
	if text == "" {
		return api.ChatInputReply{}, fmt.Errorf("%w: an empty message", ErrBadRequest)
	}
	p, err := s.o.Store.Pointer(c.ID, chat)
	if err != nil {
		return api.ChatInputReply{}, err
	}
	if name, args, ok := command(text); ok {
		if cmd, ok := commands[name]; ok {
			return cmd.run(s, ctx, c, &p, args)
		}
	}
	id, err := s.ensure(ctx, c, &p, source(c, in.Sender))
	if err != nil {
		return api.ChatInputReply{}, err
	}
	_, queued, err := s.o.Manager.SendWith(ctx, id, text, source(c, in.Sender), true)
	return api.ChatInputReply{Session: id, Queued: queued}, err
}

// ensure is the session the chat points at, opening one with its defaults if none (or gone).
func (s *Service) ensure(ctx context.Context, c Caller, p *store.ChatPointer, from string) (string, error) {
	if p.Session != "" {
		if _, err := s.o.Manager.State(p.Session); err == nil {
			return p.Session, nil
		}
	}
	return s.open(ctx, c, p, api.Agent(p.Agent), p.Cwd, from)
}

func (s *Service) open(ctx context.Context, c Caller, p *store.ChatPointer, a api.Agent, cwd, from string) (string, error) {
	a = cmp.Or(a, api.AgentClaude)
	cwd = cmp.Or(cwd, s.o.ChatDir)
	id, err := s.o.Manager.Create(ctx, api.NewSession{Agent: a, Cwd: cwd}, from)
	if err != nil && id == "" {
		return "", fmt.Errorf("%w: %v", ErrBadRequest, err)
	}
	p.Plugin, p.Session, p.Agent, p.Cwd = c.ID, id, string(a), cwd
	if err := s.o.Store.SetPointer(*p); err != nil {
		return id, err
	}
	return id, err
}

// command splits "/name args"; ok false if text is no command.
func command(text string) (name, args string, ok bool) {
	if !strings.HasPrefix(text, "/") {
		return "", "", false
	}
	name, args, _ = strings.Cut(text[1:], " ")
	name, _, _ = strings.Cut(name, "@") // Telegram's /cmd@bot
	return strings.ToLower(name), strings.TrimSpace(args), name != ""
}

// Answer answers a card: an ask's (as the web's confirmation panel does), or a command's pick.
func (s *Service) Answer(ctx context.Context, c Caller, a api.CardAnswer) (api.CardReply, error) {
	if strings.HasPrefix(a.Card, "pick:") {
		s.mu.Lock()
		p, ok := s.picks[a.Card]
		s.mu.Unlock()
		if !ok || p.plugin != c.ID {
			return api.CardReply{}, fmt.Errorf("%w: the card is no longer open", session.ErrGone)
		}
		option := a.Option
		if option == "" && len(a.Choices) > 0 {
			option = a.Choices[0]
		}
		if option == "" {
			return api.CardReply{}, fmt.Errorf("%w: pick an option", ErrBadRequest)
		}
		r, err := s.Input(ctx, c, p.chat, api.ChatInput{Text: "/" + p.command + " " + option})
		if err != nil {
			return api.CardReply{}, err
		}
		s.mu.Lock()
		delete(s.picks, a.Card)
		s.mu.Unlock()
		return api.CardReply{OK: true, Reply: r.Reply, Card: r.Card}, nil
	}
	if err := s.o.Manager.AnswerCard(ctx, a); err != nil {
		return api.CardReply{}, err
	}
	return api.CardReply{OK: true}, nil
}

func (s *Service) newPick(c Caller, chat, command string, card *api.Card) *api.Card {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	card.ID = "pick:" + hex.EncodeToString(b)
	card.Kind = "pick"
	s.mu.Lock()
	s.picks[card.ID] = pick{plugin: c.ID, chat: chat, command: command, at: time.Now()}
	s.mu.Unlock()
	return card
}

// ---- events ----

// onNotice turns what happened in a session into events for the chats that point at it.
func (s *Service) onNotice(n session.Notice) {
	type ev struct {
		typ string
		e   api.ChatEvent
	}
	var out []ev
	s.mu.Lock()
	held := s.held[n.Session]
	switch n.Kind {
	case session.NoticeTurnStart:
		delete(s.held, n.Session)
	case session.NoticeMessage:
		// The last message of a turn is its reply: the one before is progress once another comes.
		if held != "" {
			out = append(out, ev{"progress", api.ChatEvent{Text: held}})
		}
		s.held[n.Session] = n.Text
	case session.NoticeTool:
		if held != "" {
			out = append(out, ev{"progress", api.ChatEvent{Text: held}})
		}
		delete(s.held, n.Session)
	case session.NoticeTurnEnd:
		delete(s.held, n.Session)
		if text := cmp.Or(n.Text, held); text != "" {
			out = append(out, ev{"reply", api.ChatEvent{}})
			out[len(out)-1].e.Text = text // the reply is made per plugin below: its file refs
		}
	case session.NoticeAsk:
		out = append(out, ev{"card", api.ChatEvent{Card: session.CardOf(n.Session, *n.Ask)}})
	case session.NoticeAskDone:
		out = append(out, ev{"card_done", api.ChatEvent{CardID: session.AskCardID(n.Session, n.RequestID)}})
	case session.NoticeError:
		out = append(out, ev{"error", api.ChatEvent{Text: n.Text}})
	case session.NoticeStatus:
		out = append(out, ev{"status", api.ChatEvent{Status: n.Status}})
	}
	s.mu.Unlock()
	if len(out) == 0 {
		return
	}
	chats, err := s.o.Store.Pointing(n.Session)
	if err != nil {
		s.o.Logf("chat: %v", err)
		return
	}
	for _, p := range chats {
		for _, e := range out {
			e.e.Chat, e.e.Session = p.Chat, n.Session
			if e.typ == "reply" {
				r := s.reply(p.Plugin, n.Session, e.e.Text)
				e.e.Text, e.e.Reply = "", &r
			}
			s.post(p.Plugin, p.Chat, e.typ, e.e)
		}
	}
}

func (s *Service) post(plugin, chat, typ string, e api.ChatEvent) {
	b, _ := json.Marshal(e)
	if _, err := s.o.Store.Post(plugin, chat, typ, string(b)); err != nil {
		s.o.Logf("chat: %v", err)
		return
	}
	s.mu.Lock()
	if ch, ok := s.wake[plugin]; ok {
		close(ch)
		delete(s.wake, plugin)
	}
	s.mu.Unlock()
}

func (s *Service) waitCh(plugin string) chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch, ok := s.wake[plugin]
	if !ok {
		ch = make(chan struct{})
		s.wake[plugin] = ch
	}
	return ch
}

// Ping is how often an idle event stream says it is there.
var Ping = 15 * time.Second

// Serve writes the plugin's events as server-sent events until ctx ends or a write fails, from
// the one after last (its Last-Event-ID); last < 0: from now on. Each has its sequence number
// as its id.
func (s *Service) Serve(ctx context.Context, c Caller, last int64, w interface{ Write([]byte) (int, error) }, flush func()) error {
	if last < 0 {
		var err error
		if last, err = s.o.Store.OutboxLast(c.ID); err != nil {
			return err
		}
	}
	ping := time.NewTicker(Ping)
	defer ping.Stop()
	for {
		ch := s.waitCh(c.ID)
		evs, err := s.o.Store.OutboxAfter(c.ID, last)
		if err != nil {
			return err
		}
		if len(evs) > 0 {
			var b strings.Builder
			for _, e := range evs {
				fmt.Fprintf(&b, "id: %d\nevent: %s\ndata: %s\n\n", e.Seq, e.Type, e.Data)
				last = e.Seq
			}
			if _, err := w.Write([]byte(b.String())); err != nil {
				return err
			}
			flush()
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ch:
		case <-ping.C:
			if _, err := w.Write([]byte(": ping\n\n")); err != nil {
				return err
			}
			flush()
		}
	}
}
