package server

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/config"
	"github.com/kumoSleeping/iridium/internal/remote"
	"github.com/kumoSleeping/iridium/internal/remote/executor"
	"github.com/kumoSleeping/iridium/internal/remote/wire"
)

// A computer with agents pairs: its home is its scope unless it asks for another (decision 17),
// kept by the name it is allowed by. Its agents are on till turned off in its page; one turned off
// is marked in the list and not started there. One on the host carries none; one without agents
// gets all.
func TestAgentsOffAndHomeScope(t *testing.T) {
	l := startWith(t, func(s *Server, _ config.Config, _ string) { s.Remote = &remote.Hub{Self: "host-hash"}; s.WatchMachines() })
	l.want(200, "socket", "PUT", "/api/public", api.SetPublic{Origin: "https://ir.example.com"}, nil)
	l.s.OpenPairing(0)
	ask := func(req api.PairRequest) (api.Pairing, api.Need) {
		t.Helper()
		code, p, e := l.pairFrom("", req)
		if code != 200 {
			t.Fatalf("pair: %d %+v", code, e)
		}
		return p, l.pending()
	}
	dsh := []api.ExecAgent{{Agent: "dsh", Version: "0.2.2"}}
	_, nd := ask(api.PairRequest{Name: "lab", OS: "linux", Home: "/home/lab", MachineID: "other", Agents: dsh})
	if nd.Wants == nil || !slices.Equal(nd.Wants.Scope, []string{"lab:/home/lab"}) || !slices.Equal(nd.Wants.Agents, []string{"*"}) {
		t.Fatalf("the home as the scope asked: %+v", nd.Wants)
	}
	// Allowed by another name, dsh off from the start.
	l.want(200, "socket", "POST", "/api/needs/"+nd.ID, api.NeedAnswer{Allow: true, Name: "lab-pc", AgentsOff: []string{"dsh"}}, nil)
	var as api.Apps
	l.want(200, "socket", "GET", "/api/apps", nil, &as)
	i := slices.IndexFunc(as.Apps, func(a api.App) bool { return a.Name == "lab-pc" })
	if i < 0 || as.Apps[i].Perms == nil || !slices.Equal(as.Apps[i].Perms.Scope, []string{"lab-pc:/home/lab"}) {
		t.Fatalf("the home kept by the new name: %+v", as.Apps)
	}
	if off, _ := l.s.st.AgentsOff(as.Apps[i].ID); !slices.Equal(off, []string{"dsh"}) {
		t.Fatalf("turned off as allowed: %v", off)
	}
	// On the host: no agents, all.
	_, nd = ask(api.PairRequest{Name: "here", OS: "linux", Home: "/home/me", MachineID: "host-hash", Agents: dsh})
	if len(nd.Pair.Agents) != 0 || !slices.Equal(nd.Wants.Scope, []string{"*"}) {
		t.Fatalf("an app on the host: %+v %+v", nd.Pair, nd.Wants)
	}
	l.want(200, "socket", "POST", "/api/needs/"+nd.ID, api.NeedAnswer{Allow: false}, nil)
	// No agents (a bot): all.
	_, nd = ask(api.PairRequest{Name: "bot", Type: "bot"})
	if !slices.Equal(nd.Wants.Scope, []string{"*"}) {
		t.Fatalf("a bot: %+v", nd.Wants)
	}
	l.want(200, "socket", "POST", "/api/needs/"+nd.ID, api.NeedAnswer{Allow: false}, nil)

	// An executor connects: its agents listed, dsh marked off.
	app := l.newApp("box", false)
	exe, err := executor.New(executor.Options{URL: "ws" + strings.TrimPrefix(l.base, "http") + "/api/exec", Token: app.Token, State: t.TempDir(), Logf: t.Logf,
		Agents: []wire.AgentInfo{{Agent: "dsh", Version: "0.2.2", Argv: []string{"node", "bin.js"}}}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = exe.Run(ctx); close(done) }()
	defer func() { cancel(); <-done; exe.Close() }()
	box := func() *api.Computer {
		t.Helper()
		for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
			var as api.Apps
			l.want(200, "socket", "GET", "/api/apps", nil, &as)
			for _, a := range as.Apps {
				if a.ID == app.App.ID && a.Computer != nil && a.Computer.Online {
					return a.Computer
				}
			}
			if time.Now().After(deadline) {
				t.Fatalf("box not online: %+v", as.Apps)
			}
		}
	}
	if c := box(); len(c.Agents) != 1 || c.Agents[0].Off {
		t.Fatalf("on at first: %+v", c.Agents)
	}
	// Only who approves turns it off: not an agent, not an app.
	l.want(403, "agent", "PUT", "/api/apps/box/agents", api.AppAgents{Off: []string{"dsh"}}, nil)
	l.want(403, app.Token, "PUT", "/api/apps/box/agents", api.AppAgents{Off: []string{"dsh"}}, nil)
	l.want(422, "socket", "PUT", "/api/apps/nope/agents", api.AppAgents{Off: []string{"dsh"}}, nil)
	var got api.AppAgents
	l.want(200, "socket", "PUT", "/api/apps/box/agents", api.AppAgents{Off: []string{"dsh", "dsh", " "}}, &got)
	if !slices.Equal(got.Off, []string{"dsh"}) {
		t.Fatalf("set: %+v", got)
	}
	if c := box(); !c.Agents[0].Off {
		t.Fatalf("marked off: %+v", c.Agents)
	}
	var e api.Error
	if l.want(403, "socket", "POST", "/api/sessions", api.NewSession{Agent: api.AgentDsh, Machine: "box", Cwd: "/"}, &e); !strings.Contains(e.Message, "turned off") {
		t.Fatalf("started though off: %+v", e)
	}
	// On again: no longer refused for being off.
	l.want(200, "socket", "PUT", "/api/apps/box/agents", api.AppAgents{Off: nil}, nil)
	if c := box(); c.Agents[0].Off {
		t.Fatalf("on again: %+v", c.Agents)
	}
	if code := l.do("socket", "POST", "/api/sessions", api.NewSession{Agent: api.AgentDsh, Machine: "box", Cwd: "/"}, nil); code == 403 {
		t.Fatal("refused once on again")
	}
}
