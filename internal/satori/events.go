package satori

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/channel"
	"github.com/kumoSleeping/iridium/internal/session"
)

// Events (decisions 26, 30): what the agents say goes out as message-created (_data.kind
// progress: a message mid-turn, once another came; reply: the turn's last; ask; error), an ask
// closing as message-updated, a button pressed as interaction/button. Each is kept in the outbox,
// in memory, Keep long; sn is its place there, counting on from the start time in ms, so one from
// before a restart is below what follows. An event goes to each login of the app that sees its
// session, all with its sn. Drafts, states and the like are not sent (Iridium's own ops, 6 and up,
// are left for them).

// Heartbeat: IDENTIFY must come within IdentifyWithin; a connection silent for DeadAfter (the app
// pings every 10 seconds) is closed.
var (
	IdentifyWithin = 10 * time.Second
	DeadAfter      = 60 * time.Second
)

type entry struct {
	sn      int64
	at      time.Time
	session string
	ev      Event         // login left out; a message's content without its files
	files   []api.FileRef // a message's files, written in for each login
	of      string        // the session those files are of; "": session
	// app, login: an event for that login alone, whoever sees what session; login "*":
	// for the app, once a connection (a button it attached pressed); about: one about that login
	// (login-*), for every connection of the app (a login's own: its own only).
	app, login string
	about      bool
	// to: whom of the connections it is for: all, those that take messages live (Identify.Live)
	// or those that do not. passing: one of a message as it changes, of use only till the next:
	// not kept across a restart, and the one before it of the same message is dropped.
	to      int
	passing bool
}

const (
	toAll = iota
	toLive
	toWhole
)

type outbox struct {
	mu   sync.Mutex
	seq  int64
	list []entry
	wake chan struct{}
	// keep: where the events are kept across a restart (Service.Keep), dropped: when the old
	// ones were last dropped there.
	keep    Keeper
	dropped time.Time
	logf    func(string, ...any)
}

func newOutbox(logf func(string, ...any)) *outbox {
	return &outbox{seq: time.Now().UnixMilli(), wake: make(chan struct{}), logf: logf}
}

func (o *outbox) add(e entry) entry {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.seq++
	e.sn, e.ev.SN = o.seq, o.seq
	if e.passing && e.ev.Message != nil {
		for i := len(o.list) - 1; i >= 0 && i >= len(o.list)-64; i-- {
			if l := o.list[i]; l.passing && l.session == e.session && l.ev.Message != nil && l.ev.Message.ID == e.ev.Message.ID {
				o.list = slices.Delete(o.list, i, i+1)
				break
			}
		}
	}
	o.list = append(o.list, e)
	if !e.passing {
		o.store(e, o.logf)
	}
	cut := 0
	for cut < len(o.list) && (len(o.list)-cut > KeepMax || time.Since(o.list[cut].at) > Keep) {
		cut++
	}
	if cut > 0 {
		o.list = append([]entry(nil), o.list[cut:]...)
	}
	close(o.wake)
	o.wake = make(chan struct{})
	return e
}

// after is what came after sn, and a channel closed when more comes.
func (o *outbox) after(sn int64) ([]entry, <-chan struct{}) {
	o.mu.Lock()
	defer o.mu.Unlock()
	i := len(o.list)
	for i > 0 && o.list[i-1].sn > sn && time.Since(o.list[i-1].at) <= Keep {
		i--
	}
	return append([]entry(nil), o.list[i:]...), o.wake
}

func (o *outbox) last() int64 {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.seq
}

// message is the latest message kept with id in session id.
func (o *outbox) message(session, id string) (entry, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	for i := len(o.list) - 1; i >= 0; i-- {
		e := o.list[i]
		if e.session == session && e.ev.Message != nil && e.ev.Message.ID == id {
			return e, true
		}
	}
	return entry{}, false
}

// render is e as login of app sees it: its files written in.
func (s *Service) render(e entry, app, login string) Event {
	ev := e.ev
	if ev.Message != nil {
		m := *ev.Message
		m.Content += fileElements(app, login, cmp.Or(e.of, e.session), e.files)
		ev.Message = &m
	}
	return ev
}

// Notice takes a session's notice (session.Manager.Watch). It does not wait.
func (s *Service) Notice(n session.Notice) {
	s.qmu.Lock()
	s.queue = append(s.queue, noticed{n, time.Now()})
	s.qmu.Unlock()
	select {
	case s.qwake <- struct{}{}:
	default:
	}
}

func (s *Service) work() {
	defer close(s.done)
	for {
		s.qmu.Lock()
		q := s.queue
		s.queue = nil
		s.qmu.Unlock()
		for _, n := range q {
			s.handle(n.Notice, n.at)
		}
		select {
		case <-s.stop:
			return
		case <-s.qwake:
		}
	}
}

// noticed is a notice and when it came.
type noticed struct {
	session.Notice
	at time.Time
}

func (s *Service) handle(n session.Notice, at time.Time) {
	s.mu.Lock()
	held := s.held[n.Session]
	origin := s.origin[n.Session]
	s.mu.Unlock()
	switch n.Kind {
	case session.NoticeTurnStart:
		s.mu.Lock()
		delete(s.held, n.Session)
		s.origin[n.Session] = n.From
		s.started[n.Session] = at
		s.mu.Unlock()
		s.rest(n.Session, origin)
		// Told the turn began first: what is kept of the turn before (the twins of its messages
		// where it was called) is let go before this turn's first message is made.
		if s.onBegan != nil {
			s.onBegan(n.Session)
		}
		if s.log != nil {
			s.log.Listen(n.Session, at.Add(-2*time.Second).UnixMilli())
			s.greet(n.Session, n.From)
		}
	case session.NoticeTaken:
		if s.onTaken != nil {
			s.onTaken(n.Session, n.Text)
		}
		s.regreet(n.Session, origin)
	case session.NoticeUserRecorded:
		s.userRecorded(n.Session, n.Before, n.After, origin)
	case session.NoticeWriting:
		s.writing(n.Session, n.Text, origin)
	case noticeWritten:
		s.written(n.Session, origin)
	case session.NoticeMessage:
		if s.log != nil && s.log.Listening(n.Session) {
			s.say(n.Session, n.Text, origin)
		} else {
			s.hear(n.Session, n.Text, origin)
			s.heardLive(n.Session, origin)
		}
	case session.NoticeTool:
		s.flush(n.Session, "progress", origin, 0)
		s.step(n.Session, n.Tool, origin)
	case session.NoticeTurnEnd:
		if s.log != nil && s.log.Listening(n.Session) {
			// Its reply as the turn's end gives it, where it was not heard said (an agent that
			// tells its last words only so).
			if st := s.sayingOf(n.Session); n.Text != "" && !channel.Same(n.Text, held.Text) && (st.seq != 0 || !channel.Same(n.Text, st.last)) {
				s.say(n.Session, n.Text, origin)
			}
		} else if n.Text != "" && !channel.Same(n.Text, held.Text) {
			s.hear(n.Session, n.Text, origin)
			s.heardLive(n.Session, origin)
		}
		s.flush(n.Session, "reply", origin, n.Denied)
		s.rest(n.Session, origin)
		if s.onTurnEnd != nil {
			s.onTurnEnd(n.Session, n.Text, n.Waiting, n.Background)
		}
	case session.NoticeEnded:
		s.rest(n.Session, origin)
		// Cut short: nothing was its reply.
		if s.onTurnEnd != nil {
			s.onTurnEnd(n.Session, "", n.Waiting, false)
		}
	case session.NoticeAsk:
		st, err := s.b.Session(n.Session)
		if err != nil || n.Ask == nil {
			return
		}
		// Told in its own channel and in every one it is in beside (docs/channels.md "提问"):
		// one card, answered from any of them, closing in all.
		m := s.askMessage(st.Session, *n.Ask)
		m.Channel, m.Guild = nil, nil
		m.CreatedAt = time.Now().UnixMilli()
		m.User = s.agentOf(n.Session)
		for _, ch := range s.askChannels(n.Session) {
			l, noted := s.noteIn(ch, n.Session, channel.KindAsk, Plain(Parse(m.Content), nil), m.ID, n.From)
			if noted {
				// What it asked, kept past its close: drawn as a line of the conversation then.
				if r, ok := s.log.Revise(ch, l.Seq, nil, func(d *channel.Data) { d.Asked = s.askedOf(st.Session.ID, *n.Ask) }, ""); ok {
					l = r
				}
			}
			mc := m
			s.emitTo(toWhole, false, s.channelSession(ch), "", Event{Type: "message-created", Message: &mc, User: mc.User}, nil, "ask", n.From)
			// To a page: the channel's own message of it, under its number, with the ask itself:
			// the page puts it in place of its composer till it is over.
			if noted || l.Seq != 0 {
				lm := m
				lm.ID = strconv.FormatInt(l.Seq, 10)
				d := logData(l)
				d.Ask = s.askData(*n.Ask)
				lm.Data = d
				s.emitTo(toLive, false, s.channelSession(ch), "", Event{Type: "message-created", Message: &lm, User: lm.User}, nil, "ask", n.From)
			}
			if noted && s.onAsk != nil {
				s.onAsk(ch, l.Seq, n.Session, *n.Ask)
			}
		}
	case session.NoticeAskDone:
		card := cardID(n.Session, n.RequestID)
		s.forget(card)
		for _, ch := range s.askChannels(n.Session) {
			ss := s.channelSession(ch)
			e, ok := s.out.message(ss.ID, card)
			if !ok {
				continue
			}
			m := *e.ev.Message
			m.Content = closedContent(m.Content)
			m.UpdatedAt = time.Now().UnixMilli()
			s.emitTo(toWhole, false, ss, "", Event{Type: "message-updated", Message: &m, User: m.User}, nil, "ask", "")
		}
		// To a page: its message of the ask, over (no ask with it).
		for _, ch := range s.askChannels(n.Session) {
			l, ok := s.log.ByRef(ch, card)
			if !ok {
				continue
			}
			ss := s.channelSession(ch)
			lm := Message{ID: strconv.FormatInt(l.Seq, 10), Content: s.logContent(l), User: s.authorOf(l, nil), CreatedAt: l.At, UpdatedAt: time.Now().UnixMilli(), Data: logData(l)}
			s.emitTo(toLive, false, ss, "", Event{Type: "message-updated", Message: &lm, User: lm.User}, nil, "ask", "")
		}
	case session.NoticeError:
		// Told where its asks are (kumo, 2026-11-15): its own channel and each it is in beside,
		// so those who called it see why it stopped.
		for _, ch := range s.askChannels(n.Session) {
			m := Message{ID: "error:" + n.Session + ":" + fmt.Sprint(time.Now().UnixNano()), Content: Escape(s.b.Mask(n.Text)), CreatedAt: time.Now().UnixMilli()}
			m.User = s.agentOf(n.Session)
			if l, ok := s.noteIn(ch, n.Session, channel.KindError, n.Text, "", origin); ok {
				m.ID = strconv.FormatInt(l.Seq, 10)
			}
			s.emit(s.channelSession(ch), Event{Type: "message-created", Message: &m, User: m.User}, nil, "error", origin)
		}
	}
}

// askChannels are the channels session's agent's asks are told in: its own, then each it is in
// beside (docs/channels.md "提问").
func (s *Service) askChannels(session string) []string {
	out := []string{session}
	if s.log != nil {
		out = append(out, s.log.In(session)...)
	}
	return out
}

// channelSession is session as the channel its agent's asks and errors are told in: its own.
func (s *Service) channelSession(session string) api.Session {
	if st, err := s.b.Session(session); err == nil {
		return st.Session
	}
	return api.Session{ID: session}
}

// emit keeps an event of session ss in the outbox, for the connections to send.
func (s *Service) emit(ss api.Session, ev Event, files []api.FileRef, kind string, origin ...string) {
	s.emitOf(ss, "", ev, files, kind, origin...)
}

// emitOf is emit for a message whose files are session of's (the agent that said it; "": ss's).
func (s *Service) emitOf(ss api.Session, of string, ev Event, files []api.FileRef, kind string, origin ...string) {
	ev.Timestamp = time.Now().UnixMilli()
	ev.Channel = s.channelMasked(ss)
	ev.Guild = s.guildOf(ss)
	ev.XType = "iridium"
	ev.XData = &EventData{Kind: kind}
	if len(origin) > 0 {
		ev.XData.Origin = origin[0]
	}
	if kind == "" {
		ev.XData = nil
		ev.XType = ""
	}
	s.out.add(entry{at: time.Now(), session: ss.ID, of: of, ev: ev, files: files})
}

// emitTo is emitOf for some of the connections only (toLive, toWhole); passing as entry's.
func (s *Service) emitTo(to int, passing bool, ss api.Session, of string, ev Event, files []api.FileRef, kind, origin string) {
	ev.Timestamp = time.Now().UnixMilli()
	ev.Channel = s.channelMasked(ss)
	ev.Guild = s.guildOf(ss)
	ev.XType = "iridium"
	ev.XData = &EventData{Kind: kind, Origin: origin}
	s.out.add(entry{at: time.Now(), session: ss.ID, of: of, ev: ev, files: files, to: to, passing: passing})
}

// conn is one app's event stream.
type conn struct {
	ws   *websocket.Conn
	wmu  sync.Mutex
	app  string
	only string // a login's own connection: its events only
	live bool   // Identify.Live
	sns  map[string]int64
	next int64
}

func (cn *conn) send(ctx context.Context, op int, body any) error {
	b, err := json.Marshal(struct {
		Op   int `json:"op"`
		Body any `json:"body,omitempty"`
	}{op, body})
	if err != nil {
		return err
	}
	cn.wmu.Lock()
	defer cn.wmu.Unlock()
	wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return cn.ws.Write(wctx, websocket.MessageText, b)
}

// sn is login's login.sn on this connection.
func (cn *conn) sn(login string) int64 {
	if v, ok := cn.sns[login]; ok {
		return v
	}
	cn.next++
	cn.sns[login] = cn.next
	return cn.next
}

// serveEvents is the WebSocket /v1/events: IDENTIFY (its token the app's TOKEN, unless the
// upgrade came with it; sn to resume), READY with the app's logins, PING/PONG, then EVENTs.
func (s *Service) serveEvents(w http.ResponseWriter, r *http.Request) {
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer ws.CloseNow()
	ws.SetReadLimit(1 << 20)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	c, authed := s.b.Who(r)
	cn := &conn{ws: ws, sns: map[string]int64{}}
	var id Identify
	ictx, icancel := context.WithTimeout(ctx, IdentifyWithin)
	for {
		_, b, err := ws.Read(ictx)
		if err != nil {
			icancel()
			return
		}
		var sig Signal
		if json.Unmarshal(b, &sig) != nil {
			icancel()
			ws.Close(4000, "invalid message")
			return
		}
		if sig.Op == OpPing {
			_ = cn.send(ctx, OpPong, struct{}{})
			continue
		}
		if sig.Op != OpIdentify {
			icancel()
			ws.Close(4000, "IDENTIFY first")
			return
		}
		if len(sig.Body) > 0 && json.Unmarshal(sig.Body, &id) != nil {
			icancel()
			ws.Close(4000, "invalid message")
			return
		}
		break
	}
	icancel()
	if id.Token != "" {
		tc, ok := s.b.AppByToken(r, id.Token)
		// A TOKEN in IDENTIFY wins over what the browser brought along (a cookie, the host's
		// page); only an app's own Authorization must agree with it.
		if !ok || (authed && !c.Only && tc.App != c.App) {
			ws.Close(4004, "invalid token")
			return
		}
		c, authed = tc, true
	}
	if !authed {
		ws.Close(4004, "invalid token")
		return
	}
	cn.app = c.App
	cn.live = id.Live
	if c.Only {
		cn.only = c.Login
	}
	defer s.b.Track(r, c.App, cancel)()
	cursor := fullSN(s.out.last(), snSlots-1)
	if id.SN > 0 {
		cursor = min(id.SN, cursor)
	}
	ready := Ready{Logins: []Login{}, ProxyURLs: []string{}}
	for _, l := range cn.logins(s.b.Logins(c.App)) {
		ready.Logins = append(ready.Logins, loginOf(c.App, l, cn.sn(l.ID), true))
	}
	if err := cn.send(ctx, OpReady, ready); err != nil {
		return
	}
	go s.deliver(ctx, cancel, cn, cursor)
	// WATCH: the states this stream carries now (each WATCH replaces the last).
	stopWatch := func() {}
	defer func() { stopWatch() }()
	watch := func(keys []string) {
		stopWatch()
		if len(keys) == 0 || !c.Only {
			stopWatch = func() {}
			return
		}
		wctx, wcancel := context.WithCancel(ctx)
		stopWatch = wcancel
		go func() {
			err := s.b.Watch(wctx, r, c, keys, func(kind string, data []byte) error {
				return cn.send(wctx, OpState, State{Type: kind, Data: data})
			})
			if err != nil && wctx.Err() == nil {
				cancel()
			}
		}()
	}
	watch(id.Watch)
	for {
		rctx, rcancel := context.WithTimeout(ctx, DeadAfter)
		_, b, err := ws.Read(rctx)
		rcancel()
		if err != nil {
			return
		}
		var sig Signal
		if json.Unmarshal(b, &sig) != nil {
			continue
		}
		switch sig.Op {
		case OpPing:
			if cn.send(ctx, OpPong, struct{}{}) != nil {
				return
			}
		case OpWatch:
			var wb Watch
			if json.Unmarshal(sig.Body, &wb) == nil {
				watch(wb.Keys)
			}
		}
	}
}

// logins is ls, or of them only the connection's own login.
func (cn *conn) logins(ls []LoginInfo) []LoginInfo {
	if cn.only == "" {
		return ls
	}
	return slices.DeleteFunc(ls, func(l LoginInfo) bool { return l.ID != cn.only })
}

// deliver sends the outbox from after done (an sn as sent: fullSN) on: each event to each login
// that sees its session, with an sn of its own.
func (s *Service) deliver(ctx context.Context, cancel func(), cn *conn, done int64) {
	defer cancel()
	for {
		es, wake := s.out.after(done/snSlots - 1)
		if len(es) > 0 {
			logins := cn.logins(s.b.Logins(cn.app))
			for _, e := range es {
				if (e.to == toLive && !cn.live) || (e.to == toWhole && cn.live) {
					done = max(done, fullSN(e.sn, snSlots-1))
					continue
				}
				k := 0
				send := func(l LoginInfo, ev Event) bool {
					sn := fullSN(e.sn, k)
					k++
					if sn <= done {
						return true
					}
					ev.SN = sn
					if err := cn.send(ctx, OpEvent, ev); err != nil {
						return false
					}
					done = sn
					return true
				}
				switch {
				case e.about:
					if e.app == cn.app && (cn.only == "" || cn.only == e.login) {
						ev := e.ev
						lg := *ev.Login
						lg.SN = cn.sn(e.login)
						ev.Login = &lg
						if !send(LoginInfo{ID: e.login}, ev) {
							return
						}
					}
				case e.app != "" && e.login == "*":
					if e.app == cn.app && len(logins) > 0 {
						l := logins[0]
						ev := s.render(e, cn.app, l.ID)
						lg := loginOf(cn.app, l, cn.sn(l.ID), false)
						ev.Login = &lg
						if !send(l, ev) {
							return
						}
					}
				default:
					for _, l := range logins {
						if e.app != "" {
							if e.app != cn.app || e.login != l.ID {
								continue
							}
						} else if !s.b.Sees(Caller{App: cn.app, Login: l.ID, LoginName: l.Name}, e.session) {
							continue
						}
						ev := s.render(e, cn.app, l.ID)
						lg := loginOf(cn.app, l, cn.sn(l.ID), false)
						ev.Login = &lg
						if !send(l, ev) {
							return
						}
					}
				}
				done = max(done, fullSN(e.sn, snSlots-1))
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-wake:
		}
	}
}
