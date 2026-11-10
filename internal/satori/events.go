package satori

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/session"
	"github.com/kumoSleeping/iridium/internal/transcript"
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
}

type outbox struct {
	mu   sync.Mutex
	seq  int64
	list []entry
	wake chan struct{}
}

func newOutbox() *outbox {
	return &outbox{seq: time.Now().UnixMilli(), wake: make(chan struct{})}
}

func (o *outbox) add(e entry) entry {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.seq++
	e.sn, e.ev.SN = o.seq, o.seq
	o.list = append(o.list, e)
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
		m.Content += fileElements(app, login, e.session, e.files)
		ev.Message = &m
	}
	return ev
}

// Notice takes a session's notice (session.Manager.Watch). It does not wait.
func (s *Service) Notice(n session.Notice) {
	s.qmu.Lock()
	s.queue = append(s.queue, n)
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
			s.handle(n)
		}
		select {
		case <-s.stop:
			return
		case <-s.qwake:
		}
	}
}

func (s *Service) handle(n session.Notice) {
	s.mu.Lock()
	held := s.held[n.Session]
	origin := s.origin[n.Session]
	s.mu.Unlock()
	switch n.Kind {
	case session.NoticeTurnStart:
		s.mu.Lock()
		delete(s.held, n.Session)
		s.origin[n.Session] = n.From
		s.started[n.Session] = time.Now()
		s.mu.Unlock()
	case session.NoticeMessage:
		if held.text != "" {
			s.said(n.Session, held, "progress", origin)
		}
		h := said{text: n.Text}
		h.msg, h.files = s.resolve(n.Session, n.Text)
		s.mu.Lock()
		s.held[n.Session] = h
		s.mu.Unlock()
	case session.NoticeTool:
		if held.text != "" {
			s.said(n.Session, held, "progress", origin)
		}
		s.mu.Lock()
		delete(s.held, n.Session)
		s.mu.Unlock()
	case session.NoticeTurnEnd:
		s.mu.Lock()
		delete(s.held, n.Session)
		s.mu.Unlock()
		text := n.Text
		if text == "" || same(text, held.text) {
			text = held.text
		} else {
			held = said{text: text}
			held.msg, held.files = s.resolve(n.Session, text)
		}
		if n.Denied > 0 {
			held.text = strings.TrimSpace(text + fmt.Sprintf("\n\n（auto 拦了 %d 步）", n.Denied))
		}
		if held.text != "" {
			s.said(n.Session, held, "reply", origin)
		}
	case session.NoticeAsk:
		st, err := s.b.Session(n.Session)
		if err != nil || n.Ask == nil {
			return
		}
		m := s.askMessage(st.Session, *n.Ask)
		m.Channel, m.Guild = nil, nil
		m.CreatedAt = time.Now().UnixMilli()
		s.emit(st.Session, Event{Type: "message-created", Message: &m, User: m.User}, nil, "ask", n.From)
	case session.NoticeAskDone:
		card := cardID(n.Session, n.RequestID)
		s.forget(card)
		e, ok := s.out.message(n.Session, card)
		if !ok {
			return
		}
		m := *e.ev.Message
		m.Content = closedContent(m.Content)
		m.UpdatedAt = time.Now().UnixMilli()
		ss := api.Session{ID: n.Session}
		if st, err := s.b.Session(n.Session); err == nil {
			ss = st.Session
		}
		s.emit(ss, Event{Type: "message-updated", Message: &m, User: m.User}, nil, "ask", "")
	case session.NoticeError:
		ss := api.Session{ID: n.Session}
		if st, err := s.b.Session(n.Session); err == nil {
			ss = st.Session
		}
		m := Message{ID: "error:" + n.Session + ":" + fmt.Sprint(time.Now().UnixNano()), Content: Escape(s.b.Mask(n.Text)), CreatedAt: time.Now().UnixMilli()}
		if u, ok := agentUser(ss.Agent); ok {
			m.User = &u
		}
		s.emit(ss, Event{Type: "message-created", Message: &m, User: m.User}, nil, "error", origin)
	}
}

// said sends what the agent said: its block's message if found, else one by itself.
func (s *Service) said(id string, h said, kind, origin string) {
	st, err := s.b.Session(id)
	if err != nil {
		return
	}
	var m Message
	var files []api.FileRef
	if h.msg != nil {
		m = *h.msg
	} else {
		m = Message{ID: fmt.Sprintf("live:%s:%d", id, time.Now().UnixNano()), CreatedAt: time.Now().UnixMilli()}
		if u, ok := agentUser(st.Agent); ok {
			m.User = &u
		}
	}
	m.Content = Escape(s.b.Mask(h.text))
	m.Channel, m.Guild = nil, nil
	if h.msg != nil {
		files = h.files
	}
	s.emit(st.Session, Event{Type: "message-created", Message: &m, User: m.User}, files, kind, origin)
}

// emit keeps an event of session ss in the outbox, for the connections to send.
func (s *Service) emit(ss api.Session, ev Event, files []api.FileRef, kind string, origin ...string) {
	ev.Timestamp = time.Now().UnixMilli()
	ev.Channel = channelOf(ss)
	ev.Guild = s.guildOf(ss)
	ev.XType = "iridium"
	ev.XData = &EventData{Kind: kind}
	if len(origin) > 0 {
		ev.XData.Origin = origin[0]
	}
	if kind == "" && ev.Type == "interaction/button" {
		ev.XData = nil
		ev.XType = ""
	}
	s.out.add(entry{at: time.Now(), session: ss.ID, ev: ev, files: files})
}

// resolve is the transcript's message for what the agent just said, if it is there: the last
// assistant block saying text, not from before the turn began; and its files.
func (s *Service) resolve(id, text string) (*Message, []api.FileRef) {
	s.mu.Lock()
	since := s.started[id].Add(-2 * time.Second)
	s.mu.Unlock()
	for try := range 3 {
		if try > 0 {
			select {
			case <-s.stop:
				return nil, nil
			case <-time.After(time.Duration(try) * 100 * time.Millisecond):
			}
		}
		p, err := s.b.Messages(id, -1, -1)
		if err != nil {
			return nil, nil
		}
		for i := len(p.Blocks) - 1; i >= 0; i-- {
			b := p.Blocks[i]
			if b.Kind != api.BlockKindAssistant || (b.Time != 0 && b.Time < since.UnixMilli()) {
				continue
			}
			if same(b.Text, text) {
				st, err := s.b.Session(id)
				if err != nil {
					return nil, nil
				}
				m := s.messageOf(Caller{}, st.Session, b, nil)
				return &m, b.Files
			}
		}
	}
	return nil, nil
}

// same: a block's (cut) text and what the agent said are the same message.
func same(a, b string) bool {
	norm := func(t string) string {
		r := []rune(strings.TrimSpace(transcript.WithoutNameLines(t)))
		if len(r) > 200 {
			r = r[:200]
		}
		return string(r)
	}
	return a != "" && b != "" && norm(a) == norm(b)
}

// conn is one app's event stream.
type conn struct {
	ws   *websocket.Conn
	wmu  sync.Mutex
	app  string
	only string // a login's own connection: its events only
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
	if c.Only {
		cn.only = c.Login
	}
	defer s.b.Track(r, c.App, cancel)()
	cursor := s.out.last()
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

// deliver sends the outbox from after cursor on, to each login that sees each event's session.
func (s *Service) deliver(ctx context.Context, cancel func(), cn *conn, cursor int64) {
	defer cancel()
	for {
		es, wake := s.out.after(cursor)
		if len(es) > 0 {
			logins := cn.logins(s.b.Logins(cn.app))
			for _, e := range es {
				for _, l := range logins {
					if !s.b.Sees(Caller{App: cn.app, Login: l.ID, LoginName: l.Name}, e.session) {
						continue
					}
					ev := s.render(e, cn.app, l.ID)
					lg := loginOf(cn.app, l, cn.sn(l.ID), false)
					ev.Login = &lg
					if err := cn.send(ctx, OpEvent, ev); err != nil {
						return
					}
				}
				cursor = e.sn
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-wake:
		}
	}
}
