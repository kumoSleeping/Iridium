package qq

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

// Intents: private and group messages, and button presses.
const (
	IntentC2C         = 1 << 25
	IntentInteraction = 1 << 26
)

// Opcodes.
const (
	opDispatch       = 0
	opHeartbeat      = 1
	opIdentify       = 2
	opResume         = 6
	opReconnect      = 7
	opInvalidSession = 9
	opHello          = 10
	opHeartbeatAck   = 11
)

// Session is where a gateway session stands: kept, the next connection resumes it and QQ sends
// what came meanwhile.
type Session struct {
	ID  string `json:"id,omitempty"`
	Seq int64  `json:"seq,omitempty"`
}

// Gateway holds the bot's one gateway connection. One per bot: a second connection pushes the
// first off (Rhodium's §8.5), so a bot is never shared with another service.
type Gateway struct {
	Client  *Client
	Intents int
	// Session is resumed if set; Save gets every change (each event's seq, a new session).
	Session Session
	Save    func(Session)
	// Event gets each dispatch: its type (C2C_MESSAGE_CREATE, INTERACTION_CREATE, …), its id, data.
	Event func(kind, id string, data json.RawMessage)
	// State: connected or not, and why not.
	State func(connected bool, err error)
	// URL is the gateway's address; "" asks the platform (GET /gateway).
	URL    string
	Delays []time.Duration // reconnect waits; nil for 1s 2s 5s 10s 30s 60s
}

type frame struct {
	Op int             `json:"op"`
	D  json.RawMessage `json:"d,omitempty"`
	S  int64           `json:"s,omitempty"`
	T  string          `json:"t,omitempty"`
	ID string          `json:"id,omitempty"`
}

// errFatal: the platform will not take this bot as it is (bad credentials, intents not allowed).
type errFatal struct{ error }

// Run connects, and connects again whenever the connection drops, till ctx ends.
func (g *Gateway) Run(ctx context.Context) error {
	delays := g.Delays
	if delays == nil {
		delays = []time.Duration{time.Second, 2 * time.Second, 5 * time.Second, 10 * time.Second, 30 * time.Second, time.Minute}
	}
	for attempt := 0; ; attempt++ {
		ready, err := g.once(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if ready {
			attempt = 0
		}
		if g.State != nil {
			g.State(false, err)
		}
		d := delays[min(attempt, len(delays)-1)]
		var f errFatal
		if errors.As(err, &f) {
			d = delays[len(delays)-1]
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(d):
		}
	}
}

func (g *Gateway) save() {
	if g.Save != nil {
		g.Save(g.Session)
	}
}

// once is one connection: ready says it got as far as READY or RESUMED.
func (g *Gateway) once(ctx context.Context) (ready bool, err error) {
	url := g.URL
	if url == "" {
		var out struct {
			URL string `json:"url"`
		}
		if err := g.Client.Do(ctx, "GET", "/gateway", nil, &out); err != nil {
			if Refused(err) {
				return false, errFatal{err}
			}
			return false, err
		}
		url = out.URL
	}
	dctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	conn, _, err := websocket.Dial(dctx, url, nil)
	cancel()
	if err != nil {
		return false, err
	}
	conn.SetReadLimit(8 << 20)
	defer conn.CloseNow()
	cctx, stop := context.WithCancel(ctx)
	defer stop()
	send := func(op int, d any) error {
		b, _ := json.Marshal(d)
		f, _ := json.Marshal(frame{Op: op, D: b})
		wctx, cancel := context.WithTimeout(cctx, 10*time.Second)
		defer cancel()
		return conn.Write(wctx, websocket.MessageText, f)
	}
	acked := make(chan struct{}, 1)
	var seq atomic.Int64 // the heartbeat reads it
	seq.Store(g.Session.Seq)
	for {
		_, b, err := conn.Read(cctx)
		if err != nil {
			switch websocket.CloseStatus(err) {
			case 4004, 4914, 4915: // auth failed, bot offline or banned, intents not allowed
				return ready, errFatal{fmt.Errorf("qq gateway closed: %w", err)}
			case 4006, 4007, 4009:
				g.Session = Session{} // not resumable
				g.save()
			}
			return ready, err
		}
		var f frame
		if json.Unmarshal(b, &f) != nil {
			continue
		}
		switch f.Op {
		case opHello:
			var h struct {
				Interval int `json:"heartbeat_interval"`
			}
			_ = json.Unmarshal(f.D, &h)
			tok, err := g.Client.Token(cctx)
			if err != nil {
				if Refused(err) {
					return ready, errFatal{err}
				}
				return ready, err
			}
			if g.Session.ID != "" {
				err = send(opResume, map[string]any{"token": "QQBot " + tok, "session_id": g.Session.ID, "seq": g.Session.Seq})
			} else {
				err = send(opIdentify, map[string]any{"token": "QQBot " + tok, "intents": g.Intents, "shard": []int{0, 1}})
			}
			if err != nil {
				return ready, err
			}
			every := time.Duration(h.Interval) * time.Millisecond
			if every <= 0 {
				every = 40 * time.Second
			}
			go func() {
				t := time.NewTicker(every)
				defer t.Stop()
				for {
					select {
					case <-cctx.Done():
						return
					case <-t.C:
					}
					var last any
					if n := seq.Load(); n > 0 {
						last = n
					}
					if send(opHeartbeat, last) != nil {
						conn.CloseNow()
						return
					}
					select { // no ack by the next beat: the connection is dead
					case <-acked:
					case <-time.After(every):
						conn.Close(websocket.StatusGoingAway, "no heartbeat ack")
						return
					case <-cctx.Done():
						return
					}
				}
			}()
		case opHeartbeatAck:
			select {
			case acked <- struct{}{}:
			default:
			}
		case opReconnect:
			conn.Close(websocket.StatusNormalClosure, "reconnect")
			return ready, errors.New("qq gateway asked to reconnect")
		case opInvalidSession:
			g.Session = Session{}
			g.save()
			conn.Close(websocket.StatusNormalClosure, "invalid session")
			return ready, errors.New("qq gateway: invalid session")
		case opDispatch:
			if f.S > 0 {
				g.Session.Seq = f.S
				seq.Store(f.S)
			}
			switch f.T {
			case "READY":
				var r struct {
					SessionID string `json:"session_id"`
				}
				_ = json.Unmarshal(f.D, &r)
				g.Session.ID = r.SessionID
				ready = true
				if g.State != nil {
					g.State(true, nil)
				}
			case "RESUMED":
				ready = true
				if g.State != nil {
					g.State(true, nil)
				}
			default:
				if g.Event != nil {
					g.Event(f.T, f.ID, f.D)
				}
			}
			g.save()
		}
	}
}
