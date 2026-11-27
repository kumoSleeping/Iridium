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

	"github.com/kumoSleeping/iridium/internal/hold"
	"github.com/kumoSleeping/iridium/internal/version"
)

// Hub is the executors that connect here, one Machine per terminal (by its ID).
type Hub struct {
	// Dir is a holder unit's hold directory (session.Manager's): where a remote holder's mirror is.
	Dir  func(unit string) string
	Logf func(string, ...any)
	// Changed is called when a machine comes or goes.
	Changed func(id string)
	// Denied is called when an executor turns down a file request (fs.*) for path: ctx is the
	// request's.
	Denied func(ctx context.Context, machine, method, path string)
	// Self is the host's own machine hash (machineid.Hash()): an executor whose hello says the
	// same is an app on the host, whose agents are not taken (they are the host's); "": none.
	Self string
	// NameOf is an app's name now, by its ID, for Find; nil: the name it connected with.
	NameOf func(id string) string

	mu        sync.Mutex
	machines  map[string]*Machine
	handlers  map[string][]func(machine string, params json.RawMessage)
	onChanged []func(id string)
}

// Handle registers fn to be called with each event named event from an executor (its machine's
// terminal ID). Multiple handlers may be registered for the same event. Called on the connection's
// reading goroutine: it must not wait on the executor. Set before executors connect.
func (h *Hub) Handle(event string, fn func(machine string, params json.RawMessage)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.handlers == nil {
		h.handlers = map[string][]func(string, json.RawMessage){}
	}
	h.handlers[event] = append(h.handlers[event], fn)
}

// OnChanged registers an additional callback invoked when any machine connects or disconnects.
func (h *Hub) OnChanged(fn func(id string)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.onChanged = append(h.onChanged, fn)
}

func (h *Hub) notifyChanged(id string) {
	if h.Changed != nil {
		h.Changed(id)
	}
	h.mu.Lock()
	cbs := slices.Clone(h.onChanged)
	h.mu.Unlock()
	for _, fn := range cbs {
		fn(id)
	}
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
		m = &Machine{hub: h, id: id, relays: map[string]*relay{}, runs: map[string]*Run{}, change: make(chan struct{})}
		h.machines[id] = m
	}
	return m
}

// Find is the machine of app ID or name idOrName (any case) that has connected since this side
// started; nil if none. A name is the app's now (NameOf), not the one it connected with: one
// renamed is not found by its old name, nor another by the name it had.
func (h *Hub) Find(idOrName string) *Machine {
	h.mu.Lock()
	ms := make([]*Machine, 0, len(h.machines))
	for _, m := range h.machines {
		ms = append(ms, m)
	}
	nameOf := h.NameOf
	h.mu.Unlock()
	var byName *Machine
	for _, m := range ms {
		m.mu.Lock()
		name, seen := m.name, m.hello != nil
		m.mu.Unlock()
		if m.id == idOrName && (seen || nameOf == nil || nameOf(m.id) != "") {
			return m // an app's, connected or not; not one only looked up by an ID of nothing
		}
		if !seen {
			continue
		}
		if nameOf != nil {
			name = nameOf(m.id)
		}
		if name != "" && strings.EqualFold(name, idOrName) {
			byName = m
		}
	}
	return byName
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
	h.serve(w, r, id, name, false)
}

// ServeLocal takes a local-credential executor connection on this computer (zero pairing): its
// agents are always treated as the host's own (m.local = true), even if Hub.Self is unset.
func (h *Hub) ServeLocal(w http.ResponseWriter, r *http.Request, id, name string) {
	h.serve(w, r, id, name, true)
}

func (h *Hub) serve(w http.ResponseWriter, r *http.Request, id, name string, forceLocal bool) {
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
		if forceLocal || (h.Self != "" && hi.MachineID == h.Self) {
			// An app on the host: its agents are the host's own (decisions 22), not taken twice;
			// kept aside, for a launch here it composes (Local).
			if len(hi.Agents) > 0 {
				h.logf("executor %s (%s): on this computer: its agents are not taken", name, id)
			}
			m.mu.Lock()
			m.local, m.localExec, m.hostAgents = true, forceLocal, hi.Agents
			m.mu.Unlock()
			hi.Agents = nil
		} else {
			m.mu.Lock()
			m.local, m.localExec, m.hostAgents = false, false, nil
			m.mu.Unlock()
		}
		select {
		case helloed <- &hi:
		default:
		}
		return wire.HelloReply{Name: name, Version: version.Version}, nil
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
	// Forgotten unless something still hangs on it (a session's holder relayed through it).
	h.mu.Lock()
	m.mu.Lock()
	if len(m.relays) == 0 {
		delete(h.machines, id)
	}
	m.mu.Unlock()
	h.mu.Unlock()
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
	runs   map[string]*Run   // command lines running there (run), by ID
	change chan struct{}     // closed and replaced when the connection comes or goes
	// local: it said it is on this computer (Hub.Self or ServeLocal); localExec: connected via
	// the local loopback credential file; hostAgents: its agents then.
	local      bool
	localExec  bool
	hostAgents []wire.AgentInfo
}

// Local is a connected executor on this computer that has cap. When multiple local connections
// exist (e.g. a real app alongside a yielding headless fallback, or a self-tunnelled remote
// connection alongside local-exec), it prefers direct local-exec and non-headless connections.
func (h *Hub) Local(cap string) *Machine {
	h.mu.Lock()
	ms := make([]*Machine, 0, len(h.machines))
	for _, m := range h.machines {
		ms = append(ms, m)
	}
	h.mu.Unlock()
	var best *Machine
	bestScore := -1
	for _, m := range ms {
		m.mu.Lock()
		local := m.local
		localExec := m.localExec
		hi := m.hello
		online := m.conn != nil
		m.mu.Unlock()
		if !local || !online || !m.Has(cap) {
			continue
		}
		score := 0
		if hi != nil && !hi.Headless {
			score += 2
		}
		if localExec {
			score += 1
		}
		if score > bestScore {
			best, bestScore = m, score
		}
	}
	return best
}

// LocalHeadless is a connected headless executor on this computer that has cap; nil if none.
func (h *Hub) LocalHeadless(cap string) *Machine {
	h.mu.Lock()
	ms := make([]*Machine, 0, len(h.machines))
	for _, m := range h.machines {
		ms = append(ms, m)
	}
	h.mu.Unlock()
	for _, m := range ms {
		m.mu.Lock()
		local := m.local
		hi := m.hello
		online := m.conn != nil
		m.mu.Unlock()
		if local && online && hi != nil && hi.Headless && m.Has(cap) {
			return m
		}
	}
	return nil
}

// LocalPIDs are the processes of the executors connected on this computer that said theirs
// (Hello.PID): agents they run in themselves are Iridium's to drive through them.
func (h *Hub) LocalPIDs() map[int]bool {
	h.mu.Lock()
	ms := make([]*Machine, 0, len(h.machines))
	for _, m := range h.machines {
		ms = append(ms, m)
	}
	h.mu.Unlock()
	out := map[int]bool{}
	for _, m := range ms {
		m.mu.Lock()
		if m.local && m.conn != nil && m.hello != nil && m.hello.PID > 0 {
			out[m.hello.PID] = true
		}
		m.mu.Unlock()
	}
	return out
}

// IsLocal: the executor said it is on this computer (Hub.Self).
func (m *Machine) IsLocal() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.local
}

// AgentNames are the agents the executor has: those it runs as a machine's, and on this computer
// its host's own.
func (m *Machine) AgentNames() []string {
	var out []string
	if hi, ok := m.Hello(); ok && hi != nil {
		for _, a := range hi.Agents {
			out = append(out, a.Agent)
		}
	}
	m.mu.Lock()
	for _, a := range m.hostAgents {
		if !slices.Contains(out, a.Agent) {
			out = append(out, a.Agent)
		}
	}
	m.mu.Unlock()
	return out
}

// HostAgent is agent a as an executor on this computer has it (its agents are not taken as a
// machine's: Local's).
func (m *Machine) HostAgent(a string) (wire.AgentInfo, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, x := range m.hostAgents {
		if x.Agent == a {
			return x, true
		}
	}
	return wire.AgentInfo{}, false
}

// Notify sends the executor event name; an error if it is not connected.
func (m *Machine) Notify(ctx context.Context, name string, params any) error {
	c, err := m.Conn()
	if err != nil {
		return err
	}
	return c.Notify(ctx, name, params)
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
	m.hub.notifyChanged(m.id)
}

// disconnect forgets c if it is the machine's connection; false if a newer one replaced it.
// For a local-exec connection (whose holds run in-process and die with it, and whose ID is
// per-connection), active relays are ended immediately so hosted sessions cleanly return to idle.
func (m *Machine) disconnect(c *wire.Conn) bool {
	m.mu.Lock()
	if m.conn != c {
		m.mu.Unlock()
		return false
	}
	m.conn = nil
	close(m.change)
	m.change = make(chan struct{})
	var endRelays []*relay
	localExec := m.localExec
	if localExec {
		for _, r := range m.relays {
			endRelays = append(endRelays, r)
		}
	}
	m.mu.Unlock()
	for _, r := range endRelays {
		r.end(hold.Exit{From: "holder"}, "local host disconnected")
	}
	m.hub.notifyChanged(m.id)
	if localExec {
		m.hub.mu.Lock()
		m.mu.Lock()
		if m.conn == nil && len(m.relays) == 0 && m.hub.machines[m.id] == m {
			delete(m.hub.machines, m.id)
		}
		m.mu.Unlock()
		m.hub.mu.Unlock()
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
	err = c.Call(ctx, method, params, out)
	if err != nil && m.hub.Denied != nil && strings.HasPrefix(method, "fs.") && wire.IsCode(err, wire.CodeDenied) {
		var p struct{ Path string }
		if b, merr := json.Marshal(params); merr == nil && json.Unmarshal(b, &p) == nil && p.Path != "" {
			m.hub.Denied(ctx, m.id, method, p.Path)
		}
	}
	return err
}

// event is an event from the executor: ones channels/discovery handle (Hub.Handle), else for the
// relay of its holder.
func (m *Machine) event(name string, params json.RawMessage) {
	m.hub.mu.Lock()
	fns := slices.Clone(m.hub.handlers[name])
	m.hub.mu.Unlock()
	if len(fns) > 0 {
		for _, f := range fns {
			f(m.id, params)
		}
		return
	}
	if name == "run.out" || name == "run.exit" {
		m.runEvent(name, params)
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

// ErrNoGrant: the executor takes no grant (an older one): what it may do is set where it runs.
var ErrNoGrant = errors.New("that computer takes no grant from here: its owner restarts it with --shell or --roots")

// Grant asks the executor for p (wire.GrantParams): it asks the person at its terminal for
// anything wider, so it may take as long as they do. Allowed, its hello says what it has now and
// the pages hear. wire's CodeDenied: declined there.
func (m *Machine) Grant(ctx context.Context, p wire.GrantParams) (wire.Grant, error) {
	var g wire.Grant
	c, err := m.Conn()
	if err != nil {
		return g, err
	}
	if !m.Has(wire.CapGrant) {
		return g, ErrNoGrant
	}
	if err := c.Call(ctx, "grant", p, &g); err != nil {
		return g, err
	}
	m.mu.Lock()
	if m.conn == c && m.hello != nil {
		hi := *m.hello
		hi.Shell, hi.NoShell, hi.AgentShell, hi.Roots = &g.Shell, !g.Shell, g.Shell, g.Roots
		m.hello = &hi
	}
	m.mu.Unlock()
	if m.hub.Changed != nil {
		m.hub.Changed(m.id)
	}
	return g, nil
}
