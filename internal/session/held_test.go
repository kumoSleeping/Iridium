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

	"github.com/coder/websocket"

	hdsh "github.com/kumoSleeping/iridium/internal/harness/dsh"
	"github.com/kumoSleeping/iridium/internal/remote"
	"github.com/kumoSleeping/iridium/internal/remote/wire"
)

// heldFinder is desktopFinder whose session is discovered at m1.
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

// fakeHolder is an in-memory fake executor at m1 for session-layer tests.
type fakeHolder struct {
	t         *testing.T
	conn      *wire.Conn
	mu        sync.Mutex
	calls     []string
	events    []string
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
	case "agent.presets":
		return wire.PresetsReply{Presets: []wire.Preset{{ID: "omd-ptc", Plugins: "[]\n"}}, Default: "omd-ptc"}, nil
	case "desktop.models":
		return hdsh.DesktopModelsReply{
			Default: "deepseek-official/deepseek-flash",
			Models:  []hdsh.DesktopModel{{Value: "deepseek-official/deepseek-flash", Name: "DeepSeek Flash", Default: true}},
		}, nil
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

// startHolder connects a fakeHolder to hub as machine m1, saying caps.
func startHolder(t *testing.T, hub *remote.Hub, caps []string) *fakeHolder {
	return startHolderAs(t, hub, caps, "")
}

// startHolderAs is startHolder saying machineID.
func startHolderAs(t *testing.T, hub *remote.Hub, caps []string, machineID string) *fakeHolder {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { hub.Serve(w, req, "m1", "pc") }))
	t.Cleanup(srv.Close)
	ws, _, err := websocket.Dial(bg, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeHolder{t: t, conn: wire.NewConn(ws), machineID: machineID}
	f.conn.OnRequest = f.answer
	f.conn.OnEvent = f.event
	ctx, cancel := context.WithCancel(bg)
	go f.conn.Run(ctx)
	t.Cleanup(func() { cancel(); f.conn.Close() })
	hi := wire.Hello{
		Protocol: wire.Protocol, Program: "fake", OS: "linux", Arch: "amd64", Home: "/home/u", Sep: "/", Scratch: "/tmp/x",
		Agents: []wire.AgentInfo{{Agent: "dsh", Argv: []string{"@dsh-desktop"}, Version: "0.2.2-rc.5"}},
		Caps:   caps, MachineID: machineID,
	}
	if err := f.conn.Call(bg, "hello", hi, &wire.HelloReply{}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the executor", func() bool { _, ok := hub.Machine("m1").Hello(); return ok })
	return f
}
