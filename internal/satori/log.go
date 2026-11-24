package satori

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/kumoSleeping/iridium/internal/activity"
	"github.com/kumoSleeping/iridium/internal/agents"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/channel"
	"github.com/kumoSleeping/iridium/internal/store"
)

// The channels' log (package channel, docs/channels.md) as Satori's messages: message.list and
// message.get read it, a message's id its number in the channel; the events are of what is
// added to it. An agent's record (thinking, tool calls) is not here: internal/block.list.

// agentOf is the agent of session as a user: its id the member it is ("agent:<session>"), its
// name what it runs as now (the harness, the model, the effort), its nick what it is called by.
func (s *Service) agentOf(session string) *User {
	u := User{ID: store.AgentMember(session), IsBot: true}
	st, err := s.b.Session(session)
	if err != nil {
		return &u
	}
	if k, ok := s.b.(interface {
		KnownModels(api.Agent, string) []api.Model
	}); ok && len(st.Models) == 0 {
		n := *st
		n.Models = k.KnownModels(st.Agent, st.Machine)
		st = &n
	}
	u.Name = SessionName(*st)
	u.Nick = s.log.Nick(session)
	// Its face, where it was given one (the backend's to say).
	if a, ok := s.b.(interface{ AgentAvatar(session string) string }); ok {
		u.Avatar = a.AgentAvatar(session)
	}
	return &u
}

// SessionName is the name session st's agent goes by (AgentName): an agent whose models say whose
// they are (agents.Info.ModelNamed: agy) by its model and effort alone, while it has a model.
func SessionName(st api.SessionState) string {
	info, _ := agents.Get(st.Agent)
	harness := cmp.Or(info.Name, string(st.Agent))
	if info.ModelNamed && st.Model != "" {
		harness = ""
	}
	return AgentName(harness, st)
}

// AgentName is the name an agent goes by: its harness, the model it runs and the effort, as far
// as they are known ("Claude Opus 5.5 High"); harness "": the model and the effort.
func AgentName(harness string, st api.SessionState) string {
	var parts []string
	if harness != "" {
		parts = append(parts, harness)
	}
	model := st.Model
	for _, m := range st.Models {
		if (m.Value == st.Model || m.Resolved == st.Model) && m.Name != "" {
			model = m.Name
		}
	}
	if strings.HasPrefix(strings.ToLower(model), "default") {
		model = "" // the harness's own choice: not a model's name
	}
	if model != "" {
		// A harness's name is not said twice ("Claude Claude Opus").
		if harness != "" {
			model = strings.TrimSpace(strings.TrimPrefix(model, harness))
		}
		if model != "" {
			parts = append(parts, model)
		}
	}
	if e := cmp.Or(st.Effort, st.EffortNow); e != "" {
		parts = append(parts, strings.ToUpper(e[:1])+e[1:])
	}
	return strings.Join(parts, " ")
}

// authorOf is who wrote m, as a user. An agent's session since cleared, as the one it goes on
// as: its words are still that member's, its name and face, its card from them (kumo,
// 2026-11-17: they came as a bare agent once its session was cleared).
func (s *Service) authorOf(m channel.Message, agentsSeen map[string]*User) *User {
	if id, ok := store.SessionOfMember(m.Author); ok {
		if u := agentsSeen[id]; u != nil {
			return u
		}
		now := id
		if b, ok := s.b.(interface{ SessionNow(id string) string }); ok {
			now = b.SessionNow(id)
		}
		u := s.agentOf(now)
		if agentsSeen != nil {
			agentsSeen[id] = u
		}
		return u
	}
	if m.Author == NoteUser {
		return &User{ID: NoteUser, Name: "Iridium", IsBot: true}
	}
	if m.D.Script != "" {
		return &User{ID: m.Author, Name: m.D.Script, IsBot: true}
	}
	return &User{ID: m.Author, Name: m.Name}
}

// logContent is m's words as content, without its files.
func (s *Service) logContent(m channel.Message) string {
	out := Escape(s.b.Mask(m.Text))
	if m.D.Quote > 0 {
		// The message it answers, as Satori has it.
		out = `<quote id="` + strconv.FormatInt(m.D.Quote, 10) + `"/>` + out
	}
	return out
}

// logFiles is m's files: those it was kept with, then those an agent's words link (ir:<id>),
// found as it is read, as a page of the agent's record has them (files.Service.AnnotateBlocks):
// what it sent with ir, what it said before it was taken in with them.
func (s *Service) logFiles(m channel.Message) []api.FileRef {
	if m.Kind != channel.KindSaid {
		return m.D.Files
	}
	out := m.D.Files
	for _, f := range s.b.LinkFiles(m.Text) {
		if !slices.ContainsFunc(out, func(g api.FileRef) bool { return g.Ref == f.Ref }) {
			out = append(slices.Clip(out), f)
		}
	}
	return out
}

// fileOwner is the session whose files m's attachments live under: messages sent with ir
// (_data.paths / _data.attachments) attach to the channel they are sent to; replies taken in
// from an agent's transcript keep their files under the agent's own session.
func fileOwner(m channel.Message) string {
	if m.D.Sent {
		return m.Channel
	}
	return cmp.Or(m.D.Session, m.Channel)
}

// logMessage is m as c reads it: its files as c's login may fetch them.
func (s *Service) logMessage(c Caller, m channel.Message, ch *Channel, guild *Guild, seen map[string]*User) Message {
	out := Message{ID: strconv.FormatInt(m.Seq, 10), Content: s.logContent(m), Channel: ch, Guild: guild,
		User: s.authorOf(m, seen), CreatedAt: m.At, UpdatedAt: m.Edited, Data: logData(m)}
	out.Content += fileElements(c.App, c.Login, fileOwner(m), s.logFiles(m))
	// The files' paths only for the pages (ir_web, the host): an app gets the counts.
	out.Content += activityElement(s.placed(m), c.Only)
	return out
}

// placed is what m's agent did before saying it, its files written as at the machine the agent
// runs at ("<machine's name>:<path>"), where that is not Iridium's own.
func (s *Service) placed(m channel.Message) *activity.Activity {
	a := m.D.Activity
	if a == nil || len(a.Files) == 0 || m.D.Session == "" {
		return a
	}
	st, err := s.b.Session(m.D.Session)
	if err != nil || st.Machine == "" {
		return a
	}
	place := st.Machine
	if u, ok := s.b.User(st.Machine); ok && u.Name != "" {
		place = u.Name
	}
	at := a.At(place)
	return &at
}

// activityElement is what an agent did before it said a message, as an element after the
// words (docs/satori.md "活动"): counts as attributes; paths: the files changed too, each a
// child. "" for nothing done.
func activityElement(a *activity.Activity, paths bool) string {
	if a == nil || a.Empty() {
		return ""
	}
	var kv []string
	num := func(k string, n int64) {
		if n != 0 {
			kv = append(kv, k, strconv.FormatInt(n, 10))
		}
	}
	num("bash", int64(a.Bash))
	num("failed", int64(a.Failed))
	if top := a.Top(6); len(top) > 0 {
		kv = append(kv, "cmds", strings.Join(top, ", "))
	}
	num("read", int64(a.Read))
	num("search", int64(a.Search))
	num("fetch", int64(a.Fetch))
	num("agents", int64(a.Agents))
	num("other", int64(a.Other))
	add, del := a.Lines()
	num("files", int64(len(a.Files)))
	num("add", int64(add))
	num("del", int64(del))
	num("ms", a.Ms)
	var kids []Element
	if paths {
		for _, f := range a.Files {
			fk := []string{"path", f.Path}
			if f.Add != 0 {
				fk = append(fk, "add", strconv.Itoa(f.Add))
			}
			if f.Del != 0 {
				fk = append(fk, "del", strconv.Itoa(f.Del))
			}
			kids = append(kids, Tag(Adapter+":file", fk))
		}
	}
	return String([]Element{Tag(Adapter+":activity", kv, kids...)})
}

// LogData is a listed message's own (_data): Kind as the events have it (user, progress, reply,
// ask, error, note); Input, the id of the input a person's message was sent as; Card, an ask's
// card. Where an agent's message is from (docs/channels.md "指向"): Session, the agent's
// session; Block, the block of its record the message is (what it said in a turn); From, To,
// the lines of its record that led to it (what it said with ir). block.list and block.outline
// of that session read them.
type LogData struct {
	Kind    string `json:"kind"`
	Input   string `json:"input,omitempty"`
	Session string `json:"session,omitempty"`
	Block   string `json:"block,omitempty"`
	From    *int64 `json:"from,omitempty"`
	To      int64  `json:"to,omitempty"`
	Card    string `json:"card,omitempty"`
	// Event: what a note tells of (channel.Event).
	Event *channel.Event `json:"event,omitempty"`
	// Quote: the number of the message it answers (its content's <quote id/>).
	Quote int64 `json:"quote,omitempty"`
	// Device: a person's, the device their browser sent it from (api.Device).
	Device string `json:"device,omitempty"`
	// Script: a script of the channel's IR said it (its user "script:<name>").
	Script string `json:"script,omitempty"`
	// Reactions: the emoji put on it, each with who put it on (docs/channels.md "表情").
	Reactions []ReactionSum `json:"reactions,omitempty"`
}

func logData(m channel.Message) LogData {
	d := LogData{Kind: m.Kind, Session: m.D.Session, Block: m.D.Block, From: m.D.From, To: m.D.To, Card: m.D.Card, Event: m.D.Event, Quote: m.D.Quote, Device: m.D.Device, Script: m.D.Script}
	switch m.Kind {
	case channel.KindUser:
		if !strings.HasPrefix(m.Ref, "b:") {
			d.Input = m.Ref
		}
	case channel.KindSaid:
		d.Kind = "progress"
		if m.D.Final {
			d.Kind = "reply"
		}
		if m.D.Sent {
			d.Input = m.Ref // sent with ir: the input it was sent as
		}
	}
	return d
}

// logged is the message of channel ch that id names: its number, or what it is from (an
// input's id, an ask's card).
func (s *Service) logged(ch, id string) (channel.Message, bool) {
	if n, err := strconv.ParseInt(id, 10, 64); err == nil && n > 0 {
		return s.log.Get(ch, n)
	}
	return s.log.ByRef(ch, id)
}

// tell sends message m of the log as message-created (kind: user, progress, reply, error) to
// those who see its channel; denied, on a reply: how many steps auto mode turned down.
func (s *Service) tell(m channel.Message, kind, origin string, denied int) {
	st, err := s.b.Session(m.Channel)
	if err != nil {
		return
	}
	// Its _data as message.list gives it (a person's input, an agent's session and block): a page
	// that hears it live tells it apart as one that reads it later does.
	msg := Message{ID: strconv.FormatInt(m.Seq, 10), Content: s.logContent(m), User: s.authorOf(m, nil), CreatedAt: m.At, Data: logData(m)}
	if denied > 0 {
		msg.Content = Escape(s.b.Mask(strings.TrimSpace(m.Text + fmt.Sprintf("\n\n（auto 拦了 %d 步）", denied))))
	}
	msg.Content += activityElement(m.D.Activity, false)
	s.emitOf(st.Session, fileOwner(m), Event{Type: "message-created", Message: &msg, User: msg.User}, s.logFiles(m), kind, origin)
	// What an agent said in its turn; what it sent with ir went to whom it names as it was sent.
	if s.onSaid != nil && m.Kind == channel.KindSaid && !m.D.Sent {
		s.onSaid(m)
	}
}

// hear: session's agent said text. What its record has that the channel has not is told (a
// person's words typed in the agent's terminal; what the agent said before), then text is held:
// the next thing the agent does tells whether it was its reply.
func (s *Service) hear(session, text, origin string) {
	s.mu.Lock()
	after := s.heard[session]
	// A block's time is the agent's clock, the turn's start ours: a little slack.
	since := s.started[session].Add(-2 * time.Second).UnixMilli()
	s.mu.Unlock()
	others, said, err := s.log.Heard(session, text, after, since)
	if err != nil {
		s.logf("satori: channel of %s: %v", session, err)
		return
	}
	s.flush(session, "progress", origin, 0)
	for _, o := range others {
		kind := "progress"
		if o.Kind == channel.KindUser {
			kind = "user"
		}
		s.tell(o, kind, origin, 0)
	}
	s.mu.Lock()
	s.held[session] = said
	s.heard[session] = said.Seq
	s.mu.Unlock()
}

// flush tells what session's agent said last, as kind (reply: the last of its turn).
func (s *Service) flush(session, kind, origin string, denied int) {
	s.mu.Lock()
	held, ok := s.held[session]
	delete(s.held, session)
	s.mu.Unlock()
	if !ok || held.Seq == 0 {
		return
	}
	if kind == "reply" {
		held = s.log.Final(held)
	} else if cur, ok := s.log.Get(held.Channel, held.Seq); ok {
		held = cur // found in the record meanwhile: its files
	}
	s.tell(held, kind, origin, denied)
}

// noteIn writes a line that is not words into channel ch (session's own, or one its agent is
// in): an ask (ref its card), an error.
func (s *Service) noteIn(ch, session, kind, text, ref, origin string) (channel.Message, bool) {
	m, isNew, err := s.log.Say(channel.Message{ChannelMessage: store.ChannelMessage{Channel: ch, Author: store.AgentMember(session),
		Kind: kind, Text: text, Ref: ref, Origin: origin}, D: channel.Data{Session: session, Card: ref}})
	if err != nil {
		s.logf("satori: channel %s of %s: %v", ch, session, err)
		return channel.Message{}, false
	}
	return m, isNew
}

var errNoMessage = func(id string) error {
	return refuse(http.StatusUnprocessableEntity, "no_such_message", "no message "+id)
}

func (s *Service) messageGet(_ context.Context, c Caller, body []byte) (any, error) {
	var req struct {
		ChannelID string `json:"channel_id"`
		MessageID string `json:"message_id"`
	}
	if err := decode(body, &req); err != nil {
		return nil, err
	}
	st, err := s.channel(c, req.ChannelID)
	if err != nil {
		return nil, err
	}
	// An ask still open: its card, with its buttons.
	if strings.HasPrefix(req.MessageID, "ask:") {
		if e, ok := s.out.message(st.ID, req.MessageID); ok {
			ev := s.render(e, c.App, c.Login)
			m := *ev.Message
			m.Channel, m.Guild = ev.Channel, ev.Guild
			return m, nil
		}
		if m, ok := s.openAsk(st, req.MessageID); ok {
			return m, nil
		}
		return nil, errNoMessage(req.MessageID)
	}
	s.log.Touch(st.ID)
	m, ok := s.logged(st.ID, req.MessageID)
	if !ok {
		return nil, errNoMessage(req.MessageID)
	}
	out := []Message{s.logMessage(c, m, s.channelMasked(st.Session), s.guildOf(st.Session), nil)}
	s.withReactions(c, st.ID, out)
	return out[0], nil
}

// ListLimit is how many messages a page of message.list holds when none is asked; ListMax, at most.
const (
	ListLimit = 50
	ListMax   = 500
)

// messageList pages a channel's log: BidiList, tokens the numbers of messages. Iridium's own
// _data: start and end, the numbers of the page's first and last; more, whether there are
// earlier ones.
func (s *Service) messageList(_ context.Context, c Caller, body []byte) (any, error) {
	var req struct {
		ChannelID string `json:"channel_id"`
		Next      string `json:"next"`
		Direction string `json:"direction"`
		Limit     int    `json:"limit"`
		Order     string `json:"order"`
		Data      struct {
			Q string `json:"q"`
		} `json:"_data"`
	}
	if err := decode(body, &req); err != nil {
		return nil, err
	}
	st, err := s.channel(c, req.ChannelID)
	if err != nil {
		return nil, err
	}
	if q := strings.TrimSpace(req.Data.Q); q != "" {
		// Iridium's own: the messages that have these words, the latest of them; not paged.
		found, err := s.log.Search(st.ID, q, min(max(req.Limit, 0), ListMax))
		if err != nil {
			return nil, err
		}
		out := BidiList[Message]{Data: []Message{}, XData: &PageData{}}
		ch, guild := s.channelMasked(st.Session), s.guildOf(st.Session)
		seen := map[string]*User{}
		for _, m := range found {
			out.Data = append(out.Data, s.logMessage(c, m, ch, guild, seen))
		}
		if req.Order == "desc" {
			slices.Reverse(out.Data)
		}
		s.withReactions(c, st.ID, out.Data)
		return out, nil
	}
	dir := cmp.Or(req.Direction, "before")
	if !slices.Contains([]string{"before", "after", "around"}, dir) {
		return nil, bad("direction: before, after or around")
	}
	if req.Order != "" && req.Order != "asc" && req.Order != "desc" {
		return nil, bad("order: asc or desc")
	}
	at := int64(-1)
	if req.Next != "" {
		n, err := strconv.ParseInt(req.Next, 10, 64)
		if err != nil || n < 0 {
			return nil, bad("next")
		}
		at = n
	} else if dir != "before" {
		return nil, bad("with no next, the direction is before")
	}
	limit := req.Limit
	if limit <= 0 {
		limit = ListLimit
	}
	limit = min(limit, ListMax)
	var before, after []channel.Message
	moreBefore, moreAfter := false, false
	if dir == "before" || dir == "around" {
		// One more than asked: whether there are earlier ones.
		before, err = s.log.List(st.ID, max(at, 0), -1, limit+1)
		if err != nil {
			return nil, err
		}
		if len(before) > limit {
			before, moreBefore = before[1:], true
		}
	}
	if dir == "after" || dir == "around" {
		from := at
		if dir == "around" {
			from = at - 1 // the message itself is on the page
		}
		after, err = s.log.List(st.ID, 0, max(from, 0), limit+1)
		if err != nil {
			return nil, err
		}
		if len(after) > limit {
			after, moreAfter = after[:limit], true
		}
	}
	out := BidiList[Message]{Data: []Message{}, XData: &PageData{}}
	ch, guild := s.channelMasked(st.Session), s.guildOf(st.Session)
	seen := map[string]*User{}
	all := append(before, after...)
	for _, m := range all {
		out.Data = append(out.Data, s.logMessage(c, m, ch, guild, seen))
	}
	if len(all) > 0 {
		out.XData.Start, out.XData.End = all[0].Seq, all[len(all)-1].Seq
		if moreBefore {
			out.Prev = strconv.FormatInt(all[0].Seq, 10)
		}
		if moreAfter {
			out.Next = strconv.FormatInt(all[len(all)-1].Seq, 10)
		}
	} else if at >= 0 {
		out.XData.Start, out.XData.End = at, at
	}
	out.XData.More = moreBefore
	switch dir {
	case "before":
		out.Next = out.Prev
	case "after":
		out.Prev = out.Next
		if dir == "after" && len(all) > 0 {
			out.XData.More = all[0].Seq > 1
		}
	}
	if req.Order == "desc" {
		slices.Reverse(out.Data)
	}
	s.withReactions(c, st.ID, out.Data)
	return out, nil
}
