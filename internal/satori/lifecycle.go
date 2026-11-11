package satori

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
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
			// ParentID: its folder's, "" or null take it out from under its launcher (detach).
			ParentID json.RawMessage `json:"parent_id"`
		} `json:"data"`
		// Ext: Iridium's own: archived (with the sessions hung under it), pinned, color, order (the
		// key it was dragged to), workspace (a folder group by hand, 0 none); a folder's color,
		// pinned, order.
		Ext struct {
			Archived  *bool      `json:"archived"`
			Pinned    *bool      `json:"pinned"`
			Color     *api.Color `json:"color"`
			Order     *float64   `json:"order"`
			Workspace *int64     `json:"workspace"`
		} `json:"_data"`
	}
	if err := decode(body, &req); err != nil {
		return nil, err
	}
	x := req.Ext
	if path, ok := strings.CutPrefix(req.ChannelID, FolderPrefix); ok {
		if req.Data.Name != nil || req.Data.ParentID != nil || x.Archived != nil || x.Workspace != nil {
			return nil, bad("a folder takes _data.color, pinned, order")
		}
		if _, err := s.folder(ctx, c, req.ChannelID); err != nil {
			return nil, err
		}
		return struct{}{}, s.b.MarkFolder(c, path, api.FolderPatch{Color: x.Color, Pinned: x.Pinned, Order: x.Order})
	}
	st, err := s.channel(c, req.ChannelID)
	if err != nil {
		return nil, err
	}
	if x.Workspace != nil && *x.Workspace < 0 {
		return nil, bad("_data.workspace")
	}
	p := api.SessionPatch{Name: req.Data.Name, Archived: x.Archived, Pinned: x.Pinned, Color: x.Color, Order: x.Order, Workspace: x.Workspace}
	if raw := req.Data.ParentID; raw != nil {
		var to *string
		if err := json.Unmarshal(raw, &to); err != nil {
			return nil, bad("data.parent_id")
		}
		now := channelOf(st.Session).ParentID
		switch {
		case to != nil && *to == now:
		case st.Launcher != "" && !st.Own && (to == nil || *to == "" || *to == FolderPrefix+groupPath(st.Session)):
			yes := true
			p.Detach = &yes
		default:
			return nil, refuse(http.StatusUnprocessableEntity, "parent_fixed", "data.parent_id: a session only comes out from under its launcher (its folder's, or null)")
		}
	}
	if p == (api.SessionPatch{}) {
		return struct{}{}, nil
	}
	return struct{}{}, s.b.Patch(ctx, c, st.ID, p)
}

// channelDelete archives a session: it leaves the list (and its agent stops); nothing is erased.
func (s *Service) channelDelete(ctx context.Context, c Caller, body []byte) (any, error) {
	var req struct {
		ChannelID string `json:"channel_id"`
	}
	if err := decode(body, &req); err != nil {
		return nil, err
	}
	st, err := s.channel(c, req.ChannelID)
	if err != nil {
		return nil, err
	}
	yes := true
	return struct{}{}, s.b.Patch(ctx, c, st.ID, api.SessionPatch{Archived: &yes})
}

// snSlots: each event goes to each login with an sn of its own, its outbox place times snSlots
// plus the login's turn among those it goes to, so a resume picks up between two of them.
const snSlots = 1024

func fullSN(sn int64, k int) int64 { return sn*snSlots + int64(min(k, snSlots-1)) }

// channelCreate starts a session: guild_id its folder group (loose or
// none: as its folder falls), data.name its name, data.parent_id the session it hangs under (none:
// the caller's own, if it speaks from one), _data the agent's start (agent, cwd, text, ...). The
// channel it gives is the session as channel.get gives it.
func (s *Service) channelCreate(ctx context.Context, c Caller, body []byte) (any, error) {
	var req struct {
		GuildID string `json:"guild_id"`
		Data    struct {
			Name     string `json:"name"`
			ParentID string `json:"parent_id"`
		} `json:"data"`
		Ext api.NewSession `json:"_data"`
	}
	if err := decode(body, &req); err != nil {
		return nil, err
	}
	n := req.Ext
	n.Workspace = 0
	g, err := s.guildID(c, req.GuildID)
	if err != nil {
		return nil, err
	}
	if g != "" && g != Loose {
		if !s.guildThere(c, g) {
			return nil, refuse(http.StatusUnprocessableEntity, "no_such_guild", "no guild "+g)
		}
		n.Workspace, _ = strconv.ParseInt(g, 10, 64)
	}
	if p := req.Data.ParentID; p != "" {
		// A session here it sees, by its id's start; else as given (an agent's own session id, as
		// its ir has it).
		n.Caller = p
		if id, err := s.sessionOf(c, p); err == nil && s.b.Sees(c, id) {
			n.Caller = id
		}
	}
	id, err := s.b.Create(ctx, c, n)
	if err != nil {
		return nil, err
	}
	if req.Data.Name != "" {
		name := req.Data.Name
		if err := s.b.Patch(ctx, c, id, api.SessionPatch{Name: &name}); err != nil {
			return nil, err
		}
	}
	st, err := s.b.Session(id)
	if err != nil {
		return Channel{ID: id, Type: ChannelText, Name: req.Data.Name}, nil
	}
	return s.channelFull(st.Session), nil
}
