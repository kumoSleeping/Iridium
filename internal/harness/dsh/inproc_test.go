package dsh

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

	"github.com/kumoSleeping/iridium/internal/agent"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/harness"
	"github.com/kumoSleeping/iridium/internal/remote"
	"github.com/kumoSleeping/iridium/internal/remote/wire"
)

func dialFakeInproc(t *testing.T, hub *remote.Hub, local bool, id string, handler func(method string, params json.RawMessage) (any, error)) *wire.Conn {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if local {
			hub.ServeLocal(w, r, id, "Local Desktop")
		} else {
			hub.Serve(w, r, id, "Remote Desktop")
		}
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ws, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	conn := wire.NewConn(ws)
	conn.OnRequest = func(_ context.Context, method string, params json.RawMessage) (any, error) {
		return handler(method, params)
	}
	go func() { _ = conn.Run(ctx) }()
	t.Cleanup(func() { conn.Close() })

	var rep wire.HelloReply
	err = conn.Call(ctx, "hello", wire.Hello{
		Protocol: wire.Protocol,
		Program:  "dsh-remote-iridium",
		OS:       "linux",
		Arch:     "amd64",
		Home:     "/tmp",
		Sep:      "/",
		Scratch:  "/tmp/scratch",
		Caps:     []string{CapInproc, wire.CapPersona},
		Agents: []wire.AgentInfo{{
			Agent:   string(api.AgentDsh),
			Argv:    []string{inprocArgv0},
			Version: "0.2.2-rc.5",
		}},
	}, &rep)
	if err != nil {
		t.Fatalf("hello: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := hub.Machine(id).Hello(); ok {
			return conn
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("executor did not become online")
	return nil
}

func TestInprocChannelCatalogPrepareAndEvents(t *testing.T) {
	hub := &remote.Hub{}
	stateDir := t.TempDir()
	fakeRoot := t.TempDir()
	fakeExe, fakeEntry := makeFakeDesktopRoot(t, fakeRoot, "deepseek-harness")

	var modelsCalls atomic.Int32
	configCh := make(chan DesktopConfigEvent, 1)

	ch := New(Options{
		Remote:   hub,
		StateDir: stateDir,
		Logf:     t.Logf,
		OnConfig: func(_ string, cfg DesktopConfigEvent) { configCh <- cfg },
	})
	defer ch.Close()

	conn := dialFakeInproc(t, hub, true, remote.LocalExecID, func(method string, params json.RawMessage) (any, error) {
		switch method {
		case "desktop.models":
			n := modelsCalls.Add(1)
			name := "DeepSeek Flash"
			if n > 1 {
				name = "DeepSeek Reasoner"
			}
			return DesktopModelsReply{
				Default: "deepseek-official/deepseek-flash",
				Models:  []DesktopModel{{Value: "deepseek-official/deepseek-flash", Name: name, Default: true}},
			}, nil
		case "desktop.workspaces":
			return DesktopWorkspacesReply{
				Workspaces: []DesktopWorkspace{{ID: "ws1", Title: "IR", Path: "/work/ir"}},
			}, nil
		case "desktop.sessions":
			return DesktopSessionsReply{
				Sessions: []DesktopSession{{ID: "s1", Title: "Rewrite"}},
			}, nil
		case "desktop.freeze":
			return DesktopFreezeReply{Frozen: true, Running: 0}, nil
		default:
			return nil, &wire.Error{Code: wire.CodeUnsupported, Message: method}
		}
	})

	// 1. LocalExecutor routes local dsh sessions to local-exec.
	ref := harness.Ref{ID: "s1", As: "s1", Agent: api.AgentDsh}
	id, ok := ch.LocalExecutor(context.Background(), ref)
	if !ok || id != remote.LocalExecID {
		t.Fatalf("LocalExecutor = (%q, %v), want (%q, true)", id, ok, remote.LocalExecID)
	}
	if !ch.ProvidedHere() {
		t.Fatal("ProvidedHere expected true")
	}

	// 2. Prepare sets Host.Inproc without calling agent.prepare.
	var spawn agent.SpawnOptions
	if err := ch.Prepare(context.Background(), ref, &spawn); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if spawn.Host == nil || !spawn.Host.Inproc || len(spawn.Host.Argv) != 1 || spawn.Host.Argv[0] != inprocArgv0 {
		t.Fatalf("unexpected Host: %+v", spawn.Host)
	}

	// 3. Catalog fetches via desktop.models once, stays cached (zero subprocess probe), and updates on event.
	cat, err := ch.Catalog(context.Background(), "")
	if err != nil || len(cat.Models) != 1 || cat.Models[0].Name != "DeepSeek Flash" {
		t.Fatalf("Catalog 1: cat=%+v err=%v", cat, err)
	}
	if _, err := ch.Catalog(context.Background(), ""); err != nil {
		t.Fatalf("Catalog 2: %v", err)
	}
	if modelsCalls.Load() != 1 {
		t.Fatalf("expected 1 desktop.models call while cached, got %d", modelsCalls.Load())
	}
	// Push models-changed payload directly.
	if err := conn.Notify(context.Background(), eventDesktopModelsChanged, DesktopModelsReply{
		Default: "deepseek-official/deepseek-chat",
		Models:  []DesktopModel{{Value: "deepseek-official/deepseek-chat", Name: "DeepSeek V4", Default: true}},
	}); err != nil {
		t.Fatalf("notify models.changed: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if c, ok := ch.Cached(""); ok && c.Default == "deepseek-official/deepseek-chat" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if c, ok := ch.Cached(""); !ok || c.Default != "deepseek-official/deepseek-chat" {
		t.Fatalf("expected pushed catalog update, got ok=%v cat=%+v", ok, c)
	}

	// 4. Workspaces, SearchSessions, Freeze RPCs.
	ws, err := ch.Workspaces(context.Background(), "")
	if err != nil || len(ws) != 1 || ws[0].ID != "ws1" {
		t.Fatalf("Workspaces: %+v err=%v", ws, err)
	}
	ss, err := ch.SearchSessions(context.Background(), "", "Rew", 10)
	if err != nil || len(ss) != 1 || ss[0].ID != "s1" {
		t.Fatalf("SearchSessions: %+v err=%v", ss, err)
	}
	fr, err := ch.Freeze(context.Background(), "")
	if err != nil || !fr.Frozen {
		t.Fatalf("Freeze: %+v err=%v", fr, err)
	}

	// 5. desktop.config persists Desktop install path to StateDir.
	if err := conn.Notify(context.Background(), eventDesktopConfig, DesktopConfigEvent{
		DesktopPath: fakeRoot,
	}); err != nil {
		t.Fatalf("notify desktop.config: %v", err)
	}
	select {
	case <-configCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for OnConfig")
	}
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if inst, ok := LoadDesktopInstall(stateDir); ok && inst.Exe == fakeExe && inst.HostEntry == fakeEntry {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("expected desktop.config to persist DesktopInstall to StateDir")
}
