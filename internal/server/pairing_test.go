package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/config"
	"github.com/kumoSleeping/iridium/internal/remote"
	"github.com/kumoSleeping/iridium/internal/remote/executor"
	"github.com/kumoSleeping/iridium/internal/remote/wire"
)

// public sends a request by the public host with no login (or with a TOKEN).
func (l *live) public(method, path, token string, body any, out any) int {
	l.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, l.base+path, r)
	req.Host = "ir.example.com"
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		l.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if out != nil {
		json.Unmarshal(raw, out)
	}
	return resp.StatusCode
}

// pairFrom asks to pair from source ip (CF-Connecting-IP: the real IP behind a proxy on this
// machine): the status, the pairing, the refusal.
func (l *live) pairFrom(ip string, req api.PairRequest) (int, api.Pairing, api.Error) {
	l.t.Helper()
	b, _ := json.Marshal(req)
	r, _ := http.NewRequest("POST", l.base+"/v1/internal/pair", bytes.NewReader(b))
	r.Host = "ir.example.com"
	if ip != "" {
		r.Header.Set("CF-Connecting-IP", ip)
	}
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		l.t.Fatal(err)
	}
	defer resp.Body.Close()
	var p api.Pairing
	var e api.Error
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == 200 {
		json.Unmarshal(raw, &p)
	} else {
		json.Unmarshal(raw, &e)
	}
	return resp.StatusCode, p, e
}

// pending is the one pending need, or fails.
func (l *live) pending() api.Need {
	l.t.Helper()
	var ns api.Needs
	l.want(200, "socket", "GET", "/v1/internal/needs", nil, &ns)
	if len(ns.Pending) != 1 {
		l.t.Fatalf("needs: %+v", ns)
	}
	return ns.Pending[0]
}

// The window: closed at first (403 pair_closed); the host's ir opens it for 10 minutes, or as
// long as it says (an hour at most), and closes it; an agent, an app and a login that may not
// approve do neither.
func TestPairWindow(t *testing.T) {
	t.Parallel()
	l := start(t)
	l.want(200, "socket", "PUT", "/v1/internal/public", api.SetPublic{Origin: "https://ir.example.com"}, nil)
	if code, _, e := l.pairFrom("", api.PairRequest{Name: "bot"}); code != 403 || e.Code != "pair_closed" {
		t.Fatalf("closed: %d %+v", code, e)
	}
	app := l.newApp("other", true)
	l.want(403, "agent", "PUT", "/v1/internal/pairing", api.PairWindow{Open: true}, nil)
	l.want(403, app.Token, "PUT", "/v1/internal/pairing", api.PairWindow{Open: true}, nil)
	l.want(403, "agent", "GET", "/v1/internal/pairing", nil, nil)
	var w api.PairWindow
	l.want(200, "socket", "PUT", "/v1/internal/pairing", api.PairWindow{Open: true}, &w)
	if left := time.Until(time.UnixMilli(w.Until)); !w.Open || left < 9*time.Minute || left > 10*time.Minute {
		t.Fatalf("opened: %+v", w)
	}
	l.want(200, "socket", "PUT", "/v1/internal/pairing", api.PairWindow{Open: true, Minutes: 30}, &w)
	if left := time.Until(time.UnixMilli(w.Until)); left < 29*time.Minute || left > 30*time.Minute {
		t.Fatalf("30 minutes: %+v", w)
	}
	l.want(422, "socket", "PUT", "/v1/internal/pairing", api.PairWindow{Open: true, Minutes: 61}, nil)
	if code, p, _ := l.pairFrom("", api.PairRequest{Name: "bot"}); code != 200 || len(p.Code) != 4 {
		t.Fatalf("open: %d %+v", code, p)
	}
	l.want(200, "socket", "PUT", "/v1/internal/pairing", api.PairWindow{}, &w)
	if l.want(200, "socket", "GET", "/v1/internal/pairing", nil, &w); w.Open {
		t.Fatalf("closed: %+v", w)
	}
	if code, _, e := l.pairFrom("", api.PairRequest{Name: "bot2"}); code != 403 || e.Code != "pair_closed" {
		t.Fatalf("closed again: %d %+v", code, e)
	}
	// What waits stays: it may still be allowed.
	l.pending()
}

// At most 4 pending a source (an IP; IPv6 by /64) and 20 in all; a bad name is refused at once.
func TestPairLimits(t *testing.T) {
	t.Parallel()
	l := start(t)
	l.want(200, "socket", "PUT", "/v1/internal/public", api.SetPublic{Origin: "https://ir.example.com"}, nil)
	l.s.OpenPairing(0)
	if code, _, e := l.pairFrom("", api.PairRequest{Name: "a b/c"}); code != 422 || e.Code != "bad_name" || e.NameError != "invalid" {
		t.Fatalf("a bad name: %d %+v", code, e)
	}
	if code, _, e := l.pairFrom("", api.PairRequest{Name: "主机"}); code != 422 || e.NameError != "reserved" {
		t.Fatalf("a reserved name: %d %+v", code, e)
	}
	ask := func(ip string, n int) (int, api.Error) {
		code, _, e := l.pairFrom(ip, api.PairRequest{Name: fmt.Sprintf("app%d", n)})
		return code, e
	}
	n := 0
	for i := range 4 {
		if code, e := ask("2001:db8:1:1::"+strconv.Itoa(i+1), n); code != 200 {
			t.Fatalf("ask %d: %d %+v", i, code, e)
		}
		n++
	}
	// The same /64.
	if code, e := ask("2001:db8:1:1:ffff::1", n); code != 403 || e.Code != "too_many_pending" {
		t.Fatalf("a fifth from one /64: %d %+v", code, e)
	}
	for i := 0; n < 20; i++ {
		ip := fmt.Sprintf("192.0.2.%d", i/4+1)
		if code, e := ask(ip, n); code != 200 {
			t.Fatalf("ask %d: %d %+v", n, code, e)
		}
		n++
	}
	if code, e := ask("198.51.100.1", n); code != 403 || e.Code != "too_many_pending" {
		t.Fatalf("the 21st: %d %+v", code, e)
	}
}

// An executor with no TOKEN asks to pair past the gate; the person at the host sees the code and
// allows it as the owner's own machine, by another name (an app has its name: no name is made up);
// the executor alone gets the TOKEN, once, as a new app that has Claude. Denied, it hears so.
func TestExecPair(t *testing.T) {
	t.Parallel()
	l := startWith(t, func(s *Server, _ config.Config, _ string) { s.Remote = &remote.Hub{}; s.WatchMachines() })
	l.want(200, "socket", "PUT", "/v1/internal/public", api.SetPublic{Origin: "https://ir.example.com"}, nil)
	if code := l.public("GET", "/v1/internal/agents", "", nil, nil); code != 401 {
		t.Fatalf("no login: %d", code)
	}
	l.want(200, "socket", "PUT", "/v1/internal/pairing", api.PairWindow{Open: true}, nil)
	old := l.newApp("home-pc", false)

	hello := api.PairRequest{Name: "home-pc", OS: "windows", Arch: "x64", Program: "dsh-remote-iridium 0.1.0", Agents: []api.ExecAgent{{Agent: "dsh", Version: "0.2.2"}}, AgentShell: true}
	var p api.Pairing
	if code := l.public("POST", "/v1/internal/pair", "", hello, &p); code != 200 || len(p.Code) != 4 || len(p.Request) != 32 {
		t.Fatalf("pair: %d %+v", code, p)
	}
	var st api.PairState
	if l.public("GET", "/v1/internal/pair/"+p.Request, "", nil, &st); st.Status != "pending" || st.Token != "" {
		t.Fatalf("pending: %+v", st)
	}
	if code := l.public("GET", "/v1/internal/pair/nope", "", nil, nil); code != 422 {
		t.Fatalf("no such request: %d", code)
	}
	if code := l.public("POST", "/v1/internal/exec/pair", "", hello, nil); code != 401 && code != 404 {
		t.Fatalf("the old door: %d", code)
	}
	cf := l.pending()
	if cf.Kind != api.NeedAppUpdate || cf.Code != p.Code || cf.App == nil || cf.App.ID != old.App.ID || cf.Pair == nil || cf.Pair.Type != "computer" ||
		cf.Pair.OS != "windows" || cf.Pair.Agents[0].Version != "0.2.2" || cf.Pair.Shell == nil || !*cf.Pair.Shell || cf.Pair.AgentShell {
		t.Fatalf("the card: %+v %+v", cf, cf.Pair)
	}
	// An agent may not answer; the person at the host allows it as the owner's own, by a free name.
	l.want(403, "agent", "POST", "/v1/internal/needs/"+cf.ID, api.NeedAnswer{Allow: true, Owner: true, Name: "home-pc-2"}, nil)
	var bad api.Error
	l.want(422, "socket", "POST", "/v1/internal/needs/"+cf.ID, api.NeedAnswer{Allow: true, Name: "主机"}, &bad)
	if bad.Code != "bad_name" {
		t.Fatalf("renamed to a reserved name: %+v", bad)
	}
	var ans api.NeedReply
	var raw string
	if code := l.do("socket", "POST", "/v1/internal/needs/"+cf.ID, api.NeedAnswer{Allow: true, Owner: true, Name: "home-pc-2"}, &raw); code != 200 {
		t.Fatalf("allow: %d %s", code, raw)
	}
	json.Unmarshal([]byte(raw), &ans)
	if ans.State != "allowed" || ans.Name != "home-pc-2" || strings.Contains(raw, "ir_") {
		t.Fatalf("the answer: %s", raw)
	}
	l.public("GET", "/v1/internal/pair/"+p.Request, "", nil, &st)
	if st.Status != "allowed" || !strings.HasPrefix(st.Token, "ir_") {
		t.Fatalf("allowed: %+v", st)
	}
	if code := l.public("GET", "/v1/internal/pair/"+p.Request, "", nil, nil); code != 422 {
		t.Fatalf("the TOKEN twice: %d", code)
	}
	var me api.Me
	if code := l.public("GET", "/v1/internal/me", st.Token, nil, &me); code != 200 || me.Name != "home-pc-2" || me.Rules.ClaudeOwner == nil {
		t.Fatalf("the executor's app: %d %+v", code, me)
	}
	if code := l.public("GET", "/v1/internal/me", old.Token, nil, nil); code != 200 {
		t.Fatalf("the app by the name it asked with: %d", code)
	}
	var as api.Apps
	l.want(200, "socket", "GET", "/v1/internal/apps", nil, &as)
	if i := slices.IndexFunc(as.Apps, func(a api.App) bool { return a.Name == "home-pc-2" }); i < 0 || as.Apps[i].Type != "computer" ||
		as.Apps[i].Perms == nil || !slices.Equal(as.Apps[i].Perms.Agents, []string{"*"}) {
		t.Fatalf("listed: %+v", as.Apps)
	}

	// It connects: listed with the agents Iridium runs there, online; gone, still listed, away.
	exe, err := executor.New(executor.Options{URL: "ws" + strings.TrimPrefix(l.base, "http") + "/v1/internal/exec", Token: st.Token, State: t.TempDir(), Logf: t.Logf,
		Agents: []wire.AgentInfo{{Agent: "dsh", Version: "0.2.2", Argv: []string{"node", "bin.js"}}, {Agent: "claude", Version: "2.1.318", Argv: []string{"claude"}}, {Agent: "nope", Version: "1"}}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = exe.Run(ctx); close(done) }()
	machine := func(online bool) api.App {
		t.Helper()
		for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
			var as api.Apps
			l.want(200, "socket", "GET", "/v1/internal/apps", nil, &as)
			var with []api.App
			for _, a := range as.Apps {
				if a.Computer != nil {
					with = append(with, a)
				}
			}
			if len(with) == 1 && with[0].Computer.Online == online {
				return with[0]
			}
			if time.Now().After(deadline) {
				t.Fatalf("computers, want online %v: %+v", online, with)
			}
		}
	}
	m := machine(true)
	if c := m.Computer; m.Name != "home-pc-2" || c.OS != "linux" && c.OS != "darwin" || len(c.Agents) != 2 || c.Agents[0].Agent != "dsh" || c.Agents[1].Agent != "claude" || c.Home == "" || c.SeenAt == 0 {
		t.Fatalf("the machine: %+v %+v", m, c)
	}
	// The app it made sees it in the list too, without what manages it.
	var pub api.Apps
	if l.public("GET", "/v1/internal/apps", st.Token, nil, &pub); pub.Full || len(pub.Invites) != 0 ||
		!slices.ContainsFunc(pub.Apps, func(a api.App) bool {
			return a.ID == m.ID && a.Computer != nil && a.Perms == nil && a.Rules.ClaudeOwner == nil
		}) {
		t.Fatalf("the public list: %+v", pub)
	}
	// Its folders, for the picker; one made there.
	work := t.TempDir()
	for _, d := range []string{"proj", ".hidden"} {
		if err := os.Mkdir(filepath.Join(work, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	var fl api.Folders
	l.want(200, "socket", "GET", "/v1/internal/folders?machine="+m.ID+"&path="+url.QueryEscape(work), nil, &fl)
	if fl.Path != work || !slices.Equal(fl.Folders, []string{"proj"}) || fl.Parent != filepath.Dir(work) {
		t.Fatalf("folders there: %+v", fl)
	}
	var made api.FolderReply
	l.want(200, "socket", "POST", "/v1/internal/folders", api.NewFolder{Machine: m.ID, Path: work, Name: "new"}, &made)
	if st, err := os.Stat(filepath.Join(work, "new")); err != nil || !st.IsDir() || made.Path != filepath.Join(work, "new") {
		t.Fatalf("made there: %+v %v", made, err)
	}
	l.want(409, "socket", "POST", "/v1/internal/folders", api.NewFolder{Machine: m.ID, Path: work, Name: "new"}, nil)
	l.want(400, "socket", "POST", "/v1/internal/folders", api.NewFolder{Machine: m.ID, Path: work, Name: "a/b"}, nil)
	cancel()
	<-done
	exe.Close()
	machine(false)
	l.want(409, "socket", "GET", "/v1/internal/folders?machine="+m.ID+"&path=~", nil, nil)
	// Claude: at the owner's own machine (away now: not a refusal), never at another Bot.
	if code := l.sat("socket", "channel.create", createBody(api.NewSession{Agent: api.AgentClaude, Machine: "home-pc-2", Cwd: "/"}), nil); code == 403 {
		t.Fatal("claude refused at the owner's own machine")
	}
	l.create(403, "socket", api.NewSession{Agent: api.AgentClaude, Machine: "home-pc", Cwd: "/"})

	// Denied.
	l.public("POST", "/v1/internal/pair", "", api.PairRequest{Name: "lab"}, &p)
	cf = l.pending()
	l.want(200, "socket", "POST", "/v1/internal/needs/"+cf.ID, api.NeedAnswer{Allow: false}, nil)
	st = api.PairState{}
	if l.public("GET", "/v1/internal/pair/"+p.Request, "", nil, &st); st.Status != "denied" || st.Token != "" {
		t.Fatalf("denied: %+v", st)
	}

	// Its app removed, it is not listed.
	l.want(200, "socket", "DELETE", "/v1/internal/apps/home-pc-2", nil, nil)
	var as2 api.Apps
	if l.want(200, "socket", "GET", "/v1/internal/apps", nil, &as2); slices.ContainsFunc(as2.Apps, func(a api.App) bool { return a.Computer != nil }) {
		t.Fatalf("a removed machine: %+v", as2.Apps)
	}
	l.want(404, "socket", "GET", "/v1/internal/machines", nil, nil)

}

// An app by a name there already: the person updates that one (the same app, a new TOKEN, the old
// one void, its usage and permissions as they were), or allows it by another name; a new one gets
// what it asked for or less, never more.
func TestUpdatePairing(t *testing.T) {
	t.Parallel()
	l := start(t)
	l.want(200, "socket", "PUT", "/v1/internal/public", api.SetPublic{Origin: "https://ir.example.com"}, nil)
	l.s.OpenPairing(0)
	ask := func(name string, wants *api.Perms) (api.Pairing, api.Need) {
		t.Helper()
		code, p, e := l.pairFrom("", api.PairRequest{Name: name, Type: "bot", Program: "my-bot 1.0", Wants: wants})
		if code != 200 {
			t.Fatalf("pair: %d %+v", code, e)
		}
		return p, l.pending()
	}
	token := func(p api.Pairing) string {
		t.Helper()
		var st api.PairState
		if l.public("GET", "/v1/internal/pair/"+p.Request, "", nil, &st); st.Status != "allowed" || !strings.HasPrefix(st.Token, "ir_") {
			t.Fatalf("allowed: %+v", st)
		}
		return st.Token
	}
	wants := &api.Perms{Agents: []string{"codex", "pi"}, Scope: []string{"home-pc:/work"}}
	p, cf := ask("helper", wants)
	if cf.Kind != api.NeedAppJoin || cf.Pair.Type != "bot" || cf.Pair.Program != "my-bot 1.0" || !slices.Equal(cf.Wants.Agents, []string{"codex", "pi"}) {
		t.Fatalf("the card: %+v %+v", cf, cf.Pair)
	}
	// More than it asked for: refused, it stays waiting.
	for _, more := range []api.Perms{{Agents: []string{"claude"}, Scope: []string{"home-pc:/work"}}, {Agents: []string{"codex"}, Scope: []string{"*"}}, {Agents: []string{"*"}, Scope: []string{"home-pc:/work"}}, {Agents: []string{"codex"}, Scope: []string{"home-pc:/"}}} {
		var e api.Error
		if l.want(422, "socket", "POST", "/v1/internal/needs/"+cf.ID, api.NeedAnswer{Allow: true, Perms: &more}, &e); e.Code != "too_wide" {
			t.Fatalf("more: %+v", e)
		}
	}
	less := api.Perms{Agents: []string{"codex"}, Scope: []string{"home-pc:/work/a"}}
	l.want(200, "socket", "POST", "/v1/internal/needs/"+cf.ID, api.NeedAnswer{Allow: true, Perms: &less}, nil)
	first := token(p)
	var me api.Me
	if code := l.public("GET", "/v1/internal/me", first, nil, &me); code != 200 || me.Name != "helper" || me.Rules.ClaudeOwner != nil {
		t.Fatalf("the app: %d %+v", code, me)
	}
	id := me.ID
	permsOf := func(id string) *api.Perms {
		var as api.Apps
		l.want(200, "socket", "GET", "/v1/internal/apps", nil, &as)
		for _, a := range as.Apps {
			if a.ID == id {
				return a.Perms
			}
		}
		t.Fatalf("%s not listed", id)
		return nil
	}
	if got := permsOf(id); got == nil || !slices.Equal(got.Agents, less.Agents) || !slices.Equal(got.Scope, less.Scope) {
		t.Fatalf("given: %+v", got)
	}

	// Again by the same name: an update; allowed, the same app, a new TOKEN, the old one void, what
	// it had kept though it asks for all.
	p, cf = ask("helper", nil)
	if cf.Kind != api.NeedAppUpdate || cf.App == nil || cf.App.ID != id {
		t.Fatalf("the update's card: %+v", cf)
	}
	l.want(200, "socket", "POST", "/v1/internal/needs/"+cf.ID, api.NeedAnswer{Allow: true}, nil)
	second := token(p)
	if code := l.public("GET", "/v1/internal/me", second, nil, &me); code != 200 || me.ID != id || me.Name != "helper" {
		t.Fatalf("updated: %d %+v", code, me)
	}
	if code := l.public("GET", "/v1/internal/me", first, nil, nil); code != 401 {
		t.Fatalf("the old TOKEN: %d", code)
	}
	if got := permsOf(id); got == nil || !slices.Equal(got.Agents, less.Agents) {
		t.Fatalf("kept: %+v", got)
	}

	// Allowed by another name: a new app; by one taken, refused and still waiting.
	p, cf = ask("helper", nil)
	var e api.Error
	if l.want(422, "socket", "POST", "/v1/internal/needs/"+cf.ID, api.NeedAnswer{Allow: true, Name: "HELPER"}, &e); e.NameError != "taken" {
		t.Fatalf("a taken name: %+v", e)
	}
	l.want(200, "socket", "POST", "/v1/internal/needs/"+cf.ID, api.NeedAnswer{Allow: true, Name: "helper2"}, nil)
	if l.public("GET", "/v1/internal/me", token(p), nil, &me); me.Name != "helper2" || me.ID == id {
		t.Fatalf("by another name: %+v", me)
	}
	if code := l.public("GET", "/v1/internal/me", second, nil, nil); code != 200 {
		t.Fatalf("the one by the name: %d", code)
	}

	// The app asks for other permissions: an app_change need; allowed with less, it has that.
	var pend api.Pending
	l.want(202, second, "PUT", "/v1/internal/me/perms", api.Perms{Agents: []string{"codex", "pi"}, Scope: []string{"*"}}, &pend)
	if pend.Need.Kind != api.NeedAppChange || pend.Need.Has == nil || !slices.Equal(pend.Need.Has.Agents, less.Agents) {
		t.Fatalf("the change: %+v", pend.Need)
	}
	l.want(403, "socket", "PUT", "/v1/internal/me/perms", api.Perms{}, nil)
	given := api.Perms{Agents: []string{"pi"}, Scope: []string{"*"}}
	l.want(200, "socket", "POST", "/v1/internal/needs/"+pend.Need.ID, api.NeedAnswer{Allow: true, Perms: &given}, nil)
	if got := permsOf(id); !slices.Equal(got.Agents, []string{"pi"}) || !slices.Equal(got.Scope, []string{"*"}) {
		t.Fatalf("changed: %+v", got)
	}
}

func TestPermsWithin(t *testing.T) {
	t.Parallel()
	all := api.Perms{Agents: []string{"*"}, Scope: []string{"*"}}
	some := api.Perms{Agents: []string{"codex"}, Scope: []string{"pc:/a", `win:C:\w`}}
	for _, c := range []struct {
		given, wanted api.Perms
		ok            bool
	}{
		{all, all, true}, {some, all, true}, {all, some, false},
		{api.Perms{Agents: []string{}, Scope: []string{"pc:/a/b"}}, some, true},
		{api.Perms{Agents: []string{}, Scope: []string{`win:C:\w\x`}}, some, true},
		{api.Perms{Agents: []string{}, Scope: []string{"pc:/ab"}}, some, false},
		{api.Perms{Agents: []string{"pi"}, Scope: []string{}}, some, false},
	} {
		if got := permsWithin(c.given, c.wanted); got != c.ok {
			t.Errorf("%+v within %+v: %v", c.given, c.wanted, got)
		}
	}
}

// An older executor's two switches become shell (agentShell && !noShell); a newer one's shell is
// taken as said.
func TestCleanPairShell(t *testing.T) {
	t.Parallel()
	on, off := true, false
	for _, c := range []struct {
		in   api.PairRequest
		want bool
	}{
		{api.PairRequest{NoShell: true, AgentShell: true}, false},
		{api.PairRequest{AgentShell: true}, true},
		{api.PairRequest{}, false},
		{api.PairRequest{Shell: &on, NoShell: true}, true},
		{api.PairRequest{Shell: &off, AgentShell: true}, false},
	} {
		c.in.Name = "pc"
		m := cleanPair(c.in)
		if m.Shell == nil || *m.Shell != c.want || m.NoShell || m.AgentShell {
			t.Errorf("%+v: %+v", c.in, m)
		}
	}
}

// Its Bot revoked, an executor's connection ends at once, not when it next connects, and it is
// not taken again.
func TestRevokeDropsExecutor(t *testing.T) {
	t.Parallel()
	hub := &remote.Hub{}
	l := startWith(t, func(s *Server, _ config.Config, _ string) { s.Remote = hub; s.WatchMachines() })
	l.want(200, "socket", "PUT", "/v1/internal/public", api.SetPublic{Origin: "https://ir.example.com"}, nil)
	var tok api.Token
	tok = l.newApp("pc", false)
	exe, err := executor.New(executor.Options{URL: "ws" + strings.TrimPrefix(l.base, "http") + "/v1/internal/exec", Token: tok.Token, State: t.TempDir(), Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = exe.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done; exe.Close() })
	connected := func() bool { _, ok := hub.Machine(tok.App.ID).Hello(); return ok }
	until := func(what string, ok func() bool) {
		t.Helper()
		for deadline := time.Now().Add(10 * time.Second); !ok(); time.Sleep(20 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("waiting for %s", what)
			}
		}
	}
	until("the executor", connected)
	l.want(200, "socket", "DELETE", "/v1/internal/apps/pc", nil, nil)
	until("its connection to end", func() bool { return !connected() })
	time.Sleep(1500 * time.Millisecond) // it tries again
	if connected() {
		t.Fatal("a revoked executor connected again")
	}
}

// The icons and the manifest pass the public gate with no login (a browser fetches the manifest
// without cookies); nothing else does by their names.
func TestOpenIcons(t *testing.T) {
	t.Parallel()
	l := start(t)
	l.want(200, "socket", "PUT", "/v1/internal/public", api.SetPublic{Origin: "https://ir.example.com"}, nil)
	for _, p := range []string{"/manifest.webmanifest", "/favicon.svg", "/icons/icon-192.png"} {
		if code := l.public("GET", p, "", nil, nil); code == 401 || code == 302 {
			t.Errorf("GET %s: %d, want past the gate", p, code)
		}
	}
	for _, p := range []string{"/icons/../v1/internal/agents", "/v1/internal/agents", "/"} {
		if code := l.public("GET", p, "", nil, nil); code != 401 {
			t.Errorf("GET %s: %d, want 401", p, code)
		}
	}
	if code := l.public("POST", "/manifest.webmanifest", "", nil, nil); code != 401 {
		t.Errorf("POST /manifest.webmanifest: %d, want 401", code)
	}
}

// An app may ask with the login it speaks as (ir pair --login): allowed, the login comes approved
// with it, so its first Satori request goes through and READY/meta has it. A bad login ID is
// refused at once; an update leaves a login there as it is. The host's socket asks with pairing
// shut (ir pair with no address); others still may not.
func TestPairLogin(t *testing.T) {
	t.Parallel()
	l := start(t)
	l.want(200, "socket", "PUT", "/v1/internal/public", api.SetPublic{Origin: "https://ir.example.com"}, nil)
	if code, _, e := l.pairFrom("", api.PairRequest{Name: "koishi", Login: &api.PairLogin{ID: "bot"}}); code != 403 || e.Code != "pair_closed" {
		t.Fatalf("by the public host, shut: %d %+v", code, e)
	}
	var e api.Error
	l.want(422, "socket", "POST", "/v1/internal/pair", api.PairRequest{Name: "koishi", Login: &api.PairLogin{ID: "a/b"}}, &e)
	if e.Code != "bad_login" {
		t.Fatalf("a bad login: %+v", e)
	}
	var p api.Pairing
	l.want(200, "socket", "POST", "/v1/internal/pair", api.PairRequest{Name: "koishi", Program: "Koishi", Login: &api.PairLogin{ID: "bot"}}, &p)
	nd := l.pending()
	if nd.Kind != api.NeedAppJoin || nd.Pair == nil || nd.Pair.Login == nil || nd.Pair.Login.ID != "bot" || nd.Pair.Login.Name != "bot" {
		t.Fatalf("the need: %+v", nd)
	}
	l.want(200, "socket", "POST", "/v1/internal/needs/"+nd.ID, api.NeedAnswer{Allow: true}, nil)
	var st api.PairState
	l.want(200, "socket", "GET", "/v1/internal/pair/"+p.Request, nil, &st)
	if st.Status != "allowed" || st.Token == "" {
		t.Fatalf("the state: %+v", st)
	}
	var meta struct {
		Logins []struct {
			Platform string              `json:"platform"`
			User     struct{ ID string } `json:"user"`
		} `json:"logins"`
	}
	if code := l.public("POST", "/v1/meta", st.Token, nil, &meta); code != 200 || len(meta.Logins) != 1 || meta.Logins[0].User.ID != "bot" {
		t.Fatalf("meta: %d %+v", code, meta)
	}
	hdr := http.Header{"Satori-Platform": {meta.Logins[0].Platform}, "Satori-User-ID": {"bot"}, "Authorization": {"Bearer " + st.Token}}
	if code := l.doWith("", "POST", "/v1/channel.list", map[string]any{}, nil, hdr); code != 200 {
		t.Fatalf("channel.list as bot: %d", code)
	}
	var ns api.Needs
	if l.want(200, "socket", "GET", "/v1/internal/needs", nil, &ns); len(ns.Pending) != 0 {
		t.Fatalf("no login_join need: %+v", ns.Pending)
	}
	// Paired again (an update) naming its login otherwise: bot stays as it was.
	l.want(200, "socket", "POST", "/v1/internal/pair", api.PairRequest{Name: "koishi", Login: &api.PairLogin{ID: "bot", Name: "Bot 2"}}, &p)
	nd = l.pending()
	l.want(200, "socket", "POST", "/v1/internal/needs/"+nd.ID, api.NeedAnswer{Allow: true}, nil)
	ls, err := l.s.st.Logins()
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, x := range ls {
		if x.ID == "bot" {
			n++
			if x.Name != "bot" || x.ApprovedAt.IsZero() {
				t.Fatalf("bot changed: %+v", x)
			}
		}
	}
	if n != 1 {
		t.Fatalf("logins: %+v", ls)
	}
}
