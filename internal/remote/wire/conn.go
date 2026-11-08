package wire

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

// Heartbeat: each side sends the event "ping" every PingEvery, which keeps a proxy's idle timeout
// (Cloudflare's, about 100s) from cutting the connection; a side that has had a ping from the other
// and then nothing for DeadAfter ends the connection, so both learn of a dead one soon. A peer that
// never pings (an older one) is never ended for its silence.
var (
	PingEvery = 25 * time.Second
	DeadAfter = 70 * time.Second
)

// EventPing is the heartbeat's event; it never reaches OnEvent.
const EventPing = "ping"

// MaxFrame is the largest Frame read: an fs.read of MaxRead in base64, and then some.
const MaxFrame = 8 << 20

// Conn is one executor connection, either side's: requests both ways, answered by ID, and
// events. Run reads until the connection ends.
type Conn struct {
	ws  *websocket.Conn
	wmu sync.Mutex

	next  atomic.Int64
	mu    sync.Mutex
	waits map[int64]chan Frame
	done  chan struct{}
	err   error

	heard  atomic.Int64 // when a frame last came (unix ns)
	pinged atomic.Bool  // the other side pings
	rtt    atomic.Int64 // the last WebSocket ping's round trip (ns); 0 before the first

	// OnRequest answers a request from the other side; nil answers every one unsupported.
	OnRequest func(ctx context.Context, method string, params json.RawMessage) (any, error)
	// OnEvent is an event from the other side, called in order on Run's goroutine.
	OnEvent func(event string, params json.RawMessage)
}

// NewConn wraps ws; set the handlers, then Run.
func NewConn(ws *websocket.Conn) *Conn {
	ws.SetReadLimit(MaxFrame)
	return &Conn{ws: ws, waits: map[int64]chan Frame{}, done: make(chan struct{})}
}

// Run reads frames until the connection ends or ctx does, and returns why.
func (c *Conn) Run(ctx context.Context) error {
	c.heard.Store(time.Now().UnixNano())
	beat, stop := context.WithCancel(ctx)
	defer stop()
	go c.heartbeat(beat)
	err := c.run(ctx)
	c.mu.Lock()
	c.err = err
	waits := c.waits
	c.waits = map[int64]chan Frame{}
	c.mu.Unlock()
	for _, ch := range waits {
		close(ch)
	}
	close(c.done)
	_ = c.ws.CloseNow()
	return err
}

func (c *Conn) run(ctx context.Context) error {
	for {
		_, b, err := c.ws.Read(ctx)
		if err != nil {
			return err
		}
		c.heard.Store(time.Now().UnixNano())
		var f Frame
		if err := json.Unmarshal(b, &f); err != nil {
			return fmt.Errorf("executor: bad Frame: %w", err)
		}
		switch {
		case f.Event == EventPing:
			c.pinged.Store(true)
		case f.Method != "":
			go c.answer(ctx, f)
		case f.Event != "":
			if c.OnEvent != nil {
				c.OnEvent(f.Event, f.Params)
			}
		default:
			c.mu.Lock()
			ch := c.waits[f.ID]
			delete(c.waits, f.ID)
			c.mu.Unlock()
			if ch != nil {
				ch <- f
			}
		}
	}
}

// heartbeat pings the other side, and ends the connection once a peer that pings has gone quiet.
func (c *Conn) heartbeat(ctx context.Context) {
	t := time.NewTicker(PingEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if c.pinged.Load() && time.Since(time.Unix(0, c.heard.Load())) > DeadAfter {
			_ = c.ws.CloseNow() // no close handshake: the other side is not there to answer it
			return
		}
		_ = c.Notify(ctx, EventPing, nil)
		// The round trip, by a WebSocket ping, which every peer answers by itself.
		pctx, cancel := context.WithTimeout(ctx, min(10*time.Second, PingEvery))
		start := time.Now()
		if c.ws.Ping(pctx) == nil {
			c.rtt.Store(int64(max(time.Since(start), time.Microsecond)))
		}
		cancel()
	}
}

// RTT is the last heartbeat's round trip; 0 before the first.
func (c *Conn) RTT() time.Duration { return time.Duration(c.rtt.Load()) }

func (c *Conn) answer(ctx context.Context, f Frame) {
	var res any
	err := error(&Error{Code: CodeUnsupported, Message: f.Method})
	if c.OnRequest != nil {
		res, err = c.OnRequest(ctx, f.Method, f.Params)
	}
	out := Frame{ID: f.ID}
	if err != nil {
		var e *Error
		if !errors.As(err, &e) {
			e = &Error{Code: CodeFailed, Message: err.Error()}
		}
		out.Error = e
	} else {
		b, merr := json.Marshal(res)
		if merr != nil {
			out.Error = &Error{Code: CodeFailed, Message: merr.Error()}
		} else {
			out.Result = b
		}
	}
	_ = c.write(ctx, out)
}

func (c *Conn) write(ctx context.Context, f Frame) error {
	b, err := json.Marshal(f)
	if err != nil {
		return err
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return c.ws.Write(ctx, websocket.MessageText, b)
}

// Call sends a request and waits for its answer, decoded into out (if not nil).
func (c *Conn) Call(ctx context.Context, method string, params, out any) error {
	p, err := json.Marshal(params)
	if err != nil {
		return err
	}
	id := c.next.Add(1)
	ch := make(chan Frame, 1)
	c.mu.Lock()
	select {
	case <-c.done:
		c.mu.Unlock()
		return ErrOffline
	default:
	}
	c.waits[id] = ch
	c.mu.Unlock()
	if err := c.write(ctx, Frame{ID: id, Method: method, Params: p}); err != nil {
		c.mu.Lock()
		delete(c.waits, id)
		c.mu.Unlock()
		return fmt.Errorf("%w: %v", ErrOffline, err)
	}
	select {
	case f, ok := <-ch:
		if !ok {
			return ErrOffline
		}
		if f.Error != nil {
			return f.Error
		}
		if out != nil && len(f.Result) > 0 {
			return json.Unmarshal(f.Result, out)
		}
		return nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.waits, id)
		c.mu.Unlock()
		return ctx.Err()
	}
}

// Notify sends an event.
func (c *Conn) Notify(ctx context.Context, event string, params any) error {
	p, err := json.Marshal(params)
	if err != nil {
		return err
	}
	return c.write(ctx, Frame{Event: event, Params: p})
}

// Done is closed once Run has returned.
func (c *Conn) Done() <-chan struct{} { return c.done }

// Close ends the connection.
func (c *Conn) Close() { _ = c.ws.Close(websocket.StatusNormalClosure, "") }
