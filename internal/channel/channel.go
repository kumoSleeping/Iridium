// Package channel is the channels (docs/channels.md): where people and agents talk. A channel
// keeps what was said in it, a message a number, and who is in it; an agent's own record (its
// thinking, its tool calls) stays the agent's. What an agent said is read in from that record
// (Sync), what a person said is written as it is sent (Say). A session has a channel of its own,
// under the session's id: what its agent says in its turns is said there. A session may also be
// in other channels (a subscription, members.go): there it sees only what is said, and what it
// writes in a turn a message there called is said there too, as in its own. A channel may be no
// session's (store.Channel): then all its agents are such.
package channel

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/kumoSleeping/iridium/internal/activity"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/cron"
	"github.com/kumoSleeping/iridium/internal/deliver"
	"github.com/kumoSleeping/iridium/internal/store"
	"github.com/kumoSleeping/iridium/internal/transcript"
)

// Kinds of message.
const (
	KindUser  = "user"  // a person's words
	KindSaid  = "said"  // an agent's words: of its turn, read from its record, or sent with ir (Data.Sent)
	KindAsk   = "ask"   // an agent asks: a question, an approval, a plan
	KindError = "error" // an agent's process ended badly
	KindNote  = "note"  // Iridium's own line: who joined, a message not passed on
	// KindWork: an agent at work that has said nothing yet in its turn: no words, its Data.Work
	// alone (docs/next/ui.md). Agents are not given it; a page draws it as a line.
	KindWork = "work"
)

// PersonRow is what a person member's row keeps where an agent's keeps its subscriptions.
const PersonRow = "all"

// EveryoneOn is the subscription an agent of its own channel has by default where nobody chose
// one (the sessions from before 2026-11-23): the words of every person wake it.
var EveryoneOn = []api.Trigger{{Message: &api.MessageTrigger{From: []string{Everyone}}}}

// TriggersOf are the subscriptions an agent's row keeps (docs/channels.md "订阅事件"): what
// wakes it beside what names it. Rows from before them: a receive set ["*", …] is one message
// subscription from those, "all" every person's words, "at" or none nothing.
func TriggersOf(raw string) []api.Trigger {
	switch raw {
	case PersonRow:
		return slices.Clone(EveryoneOn)
	case "at", "":
		return []api.Trigger{}
	}
	var on []api.Trigger
	if json.Unmarshal([]byte(raw), &on) == nil && on != nil {
		return on
	}
	var set []string
	if json.Unmarshal([]byte(raw), &set) == nil && len(set) > 0 {
		return []api.Trigger{{Message: &api.MessageTrigger{From: set}}}
	}
	return []api.Trigger{}
}

// EncodeTriggers is on as a row keeps it.
func EncodeTriggers(on []api.Trigger) string {
	if on == nil {
		on = []api.Trigger{}
	}
	b, _ := json.Marshal(on)
	return string(b)
}

// ErrOn: a subscription is none there may be.
var ErrOn = errors.New("on")

// errFrom: an entry of a message subscription's from is none of the senders there are.
var errFrom = errors.New("from")

// cleanFrom checks set's entries and drops those twice; self (an agent's own member) may not be
// one, nor may the agents' "agent:*".
func cleanFrom(set []string, self string) ([]string, error) {
	out := []string{}
	for _, e := range set {
		e = strings.TrimSpace(e)
		ok := e == Everyone
		if id, isAgent := strings.CutPrefix(e, "agent:"); isAgent {
			ok = id != "" && id != "*" && e != self
		} else if app, login, cut := strings.Cut(e, "/"); cut {
			ok = app != "" && login != "" && !strings.Contains(login, "/")
		}
		if !ok {
			return nil, errFrom
		}
		if !slices.Contains(out, e) {
			out = append(out, e)
		}
	}
	return out, nil
}

// TextMax is the most an event's Text may be, in characters.
const TextMax = 2000

// CleanOn checks the subscriptions of self (an agent's member, a script's: what it may not
// subscribe to of its own; "" none), each of one kind: Message (from as cleanFrom, words
// trimmed and each once), Command (a name), Cron (as cron reads it), Event (of api.Events).
// The kinds from before them (recv, script, people) are read as theirs now (on, join).
func CleanOn(on []api.Trigger, self string) ([]api.Trigger, error) {
	out := make([]api.Trigger, 0, len(on))
	for i, t := range on {
		at := func(f string, a ...any) error {
			return fmt.Errorf("%w[%d].%s", ErrOn, i, fmt.Sprintf(f, a...))
		}
		kinds := 0
		if t.Message != nil {
			kinds++
			from, err := cleanFrom(t.Message.From, self)
			if err != nil {
				return nil, at(`message.from: a list of "*" (every person), "<app>/*", "<app>/<login>", "agent:<session>", not its own`)
			}
			var words []string
			for _, w := range t.Message.Words {
				if w = strings.TrimSpace(w); w != "" && !slices.Contains(words, w) {
					words = append(words, w)
				}
			}
			t.Message = &api.MessageTrigger{Words: words}
			if len(from) > 0 {
				t.Message.From = from
			}
		}
		if t.Command != "" {
			kinds++
			t.Command = strings.TrimPrefix(strings.TrimSpace(t.Command), "/")
			if !deliver.ValidNick(t.Command) {
				return nil, at("command: a name, letters, digits, _ and -")
			}
		}
		if t.Cron != "" {
			kinds++
			t.Cron = strings.Join(strings.Fields(t.Cron), " ")
			if _, err := cron.Parse(t.Cron); err != nil {
				return nil, at("cron: %v", err)
			}
		}
		if len(t.Event) > 0 {
			kinds++
			var evs []string
			for _, e := range t.Event {
				e = EventKind(e)
				if !slices.Contains(api.Events, e) {
					return nil, at("event %q: of %s", e, strings.Join(api.Events, " "))
				}
				if !slices.Contains(evs, e) {
					evs = append(evs, e)
				}
			}
			t.Event = evs
		}
		if kinds != 1 {
			return nil, at("each one of message, command, cron, event")
		}
		t.Text = strings.TrimSpace(t.Text)
		if utf8.RuneCountInString(t.Text) > TextMax {
			return nil, at("text: at most %d characters", TextMax)
		}
		out = append(out, t)
	}
	return out, nil
}

// EventKind is an event entry's name of a kind of line: those of before it as theirs now,
// "recv", "script" and "on" (2026-11-23) are "wake", "people" "join" (and "leave").
func EventKind(e string) string {
	switch e = strings.TrimSpace(e); e {
	case "recv", "script", "on":
		return "wake"
	case "people":
		return "join"
	}
	return e
}

// Everyone is the member that is every login of every app: a channel nobody chose the people
// of has it, and whoever sees the channel may speak in it.
const Everyone = "*"

// Sessions is what the channels ask of the sessions (session.Manager).
type Sessions interface {
	State(id string) (*api.SessionState, error)
	Messages(id string, before, after int64) (api.MessagePage, error)
	Full(id, at string) (string, error)
}

// Data is a message's own, by its kind.
type Data struct {
	// Session: the agent's session the message is from (its files are that session's); Block,
	// its place in that record.
	Session string        `json:"session,omitempty"`
	Block   string        `json:"block,omitempty"`
	Files   []api.FileRef `json:"files,omitempty"`
	// From, To: for what an agent said with ir (in a channel it is in by a subscription), the
	// stretch of its own record (Session's) that led to it: the lines it spans. From nil: not
	// known where it began (0 is a place: a session called first, its record empty then).
	From *int64 `json:"from,omitempty"`
	To   int64  `json:"to,omitempty"`
	// Final: the last thing the agent said in its turn.
	Final bool `json:"final,omitempty"`
	// Sent: an agent's words said with ir, or for it as its turn ended (then Final), not read
	// from its record: given to whom they name as they were sent, not once more as said.
	Sent bool `json:"sent,omitempty"`
	// Quote: the message it answers.
	Quote int64 `json:"quote,omitempty"`
	// Card: an ask's card id.
	Card string `json:"card,omitempty"`
	// Answer: an ask's, once a person answered it: who and what (kumo, 2026-11-27: each said by
	// whom, in the conversation). None: open still, or over unanswered.
	Answer *Answer `json:"answer,omitempty"`
	// Asked: an ask's, what it asked, kept past its close (the ask itself goes with it): drawn
	// as a line in the conversation once answered or over.
	Asked *Asked `json:"asked,omitempty"`
	// Event: what a line of Iridium's own tells of (a member joined, left…), for a page to say
	// in its own words; the message's text says it too.
	Event *Event `json:"event,omitempty"`
	// Activity: what the agent did between the last thing it said and this.
	Activity *activity.Activity `json:"activity,omitempty"`
	// Device: the device a person's browser sent it from (api.Device), for the badge on its
	// avatar; "" not known.
	Device string `json:"device,omitempty"`
	// Script: the script of the channel's IR that said it (its author "script:<name>").
	Script string `json:"script,omitempty"`
	// Held: said in a session's own channel without waking its agent (none of its subscriptions
	// was for it): given to it before what next wakes it there.
	Held bool `json:"held,omitempty"`
	// Writing: the agent is writing it now: its text is what there is so far (docs/next/ui.md).
	Writing bool `json:"writing,omitempty"`
	// Work: what the agent did after saying it (or, of a KindWork, before it said anything),
	// counted live.
	Work *Work `json:"work,omitempty"`
}

// Answer is how an ask was answered: By, the member who did (a login's id), Name as it was
// then; Option, the option's id (allow, deny, always…; "" for words alone); Label, what was
// chosen in words, as its key said it; At (ms).
type Answer struct {
	By     string `json:"by,omitempty"`
	Name   string `json:"name,omitempty"`
	Option string `json:"option,omitempty"`
	Label  string `json:"label,omitempty"`
	At     int64  `json:"at"`
}

// Asked is what an ask asked: its kind (permission, plan, question, manual), its tool, its
// card's first line under the title (a command, a path, a reason, a question).
type Asked struct {
	Kind    string `json:"kind,omitempty"`
	Tool    string `json:"tool,omitempty"`
	Summary string `json:"summary,omitempty"`
}

// Work is what an agent does after a message of its turn, as the message carries it: Steps
// calls so far, Tool the last one's name, Running while its turn goes on and it has not spoken
// again; At when the first began (ms), Ms how long they took once done. From, To: the lines of
// the agent's own record (Data.Session's) they span, To 0 while running.
type Work struct {
	Steps   int    `json:"steps,omitempty"`
	Tool    string `json:"tool,omitempty"`
	Running bool   `json:"running,omitempty"`
	At      int64  `json:"at,omitempty"`
	Ms      int64  `json:"ms,omitempty"`
	From    int64  `json:"from,omitempty"`
	To      int64  `json:"to,omitempty"`
	// Agents: how many of the steps sent a subagent off (the harness's own: Claude's Agent tool).
	Agents int `json:"agents,omitempty"`
}

// Event is something that happened to a channel, kept as a line of it. Type: "join" (an agent
// joined: Member, Nick, Name), "leave" (Member, Nick), "rename" (Member, Nick its new one, From
// its old), "on" (Member, Nick, On: its subscriptions now; "recv" before them, Recv its receive set), "people" (Added, Removed: member strings; Everyone:
// now everyone), "undelivered" (Seq not given to Nick), "stopped" (Seq not passed on: too many
// agents in a row), "silent" (Member, Nick was called for Seq and said nothing in the channel;
// Reason: why, when its turn ended in an error: 余额不足, an API's words…), "recall" (Seq was
// taken back by Member: recall.go). By: who did it, by name.
type Event struct {
	Type     string        `json:"type"`
	Member   string        `json:"member,omitempty"`
	Nick     string        `json:"nick,omitempty"`
	Name     string        `json:"name,omitempty"`
	From     string        `json:"from,omitempty"`
	Recv     []string      `json:"recv,omitempty"`
	On       []api.Trigger `json:"on,omitempty"`
	Added    []string      `json:"added,omitempty"`
	Removed  []string      `json:"removed,omitempty"`
	Everyone bool          `json:"everyone,omitempty"`
	Seq      int64         `json:"seq,omitempty"`
	// Count: for "stopped", how many turns in a row it came to (Reason "idle": of words alone).
	Count  int    `json:"count,omitempty"`
	Reason string `json:"reason,omitempty"`
	By     string `json:"by,omitempty"`
	// What: for "profile" (a card changed: Member's, or the channel's when there is none), the
	// fields that did: "name", "avatar".
	What []string `json:"what,omitempty"`
}

// Message is a channel's message with its data read.
type Message struct {
	store.ChannelMessage
	D Data
}

func read(m store.ChannelMessage) Message {
	out := Message{ChannelMessage: m}
	if m.Data != "" {
		_ = json.Unmarshal([]byte(m.Data), &out.D)
	}
	return out
}

func (d Data) encode() string {
	if d.Session == "" && d.Block == "" && d.From == nil && d.To == 0 && len(d.Files) == 0 && !d.Final && !d.Sent && d.Quote == 0 && d.Card == "" && d.Activity == nil && d.Event == nil && d.Script == "" && !d.Held && !d.Writing && d.Work == nil {
		return ""
	}
	b, _ := json.Marshal(d)
	return string(b)
}

// Member is a member of a channel. Session: the agent's session, if it is one; Nick, what that
// agent is called by, in every channel.
type Member struct {
	store.ChannelMember
	Session string
	Nick    string
}

func (m Member) Agent() bool { return m.Session != "" }

// On are an agent member's subscriptions.
func (m Member) On() []api.Trigger { return TriggersOf(m.Triggers) }

// Direct: the channel is the agent's own session's: what it was given there is in its record as
// the channel's own words, not as a channel's message (every agent's words go where it was
// called, its own channel or another: Server.answer).
func (m Member) Direct() bool { return m.Session != "" && m.Session == m.Channel }

type Service struct {
	st   *store.Store
	ss   Sessions
	logf func(string, ...any)

	// OnRecall, if set, is told a channel before a message of it is taken back (Recall): who
	// keeps which channels have such must know before the store does, or one asking between
	// is told there is none.
	OnRecall func(channel string)

	// Numbered: a nick given by default is the harness's name and a number (claude2), as the
	// tests read them; else its name and four hex digits of its session (Claude-b134).
	Numbered bool

	mu    sync.Mutex
	locks map[string]*sync.Mutex // a session's record is read in by one at a time
	// listen: a session whose agent's turns are heard live (Listen): what its agent says from
	// then on is a message as it is heard (Begin), never made from the record, which only says
	// where each is. since: when its turn began (ms). await: those messages, in order, not yet
	// found in the record. waited: the block of the record that waits for one, and since when.
	listen map[string]bool
	since  map[string]int64
	await  map[string][]int64
	waited map[string]waitedBlock

	jmu sync.Mutex // one agent joins at a time (Join)

	// The members as written, all of them, read once and again after a change (members.go).
	imu   sync.Mutex
	index *index

	added      atomic.Pointer[func(Message)] // OnAdded
	changes    atomic.Pointer[func(Message)] // OnChanged
	listNotify atomic.Pointer[func(Message)] // OnList
}

// OnList has f told whenever a channel's visible message text is created or updated (words
// promoted from KindWork, finalized by Wrote, settled by Revise, or edited) so the sidebar's
// last-message preview stays current.
func (s *Service) OnList(f func(Message)) { s.listNotify.Store(&f) }

func (s *Service) notifyList(m Message) {
	if f := s.listNotify.Load(); f != nil {
		(*f)(m)
	}
}

// OnChanged has f told a message found in its agent's record after it was said live (its
// block known now), apart from what found it.
func (s *Service) OnChanged(f func(Message)) { s.changes.Store(&f) }

func (s *Service) changedMessage(ch string, seq int64) {
	// Told apart from here: this is under the session's lock, and who is told may read the
	// channel.
	if f := s.changes.Load(); f != nil {
		if m, ok := s.Get(ch, seq); ok {
			go (*f)(m)
		}
	}
}

// OnAdded has f told each message as it is added to a channel (not one said a second time).
// It is called where the message is written, maybe under a session's lock: it must not wait.
func (s *Service) OnAdded(f func(Message)) { s.added.Store(&f) }

// index is every membership written: a channel's rows, the channels each session is in beside
// its own, the agents' nicks, and the channels that are no session's.
type index struct {
	rows  map[string][]store.ChannelMember
	in    map[string][]string
	nicks map[string]string
	own   map[string]store.Channel
}

func (s *Service) members() *index {
	s.imu.Lock()
	defer s.imu.Unlock()
	if s.index != nil {
		return s.index
	}
	ix := &index{rows: map[string][]store.ChannelMember{}, in: map[string][]string{}, nicks: map[string]string{}, own: map[string]store.Channel{}}
	all, err := s.st.AllChannelMembers()
	if err != nil {
		s.logf("channel: the members: %v", err)
		return ix
	}
	for _, r := range all {
		ix.rows[r.Channel] = append(ix.rows[r.Channel], r)
		if id, ok := store.SessionOfMember(r.Member); ok && id != r.Channel {
			ix.in[id] = append(ix.in[id], r.Channel)
		}
	}
	if ix.nicks, err = s.st.Nicks(); err != nil {
		s.logf("channel: the nicks: %v", err)
		ix.nicks = map[string]string{}
		return ix
	}
	chs, err := s.st.Channels()
	if err != nil {
		s.logf("channel: the channels: %v", err)
		return ix
	}
	for _, c := range chs {
		ix.own[c.ID] = c
	}
	s.index = ix
	return ix
}

// Subscribers are the agents of every channel that have subscriptions of their own, as written
// (a channel nobody changed the members of keeps its own agent's default: words alone).
func (s *Service) Subscribers() []Member {
	ix := s.members()
	var out []Member
	for _, rows := range ix.rows {
		for _, r := range rows {
			id, ok := store.SessionOfMember(r.Member)
			if !ok || r.Triggers == "" || r.Triggers == "[]" {
				continue
			}
			out = append(out, Member{ChannelMember: r, Session: id, Nick: ix.nicks[id]})
		}
	}
	return out
}

// changed: the members were written; read them again.
func (s *Service) changed() {
	s.imu.Lock()
	s.index = nil
	s.imu.Unlock()
}

func New(st *store.Store, ss Sessions, logf func(string, ...any)) *Service {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Service{st: st, ss: ss, logf: logf, locks: map[string]*sync.Mutex{}, listen: map[string]bool{}, since: map[string]int64{}, await: map[string][]int64{}, waited: map[string]waitedBlock{}}
}

func (s *Service) lock(session string) func() {
	s.mu.Lock()
	l := s.locks[session]
	if l == nil {
		l = &sync.Mutex{}
		s.locks[session] = l
	}
	s.mu.Unlock()
	l.Lock()
	return l.Unlock
}

// Own is channel id, if it is one of its own (no session's).
func (s *Service) Own(id string) (store.Channel, bool) {
	c, ok := s.members().own[id]
	return c, ok
}

// Owns are the channels of their own, oldest first.
func (s *Service) Owns() []store.Channel {
	ix := s.members()
	out := make([]store.Channel, 0, len(ix.own))
	for _, c := range ix.own {
		out = append(out, c)
	}
	slices.SortFunc(out, func(a, b store.Channel) int {
		return cmp.Or(cmp.Compare(a.Created, b.Created), cmp.Compare(a.ID, b.ID))
	})
	return out
}

// In are the channels session is in beside its own, in the order it joined them.
func (s *Service) In(session string) []string { return s.members().in[session] }

// Speaks: session's agent is one of channel's: the channel is its own, or it is in it.
func (s *Service) Speaks(channel, session string) bool {
	if session == "" {
		return false
	}
	if channel == session {
		_, own := s.Own(channel)
		return !own
	}
	return slices.Contains(s.In(session), channel)
}

// Nick is what session's agent is called by; "" if it has none yet (it is in no channel with
// another agent).
func (s *Service) Nick(session string) string { return s.members().nicks[session] }

// Members are channel's members. One nobody has changed has the default: everyone, and (a
// session's) the session it is, which receives everything and speaks in it directly.
func (s *Service) Members(channel string) ([]Member, error) {
	ix := s.members()
	rows := ix.rows[channel]
	if len(rows) == 0 {
		rows = []store.ChannelMember{{Channel: channel, Member: Everyone, Triggers: PersonRow}}
		if _, own := ix.own[channel]; !own {
			rows = append(rows, store.ChannelMember{Channel: channel, Member: store.AgentMember(channel), Triggers: EncodeTriggers(EveryoneOn), Send: store.SendDirect})
		}
	}
	out := make([]Member, len(rows))
	for i, r := range rows {
		out[i] = Member{ChannelMember: r}
		if id, ok := store.SessionOfMember(r.Member); ok {
			out[i].Session, out[i].Nick = id, ix.nicks[id]
		}
	}
	return out, nil
}

// Agents are the sessions of channel's agents, in the order they joined.
func (s *Service) Agents(channel string) []string {
	ms, _ := s.Members(channel)
	var out []string
	for _, m := range ms {
		if m.Agent() {
			out = append(out, m.Session)
		}
	}
	return out
}

// Say appends m, a message said now, to its channel; the agents' records are read in first, so
// that it comes after what they said before it. One with a Ref the channel has is not said twice.
func (s *Service) Say(m Message) (Message, bool, error) {
	if m.Channel == "" {
		return Message{}, false, errors.New("channel: a message of no channel")
	}
	s.Touch(m.Channel)
	return s.add(m)
}

func (s *Service) add(m Message) (Message, bool, error) {
	m.Data = m.D.encode()
	if m.At == 0 {
		m.At = time.Now().UnixMilli()
	}
	seq, added, err := s.st.AddChannelMessage(m.ChannelMessage)
	if err != nil {
		return Message{}, false, err
	}
	if !added {
		old, _, err := s.st.ChannelMessage(m.Channel, seq)
		return read(old), false, err
	}
	m.Seq = seq
	if f := s.added.Load(); f != nil {
		(*f)(m)
	}
	return m, true, nil
}

// Touch reads the record of channel's own agent in, as far as it is written. What it finds is
// not news (nobody is told): it was said before anyone looked.
func (s *Service) Touch(channel string) {
	if _, own := s.Own(channel); own {
		return
	}
	if _, err := s.Sync(channel); err != nil {
		s.logf("channel %s: reading its record in: %v", channel, err)
	}
}

// Sync reads what session's record has past where it was read to into the session's own
// channel: what its agent said, and what a person typed to it outside Iridium. It returns the
// messages that are new to the channel, in order.
func (s *Service) Sync(session string) ([]Message, error) {
	defer s.lock(session)()
	ch := session
	off, pending, had, err := s.st.ChannelSync(session)
	if err != nil {
		return nil, err
	}
	// What the agent did since it last spoke: counted as its record is read, given to what it
	// says next.
	var did activity.Activity
	if pending != "" {
		_ = json.Unmarshal([]byte(pending), &did)
	}
	// Alone: no other agent is in the channel, so what Iridium wrote into the session's record
	// is what was said in the channel. With others in it, that is what the agent was given.
	alone := len(s.Agents(ch)) == 1
	author := store.AgentMember(session)
	var added []Message
	again := false
	for {
		p, err := s.ss.Messages(session, -1, off)
		if err != nil {
			return added, err
		}
		if p.End < off && !again {
			// The record is another, shorter one (the agent cleared it in place): from its start.
			off, again = 0, true
			continue
		}
		for _, b := range p.Blocks {
			var m Message
			var err error
			var isNew bool
			switch b.Kind {
			case api.BlockKindTool:
				did.Add(activity.Call{Name: b.Name, Input: s.input(session, b), Failed: b.Result != nil && b.Result.Error, At: b.Time})
				continue
			case api.BlockKindAssistant:
				var act *activity.Activity
				if !did.Empty() && strings.TrimSpace(b.Text) != "" {
					did.Close(b.Time)
					done := did
					act, did = &done, activity.Activity{}
				}
				if strings.TrimSpace(b.Text) != "" && s.Listening(session) {
					// Heard live: the message is there already, or about to be. The next of them
					// in order is this block's; none yet (a record that has the words before
					// they are heard), it waits here, unless it has waited too long to be one.
					seq, ok := s.awaited(session)
					if !ok && s.waits(session, b.At) {
						if act != nil {
							did = *act
						}
						return added, s.st.SetChannelSync(session, lineOf(b.At), pendingOf(did))
					}
					if ok {
						err = s.anchor(ch, seq, session, b, act)
						break
					}
				}
				m, isNew, err = s.syncSaid(ch, session, author, b, act)
			case api.BlockKindUser:
				m, isNew, err = s.syncUser(ch, session, b, alone, !had)
			default:
				continue
			}
			if err != nil {
				return added, err
			}
			if isNew {
				added = append(added, m)
			}
		}
		if p.End <= off || len(p.Blocks) == 0 {
			off = max(off, p.End)
			break
		}
		off = p.End
	}
	pending = pendingOf(did)
	return added, s.st.SetChannelSync(session, off, pending)
}

// Doing is what session's agent has done since it last spoke.
func (s *Service) Doing(session string) activity.Activity {
	var did activity.Activity
	if _, pending, _, err := s.st.ChannelSync(session); err == nil && pending != "" {
		_ = json.Unmarshal([]byte(pending), &did)
	}
	return did
}

// input is a tool call's whole input.
func (s *Service) input(session string, b api.Block) string {
	if b.Length > len([]rune(b.Input)) {
		if full, err := s.ss.Full(session, b.At); err == nil {
			return full
		}
	}
	return b.Input
}

func (s *Service) whole(session string, b api.Block) string {
	if b.Length > len([]rune(b.Text)) {
		if full, err := s.ss.Full(session, b.At); err == nil {
			return full
		}
	}
	return b.Text
}

func blockRef(session, at string) string { return "b:" + session + ":" + at }

// syncSaid takes in a block the agent said. Said live before the record had it (Heard), it is
// that message, now known by its place.
func (s *Service) syncSaid(ch, session, author string, b api.Block, did *activity.Activity) (Message, bool, error) {
	text := s.whole(session, b)
	if strings.TrimSpace(text) == "" {
		return Message{}, false, nil
	}
	d := Data{Session: session, Block: b.At, Files: b.Files, Activity: did}
	ref := blockRef(session, b.At)
	loose, err := s.st.UnanchoredMessages(ch, author, 8)
	if err != nil {
		return Message{}, false, err
	}
	for _, l := range loose {
		if l.Kind == KindSaid && Same(l.Text, text) {
			old := read(l)
			d.Final, d.Work = old.D.Final, old.D.Work
			if ok, err := s.st.AnchorMessage(ch, l.Seq, ref, d.encode()); err != nil || ok {
				if ok {
					s.changedMessage(ch, l.Seq)
				}
				return Message{}, false, err
			}
		}
	}
	return s.add(Message{ChannelMessage: store.ChannelMessage{Channel: ch, Author: author, Kind: KindSaid, Text: text, Ref: ref, At: b.Time}, D: d})
}

// syncUser takes in a person's block. One Iridium wrote was said in the channel when it was
// sent; it is taken from the record only where the channel is this session alone (what was sent
// before channels were kept, a new session's first message), and never twice.
func (s *Service) syncUser(ch, session string, b api.Block, alone, first bool) (Message, bool, error) {
	ours := b.From != "" || b.FromTerminal != "" || b.FromLogin != ""
	if ours && !alone {
		return Message{}, false, nil
	}
	text := s.whole(session, b)
	if strings.TrimSpace(text) == "" {
		return Message{}, false, nil
	}
	// What the agent was given of another channel it is in: said there, not here.
	if ours && (Given(text) || s.isOtherChannelInput(session, b.ID, text)) {
		return Message{}, false, nil
	}
	ref := b.ID
	if ref == "" {
		ref = blockRef(session, b.At)
		if ours {
			// No id to know it by: by its words, among the latest.
			last, err := s.st.ChannelMessages(ch, 0, -1, 30)
			if err != nil {
				return Message{}, false, err
			}
			for _, l := range last {
				if l.Kind == KindUser && Same(l.Text, text) {
					return Message{}, false, nil
				}
			}
		}
	}
	m := Message{ChannelMessage: store.ChannelMessage{Channel: ch, Kind: KindUser, Text: text, Ref: ref, At: b.Time, Name: b.From},
		D: Data{Session: session, Block: b.At, Files: b.Files}}
	switch {
	case b.FromLogin != "":
		m.Author = b.FromLogin
	case b.FromTerminal != "":
		m.Author = b.FromTerminal
	default:
		m.Author = HostTerminal
	}
	m.Origin = m.Author
	return s.add(m)
}

// Given: text is what an agent was given of a channel (assemble.ChannelTurn or ChannelOverhear):
// the words under their number with the channel's context, or an overheard live message.
func Given(text string) bool {
	return (strings.HasPrefix(text, "#") && strings.Contains(text, "\n<channel-context")) ||
		strings.HasPrefix(text, "<channel-overhear>")
}

// isOtherChannelInput reports whether a user block in session's own transcript was actually
// delivered from another channel (e.g. a brief same-channel turn with no <channel-context> block).
func (s *Service) isOtherChannelInput(session, input, text string) bool {
	if !strings.HasPrefix(text, "#") {
		return false
	}
	if input != "" {
		if in, ok := s.st.InjectionOf(session, input); ok && in.Channel != "" && in.Channel != session {
			return true
		}
	}
	rows, err := s.st.Injections(session, 20)
	if err != nil {
		return false
	}
	for _, in := range rows {
		if in.Channel == "" || in.Channel == session || in.Seq <= 0 {
			continue
		}
		prefix := "#" + strconv.FormatInt(in.Seq, 10)
		if strings.HasPrefix(text, prefix+": ") || strings.HasPrefix(text, prefix+" [") {
			return true
		}
	}
	return false
}

// HostTerminal is the author of what was typed to an agent in its own terminal, outside Iridium.
const HostTerminal = "terminal"

// Heard: session's agent just said text, in a turn that began at since (ms). Its record is read
// in; the message that is text, of this turn and with a number above after (the last one heard),
// is said (written from text itself where the record has not got it yet), and what else came in
// with it is others, in order.
func (s *Service) Heard(session, text string, after, since int64) (others []Message, said Message, err error) {
	ch := session
	author := store.AgentMember(session)
	// Never read in before: what the record has is from before anyone listened, not news.
	if _, _, had, err := s.st.ChannelSync(session); err == nil && !had {
		s.Touch(ch)
	}
	var all []Message
	for try := range 3 {
		if try > 0 {
			time.Sleep(time.Duration(try) * 100 * time.Millisecond)
		}
		added, err := s.Sync(session)
		if err != nil {
			s.logf("channel %s: reading %s in: %v", ch, session, err)
		}
		all = append(all, added...)
		for i := len(all) - 1; i >= 0; i-- {
			if m := all[i]; m.Author == author && m.Kind == KindSaid && Same(m.Text, text) {
				return append(all[:i:i], all[i+1:]...), m, nil
			}
		}
		// Read in a moment ago by someone looking (Touch): it is in the channel, not among the new.
		last, err := s.st.ChannelMessages(ch, 0, -1, 12)
		if err != nil {
			return all, Message{}, err
		}
		for i := len(last) - 1; i >= 0; i-- {
			if l := last[i]; l.Seq > after && (l.At == 0 || l.At >= since) && l.Author == author && l.Kind == KindSaid && !read(l).D.Sent && Same(l.Text, text) {
				return all, read(l), nil
			}
		}
	}
	m, _, err := s.add(Message{ChannelMessage: store.ChannelMessage{Channel: ch, Author: author, Kind: KindSaid, Text: text}, D: Data{Session: session}})
	return all, m, err
}

// Final marks message seq as the last its agent said in the turn.
func (s *Service) Final(m Message) Message {
	if m.D.Final || m.Seq == 0 {
		return m
	}
	m.D.Final = true
	// Read again: it may have been found in the record meanwhile.
	if cur, ok, err := s.st.ChannelMessage(m.Channel, m.Seq); err == nil && ok {
		c := read(cur)
		c.D.Final = true
		m = c
	}
	if err := s.st.SetChannelMessageData(m.Channel, m.Seq, m.D.encode()); err != nil {
		s.logf("channel %s: marking %d: %v", m.Channel, m.Seq, err)
	}
	return m
}

// List is a page of channel's messages (store.ChannelMessages), the agents' records read in first.
func (s *Service) List(channel string, before, after int64, limit int) ([]Message, error) {
	s.Touch(channel)
	rows, err := s.st.ChannelMessages(channel, before, after, limit)
	if err != nil {
		return nil, err
	}
	out := make([]Message, len(rows))
	for i, r := range rows {
		out[i] = read(r)
	}
	return out, nil
}

// Latest is channel's last message that someone said (a person's, an agent's; not Iridium's own
// lines), if any; it does not touch the channel (List does).
func (s *Service) Latest(channel string) (Message, bool) {
	rows, err := s.st.ChannelMessages(channel, 0, -1, 20)
	if err != nil {
		return Message{}, false
	}
	for i := len(rows) - 1; i >= 0; i-- {
		if m := read(rows[i]); m.Kind == KindUser || m.Kind == KindSaid {
			return m, true
		}
	}
	return Message{}, false
}

// Search are channel's messages that have all of words, the latest limit of them, in order.
func (s *Service) Search(channel, words string, limit int) ([]Message, error) {
	s.Touch(channel)
	rows, err := s.st.SearchChannelMessages(channel, words, limit)
	if err != nil {
		return nil, err
	}
	out := make([]Message, len(rows))
	for i, r := range rows {
		out[i] = read(r)
	}
	return out, nil
}

// Get is message seq of channel.
func (s *Service) Get(channel string, seq int64) (Message, bool) {
	m, ok, err := s.st.ChannelMessage(channel, seq)
	if err != nil || !ok || m.Gone {
		return Message{}, false
	}
	return read(m), true
}

// ByRef is the message of channel that is from ref (an input's id, an ask's card).
func (s *Service) ByRef(channel, ref string) (Message, bool) {
	m, ok, err := s.st.ChannelMessageByRef(channel, ref)
	if err != nil || !ok || m.Gone {
		return Message{}, false
	}
	return read(m), true
}

// End is the number of channel's last message.
func (s *Service) End(channel string) int64 {
	n, _ := s.st.ChannelEnd(channel)
	return n
}

// Edit changes the words of message seq; Drop takes it back.
func (s *Service) Edit(channel string, seq int64, text string) error {
	if err := s.st.EditChannelMessage(channel, seq, text); err != nil {
		return err
	}
	if m, ok := s.Get(channel, seq); ok {
		s.notifyList(m)
	}
	return nil
}

func (s *Service) Drop(channel string, seq int64) error {
	return s.st.DropChannelMessage(channel, seq)
}

// Unsay takes back a message that was not sent after all: gone, and the same input sent again
// is a new message.
func (s *Service) Unsay(channel string, seq int64) {
	if err := s.st.ForgetChannelMessage(channel, seq); err != nil {
		s.logf("channel %s: taking %d back: %v", channel, seq, err)
	}
}

// Same: a block's (cut) text and what the agent said are the same message.
func Same(a, b string) bool {
	norm := func(t string) string {
		r := []rune(strings.TrimSpace(transcript.WithoutNameLines(t)))
		if len(r) > 200 {
			r = r[:200]
		}
		return string(r)
	}
	return a != "" && b != "" && norm(a) == norm(b)
}

// Listen: session's agent begins a turn that is heard live, at since (ms). From its first such
// turn on, what it says is a message as it is heard (Begin), and its record only says where
// each is. What the record has by now is read in first, as ever.
func (s *Service) Listen(session string, since int64) {
	if !s.Listening(session) {
		s.Touch(session)
	}
	s.mu.Lock()
	s.listen[session] = true
	s.since[session] = since
	s.mu.Unlock()
}

// Listening: session's agent's turns are heard live (Listen).
func (s *Service) Listening(session string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listen[session]
}

// waitedBlock is a block of a record that waits for the message heard live it is, and since when.
type waitedBlock struct {
	at    string
	since time.Time
}

// waits: block at of session's record, of words not heard yet, may wait for them longer.
func (s *Service) waits(session, at string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	w := s.waited[session]
	if w.at != at {
		w = waitedBlock{at, time.Now()}
		s.waited[session] = w
	}
	return time.Since(w.since) < unheardFor
}

// awaited takes the first message heard live of session's agent that its record has not been
// found to have.
func (s *Service) awaited(session string) (int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.await[session]
	if len(a) == 0 {
		return 0, false
	}
	s.await[session] = a[1:]
	return a[0], true
}

// unheardFor: how long what a record has of an agent heard live may be there without having
// been heard, before it is taken from the record after all.
const unheardFor = time.Minute

// lineOf is the line of a record a block's place ("<line>.<n>") is in.
func lineOf(at string) int64 {
	line, _, _ := strings.Cut(at, ".")
	n, _ := strconv.ParseInt(line, 10, 64)
	return n
}

func pendingOf(did activity.Activity) string {
	if did.Empty() {
		return ""
	}
	b, err := json.Marshal(did)
	if err != nil {
		return ""
	}
	return string(b)
}

// anchor: message seq of ch, heard live, is block b of session's record.
func (s *Service) anchor(ch string, seq int64, session string, b api.Block, did *activity.Activity) error {
	cur, ok, err := s.st.ChannelMessage(ch, seq)
	if err != nil || !ok || cur.Gone {
		return err
	}
	m := read(cur)
	if !m.D.Writing && !Same(m.Text, s.whole(session, b)) {
		s.logf("channel %s: message %d, heard live, is not what its record has next (%s)", ch, seq, b.At)
	}
	m.D.Block, m.D.Files, m.D.Activity = b.At, b.Files, did
	if ok, err := s.st.AnchorMessage(ch, seq, blockRef(session, b.At), m.D.encode()); err != nil || !ok {
		return err
	}
	s.changedMessage(ch, seq)
	return nil
}

// Begin: session's agent, heard live (Listen), says text in its own channel: a message of it
// now; writing: it is still writing it, text what there is so far. after: the number of the last
// message heard from it. Its record may have had these words, and whoever looked read them in,
// before its turn was known to be heard (Listen comes a little late): then the first message of
// this turn that is from the record and was not heard yet is the one (adopted, whole).
func (s *Service) Begin(session, text string, writing bool, after, pre int64) (m Message, adopted bool, err error) {
	// What its record has before these words (what it was asked, typed in its terminal or as a
	// new session's first message) comes before them.
	s.Touch(session)
	unlock := s.lock(session)
	unlocked := false
	defer func() {
		if !unlocked {
			unlock()
		}
	}()
	author := store.AgentMember(session)
	s.mu.Lock()
	since := s.since[session]
	s.mu.Unlock()
	if since > 0 {
		last, err := s.st.ChannelMessages(session, 0, -1, 12)
		if err != nil {
			return Message{}, false, err
		}
		begun := strings.TrimSpace(transcript.WithoutNameLines(text))
		for _, l := range last {
			if l.Seq > after && l.At >= since && l.Author == author && l.Kind == KindSaid && strings.HasPrefix(l.Ref, "b:") && !l.Gone &&
				strings.HasPrefix(strings.TrimSpace(transcript.WithoutNameLines(l.Text)), begun) {
				return read(l), true, nil
			}
		}
	}
	// pre: the message made for it as its turn began (BeginWork), of no words yet and no work:
	// these words are its.
	if pre != 0 {
		if m, ok := s.words(session, pre, text, writing); ok {
			s.mu.Lock()
			s.await[session] = append(s.await[session], m.Seq)
			s.mu.Unlock()
			unlock()
			unlocked = true
			s.notifyList(m)
			return m, false, nil
		}
	}
	m, _, err = s.add(Message{ChannelMessage: store.ChannelMessage{Channel: session, Author: author, Kind: KindSaid, Text: text},
		D: Data{Session: session, Writing: writing}})
	if err == nil && m.Seq != 0 {
		s.mu.Lock()
		s.await[session] = append(s.await[session], m.Seq)
		s.mu.Unlock()
	}
	return m, false, err
}

// words gives message seq of ch, one of no words (KindWork), its words: an agent's words from
// now (KindSaid), text, being written or whole; the work it carried so far stays where it did
// some. With the channel's lock held, or of a channel that is no session's.
func (s *Service) words(ch string, seq int64, text string, writing bool) (Message, bool) {
	m, ok := s.Get(ch, seq)
	if !ok || m.Kind != KindWork {
		return Message{}, false
	}
	if err := s.st.SetChannelMessageKind(ch, seq, KindSaid); err != nil {
		s.logf("channel %s: message %d: %v", ch, seq, err)
		return Message{}, false
	}
	m.Kind = KindSaid
	if err := s.st.EditChannelMessage(ch, seq, text); err != nil {
		s.logf("channel %s: message %d: %v", ch, seq, err)
	}
	m.Text = text
	m.D.Writing = writing
	if m.D.Work != nil && m.D.Work.Steps == 0 {
		m.D.Work = nil
	}
	if err := s.st.SetChannelMessageData(ch, seq, m.D.encode()); err != nil {
		s.logf("channel %s: message %d: %v", ch, seq, err)
	}
	return m, true
}

// Words is words for a message of another channel than its agent's own (a twin).
func (s *Service) Words(ch string, seq int64, text string, writing bool) (Message, bool) {
	unlock := s.lock(ch)
	m, ok := s.words(ch, seq, text, writing)
	unlock()
	if ok {
		s.notifyList(m)
	}
	return m, ok
}

// Forget takes message seq of ch away as one never said (a message made as a turn began, of a
// turn that said and did nothing).
func (s *Service) Forget(ch string, seq int64) {
	if err := s.st.ForgetChannelMessage(ch, seq); err != nil {
		s.logf("channel %s: message %d: %v", ch, seq, err)
	}
}

// Wrote: message seq of channel ch is written whole, text its words.
func (s *Service) Wrote(ch string, seq int64, text string) (Message, bool) {
	unlock := s.lock(ch)
	m, ok := s.Get(ch, seq)
	if !ok {
		unlock()
		return Message{}, false
	}
	if m.Text != text {
		if err := s.st.EditChannelMessage(ch, seq, text); err != nil {
			s.logf("channel %s: message %d: %v", ch, seq, err)
		}
		m.Text = text
	}
	if m.D.Writing {
		m.D.Writing = false
		if err := s.st.SetChannelMessageData(ch, seq, m.D.encode()); err != nil {
			s.logf("channel %s: message %d: %v", ch, seq, err)
		}
	}
	unlock()
	s.notifyList(m)
	return m, true
}

// BeginWork: session's agent is at work in its turn and has said nothing in it: a message of
// no words that carries the work.
func (s *Service) BeginWork(session string, w Work) (Message, error) {
	s.Touch(session)
	m, _, err := s.add(Message{ChannelMessage: store.ChannelMessage{Channel: session, Author: store.AgentMember(session), Kind: KindWork,
		Ref: fmt.Sprintf("w:%s:%d", session, time.Now().UnixNano())}, D: Data{Session: session, Work: &w}})
	return m, err
}

// SetWork changes the work message seq of channel ch carries: f is given what it has (never
// nil) and changes it.
func (s *Service) SetWork(ch string, seq int64, f func(*Work)) (Message, bool) {
	defer s.lock(ch)()
	m, ok := s.Get(ch, seq)
	if !ok {
		return Message{}, false
	}
	if m.D.Work == nil {
		m.D.Work = &Work{}
	}
	f(m.D.Work)
	if err := s.st.SetChannelMessageData(ch, seq, m.D.encode()); err != nil {
		s.logf("channel %s: message %d: %v", ch, seq, err)
	}
	return m, true
}

// SettleLive: nothing is being written and no work runs: what a stop of Iridium left marked so
// is as it was left. Once, as it starts.
func (s *Service) SettleLive() {
	if err := s.st.SettleLiveMessages(); err != nil {
		s.logf("channel: settling what was live: %v", err)
	}
}

// first reads session's record in where it never was: what it has is from before anyone
// listened.
func (s *Service) first(session string) {
	if _, _, had, err := s.st.ChannelSync(session); err == nil && !had {
		s.Touch(session)
	}
}

// Revise changes message seq of channel ch: its words, where text is not nil; its data, by f
// (nil: as it is); and what it is from, where ref is not "".
func (s *Service) Revise(ch string, seq int64, text *string, f func(*Data), ref string) (Message, bool) {
	unlock := s.lock(ch)
	m, ok := s.Get(ch, seq)
	if !ok {
		unlock()
		return Message{}, false
	}
	if text != nil && m.Text != *text {
		if err := s.st.EditChannelMessage(ch, seq, *text); err != nil {
			s.logf("channel %s: message %d: %v", ch, seq, err)
		}
		m.Text = *text
	}
	if f != nil {
		f(&m.D)
	}
	var err error
	if ref != "" && ref != m.Ref {
		if ok, aerr := s.st.AnchorMessage(ch, seq, ref, m.D.encode()); aerr == nil && ok {
			m.Ref = ref
		} else {
			err = cmp.Or(aerr, s.st.SetChannelMessageData(ch, seq, m.D.encode()))
		}
	} else if f != nil {
		err = s.st.SetChannelMessageData(ch, seq, m.D.encode())
	}
	if err != nil {
		s.logf("channel %s: message %d: %v", ch, seq, err)
	}
	unlock()
	if text != nil {
		s.notifyList(m)
	}
	return m, true
}
