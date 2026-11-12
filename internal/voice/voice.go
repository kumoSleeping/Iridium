// Package voice is a folder group's voice: the people in the group's room hear each other. One
// WebSocket a member (docs/voice.md): binary frames are audio, which the room passes on to the
// others as they came, never decoded; text frames are its signals (who is in, muted, the clock).
//
// It rides TCP (a tunnel has no UDP), where a frame is never lost, only late: so nothing is sent
// twice or repaired, and what piles up is dropped, oldest first, here and at the listener.
package voice

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
)

const (
	// Header is a frame's head as a member sends it: kind, flags, seq (2), the time it was caught
	// in the room's clock (4, milliseconds, wrapping). The room puts the sender's number (2)
	// before it.
	Header   = 8
	KindOpus = 1

	// MaxFrame is the longest frame taken: 120 ms of Opus at its highest rate is under it.
	MaxFrame = 1500
	// MaxMembers a room holds.
	MaxMembers = 16
	// Backlog is how many frames wait for a slow listener before the oldest go: some seconds of
	// the others speaking at once (20 ms a frame at the highest quality), more than it can play
	// in time anyway.
	Backlog = 256
	// DeadAfter with no frame of any kind (a member pings every few seconds), the member is gone.
	DeadAfter = 30 * time.Second

	writeWithin = 10 * time.Second
	// rate and burst bound a member's frames: 2.5 ms frames are 400 a second, nobody needs more.
	rate, burst = 100, 200
)

// ErrFull: the room has MaxMembers.
var ErrFull = errors.New("voice: the room is full")

// Who is a member as the others see it.
type Who struct {
	App   string `json:"app"`
	Login string `json:"login,omitempty"`
	Name  string `json:"name"`
}

// Member is one connection in a room.
type Member struct {
	ID uint16 `json:"id"`
	Who
	Muted bool `json:"muted"`
}

// Stats is what a member measured, said as it leaves and now and then: logged, for the tests on
// real networks (docs/voice.md "测").
type Stats struct {
	RTT      float64 `json:"rtt"`      // round trip to the room, median, ms
	NetP50   float64 `json:"netP50"`   // from caught to arrived, ms
	NetP95   float64 `json:"netP95"`   //
	NetMax   float64 `json:"netMax"`   //
	Buffer   float64 `json:"buffer"`   // waited in the listener's buffer, mean, ms
	Frames   int     `json:"frames"`   // frames heard
	Late     int     `json:"late"`     // dropped at the listener: too far behind
	Gaps     int     `json:"gaps"`     // times a speaker's queue ran dry: the ends of speech, and the gaps within it
	Skipped  int     `json:"skipped"`  // pauses let go at the listener, to catch up
	Unsent   int     `json:"unsent"`   // not sent: the way up was full
	Stalls   int     `json:"stalls"`   // gaps over 500 ms within a member's speech
	Seconds  float64 `json:"seconds"`  // what the numbers cover
	Path     string  `json:"path"`     // ws
	Codec    string  `json:"codec"`    // opus@24000/40 ns=rnnoise agc=1
	Platform string  `json:"platform"` // the browser, short
}

// signal is a text frame, either way.
type signal struct {
	Op      string   `json:"op"`
	T       float64  `json:"t,omitempty"`       // ping, pong: the member's clock, back as it came
	Now     float64  `json:"now,omitempty"`     // pong, hello: the room's clock, ms
	Self    uint16   `json:"self,omitempty"`    // hello
	Muted   *bool    `json:"muted,omitempty"`   // mute
	Members []Member `json:"members,omitempty"` // hello, members
	Stats   *Stats   `json:"stats,omitempty"`   // stats
}

// Rooms are the rooms with someone in them, by channel.
type Rooms struct {
	logf func(string, ...any)
	now  func() time.Time
	// Changed, if set, is told a room's members as they change (none: the room is gone).
	Changed func(room string, members []Member)

	mu    sync.Mutex
	rooms map[string]*room
}

// New is no rooms.
func New(logf func(string, ...any)) *Rooms {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Rooms{logf: logf, now: time.Now, rooms: map[string]*room{}}
}

type room struct {
	members map[uint16]*member
	next    uint16
}

type member struct {
	Member
	audio   chan []byte // to this member; the oldest dropped when full
	ctl     chan []byte // signals, in order, never dropped
	dropped int
}

// Members are room's members, by number.
func (rs *Rooms) Members(id string) []Member {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return rs.list(rs.rooms[id])
}

func (rs *Rooms) list(r *room) []Member {
	out := []Member{}
	if r == nil {
		return out
	}
	for i := uint16(1); i <= r.next; i++ {
		if m := r.members[i]; m != nil {
			out = append(out, m.Member)
		}
	}
	return out
}

// clock is the room's clock: milliseconds, as a frame's head carries their low 32 bits.
func (rs *Rooms) clock() float64 { return float64(rs.now().UnixMicro()) / 1000 }

func (rs *Rooms) join(id string, who Who) (*member, error) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	r := rs.rooms[id]
	if r == nil {
		r = &room{members: map[uint16]*member{}}
		rs.rooms[id] = r
	}
	if len(r.members) >= MaxMembers {
		return nil, ErrFull
	}
	r.next++
	m := &member{Member: Member{ID: r.next, Who: who}, audio: make(chan []byte, Backlog), ctl: make(chan []byte, 32)}
	r.members[m.ID] = m
	rs.tell(id, r, m)
	return m, nil
}

func (rs *Rooms) leave(id string, m *member) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	r := rs.rooms[id]
	if r == nil || r.members[m.ID] != m {
		return
	}
	delete(r.members, m.ID)
	if len(r.members) == 0 {
		delete(rs.rooms, id)
	}
	rs.tell(id, r, nil)
}

// tell says the room's members to each of them (to fresh, with its own number, as its hello).
// Under rs.mu.
func (rs *Rooms) tell(id string, r *room, fresh *member) {
	l := rs.list(r)
	all, _ := json.Marshal(signal{Op: "members", Members: l})
	for _, m := range r.members {
		b := all
		if m == fresh {
			b, _ = json.Marshal(signal{Op: "hello", Self: m.ID, Now: rs.clock(), Members: l})
		}
		select {
		case m.ctl <- b:
		default: // not reading its signals: its writer will find the connection dead
		}
	}
	if rs.Changed != nil {
		rs.Changed(id, l)
	}
}

// pass gives from's frame to every other member of the room, its number before it.
func (rs *Rooms) pass(id string, from *member, frame []byte) {
	b := make([]byte, 2+len(frame))
	binary.BigEndian.PutUint16(b, from.ID)
	copy(b[2:], frame)
	rs.mu.Lock()
	defer rs.mu.Unlock()
	r := rs.rooms[id]
	if r == nil {
		return
	}
	for _, m := range r.members {
		if m == from {
			continue
		}
		for {
			select {
			case m.audio <- b:
			default:
				// Full: the oldest goes, the newest is what is worth hearing.
				select {
				case <-m.audio:
					m.dropped++
				default:
				}
				continue
			}
			break
		}
	}
}

// Serve is one member's connection to room id, till it ends. The caller has checked that who may
// be there.
func (rs *Rooms) Serve(w http.ResponseWriter, r *http.Request, id string, who Who) {
	// The page's Origin is checked before the core (package webapp); an app has none.
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true, CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return
	}
	defer ws.CloseNow()
	ws.SetReadLimit(4096)
	m, err := rs.join(id, who)
	if err != nil {
		ws.Close(4008, "full")
		return
	}
	defer rs.leave(id, m)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go func() {
		defer cancel()
		for {
			var b []byte
			kind := websocket.MessageBinary
			// A signal first, if one waits: who is in must not sit behind audio.
			select {
			case b = <-m.ctl:
				kind = websocket.MessageText
			default:
				select {
				case <-ctx.Done():
					return
				case b = <-m.ctl:
					kind = websocket.MessageText
				case b = <-m.audio:
				}
			}
			wctx, wcancel := context.WithTimeout(ctx, writeWithin)
			err := ws.Write(wctx, kind, b)
			wcancel()
			if err != nil {
				return
			}
		}
	}()
	tokens, last := float64(burst), rs.now()
	for {
		rctx, rcancel := context.WithTimeout(ctx, DeadAfter)
		kind, b, err := ws.Read(rctx)
		rcancel()
		if err != nil {
			return
		}
		if kind == websocket.MessageBinary {
			now := rs.now()
			tokens = min(burst, tokens+now.Sub(last).Seconds()*rate)
			last = now
			if tokens < 1 || len(b) <= Header || len(b) > MaxFrame || b[0] != KindOpus || rs.muted(id, m) {
				continue
			}
			tokens--
			rs.pass(id, m, b)
			continue
		}
		var s signal
		if json.Unmarshal(b, &s) != nil {
			continue
		}
		switch s.Op {
		case "ping":
			pong, _ := json.Marshal(signal{Op: "pong", T: s.T, Now: rs.clock()})
			select {
			case m.ctl <- pong:
			default:
				return
			}
		case "mute":
			if s.Muted != nil {
				rs.mute(id, m, *s.Muted)
			}
		case "stats":
			if st := s.Stats; st != nil {
				rs.logf("voice: %s %q in %.8s: rtt %.0f net %.0f/%.0f/%.0f buffer %.0f frames %d late %d gaps %d skipped %d unsent %d stalls %d dropped %d over %.0fs %.24s %.60s %.40s",
					who.App, who.Name, id, st.RTT, st.NetP50, st.NetP95, st.NetMax, st.Buffer, st.Frames, st.Late, st.Gaps, st.Skipped, st.Unsent, st.Stalls, rs.droppedOf(m), st.Seconds, st.Path, st.Codec, st.Platform)
			}
		}
	}
}

func (rs *Rooms) muted(id string, m *member) bool {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return m.Muted
}

func (rs *Rooms) droppedOf(m *member) int {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return m.dropped
}

func (rs *Rooms) mute(id string, m *member, on bool) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	r := rs.rooms[id]
	if r == nil || m.Muted == on {
		return
	}
	m.Muted = on
	rs.tell(id, r, nil)
}
