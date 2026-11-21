package server

import (
	"cmp"
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/channel"
	"github.com/kumoSleeping/iridium/internal/ctx/assemble"
	"github.com/kumoSleeping/iridium/internal/deliver"
	"github.com/kumoSleeping/iridium/internal/satori"
	"github.com/kumoSleeping/iridium/internal/session"
	"github.com/kumoSleeping/iridium/internal/store"
)

// What a channel's members do to a message (docs/channels.md "撤回" "表情"), the server's part:
// who may (whoever may speak in the channel: people, apps and agents alike), and what follows:
// a message taken back leaves no mark and no last words behind it; a reaction is given to the
// agents that receive its author, as words that name nobody are.

// Actor is who c acts as in channel ch (satori.Acts): an agent's ir as the agent, a script's as
// the script, a login as itself; with an error where it is not one of the channel's people.
func (b satoriBackend) Actor(c satori.Caller, ch string) (satori.User, error) {
	s, cl := b.s, callerAs(c)
	var u satori.User
	switch id := cmp.Or(cl.session, cl.Session); {
	case cl.script != "":
		_, name, _ := strings.Cut(cl.script, "/")
		u = satori.User{ID: scriptMember(name), Name: name}
	case id != "":
		u = satori.User{ID: store.AgentMember(id), Name: cmp.Or(s.ch.Nick(id), s.agentName(id))}
	default:
		u = s.satoriUser(cl)
		if c.LoginName != "" && !cl.Host() {
			u.Name = c.LoginName
		}
	}
	return u, s.inChannel(cl, ch)
}

// Recalled: a message was taken back (satori.Acts): the marks that pointed at it went with it,
// and the list's last words may have been it.
func (b satoriBackend) Recalled(_ satori.Caller, m channel.Message) {
	s := b.s
	s.recalls.add(m.Channel)
	s.atsChanged()
	s.listSoon()
}

// Reacted: who put emoji on message seq of channel ch (satori.Acts): given to the agents that
// receive who, in the background.
func (b satoriBackend) Reacted(c satori.Caller, who satori.User, ch string, seq int64, emoji string) {
	cl := callerAs(c)
	go b.s.giveReaction(cl, who, ch, seq, emoji)
}

// giveReaction gives a reaction to the agents of channel ch whose receive set has its author
// (deliver.Reacts): an agent's counts in the run as its words do, and goes nowhere past
// MaxHops. The agent of a session alone in its channel gets the reaction's line alone.
func (s *Server) giveReaction(c caller, who satori.User, ch string, seq int64, emoji string) {
	m, ok := s.ch.Get(ch, seq)
	if !ok {
		return
	}
	list, by := s.agentsOf(ch)
	from := deliver.From{Member: readerOf(c)}
	if id, agent := store.SessionOfMember(who.ID); agent {
		from = deliver.From{Member: who.ID, Session: id}
	} else if strings.HasPrefix(who.ID, scriptMemberPrefix) {
		from.Member = who.ID
	}
	targets := deliver.Reacts(from, list)
	if len(targets) == 0 {
		return
	}
	lk := s.chain.of(ch, from.Session)
	lk.origin = cmp.Or(lk.origin, c.ID())
	if passes(from) && lk.hop >= deliver.MaxHops {
		return
	}
	r := assemble.Reaction{Seq: seq, Who: cmp.Or(who.Name, who.ID), Emoji: emoji, Text: m.Text}
	// Who it is from, as the agent reads a message's sender: Iridium's own line.
	sender := session.From(api.SourceIridium, api.SourceIridium)
	for _, t := range targets {
		if from.Session == "" && !passes(from) {
			// A person wakes only the agents it may use (Claude: the owner's own).
			if _, err := s.using(c, t); err != nil {
				continue
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		var err error
		if by[t].Direct() && !s.multi(ch) {
			err = s.giveReactionAlone(ctx, ch, t, r, sender)
		} else {
			_, err = s.deliverAs(ctx, ch, t, seq, m.Text, "", sender, nil, reach(from, lk), &r)
		}
		cancel()
		if err != nil {
			s.logf("server: channel %s: a reaction to %d to %s: %v", ch, seq, t, err)
			continue
		}
		s.noteOrigin(c, t, c.session)
	}
}

// giveReactionAlone gives the agent of session to, alone in its channel ch, the line of reaction
// r: no channel's context, which such an agent is never given.
func (s *Server) giveReactionAlone(ctx context.Context, ch, to string, r assemble.Reaction, from string) error {
	in, err := assemble.Reacted(r)
	if err != nil {
		return err
	}
	id := session.NewInputID()
	if rec, err := json.Marshal(in.Record()); err == nil {
		if err := s.st.AddInjection(store.Injection{Session: to, Channel: ch, Input: id, Seq: r.Seq, Record: string(rec)}); err != nil {
			s.logf("server: channel %s: keeping what %s was given: %v", ch, to, err)
		}
	}
	if _, _, err := s.m.SendAs(ctx, to, id, in.String(), from, true); err != nil {
		_ = s.st.RemoveInjection(to, id)
		return err
	}
	return nil
}

// recalledFor are the numbers of the messages of channel ch, up to seen, that were taken back
// after session's agent was last given something there: it had them, and is told they are gone.
func (s *Server) recalledFor(session, ch string, seen int64) []int64 {
	if !s.recalls.has(s.st, ch) {
		return nil
	}
	since := s.st.LastGivenAt(session, ch)
	if since == 0 {
		return nil
	}
	var out []int64
	for _, r := range s.ch.Recalls(ch, since) {
		if r.Seq <= seen {
			out = append(out, r.Seq)
		}
	}
	return out
}

// recalls are the channels some message was taken back in: only theirs are looked at for it.
type recalls struct {
	mu   sync.Mutex
	of   map[string]bool
	read bool
}

func (r *recalls) add(ch string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.of == nil {
		r.of = map[string]bool{}
	}
	r.of[ch] = true
}

func (r *recalls) has(st *store.Store, ch string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.read {
		all, err := st.RecalledChannels()
		if err != nil {
			return true
		}
		if r.of == nil {
			r.of = map[string]bool{}
		}
		for c := range all {
			r.of[c] = true
		}
		r.read = true
	}
	return r.of[ch]
}

// recallSlack: how much later than a message taken back its block in the record may be written.
const recallSlack = int64(2 * time.Minute / time.Millisecond)

// recordLast is what a session alone in its channel is listed with: its record's last words
// (rec), unless they may be a message taken back since; then the channel's last that is left.
func (s *Server) recordLast(id string, rec *api.LastSaid) (*api.LastSaid, bool) {
	if rec == nil || !s.recalls.has(s.st, id) {
		return nil, false
	}
	_, latest := s.ch.RecalledRefs(id)
	if latest == 0 || rec.At > latest+recallSlack {
		return nil, false
	}
	m, ok := s.ch.Latest(id)
	if !ok {
		return nil, true
	}
	return s.lastSaid(m), true
}

var _ satori.Acts = satoriBackend{}
