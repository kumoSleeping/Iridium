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
	"github.com/kumoSleeping/iridium/internal/place"
	"github.com/kumoSleeping/iridium/internal/satori"
	"github.com/kumoSleeping/iridium/internal/session"
	"github.com/kumoSleeping/iridium/internal/store"
	"github.com/kumoSleeping/iridium/internal/transcript"
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

// link is where a session's turn stands in a channel's run: the agents passing what is said
// there on to each other since a person last spoke in it. origin: that person; epoch: which run
// of the channel (a person speaking begins the next); hop: how many agents (or scripts) in a row
// passed it on to reach this turn.
type link struct {
	origin string
	epoch  int64
	hop    int
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
	r.at[session] = link{hop: max(cur.hop, l.hop)}
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
	// React: it was a reaction it was given (docs/channels.md "表情"): saying nothing for it is
	// no silence to tell of.
	React bool `json:"react,omitempty"`
	// old: read from the store as this process began, its input given to the agent by then:
	// the turn that took it may have ended while nobody listened (calls.began).
	old bool
}

// calls are the calls each session's agent has open: made as it is given a channel's message,
// closed as the turn that took it ends (turnEnded). What the agent writes is said in the channel
// of the latest it has taken (answering). Kept in the store too (callsKey), so that a turn going
// on across a restart still has its words said where it was called (the user, 2026-11-13).
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

// answering is session's latest open call whose input its agent had been given (taken: the
// inputs given before the words it wrote, answers.took): what it wrote answers that call, and is
// said in its channel. Not one given after (its last turn's words heard late, as the next turn
// takes the call: kumo, 2026-11-17), nor one still queued. One kept from before this process,
// its input given by then, was taken: a turn going on across a restart still answers it.
func (k *calls) answering(session string, taken []string) (called, bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	cs := k.load(session)
	for i := len(cs) - 1; i >= 0; i-- {
		if wasGiven(k.st, cs[i], taken) {
			return cs[i], true
		}
	}
	return called{}, false
}

// wasGiven: call c's input was given to its agent, taken naming those given lately. One kept from
// before this process (old), from before calls had an input, or whose input the store no longer
// has (not waiting either), was.
func wasGiven(st *store.Store, c called, taken []string) bool {
	if c.old || c.Input == "" || slices.Contains(taken, c.Input) {
		return true
	}
	if st == nil {
		return false
	}
	_, _, ok := st.InputOf(c.Input)
	return !ok
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
	s.closeCalls(id, over)
}

// turnEnded: a turn of session's agent ended (also one cut short, its process gone); taken, the
// inputs it had been given by then (answers.given, as the end was told). The calls it took are
// over; the others, still queued or given after (a message steered in as the turn ended, which
// the next turn takes), stay open for the turn that takes them. What it wrote was said where it
// was called as it wrote it (answer); a call it said nothing for leaves a line in its channel.
func (s *Server) turnEnded(id string, taken []string) {
	// Not while it is being given a message: that call is made once the message is its.
	unlock := s.chain.lock(id)
	over := s.calls.take(id, func(c called) bool { return wasGiven(s.st, c, taken) })
	unlock()
	s.closeCalls(id, over)
}

// closeCalls closes calls over of session id, whose turn ended (turnEnded): each it said nothing
// for is told in its channel.
func (s *Server) closeCalls(id string, over []called) {
	done := map[string]bool{}
	for _, c := range over {
		if c.Spoke || done[c.Channel] || !s.ch.Speaks(c.Channel, id) {
			continue
		}
		done[c.Channel] = true
		nick := s.ch.Nick(id)
		// Its turn ended in an error (no balance left, a key refused): the channel is told why
		// (kumo, 2026-11-15: not a bare 未发话).
		why, words := s.turnError(id), ""
		// Called by another agent and nothing to say: nothing is told either (kumo, 2026-11-16);
		// only one a person called, or one whose turn failed, leaves its line.
		if why == "" && (c.React || s.agentSaid(c.Channel, c.Seq)) {
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
	s.scriptRoutes(mux)
	mux.HandleFunc("GET /v1/internal/channels/{id}/members", s.membersGet)
	mux.HandleFunc("POST /v1/internal/channels/{id}/members", s.memberAdd)
	mux.HandleFunc("PATCH /v1/internal/channels/{id}/members/{member}", s.memberPatch)
	mux.HandleFunc("DELETE /v1/internal/channels/{id}/members/{member}", s.memberRemove)
	mux.HandleFunc("PUT /v1/internal/channels/{id}/people", s.peoplePut)
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

// soloAgent returns the single agent session in channel ch, or "" if ch has 0 or 2+ agents.
func (s *Server) soloAgent(ch string) string {
	as := s.ch.Agents(ch)
	if len(as) == 1 {
		return as[0]
	}
	return ""
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
		am := api.ChannelMember{Member: m.Member, Session: m.Session, Nick: m.Nick, Seen: m.Seen}
		if m.Agent() {
			am.On = m.On()
			if st, err := s.m.State(m.Session); err == nil {
				am.Agent, am.Status, am.Cwd, am.Machine = st.Agent, st.Status, st.Cwd, st.Machine
				s.keepChoices(st)
				am.Name = s.sessionName(st)
			}
			p := s.agentProfile(m.Session)
			am.Avatar, am.Background, am.Role = p.Avatar, p.Background, p.Role
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
	case errors.Is(err, channel.ErrOn):
		refuse(w, http.StatusUnprocessableEntity, "bad_on", err.Error())
	case errors.Is(err, channel.ErrNick), errors.Is(err, channel.ErrPeople):
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
			Effort: req.Effort, Preset: req.Preset, Role: req.Role, Avatar: req.Avatar, Background: req.Background}
		if req.Cwd != "" {
			ns.Machine = req.Machine
		} else if req.Machine != "" && req.Machine != st.Machine {
			// At another computer than the channel's, no folder said: its home there. The
			// channel's folder is a path of the channel's computer, and the computer asked for was
			// let go with it: the agent started here, with this computer's agent and model.
			m := s.computerOf(req.Machine)
			if m == nil || m.Home == "" {
				return "", fmt.Errorf("%w: cwd: a folder at %s", session.ErrBadRequest, req.Machine)
			}
			ns.Machine, ns.Cwd = req.Machine, m.Home
		}
		var err error
		if id, err = s.create(ctx, c, ns); err != nil {
			return "", err
		}
		started = true
		s.keepSpawned(id, api.Spawned{By: c.session, Channel: ch})
	}
	// Its nick with the joining: one that does not join keeps the nick it had.
	m, err := s.ch.Join(ch, id, req.On, req.Nick, s.harnessOf, s.full(ch, launched))
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
	s.logf("server: channel %s: %s joined as @%s (%s)", ch, id, m.Nick, m.Triggers)
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
	m, err := s.ch.Join(ch, id, nil, "", s.harnessOf, s.full(ch, true))
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
	if _, err := s.deliverTo(ctx, ch, id, seq, first, "", from, nil, reach(deliver.From{Session: by}, s.chain.of(ch, by))); err != nil {
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
	s.facesChanged()
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
	// Its files by what they are ([图片], [文件]), not their paths; a message of files alone too.
	text := transcript.FilesAsWords(m.Text)
	for _, f := range m.D.Files {
		word := "[文件]"
		if f.Kind == api.FileKindImage {
			word = "[图片]"
		}
		if !strings.Contains(text, word) {
			text = strings.TrimSpace(text + " " + word)
		}
	}
	last := &api.LastSaid{Role: "user", Text: head(text, 200), From: m.Name, At: m.At}
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
// (listsChannel) and a session's (a bare one's row is made whole by bareChannels), unless its
// record's (rec) are later: its agent's own may not be read into the channel yet. A session alone
// in its channel: its record's, unless they were taken back (recordLast).
func (s *Server) channelLast(id string, rec *api.LastSaid) (*api.LastSaid, bool) {
	if _, bare := s.ch.Own(id); bare {
		return nil, false
	}
	if !s.listsChannel(id) {
		return s.recordLast(id, rec)
	}
	m, ok := s.ch.Latest(id)
	if !ok {
		return nil, false
	}
	last := s.lastSaid(m)
	if rec != nil && rec.At > last.At {
		return nil, false
	}
	return last, true
}

// channelAdded: message m was just written to its channel. Someone's words in a channel listed
// with them make the list again, shortly: m's writer may hold a session's lock.
func (s *Server) channelAdded(m channel.Message) {
	if (m.Kind == channel.KindUser || m.Kind == channel.KindSaid) && s.listsChannel(m.Channel) {
		s.listSoon()
	}
	// A line of the channel's own, an agent's error or ask: the subscriptions waiting for it
	// (subscribe.go).
	switch {
	case m.Kind == channel.KindNote && m.D.Event != nil:
		go s.eventHeard(m.Channel, m.Seq, m.D.Event, m.Text)
	case m.Kind == channel.KindError, m.Kind == channel.KindAsk:
		go s.eventHeard(m.Channel, m.Seq, &channel.Event{Type: m.Kind, Member: m.Author}, m.Text)
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
	if req.Machine != "" && !c.byAgent() {
		if err := s.st.SetOpener(id, c.ID()); err != nil {
			s.logf("server: opener of %s: %v", id, err)
		}
	}
	if err := s.m.ListBare(id, req.Machine, req.Cwd, req.Caller); err != nil {
		s.logf("server: channel %s: listing it: %v", id, err)
	}
	if req.Workspace != 0 {
		if err := s.st.Assign(id, req.Workspace, true); err != nil {
			s.logf("server: channel %s: its group: %v", id, err)
		}
	}
	setup.Nick, setup.On = "", nil
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
// be started leaves the channel without it, and a line of Iridium's in the channel says so.
func (s *Server) setUp(ctx context.Context, c caller, id string, setup api.ChannelSetup, first string) error {
	st, err := s.m.State(id)
	if err != nil {
		return err
	}
	if err := s.setNewPeople(c, id, setup.People); err != nil {
		return fmt.Errorf("%w: %w", session.ErrBadRequest, err)
	}
	var failed error
	// Its own agent (a session's channel): woken by what its maker says, unless told otherwise.
	if _, bare := s.ch.Own(id); !bare {
		var nick *string
		if setup.Nick != "" {
			nick = &setup.Nick
		}
		on := setup.On
		if on == nil {
			on = makerOn(c)
		}
		if _, err := s.ch.Change(id, store.AgentMember(id), nick, &on, s.harnessOf); err != nil {
			failed = cmp.Or(failed, err)
		}
	}
	for _, a := range setup.Agents {
		if _, err := s.addAgent(ctx, c, id, st, a); err != nil {
			// The channel is made and stays; it says who is not in it and why (kumo, 2026-11-18:
			// answered with an error, the page stayed where it was and each press made another).
			s.logf("server: channel %s: adding %s: %v", id, a.Agent, err)
			who := cmp.Or(a.Nick, string(a.Agent), a.Role, a.Session)
			if a.Machine != "" {
				info, _ := s.gate().Endpoint(a.Machine)
				who += "（" + cmp.Or(info.Name, a.Machine) + "）"
			}
			s.sat.Note(id, fmt.Sprintf("%s 未加入：%s", who, s.MaskText(err.Error())), c.ID(), nil)
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
		out.Presets = s.presetsAt(r.Context(), kind, machine)
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
	out.Presets = s.presetsAt(r.Context(), kind, machine)
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

// presetsWait is how long the presets of kind at machine are waited for before the kept ones
// stand in (an agent's host's registry, which its channel keeps half a minute).
const presetsWait = 2 * time.Second

// presetsAt are the presets kind has at machine now (otae, 2026-11-22: the list a session saw
// before the agent's host was there stood in for the host's), kept for the next time; those kept last
// while it does not answer; nil for a kind with none.
func (s *Server) presetsAt(ctx context.Context, kind api.Agent, machine string) *api.Presets {
	if info, _ := agents.Get(kind); !info.Presets {
		return nil
	}
	ask := func(ctx context.Context, wait time.Duration) (*api.Presets, error) {
		ctx, cancel := context.WithTimeout(ctx, wait)
		defer cancel()
		p, err := s.m.PagePresets(ctx, kind, machine)
		if err != nil || len(p.Presets) == 0 {
			return nil, err
		}
		if b, err := json.Marshal(p); err == nil {
			_ = s.st.Keep("presets:"+string(kind)+":"+machine, string(b))
		}
		return &p, nil
	}
	if p, _ := ask(ctx, presetsWait); p != nil {
		return p
	}
	// Slow the first time (its host reads each preset): asked on past this request, kept for the
	// next.
	go func() {
		if _, err := ask(context.Background(), 30*time.Second); err != nil {
			s.logf("server: presets of %s at %q: %v", kind, machine, err)
		}
	}()
	return s.keptPresets(kind, machine)
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
		if _, err := s.deliverTo(ctx, ch, id, seq, words, "", session.From(gate.HostID, ""), nil, lk); err != nil {
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
	now, err := s.ch.Change(ch, r.PathValue("member"), p.Nick, p.On, s.harnessOf)
	if err != nil {
		s.memberFault(w, err)
		return
	}
	if was.Nick != "" && now.Nick != was.Nick {
		s.tellMembers(c, ch, fmt.Sprintf("@%s 改名为 @%s", was.Nick, now.Nick), channel.Event{Type: "rename", Member: now.Member, Nick: now.Nick, From: was.Nick})
	}
	var warn []string
	if now.Agent() && was.Triggers != now.Triggers {
		on := now.On()
		s.tellMembers(c, ch, fmt.Sprintf("@%s 唤醒：%s", now.Nick, s.onWords(on)), channel.Event{Type: "wake", Member: now.Member, Nick: now.Nick, On: on})
		s.readsChanged(ch) // the channel's own agent: how far it has read depends on it
		warn = s.loops(ch, now.Member)
	}
	s.membersChanged()
	out, err := s.membersOf(c, ch)
	if err != nil {
		s.fault(w, err)
		return
	}
	out.Warn = warn
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
	return avatarIn(liveFaces{s.st}, ch, st)
}

// avatarIn is avatarOf, looked up in src.
func avatarIn(src faceSrc, ch string, st *api.SessionState) (image []byte, version string, ok bool) {
	return faceIn(src, store.ChannelProfileKey(ch), false, modelAvatarKey(st))
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
	return s.channelFaceIn(liveFaces{s.st}, ch, st, bg)
}

// channelFaceIn is channelFace, looked up in src.
func (s *Server) channelFaceIn(src faceSrc, ch string, st *api.SessionState, bg bool) (image []byte, version string, ok bool) {
	if key := s.roomKey(ch); key != store.ChannelProfileKey(ch) {
		if img, v, ok := faceIn(src, key, bg, ""); ok {
			return img, v, true
		}
		if !bg && len(s.ch.Agents(ch)) > 1 {
			return nil, "", false
		}
	}
	if bg {
		return faceIn(src, store.ChannelProfileKey(ch), true, "")
	}
	return avatarIn(src, ch, st)
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
// of their own have it, those started later too. Any of the channel's members may, its agents
// too (kumo, 2026-11-17: an agent asked to change a face was refused here, though its card's
// PATCH let it).
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
	if r.Method == http.MethodDelete {
		if err := s.st.DeleteAvatar(channelAvatarKey(ch)); err != nil {
			s.fault(w, err)
			return
		}
	} else {
		img, v, ok := avatarBody(w, r)
		if !ok {
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
	s.facesChanged()
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
				out = append(out, deliver.Agent{Session: m.Session, Nick: m.Nick, On: m.On()})
			}
		}
	}
	return out, by
}

// sendChannel says req in channel ch (several agents) as c and gives it to the agents it wakes.
// A person needs the right to use each agent the message names (Claude: the owner's own); one
// that would merely receive everything is passed over without it.
func (s *Server) sendChannel(ctx context.Context, c caller, from, ch string, req api.Send) (api.SendReply, error) {
	return s.sendChannelAs(ctx, c, from, ch, req, nil)
}

// BehindMax is how many messages an agent may not have seen of a channel and still speak in it
// with ir; with more, or with one that names it, it is told to read them first.
const BehindMax = 12

// behindError refuses an agent's ir: what it has not seen of the channel, as it is told.
type behindError struct{ text string }

func (e *behindError) Error() string { return e.text }

// wakes are the agents of ch a message with words text wakes (deliver.Route), said by from,
// whose turn (an agent's, a script's) stands at l in the channel's run. A person's begins a run
// and wakes whom the rules give it; past MaxHops in a row nobody is woken. limit: someone was to
// be woken and nobody is, for the limit reached (0: none was).
func (s *Server) wakes(text string, from deliver.From, l link, list []deliver.Agent) (wake, named []string, texts map[string][]api.Trigger, limit int) {
	// <at> written in the words calls as "@nick" does (Markdown and Satori's elements mixed).
	text = satori.AtWords(text, s.ch.Nick)
	d := deliver.Route(text, from, list)
	if !passes(from) || len(d.Wake) == 0 || l.hop < deliver.MaxHops {
		return d.Wake, d.Named, d.Texts, 0
	}
	return nil, d.Named, nil, deliver.MaxHops
}

// passes: from is an agent or a script, which go on a run a person began (a person begins one).
func passes(from deliver.From) bool {
	return from.Session != "" || strings.HasPrefix(from.Member, scriptMemberPrefix)
}

// reach is the link of a message said by from (its turn at l) as it reaches an agent: one more
// passed it on, unless a person said it.
func reach(from deliver.From, l link) link {
	if passes(from) {
		l.hop++
	}
	return l
}

// errTooFar refuses an agent's ir: limit agents in a row passed the message on.
func errTooFar(limit int) error {
	return fmt.Errorf("%w: agents have woken one another %d times in a row; a person goes on from here", session.ErrBadRequest, limit)
}

// stopped says in channel ch that message seq was not passed on: limit agents in a row had.
func (s *Server) stopped(ch string, seq int64, limit int, origin string) {
	s.sat.Note(ch, fmt.Sprintf("未转交 #%d：Agent 已连续互相叫起 %d 次", seq, limit), origin, &channel.Event{Type: "stopped", Seq: seq})
}

// written is what an agent wrote in its turn, said where it was called (answer), not sent by it
// with ir. final: its turn's last words. from, to: the stretch of its record that led to them.
type written struct {
	final bool
	from  *int64
	to    int64
}

// sendChannelAs is sendChannel. relay: the words are what an agent wrote, said for it where it
// was called (answer), not sent by it with ir: said whatever comes of passing them on (a line of
// the channel says whom they did not reach, as for what an agent says in its own channel), and
// the agent is neither held to what it has seen of the channel (docs/channels.md "发送检查")
// nor given it.
func (s *Server) sendChannelAs(ctx context.Context, c caller, from, ch string, req api.Send, relay *written) (api.SendReply, error) {
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
	// Another session's agent, not of the channel, writing to it with ir: its words are that
	// agent's, drawn and counted as its, not as the login its turn acts for (kumo, 2026-11-21: a
	// letter from another session showed as his). Whom it wakes is as for a letter: as before.
	outsider := ""
	if sender == "" && c.session != "" && c.session != ch {
		outsider = c.session
		u = satori.User{ID: store.AgentMember(outsider)}
	}
	unseen := ""
	var given int64 // how far the agent that speaks has been given the channel, once this is sent
	if sender != "" {
		u = satori.User{ID: store.AgentMember(sender)}
		_, by := s.agentsOf(ch)
		me := by[sender]
		given = me.Seen
		if relay == nil {
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
	// Answering a message (<quote>): kept with it, and given to whom it wakes; it calls no one
	// (docs/channels.md "叫起谁": only the @ in the words do).
	var quote int64
	if req.Quote > 0 {
		q, ok := s.ch.Get(ch, req.Quote)
		if !ok {
			return api.SendReply{}, fmt.Errorf("%w: quote: no message %d in the channel", session.ErrBadRequest, req.Quote)
		}
		quote = q.Seq
	}
	// An agent's words wake those subscribed to it only as its turn's last; on its way (and with
	// ir), only whom they name.
	final := relay != nil && relay.final
	who := deliver.From{Member: readerOf(c), Session: sender, Quiet: sender != "" && !final}
	if sender != "" {
		who.Member = store.AgentMember(sender)
	}
	routed, namedNicks, texts, limit := s.wakes(req.Text, who, lk, list)
	if limit > 0 && relay == nil {
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
	// An agent's words are an agent's (kind "said"), marked as sent; its turn's last are final.
	// A person's that wake no agent of a session's own channel are held for it (heldWords).
	of := channel.Data{Quote: quote, Sent: sender != "" || outsider != "", Final: sender != "" && final}
	if !s.multi(ch) && sender == "" && outsider == "" && !slices.Contains(wake, ch) {
		of.Held = true
	}
	if outsider != "" {
		of.Session = outsider
	}
	// Its files, as the page draws them (the words given to the agents name them already).
	of.Files = s.files.Refs(ch, req.Attachments)
	if sender != "" && sender != ch {
		// Where it is from: the stretch of the agent's own record since it was called here.
		of.Session = sender
		if relay != nil {
			of.From, of.To = relay.from, relay.to
		} else {
			if sst, err := s.m.State(sender); err == nil {
				of.To = sst.End
			}
			of.From = s.calls.spoke(sender, ch, of.To)
		}
	}
	of.Device = s.deviceOfCaller(c)
	seq := s.sat.Input(st.Session, req.ID, req.Text, u, lk.origin, of)
	if seq == 0 {
		return api.SendReply{}, errors.New("the channel did not take the message")
	}
	// The logins it names have a mark (ats.go).
	author := ""
	if sender == "" {
		author = readerOf(c)
	}
	if outsider != "" {
		author = store.AgentMember(outsider)
	}
	named := s.names(ch, seq, req.Text, author)
	// Said: a person's begins a new run.
	if sender == "" {
		lk = s.chain.person(ch, c.ID())
		if outsider != "" {
			go s.heard(ch, seq, req.Text, author, cmp.Or(s.ch.Nick(outsider), s.m.SourceTag(from)), lk)
		} else {
			go s.heard(ch, seq, req.Text, readerOf(c), u.Name, lk)
		}
	} else {
		if final {
			go s.heard(ch, seq, req.Text, store.AgentMember(sender), s.ch.Nick(sender), lk)
		}
		// An agent's words reach the others under its nick, as routeSaid gives them, not its
		// ir's tag (a session's id: kumo, 2026-11-16).
		from = session.From(gate.HostID, "@"+s.ch.Nick(sender))
	}
	out := api.SendReply{Input: req.ID, Seq: seq, Said: true, Unseen: unseen}
	var firstErr error
	var failed []string
	sent := 0
	for _, t := range wake {
		queued, err := s.deliverTo(ctx, ch, t, seq, text+s.onText(texts[t]), req.ID, from, images, reach(who, lk))
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
	if sent == 0 && firstErr != nil && relay == nil {
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

// channelPlace is the folder of channel ch: its session's, or its own where it is no session's.
func (s *Server) channelPlace(ch string) (place.Place, bool) {
	if st, err := s.m.State(ch); err == nil && st.Cwd != "" {
		return place.Place{App: st.Machine, Path: st.Cwd}, true
	}
	for _, c := range s.ch.Owns() {
		if c.ID == ch && c.Cwd != "" {
			return place.Place{App: c.Machine, Path: c.Cwd}, true
		}
	}
	return place.Place{}, false
}

// placeWords is p as an agent is told it: its computer by the name its app goes by now.
func (s *Server) placeWords(p place.Place) string {
	return place.Format(p, func(id string) string {
		info, _ := s.gate().Endpoint(id)
		return info.Name
	})
}

// samePlace: a and b are one folder of one computer, however its end and its separators are
// written.
func samePlace(a, b place.Place) bool {
	norm := func(p place.Place) string { return strings.TrimRight(place.Key(p), "/") }
	return a.App == b.App && norm(a) == norm(b)
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
func (s *Server) deliverTo(ctx context.Context, ch, to string, seq int64, said, input, from string, images []agent.Image, lk link) (bool, error) {
	return s.deliverAs(ctx, ch, to, seq, said, input, from, images, lk, nil)
}

// deliverAs is deliverTo; react: what is given is a reaction to message seq (said, its words),
// not the message: it is the reaction's line, with what was said since the agent last read the
// channel, all of it, before or after seq (docs/channels.md "表情").
func (s *Server) deliverAs(ctx context.Context, ch, to string, seq int64, said, input, from string, images []agent.Image, lk link, react *assemble.Reaction) (bool, error) {
	// One at a time for an agent: each is given what it has not seen, once.
	defer s.chain.lock(to)()
	said = satori.ForwardWords(said) // a forward in it as lines
	list, by := s.agentsOf(ch)
	me, ok := by[to]
	if !ok {
		return false, channel.ErrNoMember
	}
	before := seq
	if react != nil {
		before = 0
	}
	unseen, did, work, _, upTo, err := s.unread(ch, me, before, by)
	if err != nil {
		return false, err
	}
	cc := assemble.Channel{You: me.Nick, Plan: strategy.Fold(unseen, strategy.Default), Pull: true, Zone: time.Local, Did: did}
	if !me.Direct() {
		// In it by a subscription: told which channel this is of and that what it writes is said
		// there as it writes it; with ir, also that ir is for speaking elsewhere.
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
	// Still under the name Iridium gave it: it names itself first, where it has ir to. A role's
	// session has its role's name.
	cc.Rename = channel.IsDefaultNick(me.Nick) && s.m.HasIR(to) && s.st.RoleOf(to) == ""
	// Where the channel is, and each agent that works elsewhere (the user, 2026-11-18).
	home, homeOK := s.channelPlace(ch)
	if homeOK {
		cc.Place = s.placeWords(home)
	}
	var keys []string
	for _, a := range list {
		ca := assemble.ChannelAgent{Nick: a.Nick}
		if st, err := s.m.State(a.Session); err == nil {
			ca.Name = s.sessionName(st)
			if at := (place.Place{App: st.Machine, Path: st.Cwd}); homeOK && st.Cwd != "" && !samePlace(at, home) {
				ca.At = s.placeWords(at)
				if a.Session == to {
					cc.At = ca.At
				}
			}
		}
		ca.Recv = s.triggerWords(a.On)
		cc.Agents = append(cc.Agents, ca)
		keys = append(keys, a.Session+"="+channel.EncodeTriggers(a.On)+"@"+ca.At)
	}
	// The scripts IR runs there: their names and triggers are part of the standing words.
	if xs, err := s.st.Scripts(ch); err == nil {
		for _, x := range xs {
			if !x.Enabled {
				continue
			}
			on := s.triggerWords(triggersOf(x))
			cc.Scripts = append(cc.Scripts, assemble.ChannelScript{Name: x.Name, On: on})
			keys = append(keys, "script:"+x.Name+"="+on)
		}
	}
	slices.Sort(keys)
	cc.Key = strings.Join(keys, ",")
	for _, pc := range s.profileChanges(ch, to) {
		cc.Changes = append(cc.Changes, assemble.ProfileChange{Who: pc.Who, What: pc.What, To: pc.To})
	}
	// The message it answers, if it answers one: a line of it before its words.
	if m, ok := s.ch.Get(ch, seq); ok && m.D.Quote > 0 && react == nil {
		if q, ok := s.ch.Get(ch, m.D.Quote); ok {
			cc.Quote = &assemble.Quote{Seq: q.Seq, Who: whoOf(q, by), Text: satori.ForwardWords(q.Text)}
		}
	}
	// What it was given and was taken back since it was last given something here.
	cc.Recalled = s.recalledFor(to, ch, me.Seen)
	var line int64 // where its record is as it is given this
	if st, err := s.m.State(to); err == nil {
		line = st.End
	}
	// The standing words again only when they changed, were never given here, or may be gone
	// from the agent's context since (a compaction): docs/channels.md "增量".
	static := assemble.ChannelStaticKey(cc)
	if last, ok := s.lastInjection(to, ch); ok && last.Static == static && !s.compactedSince(to, last.Line) {
		cc.Brief = true
	}
	var in assemble.Injection
	if react != nil {
		in, err = assemble.ChannelReaction(cc, *react)
	} else {
		in, err = assemble.ChannelTurn(cc, seq, said, input)
	}
	if err != nil {
		return false, err
	}
	id := session.NewInputID()
	rec := in.Record()
	rec.Static, rec.Full, rec.Line = static, !cc.Brief, line
	if rec, err := json.Marshal(rec); err == nil {
		if err := s.st.AddInjection(store.Injection{Session: to, Channel: ch, Input: id, Seq: seq, Record: string(rec)}); err != nil {
			s.logf("server: channel %s: keeping what %s was given: %v", ch, to, err)
		}
	}
	_, queued, err := s.m.SendAs(ctx, to, id, in.String(), from, true, images...)
	if err != nil {
		// Not given: not kept as given, or the next time would take the standing words as had.
		if err := s.st.RemoveInjection(to, id); err != nil {
			s.logf("server: channel %s: forgetting what %s was not given: %v", ch, to, err)
		}
		return false, err
	}
	if react != nil {
		s.seen(ch, to, upTo)
	} else {
		s.seen(ch, to, seq)
	}
	if !me.Direct() {
		s.calls.add(to, called{Channel: ch, Seq: seq, From: line, Input: id, React: react != nil})

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
		msgs = append(msgs, strategy.Msg{Seq: m.Seq, Who: whoOf(m, by), Agent: agent, Text: fileNames(satori.ForwardWords(m.Text), m.D.Files), At: m.At})
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
	// What it sent with ir (or wrote where it was called) was given to whom it names as it was sent.
	if !ok || m.Kind != channel.KindSaid || m.D.Sent {
		return
	}
	s.answer(sender, m)
	// The logins it names (ats.go), once it is on its way to the agents it names: what is said
	// after it is told after this returns.
	defer s.names(m.Channel, m.Seq, m.Text, "")
	if m.D.Final {
		go s.heard(m.Channel, m.Seq, m.Text, m.Author, s.ch.Nick(sender), s.chain.of(m.Channel, sender))
	}
	list, by := s.agentsOf(m.Channel)
	if len(list) < 2 {
		return
	}
	lk := s.chain.of(m.Channel, sender)
	// Its turn's last words (Final) wake those subscribed to it too, and the scripts listening;
	// what it says on its way, only whom it names.
	who := deliver.From{Member: m.Author, Session: sender, Quiet: !m.D.Final}
	wake, _, texts, limit := s.wakes(m.Text, who, lk, list)
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
			_, err := s.deliverTo(ctx, m.Channel, t, m.Seq, m.Text+s.onText(texts[t]), "", from, nil, reach(who, lk))
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

// answer: session id's agent wrote m in its own channel. Called in another channel and
// answering there (calls.answering), it is said there too, as it was written: what an agent
// writes is said where it was called, as a person answers where they were spoken to (kumo,
// 2026-11-17). Which inputs it had been given by then is taken now, in order with its words;
// the rest is done in its turn of answers, after what it wrote before and before its turn's end
// closes the call.
func (s *Server) answer(id string, m channel.Message) {
	taken := s.answers.given(id)
	text, nick, final := m.Text, s.ch.Nick(id), m.D.Final
	s.answers.add(id, func() {
		// A message being given to it has its call made by the time this lock is had (deliverTo).
		unlock := s.chain.lock(id)
		c, ok := s.calls.answering(id, taken)
		// Its own channel may have the same words twice (heard live, then read from its record
		// as another): they are said for a call once.
		ok = ok && c.Channel != m.Channel && s.ch.Speaks(c.Channel, id) && s.answers.first(id, c, text)
		w := &written{final: final}
		if ok {
			if st, err := s.m.State(id); err == nil {
				w.to = st.End
			}
			w.from = s.calls.spoke(id, c.Channel, w.to)
		}
		unlock()
		if !ok {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if _, err := s.sendChannelAs(ctx, caller{Identity: localIdentity, socket: true, agent: true, session: id}, session.From(gate.HostID, "@"+nick), c.Channel, api.Send{Text: text}, w); err != nil {
			s.logf("server: channel %s: saying what %s wrote: %v", c.Channel, id, err)
		}
	})
}

// answers is, for each agent, what is done of its turns in order: what it wrote said where it
// was called, its turn's start and end closing calls (server.go), one at a time; and the inputs
// it was given lately (took), in order with what it says.
type answers struct {
	mu     sync.Mutex
	of     map[string][]func()
	taken  map[string][]string
	said   map[string][]saidFor
	closed bool
	wg     sync.WaitGroup
}

// close drops what waits and waits a little for what is being done (Server.Close).
func (a *answers) close() {
	a.mu.Lock()
	a.closed = true
	for k := range a.of {
		a.of[k] = a.of[k][:min(1, len(a.of[k]))] // the one being done, if any
	}
	a.mu.Unlock()
	done := make(chan struct{})
	go func() { a.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
}

// saidFor is words said for a call (its channel and number).
type saidFor struct {
	channel string
	seq     int64
	text    string
}

// first: text is not yet said for call c of session's agent; it is now. The last few are kept.
func (a *answers) first(session string, c called, text string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.said == nil {
		a.said = map[string][]saidFor{}
	}
	for _, x := range a.said[session] {
		if x.channel == c.Channel && x.seq == c.Seq && channel.Same(x.text, text) {
			return false
		}
	}
	l := append(a.said[session], saidFor{c.Channel, c.Seq, text})
	a.said[session] = l[max(0, len(l)-keptTaken):]
	return true
}

// keptTaken is how many inputs given are kept for each agent.
const keptTaken = 64

// took: session's agent is given input now (session.NoticeTaken).
func (a *answers) took(session, input string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.taken == nil {
		a.taken = map[string][]string{}
	}
	t := append(a.taken[session], input)
	a.taken[session] = t[max(0, len(t)-keptTaken):]
}

// given are the inputs session's agent was given lately, as told so far.
func (a *answers) given(session string) []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.taken[session])
}

// add queues do for session's agent; the first queued starts the one that does them.
func (a *answers) add(session string, do func()) {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return
	}
	if a.of == nil {
		a.of = map[string][]func(){}
	}
	a.of[session] = append(a.of[session], do)
	first := len(a.of[session]) == 1
	if first {
		a.wg.Add(1)
	}
	a.mu.Unlock()
	if first {
		go a.run(session)
	}
}

func (a *answers) run(session string) {
	defer a.wg.Done()
	for {
		a.mu.Lock()
		q := a.of[session]
		if len(q) == 0 {
			delete(a.of, session)
			a.mu.Unlock()
			return
		}
		do := q[0]
		a.mu.Unlock()
		do()
		a.mu.Lock()
		a.of[session] = a.of[session][1:]
		a.mu.Unlock()
	}
}

// relayHead is how a channel's message given to an agent begins ("#12: …").
var relayHead = regexp.MustCompile(`^#(\d+)(?:: | \[)`)

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

// avatarBody is the image a PUT of a face carries (the image itself, or JSON {"avatar": base64 or
// a data: URL}), made into a face (gate.MakeAvatar); when it is not one it has answered.
func avatarBody(w http.ResponseWriter, r *http.Request) ([]byte, string, bool) {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 2*gate.MaxAvatarBytes))
	if err != nil {
		refuse(w, http.StatusRequestEntityTooLarge, "avatar_too_large", "at most 2 MB")
		return nil, "", false
	}
	// As JSON: {"avatar": the image in base64, or a data: URL}.
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") || bytes.HasPrefix(bytes.TrimSpace(data), []byte("{")) {
		var body struct {
			Avatar string `json:"avatar"`
		}
		if json.Unmarshal(data, &body) != nil {
			refuse(w, http.StatusBadRequest, "bad_request", "avatar: the image in base64")
			return nil, "", false
		}
		b64 := body.Avatar
		if strings.HasPrefix(b64, "data:") {
			_, b64, _ = strings.Cut(b64, ",")
		}
		if data, err = base64.StdEncoding.DecodeString(b64); err != nil {
			refuse(w, http.StatusBadRequest, "bad_request", "avatar: the image in base64")
			return nil, "", false
		}
	}
	img, v, err := gate.MakeAvatar(data)
	switch {
	case errors.Is(err, gate.ErrAvatarTooLarge):
		refuse(w, http.StatusRequestEntityTooLarge, "avatar_too_large", "at most 2 MB")
		return nil, "", false
	case err != nil:
		refuse(w, http.StatusUnprocessableEntity, "avatar_format", "a PNG, JPEG or WebP image")
		return nil, "", false
	}
	return img, v, true
}
