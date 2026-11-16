// Package channel is the channels (docs/channels.md): where people and agents talk. A channel
// keeps what was said in it, a message a number, and who is in it; an agent's own record (its
// thinking, its tool calls) stays the agent's. What an agent said is read in from that record
// (Sync), what a person said is written as it is sent (Say). A session has a channel of its own,
// under the session's id: what its agent says in its turns is said there. A session may also be
// in other channels (a subscription, members.go): there it sees only what is said, and what it
// says there is its turn's reply, said for it, or what it sends with ir. A channel may be no
// session's (store.Channel): then all its agents are such.
package channel

import (
	"cmp"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kumoSleeping/iridium/internal/activity"
	"github.com/kumoSleeping/iridium/internal/api"
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
)

// What a member receives. An agent: RecvAll, what people say (and what names it); RecvAt, only
// what names it. A person: RecvAll, everything said; RecvMine, what is theirs.
const (
	RecvAll  = "all"
	RecvAt   = "at"
	RecvMine = "mine"
)

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
	// stretch of its own record (Session's) that led to it: the lines it spans. From 0: not
	// known where it began.
	From int64 `json:"from,omitempty"`
	To   int64 `json:"to,omitempty"`
	// Final: the last thing the agent said in its turn.
	Final bool `json:"final,omitempty"`
	// Sent: an agent's words said with ir, or for it as its turn ended (then Final), not read
	// from its record: given to whom they name as they were sent, not once more as said.
	Sent bool `json:"sent,omitempty"`
	// Quote: the message it answers.
	Quote int64 `json:"quote,omitempty"`
	// Card: an ask's card id.
	Card string `json:"card,omitempty"`
	// Event: what a line of Iridium's own tells of (a member joined, left…), for a page to say
	// in its own words; the message's text says it too.
	Event *Event `json:"event,omitempty"`
	// Activity: what the agent did between the last thing it said and this.
	Activity *activity.Activity `json:"activity,omitempty"`
}

// Event is something that happened to a channel, kept as a line of it. Type: "join" (an agent
// joined: Member, Nick, Name), "leave" (Member, Nick), "rename" (Member, Nick its new one, From
// its old), "recv" (Member, Nick, Recv), "people" (Added, Removed: member strings; Everyone:
// now everyone), "undelivered" (Seq not given to Nick), "stopped" (Seq not passed on: too many
// agents in a row), "silent" (Member, Nick was called for Seq and said nothing in the channel;
// Reason: why, when its turn ended in an error: 余额不足, an API's words…). By: who did it, by name.
type Event struct {
	Type     string   `json:"type"`
	Member   string   `json:"member,omitempty"`
	Nick     string   `json:"nick,omitempty"`
	Name     string   `json:"name,omitempty"`
	From     string   `json:"from,omitempty"`
	Recv     string   `json:"recv,omitempty"`
	Added    []string `json:"added,omitempty"`
	Removed  []string `json:"removed,omitempty"`
	Everyone bool     `json:"everyone,omitempty"`
	Seq      int64    `json:"seq,omitempty"`
	Reason   string   `json:"reason,omitempty"`
	By       string   `json:"by,omitempty"`
	// What: for "profile" (a card changed: Member's, or the channel's when there is none), the
	// fields that did: "name", "intro", "avatar", "background".
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
	if d.Session == "" && d.Block == "" && d.From == 0 && d.To == 0 && len(d.Files) == 0 && !d.Final && !d.Sent && d.Quote == 0 && d.Card == "" && d.Activity == nil && d.Event == nil {
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

// Direct: the agent's turns are said in the channel (it is the session's own).
func (m Member) Direct() bool { return m.Send == store.SendDirect }

type Service struct {
	st   *store.Store
	ss   Sessions
	logf func(string, ...any)

	// Numbered: a nick given by default is the harness's name and a number (claude2), as the
	// tests read them; else its name and four hex digits of its session (Claude-b134).
	Numbered bool

	mu    sync.Mutex
	locks map[string]*sync.Mutex // a session's record is read in by one at a time

	jmu sync.Mutex // one agent joins at a time (Join)

	// The members as written, all of them, read once and again after a change (members.go).
	imu   sync.Mutex
	index *index

	added atomic.Pointer[func(Message)] // OnAdded
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
	return &Service{st: st, ss: ss, logf: logf, locks: map[string]*sync.Mutex{}}
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
	slices.SortFunc(out, func(a, b store.Channel) int { return cmp.Or(cmp.Compare(a.Created, b.Created), cmp.Compare(a.ID, b.ID)) })
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
		rows = []store.ChannelMember{{Channel: channel, Member: Everyone, Recv: RecvAll}}
		if _, own := ix.own[channel]; !own {
			rows = append(rows, store.ChannelMember{Channel: channel, Member: store.AgentMember(channel), Recv: RecvAll, Send: store.SendDirect})
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
	pending = ""
	if !did.Empty() {
		if b, err := json.Marshal(did); err == nil {
			pending = string(b)
		}
	}
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
			d.Final = old.D.Final
			if ok, err := s.st.AnchorMessage(ch, l.Seq, ref, d.encode()); err != nil || ok {
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
	if ours && Given(text) {
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

// Given: text is what an agent was given of a channel (assemble.ChannelTurn): the words under
// their number, then the channel's context.
func Given(text string) bool {
	return strings.HasPrefix(text, "#") && strings.Contains(text, "\n<channel-context")
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
	return s.st.EditChannelMessage(channel, seq, text)
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
