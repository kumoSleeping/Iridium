package satori

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"

	"github.com/kumoSleeping/iridium/internal/api"
	channel_ "github.com/kumoSleeping/iridium/internal/channel"
	"github.com/kumoSleeping/iridium/internal/store"
)

// What a channel's members do to a message said there (docs/channels.md "撤回" "表情"): take it
// back for everyone (message.delete by its number), put an emoji on it (reaction.*). Satori's own
// shape: message-deleted, reaction-added, reaction-removed.

// Acts is a Backend that keeps the channels' rules for these: who a caller is in a channel and
// whether it may act there (one of its people: who may speak in it may take back any message and
// react to any), and what follows one done.
type Acts interface {
	// Actor is who c acts as in channel ch: a login (its id and name), an agent's ir as the agent
	// ("agent:<session>", its nick). err: it may not act there (it may still read).
	Actor(c Caller, ch string) (User, error)
	// Recalled: c took back m (as it was) in its channel: the marks, the list.
	Recalled(c Caller, m channel_.Message)
	// Reacted: who (Actor's) put emoji on message seq of channel ch: the agents that receive who
	// are given it.
	Reacted(c Caller, who User, ch string, seq int64, emoji string)
}

// Emoji is Satori's emoji resource: a Unicode emoji is its own id.
type Emoji struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

// ReactionSum is one emoji on a message, as message.list and message.get give it (_data.reactions):
// how many put it on, who (in that order), whether the caller is one of them.
type ReactionSum struct {
	Emoji string `json:"emoji"`
	Count int    `json:"count"`
	Users []User `json:"users"`
	Me    bool   `json:"me,omitempty"`
}

// sums are a message's reactions summed by emoji, in the order each was first put on.
func sums(rs []store.Reaction, me string) []ReactionSum {
	var out []ReactionSum
	for _, r := range rs {
		i := slices.IndexFunc(out, func(x ReactionSum) bool { return x.Emoji == r.Emoji })
		if i < 0 {
			out = append(out, ReactionSum{Emoji: r.Emoji, Users: []User{}})
			i = len(out) - 1
		}
		out[i].Count++
		out[i].Users = append(out[i].Users, User{ID: r.Member, Name: r.Name})
		if me != "" && r.Member == me {
			out[i].Me = true
		}
	}
	return out
}

// me is who c is in channel ch, for what it has done there ("" not known).
func (s *Service) me(c Caller, ch string) string {
	if a, ok := s.b.(Acts); ok {
		u, _ := a.Actor(c, ch)
		return u.ID
	}
	return c.Login
}

// withReactions puts each listed message's reactions in its _data.
func (s *Service) withReactions(c Caller, ch string, ms []Message) {
	if s.log == nil || len(ms) == 0 {
		return
	}
	var seqs []int64
	for _, m := range ms {
		if n, err := strconv.ParseInt(m.ID, 10, 64); err == nil {
			seqs = append(seqs, n)
		}
	}
	by := s.log.Reactions(ch, seqs)
	if len(by) == 0 {
		return
	}
	me := s.me(c, ch)
	for i, m := range ms {
		n, _ := strconv.ParseInt(m.ID, 10, 64)
		d, ok := m.Data.(LogData)
		if rs := by[n]; ok && len(rs) > 0 {
			d.Reactions = sums(rs, me)
			ms[i].Data = d
		}
	}
}

// BlockData is what block.list adds to a block of a session's record that is a message of its
// own channel too: its number there, its reactions.
type BlockData struct {
	Seq       int64         `json:"seq"`
	Reactions []ReactionSum `json:"reactions,omitempty"`
}

// blockRef is what a block of session's record is in its channel (channel.Service's refs): a
// person's words by their input, an agent's by its place.
func blockRef(session string, b api.Block) string {
	if b.Kind == api.BlockKindUser && b.ID != "" {
		return b.ID
	}
	return "b:" + session + ":" + b.At
}

// ---- message.delete ----

// recall takes back message m of channel st (message.delete of a message said there) as c: what
// still waits in a queue for it is withdrawn, then it is gone for everyone, a line of Iridium's
// own says who took it back, and message-deleted is told.
func (s *Service) recall(c Caller, st *api.SessionState, m channel_.Message, acts Acts) error {
	if !channel_.Recallable(m) {
		return refuse(http.StatusUnprocessableEntity, "no_such_message", channel_.ErrNotWords.Error())
	}
	who, err := acts.Actor(c, st.ID)
	if err != nil {
		return err
	}
	// Not given yet: not given at all. Who was given it is told it is gone the next time it
	// is called there.
	if m.Kind == channel_.KindUser && m.Ref != "" && slices.ContainsFunc(st.Queue, func(q api.Queued) bool { return q.ID == m.Ref }) {
		if err := s.b.EditQueued(c, st.ID, m.Ref, nil); err != nil {
			s.logf("satori: channel %s: withdrawing %d: %v", st.ID, m.Seq, err)
		}
	}
	if _, err := s.b.EditGiven(c, st.ID, m.Seq, m.Text, nil); err != nil {
		s.logf("satori: channel %s: withdrawing %d: %v", st.ID, m.Seq, err)
	}
	was := m
	if _, err := s.log.Recall(st.ID, m.Seq, who.Name); err != nil {
		if errors.Is(err, channel_.ErrNoMessage) {
			return errNoMessage(strconv.FormatInt(m.Seq, 10))
		}
		return err
	}
	// Told with what it was (its kind, where it is from), not its words: a page takes away what
	// it drew of it, the block of the agent's record too.
	d := logData(was)
	msg := Message{ID: strconv.FormatInt(was.Seq, 10), User: s.authorOf(was, nil), CreatedAt: was.At, Data: d}
	op := who
	s.emit(st.Session, Event{Type: "message-deleted", Message: &msg, User: msg.User, Operator: &op}, nil, d.Kind)
	name := who.Name
	if name == "" {
		name = who.ID
	}
	s.Note(st.ID, name+" 撤回了一条消息", "", &channel_.Event{Type: channel_.EventRecall, Seq: was.Seq, Member: who.ID, By: name})
	acts.Recalled(c, was)
	return nil
}

// ---- reaction.* ----

type reactionReq struct {
	ChannelID string `json:"channel_id"`
	MessageID string `json:"message_id"`
	EmojiID   string `json:"emoji_id"`
	Emoji     string `json:"emoji"` // as emoji_id
	UserID    string `json:"user_id"`
	Next      string `json:"next"`
}

// reactionTarget is the message a reaction request names, in the channel c sees, and who c is
// there; mustAct: c is to act there (else it reads).
func (s *Service) reactionTarget(c Caller, body []byte, mustAct bool) (reactionReq, *api.SessionState, channel_.Message, User, Acts, error) {
	var req reactionReq
	if err := decode(body, &req); err != nil {
		return req, nil, channel_.Message{}, User{}, nil, err
	}
	if req.EmojiID == "" {
		req.EmojiID = req.Emoji
	}
	acts, ok := s.b.(Acts)
	if !ok || s.log == nil {
		return req, nil, channel_.Message{}, User{}, nil, refuse(http.StatusNotFound, "no_such_api", "no reactions here")
	}
	if req.MessageID == "" {
		return req, nil, channel_.Message{}, User{}, nil, bad("message_id")
	}
	st, err := s.channel(c, req.ChannelID)
	if err != nil {
		return req, nil, channel_.Message{}, User{}, nil, err
	}
	s.log.Touch(st.ID)
	m, ok := s.logged(st.ID, req.MessageID)
	if !ok || !channel_.Recallable(m) {
		return req, nil, channel_.Message{}, User{}, nil, errNoMessage(req.MessageID)
	}
	who, err := acts.Actor(c, st.ID)
	if err != nil && mustAct {
		return req, nil, channel_.Message{}, User{}, nil, err
	}
	return req, st, m, who, acts, nil
}

// emoji is req's emoji, checked: one Unicode emoji.
func (r reactionReq) emoji(need bool) (string, error) {
	if r.EmojiID == "" && !need {
		return "", nil
	}
	e, err := channel_.Emoji(r.EmojiID)
	if err != nil {
		return "", bad("emoji_id: one Unicode emoji")
	}
	return e, nil
}

// reactionEvent tells those who see channel st that who put emoji on (or took it off) message m.
func (s *Service) reactionEvent(st *api.SessionState, typ string, m channel_.Message, who User, emoji string) {
	d := logData(m)
	msg := Message{ID: strconv.FormatInt(m.Seq, 10), Data: LogData{Kind: d.Kind, Session: d.Session, Block: d.Block, Input: d.Input}}
	u := who
	s.emit(st.Session, Event{Type: typ, Message: &msg, User: &u, Emoji: &Emoji{ID: emoji, Name: emoji}}, nil, "")
}

// reactionCreate puts the caller's emoji on a message of a channel (reaction.create): those who
// see it are told (reaction-added), the agents that receive the caller given it.
func (s *Service) reactionCreate(_ context.Context, c Caller, body []byte) (any, error) {
	req, st, m, who, acts, err := s.reactionTarget(c, body, true)
	if err != nil {
		return nil, err
	}
	e, err := req.emoji(true)
	if err != nil {
		return nil, err
	}
	added, err := s.log.React(st.ID, m.Seq, e, who.ID, who.Name)
	if err != nil {
		return nil, err
	}
	if added {
		s.reactionEvent(st, "reaction-added", m, who, e)
		acts.Reacted(c, who, st.ID, m.Seq, e)
	}
	return struct{}{}, nil
}

// reactionDelete takes an emoji off a message (reaction.delete): the caller's own, or user_id's.
func (s *Service) reactionDelete(_ context.Context, c Caller, body []byte) (any, error) {
	req, st, m, who, _, err := s.reactionTarget(c, body, true)
	if err != nil {
		return nil, err
	}
	e, err := req.emoji(true)
	if err != nil {
		return nil, err
	}
	of := who
	if req.UserID != "" && req.UserID != who.ID {
		of = User{ID: req.UserID}
		for _, r := range s.log.Reactions(st.ID, []int64{m.Seq})[m.Seq] {
			if r.Member == req.UserID {
				of.Name = r.Name
			}
		}
	}
	had, err := s.log.Unreact(st.ID, m.Seq, e, of.ID)
	if err != nil {
		return nil, err
	}
	if had {
		s.reactionEvent(st, "reaction-removed", m, of, e)
	}
	return struct{}{}, nil
}

// reactionClear takes every reaction of emoji_id (none given: of every emoji) off a message.
func (s *Service) reactionClear(_ context.Context, c Caller, body []byte) (any, error) {
	req, st, m, _, _, err := s.reactionTarget(c, body, true)
	if err != nil {
		return nil, err
	}
	e, err := req.emoji(false)
	if err != nil {
		return nil, err
	}
	gone, err := s.log.ClearReactions(st.ID, m.Seq, e)
	if err != nil {
		return nil, err
	}
	for _, r := range gone {
		s.reactionEvent(st, "reaction-removed", m, User{ID: r.Member, Name: r.Name}, r.Emoji)
	}
	return struct{}{}, nil
}

// reactionList is who put emoji_id on a message (reaction.list), in that order: one page.
func (s *Service) reactionList(_ context.Context, c Caller, body []byte) (any, error) {
	req, st, m, _, _, err := s.reactionTarget(c, body, false)
	if err != nil {
		return nil, err
	}
	e, err := req.emoji(true)
	if err != nil {
		return nil, err
	}
	out := List[User]{Data: []User{}}
	for _, r := range s.log.Reactions(st.ID, []int64{m.Seq})[m.Seq] {
		if r.Emoji == e {
			out.Data = append(out.Data, s.reactor(r))
		}
	}
	return out, nil
}

// reactor is who put r on: an agent as its user (its name, its nick, its face), anyone else as
// kept.
func (s *Service) reactor(r store.Reaction) User {
	if _, ok := store.SessionOfMember(r.Member); ok {
		u := s.authorOf(channel_.Message{ChannelMessage: store.ChannelMessage{Author: r.Member}}, nil)
		return *u
	}
	return User{ID: r.Member, Name: r.Name}
}
