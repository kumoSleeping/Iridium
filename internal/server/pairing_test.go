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
	r, _ := http.NewRequest("POST", l.base+"/api/pair", bytes.NewReader(b))
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
	l.want(200, "socket", "GET", "/api/needs", nil, &ns)
	if len(ns.Pending) != 1 {
		l.t.Fatalf("needs: %+v", ns)
	}
	return ns.Pending[0]
}

// The window: closed at first (403 pair_closed); the host's ir opens it for 10 minutes, or as
// long as it says (an hour at most), and closes it; an agent, an app and a login that may not
// approve do neither.
func TestPairWindow(t *testing.T) {
	l := start(t)
	l.want(200, "socket", "PUT", "/api/public", api.SetPublic{Origin: "https://ir.example.com"}, nil)
	if code, _, e := l.pairFrom("", api.PairRequest{Name: "bot"}); code != 403 || e.Code != "pair_closed" {
		t.Fatalf("closed: %d %+v", code, e)
	}
	app := l.newApp("other", true)
	l.want(403, "agent", "PUT", "/api/pairing", api.PairWindow{Open: true}, nil)
	l.want(403, app.Token, "PUT", "/api/pairing", api.PairWindow{Open: true}, nil)
	l.want(403, "agent", "GET", "/api/pairing", nil, nil)
	var w api.PairWindow
	l.want(200, "socket", "PUT", "/api/pairing", api.PairWindow{Open: true}, &w)
	if left := time.Until(time.UnixMilli(w.Until)); !w.Open || left < 9*time.Minute || left > 10*time.Minute {
		t.Fatalf("opened: %+v", w)
	}
	l.want(200, "socket", "PUT", "/api/pairing", api.PairWindow{Open: true, Minutes: 30}, &w)
	if left := time.Until(time.UnixMilli(w.Until)); left < 29*time.Minute || left > 30*time.Minute {
		t.Fatalf("30 minutes: %+v", w)
	}
	l.want(422, "socket", "PUT", "/api/pairing", api.PairWindow{Open: true, Minutes: 61}, nil)
	if code, p, _ := l.pairFrom("", api.PairRequest{Name: "bot"}); code != 200 || len(p.Code) != 4 {
		t.Fatalf("open: %d %+v", code, p)
	}
	l.want(200, "socket", "PUT", "/api/pairing", api.PairWindow{}, &w)
	if l.want(200, "socket", "GET", "/api/pairing", nil, &w); w.Open {
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
	l := start(t)
	l.want(200, "socket", "PUT", "/api/public", api.SetPublic{Origin: "https://ir.example.com"}, nil)
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
	l := startWith(t, func(s *Server, _ config.Config, _ string) { s.Remote = &remote.Hub{}; s.WatchMachines() })
	l.want(200, "socket", "PUT", "/api/public", api.SetPublic{Origin: "https://ir.example.com"}, nil)
	if code := l.public("GET", "/api/sessions", "", nil, nil); code != 401 {
		t.Fatalf("no login: %d", code)
	}
	l.want(200, "socket", "PUT", "/api/pairing", api.PairWindow{Open: true}, nil)
	old := l.newApp("home-pc", false)

	hello := api.PairRequest{Name: "home-pc", OS: "windows", Arch: "x64", Program: "dsh-remote-iridium 0.1.0", Agents: []api.ExecAgent{{Agent: "dsh", Version: "0.2.2"}}, AgentShell: true}
	var p api.Pairing
	if code := l.public("POST", "/api/pair", "", hello, &p); code != 200 || len(p.Code) != 4 || len(p.Request) != 32 {
		t.Fatalf("pair: %d %+v", code, p)
	}
	var st api.PairState
	if l.public("GET", "/api/pair/"+p.Request, "", nil, &st); st.Status != "pending" || st.Token != "" {
		t.Fatalf("pending: %+v", st)
	}
	if code := l.public("GET", "/api/pair/nope", "", nil, nil); code != 422 {
		t.Fatalf("no such request: %d", code)
	}
	if code := l.public("POST", "/api/exec/pair", "", hello, nil); code != 401 && code != 404 {
		t.Fatalf("the old door: %d", code)
	}
	cf := l.pending()
	if cf.Kind != api.NeedAppUpdate || cf.Code != p.Code || cf.App == nil || cf.App.ID != old.App.ID || cf.Pair == nil || cf.Pair.Type != "computer" ||
		cf.Pair.OS != "windows" || cf.Pair.Agents[0].Version != "0.2.2" || !cf.Pair.AgentShell || cf.Pair.NoShell {
		t.Fatalf("the card: %+v %+v", cf, cf.Pair)
	}
	// An agent may not answer; the person at the host allows it as the owner's own, by a free name.
	l.want(403, "agent", "POST", "/api/needs/"+cf.ID, api.NeedAnswer{Allow: true, Owner: true, Name: "home-pc-2"}, nil)
	var bad api.Error
	l.want(422, "socket", "POST", "/api/needs/"+cf.ID, api.NeedAnswer{Allow: true, Name: "主机"}, &bad)
	if bad.Code != "bad_name" {
		t.Fatalf("renamed to a reserved name: %+v", bad)
	}
	var ans api.NeedReply
	var raw string
	if code := l.do("socket", "POST", "/api/needs/"+cf.ID, api.NeedAnswer{Allow: true, Owner: true, Name: "home-pc-2"}, &raw); code != 200 {
		t.Fatalf("allow: %d %s", code, raw)
	}
	json.Unmarshal([]byte(raw), &ans)
	if ans.State != "allowed" || ans.Name != "home-pc-2" || strings.Contains(raw, "ir_") {
		t.Fatalf("the answer: %s", raw)
	}
	l.public("GET", "/api/pair/"+p.Request, "", nil, &st)
	if st.Status != "allowed" || !strings.HasPrefix(st.Token, "ir_") {
		t.Fatalf("allowed: %+v", st)
	}
	if code := l.public("GET", "/api/pair/"+p.Request, "", nil, nil); code != 422 {
		t.Fatalf("the TOKEN twice: %d", code)
	}
	var me api.Me
	if code := l.public("GET", "/api/me", st.Token, nil, &me); code != 200 || me.Name != "home-pc-2" || me.Rules.ClaudeOwner == nil {
		t.Fatalf("the executor's app: %d %+v", code, me)
	}
	if code := l.public("GET", "/api/me", old.Token, nil, nil); code != 200 {
		t.Fatalf("the app by the name it asked with: %d", code)
	}
	var as api.Apps
	l.want(200, "socket", "GET", "/api/apps", nil, &as)
	if i := slices.IndexFunc(as.Apps, func(a api.App) bool { return a.Name == "home-pc-2" }); i < 0 || as.Apps[i].Type != "computer" ||
		as.Apps[i].Perms == nil || !slices.Equal(as.Apps[i].Perms.Agents, []string{"*"}) {
		t.Fatalf("listed: %+v", as.Apps)
	}

	// It connects: listed with the agents Iridium runs there, online; gone, still listed, away.
	exe, err := executor.New(executor.Options{URL: "ws" + strings.TrimPrefix(l.base, "http") + "/api/exec", Token: st.Token, State: t.TempDir(), Logf: t.Logf,
		Agents: []wire.AgentInfo{{Agent: "dsh", Version: "0.2.2", Argv: []string{"node", "bin.js"}}, {Agent: "claude", Version: "2.1.318", Argv: []string{"claude"}}, {Agent: "nope", Version: "1"}}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = exe.Run(ctx); close(done) }()
	machine := func(online bool) api.Machine {
		t.Helper()
		for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
			var ms []api.Machine
			l.want(200, "socket", "GET", "/api/machines", nil, &ms)
			if len(ms) == 1 && ms[0].Online == online {
				return ms[0]
			}
			if time.Now().After(deadline) {
				t.Fatalf("machines, want online %v: %+v", online, ms)
			}
		}
	}
	m := machine(true)
	if m.Name != "home-pc-2" || m.OS != "linux" && m.OS != "darwin" || len(m.Agents) != 2 || m.Agents[0].Agent != "dsh" || m.Agents[1].Agent != "claude" || m.Home == "" || m.SeenAt == 0 {
		t.Fatalf("the machine: %+v", m)
	}
	// Its folders, for the picker; one made there.
	work := t.TempDir()
	for _, d := range []string{"proj", ".hidden"} {
		if err := os.Mkdir(filepath.Join(work, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	var fl api.Folders
	l.want(200, "socket", "GET", "/api/folders?machine="+m.ID+"&path="+url.QueryEscape(work), nil, &fl)
	if fl.Path != work || !slices.Equal(fl.Folders, []string{"proj"}) || fl.Parent != filepath.Dir(work) {
		t.Fatalf("folders there: %+v", fl)
	}
	var made api.FolderReply
	l.want(200, "socket", "POST", "/api/folders", api.NewFolder{Machine: m.ID, Path: work, Name: "new"}, &made)
	if st, err := os.Stat(filepath.Join(work, "new")); err != nil || !st.IsDir() || made.Path != filepath.Join(work, "new") {
		t.Fatalf("made there: %+v %v", made, err)
	}
	l.want(409, "socket", "POST", "/api/folders", api.NewFolder{Machine: m.ID, Path: work, Name: "new"}, nil)
	l.want(400, "socket", "POST", "/api/folders", api.NewFolder{Machine: m.ID, Path: work, Name: "a/b"}, nil)
	cancel()
	<-done
	exe.Close()
	machine(false)
	l.want(409, "socket", "GET", "/api/folders?machine="+m.ID+"&path=~", nil, nil)
	// Claude: at the owner's own machine (away now: not a refusal), never at another Bot.
	if code := l.do("socket", "POST", "/api/sessions", api.NewSession{Agent: api.AgentClaude, Machine: "home-pc-2", Cwd: "/"}, nil); code == 403 {
		t.Fatal("claude refused at the owner's own machine")
	}
	l.want(403, "socket", "POST", "/api/sessions", api.NewSession{Agent: api.AgentClaude, Machine: "home-pc", Cwd: "/"}, nil)

	// Denied.
	l.public("POST", "/api/pair", "", api.PairRequest{Name: "lab"}, &p)
	cf = l.pending()
	l.want(200, "socket", "POST", "/api/needs/"+cf.ID, api.NeedAnswer{Allow: false}, nil)
	st = api.PairState{}
	if l.public("GET", "/api/pair/"+p.Request, "", nil, &st); st.Status != "denied" || st.Token != "" {
		t.Fatalf("denied: %+v", st)
	}

	// Its app removed, it is not listed.
	l.want(200, "socket", "DELETE", "/api/apps/home-pc-2", nil, nil)
	var ms []api.Machine
	if l.want(200, "socket", "GET", "/api/machines", nil, &ms); len(ms) != 0 {
		t.Fatalf("a removed machine: %+v", ms)
	}

}

// An app by a name there already: the person updates that one (the same app, a new TOKEN, the old
// one void, its usage and permissions as they were), or allows it by another name; a new one gets
// what it asked for or less, never more.
func TestUpdatePairing(t *testing.T) {
	l := start(t)
	l.want(200, "socket", "PUT", "/api/public", api.SetPublic{Origin: "https://ir.example.com"}, nil)
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
		if l.public("GET", "/api/pair/"+p.Request, "", nil, &st); st.Status != "allowed" || !strings.HasPrefix(st.Token, "ir_") {
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
		if l.want(422, "socket", "POST", "/api/needs/"+cf.ID, api.NeedAnswer{Allow: true, Perms: &more}, &e); e.Code != "too_wide" {
			t.Fatalf("more: %+v", e)
		}
	}
	less := api.Perms{Agents: []string{"codex"}, Scope: []string{"home-pc:/work/a"}}
	l.want(200, "socket", "POST", "/api/needs/"+cf.ID, api.NeedAnswer{Allow: true, Perms: &less}, nil)
	first := token(p)
	var me api.Me
	if code := l.public("GET", "/api/me", first, nil, &me); code != 200 || me.Name != "helper" || me.Rules.ClaudeOwner != nil {
		t.Fatalf("the app: %d %+v", code, me)
	}
	id := me.ID
	permsOf := func(id string) *api.Perms {
		var as api.Apps
		l.want(200, "socket", "GET", "/api/apps", nil, &as)
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
	l.want(200, "socket", "POST", "/api/needs/"+cf.ID, api.NeedAnswer{Allow: true}, nil)
	second := token(p)
	if code := l.public("GET", "/api/me", second, nil, &me); code != 200 || me.ID != id || me.Name != "helper" {
		t.Fatalf("updated: %d %+v", code, me)
	}
	if code := l.public("GET", "/api/me", first, nil, nil); code != 401 {
		t.Fatalf("the old TOKEN: %d", code)
	}
	if got := permsOf(id); got == nil || !slices.Equal(got.Agents, less.Agents) {
		t.Fatalf("kept: %+v", got)
	}

	// Allowed by another name: a new app; by one taken, refused and still waiting.
	p, cf = ask("helper", nil)
	var e api.Error
	if l.want(422, "socket", "POST", "/api/needs/"+cf.ID, api.NeedAnswer{Allow: true, Name: "HELPER"}, &e); e.NameError != "taken" {
		t.Fatalf("a taken name: %+v", e)
	}
	l.want(200, "socket", "POST", "/api/needs/"+cf.ID, api.NeedAnswer{Allow: true, Name: "helper2"}, nil)
	if l.public("GET", "/api/me", token(p), nil, &me); me.Name != "helper2" || me.ID == id {
		t.Fatalf("by another name: %+v", me)
	}
	if code := l.public("GET", "/api/me", second, nil, nil); code != 200 {
		t.Fatalf("the one by the name: %d", code)
	}

	// The app asks for other permissions: an app_change need; allowed with less, it has that.
	var pend api.Pending
	l.want(202, second, "PUT", "/api/me/perms", api.Perms{Agents: []string{"codex", "pi"}, Scope: []string{"*"}}, &pend)
	if pend.Need.Kind != api.NeedAppChange || pend.Need.Has == nil || !slices.Equal(pend.Need.Has.Agents, less.Agents) {
		t.Fatalf("the change: %+v", pend.Need)
	}
	l.want(403, "socket", "PUT", "/api/me/perms", api.Perms{}, nil)
	given := api.Perms{Agents: []string{"pi"}, Scope: []string{"*"}}
	l.want(200, "socket", "POST", "/api/needs/"+pend.Need.ID, api.NeedAnswer{Allow: true, Perms: &given}, nil)
	if got := permsOf(id); !slices.Equal(got.Agents, []string{"pi"}) || !slices.Equal(got.Scope, []string{"*"}) {
		t.Fatalf("changed: %+v", got)
	}
}

func TestPermsWithin(t *testing.T) {
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

// A machine that starts only its agents runs no agent's command line either.
func TestCleanPairShell(t *testing.T) {
	m := cleanPair(api.PairRequest{Name: "pc", NoShell: true, AgentShell: true})
	if !m.NoShell || m.AgentShell {
		t.Fatalf("%+v", m)
	}
}

// Its Bot revoked, an executor's connection ends at once, not when it next connects, and it is
// not taken again.
func TestRevokeDropsExecutor(t *testing.T) {
	hub := &remote.Hub{}
	l := startWith(t, func(s *Server, _ config.Config, _ string) { s.Remote = hub; s.WatchMachines() })
	l.want(200, "socket", "PUT", "/api/public", api.SetPublic{Origin: "https://ir.example.com"}, nil)
	var tok api.Token
	tok = l.newApp("pc", false)
	exe, err := executor.New(executor.Options{URL: "ws" + strings.TrimPrefix(l.base, "http") + "/api/exec", Token: tok.Token, State: t.TempDir(), Logf: t.Logf})
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
	l.want(200, "socket", "DELETE", "/api/apps/pc", nil, nil)
	until("its connection to end", func() bool { return !connected() })
	time.Sleep(1500 * time.Millisecond) // it tries again
	if connected() {
		t.Fatal("a revoked executor connected again")
	}
}

// The icons and the manifest pass the public gate with no login (a browser fetches the manifest
// without cookies); nothing else does by their names.
func TestOpenIcons(t *testing.T) {
	l := start(t)
	l.want(200, "socket", "PUT", "/api/public", api.SetPublic{Origin: "https://ir.example.com"}, nil)
	for _, p := range []string{"/manifest.webmanifest", "/favicon.svg", "/icons/icon-192.png"} {
		if code := l.public("GET", p, "", nil, nil); code == 401 || code == 302 {
			t.Errorf("GET %s: %d, want past the gate", p, code)
		}
	}
	for _, p := range []string{"/icons/../api/sessions", "/api/sessions", "/"} {
		if code := l.public("GET", p, "", nil, nil); code != 401 {
			t.Errorf("GET %s: %d, want 401", p, code)
		}
	}
	if code := l.public("POST", "/manifest.webmanifest", "", nil, nil); code != 401 {
		t.Errorf("POST /manifest.webmanifest: %d, want 401", code)
	}
}
