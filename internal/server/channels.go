package server

import (
	"bytes"
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kumoSleeping/iridium/internal/access"
	"github.com/kumoSleeping/iridium/internal/activity"
	"github.com/kumoSleeping/iridium/internal/agent"
	"github.com/kumoSleeping/iridium/internal/agents"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/channel"
	"github.com/kumoSleeping/iridium/internal/ctx/assemble"
	"github.com/kumoSleeping/iridium/internal/ctx/strategy"
	"github.com/kumoSleeping/iridium/internal/deliver"
	"github.com/kumoSleeping/iridium/internal/gate"
	"github.com/kumoSleeping/iridium/internal/satori"
	"github.com/kumoSleeping/iridium/internal/session"
	"github.com/kumoSleeping/iridium/internal/store"
)

// Channels of several agents (docs/channels.md): who is in one, and where a message said there
// goes. A session's channel with its own agent alone is the session it always was (sendAs). With
// other sessions in it (subscriptions: they see what is said and speak with ir), a message is
// said in the channel and given only to the agents the rules wake (package deliver): each gets
// the words under their number and what the channel said since it last read it (package
// strategy plans it, assemble writes it, and what was given is kept: store.Injection).

// MaxAgents is how many agents a channel may have invited; those its agents launched (Spawned,
// by an agent in it) are not counted (docs/channels.md "成员"). MaxAll: how many in all.
const (
	MaxAgents = 8
	MaxAll    = 32
)

// atMost is a channel full at max agents, each counted (a test's).
func atMost(max int) func([]string) bool {
	return func(agents []string) bool { return len(agents) >= max }
}

// full is channel ch's room for one more agent: launched (started by an agent in it) or not.
// Those counts passes over (archived) are not counted.
func (s *Server) full(ch string, launched bool) func([]string) bool {
	return func(agents []string) bool {
		agents = slices.DeleteFunc(slices.Clone(agents), func(a string) bool { return !s.counts(ch, a) })
		if len(agents) >= MaxAll {
			return true
		}
		if launched {
			return false
		}
		invited := slices.DeleteFunc(agents, func(a string) bool { return s.launchedIn(ch, a, agents) })
		return len(invited) >= MaxAgents
	}
}

// launchedIn: session a was started for channel ch by one of its agents.
func (s *Server) launchedIn(ch, a string, agents []string) bool {
	sp := s.spawnedOf(a)
	return sp != nil && sp.Channel == ch && sp.By != "" && sp.By != a && slices.Contains(agents, sp.By)
}

// MaxKinHops is how many times in a row an agent and the one that started it for a channel may
// pass a message on to each other there since a person last spoke (the user, 2026-11-14): wider
// than deliver.MaxHops, which is for agents that are not kin, and not without end.
const MaxKinHops = 12

// link is where a session's turn stands in a channel's run: the agents passing what is said
// there on to each other since a person last spoke in it. origin: that person; epoch: which run
// of the channel (a person speaking begins the next); hop: how many agents in a row passed it
// on to reach this turn; kin: how many of the passes on the way were between an agent and the
// one that started it, which count apart.
type link struct {
	origin string
	epoch  int64
	hop    int
	kin    int
}

// run is a channel's run now, and where each of its agents' turns stands in it.
type run struct {
	origin string
	epoch  int64
	at     map[string]link
}

// chains are the channels' runs: counted by channel, so that what agents pass on in one never
// counts in another, and begun anew by a person speaking, so that no count outlives it.
type chains struct {
	mu    sync.Mutex
	runs  map[string]*run
	locks map[string]*sync.Mutex // a session is given one message at a time
}

func (c *chains) lock(session string) func() {
	c.mu.Lock()
	if c.locks == nil {
		c.locks = map[string]*sync.Mutex{}
	}
	l := c.locks[session]
	if l == nil {
		l = &sync.Mutex{}
		c.locks[session] = l
	}
	c.mu.Unlock()
	l.Lock()
	return l.Unlock
}

// run is channel ch's run; with c.mu held.
func (c *chains) run(ch string) *run {
	if c.runs == nil {
		c.runs = map[string]*run{}
	}
	r := c.runs[ch]
	if r == nil {
		r = &run{at: map[string]link{}}
		c.runs[ch] = r
	}
	return r
}

// person: a person (origin) spoke in channel ch: a new run begins, every count at nothing. The
// link of what that person said.
func (c *chains) person(ch, origin string) link {
	c.mu.Lock()
	defer c.mu.Unlock()
	r := c.run(ch)
	r.origin, r.epoch, r.at = origin, r.epoch+1, map[string]link{}
	return link{origin: origin, epoch: r.epoch}
}

// of is where session's turn stands in channel ch's run now.
func (c *chains) of(ch, session string) link {
	c.mu.Lock()
	defer c.mu.Unlock()
	r := c.run(ch)
	l := r.at[session]
	l.origin, l.epoch = r.origin, r.epoch
	return l
}

// pass: a message of channel ch reached session's agent with link l. What agents pass on
// never shortens the count its receiver stands at (two lines crossing would else go on past
// the limit); one of a run that is over (a person spoke since) counts for nothing.
func (c *chains) pass(ch, session string, l link) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r := c.run(ch)
	if l.epoch != r.epoch {
		return
	}
	cur := r.at[session]
	r.at[session] = link{hop: max(cur.hop, l.hop), kin: max(cur.kin, l.kin)}
}

// called is a channel an agent was called in by a subscription: for message seq; spoke, it has
// said something there since.
type called struct {
	Channel string `json:"channel"`
	Seq     int64  `json:"seq"`
	Spoke   bool   `json:"spoke,omitempty"`
	// From: the line its own record was at when it was called: where what it does for the call
	// begins.
	From int64 `json:"from,omitempty"`
	// Input: the input the call was given to the agent as. The call is closed by the end of the
	// turn that took that input, not by one that ends while it still waits in the queue. None:
	// a call kept before calls had one, closed by any turn's end.
	Input string `json:"input,omitempty"`
	// old: read from the store as this process began, its input given to the agent by then:
	// the turn that took it may have ended while nobody listened (calls.began).
	old bool
}

// calls are the calls each session's agent has open: made as it is given a channel's message,
// closed as the turn that took it ends (turnEnded). Kept in the store too (callsKey), so that a
// turn going on across a restart still has its reply said where it was called (the user,
// 2026-11-13).
type calls struct {
	mu sync.Mutex
	of map[string][]called
	st *store.Store
}

func callsKey(session string) string { return "calls:" + session }

// open reads every session's calls from the store, as the process begins: those whose input
// the agent had been given by then are old.
func (k *calls) open() {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.of == nil {
		k.of = map[string][]called{}
	}
	kept, err := k.st.KeptUnder(callsKey(""))
	if err != nil {
		return
	}
	for key, v := range kept {
		var cs []called
		if v == "" || json.Unmarshal([]byte(v), &cs) != nil {
			continue
		}
		for i := range cs {
			cs[i].old = !waits(k.st, cs[i], nil)
		}
		k.of[strings.TrimPrefix(key, callsKey(""))] = cs
	}
}

// waits: call c's input still waits in its session's queue (waiting: those a turn's end named),
// so no turn has taken it yet.
func waits(st *store.Store, c called, waiting []string) bool {
	if c.Input == "" {
		return false
	}
	if slices.Contains(waiting, c.Input) {
		return true
	}
	if st == nil {
		return false
	}
	_, state, ok := st.InputOf(c.Input)
	return ok && state != "sent"
}

// load is session's calls, from the store if not had yet; with k.mu held.
func (k *calls) load(session string) []called {
	if k.of == nil {
		k.of = map[string][]called{}
	}
	if cs, ok := k.of[session]; ok || k.st == nil {
		return cs
	}
	var cs []called
	if kept := k.st.Kept(callsKey(session)); kept != "" {
		_ = json.Unmarshal([]byte(kept), &cs)
	}
	k.of[session] = cs
	return cs
}

// save keeps session's calls as they are now; with k.mu held.
func (k *calls) save(session string) {
	cs := k.of[session]
	if len(cs) == 0 {
		delete(k.of, session)
	}
	if k.st == nil {
		return
	}
	v := ""
	if len(cs) > 0 {
		b, _ := json.Marshal(cs)
		v = string(b)
	}
	_ = k.st.Keep(callsKey(session), v)
}

func (k *calls) add(session string, c called) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.of[session] = append(k.load(session), c)
	k.save(session)
}

// current is the channel session's agent works for now: that of its latest open call; "" none.
func (k *calls) current(session string) string {
	k.mu.Lock()
	defer k.mu.Unlock()
	cs := k.load(session)
	if len(cs) == 0 {
		return ""
	}
	return cs[len(cs)-1].Channel
}

// spoke: session's agent said something in channel, its record at line to by then. It gives
// the line the stretch that led to it began at (nil: not called there; 0 is a line, that of a
// session called before its record had any), and the next begins at to.
func (k *calls) spoke(session, channel string, to int64) (from *int64) {
	k.mu.Lock()
	defer k.mu.Unlock()
	cs := k.load(session)
	for i := range cs {
		if c := &cs[i]; c.Channel == channel {
			if from == nil || c.From < *from {
				from = new(c.From)
			}
			c.Spoke, c.From = true, to
		}
	}
	k.save(session)
	return from
}

// forget closes session's call c (the one its reply was said for).
func (k *calls) forget(session string, c called) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.of[session] = slices.DeleteFunc(k.load(session), func(x called) bool { return x.Channel == c.Channel && x.Seq == c.Seq })
	k.save(session)
}

// take are those of session's open calls that closes says are over, which are then closed; the
// rest stay open.
func (k *calls) take(session string, closes func(called) bool) []called {
	k.mu.Lock()
	defer k.mu.Unlock()
	var out, kept []called
	for _, c := range k.load(session) {
		if closes(c) {
			out = append(out, c)
		} else {
			kept = append(kept, c)
		}
	}
	k.of[session] = kept
	k.save(session)
	return out
}

// turnBegan: a turn of session's agent began. Its old calls (kept from before this process,
// their input given by then) were for a turn that ended while nobody listened: what this turn
// says is not their answer. Each is closed as one nothing was said for.
func (s *Server) turnBegan(id string) {
	unlock := s.chain.lock(id)
	over := s.calls.take(id, func(c called) bool { return c.old })
	unlock()
	s.closeCalls(id, "", over)
}

// turnEnded: a turn of session's agent ended with reply (none: also one cut short, its process
// gone); waiting, the inputs that still wait in its queue. The calls it took are over, those
// whose input still waits stay open for the turn that takes them. In each channel it was called
// in by a subscription and said nothing there with ir, its reply is said there for it (the user,
// 2026-11-13: an agent may simply answer); with no reply, the channel is told it said nothing.
func (s *Server) turnEnded(id, reply string, waiting []string) {
	// Not while it is being given a message: that call is made once the message is its.
	unlock := s.chain.lock(id)
	over := s.calls.take(id, func(c called) bool { return !waits(s.st, c, waiting) })
	unlock()
	s.closeCalls(id, reply, over)
}

// closeCalls closes calls over of session id, whose turn ended with reply (turnEnded).
func (s *Server) closeCalls(id, reply string, over []called) {
	done := map[string]bool{}
	for _, c := range over {
		if c.Spoke || done[c.Channel] || !s.ch.Speaks(c.Channel, id) {
			continue
		}
		done[c.Channel] = true
		if strings.TrimSpace(reply) != "" {
			// Its pointer the stretch of its record since it was called, as ir's would be.
			s.calls.add(id, c)
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			_, err := s.sendChannelAs(ctx, caller{Identity: localIdentity, socket: true, agent: true, session: id}, session.From(gate.HostID, "@"+s.ch.Nick(id)), c.Channel, api.Send{Text: reply}, true)
			cancel()
			s.calls.forget(id, c)
			if err == nil {
				continue
			}
			s.logf("server: channel %s: saying %s's reply: %v", c.Channel, id, err)
		}
		nick := s.ch.Nick(id)
		// Its turn ended in an error (no balance left, a key refused): the channel is told why
		// (kumo, 2026-11-15: not a bare 未发话).
		why, words := s.turnError(id), ""
		// Called by another agent and nothing to say: nothing is told either (kumo, 2026-11-16);
		// only one a person called, or one whose turn failed, leaves its line.
		if why == "" && s.agentSaid(c.Channel, c.Seq) {
			continue
		}
		if why != "" {
			words = "：" + why
		}
		s.sat.Note(c.Channel, fmt.Sprintf("@%s 被叫起（#%d），未在频道发话%s", nick, c.Seq, words), "", &channel.Event{Type: "silent", Member: store.AgentMember(id), Nick: nick, Seq: c.Seq, Reason: why})
		// Started by another agent for this channel: that one is told too.
		if sp := s.spawnedOf(id); sp != nil && sp.By != "" && sp.By != id && sp.Channel == c.Channel {
			if in, err := assemble.ChannelSilent(nick, cmp.Or(s.channelName(c.Channel), c.Channel), c.Seq); err == nil {
				ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
				if _, _, err := s.m.SendAs(ctx, sp.By, session.NewInputID(), in.String(), session.From(gate.HostID, "Iridium"), true); err != nil {
					s.logf("server: channel %s: telling %s that %s said nothing: %v", c.Channel, sp.By, id, err)
				}
				cancel()
			}
		}
	}
}

// agentSaid: message seq of channel ch is an agent's words.
func (s *Server) agentSaid(ch string, seq int64) bool {
	m, ok := s.ch.Get(ch, seq)
	if !ok {
		return false
	}
	_, agent := store.SessionOfMember(m.Author)
	return agent
}

// turnError is why session id's last turn ended in an error, in a few words: its process ended
// badly (its state's error), or the turn's last block is the API's error. "" for none.
func (s *Server) turnError(id string) string {
	if st, err := s.m.State(id); err == nil && st.Status == api.StatusError && st.Error != "" {
		return errorWords(s.MaskText(st.Error))
	}
	p, err := s.m.Messages(id, -1, -1)
	if err != nil {
		return ""
	}
	for i := len(p.Blocks) - 1; i >= 0; i-- {
		b := p.Blocks[i]
		switch {
		case b.Kind == api.BlockKindSystem && b.Event == api.SystemEventAPIError:
			return errorWords(s.MaskText(b.Text))
		case b.Kind == api.BlockKindUser, b.Kind == api.BlockKindAssistant && strings.TrimSpace(b.Text) != "":
			return ""
		}
	}
	return ""
}

// errorWords is an error said short: the usual ones by name (an API's 402, 401, 429), else its
// first line, at most 80 characters.
func errorWords(text string) string {
	low := strings.ToLower(text)
	has := func(words ...string) bool {
		for _, w := range words {
			if strings.Contains(low, w) {
				return true
			}
		}
		return false
	}
	switch {
	case has("402", "insufficient balance", "insufficient_quota", "余额不足", "credit balance"):
		return "余额不足"
	case has("401", "unauthorized", "invalid api key", "authentication", "invalid_api_key"):
		return "认证失败"
	case has("429", "rate limit", "too many requests", "rate_limit"):
		return "请求太频繁"
	}
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	if r := []rune(line); len(r) > 80 {
		line = string(r[:80]) + "…"
	}
	return line
}

func (s *Server) channelRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/internal/channels/{id}/members", s.membersGet)
	mux.HandleFunc("POST /v1/internal/channels/{id}/members", s.memberAdd)
	mux.HandleFunc("PATCH /v1/internal/channels/{id}/members/{member}", s.memberPatch)
	mux.HandleFunc("DELETE /v1/internal/channels/{id}/members/{member}", s.memberRemove)
	mux.HandleFunc("PUT /v1/internal/channels/{id}/people", s.peoplePut)
	mux.HandleFunc("POST /v1/internal/channels/{id}/follow", s.followAdd)
	mux.HandleFunc("GET /v1/internal/channels/{id}/avatar", s.avatarGet)
	mux.HandleFunc("PUT /v1/internal/channels/{id}/avatar", s.avatarPut)
	mux.HandleFunc("DELETE /v1/internal/channels/{id}/avatar", s.avatarPut)
	mux.HandleFunc("GET /v1/internal/channels/{id}/reads", s.readsGet)
	mux.HandleFunc("POST /v1/internal/channels/{id}/read", s.readPost)
	mux.HandleFunc("GET /v1/internal/ats", s.atsGet)
	mux.HandleFunc("GET /v1/internal/agents/{agent}/choices", s.agentChoices)
	mux.HandleFunc("POST /v1/internal/dm", s.dmPost)
	mux.HandleFunc("GET /v1/internal/sessions/{id}/injections", s.use(s.injections))
	s.profileRoutes(mux)
}

// channelOf is what the list of sessions marks session id with: the agents of its channel, where
// it has several (its own first) or is no session's, and the channels it is in beside its own.
// Each agent's kind (ChannelAgent.Agent) the list fills in, which knows it (markChannels).
func (s *Server) channelOf(id string) (agents []api.ChannelAgent, in []string) {
	ms, _ := s.ch.Members(id)
	for _, m := range ms {
		if m.Agent() {
			agents = append(agents, api.ChannelAgent{Session: m.Session, Nick: m.Nick})
		}
	}
	// A session's own channel lists them from two on; one that is no session's (bare), any.
	if _, bare := s.ch.Own(id); len(agents) < 2 && !bare {
		agents = nil
	}
	return agents, s.ch.In(id)
}

// membersChanged tells the pages a channel's members changed: the list of sessions again, and
// the state "channels" (a number that only goes up; a page reads the members again).
func (s *Server) membersChanged() {
	s.hub.Set("channels", s.channelsSeq.Add(1))
	s.m.PublishList()
}

// errNotMember: the caller sees the channel but is not one of its people.
var errNotMember = &access.Denied{Reason: "not one of this channel's people: it can be read, not spoken in"}

// inChannel checks that c is one of the people of channel ch (docs/channels.md "人"): the host
// always; an agent of the channel; else the caller's login, as the channel's people name it.
// Whoever is not may still read.
func (s *Server) inChannel(c caller, ch string) error {
	if c.Host() && !c.byAgent() {
		return nil
	}
	if s.ch.Speaks(ch, cmp.Or(c.session, c.Session)) {
		return nil
	}
	if s.ch.Has(ch, c.App, c.Login) {
		return nil
	}
	return errNotMember
}

// mayAnswer: c may answer session id's asks (docs/channels.md "提问"): it may use the session and
// is in its channel, or it sees one of the channels the session's agent is in and may act there.
func (s *Server) mayAnswer(c caller, id string) error {
	_, err := s.using(c, id)
	if err == nil {
		err = s.inChannel(c, id)
	}
	if err != nil && s.answersIn(c, id) {
		return nil
	}
	return err
}

// answersIn: c, a person or an app, sees one of the channels session id's agent is in beside its
// own, and is one of those who act there.
func (s *Server) answersIn(c caller, id string) bool {
	if c.byAgent() {
		return false // people and apps answer; an agent, only where it may use the session
	}
	for _, ch := range s.ch.In(id) {
		st, err := s.m.State(ch)
		if err == nil && s.seenBy(c, st.Session) && s.inChannel(c, ch) == nil {
			return true
		}
	}
	return false
}

// memberOf is the member c is as a login.
func memberOf(c caller) string {
	if c.Login == "" {
		return channel.AppMember(c.App)
	}
	return channel.PersonMember(c.App, c.Login)
}

// harnessOf names session's harness, for a nick.
func (s *Server) harnessOf(id string) string {
	st, err := s.m.State(id)
	if err != nil {
		return ""
	}
	return string(st.Agent)
}

// multi: channel ch has more than one agent, or is no session's: what is said in it is given
// to its agents by the rules, not to one session.
func (s *Server) multi(ch string) bool {
	if _, own := s.ch.Own(ch); own {
		return true
	}
	return len(s.ch.Agents(ch)) > 1
}

// seeingChannel is the channel ch names (its id, the start of it, or self: the caller's own) if
// c sees it.
func (s *Server) seeingChannel(c caller, ch string) (string, *api.SessionState, error) {
	ch, err := s.sessionArg(context.Background(), c, ch)
	if err != nil {
		return "", nil, err
	}
	st, err := s.m.State(ch)
	if err != nil {
		return "", nil, err
	}
	if !s.seenBy(c, st.Session) {
		return "", nil, errNoSession
	}
	return ch, st, nil
}

// membersOf are channel ch's members as the API gives them.
func (s *Server) membersOf(c caller, ch string) (api.ChannelMembers, error) {
	ms, err := s.ch.Members(ch)
	if err != nil {
		return api.ChannelMembers{}, err
	}
	out := api.ChannelMembers{Channel: ch, Members: []api.ChannelMember{}, Joined: s.inChannel(c, ch) == nil, Me: memberOf(c)}
	for _, m := range ms {
		am := api.ChannelMember{Member: m.Member, Session: m.Session, Nick: m.Nick, Recv: m.Recv, Send: m.Send, Seen: m.Seen}
		if m.Agent() {
			if st, err := s.m.State(m.Session); err == nil {
				am.Agent, am.Status, am.Cwd, am.Machine = st.Agent, st.Status, st.Cwd, st.Machine
				s.keepChoices(st)
				am.Name = s.sessionName(st)
			}
			p := s.agentProfile(m.Session)
			am.Intro, am.Avatar, am.Background, am.Role = p.Intro, p.Avatar, p.Background, p.Role
		}
		out.Members = append(out.Members, am)
	}
	out.Multi = s.multi(ch)
	return out, nil
}

func (s *Server) membersGet(w http.ResponseWriter, r *http.Request) {
	c := callerOf(r)
	ch, _, err := s.seeingChannel(c, r.PathValue("id"))
	if err != nil {
		s.error(w, err)
		return
	}
	out, err := s.membersOf(c, ch)
	if err != nil {
		s.fault(w, err)
		return
	}
	reply(w, http.StatusOK, out)
}

// memberFault answers with a membership's error.
func (s *Server) memberFault(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errFull):
		refuse(w, http.StatusConflict, "full", err.Error())
	case errors.Is(err, channel.ErrNick), errors.Is(err, channel.ErrRecv), errors.Is(err, channel.ErrPeople):
		refuse(w, http.StatusBadRequest, "bad_request", err.Error())
	case errors.Is(err, channel.ErrNoMember):
		refuse(w, http.StatusUnprocessableEntity, "no_such_member", err.Error())
	case errors.Is(err, channel.ErrMember):
		refuse(w, http.StatusConflict, "member", err.Error())
	default:
		s.error(w, err)
	}
}

// memberAdd puts an agent in the channel: a session there is (Session), or a new one started
// for it. Whoever may use that session, or start that agent there, may add it: a person, or an
// agent of the channel (ir).
func (s *Server) memberAdd(w http.ResponseWriter, r *http.Request) {
	c := callerOf(r)
	ch, st, err := s.seeingChannel(c, r.PathValue("id"))
	if err != nil {
		s.error(w, err)
		return
	}
	if err := s.inChannel(c, ch); err != nil {
		s.error(w, err)
		return
	}
	var req api.NewMember
	if !readJSON(w, r, &req) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	id, err := s.addAgent(ctx, c, ch, st, req)
	if err != nil {
		s.memberFault(w, err)
		return
	}
	if m, ok := s.memberRow(ch, store.AgentMember(id)); ok {
		seq := s.tellMembers(c, ch, fmt.Sprintf("@%s 加入了频道", m.Nick), channel.Event{Type: "join", Member: m.Member, Nick: m.Nick, Name: s.agentName(id)})
		switch {
		case strings.TrimSpace(req.Text) != "":
			// Invited with words (an agent bringing one in to work with: kumo, 2026-11-16): said
			// by the one inviting, calling it, as any message there is.
			if _, err := s.sendAs(ctx, c, s.fromOf(c, ""), ch, api.Send{Text: "@" + m.Nick + " " + req.Text}); err != nil {
				// It is in all the same; the words were not said (409 behind: read, then say them).
				s.logf("server: channel %s: the words for @%s: %v", ch, m.Nick, err)
				s.membersChanged()
				s.memberFault(w, err)
				return
			}
		case seq > 0 && req.Greet:
			s.greet(c, ch, id, seq)
		}
	}
	out, err := s.membersOf(c, ch)
	if err != nil {
		s.fault(w, err)
		return
	}
	s.membersChanged()
	reply(w, http.StatusOK, out)
}

// addAgent puts an agent in channel ch (st: the channel's state) as c: session req.Session,
// which c may use, else a new session of req.Agent, started in req.Cwd at req.Machine (none:
// the channel's folder) as any session is: listed in its folder, with a channel of its own.
// Where it was started and by whom is kept (Spawned). The session it is.
func (s *Server) addAgent(ctx context.Context, c caller, ch string, st *api.SessionState, req api.NewMember) (string, error) {
	// Before a session is started for it; Join counts again, one at a time. One an agent in it
	// starts is launched (not counted among those invited).
	launched := req.Session == "" && c.session != "" && s.ch.Speaks(ch, c.session)
	if s.full(ch, launched)(s.ch.Agents(ch)) {
		return "", errFull
	}
	if req.Nick != "" && !deliver.ValidNick(req.Nick) {
		return "", channel.ErrNick
	}
	id, started := req.Session, false
	if id != "" {
		full, err := s.sessionArg(ctx, c, id)
		if err != nil {
			return "", err
		}
		id = full
		if _, err := s.using(c, id); err != nil {
			return "", err
		}
		if s.ch.Speaks(ch, id) {
			return "", channel.ErrMember
		}
	} else {
		ns := api.NewSession{Agent: req.Agent, Cwd: cmp.Or(req.Cwd, st.Cwd), Machine: st.Machine, Model: req.Model, Mode: req.Mode,
			Effort: req.Effort, Preset: req.Preset, Role: req.Role, Intro: req.Intro, Avatar: req.Avatar, Background: req.Background}
		if req.Cwd != "" {
			ns.Machine = req.Machine
		}
		var err error
		if id, err = s.create(ctx, c, ns); err != nil {
			return "", err
		}
		started = true
		s.keepSpawned(id, api.Spawned{By: c.session, Channel: ch})
	}
	// Its nick with the joining: one that does not join keeps the nick it had.
	m, err := s.ch.Join(ch, id, req.Recv, req.Nick, s.harnessOf, s.full(ch, launched))
	if err != nil {
		if started {
			// Not in the channel after all: not started for it either.
			if err := s.st.Keep(spawnedKey(id), ""); err != nil {
				s.logf("server: forgetting where %s was started: %v", id, err)
			}
			_ = s.m.ArchiveTree(ctx, id, true)
		}
		if errors.Is(err, channel.ErrFull) {
			err = errFull
		}
		return "", err
	}
	s.logf("server: channel %s: %s joined as @%s (%s)", ch, id, m.Nick, m.Recv)
	s.m.Refresh([]string{ch, id})
	return id, nil
}

// launch puts session id, just started by session by's agent (ir new), in the channel by works
// for (docs/channels.md "拉起"): the one whose call it is answering, else its own. It is there as
// one invited by it (Spawned, recv at), not counted among those invited. No room: the session
// goes to the archive, and errFull. first: the words it was started with, given it as a call
// from by in that channel (as by's own words there would be), its answer said there (kumo,
// 2026-11-16: not kept in its own session, a face that flashed up and went).
func (s *Server) launch(c caller, by, id, first string) error {
	st, err := s.m.State(by)
	if err != nil || st.Archived || st.Subagent || st.Bare || by == id {
		// Not an agent's session (a person's shell, one gone): as any new session, its words
		// its own.
		return s.firstWords(c, by, id, first)
	}
	ch := s.calls.current(by)
	if ch == "" || !s.ch.Speaks(ch, by) {
		ch = by
	}
	s.keepSpawned(id, api.Spawned{By: by, Channel: ch})
	m, err := s.ch.Join(ch, id, channel.RecvAt, "", s.harnessOf, s.full(ch, true))
	if err != nil {
		if err := s.st.Keep(spawnedKey(id), ""); err != nil {
			s.logf("server: forgetting where %s was started: %v", id, err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = s.m.ArchiveTree(ctx, id, true)
		if errors.Is(err, channel.ErrFull) {
			err = errFull
		}
		return &createError{err}
	}
	s.logf("server: channel %s: %s launched %s as @%s", ch, by, id, m.Nick)
	s.m.Refresh([]string{ch, id})
	seq := s.tellMembers(c, ch, fmt.Sprintf("@%s 加入了频道", m.Nick), channel.Event{Type: "join", Member: m.Member, Nick: m.Nick, Name: s.agentName(id)})
	s.membersChanged()
	if strings.TrimSpace(first) == "" {
		return nil
	}
	if seq == 0 {
		return s.firstWords(c, by, id, first)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	from := session.From(gate.HostID, "@"+s.ch.Nick(by))
	if _, err := s.deliverTo(ctx, ch, id, seq, first, "", from, false, nil, s.reach(ch, by, id, s.chain.of(ch, by))); err != nil {
		s.logf("server: channel %s: %s's first words to %s: %v", ch, by, id, err)
		return s.firstWords(c, by, id, first)
	}
	return nil
}

// firstWords gives session id, started by session by, the words it was started with, as its
// own (no channel).
func (s *Server) firstWords(c caller, by, id, first string) error {
	if strings.TrimSpace(first) == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	_, err := s.sendAs(ctx, c, s.fromOf(c, by), id, api.Send{Text: first})
	return err
}

// counts: session's agent is one channel ch gives messages to and counts among its agents
// (MaxAgents): the channel's own always; one in it by a subscription unless its session is in
// the archive (it is passed over until it is shown again) or gone.
func (s *Server) counts(ch, session string) bool {
	if session == ch {
		return true
	}
	st, err := s.m.State(session)
	return err == nil && !st.Archived
}

// cleared: session next goes on in session old's place (/clear). It is in the channels old was
// in, under old's nick and face; old's own channel's members are next's channel's; what old
// started for a channel, next started. Old, in the archive, is in none of them any more.
func (s *Server) cleared(old, next string) {
	if err := s.ch.Moved(old, next); err != nil {
		s.logf("server: %s going on as %s: its channels: %v", old, next, err)
		return
	}
	if kept, err := s.st.KeptUnder(spawnedKey("")); err == nil {
		for key, v := range kept {
			var sp api.Spawned
			if json.Unmarshal([]byte(v), &sp) != nil || (sp.By != old && sp.Channel != old) {
				continue
			}
			if sp.By == old {
				sp.By = next
			}
			if sp.Channel == old {
				sp.Channel = next
			}
			s.keepSpawned(strings.TrimPrefix(key, spawnedKey("")), sp)
		}
	}
	s.hub.Set("avatars", s.avatarsSeq.Add(1))
	s.membersChanged()
}

// bareChannels are the channels that are no session's, each as the list of sessions has it: no
// agent, in its folder, last active when something was last said in it.
func (s *Server) bareChannels() []api.Session {
	var out []api.Session
	for _, c := range s.ch.Owns() {
		row := api.Session{ID: c.ID, Cwd: c.Cwd, Machine: c.Machine, Title: c.Name, Status: api.StatusIdle, Bare: true,
			UpdatedAt: max(c.Created, s.st.ChannelTouched(c.ID))}
		// Its last words under its name in the list, as a session's are (kumo, 2026-11-16: an
		// agent's reply there was not shown): who said them, what.
		if m, ok := s.ch.Latest(c.ID); ok {
			row.Last = s.lastSaid(m)
		}
		out = append(out, row)
	}
	return out
}

// lastSaid is message m as a row of the list shows it under its name: who said it, what.
func (s *Server) lastSaid(m channel.Message) *api.LastSaid {
	last := &api.LastSaid{Role: "user", Text: head(m.Text, 200), From: m.Name, At: m.At}
	if id, agent := store.SessionOfMember(m.Author); agent {
		last.Role, last.From, last.Nick = "assistant", "", cmp.Or(s.ch.Nick(id), m.Name)
	} else if _, login, ok := strings.Cut(m.Author, "/"); ok {
		last.FromTerminal = login
	} else {
		last.FromTerminal = m.Author
	}
	return last
}

// listsChannel: channel ch is listed with the last words said in it, not its own agent's: one
// that is no session's, or a session's with other agents in it (kumo, 2026-11-16: the list
// showed the agent's last words, not the channel's, the people's and the others' after them).
func (s *Server) listsChannel(ch string) bool {
	if _, bare := s.ch.Own(ch); bare {
		return true
	}
	return len(s.ch.Agents(ch)) > 1
}

// channelLast are the last words said in session id's channel, if it is listed with them
// (listsChannel) and a session's (a bare one's row is made whole by bareChannels).
func (s *Server) channelLast(id string) (*api.LastSaid, bool) {
	if _, bare := s.ch.Own(id); bare || !s.listsChannel(id) {
		return nil, false
	}
	m, ok := s.ch.Latest(id)
	if !ok {
		return nil, false
	}
	return s.lastSaid(m), true
}

// channelAdded: message m was just written to its channel. Someone's words in a channel listed
// with them make the list again, shortly: m's writer may hold a session's lock.
func (s *Server) channelAdded(m channel.Message) {
	if (m.Kind == channel.KindUser || m.Kind == channel.KindSaid) && s.listsChannel(m.Channel) {
		s.listSoon()
	}
}

// listSoon makes the list of sessions again in a moment, once for all asked for meanwhile.
func (s *Server) listSoon() {
	if s.listDue.Swap(true) {
		return
	}
	time.AfterFunc(listDelay, func() {
		s.listDue.Store(false)
		s.m.PublishList()
	})
}

// listDelay: how long listSoon gathers.
const listDelay = 200 * time.Millisecond

// createChannel makes a channel that is no session's as c (channel.create with no agent): in
// req.Cwd, with the people and agents req.Members names; then req.Text, its first words, said
// in it. Whoever may see that folder may make one there.
func (s *Server) createChannel(ctx context.Context, c caller, req api.NewSession) (string, error) {
	id := session.NewChannelID()
	if made, err := s.createChannelAs(ctx, c, req, id); !made {
		return "", err
	} else {
		return id, err
	}
}

// createChannelAs is createChannel under the id given; made: the channel is there, whatever
// came of setting it up.
func (s *Server) createChannelAs(ctx context.Context, c caller, req api.NewSession, id string) (made bool, err error) {
	if !c.Host() {
		app := gate.HostID
		if req.Machine != "" {
			if info, ok := s.gate().Find(req.Machine); ok {
				app = info.ID
			}
		}
		if !s.scopeHas(s.permsFor(c).Scope, app, req.Cwd) {
			return false, &access.Denied{Reason: fmt.Sprintf("%s is out of this one's scope", req.Cwd)}
		}
	}
	setup := api.ChannelSetup{}
	if req.Members != nil {
		setup = *req.Members
	}
	if len(setup.Agents) > MaxAgents {
		return false, &createError{errFull}
	}
	for _, a := range setup.Agents {
		if a.Nick != "" && !deliver.ValidNick(a.Nick) {
			return false, &createError{fmt.Errorf("%w: %w", session.ErrBadRequest, channel.ErrNick)}
		}
	}
	if err := s.ch.Create(store.Channel{ID: id, Cwd: req.Cwd, Machine: req.Machine, Creator: c.ID()}); err != nil {
		return false, &createError{err}
	}
	if err := s.m.ListBare(id, req.Cwd, req.Caller); err != nil {
		s.logf("server: channel %s: listing it: %v", id, err)
	}
	if req.Workspace != 0 {
		if err := s.st.Assign(id, req.Workspace, true); err != nil {
			s.logf("server: channel %s: its group: %v", id, err)
		}
	}
	setup.Nick, setup.Recv = "", ""
	err = s.setUp(ctx, c, id, setup, req.Text)
	s.m.Refresh([]string{id})
	return true, err
}

// spawnedKey is where what is kept of a session started for a channel is.
func spawnedKey(id string) string { return "spawned:" + id }

// keepSpawned keeps where session id was started and by whom.
func (s *Server) keepSpawned(id string, sp api.Spawned) {
	b, err := json.Marshal(sp)
	if err == nil {
		err = s.st.Keep(spawnedKey(id), string(b))
	}
	if err != nil {
		s.logf("server: keeping where %s was started: %v", id, err)
	}
}

// spawnedOf is where session id was started and by whom, if it was started for a channel.
func (s *Server) spawnedOf(id string) *api.Spawned {
	kept := s.st.Kept(spawnedKey(id))
	if kept == "" {
		return nil
	}
	var sp api.Spawned
	if json.Unmarshal([]byte(kept), &sp) != nil {
		return nil
	}
	return &sp
}

var errFull = fmt.Errorf("a channel has at most %d agents invited, %d in all", MaxAgents, MaxAll)

// peoplePut sets who the channel's people are; the one who sets them stays one.
func (s *Server) peoplePut(w http.ResponseWriter, r *http.Request) {
	c := callerOf(r)
	ch, _, err := s.seeingChannel(c, r.PathValue("id"))
	if err != nil {
		s.error(w, err)
		return
	}
	if c.byAgent() {
		forbid(w, errors.New("an agent's ir may not change a channel's people"))
		return
	}
	if err := s.inChannel(c, ch); err != nil {
		s.error(w, err)
		return
	}
	var req api.People
	if !readJSON(w, r, &req) {
		return
	}
	before := s.ch.People(ch)
	if err := s.setPeople(c, ch, req.People); err != nil {
		s.memberFault(w, err)
		return
	}
	after := s.ch.People(ch)
	ev := channel.Event{Type: "people", Everyone: slices.Contains(after, channel.Everyone)}
	for _, p := range after {
		// The host is one always: its own entry (it chose the people) is not news.
		if !slices.Contains(before, p) && p != channel.Everyone && !strings.HasPrefix(p, gate.HostID+"/") {
			ev.Added = append(ev.Added, p)
		}
	}
	for _, p := range before {
		if !slices.Contains(after, p) && p != channel.Everyone && !strings.HasPrefix(p, gate.HostID+"/") {
			ev.Removed = append(ev.Removed, p)
		}
	}
	if ev.Everyone != slices.Contains(before, channel.Everyone) || len(ev.Added)+len(ev.Removed) > 0 {
		text := "成员改为全体"
		if !ev.Everyone {
			text = fmt.Sprintf("成员改为 %d 个", len(after))
		}
		s.tellMembers(c, ch, text, ev)
	}
	s.membersChanged()
	out, err := s.membersOf(c, ch)
	if err != nil {
		s.fault(w, err)
		return
	}
	reply(w, http.StatusOK, out)
}

// setUp makes new session id the channel setup says: its people, its own agent's nick and what
// it receives, the other agents; then first, its first words, said in it. An agent that cannot
// be started leaves the channel without it, and the error says so.
func (s *Server) setUp(ctx context.Context, c caller, id string, setup api.ChannelSetup, first string) error {
	st, err := s.m.State(id)
	if err != nil {
		return err
	}
	if err := s.setNewPeople(c, id, setup.People); err != nil {
		return fmt.Errorf("%w: %w", session.ErrBadRequest, err)
	}
	var failed error
	if setup.Nick != "" || setup.Recv != "" {
		var nick, recv *string
		if setup.Nick != "" {
			nick = &setup.Nick
		}
		if setup.Recv != "" {
			recv = &setup.Recv
		}
		if _, err := s.ch.Change(id, store.AgentMember(id), nick, recv, s.harnessOf); err != nil {
			failed = cmp.Or(failed, err)
		}
	}
	for _, a := range setup.Agents {
		if _, err := s.addAgent(ctx, c, id, st, a); err != nil {
			s.logf("server: channel %s: adding %s: %v", id, a.Agent, err)
			failed = cmp.Or(failed, fmt.Errorf("%s: %w", a.Agent, err))
		}
	}
	s.membersChanged()
	if strings.TrimSpace(first) != "" {
		if _, err := s.sendAs(ctx, c, s.fromOf(c, ""), id, api.Send{Text: first}); err != nil {
			return err
		}
	}
	return failed
}

// choicesTried is how many sessions of a kind are asked for their models, the latest first.
const choicesTried = 30

// agentChoices is what a new agent of kind {agent} may be started with: at ?machine= (none:
// here), or where ?channel= runs. No harness lists its models before it runs, so they are those
// the latest hosted session of the kind there has.
func (s *Server) agentChoices(w http.ResponseWriter, r *http.Request) {
	c := callerOf(r)
	kind, machine := api.Agent(r.PathValue("agent")), r.URL.Query().Get("machine")
	if ch := r.URL.Query().Get("channel"); ch != "" {
		_, st, err := s.seeingChannel(c, ch)
		if err != nil {
			s.error(w, err)
			return
		}
		machine = st.Machine
	}
	out := api.AgentChoices{Models: []api.Model{}, Efforts: []string{}}
	// What one said last, kept (keepChoices), at once (kumo, 2026-11-15: the invite menu's models
	// pop as the chat's own do); the sessions are asked after, for the next time.
	if kept := s.st.Kept(choicesKey(kind, machine)); kept != "" && json.Unmarshal([]byte(kept), &out) == nil && len(out.Models) > 0 {
		out.Presets = s.keptPresets(kind, machine)
		s.nameChoices(&out, kind, machine)
		reply(w, http.StatusOK, out)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_, _ = s.askChoices(ctx, c, kind, machine)
		}()
		return
	}
	got, err := s.askChoices(r.Context(), c, kind, machine)
	if err != nil {
		s.fault(w, err)
		return
	}
	if got != nil {
		out = *got
	}
	out.Presets = s.keptPresets(kind, machine)
	s.nameChoices(&out, kind, machine)
	reply(w, http.StatusOK, out)
}

// nameChoices makes out say which model a new session of kind at machine runs when given none:
// the one its models mark Default (kept ones from before may still list "default": NamedModels);
// an agent that names none: Ran, the model its kind last ran there, a row added for one not
// listed. Effort: the level one given neither ran at.
func (s *Server) nameChoices(out *api.AgentChoices, kind api.Agent, machine string) {
	out.Models = api.NamedModels(out.Models)
	out.Effort = s.st.Kept(session.RanEffortKey(kind, machine))
	if _, ok := api.DefaultModel(out.Models); ok {
		return
	}
	ran := s.st.Kept(session.RanKey(kind, machine))
	if ran == "" {
		return
	}
	for _, m := range out.Models {
		if m.Value == ran || m.Resolved == ran || m.Name == ran {
			out.Ran = m.Value
			return
		}
	}
	out.Models, out.Ran = append([]api.Model{{Value: ran, Name: ran}}, out.Models...), ran
}

// keptPresets are the presets a session of kind at machine listed last (askChoices); nil for
// none, or a kind with none.
func (s *Server) keptPresets(kind api.Agent, machine string) *api.Presets {
	var p api.Presets
	if kept := s.st.Kept("presets:" + string(kind) + ":" + machine); kept == "" || json.Unmarshal([]byte(kept), &p) != nil || len(p.Presets) == 0 {
		return nil
	}
	return &p
}

// askChoices asks the latest hosted sessions of kind at machine that c sees for their models,
// keeping what the first that says them says (keepChoices); nil when none says them.
func (s *Server) askChoices(ctx context.Context, c caller, kind api.Agent, machine string) (*api.AgentChoices, error) {
	list, err := s.m.List(ctx)
	if err != nil {
		return nil, err
	}
	tried := 0
	for _, ss := range list {
		if ss.Agent != kind || !ss.Hosted || ss.Machine != machine || (!c.Host() && !s.sees(s.permsFor(c), ss)) {
			continue
		}
		if tried++; tried > choicesTried {
			break
		}
		// One that says its models (pi says them only while it runs: an idle one has its
		// efforts alone, and is passed over).
		if st, err := s.m.State(ss.ID); err == nil && len(st.Models) > 0 {
			s.keepChoices(st)
			// An agent with presets (dsh): its list kept beside, for the invite menu.
			if info, _ := agents.Get(kind); info.Presets {
				if p, err := s.m.SessionPresets(ctx, ss.ID); err == nil && len(p.Presets) > 0 {
					if b, err := json.Marshal(p); err == nil {
						_ = s.st.Keep("presets:"+string(kind)+":"+machine, string(b))
					}
				}
			}
			return &api.AgentChoices{Models: append([]api.Model{}, st.Models...), Efforts: append([]string{}, st.Efforts...)}, nil
		}
	}
	return nil, nil
}

// knownModels are the models kept for agent a at machine (keepChoices); nil if none.
func (s *Server) knownModels(a api.Agent, machine string) []api.Model {
	var c api.AgentChoices
	if kept := s.st.Kept(choicesKey(a, machine)); kept != "" && json.Unmarshal([]byte(kept), &c) == nil {
		return c.Models
	}
	return nil
}

// sessionName is satori.SessionName of st, its model named from the models kept for its agent
// where st has none (a session not running since the daemon started).
func (s *Server) sessionName(st *api.SessionState) string {
	n := *st
	if len(n.Models) == 0 {
		n.Models = s.knownModels(n.Agent, n.Machine)
	}
	return satori.SessionName(n)
}

func choicesKey(kind api.Agent, machine string) string {
	return "choices:" + string(kind) + ":" + machine
}

// keepChoices keeps the models and efforts session st says its kind has where it runs, if it
// says any and they are not those kept.
func (s *Server) keepChoices(st *api.SessionState) {
	// Without its models (an idle pi says its efforts alone) it would put over what has them.
	if len(st.Models) == 0 {
		return
	}
	b, err := json.Marshal(api.AgentChoices{Models: append([]api.Model{}, st.Models...), Efforts: append([]string{}, st.Efforts...)})
	if key := choicesKey(st.Agent, st.Machine); err == nil && s.st.Kept(key) != string(b) {
		if err := s.st.Keep(key, string(b)); err != nil {
			s.logf("server: keeping the choices of %s: %v", st.Agent, err)
		}
	}
}

// memberRow is member of channel ch as written.
func (s *Server) memberRow(ch, member string) (channel.Member, bool) {
	ms, _ := s.ch.Members(ch)
	for _, m := range ms {
		if m.Member == member {
			return m, true
		}
	}
	return channel.Member{}, false
}

// agentName is what session id's agent runs as now.
func (s *Server) agentName(id string) string {
	st, err := s.m.State(id)
	if err != nil {
		return ""
	}
	return s.sessionName(st)
}

// tellMembers says in channel ch that its members changed: a line of Iridium's own (text, for
// those that only read words) with ev, which c did.
func (s *Server) tellMembers(c caller, ch, text string, ev channel.Event) int64 {
	ev.By = s.satoriUser(c).Name
	// An agent's ir: the agent, by its nick (not the app its session was opened from).
	if c.session != "" {
		if nick := s.ch.Nick(c.session); nick != "" {
			ev.By = nick
		}
	}
	return s.sat.Note(ch, text, c.ID(), &ev)
}

// greet asks session id's agent, just invited into channel ch by c (its line there, seq), to
// say a line there: hello, and who it is (kumo, 2026-11-16: an agent invited says a word, not
// a face that flashes up and goes). A turn of its own, as a message given to it is; not waited
// for.
func (s *Server) greet(c caller, ch, id string, seq int64) {
	by := s.satoriUser(c).Name
	words := fmt.Sprintf("%s 邀请你加入了这个频道。用一两句话跟大家打个招呼，说说你是谁。", cmp.Or(by, "Iridium"))
	lk := s.chain.person(ch, c.ID())
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if _, err := s.deliverTo(ctx, ch, id, seq, words, "", session.From(gate.HostID, ""), false, nil, lk); err != nil {
			s.logf("server: channel %s: greeting from %s: %v", ch, id, err)
		}
	}()
}

// DefaultPeople are a new channel's people when its maker names none (the user, 2026-11-13):
// the panel's logins and the host's own (its terminal, its ir), no Bot's; the one that makes it
// is one too (setPeople).
var DefaultPeople = []string{channel.AppMember(api.WebApp), channel.AppMember(gate.HostID)}

// setPeople makes people channel ch's, c with them (the host is one always).
func (s *Server) setPeople(c caller, ch string, people []string) error {
	if len(people) > 0 && !slices.Contains(people, channel.Everyone) && !c.Host() {
		if me := memberOf(c); !slices.Contains(people, me) && !slices.Contains(people, channel.AppMember(c.App)) {
			people = append(people, me)
		}
	}
	return s.ch.SetPeople(ch, people, s.harnessOf)
}

// setNewPeople makes the people of channel ch, just made by c, people; none named, the
// DefaultPeople and c's app (a Bot that makes one speaks in it, as any of its logins).
func (s *Server) setNewPeople(c caller, ch string, people []string) error {
	if len(people) == 0 {
		people = slices.Clone(DefaultPeople)
		if app := channel.AppMember(c.App); !c.Host() && c.App != "" && !slices.Contains(people, app) {
			people = append(people, app)
		}
	}
	return s.setPeople(c, ch, people)
}

func (s *Server) memberPatch(w http.ResponseWriter, r *http.Request) {
	c := callerOf(r)
	ch, _, err := s.seeingChannel(c, r.PathValue("id"))
	if err != nil {
		s.error(w, err)
		return
	}
	if err := s.inChannel(c, ch); err != nil {
		s.error(w, err)
		return
	}
	var p api.MemberPatch
	if !readJSON(w, r, &p) {
		return
	}
	// An agent's ir changes what the agent itself receives there, nothing else.
	if c.byAgent() && p.Recv != nil && r.PathValue("member") != store.AgentMember(c.session) {
		forbid(w, errors.New("an agent's ir changes what it receives itself (recv), not another's"))
		return
	}
	// A nick is part of the agent's card: any member of a channel it is in may change it
	// (profile.go), and is not told of its own change again.
	// A person or an app that renames it must have the right to use that session, not merely be
	// one of this channel's people.
	if p.Nick != nil && c.session != "" {
		defer s.toldNow(c.session, r.PathValue("member"), "nick")
	} else if p.Nick != nil {
		if id, ok := store.SessionOfMember(r.PathValue("member")); ok {
			if _, err := s.using(c, id); err != nil {
				s.error(w, err)
				return
			}
		}
	}
	was, _ := s.memberRow(ch, r.PathValue("member"))
	now, err := s.ch.Change(ch, r.PathValue("member"), p.Nick, p.Recv, s.harnessOf)
	if err != nil {
		s.memberFault(w, err)
		return
	}
	if was.Nick != "" && now.Nick != was.Nick {
		s.tellMembers(c, ch, fmt.Sprintf("@%s 改名为 @%s", was.Nick, now.Nick), channel.Event{Type: "rename", Member: now.Member, Nick: now.Nick, From: was.Nick})
	}
	if now.Agent() && was.Recv != "" && now.Recv != was.Recv {
		what := map[string]string{channel.RecvAll: "全部", channel.RecvAt: "@我"}[now.Recv]
		s.tellMembers(c, ch, fmt.Sprintf("@%s 接收：%s", now.Nick, what), channel.Event{Type: "recv", Member: now.Member, Nick: now.Nick, Recv: now.Recv})
		s.readsChanged(ch) // the channel's own agent: how far it has read depends on it
	}
	s.membersChanged()
	out, err := s.membersOf(c, ch)
	if err != nil {
		s.fault(w, err)
		return
	}
	reply(w, http.StatusOK, out)
}

// Faces (docs/channels.md "头像"): a channel's; a session's own channel's agent's under the
// same id, and the channel's own beside it (?of=channel, roomKey). A session with none of its own has the one last given to a session of its model.

func channelAvatarKey(id string) string { return "c:" + id }

// modelAvatarKey is where the face of the sessions of st's model is kept; "" while the model is
// not known.
func modelAvatarKey(st *api.SessionState) string {
	if st == nil || st.Bare || st.Agent == "" {
		return ""
	}
	model := cmp.Or(st.Model, st.Pick)
	if model == "" || model == "default" {
		return ""
	}
	return "m:" + string(st.Agent) + ":" + model
}

// avatarOf is the face of channel ch (st its state): its own (an image, or an emoji or a colour
// drawn as one), else its model's.
func (s *Server) avatarOf(ch string, st *api.SessionState) (image []byte, version string, ok bool) {
	return s.face(store.ChannelProfileKey(ch), false, modelAvatarKey(st))
}

// roomKey is the key of channel ch's card as a channel: a session's own channel's apart from
// its agent's (store.RoomProfileKey; kumo, 2026-11-16: the channel's face is not the agent's),
// another channel's the one it always had.
func (s *Server) roomKey(ch string) string {
	if _, own := s.ch.Own(ch); own {
		return store.ChannelProfileKey(ch)
	}
	return store.RoomProfileKey(ch)
}

// channelFace is channel ch's face (bg: its background) as a channel (GET …?of=channel): a
// session's own channel's own, else its agent's, as it was before it had one; one of several
// agents without a face of its own has none: the page draws its agents' (the user, 2026-11-16).
func (s *Server) channelFace(ch string, st *api.SessionState, bg bool) (image []byte, version string, ok bool) {
	if key := s.roomKey(ch); key != store.ChannelProfileKey(ch) {
		if img, v, ok := s.face(key, bg, ""); ok {
			return img, v, true
		}
		if !bg && len(s.ch.Agents(ch)) > 1 {
			return nil, "", false
		}
	}
	if bg {
		return s.face(store.ChannelProfileKey(ch), true, "")
	}
	return s.avatarOf(ch, st)
}

// AgentAvatar is where the face of session id's agent is fetched; "" if it has none.
func (s *Server) AgentAvatar(id string) string {
	st, err := s.m.State(id)
	if err != nil {
		return ""
	}
	if _, v, ok := s.avatarOf(id, st); ok {
		return "/v1/internal/channels/" + id + "/avatar?v=" + v
	}
	return ""
}

func (s *Server) avatarGet(w http.ResponseWriter, r *http.Request) {
	ch, st, err := s.seeingChannel(callerOf(r), r.PathValue("id"))
	if err != nil {
		s.error(w, err)
		return
	}
	img, v, ok := s.avatarOf(ch, st)
	if r.URL.Query().Get("of") == "channel" {
		img, v, ok = s.channelFace(ch, st, false)
	}
	if !ok {
		refuse(w, http.StatusNotFound, "not_found", "no avatar")
		return
	}
	serveFace(w, r, img, v)
}

// avatarPut gives channel {id} a face (the body: a PNG, JPEG or WebP image, or JSON with it in
// base64), or with DELETE
// takes it away. A session's is also kept for its model: the sessions of that model with none
// of their own have it, those started later too. One of the channel's people may; not an agent.
func (s *Server) avatarPut(w http.ResponseWriter, r *http.Request) {
	c := callerOf(r)
	ch, st, err := s.seeingChannel(c, r.PathValue("id"))
	if err != nil {
		s.error(w, err)
		return
	}
	if err := s.inChannel(c, ch); err != nil {
		s.error(w, err)
		return
	}
	if c.byAgent() {
		forbid(w, errors.New("an agent's ir may not change a face"))
		return
	}
	if r.Method == http.MethodDelete {
		if err := s.st.DeleteAvatar(channelAvatarKey(ch)); err != nil {
			s.fault(w, err)
			return
		}
	} else {
		data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 2*gate.MaxAvatarBytes))
		if err != nil {
			refuse(w, http.StatusRequestEntityTooLarge, "avatar_too_large", "at most 2 MB")
			return
		}
		// As JSON: {"avatar": the image in base64, or a data: URL}.
		if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") || bytes.HasPrefix(bytes.TrimSpace(data), []byte("{")) {
			var body struct {
				Avatar string `json:"avatar"`
			}
			if json.Unmarshal(data, &body) != nil {
				refuse(w, http.StatusBadRequest, "bad_request", "avatar: the image in base64")
				return
			}
			b64 := body.Avatar
			if strings.HasPrefix(b64, "data:") {
				_, b64, _ = strings.Cut(b64, ",")
			}
			if data, err = base64.StdEncoding.DecodeString(b64); err != nil {
				refuse(w, http.StatusBadRequest, "bad_request", "avatar: the image in base64")
				return
			}
		}
		img, v, err := gate.MakeAvatar(data)
		switch {
		case errors.Is(err, gate.ErrAvatarTooLarge):
			refuse(w, http.StatusRequestEntityTooLarge, "avatar_too_large", "at most 2 MB")
			return
		case err != nil:
			refuse(w, http.StatusUnprocessableEntity, "avatar_format", "a PNG, JPEG or WebP image")
			return
		}
		for _, key := range []string{channelAvatarKey(ch), modelAvatarKey(st)} {
			if key == "" {
				continue
			}
			if err := s.st.PutAvatar(key, img, v); err != nil {
				s.fault(w, err)
				return
			}
		}
	}
	// An image, or none: not the emoji or the colour it may have had.
	if p := s.st.Profile(store.ChannelProfileKey(ch)); p.Avatar != "" {
		p.Avatar = ""
		if err := s.st.SetProfile(store.ChannelProfileKey(ch), p); err != nil {
			s.fault(w, err)
			return
		}
	}
	// The pages draw the faces again.
	s.hub.Set("avatars", s.avatarsSeq.Add(1))
	reply(w, http.StatusOK, map[string]any{"ok": true})
}

// viewers are the people looking at each channel now: by channel, by member, how many pages of
// theirs have it open.
type viewers struct {
	mu sync.Mutex
	of map[string]map[string]*api.Viewer
}

// viewing: c has channel ch open on a page, until the func it gives is called. The pages are
// told who is looking (the carried state "viewers:<ch>").
func (s *Server) viewing(c caller, ch string) func() {
	me, name := memberOf(c), s.satoriUser(c).Name
	change := func(by int) {
		s.viewers.mu.Lock()
		if s.viewers.of == nil {
			s.viewers.of = map[string]map[string]*api.Viewer{}
		}
		in := s.viewers.of[ch]
		if in == nil {
			in = map[string]*api.Viewer{}
			s.viewers.of[ch] = in
		}
		v := in[me]
		if v == nil {
			v = &api.Viewer{Member: me, Name: name, Since: time.Now().UnixMilli()}
			in[me] = v
		}
		if v.Pages += by; v.Pages <= 0 {
			delete(in, me)
		}
		if len(in) == 0 {
			delete(s.viewers.of, ch)
		}
		s.viewers.mu.Unlock()
		s.hub.Set("viewers:"+ch, s.viewersOf(ch))
	}
	change(1)
	return func() { change(-1) }
}

// viewersOf are the people looking at channel ch now, the first to open it first.
func (s *Server) viewersOf(ch string) []api.Viewer {
	s.viewers.mu.Lock()
	defer s.viewers.mu.Unlock()
	out := []api.Viewer{}
	for _, v := range s.viewers.of[ch] {
		out = append(out, *v)
	}
	slices.SortFunc(out, func(a, b api.Viewer) int {
		return cmp.Or(cmp.Compare(a.Since, b.Since), cmp.Compare(a.Member, b.Member))
	})
	return out
}

// readsOf is how far each member has read channel ch: the people that opened it, as they said
// (readPost); its agents, as far as each was given. The agent of a session that is its alone
// has all of it, whatever it is set to receive (everything said there is sent to it). Among
// several, the channel's own agent has what it was given and what it said itself: what named
// another was not given to it, though it receives all that names nobody. Not the host's login:
// it is Iridium itself on the page, no reader among the people (the owner, 2026-11-15).
func (s *Server) readsOf(ch string) api.ChannelReads {
	out := api.ChannelReads{Channel: ch, End: s.ch.End(ch), Readers: []api.Reader{}}
	rows, err := s.st.ChannelReads(ch)
	if err != nil {
		s.logf("server: channel %s: who read it: %v", ch, err)
	}
	for _, r := range rows {
		if r.Reader == gate.HostID || strings.HasPrefix(r.Reader, gate.HostID+"/") {
			continue
		}
		out.Readers = append(out.Readers, api.Reader{Member: r.Reader, Name: r.Name, Seq: min(r.Seq, out.End), At: r.At})
	}
	ms, _ := s.ch.Members(ch)
	multi := s.multi(ch)
	for _, m := range ms {
		if !m.Agent() {
			continue
		}
		rd := api.Reader{Member: m.Member, Nick: m.Nick, Seq: m.Seen}
		if m.Direct() {
			if multi {
				rd.Seq = min(max(m.Seen, s.st.ChannelLastBy(ch, m.Member)), out.End)
			} else {
				rd.Seq = out.End
			}
		}
		if st, err := s.m.State(m.Session); err == nil {
			rd.Agent, rd.Name = st.Agent, s.sessionName(st)
		}
		out.Readers = append(out.Readers, rd)
	}
	return out
}

// seen: session's agent was given channel ch up to message seq; the pages are told.
func (s *Server) seen(ch, session string, seq int64) {
	s.ch.Seen(ch, session, seq)
	s.readsChanged(ch)
}

// readsChanged tells the pages how far channel ch is read now (the carried state "reads:<ch>").
func (s *Server) readsChanged(ch string) { s.hub.Set("reads:"+ch, s.readsOf(ch)) }

func (s *Server) readsGet(w http.ResponseWriter, r *http.Request) {
	ch, _, err := s.seeingChannel(callerOf(r), r.PathValue("id"))
	if err != nil {
		s.error(w, err)
		return
	}
	reply(w, http.StatusOK, s.readsOf(ch))
}

// readPost: the person who calls has read the channel up to a message. A login's own; an
// agent's place is what it was given.
func (s *Server) readPost(w http.ResponseWriter, r *http.Request) {
	c := callerOf(r)
	ch, _, err := s.seeingChannel(c, r.PathValue("id"))
	if err != nil {
		s.error(w, err)
		return
	}
	if c.byAgent() {
		forbid(w, errors.New("an agent has read what it was given: its place is not set by hand"))
		return
	}
	var req api.Read
	if !readJSON(w, r, &req) {
		return
	}
	seq := min(req.Seq, s.ch.End(ch))
	if seq < 1 {
		refuse(w, http.StatusBadRequest, "bad_request", "seq: the number of a message of the channel")
		return
	}
	moved, err := s.st.SetChannelRead(ch, memberOf(c), s.satoriUser(c).Name, seq)
	if err != nil {
		s.fault(w, err)
		return
	}
	if moved {
		s.readsChanged(ch)
	}
	s.readTo(ch, readerOf(c), seq, seq >= s.ch.End(ch)) // what named it up to there (ats.go)
	reply(w, http.StatusOK, s.readsOf(ch))
}

// Follows: how many messages one may ask for, and for how long (seconds).
const (
	FollowCount   = 50
	FollowSeconds = 3600
)

// followAdd: the agent that calls asks to be given the next messages of a user in the channel
// (docs/channels.md "收"), named or not: Count of them (1), within Seconds (600).
func (s *Server) followAdd(w http.ResponseWriter, r *http.Request) {
	c := callerOf(r)
	ch, _, err := s.seeingChannel(c, r.PathValue("id"))
	if err != nil {
		s.error(w, err)
		return
	}
	if !s.ch.Speaks(ch, c.session) {
		forbid(w, errors.New("only an agent of the channel follows someone in it, with its ir"))
		return
	}
	var req api.Follow
	if !readJSON(w, r, &req) {
		return
	}
	user := strings.TrimPrefix(strings.TrimSpace(req.User), "@")
	if user == "" {
		refuse(w, http.StatusBadRequest, "bad_request", "user: a nick, a user's id, or * for anyone")
		return
	}
	// An agent by its nick.
	if _, by := s.agentsOf(ch); user != "*" {
		for id, m := range by {
			if strings.EqualFold(m.Nick, user) {
				user = store.AgentMember(id)
			}
		}
	}
	count, secs := cmp.Or(req.Count, 1), cmp.Or(req.Seconds, 600)
	if count < 1 || count > FollowCount || secs < 1 || secs > FollowSeconds {
		refuse(w, http.StatusBadRequest, "bad_request", fmt.Sprintf("count: 1 to %d; seconds: 1 to %d", FollowCount, FollowSeconds))
		return
	}
	s.follows.add(ch, follow{session: c.session, user: user, left: count, until: time.Now().Add(time.Duration(secs) * time.Second)})
	reply(w, http.StatusOK, api.Follow{User: user, Count: count, Seconds: secs})
}

// memberRemove takes a member out; an agent's session stays, as any session.
func (s *Server) memberRemove(w http.ResponseWriter, r *http.Request) {
	c := callerOf(r)
	ch, _, err := s.seeingChannel(c, r.PathValue("id"))
	if err != nil {
		s.error(w, err)
		return
	}
	if err := s.inChannel(c, ch); err != nil {
		s.error(w, err)
		return
	}
	if c.byAgent() {
		forbid(w, errors.New("an agent's ir may not change a channel's members"))
		return
	}
	member := r.PathValue("member")
	// Agents alone: the people are set whole (PUT …/people), which keeps whoever sets them in.
	if _, ok := store.SessionOfMember(member); !ok {
		refuse(w, http.StatusBadRequest, "bad_request", "member: an agent (agent:<session>); the people are set with PUT …/people")
		return
	}
	was, _ := s.memberRow(ch, member)
	if err := s.ch.Leave(ch, member); err != nil {
		s.memberFault(w, err)
		return
	}
	if was.Agent() {
		s.tellMembers(c, ch, fmt.Sprintf("@%s 离开了频道", was.Nick), channel.Event{Type: "leave", Member: member, Nick: was.Nick})
	}
	out, err := s.membersOf(c, ch)
	if err != nil {
		s.fault(w, err)
		return
	}
	refresh := []string{ch}
	if id, ok := store.SessionOfMember(member); ok {
		refresh = append(refresh, id)
	}
	s.m.Refresh(refresh)
	s.membersChanged()
	reply(w, http.StatusOK, out)
}

func (s *Server) injections(w http.ResponseWriter, r *http.Request) {
	rows, err := s.st.Injections(r.PathValue("id"), 50)
	if err != nil {
		s.fault(w, err)
		return
	}
	out := []api.Injection{}
	for _, in := range rows {
		out = append(out, api.Injection{ID: in.ID, Channel: in.Channel, Seq: in.Seq, Input: in.Input, At: in.At, Record: json.RawMessage(in.Record)})
	}
	reply(w, http.StatusOK, out)
}

// ---- where a message goes ----

// agentsOf are channel ch's agents as the rules see them (those that count: one whose session
// is in the archive is given nothing), and every agent's member row.
func (s *Server) agentsOf(ch string) ([]deliver.Agent, map[string]channel.Member) {
	ms, _ := s.ch.Members(ch)
	var out []deliver.Agent
	by := map[string]channel.Member{}
	for _, m := range ms {
		if m.Agent() {
			by[m.Session] = m
			if s.counts(ch, m.Session) {
				out = append(out, deliver.Agent{Session: m.Session, Nick: m.Nick, All: m.Recv == channel.RecvAll})
			}
		}
	}
	return out, by
}

// sendChannel says req in channel ch (several agents) as c and gives it to the agents it wakes.
// A person needs the right to use each agent the message names (Claude: the owner's own); one
// that would merely receive everything is passed over without it.
func (s *Server) sendChannel(ctx context.Context, c caller, from, ch string, req api.Send) (api.SendReply, error) {
	return s.sendChannelAs(ctx, c, from, ch, req, false)
}

// BehindMax is how many messages an agent may not have seen of a channel and still speak in it
// with ir; with more, or with one that names it, it is told to read them first.
const BehindMax = 12

// behindError refuses an agent's ir: what it has not seen of the channel, as it is told.
type behindError struct{ text string }

func (e *behindError) Error() string { return e.text }

// kin: one of sessions a and b started the other for channel ch (Spawned). What they say to
// each other there counts apart from the agents that may pass a message on (MaxKinHops); in any
// other channel they are as any two agents.
func (s *Server) kin(ch, a, b string) bool {
	if sp := s.spawnedOf(a); sp != nil && sp.By == b && sp.Channel == ch {
		return true
	}
	sp := s.spawnedOf(b)
	return sp != nil && sp.By == a && sp.Channel == ch
}

// follow: session's agent asked to be given the next left messages of user in a channel, until
// a time.
type follow struct {
	session, user string
	left          int
	until         time.Time
}

// follows are what the agents of each channel asked to be given beside what names them.
type follows struct {
	mu sync.Mutex
	of map[string][]follow
}

func (f *follows) add(ch string, x follow) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.of == nil {
		f.of = map[string][]follow{}
	}
	// One per agent and user: the new one in place of the old.
	f.of[ch] = append(slices.DeleteFunc(f.of[ch], func(o follow) bool { return o.session == x.session && o.user == x.user }), x)
}

// take are the sessions that asked for a message of author in ch (not the one that said it);
// count: the message is said, each is counted one message less.
func (f *follows) take(ch, author, sender string, count bool) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now()
	var out []string
	kept := f.of[ch][:0]
	for _, x := range f.of[ch] {
		if now.After(x.until) {
			continue
		}
		if x.session != sender && (x.user == "*" || x.user == author) {
			out = append(out, x.session)
			if count {
				x.left--
			}
		}
		if x.left > 0 {
			kept = append(kept, x)
		}
	}
	if len(kept) == 0 {
		delete(f.of, ch)
	} else {
		f.of[ch] = kept
	}
	return out
}

// wakes are the agents of ch a message with words text wakes: those the rules give it to
// (deliver.Route) and those that asked for its author's (follows; said: the message is said
// already, so it counts against what they asked for); sender, the agent that said it ("": a
// person), whose turn stands at l in the channel's run. After too many agents in a row only
// its kin get it, and after too many passes between kin not they either; limit: it names
// someone and nobody gets it, for the limit reached (0: none was).
func (s *Server) wakes(ch, text, author, sender string, l link, list []deliver.Agent, said bool) (wake, named []string, limit int) {
	// <at> written in the words calls as "@nick" does (Markdown and Satori's elements mixed).
	text = satori.AtWords(text, s.ch.Nick)
	d := deliver.Route(text, sender, list)
	wake = d.Wake
	for _, t := range s.follows.take(ch, author, sender, said) {
		if !slices.Contains(wake, t) && s.ch.Speaks(ch, t) {
			wake = append(wake, t)
		}
	}
	if sender == "" {
		return wake, d.Named, 0
	}
	wake = slices.DeleteFunc(wake, func(t string) bool {
		over := l.hop >= deliver.MaxHops
		if s.kin(ch, sender, t) {
			over = l.kin >= MaxKinHops
			if over {
				limit = max(limit, MaxKinHops)
			}
		} else if over {
			limit = max(limit, deliver.MaxHops)
		}
		return over
	})
	if len(d.Named) == 0 || len(wake) > 0 {
		limit = 0
	}
	return wake, d.Named, limit
}

// reach is the link of sender's message (its turn at l) as it reaches to: one more agent passed
// it on, or one more pass between kin.
func (s *Server) reach(ch, sender, to string, l link) link {
	switch {
	case sender == "":
	case s.kin(ch, sender, to):
		l.kin++
	default:
		l.hop++
	}
	return l
}

// errTooFar refuses an agent's ir: limit agents in a row passed the message on.
func errTooFar(limit int) error {
	return fmt.Errorf("%w: more than %d agents in a row passed this on; a person goes on from here", session.ErrBadRequest, limit)
}

// stopped says in channel ch that message seq was not passed on: limit agents in a row had.
func (s *Server) stopped(ch string, seq int64, limit int, origin string) {
	s.sat.Note(ch, fmt.Sprintf("未转交 #%d：Agent 已连续转交 %d 次", seq, limit), origin, &channel.Event{Type: "stopped", Seq: seq})
}

// sendChannelAs is sendChannel. relay: the words are an agent's reply, said for it as its turn
// ended (closeCalls), not sent by it with ir: said whatever comes of passing them on (a line of
// the channel says whom they did not reach, as for what an agent says in its own channel), and
// the agent is neither held to what it has seen of the channel (docs/channels.md "发送检查")
// nor given it.
func (s *Server) sendChannelAs(ctx context.Context, c caller, from, ch string, req api.Send, relay bool) (api.SendReply, error) {
	st, err := s.m.State(ch)
	if err != nil {
		return api.SendReply{}, err
	}
	if !s.seenBy(c, st.Session) {
		return api.SendReply{}, errNoSession
	}
	if err := s.inChannel(c, ch); err != nil {
		return api.SendReply{}, err
	}
	text, images, err := s.files.Message(ch, req.Text, req.Attachments)
	if err != nil {
		return api.SendReply{}, fileErr{err}
	}
	list, _ := s.agentsOf(ch)
	// An agent of the channel speaking through ir: the run goes on, and it wakes only whom it names.
	sender, lk := "", link{origin: c.ID()}
	if s.ch.Speaks(ch, c.session) {
		sender, lk = c.session, s.chain.of(ch, c.session)
		lk.origin = cmp.Or(lk.origin, c.ID())
	}
	u := s.satoriUser(c)
	if tag := s.m.SourceTag(from); tag != "" {
		u.Name = tag
	}
	unseen := ""
	var given int64 // how far the agent that speaks has been given the channel, once this is sent
	if sender != "" {
		u = satori.User{ID: store.AgentMember(sender)}
		_, by := s.agentsOf(ch)
		me := by[sender]
		given = me.Seen
		if !relay {
			// What it has not seen is given to it now: with the refusal, or with the answer once
			// the message is sent. Its place moves as far as what it is given, no further.
			msgs, did, _, named, upTo, err := s.unread(ch, me, 0, by)
			if err != nil {
				return api.SendReply{}, err
			}
			cc := assemble.Channel{Plan: strategy.Fold(msgs, strategy.Default), Zone: time.Local, Did: did}
			if !me.Direct() {
				cc.ID = ch
			}
			if named || len(msgs) > BehindMax {
				in, err := assemble.ChannelBehind(cc, len(msgs), named)
				if err != nil {
					return api.SendReply{}, err
				}
				s.irGiven(sender, ch, in)
				s.seen(ch, sender, upTo)
				return api.SendReply{}, &behindError{in.String()}
			}
			if in, err := assemble.ChannelUnseen(cc); err == nil {
				unseen = in.String()
				if unseen != "" {
					s.irGiven(sender, ch, in)
				}
			}
			given = upTo
		}
	}
	// Answering a message (<quote>): its agent is called as if named (docs/channels.md "叫起谁").
	route := req.Text
	var quote int64
	if req.Quote > 0 {
		q, ok := s.ch.Get(ch, req.Quote)
		if !ok {
			return api.SendReply{}, fmt.Errorf("%w: quote: no message %d in the channel", session.ErrBadRequest, req.Quote)
		}
		quote = q.Seq
		if id, ok := store.SessionOfMember(q.Author); ok && id != sender {
			if m, ok := by0(list, id); ok {
				route += "\n@" + m.Nick
			}
		}
	}
	routed, namedNicks, limit := s.wakes(ch, route, u.ID, sender, lk, list, false)
	if limit > 0 && !relay {
		return api.SendReply{}, errTooFar(limit)
	}
	var wake []string
	for _, t := range routed {
		if _, err := s.using(c, t); err != nil && sender == "" {
			if slices.ContainsFunc(list, func(a deliver.Agent) bool { return a.Session == t && slices.Contains(namedNicks, a.Nick) }) {
				return api.SendReply{}, err
			}
			continue
		}
		wake = append(wake, t)
	}
	if req.ID == "" {
		req.ID = session.NewInputID()
	}
	// An agent's words are an agent's (kind "said"), marked as sent; a reply said for it is its
	// turn's last.
	of := channel.Data{Quote: quote, Sent: sender != "", Final: sender != "" && relay}
	// Its files, as the page draws them (the words given to the agents name them already).
	of.Files = s.files.Refs(ch, req.Attachments)
	if sender != "" && sender != ch {
		// Where it is from: the stretch of the agent's own record since it was called here.
		of.Session = sender
		if sst, err := s.m.State(sender); err == nil {
			of.To = sst.End
		}
		of.From = s.calls.spoke(sender, ch, of.To)
	}
	of.Device = s.deviceOfCaller(c)
	seq := s.sat.Input(st.Session, req.ID, req.Text, u, lk.origin, of)
	if seq == 0 {
		return api.SendReply{}, errors.New("the channel did not take the message")
	}
	// The logins it names, or whose message it answers, have a mark (ats.go).
	author := ""
	if sender == "" {
		author = readerOf(c)
	}
	named := s.names(ch, seq, req.Text, quote, author)
	// Said: it counts against what was asked for of its author, and a person's begins a new run.
	s.follows.take(ch, u.ID, sender, true)
	if sender == "" {
		lk = s.chain.person(ch, c.ID())
	} else {
		// An agent's words reach the others under its nick, as routeSaid gives them, not its
		// ir's tag (a session's id: kumo, 2026-11-16).
		from = session.From(gate.HostID, "@"+s.ch.Nick(sender))
	}
	out := api.SendReply{Input: req.ID, Seq: seq, Said: true, Unseen: unseen}
	var firstErr error
	var failed []string
	sent := 0
	for _, t := range wake {
		queued, err := s.deliverTo(ctx, ch, t, seq, text, req.ID, from, req.Steer, images, s.reach(ch, sender, t, lk))
		if err != nil {
			s.logf("server: channel %s: message %d to %s: %v", ch, seq, t, err)
			firstErr = cmp.Or(firstErr, err)
			failed = append(failed, t)
			continue
		}
		// The turn it begins is the sender's doing: the woken agent's ir acts as that login, the
		// channel's own agent's only when it is the one woken (origin.go).
		s.noteOrigin(c, t, c.session)
		sent++
		out.Queued = out.Queued || queued
	}
	if sent == 0 && firstErr != nil && !relay {
		s.ch.Unsay(ch, seq)
		s.unnames(ch, seq, named)
		return api.SendReply{}, firstErr
	}
	if sender != "" {
		// Sent: it has what came back with the answer, and its own words where they follow.
		if seq == given+1 {
			given = seq
		}
		s.seen(ch, sender, given)
	}
	if limit > 0 {
		s.stopped(ch, seq, limit, lk.origin)
	}
	// Some got it, some not: said in the channel, where everyone sees whom it did not reach.
	_, by := s.agentsOf(ch)
	for _, t := range failed {
		s.sat.Note(ch, fmt.Sprintf("未送达 #%d → @%s", seq, by[t].Nick), lk.origin, &channel.Event{Type: "undelivered", Seq: seq, Nick: by[t].Nick})
	}
	return out, nil
}

// channelName is what channel ch goes by: its name, else its title.
func (s *Server) channelName(ch string) string {
	st, err := s.m.State(ch)
	if err != nil {
		return ""
	}
	return s.MaskText(cmp.Or(st.Name, st.Title))
}

// quoted is text as Markdown quoted lines (the first 400 characters), then a blank line.
func quoted(text string) string {
	if r := []rune(strings.TrimSpace(text)); len(r) > 400 {
		text = string(r[:400]) + "…"
	}
	var b strings.Builder
	for _, l := range strings.Split(strings.TrimSpace(text), "\n") {
		b.WriteString("> " + l + "\n")
	}
	return b.String() + "\n"
}

// lastInjection is the record of the last message channel ch gave session's agent, if it is
// kept (not what its ir was answered with there).
func (s *Server) lastInjection(session, ch string) (assemble.Record, bool) {
	rows, err := s.st.Injections(session, 0)
	if err != nil {
		return assemble.Record{}, false
	}
	for _, in := range rows {
		if in.Channel != ch {
			continue
		}
		var r assemble.Record
		if json.Unmarshal([]byte(in.Record), &r) != nil {
			return assemble.Record{}, false
		}
		if r.Kind != assemble.KindTurnPrefix {
			continue
		}
		return r, true
	}
	return assemble.Record{}, false
}

// irGiven keeps what session's ir was answered with in channel ch (what was said there since it
// last read it, a send refused): it is in the agent's context as much as a message is.
func (s *Server) irGiven(session, ch string, in assemble.Injection) {
	b, err := json.Marshal(in.Record())
	if err != nil {
		return
	}
	if err := s.st.AddInjection(store.Injection{Session: session, Channel: ch, Record: string(b)}); err != nil {
		s.logf("server: channel %s: keeping what %s's ir was answered: %v", ch, session, err)
	}
}

// compactedSince: session's agent compacted its context after line of its record.
func (s *Server) compactedSince(session string, line int64) bool {
	for at := line; ; {
		p, err := s.m.Messages(session, -1, at)
		if err != nil {
			return true // not known: as if it had
		}
		for _, b := range p.Blocks {
			if b.Kind == api.BlockKindSystem && b.Event == api.SystemEventCompact {
				return true
			}
		}
		if len(p.Blocks) == 0 || p.End <= at {
			return false
		}
		at = p.End
	}
}

// by0 is the agent of session id among list.
func by0(list []deliver.Agent, id string) (deliver.Agent, bool) {
	for _, a := range list {
		if a.Session == id {
			return a, true
		}
	}
	return deliver.Agent{}, false
}

// whoOf is the name a message's author goes by in the channel: an agent's nick, a person's tag.
func whoOf(m channel.Message, by map[string]channel.Member) string {
	if id, ok := store.SessionOfMember(m.Author); ok {
		return cmp.Or(by[id].Nick, "agent")
	}
	return cmp.Or(m.Name, m.Author)
}

// deliverTo gives message seq of channel ch (its words said, as the agent gets them) to the
// agent of session to: under its number, with what the channel said since the agent last read
// it. from: who it is from, as Send takes it; lk: where the turn it begins stands in the
// channel's run.
func (s *Server) deliverTo(ctx context.Context, ch, to string, seq int64, said, input, from string, steer bool, images []agent.Image, lk link) (bool, error) {
	// One at a time for an agent: each is given what it has not seen, once.
	defer s.chain.lock(to)()
	list, by := s.agentsOf(ch)
	me, ok := by[to]
	if !ok {
		return false, channel.ErrNoMember
	}
	unseen, did, work, _, _, err := s.unread(ch, me, seq, by)
	if err != nil {
		return false, err
	}
	cc := assemble.Channel{You: me.Nick, Plan: strategy.Fold(unseen, strategy.Default), Pull: true, Zone: time.Local, Did: did}
	if !me.Direct() {
		// In it by a subscription: told which channel this is of and that its reply is said there
		// for it as its turn ends; with ir, also how to say something there before that.
		cc.ID, cc.Name, cc.Pull = ch, s.channelName(ch), s.m.HasIR(to)
	}
	for _, a := range list {
		if a.Session == to {
			continue
		}
		// And what it has done since it last spoke.
		sum := s.ch.Doing(a.Session)
		sum.First = 0
		if w := work[a.Session]; w != nil {
			w.Sum(sum)
			sum = *w
		}
		// At another machine: its files are there.
		if st, err := s.m.State(a.Session); err == nil && st.Machine != "" {
			place := st.Machine
			if info, ok := s.gate().Endpoint(st.Machine); ok && info.Name != "" {
				place = info.Name
			}
			sum = sum.At(place)
		}
		cc.Work = append(cc.Work, assemble.Work{Who: a.Nick, Did: sum})
	}
	if st, err := s.m.State(to); err == nil && st.Machine != "" && !s.m.HasIR(to) {
		cc.Pull = false // at another machine, with no ir to read the channel with
	}
	// Still under the name Iridium gave it: it names itself first, where it has ir to.
	cc.Rename = channel.IsDefaultNick(me.Nick) && s.m.HasIR(to)
	var keys []string
	for _, a := range list {
		ca := assemble.ChannelAgent{Nick: a.Nick}
		if st, err := s.m.State(a.Session); err == nil {
			ca.Name = s.sessionName(st)
		}
		ca.Intro = head(s.st.Profile(store.ChannelProfileKey(a.Session)).Intro, 80)
		cc.Agents = append(cc.Agents, ca)
		keys = append(keys, a.Session)
	}
	slices.Sort(keys)
	cc.Key, cc.People = strings.Join(keys, ","), s.peopleOf(ch)
	for _, pc := range s.profileChanges(ch, to) {
		cc.Changes = append(cc.Changes, assemble.ProfileChange{Who: pc.Who, What: pc.What, To: pc.To})
	}
	// The message it answers, if it answers one: a line of it before its words.
	if m, ok := s.ch.Get(ch, seq); ok && m.D.Quote > 0 {
		if q, ok := s.ch.Get(ch, m.D.Quote); ok {
			cc.Quote = &assemble.Quote{Seq: q.Seq, Who: whoOf(q, by), Text: q.Text}
		}
	}
	var before int64
	if st, err := s.m.State(to); err == nil {
		before = st.End
	}
	// The standing words again only when they changed, were never given here, or may be gone
	// from the agent's context since (a compaction): docs/channels.md "增量".
	static := assemble.ChannelStaticKey(cc)
	if last, ok := s.lastInjection(to, ch); ok && last.Static == static && !s.compactedSince(to, last.Line) {
		cc.Brief = true
	}
	in, err := assemble.ChannelTurn(cc, seq, said, input)
	if err != nil {
		return false, err
	}
	id := session.NewInputID()
	rec := in.Record()
	rec.Static, rec.Full, rec.Line = static, !cc.Brief, before
	if rec, err := json.Marshal(rec); err == nil {
		if err := s.st.AddInjection(store.Injection{Session: to, Channel: ch, Input: id, Seq: seq, Record: string(rec)}); err != nil {
			s.logf("server: channel %s: keeping what %s was given: %v", ch, to, err)
		}
	}
	_, queued, err := s.m.SendAs(ctx, to, id, in.String(), from, steer, images...)
	if err != nil {
		// Not given: not kept as given, or the next time would take the standing words as had.
		if err := s.st.RemoveInjection(to, id); err != nil {
			s.logf("server: channel %s: forgetting what %s was not given: %v", ch, to, err)
		}
		return false, err
	}
	s.seen(ch, to, seq)
	if !me.Direct() {
		s.calls.add(to, called{Channel: ch, Seq: seq, From: before, Input: id})
	}
	s.chain.pass(ch, to, lk)
	return queued, nil
}

// unread are the messages of ch that me has not seen, below number before (0: all there are),
// others' only: as the plan of what to give takes them; did, what an agent did before saying
// one, by its number; work, by agent session, all it did over them; named: one of them names
// me; upTo: the number of the last one looked at (me's place, if none): how far me has the
// channel once given these.
func (s *Server) unread(ch string, me channel.Member, before int64, by map[string]channel.Member) (msgs []strategy.Msg, did map[int64]activity.Activity, work map[string]*activity.Activity, named bool, upTo int64, err error) {
	rows, err := s.ch.Between(ch, me.Seen, before, 2000)
	if err != nil {
		return nil, nil, nil, false, 0, err
	}
	did, work, upTo = map[int64]activity.Activity{}, map[string]*activity.Activity{}, me.Seen
	for _, m := range rows {
		upTo = max(upTo, m.Seq)
		if m.Author == me.Member || (m.Kind != channel.KindUser && m.Kind != channel.KindSaid) {
			continue
		}
		id, agent := store.SessionOfMember(m.Author)
		msgs = append(msgs, strategy.Msg{Seq: m.Seq, Who: whoOf(m, by), Agent: agent, Text: fileNames(m.Text, m.D.Files), At: m.At})
		if me.Nick != "" && len(deliver.Mentions(m.Text, []string{me.Nick})) > 0 {
			named = true
		}
		if a := m.D.Activity; agent && a != nil {
			did[m.Seq] = *a
			if work[id] == nil {
				work[id] = &activity.Activity{}
			}
			work[id].Sum(*a)
		}
	}
	return msgs, did, work, named, upTo, nil
}

// fileLine is a line that names a file by its path ("[附件] `path`", an agent's "[文件] `path`").
var fileLine = regexp.MustCompile("^\\[(?:附件|文件|file|attachment)\\] `([^`]+)`$")

// fileNames is text, said earlier, as an agent is given it among what it has not read (kumo,
// 2026-11-16): its files by their names alone, not their paths, so that one it was not
// called by does not go and read them; message.get has them whole. The message that calls an
// agent keeps them (deliverTo's said).
func fileNames(text string, files []api.FileRef) string {
	var names []string
	var kept []string
	for _, l := range strings.Split(text, "\n") {
		if m := fileLine.FindStringSubmatch(strings.TrimSpace(l)); m != nil {
			names = append(names, filepath.Base(m[1]))
			continue
		}
		kept = append(kept, l)
	}
	for _, f := range files {
		if n := cmp.Or(f.Name, filepath.Base(f.Path)); n != "" && !slices.Contains(names, n) {
			names = append(names, n)
		}
	}
	out := strings.TrimSpace(strings.Join(kept, "\n"))
	if len(names) == 0 {
		return out
	}
	return strings.TrimSpace(out + "\n（附件：" + strings.Join(names, "、") + "）")
}

// routeSaid: an agent said m in its channel. In a channel of several, the agents it names are
// given it; after too many agents in a row, nobody is, and the channel says so.
func (s *Server) routeSaid(m channel.Message) {
	sender, ok := store.SessionOfMember(m.Author)
	// What it sent with ir (or was said for it) was given to whom it names as it was sent.
	if !ok || m.Kind != channel.KindSaid || m.D.Sent {
		return
	}
	// The logins it names (ats.go), once it is on its way to the agents it names: what is said
	// after it is told after this returns.
	defer s.names(m.Channel, m.Seq, m.Text, 0, "")
	list, by := s.agentsOf(m.Channel)
	if len(list) < 2 {
		return
	}
	lk := s.chain.of(m.Channel, sender)
	wake, _, limit := s.wakes(m.Channel, m.Text, m.Author, sender, lk, list, true)
	if limit > 0 {
		s.stopped(m.Channel, m.Seq, limit, lk.origin)
		return
	}
	if len(wake) == 0 {
		return
	}
	from := session.From(gate.HostID, "@"+by[sender].Nick)
	go func() {
		for _, t := range wake {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			_, err := s.deliverTo(ctx, m.Channel, t, m.Seq, m.Text, "", from, true, nil, s.reach(m.Channel, sender, t, lk))
			cancel()
			if err != nil {
				s.logf("server: channel %s: message %d to %s: %v", m.Channel, m.Seq, t, err)
				s.sat.Note(m.Channel, fmt.Sprintf("未送达 #%d → @%s：%s", m.Seq, by[t].Nick, strings.TrimSpace(err.Error())), lk.origin, &channel.Event{Type: "undelivered", Seq: m.Seq, Nick: by[t].Nick})
				continue
			}
			// Its turn goes on for whom the sender's does.
			s.noteOrigin(caller{Identity: localIdentity, socket: true, agent: true, session: sender}, t, sender)
		}
	}()
}

// relayHead is how a channel's message given to an agent begins ("#12: …").
var relayHead = regexp.MustCompile(`^#(\d+): `)

// relayOf is what block b of session's record was given as by a channel (satori.Relayer): the
// messages its injection record cites, in order, the one that called it last. The injection is
// the one kept under the block's input (which channel it is of is read there, not guessed from
// the words); a block that names no input, the latest one for that number. None for a block
// that is no such message or whose record is no longer kept.
func (s *Server) relayOf(session string, b api.Block) (satori.Relay, bool) {
	m := relayHead.FindStringSubmatch(b.Text)
	ctxAt := strings.Index(b.Text, "\n\n<channel-context>\n")
	if m == nil || ctxAt < 0 {
		return satori.Relay{}, false
	}
	seq, _ := strconv.ParseInt(m[1], 10, 64)
	rows, err := s.st.Injections(session, 0)
	if err != nil {
		return satori.Relay{}, false
	}
	var rec assemble.Record
	ch, found := "", false
	for _, in := range rows {
		if in.Seq != seq || (b.ID != "" && in.Input != b.ID) {
			continue
		}
		if json.Unmarshal([]byte(in.Record), &rec) == nil {
			ch, found = in.Channel, true
			break
		}
	}
	if !found {
		return satori.Relay{}, false
	}
	_, by := s.agentsOf(ch)
	msg := func(n int64) (satori.RelayMsg, bool) {
		cm, ok := s.ch.Get(ch, n)
		if !ok {
			return satori.RelayMsg{}, false
		}
		u := satori.User{ID: cm.Author, Name: whoOf(cm, by)}
		return satori.RelayMsg{ID: strconv.FormatInt(n, 10), User: u, Text: cm.Text, Activity: cm.D.Activity}, true
	}
	out := satori.Relay{Channel: ch, Name: s.channelName(ch), Seq: seq, Full: rec.Full}
	full := rec.Full || rec.Static == "" // a record from before: its words were all given
	open := false
	for _, p := range rec.Parts {
		switch {
		case p.Source == "prompts:channel.context.open":
			open = true
		case !open:
		case p.Source == "prompts:channel.context.guest" || p.Source == "prompts:channel.context.members" || p.Source == "prompts:channel.context.guest-bare":
			out.Full = full
		case p.Source == "prompts:channel.context.turn":
			out.Msgs = append(out.Msgs, satori.RelayMsg{ID: p.Values["from"] + "-" + p.Values["to"], User: satori.User{Name: p.Values["who"]}, Text: p.Values["head"], Turn: true})
		case p.Source == "input":
			for _, r := range p.Refs {
				if r.Kind != "message" {
					continue
				}
				if n, err := strconv.ParseInt(r.ID, 10, 64); err == nil && n != seq {
					if rm, ok := msg(n); ok {
						out.Msgs = append(out.Msgs, rm)
					}
				}
			}
		}
	}
	if rm, ok := msg(seq); ok {
		out.Msgs = append(out.Msgs, rm)
	}
	if out.Full {
		out.Context = strings.TrimSuffix(strings.TrimSpace(b.Text[ctxAt+len("\n\n<channel-context>\n"):]), "</channel-context>")
	}
	return out, true
}

// dms are the channels that are two people's ("dm:<the two>" kept in the store names each).
type dms struct {
	making sync.Mutex // one is looked up and made at a time (dmPost)
	mu     sync.Mutex
	of     map[string]bool
	read   bool
}

// mark: channel id is (on) or is not two people's.
func (s *Server) markDM(id string, on bool) {
	s.dms.mu.Lock()
	defer s.dms.mu.Unlock()
	s.dms.load(s.st)
	if on {
		s.dms.of[id] = true
	} else {
		delete(s.dms.of, id)
	}
}

// load reads them from the store, once; with d.mu held.
func (d *dms) load(st *store.Store) {
	if d.read || st == nil {
		return
	}
	d.read, d.of = true, map[string]bool{}
	if kept, err := st.KeptUnder("dm:"); err == nil {
		for _, id := range kept {
			if id != "" {
				d.of[id] = true
			}
		}
	}
}

// any: there is one at all.
func (d *dms) any() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.of) > 0
}

// isDM: channel id is two people's.
func (s *Server) isDM(id string) bool {
	s.dms.mu.Lock()
	defer s.dms.mu.Unlock()
	s.dms.load(s.st)
	return s.dms.of[id]
}

// dmPost opens the channel of two people (POST /v1/internal/dm {user}): the caller and the login
// whose Satori user id user is (the host's: "local"). The same two always have the same one,
// made at first with no agent, its people the two, in the home folder (docs/channels.md "私聊");
// only the two see it, whatever folders they may see.
func (s *Server) dmPost(w http.ResponseWriter, r *http.Request) {
	c := callerOf(r)
	var req struct {
		User string `json:"user"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.User == "" {
		refuse(w, http.StatusBadRequest, "bad_request", "user: whom to talk to, by their user id")
		return
	}
	them, name := "", ""
	if req.User == gate.HostID {
		them, name = channel.AppMember(gate.HostID), s.Tag(gate.HostID)
	} else if ls, err := s.st.Logins(); err == nil {
		for _, l := range ls {
			if l.ID == req.User && !l.ApprovedAt.IsZero() {
				them, name = channel.PersonMember(l.App, l.ID), l.Name
				break
			}
		}
	}
	me := memberOf(c)
	if c.Host() {
		me = channel.AppMember(gate.HostID)
	}
	if them == "" {
		refuse(w, http.StatusUnprocessableEntity, "no_such_user", "no login "+req.User)
		return
	}
	if them == me {
		refuse(w, http.StatusBadRequest, "bad_request", "talking to oneself")
		return
	}
	pair := []string{me, them}
	slices.Sort(pair)
	key := "dm:" + strings.Join(pair, "|")
	// Looked up and made under one lock: the same two asking at once get the same one.
	s.dms.making.Lock()
	defer s.dms.making.Unlock()
	if id := s.st.Kept(key); id != "" {
		if _, ok := s.ch.Own(id); ok {
			reply(w, http.StatusOK, map[string]string{"channel": id})
			return
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		s.fault(w, err)
		return
	}
	// Made as the host: two people's channel is no folder's to allow (its people are the two).
	// Known as theirs before it is there, so that it is never listed to anyone else.
	id := session.NewChannelID()
	s.markDM(id, true)
	if made, err := s.createChannelAs(r.Context(), caller{Identity: localIdentity, socket: true}, api.NewSession{Cwd: home, Members: &api.ChannelSetup{People: pair}}, id); err != nil {
		if !made {
			s.markDM(id, false)
		}
		s.error(w, err)
		return
	}
	if err := s.ch.Rename(id, s.satoriUser(c).Name+" · "+name); err != nil {
		s.logf("server: dm %s: naming it: %v", id, err)
	}
	if err := s.st.Keep(key, id); err != nil {
		s.logf("server: dm %s: keeping it: %v", id, err)
	}
	s.membersChanged()
	reply(w, http.StatusOK, map[string]string{"channel": id})
}

// EditGiven changes message seq of channel ch where it waits in the queues of the agents it
// was given to (satori.Backend): each got it under an input of its own, which the injection
// kept of it names. Taken back, the agent was never given it: its call is closed, what it was
// given is forgotten, and its place in the channel is where it was before.
func (b satoriBackend) EditGiven(c satori.Caller, ch string, seq int64, old string, text *string) (int, error) {
	s := b.s
	n := 0
	for _, id := range s.ch.Agents(ch) {
		st, err := s.m.State(id)
		if err != nil {
			continue
		}
		rows, err := s.st.Injections(id, 0)
		if err != nil {
			return n, err
		}
		for _, in := range rows {
			if in.Channel != ch || in.Seq != seq {
				continue
			}
			i := slices.IndexFunc(st.Queue, func(q api.Queued) bool { return q.ID == in.Input })
			if i < 0 {
				continue
			}
			if _, err := s.using(callerAs(c), id); err != nil {
				return n, err
			}
			if text != nil {
				if err := s.m.EditQueued(id, in.Input, givenWith(st.Queue[i].Text, seq, old, *text)); err != nil {
					return n, err
				}
				n++
				continue
			}
			if err := s.m.DeleteQueued(id, in.Input); err != nil {
				return n, err
			}
			n++
			s.calls.take(id, func(c called) bool { return c.Input == in.Input })
			if err := s.st.RemoveInjection(id, in.Input); err != nil {
				s.logf("server: channel %s: forgetting what %s was not given: %v", ch, id, err)
			}
			var rec assemble.Record
			if json.Unmarshal([]byte(in.Record), &rec) == nil {
				s.ch.Unseen(ch, id, firstGiven(rec, seq)-1)
				s.readsChanged(ch)
			}
		}
	}
	return n, nil
}

// givenWith is given, what an agent gets for message seq of a channel ("#seq: " and the words
// old, then the channel's context), with the words text in old's place.
func givenWith(given string, seq int64, old, text string) string {
	head, rest := given, ""
	if at := strings.Index(given, "\n\n<channel-context>\n"); at >= 0 {
		head, rest = given[:at], given[at:]
	}
	if at := strings.LastIndex(head, old); old != "" && at >= 0 {
		return head[:at] + text + head[at+len(old):] + rest
	}
	return fmt.Sprintf("#%d: %s", seq, text) + rest
}

// firstGiven is the number of the first message record rec cites: where what the agent was
// given for message seq began.
func firstGiven(rec assemble.Record, seq int64) int64 {
	first := seq
	for _, p := range rec.Parts {
		if n, err := strconv.ParseInt(p.Values["from"], 10, 64); err == nil && n > 0 && p.Source == "prompts:channel.context.turn" {
			first = min(first, n)
		}
		for _, r := range p.Refs {
			if n, err := strconv.ParseInt(r.ID, 10, 64); err == nil && n > 0 && r.Kind == "message" {
				first = min(first, n)
			}
		}
	}
	return first
}
