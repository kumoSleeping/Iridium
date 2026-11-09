package session

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/config"
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
// held or not, a feature with options.
type fakeHolder struct {
	t    *testing.T
	conn *wire.Conn

	mu    sync.Mutex
	live  wire.HeldLive
	calls []string // method and params, as they came
	down  bool     // held.get fails
}

func (f *fakeHolder) answer(_ context.Context, method string, params json.RawMessage) (any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, method+" "+string(params))
	switch method {
	case "held.list", "dsh.open":
		return wire.HeldListReply{Sessions: []string{}}, nil
	case "held.send", "dsh.prompt":
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
		case p.Feature != nil:
			for i := range f.live.Features {
				if x := &f.live.Features[i]; x.ID == p.Feature.ID {
					x.On, x.Active = p.Feature.On, p.Feature.On
					if p.Feature.Option != "" {
						x.Option = p.Feature.Option
					}
				}
			}
			if p.Feature.On {
				f.live.Effort = "max" // the feature's own level while on
			}
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
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { hub.Serve(w, req, "m1", "pc") }))
	t.Cleanup(srv.Close)
	ws, _, err := websocket.Dial(bg, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeHolder{t: t, conn: wire.NewConn(ws), live: live}
	f.conn.OnRequest = f.answer
	ctx, cancel := context.WithCancel(bg)
	go f.conn.Run(ctx)
	t.Cleanup(func() { cancel(); f.conn.Close() })
	hi := wire.Hello{Protocol: wire.Protocol, Program: "fake", OS: "linux", Arch: "amd64", Home: "/home/u", Sep: "/", Scratch: "/tmp/x",
		Agents: []wire.AgentInfo{{Agent: "dsh", Argv: []string{"dsh"}, Version: "0.2.2-rc.1"}}, Caps: caps}
	if err := f.conn.Call(bg, "hello", hi, &wire.HelloReply{}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the executor", func() bool { _, ok := hub.Machine("m1").Hello(); return ok })
	return f
}

func ultraLike(on bool) wire.Feature {
	return wire.Feature{ID: "x.ultra", Name: "Ultra", On: on, Active: on, Available: true, Hint: "its hint", Place: "effort", Tone: "warm", V: 1,
		Options: []wire.FeatureOption{{ID: "workflow", Name: "Workflow"}, {ID: "free", Name: "Free"}}, Option: "free"}
}

// A held session's features and the level it runs at come from its machine's holder (held.get),
// without the state waiting for them; a feature is switched there (held.set), only one it has,
// at an option of its; model and effort are set there too; held.changed shows the session again.
func TestHeld(t *testing.T) {
	t.Parallel()
	const id = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	hub := &remote.Hub{Logf: t.Logf}
	f := &heldFinder{desktopFinder: desktopFinder{id}, held: true}
	r := newRig(t, func(_ *config.Config, o *Options) { o.Remote = hub; o.Discovery = f })
	x := startHolder(t, hub, []string{wire.CapHeld, wire.CapHeldFeatures},
		wire.HeldLive{Held: true, Model: "p/m", Effort: "high", Features: []wire.Feature{ultraLike(false), {ID: "bad id", Name: "no", Place: "effort"}}})

	st := r.waitState(id, "the holder's state", func(s *api.SessionState) bool { return len(s.Features) > 0 })
	if len(st.Features) != 1 || st.Features[0].ID != "x.ultra" || st.Features[0].On || !st.Features[0].Available || st.Features[0].Option != "free" ||
		st.EffortNow != "high" || st.Effort != "high" || st.Model != "p/m" {
		t.Fatalf("state: %+v effort %q/%q model %q", st.Features, st.Effort, st.EffortNow, st.Model)
	}
	// Kept: a second look does not ask again.
	r.m.State(id)
	if n := len(x.asked("held.get")); n != 1 {
		t.Errorf("held.get asked %d times", n)
	}

	// Only a feature the session has, at an option of its: else nothing is asked.
	if err := r.m.SetFeature(bg, id, "x.other", api.SetFeature{On: true}); !errors.Is(err, ErrBadRequest) {
		t.Errorf("an unknown feature: %v", err)
	}
	if err := r.m.SetFeature(bg, id, "x.ultra", api.SetFeature{On: true, Option: "team"}); !errors.Is(err, ErrBadRequest) {
		t.Errorf("an unknown option: %v", err)
	}
	if n := len(x.asked("held.set")); n != 0 {
		t.Fatalf("held.set asked: %v", x.asked("held.set"))
	}
	if err := r.m.SetFeature(bg, id, "x.ultra", api.SetFeature{On: true, Option: "workflow"}); err != nil {
		t.Fatal(err)
	}
	if got := x.asked("held.set"); len(got) != 1 || !strings.Contains(got[0], `"feature":{"id":"x.ultra","on":true,"option":"workflow"}`) {
		t.Errorf("held.set: %v", got)
	}
	st, _ = r.m.State(id)
	if !st.Features[0].On || st.Features[0].Option != "workflow" || st.EffortNow != "max" {
		t.Errorf("after the switch: %+v effortNow %q", st.Features, st.EffortNow)
	}
	// Model and effort, set there: not taken up here.
	if err := r.m.SetEffort(bg, id, "low"); err != nil {
		t.Fatal(err)
	}
	if err := r.m.SetModel(bg, id, "p/n"); err != nil {
		t.Fatal(err)
	}
	if got := x.asked("held.set"); len(got) != 3 || !strings.Contains(got[1], `"effort":"low"`) || !strings.Contains(got[2], `"model":"p/n"`) {
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
	if got := x.asked("held.send"); len(got) != 1 || len(x.asked("dsh.prompt")) != 0 {
		t.Errorf("held.send: %v", got)
	}

	// held.changed: shown again, at most once in heldEvery, the last told.
	key := "session:" + id
	shown := func() api.SessionState {
		var s api.SessionState
		json.Unmarshal(r.hub.Get(key), &s)
		return s
	}
	live := wire.HeldLive{Held: true, Model: "p/n", Effort: "medium", Features: []wire.Feature{ultraLike(false)}}
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

	// Not held: shown, not switched, the plugin's reason said.
	f.setHeld(false)
	unheld := ultraLike(false)
	unheld.Available, unheld.Reason = false, "not open there"
	x.set(wire.HeldLive{Features: []wire.Feature{unheld}})
	x.tell(id, wire.HeldLive{Features: []wire.Feature{unheld}})
	st = r.waitState(id, "not held", func(s *api.SessionState) bool { return len(s.Features) == 1 && !s.Features[0].Available })
	if st.EffortNow != "" {
		t.Errorf("not held, effortNow %q", st.EffortNow)
	}
	if err := r.m.SetFeature(bg, id, "x.ultra", api.SetFeature{On: true}); !errors.Is(err, ErrBadRequest) || !strings.Contains(err.Error(), "not open there") {
		t.Errorf("not held: %v", err)
	}
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
	x := startHolder(t, hub, []string{wire.CapHeld, wire.CapHeldFeatures}, wire.HeldLive{Held: true, Model: "p/m", Effort: "xhigh", Features: []wire.Feature{ultraLike(true)}})

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

// A held.get that fails is not taken as told (a feature is asked for again when switched); a
// language changed asks again in it, the session shown again.
func TestHeldFailedAndLang(t *testing.T) {
	t.Parallel()
	const id = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	hub := &remote.Hub{Logf: t.Logf}
	f := &heldFinder{desktopFinder: desktopFinder{id}, held: true}
	r := newRig(t, func(_ *config.Config, o *Options) { o.Remote = hub; o.Discovery = f })
	x := startHolder(t, hub, []string{wire.CapHeld, wire.CapHeldFeatures}, wire.HeldLive{Held: true, Features: []wire.Feature{ultraLike(false)}})
	x.mu.Lock()
	x.down = true
	x.mu.Unlock()
	r.m.State(id)
	waitFor(t, "a held.get", func() bool { return len(x.asked("held.get")) == 1 })
	time.Sleep(50 * time.Millisecond)
	if st, _ := r.m.State(id); len(st.Features) > 0 {
		t.Fatal("a failed held.get kept as told")
	}
	x.mu.Lock()
	x.down = false
	x.mu.Unlock()
	if err := r.m.SetFeature(bg, id, "x.ultra", api.SetFeature{On: true}); err != nil {
		t.Fatalf("after a failed look: %v", err)
	}

	tn := r.m.Tuned()
	tn.Lang = "en"
	r.m.Tune(tn)
	waitFor(t, "asked in English", func() bool {
		got := x.asked("held.get")
		return len(got) > 0 && strings.Contains(got[len(got)-1], `"lang":"en"`)
	})
}

// An executor without held.features: no features, nothing asked; a message by dsh.prompt.
func TestHeldOlderPlugin(t *testing.T) {
	t.Parallel()
	const id = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	hub := &remote.Hub{Logf: t.Logf}
	f := &heldFinder{desktopFinder: desktopFinder{id}, held: true}
	r := newRig(t, func(_ *config.Config, o *Options) { o.Remote = hub; o.Discovery = f })
	x := startHolder(t, hub, nil, wire.HeldLive{Held: true, Features: []wire.Feature{ultraLike(false)}})
	if st, err := r.m.State(id); err != nil || len(st.Features) != 0 {
		t.Fatalf("state: %+v %v", st, err)
	}
	if err := r.m.SetFeature(bg, id, "x.ultra", api.SetFeature{On: true}); !errors.Is(err, ErrBadRequest) {
		t.Errorf("set: %v", err)
	}
	if _, _, err := r.m.SendWith(bg, id, "go on", "web", false); err != nil {
		t.Fatal(err)
	}
	if len(x.asked("dsh.prompt")) != 1 || len(x.asked("held.get")) != 0 || len(x.asked("held.send")) != 0 {
		t.Errorf("asked: %v", x.calls)
	}
}
