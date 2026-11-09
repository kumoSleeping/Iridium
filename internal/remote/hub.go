package remote

import (
	"github.com/kumoSleeping/iridium/internal/remote/wire"

	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// Hub is the executors that connect here, one Machine per terminal (by its ID).
type Hub struct {
	// Dir is a holder unit's hold directory (session.Manager's): where a remote holder's mirror is.
	Dir  func(unit string) string
	Logf func(string, ...any)
	// Changed is called when a machine comes or goes.
	Changed func(id string)

	mu       sync.Mutex
	machines map[string]*Machine
	handlers map[string]func(machine string, params json.RawMessage)
}

// Handle has fn called with each event named event from an executor (its machine's terminal ID),
// one a harness's channel speaks (held.changed). It is called on the connection's reading
// goroutine: it must not wait on the executor. Set before executors connect.
func (h *Hub) Handle(event string, fn func(machine string, params json.RawMessage)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.handlers == nil {
		h.handlers = map[string]func(string, json.RawMessage){}
	}
	h.handlers[event] = fn
}

func (h *Hub) logf(format string, a ...any) {
	if h.Logf != nil {
		h.Logf(format, a...)
		return
	}
	log.Printf(format, a...)
}

// Machine is the executor of terminal id, connected or not.
func (h *Hub) Machine(id string) *Machine {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.machines == nil {
		h.machines = map[string]*Machine{}
	}
	m := h.machines[id]
	if m == nil {
		m = &Machine{hub: h, id: id, relays: map[string]*relay{}, change: make(chan struct{})}
		h.machines[id] = m
	}
	return m
}

// Find is the machine of terminal ID or name idOrName (any case) that has connected since this
// side started; nil if none.
func (h *Hub) Find(idOrName string) *Machine {
	h.mu.Lock()
	defer h.mu.Unlock()
	if m := h.machines[idOrName]; m != nil {
		return m
	}
	for _, m := range h.machines {
		m.mu.Lock()
		name := m.name
		m.mu.Unlock()
		if name != "" && strings.EqualFold(name, idOrName) {
			return m
		}
	}
	return nil
}

// Online lists the machines connected now, by terminal ID.
func (h *Hub) Online() []string {
	h.mu.Lock()
	ms := make([]*Machine, 0, len(h.machines))
	for _, m := range h.machines {
		ms = append(ms, m)
	}
	h.mu.Unlock()
	var out []string
	for _, m := range ms {
		if _, ok := m.Hello(); ok {
			out = append(out, m.id)
		}
	}
	return out
}

// Serve takes an executor's connection for terminal id (named name), until it ends. A newer
// connection of the same terminal replaces it.
func (h *Hub) Serve(w http.ResponseWriter, r *http.Request, id, name string) {
	ws, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	m := h.Machine(id)
	m.mu.Lock()
	m.name = name
	m.mu.Unlock()
	c := wire.NewConn(ws)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	helloed := make(chan *wire.Hello, 1)
	c.OnRequest = func(_ context.Context, method string, params json.RawMessage) (any, error) {
		if method != "hello" {
			return nil, &wire.Error{Code: wire.CodeUnsupported, Message: method}
		}
		var hi wire.Hello
		if err := json.Unmarshal(params, &hi); err != nil {
			return nil, &wire.Error{Code: wire.CodeBadRequest, Message: err.Error()}
		}
		if hi.Protocol != wire.Protocol {
			return nil, &wire.Error{Code: wire.CodeUnsupported, Message: fmt.Sprintf("protocol %d; this side speaks %d", hi.Protocol, wire.Protocol)}
		}
		if hi.Sep != "/" && hi.Sep != `\` {
			return nil, &wire.Error{Code: wire.CodeBadRequest, Message: "sep must be / or \\"}
		}
		select {
		case helloed <- &hi:
		default:
		}
		return wire.HelloReply{Name: name}, nil
	}
	c.OnEvent = m.event
	go func() {
		select {
		case hi := <-helloed:
			m.connect(c, hi)
			h.logf("executor %s (%s): connected, %s %s/%s, %d holders", name, id, hi.Program, hi.OS, hi.Arch, len(hi.Holds))
		case <-time.After(30 * time.Second):
			h.logf("executor %s: no hello", name)
			c.Close()
		case <-c.Done():
		}
	}()
	err = c.Run(ctx)
	if m.disconnect(c) {
		h.logf("executor %s (%s): gone: %v", name, id, err)
	}
}

// Drop ends executor id's connection, if it has one: its Bot was revoked. Connecting again, its
// TOKEN is turned away before it gets here.
func (h *Hub) Drop(id string) {
	h.mu.Lock()
	m := h.machines[id]
	h.mu.Unlock()
	if m == nil {
		return
	}
	if c, err := m.Conn(); err == nil {
		c.Close()
	}
}

// Machine is one executor: its connection while there is one, and the holders relayed through it.
type Machine struct {
	hub *Hub
	id  string

	mu     sync.Mutex
	name   string
	conn   *wire.Conn
	hello  *wire.Hello
	relays map[string]*relay // by unit
	change chan struct{}     // closed and replaced when the connection comes or goes
}

// ID is the machine's terminal ID.
func (m *Machine) ID() string { return m.id }

// wire.Hello is what the executor said when it connected; ok false while it is not.
func (m *Machine) Hello() (*wire.Hello, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.hello, m.conn != nil
}

// Caps are the optional parts of the protocol the executor said it speaks; nil while it is not
// connected.
func (m *Machine) Caps() []string {
	hi, ok := m.Hello()
	if !ok || hi == nil {
		return nil
	}
	return hi.Caps
}

// Has: the executor is connected and speaks cap (one of wire's Cap…).
func (m *Machine) Has(cap string) bool { return slices.Contains(m.Caps(), cap) }

// Agent is agent a as the executor has it; ok false if not connected or not there.
func (m *Machine) Agent(a string) (wire.AgentInfo, bool) {
	hi, ok := m.Hello()
	if !ok {
		return wire.AgentInfo{}, false
	}
	for _, x := range hi.Agents {
		if x.Agent == a {
			return x, true
		}
	}
	return wire.AgentInfo{}, false
}

func (m *Machine) connect(c *wire.Conn, hi *wire.Hello) {
	m.mu.Lock()
	old := m.conn
	m.conn, m.hello = c, hi
	close(m.change)
	m.change = make(chan struct{})
	relays := make([]*relay, 0, len(m.relays))
	for _, r := range m.relays {
		relays = append(relays, r)
	}
	m.mu.Unlock()
	if old != nil {
		old.Close()
	}
	states := map[string]wire.HoldState{}
	for _, s := range hi.Holds {
		states[s.Hold] = s
	}
	for _, r := range relays {
		r.connected(c, states)
	}
	if m.hub.Changed != nil {
		m.hub.Changed(m.id)
	}
}

// disconnect forgets c if it is the machine's connection; false if a newer one replaced it.
func (m *Machine) disconnect(c *wire.Conn) bool {
	m.mu.Lock()
	if m.conn != c {
		m.mu.Unlock()
		return false
	}
	m.conn = nil
	close(m.change)
	m.change = make(chan struct{})
	m.mu.Unlock()
	if m.hub.Changed != nil {
		m.hub.Changed(m.id)
	}
	return true
}

// changed is closed when the connection next comes or goes.
func (m *Machine) changed() <-chan struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.change
}

// wire.Conn is the connection now, or wire.ErrOffline.
func (m *Machine) Conn() (*wire.Conn, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.conn == nil {
		return nil, wire.ErrOffline
	}
	return m.conn, nil
}

// Call is a request to the executor; wire.ErrOffline while it is not connected.
func (m *Machine) Call(ctx context.Context, method string, params, out any) error {
	c, err := m.Conn()
	if err != nil {
		return err
	}
	return c.Call(ctx, method, params, out)
}

// event is an event from the executor: one a channel handles (Hub.Handle), else for the relay
// of its holder.
func (m *Machine) event(name string, params json.RawMessage) {
	m.hub.mu.Lock()
	f := m.hub.handlers[name]
	m.hub.mu.Unlock()
	if f != nil {
		f(m.id, params)
		return
	}
	var h wire.HoldParams
	if err := json.Unmarshal(params, &h); err != nil || h.Hold == "" {
		return
	}
	m.mu.Lock()
	var r *relay
	for _, x := range m.relays {
		if x.hold() == h.Hold {
			r = x
			break
		}
	}
	m.mu.Unlock()
	if r == nil {
		return
	}
	switch name {
	case "out":
		var e wire.OutEvent
		if json.Unmarshal(params, &e) == nil {
			r.out(e)
		}
	case "exit":
		var e wire.ExitEvent
		if json.Unmarshal(params, &e) == nil {
			r.exit(e)
		}
	}
}

var errStopped = errors.New("relay stopped")
