package server

import (
	"slices"
	"strings"
	"testing"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/config"
	"github.com/kumoSleeping/iridium/internal/remote"
)

// A computer's owner (kumo, 2026-11-23): the login of ir_web that allowed its pairing, listed
// with it by id and name; set or cleared later by a person; none for an app not a computer. Its
// online is the app's, one meaning, written out false too.
func TestComputerOwner(t *testing.T) {
	t.Parallel()
	l := startWith(t, func(s *Server, _ config.Config, _ string) {
		s.Remote = &remote.Hub{Self: "host-hash"}
		s.WatchMachines()
	})
	l.want(200, "socket", "PUT", "/v1/internal/public", api.SetPublic{Origin: "https://ir.example.com"}, nil)
	l.s.SetPairing(true)
	kumo, err := l.s.gate().CreateWebLogin("kumo")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.s.gate().CreateWebLogin("otae"); err != nil {
		t.Fatal(err)
	}
	pair := func(req api.PairRequest, pairer string) api.App {
		t.Helper()
		if code, _, e := l.pairFrom("", req); code != 200 {
			t.Fatalf("pair: %d %+v", code, e)
		}
		nd := l.pending()
		l.s.asks.take(nd.ID)
		tok, err := l.s.pairAllowed(nd, api.NeedAnswer{Allow: true}, pairer)
		if err != nil {
			t.Fatal(err)
		}
		return tok.App
	}
	pc := pair(api.PairRequest{Name: "mac", OS: "darwin", Home: "/Users/kumo", MachineID: "mac-hash"}, kumo.ID)
	bot := pair(api.PairRequest{Name: "qq", Type: "bot"}, kumo.ID)
	if err := l.s.st.SetMachine(pc.ID, mustJSON(keptComputer{Computer: api.Computer{OS: "darwin", Home: "/Users/kumo", Sep: "/", Agents: []api.ExecAgent{}}, MachineID: "mac-hash"})); err != nil {
		t.Fatal(err)
	}

	listed := func(id string) api.App {
		t.Helper()
		var as api.Apps
		l.want(200, "socket", "GET", "/v1/internal/apps", nil, &as)
		i := slices.IndexFunc(as.Apps, func(a api.App) bool { return a.ID == id })
		if i < 0 {
			t.Fatalf("%s not listed: %+v", id, as.Apps)
		}
		return as.Apps[i]
	}
	if a := listed(pc.ID); a.Owner == nil || a.Owner.ID != kumo.ID || a.Owner.Name != "kumo" || a.Type != api.TypeComputer || a.Online {
		t.Fatalf("paired by kumo: %+v %+v", a, a.Owner)
	}
	if a := listed(bot.ID); a.Owner != nil {
		t.Fatalf("a bot has no owner: %+v", a.Owner)
	}
	var raw string
	l.do("socket", "GET", "/v1/internal/apps", nil, &raw)
	if !strings.Contains(raw, `"online":false`) {
		t.Fatalf("online false not written out: %s", raw)
	}

	// Given to another by name, then nobody's; refused for a bot and for no such login.
	l.want(200, "socket", "PUT", "/v1/internal/apps/mac/owner", api.SetOwner{Login: "otae"}, nil)
	if a := listed(pc.ID); a.Owner == nil || a.Owner.Name != "otae" {
		t.Fatalf("otae's: %+v", a.Owner)
	}
	l.want(200, "socket", "PUT", "/v1/internal/apps/mac/owner", api.SetOwner{Clear: true}, nil)
	if a := listed(pc.ID); a.Owner != nil {
		t.Fatalf("nobody's: %+v", a.Owner)
	}
	l.want(422, "socket", "PUT", "/v1/internal/apps/qq/owner", api.SetOwner{Login: "kumo"}, nil)
	l.want(422, "socket", "PUT", "/v1/internal/apps/mac/owner", api.SetOwner{Login: "nobody"}, nil)
	// The host's ir names no login of its own.
	l.want(422, "socket", "PUT", "/v1/internal/apps/mac/owner", api.SetOwner{}, nil)
}
