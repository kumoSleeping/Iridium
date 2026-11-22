package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/config"
	"github.com/kumoSleeping/iridium/internal/remote"
	"github.com/kumoSleeping/iridium/internal/remote/wire"
)

// fakeHost connects an executor to hub as machine id saying caps, answering agent.personas with
// list; asked counts the times.
func fakeHost(t *testing.T, hub *remote.Hub, id string, caps []string, list []wire.Persona) *atomic.Int32 {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hub.Serve(w, r, id, "pc-"+id) }))
	t.Cleanup(srv.Close)
	ctx := context.Background()
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	asked := &atomic.Int32{}
	conn := wire.NewConn(ws)
	conn.OnRequest = func(_ context.Context, method string, params json.RawMessage) (any, error) {
		if method == "agent.personas" {
			asked.Add(1)
			var p wire.PersonasParams
			_ = json.Unmarshal(params, &p)
			if p.Agent != "dsh" {
				return nil, &wire.Error{Code: wire.CodeUnsupported, Message: p.Agent}
			}
			return wire.PersonasReply{Personas: list}, nil
		}
		return nil, &wire.Error{Code: wire.CodeUnsupported, Message: method}
	}
	run, cancel := context.WithCancel(ctx)
	go conn.Run(run)
	t.Cleanup(func() { cancel(); conn.Close() })
	hi := wire.Hello{Protocol: wire.Protocol, Program: "fake", OS: "linux", Arch: "amd64", Home: "/home/u", Sep: "/", Scratch: "/tmp/x",
		Agents: []wire.AgentInfo{{Agent: "dsh", Argv: []string{"dsh"}, Version: "0.2.2-rc.1"}}, Caps: caps}
	if err := conn.Call(ctx, "hello", hi, &wire.HelloReply{}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 200; i++ {
		if _, ok := hub.Machine(id).Hello(); ok {
			return asked
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the executor did not connect")
	return nil
}

// Personas come from the agents' hosts alone (agent.personas; otae, 2026-11-22): one that says
// the cap is asked, its answer kept a while; one that does not is not; none: an empty list.
func TestHostPersonas(t *testing.T) {
	t.Parallel()
	hub := &remote.Hub{Logf: t.Logf}
	l := startWith(t, func(s *Server, _ config.Config, _ string) { s.Remote = hub })
	var got []api.HostPersona
	l.want(200, "web", "GET", "/v1/internal/personas", nil, &got)
	if got == nil || len(got) != 0 {
		t.Fatalf("with no host: %+v", got)
	}
	asked := fakeHost(t, hub, "m1", []string{wire.CapPrepare, wire.CapPersonas}, []wire.Persona{
		{ID: "overclock", Name: "超频", Description: "冷静", Text: "你是超频。\n\n## 语言风格\n直接", From: "OMD"},
		{ID: "empty", Name: "空", Text: ""},
	})
	older := fakeHost(t, hub, "m2", []string{wire.CapPrepare}, []wire.Persona{{ID: "x", Name: "x", Text: "x"}})
	l.want(200, "web", "GET", "/v1/internal/personas", nil, &got)
	if len(got) != 1 || got[0].ID != "overclock" || got[0].Text != "你是超频。\n\n## 语言风格\n直接" || got[0].Machine != "m1" || got[0].Agent != api.AgentDsh || got[0].From != "OMD" {
		t.Fatalf("the personas: %+v", got)
	}
	l.want(200, "web", "GET", "/v1/internal/personas", nil, &got)
	if asked.Load() != 1 || older.Load() != 0 {
		t.Errorf("asked %d and %d times", asked.Load(), older.Load())
	}
}
