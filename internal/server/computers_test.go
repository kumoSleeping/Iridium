package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/config"
	"github.com/kumoSleeping/iridium/internal/remote"
	"github.com/kumoSleeping/iridium/internal/remote/executor"
)

// Computers (k_glossary "电脑"): the host lists its own; apps on one machine are one computer's
// connections, under the one that paired first, those on the host under the host. A pairing that
// says its system is a computer; no other is.
func TestComputers(t *testing.T) {
	t.Parallel()
	l := startWith(t, func(s *Server, _ config.Config, _ string) {
		s.Remote = &remote.Hub{Self: "host-hash"}
		s.WatchMachines()
	})
	run := func(tok, machine string) {
		t.Helper()
		exe, err := executor.New(executor.Options{URL: "ws" + strings.TrimPrefix(l.base, "http") + "/v1/internal/exec", Token: tok, State: t.TempDir(), Logf: t.Logf, MachineID: machine})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { _ = exe.Run(ctx); close(done) }()
		t.Cleanup(func() { cancel(); <-done; exe.Close() })
	}
	first := l.newApp("first", false)
	time.Sleep(5 * time.Millisecond)
	second := l.newApp("second", false)
	here := l.newApp("here", false)
	run(second.Token, "m1")
	run(first.Token, "m1")
	run(here.Token, "host-hash")

	var as api.Apps
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		as = api.Apps{}
		l.want(200, "socket", "GET", "/v1/internal/apps", nil, &as)
		n := 0
		for _, a := range as.Apps {
			if a.Computer != nil && a.Computer.Online && !a.Host {
				n++
			}
		}
		if n == 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("not all online: %+v", as.Apps)
		}
	}
	under := map[string]string{}
	for _, a := range as.Apps {
		if a.Host {
			if c := a.Computer; c == nil || !c.Online || !c.Shell || c.OS == "" || c.Under != "" {
				t.Fatalf("the host's computer: %+v", c)
			}
			continue
		}
		if a.Computer != nil {
			under[a.ID] = a.Computer.Under
		}
	}
	if under[first.App.ID] != "" || under[second.App.ID] != first.App.ID || under[here.App.ID] != api.HostID {
		t.Fatalf("under: %v (first %s)", under, first.App.ID)
	}

	// Its first connection removed, the next is the computer.
	l.want(200, "socket", "DELETE", "/v1/internal/apps/first", nil, nil)
	as = api.Apps{}
	l.want(200, "socket", "GET", "/v1/internal/apps", nil, &as)
	for _, a := range as.Apps {
		if a.ID == second.App.ID && (a.Computer == nil || a.Computer.Under != "") {
			t.Fatalf("second, alone on m1: %+v", a.Computer)
		}
	}

	// The kind a pairing says.
	l.want(200, "socket", "PUT", "/v1/internal/public", api.SetPublic{Origin: "https://ir.example.com"}, nil)
	l.s.SetPairing(true)
	for _, c := range []struct {
		req  api.PairRequest
		want string
	}{
		{api.PairRequest{Name: "pc", OS: "linux", Type: "bot"}, api.TypeComputer},
		{api.PairRequest{Name: "pc", Type: api.TypeComputer}, ""},
		{api.PairRequest{Name: "pc", Type: "bot"}, "bot"},
	} {
		if code, _, e := l.pairFrom("", c.req); code != 200 {
			t.Fatalf("pair: %d %+v", code, e)
		}
		nd := l.pending()
		if nd.Pair.Type != c.want {
			t.Fatalf("%+v: type %q, want %q", c.req, nd.Pair.Type, c.want)
		}
		l.want(200, "socket", "POST", "/v1/internal/needs/"+nd.ID, api.NeedAnswer{Allow: false}, nil)
	}
}
