package chat

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/store"
)

// A session no chat points at still reaches the user through a bot (the user, 2026-11-08):
//
//   - its asks (approvals, questions) go as they are, the session named, answered there without
//     moving the chat;
//   - what it says with ir bot goes as a letter: the session named, its text, and 进入会话 (the
//     chat points at it then) or 关闭. Anything else the user says goes where the chat points.
//
// Which bots (the user, 2026-11-09): those that follow the panel that began the session's turn
// (followed back through the agents that passed the work on: ir send, ir new), and the bot itself
// when its chat began it. An ask no bot follows goes nowhere but the web; a letter no bot follows
// goes to one bot, picked as before. In each bot, the chat is the one that last had the session in it, else the one
// the user last wrote in (QQ lets a bot answer a message for an hour: what goes there then costs
// nothing); of the plugins connected now, if any are.

// seen notes that the chat said something now, with session in it.
func (s *Service) seen(c Caller, chat, session string) {
	had, err := s.o.Store.HasChat(c.ID)
	if err != nil {
		s.o.Logf("chat: %v", err)
	}
	if err := s.o.Store.Seen(c.ID, chat, session); err != nil {
		s.o.Logf("chat: %v", err)
		return
	}
	if !had && err == nil {
		s.botsChanged()
	}
}

func (s *Service) botsChanged() {
	if s.o.BotsChanged != nil {
		s.o.BotsChanged()
	}
}

// reach is the chat that reaches the user for session, of plugin only if it is not "".
func (s *Service) reach(session, plugin string) (store.ChatSeen, bool) {
	all, err := s.o.Store.ChatsSeen()
	if err != nil {
		s.o.Logf("chat: %v", err)
		return store.ChatSeen{}, false
	}
	type cand struct {
		store.ChatSeen
		had    int64 // when it last had session in it
		online bool
	}
	byChat := map[string]*cand{}
	var order []*cand
	for _, x := range all { // the latest first
		if plugin != "" && x.Plugin != plugin {
			continue
		}
		k := x.Plugin + "\x00" + x.Chat
		c := byChat[k]
		if c == nil {
			if s.o.Terminal != nil && !s.o.Terminal(x.Plugin) {
				continue
			}
			if s.guard().Use(Caller{ID: x.Plugin}, session) != nil {
				continue
			}
			c = &cand{ChatSeen: x, online: s.isOnline(x.Plugin)}
			byChat[k] = c
			order = append(order, c)
		}
		if x.Session == session && c.had == 0 {
			c.had = x.At
		}
	}
	if len(order) == 0 {
		return store.ChatSeen{}, false
	}
	if slices.ContainsFunc(order, func(c *cand) bool { return c.online }) {
		order = slices.DeleteFunc(order, func(c *cand) bool { return !c.online })
	}
	best := order[0]
	for _, c := range order[1:] {
		if c.had > best.had {
			best = c
		}
	}
	return best.ChatSeen, true
}

func (s *Service) isOnline(plugin string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.online[plugin] > 0
}

// awayCard is an ask of a session no chat points at, as the chat it goes to shows it: the
// session first.
func (s *Service) awayCard(session string, c *api.Card) *api.Card {
	st, err := s.o.Manager.State(session)
	if err != nil {
		return c
	}
	x := *c
	x.Title = sessionText(st.Session)
	x.Summary = append([]string{c.Title}, c.Summary...)
	return &x
}

// ErrNoBot: no bot's chat may reach the user for the session.
var ErrNoBot = fmt.Errorf("%w: no bot to reach the user through (none has a chat yet, or none may use this session)", ErrNotFound)

// Letter has session say text to the user through a bot: plugin's, or the one reach picks. A chat
// that points at it gets it as the session's reply; else a letter.
func (s *Service) Letter(ctx context.Context, session, text, plugin string) (api.BotReply, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return api.BotReply{}, fmt.Errorf("%w: an empty message", ErrBadRequest)
	}
	st, err := s.o.Manager.State(session)
	if err != nil {
		return api.BotReply{}, err
	}
	chats, err := s.o.Store.Pointing(session)
	if err != nil {
		return api.BotReply{}, err
	}
	chats = slices.DeleteFunc(chats, func(p store.ChatPointer) bool { return plugin != "" && p.Plugin != plugin })
	if len(chats) > 0 {
		p := chats[0]
		r := s.reply(p.Plugin, session, text)
		s.post(p.Plugin, p.Chat, "reply", api.ChatEvent{Chat: p.Chat, Session: session, Reply: &r})
		return api.BotReply{Bot: p.Plugin, Chat: p.Chat}, nil
	}
	var tos []store.ChatSeen
	if plugin == "" {
		tos = s.following(session, s.originOf(session))
	}
	if len(tos) == 0 {
		to, ok := s.reach(session, plugin)
		if !ok {
			return api.BotReply{}, ErrNoBot
		}
		tos = []store.ChatSeen{to}
	}
	for _, to := range tos {
		card := &api.Card{Session: session, Title: sessionText(st.Session), Summary: []string{text}, Options: []api.CardOption{
			{ID: session, Text: "进入会话", Primary: true}, {ID: dismiss, Text: "关闭"}}}
		s.newPick(Caller{ID: to.Plugin}, to.Chat, "use", card)
		s.post(to.Plugin, to.Chat, "card", api.ChatEvent{Chat: to.Chat, Session: session, Card: card})
	}
	return api.BotReply{Bot: tos[0].Plugin, Chat: tos[0].Chat, Letter: true}, nil
}

// Bring points plugin's chat at session, from a session's menu on the web (the user, 2026-11-08):
// the chat reach picks for it, told as /use tells it. ErrNoBot if the bot has no chat that may use
// the session.
func (s *Service) Bring(session, plugin string) (api.BotReply, error) {
	st, err := s.o.Manager.State(session)
	if err != nil {
		return api.BotReply{}, err
	}
	to, ok := s.reach(session, plugin)
	if !ok {
		return api.BotReply{}, ErrNoBot
	}
	p, err := s.o.Store.Pointer(to.Plugin, to.Chat)
	if err != nil {
		return api.BotReply{}, err
	}
	p.Session = session
	if err := s.setPointer(p); err != nil {
		return api.BotReply{}, err
	}
	s.post(p.Plugin, p.Chat, "reply", api.ChatEvent{Chat: p.Chat, Session: session, Reply: text(s.nowIn(st.Session))})
	return api.BotReply{Bot: p.Plugin, Chat: p.Chat}, nil
}

// Bots is every plugin with a chat (true) or, without one yet, its event stream open now (false):
// a Bot paired but not yet written to.
func (s *Service) Bots() map[string]bool {
	all, err := s.o.Store.ChatsSeen()
	if err != nil {
		s.o.Logf("chat: %v", err)
	}
	out := map[string]bool{}
	s.mu.Lock()
	for id := range s.online {
		out[id] = false
	}
	s.mu.Unlock()
	for _, x := range all {
		out[x.Plugin] = true
	}
	return out
}

// dismiss is a pick's option that does nothing: a letter's 关闭.
const dismiss = "-"

// ---- history ----

// round is a message to the session and the reply it got (its last message), "" while it runs.
type round struct{ from, said, reply string }

// rounds are the session's last n rounds, the oldest first, from a few pages of its transcript.
func (s *Service) rounds(id string, n int) []round {
	var out []round
	before, carry := int64(-1), ""
	for range 4 {
		page, err := s.o.Manager.Messages(id, before, -1)
		if err != nil || len(page.Blocks) == 0 {
			break
		}
		var here []round
		var cur *round
		lead := "" // replies before the page's first message: its last round's, on the page before
		for _, b := range page.Blocks {
			switch b.Kind {
			case api.BlockKindUser:
				if strings.TrimSpace(b.Text) == "" {
					continue
				}
				here = append(here, round{from: b.From, said: b.Text})
				cur = &here[len(here)-1]
			case api.BlockKindAssistant:
				switch {
				case strings.TrimSpace(b.Text) == "":
				case cur != nil:
					cur.reply = b.Text
				default:
					lead = b.Text
				}
			}
		}
		if len(here) > 0 && len(out) > 0 && carry != "" {
			here[len(here)-1].reply = carry
		}
		carry = lead
		out = append(here, out...)
		if len(out) > n || !page.More || page.Start <= 0 {
			break
		}
		before = page.Start
	}
	// A round cut by the page's start has no message: not one.
	if len(out) > n {
		out = out[len(out)-n:]
	}
	return out
}

// oneLine is md as plain words on one line, its middle left out past n characters (its start and
// its end kept), safe in QQ's Markdown.
func oneLine(md string, n int) string {
	t := strings.Join(strings.Fields(plain(md)), " ")
	if r := []rune(t); len(r) > n {
		head := n * 2 / 3
		t = strings.TrimSpace(string(r[:head])) + " … " + strings.TrimSpace(string(r[len(r)-(n-head-1):]))
	}
	return mdSafe.Replace(t)
}

var mdSafe = strings.NewReplacer(`\`, `\\`, "`", "\\`", "*", `\*`, "_", `\_`, "~", `\~`, "<", `\<`, "#", `\#`)

// who is the name a message to the session goes by in a preview: its source's (a plugin's sender,
// a terminal's name), else 我.
func who(from string) string {
	if from == "" {
		return "我"
	}
	if _, sender, ok := strings.Cut(from, "·"); ok && sender != "" {
		return sender
	}
	return from
}

// history is the last n rounds of the session, at most max characters, the oldest dropped first,
// light (the user, 2026-11-08): each "[who] message" and "[CC] reply", their middles left out, a
// rule between rounds.
func (s *Service) history(id string, n, max int) string {
	running, mark := false, tag("")
	if st, err := s.o.Manager.State(id); err == nil {
		running, mark = st.Status == api.StatusRunning, tag(st.Agent)
	}
	rs := s.rounds(id, n)
	var parts []string
	total := 0
	for i := len(rs) - 1; i >= 0; i-- {
		r := rs[i]
		reply := oneLine(r.reply, 160)
		if i == len(rs)-1 && running {
			reply = "进行中"
		} else if reply == "" {
			reply = "无回复"
		}
		// Each line break a hard one (QQ's Markdown: two spaces before it).
		part := "[" + mdSafe.Replace(who(r.from)) + "] " + oneLine(r.said, 60) + "  \n" + mark + " " + reply
		if total+len([]rune(part)) > max && len(parts) > 0 {
			break
		}
		total += len([]rune(part))
		parts = append([]string{part}, parts...)
	}
	if len(parts) == 0 {
		return ""
	}
	return "# 近期消息预览\n\n" + strings.Join(parts, "\n\n---\n\n")
}

const (
	switchRounds  = 3
	switchChars   = 1500
	historyRounds = 5
	historyMax    = 10
	historyChars  = 1900
)

func cmdHistory(s *Service, ctx context.Context, c Caller, p *store.ChatPointer, args string) (api.ChatInputReply, error) {
	st, err := s.current(c, p)
	if err != nil {
		return api.ChatInputReply{}, err
	}
	n := historyRounds
	if args != "" {
		v, err := strconv.Atoi(args)
		if err != nil || v < 1 {
			return api.ChatInputReply{}, fmt.Errorf("%w: /history [轮数]", ErrBadRequest)
		}
		n = min(v, historyMax)
	}
	return reply(st.ID, cmp.Or(s.history(st.ID, n, historyChars), "还没有消息。"))
}

// away1 sends a session's asks to the one chat that reaches the user for it, no chat pointing at
// it: an ask's card, the session named, and its end. Nothing else of the session goes there.
func (s *Service) away1(session, origin string, out []ev) {
	for _, e := range out {
		switch e.typ {
		case "card":
			// Nor one of a session not listed (a group at another machine no one opened).
			if e.e.Card == nil || !s.o.Manager.Listed(session) {
				continue
			}
			card := s.awayCard(session, e.e.Card)
			var sent []store.ChatPointer
			for _, to := range s.following(session, origin) {
				x := e.e
				x.Chat, x.Session, x.Card = to.Chat, session, card
				s.post(to.Plugin, to.Chat, "card", x)
				sent = append(sent, store.ChatPointer{Plugin: to.Plugin, Chat: to.Chat})
			}
			if len(sent) > 0 {
				s.mu.Lock()
				s.away[e.e.Card.ID] = sent
				s.mu.Unlock()
			}
		case "card_done":
			s.mu.Lock()
			sent := s.away[e.e.CardID]
			delete(s.away, e.e.CardID)
			s.mu.Unlock()
			for _, to := range sent {
				x := e.e
				x.Chat, x.Session = to.Chat, session
				s.post(to.Plugin, to.Chat, "card_done", x)
			}
		}
	}
}

// originLocked is the terminal a turn began at: from, or, when the agent of session via passed it
// on, where via's own turn began, as far as this Service saw it. s.mu is held.
func (s *Service) originLocked(from, via string) string {
	if o, ok := s.origin[via]; ok && via != "" && o != "" {
		return o
	}
	return from
}

// originOf is where session's latest turn began (originLocked), or, before its first, who opened it.
func (s *Service) originOf(session string) string {
	from, via, ok := s.o.Manager.TurnFrom(session)
	if !ok {
		return cmp.Or(s.o.Store.Opener(session), "local")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.originLocked(from, via)
}

// following is a chat (reach's) of each bot that follows origin, and of origin itself when it is a
// bot: where what session asks goes while no chat points at it.
func (s *Service) following(session, origin string) []store.ChatSeen {
	if origin == "" {
		return nil
	}
	bots, err := s.o.Store.Followers(origin)
	if err != nil {
		s.o.Logf("chat: %v", err)
	}
	if had, _ := s.o.Store.HasChat(origin); had && !slices.Contains(bots, origin) {
		bots = append(bots, origin)
	}
	var out []store.ChatSeen
	for _, b := range bots {
		if to, ok := s.reach(session, b); ok {
			out = append(out, to)
		}
	}
	return out
}
