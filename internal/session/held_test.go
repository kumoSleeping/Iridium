package session

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/config"
	"github.com/kumoSleeping/iridium/internal/harness"
	"github.com/kumoSleeping/iridium/internal/remote"
	"github.com/kumoSleeping/iridium/internal/remote/wire"
)

// heldFinder is desktopFinder whose session DSH Desktop holds (or not).
type heldFinder struct {
	desktopFinder
	mu   sync.Mutex
	held bool
}

func (f *heldFinder) HeldElsewhere(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.held && id == f.id
}

func (f *heldFinder) setHeld(v bool) {
	f.mu.Lock()
	f.held = v
	f.mu.Unlock()
}

// fakeHolder is an executor at m1 speaking held.* as the dsh plugin does, in Go: one session,
// held or not.
type fakeHolder struct {
	t    *testing.T
	conn *wire.Conn

	mu     sync.Mutex
	live   wire.HeldLive
	calls  []string // method and params, as they came
	events []string // events from here: name and params
	down   bool     // held.get fails
	// patches: what agent.prepare gives; machineID: the hello's.
	patches   []string
	machineID string
}

func (f *fakeHolder) event(name string, params json.RawMessage) {
	f.mu.Lock()
	f.events = append(f.events, name+" "+string(params))
	f.mu.Unlock()
}

func (f *fakeHolder) told() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.events)
}

func (f *fakeHolder) answer(_ context.Context, method string, params json.RawMessage) (any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, method+" "+string(params))
	switch method {
	case "held.list":
		return wire.HeldListReply{Sessions: []string{}}, nil
	case "agent.presets":
		return wire.PresetsReply{Presets: []wire.Preset{{ID: "omd-ptc", Plugins: "[]\n"}}, Default: "omd-ptc"}, nil
	case "agent.prepare":
		return wire.PrepareReply{Patches: f.patches, Preset: true}, nil
	case "held.send":
		return struct{}{}, nil
	case "held.get":
		if f.down {
			return nil, &wire.Error{Code: wire.CodeFailed, Message: "down"}
		}
		return f.live, nil
	case "held.set":
		var p wire.HeldSetParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &wire.Error{Code: wire.CodeBadRequest, Message: err.Error()}
		}
		if !f.live.Held {
			return nil, &wire.Error{Code: wire.CodeNotFound, Message: "not held"}
		}
		switch {
		case p.Model != "":
			f.live.Model = p.Model
		case p.Effort != "":
			f.live.Effort = p.Effort
		}
		return f.live, nil
	}
	return nil, &wire.Error{Code: wire.CodeUnsupported, Message: method}
}

func (f *fakeHolder) asked(method string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		if strings.HasPrefix(c, method+" ") {
			out = append(out, strings.TrimPrefix(c, method+" "))
		}
	}
	return out
}

func (f *fakeHolder) set(live wire.HeldLive) {
	f.mu.Lock()
	f.live = live
	f.mu.Unlock()
}

// tell sends held.changed for session.
func (f *fakeHolder) tell(session string, live wire.HeldLive) {
	if err := f.conn.Notify(bg, "held.changed", wire.HeldChanged{Session: session, Live: live}); err != nil {
		f.t.Fatal(err)
	}
}

// startHolder connects a fakeHolder to hub as machine m1, saying caps.
func startHolder(t *testing.T, hub *remote.Hub, caps []string, live wire.HeldLive) *fakeHolder {
	return startHolderAs(t, hub, caps, live, "", nil)
}

// startHolderAs is startHolder saying machineID, agent.prepare giving patches.
func startHolderAs(t *testing.T, hub *remote.Hub, caps []string, live wire.HeldLive, machineID string, patches []string) *fakeHolder {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { hub.Serve(w, req, "m1", "pc") }))
	t.Cleanup(srv.Close)
	ws, _, err := websocket.Dial(bg, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeHolder{t: t, conn: wire.NewConn(ws), live: live, patches: patches, machineID: machineID}
	f.conn.OnRequest = f.answer
	f.conn.OnEvent = f.event
	ctx, cancel := context.WithCancel(bg)
	go f.conn.Run(ctx)
	t.Cleanup(func() { cancel(); f.conn.Close() })
	hi := wire.Hello{Protocol: wire.Protocol, Program: "fake", OS: "linux", Arch: "amd64", Home: "/home/u", Sep: "/", Scratch: "/tmp/x",
		Agents: []wire.AgentInfo{{Agent: "dsh", Argv: []string{"/desktop/node", "/desktop/dsh/lib/bin.js"}, Package: "/desktop/dsh", Scope: "/desktop/scope", Version: "0.2.2-rc.1"}}, Caps: caps, MachineID: machineID}
	if err := f.conn.Call(bg, "hello", hi, &wire.HelloReply{}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the executor", func() bool { _, ok := hub.Machine("m1").Hello(); return ok })
	return f
}

// A held session's model and the level it runs at come from its machine's holder (held.get),
// without the state waiting for them; model and effort are set there (held.set); held.changed
// shows the session again.
func TestHeld(t *testing.T) {
	t.Parallel()
	const id = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	hub := &remote.Hub{Logf: t.Logf}
	f := &heldFinder{desktopFinder: desktopFinder{id}, held: true}
	r := newRig(t, func(_ *config.Config, o *Options) { o.Remote = hub; o.Discovery = f })
	x := startHolder(t, hub, []string{wire.CapHeld}, wire.HeldLive{Held: true, Model: "p/m", Effort: "high"})

	st := r.waitState(id, "the holder's state", func(s *api.SessionState) bool { return s.EffortNow == "high" })
	if st.Effort != "high" || st.Model != "p/m" {
		t.Fatalf("state: effort %q/%q model %q", st.Effort, st.EffortNow, st.Model)
	}
	// Kept: a second look does not ask again.
	r.m.State(id)
	if n := len(x.asked("held.get")); n != 1 {
		t.Errorf("held.get asked %d times", n)
	}
	// Model and effort, set there: not taken up here.
	if err := r.m.SetEffort(bg, id, "low"); err != nil {
		t.Fatal(err)
	}
	if err := r.m.SetModel(bg, id, "p/n"); err != nil {
		t.Fatal(err)
	}
	if got := x.asked("held.set"); len(got) != 2 || !strings.Contains(got[0], `"effort":"low"`) || !strings.Contains(got[1], `"model":"p/n"`) {
		t.Errorf("held.set: %v", got)
	}
	if _, err := r.m.get(id); err == nil {
		t.Error("taken up here")
	}
	if st, _ = r.m.State(id); st.Model != "p/n" || st.EffortNow != "low" {
		t.Errorf("after: model %q effortNow %q", st.Model, st.EffortNow)
	}
	// A message goes by held.send.
	if _, _, err := r.m.SendWith(bg, id, "go on", "web", false); err != nil {
		t.Fatal(err)
	}
	if got := x.asked("held.send"); len(got) != 1 {
		t.Errorf("held.send: %v", got)
	}

	// held.changed: shown again, at most once in heldEvery, the last told.
	key := "session:" + id
	shown := func() api.SessionState {
		var s api.SessionState
		json.Unmarshal(r.hub.Get(key), &s)
		return s
	}
	live := wire.HeldLive{Held: true, Model: "p/n", Effort: "medium"}
	x.tell(id, live)
	waitFor(t, "held.changed shown", func() bool { return shown().EffortNow == "medium" })
	at := time.Now()
	for _, e := range []string{"low", "high", "xhigh"} {
		live.Effort = e
		x.tell(id, live)
	}
	time.Sleep(100 * time.Millisecond)
	if s := shown(); time.Since(at) < 400*time.Millisecond && s.EffortNow != "medium" {
		t.Errorf("shown within %v of the last: %q", time.Since(at), s.EffortNow)
	}
	waitFor(t, "the last held.changed shown", func() bool { return shown().EffortNow == "xhigh" })
	// Of a session not at that machine: let be.
	x.tell("eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee", live)
}

// A hosted dsh session (Iridium ran it here once: a level and a model in its settings) that DSH
// Desktop holds now: the track and the key show what Desktop runs, and a level set there shows.
func TestHeldHosted(t *testing.T) {
	t.Parallel()
	const id = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	hub := &remote.Hub{Logf: t.Logf}
	f := &heldFinder{desktopFinder: desktopFinder{id}, held: true}
	r := newRig(t, func(_ *config.Config, o *Options) { o.Remote = hub; o.Discovery = f })
	h := r.m.newHosted(meta{ID: id, Agent: api.AgentDsh, Cwd: r.cwd, Created: time.Now().UnixMilli(), Machine: "m1", Model: "p/old"})
	h.mu.Lock()
	h.effort, h.model = "high", "p/old"
	h.mu.Unlock()
	r.m.mu.Lock()
	r.m.hosted[id] = h
	r.m.mu.Unlock()
	x := startHolder(t, hub, []string{wire.CapHeld}, wire.HeldLive{Held: true, Model: "p/m", Effort: "xhigh"})

	st := r.waitState(id, "Desktop's level", func(s *api.SessionState) bool { return s.EffortNow == "xhigh" })
	if st.Effort != "xhigh" || st.Model != "p/m" || st.Pick != "p/m" {
		t.Fatalf("effort %q/%q model %q pick %q", st.Effort, st.EffortNow, st.Model, st.Pick)
	}
	if err := r.m.SetEffort(bg, id, "max"); err != nil {
		t.Fatal(err)
	}
	if st, _ = r.m.State(id); st.Effort != "max" || st.EffortNow != "max" {
		t.Errorf("after the track: %q/%q", st.Effort, st.EffortNow)
	}
	if got := x.asked("held.set"); len(got) != 1 || !strings.Contains(got[0], `"effort":"max"`) {
		t.Errorf("held.set: %v", got)
	}
}

// An executor without held: nothing asked, a message the usual way.
func TestHeldNoPlugin(t *testing.T) {
	t.Parallel()
	const id = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	hub := &remote.Hub{Logf: t.Logf}
	f := &heldFinder{desktopFinder: desktopFinder{id}, held: true}
	r := newRig(t, func(_ *config.Config, o *Options) { o.Remote = hub; o.Discovery = f })
	x := startHolder(t, hub, nil, wire.HeldLive{Held: true, Effort: "high"})
	if st, err := r.m.State(id); err != nil || st.EffortNow == "high" {
		t.Fatalf("state: %+v %v", st, err)
	}
	time.Sleep(50 * time.Millisecond)
	if len(x.asked("held.get")) != 0 || len(x.asked("held.send")) != 0 {
		t.Errorf("asked: %v", x.calls)
	}
}

// effortOwner is a channel whose session's own process has the say over an effort ("max": a
// feature there owns it), though the session runs here.
type effortOwner struct {
	mu   sync.Mutex
	asks []harness.Ref
}

func (e *effortOwner) Agent() api.Agent  { return api.AgentClaude }
func (e *effortOwner) Bind(harness.Host) {}
func (e *effortOwner) Set(_ context.Context, r harness.Ref, ch harness.Change) (bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.asks = append(e.asks, r)
	return ch.Effort == "max", nil
}

// A session that runs here: its channel is asked first, and an effort it takes is not the driver's.
func TestChannelSetLive(t *testing.T) {
	t.Parallel()
	owner := &effortOwner{}
	r := newRig(t, func(_ *config.Config, o *Options) { o.Channels = harness.Set{api.AgentClaude: owner} })
	id, err := r.m.Create(bg, api.NewSession{Cwd: r.cwd, Text: "hi"}, "网页")
	if err != nil {
		t.Fatal(err)
	}
	r.waitState(id, "the reply", func(s *api.SessionState) bool { return said(s, "ok: hi") })
	if err := r.m.SetEffort(bg, id, "max"); err != nil {
		t.Fatal(err)
	}
	if err := r.m.SetEffort(bg, id, "high"); err != nil {
		t.Fatal(err)
	}
	owner.mu.Lock()
	asks := slices.Clone(owner.asks)
	owner.mu.Unlock()
	if len(asks) != 2 || !asks[0].Live || asks[0].ID != id {
		t.Fatalf("asked: %+v", asks)
	}
	in := r.inLog(id)
	if strings.Contains(in, `"effortLevel":"max"`) || !strings.Contains(in, `"effortLevel":"high"`) {
		t.Errorf("in.log:\n%s", in)
	}
}
