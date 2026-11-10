package satori

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
)

// What else goes out as events beside what the agents say: a session added, renamed, moved or
// archived (channel-added, channel-updated, channel-removed); a person's message to a session
// (message-created, _data.kind "user"); a login approved, renamed or removed (login-added,
// login-updated, login-removed: to its app). And the events kept across a restart (Keeper).

// Keeper keeps the outbox across a restart: Keep stores an event, Kept gives those since then,
// oldest first, Drop those before then.
type Keeper interface {
	Keep(session, typ string, data []byte) error
	Kept(since time.Time) ([][]byte, error)
	Drop(before time.Time) error
}

// kept is an outbox entry as stored.
type kept struct {
	SN      int64         `json:"sn"`
	At      int64         `json:"at"`
	Session string        `json:"session,omitempty"`
	Ev      Event         `json:"ev"`
	Files   []api.FileRef `json:"files,omitempty"`
	App     string        `json:"app,omitempty"`
	Login   string        `json:"login,omitempty"`
}

// Keep loads what k kept within Keep and stores each event from now on. Call it once, at the
// start.
func (s *Service) Keep(k Keeper) {
	o := s.out
	if err := k.Drop(time.Now().Add(-Keep)); err != nil {
		s.logf("satori: kept events: %v", err)
	}
	l, err := k.Kept(time.Now().Add(-Keep))
	if err != nil {
		s.logf("satori: kept events: %v", err)
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	var es []entry
	for _, b := range l {
		var x kept
		if json.Unmarshal(b, &x) != nil || x.SN == 0 {
			continue
		}
		es = append(es, entry{sn: x.SN, at: time.UnixMilli(x.At), session: x.Session, ev: x.Ev, files: x.Files, app: x.App, login: x.Login})
	}
	slices.SortFunc(es, func(a, b entry) int { return int(a.sn - b.sn) })
	if n := len(es); n > KeepMax {
		es = es[n-KeepMax:]
	}
	o.list = append(es, o.list...)
	if n := len(es); n > 0 && es[n-1].sn > o.seq {
		o.seq = es[n-1].sn
	}
	o.keep = k
}

// store keeps e (the outbox locked); and drops the old ones now and then.
func (o *outbox) store(e entry, logf func(string, ...any)) {
	if o.keep == nil {
		return
	}
	b, err := json.Marshal(kept{SN: e.sn, At: e.at.UnixMilli(), Session: e.session, Ev: e.ev, Files: e.files, App: e.app, Login: e.login})
	if err == nil {
		err = o.keep.Keep(e.session, e.ev.Type, b)
	}
	if err != nil {
		logf("satori: keep an event: %v", err)
	}
	if time.Since(o.dropped) > time.Minute {
		o.dropped = time.Now()
		if err := o.keep.Drop(time.Now().Add(-Keep)); err != nil {
			logf("satori: drop old events: %v", err)
		}
	}
}

// ---- channels ----

type chanSeen struct {
	name, guild string
	shown       bool
}

type channels struct {
	mu    sync.Mutex
	seen  map[string]chanSeen
	ready bool
}

func shownChannel(ss api.Session) bool { return !ss.Archived && !ss.Subagent }

// Sessions takes the session list as it is now (the hub's "list"): what came, changed or went
// since the last goes out as channel events. The first list only sets where it starts.
func (s *Service) Sessions(l []api.Session) {
	s.ch.mu.Lock()
	defer s.ch.mu.Unlock()
	now := make(map[string]chanSeen, len(l))
	for _, ss := range l {
		c := channelOf(ss)
		now[ss.ID] = chanSeen{name: c.Name, guild: s.guildOf(ss).ID, shown: shownChannel(ss)}
	}
	if s.ch.ready {
		for _, ss := range l {
			was, had := s.ch.seen[ss.ID]
			n := now[ss.ID]
			switch {
			case n.shown && (!had || !was.shown):
				s.emit(ss, Event{Type: "channel-added"}, nil, "")
			case !n.shown && had && was.shown:
				s.emit(ss, Event{Type: "channel-removed"}, nil, "")
			case n.shown && (was.name != n.name || was.guild != n.guild):
				s.emit(ss, Event{Type: "channel-updated"}, nil, "")
			}
		}
		for id, was := range s.ch.seen {
			if _, ok := now[id]; !ok && was.shown {
				// Gone from the list: those that saw it last see it go.
				s.emit(api.Session{ID: id, Name: was.name}, Event{Type: "channel-removed"}, nil, "")
			}
		}
	}
	s.ch.seen, s.ch.ready = now, true
}

// ---- a person's message ----

// Input sends what someone wrote to session ss (its input id, its text) as message-created,
// _data.kind "user", to the logins that see it: the agent's words are not alone in the stream.
func (s *Service) Input(ss api.Session, id, text string, from User, origin string) {
	if id == "" {
		return
	}
	m := Message{ID: id, Content: Escape(s.b.Mask(text)), CreatedAt: time.Now().UnixMilli(), User: &from}
	s.emit(ss, Event{Type: "message-created", Message: &m, User: &from}, nil, "user", origin)
}

// ---- logins ----

// LoginEvent tells app's connections that its login l was added, updated or removed (typ
// login-added, login-updated, login-removed).
func (s *Service) LoginEvent(app, typ string, l LoginInfo) {
	lg := loginOf(app, l, 0, true)
	if typ == "login-removed" {
		off := StatusOffline
		lg.Status = &off
	}
	ev := Event{Type: typ, Timestamp: time.Now().UnixMilli(), Login: &lg, User: lg.User}
	s.out.add(entry{at: time.Now(), ev: ev, app: app, login: l.ID, about: true})
}

// ---- channel.update, channel.delete ----

// channelUpdate renames a session (data.name); the rest of a channel is not its to change.
func (s *Service) channelUpdate(ctx context.Context, c Caller, body []byte) (any, error) {
	var req struct {
		ChannelID string `json:"channel_id"`
		Data      struct {
			Name *string `json:"name"`
		} `json:"data"`
	}
	if err := decode(body, &req); err != nil {
		return nil, err
	}
	if _, err := s.channel(c, req.ChannelID); err != nil {
		return nil, err
	}
	if req.Data.Name == nil {
		return struct{}{}, nil
	}
	return struct{}{}, s.b.Patch(ctx, c, req.ChannelID, api.SessionPatch{Name: req.Data.Name})
}

// channelDelete archives a session: it leaves the list (and its agent stops); nothing is erased.
func (s *Service) channelDelete(ctx context.Context, c Caller, body []byte) (any, error) {
	var req struct {
		ChannelID string `json:"channel_id"`
	}
	if err := decode(body, &req); err != nil {
		return nil, err
	}
	if _, err := s.channel(c, req.ChannelID); err != nil {
		return nil, err
	}
	yes := true
	return struct{}{}, s.b.Patch(ctx, c, req.ChannelID, api.SessionPatch{Archived: &yes})
}

// snSlots: each event goes to each login with an sn of its own, its outbox place times snSlots
// plus the login's turn among those it goes to, so a resume picks up between two of them.
const snSlots = 1024

func fullSN(sn int64, k int) int64 { return sn*snSlots + int64(min(k, snSlots-1)) }
