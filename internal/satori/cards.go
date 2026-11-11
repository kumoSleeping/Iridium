package satori

import (
	"context"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/session"
)

// Asks (decision 27): an ask goes out as a message whose id is its card's ("ask:<session>:<request>"),
// with a button per option, id "<card>#<option>". An action button pressed (POST
// /v1/internal/button) answers at once where one choice does it; where it takes more (several
// questions, a multiSelect one) the presses add up until "<card>#submit". An input button's text
// ("<card>#w<question>", "<card>#deny", "<card>#reject", then a space) begins a message that
// answers in the person's own words. Each press is an interaction/button event; the ask closing,
// a message-updated.

func cardID(id, request string) string { return session.AskCardID(id, request) }

// cardSession is the session of a card id, "" if it is not one.
func cardSession(card string) string {
	rest, ok := strings.CutPrefix(card, "ask:")
	if !ok {
		return ""
	}
	id, _, _ := strings.Cut(rest, ":")
	return id
}

func button(id, label string, primary bool) Element {
	kv := []string{"type", "action", "id", id}
	if primary {
		kv = append(kv, "theme", "primary")
	}
	return Tag("button", kv, Text(label))
}

func inputButton(id, label string) Element {
	return Tag("button", []string{"type", "input", "id", id, "text", id + " "}, Text(label))
}

// cardContent is a card as a message: its title, its lines, its buttons.
func cardContent(card *api.Card) string {
	els := []Element{Tag("p", nil, Tag("b", nil, Text(card.Title)))}
	if card.Kind == "question" {
		for g, q := range card.Summary {
			els = append(els, Tag("p", nil, Text(q)))
			for _, o := range card.Options {
				if o.Group == g {
					els = append(els, button(card.ID+"#"+o.ID, o.Text, false))
				}
			}
			els = append(els, inputButton(card.ID+"#w"+strconv.Itoa(g), "自己填写"))
		}
		if len(card.Summary) > 1 || len(card.Multi) > 0 {
			els = append(els, button(card.ID+"#submit", "提交", true))
		}
		return String(els)
	}
	for _, l := range card.Summary {
		els = append(els, Tag("p", nil, Text(l)))
	}
	for _, o := range card.Options {
		els = append(els, button(card.ID+"#"+o.ID, o.Text, o.Primary))
	}
	switch card.Kind {
	case "permission":
		els = append(els, inputButton(card.ID+"#"+session.OptionDeny, "拒绝并说明"))
	case "plan":
		els = append(els, inputButton(card.ID+"#"+session.OptionReject, "拒绝并说明"))
	}
	return String(els)
}

// closedContent is an ask's message once it closed: its buttons gone.
func closedContent(content string) string {
	var drop func([]Element) []Element
	drop = func(els []Element) []Element {
		out := els[:0]
		for _, e := range els {
			if e.Type == "button" {
				continue
			}
			e.Children = drop(e.Children)
			out = append(out, e)
		}
		return out
	}
	return String(append(drop(Parse(content)), Tag("p", nil, Text("已结束"))))
}

// askMessage is an open ask of session ss as a message.
func (s *Service) askMessage(ss api.Session, a api.Ask) Message {
	card := session.CardOf(ss.ID, a)
	m := Message{ID: card.ID, Content: s.b.Mask(cardContent(card)), Channel: s.channelMasked(ss), Guild: s.guildOf(ss)}
	if u, ok := agentUser(ss.Agent); ok {
		m.User = &u
	}
	return m
}

// typed takes a message that begins with an input button's text: the answer it gives. done false:
// it is not one, send it on.
func (s *Service) typed(ctx context.Context, c Caller, text string) (bool, error) {
	id, rest, _ := strings.Cut(text, " ")
	if !strings.Contains(id, "#") || cardSession(id) == "" {
		return false, nil
	}
	return true, s.press(ctx, c, id, strings.TrimSpace(rest))
}

var noAsk = refuse(http.StatusUnprocessableEntity, "no_such_ask", "the ask is no longer open")

// press is button id pressed by c (words: what an input button's message said).
func (s *Service) press(ctx context.Context, c Caller, id, words string) error {
	i := strings.LastIndexByte(id, '#')
	if i < 0 || cardSession(id) == "" {
		return refuse(http.StatusUnprocessableEntity, "no_such_button", "no button "+id)
	}
	card, opt := id[:i], id[i+1:]
	st, err := s.channel(c, cardSession(card))
	if err != nil {
		return err
	}
	j := slices.IndexFunc(st.Asks, func(a api.Ask) bool { return cardID(st.ID, a.RequestID) == card })
	if j < 0 {
		return noAsk
	}
	cd := session.CardOf(st.ID, st.Asks[j])
	single := cd.Kind != "question" || (len(cd.Summary) == 1 && len(cd.Multi) == 0)
	var ans *api.CardAnswer
	s.mu.Lock()
	switch {
	case opt == "submit":
		a := api.CardAnswer{Card: card}
		picks := s.picks[card]
		for g := range cd.Summary {
			a.Choices = append(a.Choices, picks[g]...)
		}
		if w := s.words[card]; len(w) > 0 {
			a.Words = make([]string, len(cd.Summary))
			for g, t := range w {
				if g < len(a.Words) {
					a.Words[g] = t
				}
			}
		}
		ans = &a
	case len(opt) > 1 && opt[0] == 'w' && cd.Kind == "question":
		g, err := strconv.Atoi(opt[1:])
		if err != nil || g < 0 || g >= len(cd.Summary) || words == "" {
			s.mu.Unlock()
			return refuse(http.StatusUnprocessableEntity, "no_such_button", "no button "+id)
		}
		if single {
			ans = &api.CardAnswer{Card: card, Words: []string{words}}
			break
		}
		if s.words[card] == nil {
			s.words[card] = map[int]string{}
		}
		s.words[card][g] = words
		delete(s.picks[card], g)
	default:
		k := slices.IndexFunc(cd.Options, func(o api.CardOption) bool { return o.ID == opt })
		if k < 0 {
			s.mu.Unlock()
			return refuse(http.StatusUnprocessableEntity, "no_such_button", "no button "+id)
		}
		if single {
			ans = &api.CardAnswer{Card: card, Option: opt, Message: words}
			break
		}
		g := cd.Options[k].Group
		if s.picks[card] == nil {
			s.picks[card] = map[int][]string{}
		}
		if slices.Contains(cd.Multi, g) {
			l := s.picks[card][g]
			if n := slices.Index(l, opt); n >= 0 {
				s.picks[card][g] = slices.Delete(l, n, n+1)
			} else {
				s.picks[card][g] = append(l, opt)
			}
		} else {
			s.picks[card][g] = []string{opt}
		}
		delete(s.words[card], g)
	}
	s.mu.Unlock()
	if ans != nil {
		if err := s.b.Answer(ctx, c, *ans); err != nil {
			return err
		}
		s.forget(card)
	}
	s.emit(st.Session, Event{Type: "interaction/button", Button: &Button{ID: id}, User: &User{ID: c.Login, Name: c.LoginName}}, nil, "")
	return nil
}

func (s *Service) forget(card string) {
	s.mu.Lock()
	delete(s.picks, card)
	delete(s.words, card)
	s.mu.Unlock()
}

// serveInternal serves /v1/internal/: button (an action button pressed: {id}, or a card answered
// whole: {id: <card>, option, choices, words, message}).
func (s *Service) serveInternal(w http.ResponseWriter, r *http.Request, name string) {
	if name != "button" {
		s.fail(w, refuse(http.StatusNotFound, "no_such_api", "no API internal/"+name))
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
	var req struct {
		ID      string   `json:"id"`
		Option  string   `json:"option"`
		Choices []string `json:"choices"`
		Words   []string `json:"words"`
		Message string   `json:"message"`
	}
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		s.fail(w, bad("the body: "+err.Error()))
		return
	}
	if err := decode(b, &req); err != nil {
		s.fail(w, err)
		return
	}
	if req.ID == "" {
		s.fail(w, bad("id"))
		return
	}
	if !strings.Contains(req.ID, "#") {
		// A card answered whole.
		id := cardSession(req.ID)
		if id == "" {
			s.fail(w, refuse(http.StatusUnprocessableEntity, "no_such_ask", "no ask "+req.ID))
			return
		}
		st, err := s.channel(c, id)
		if err == nil {
			err = s.b.Answer(r.Context(), c, api.CardAnswer{Card: req.ID, Option: req.Option, Choices: req.Choices, Words: req.Words, Message: req.Message})
		}
		if err != nil {
			s.fail(w, err)
			return
		}
		s.forget(req.ID)
		s.emit(st.Session, Event{Type: "interaction/button", Button: &Button{ID: req.ID}, User: &User{ID: c.Login, Name: c.LoginName}}, nil, "")
		reply(w, http.StatusOK, struct{}{})
		return
	}
	if err := s.press(r.Context(), c, req.ID, req.Message); err != nil {
		s.fail(w, err)
		return
	}
	reply(w, http.StatusOK, struct{}{})
}
