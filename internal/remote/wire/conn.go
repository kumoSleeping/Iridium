package wire

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/coder/websocket"
)

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
		var f Frame
		if err := json.Unmarshal(b, &f); err != nil {
			return fmt.Errorf("executor: bad Frame: %w", err)
		}
		switch {
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
