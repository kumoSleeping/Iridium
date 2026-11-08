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
//   - its asks (approvals, questions) go to one chat as they are, the session named, answered
//     there without moving the chat;
//   - what it says with ir bot goes as a letter: the session named, its text, and 进入会话 (the
//     chat points at it then) or 关闭. Anything else the user says goes where the chat points.
//
// The chat is the one that last had the session in it, else the one the user last wrote in (QQ
// lets a bot answer a message for an hour: what goes there then costs nothing); of the plugins
// connected now, if any are.

// seen notes that the chat said something now, with session in it.
func (s *Service) seen(c Caller, chat, session string) {
	if err := s.o.Store.Seen(c.ID, chat, session); err != nil {
		s.o.Logf("chat: %v", err)
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
	to, ok := s.reach(session, plugin)
	if !ok {
		return api.BotReply{}, ErrNoBot
	}
	card := &api.Card{Session: session, Title: sessionText(st.Session), Summary: []string{text}, Options: []api.CardOption{
		{ID: session, Text: "进入会话", Primary: true}, {ID: dismiss, Text: "关闭"}}}
	s.newPick(Caller{ID: to.Plugin}, to.Chat, "use", card)
	s.post(to.Plugin, to.Chat, "card", api.ChatEvent{Chat: to.Chat, Session: session, Card: card})
	return api.BotReply{Bot: to.Plugin, Chat: to.Chat, Letter: true}, nil
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
func (s *Service) away1(session string, out []ev) {
	for _, e := range out {
		switch e.typ {
		case "card":
			to, ok := s.reach(session, "")
			if !ok || e.e.Card == nil {
				continue
			}
			s.mu.Lock()
			s.away[e.e.Card.ID] = store.ChatPointer{Plugin: to.Plugin, Chat: to.Chat}
			s.mu.Unlock()
			e.e.Chat, e.e.Session, e.e.Card = to.Chat, session, s.awayCard(session, e.e.Card)
			s.post(to.Plugin, to.Chat, "card", e.e)
		case "card_done":
			s.mu.Lock()
			to, ok := s.away[e.e.CardID]
			delete(s.away, e.e.CardID)
			s.mu.Unlock()
			if ok {
				e.e.Chat, e.e.Session = to.Chat, session
				s.post(to.Plugin, to.Chat, "card_done", e.e)
			}
		}
	}
}
