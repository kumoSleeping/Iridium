package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
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

// An executor with no TOKEN asks to pair past the gate; a panel sees the code and allows it as
// the owner's own machine; the executor alone gets the TOKEN, once, as a new Bot that has Claude
// and never another's name. Denied, it hears so; too many asks wait.
func TestExecPair(t *testing.T) {
	l := startWith(t, func(s *Server, _ config.Config, _ string) { s.Remote = &remote.Hub{}; s.WatchMachines() })
	l.want(200, "socket", "PUT", "/api/public", api.SetPublic{Origin: "https://ir.example.com"}, nil)
	if code := l.public("GET", "/api/sessions", "", nil, nil); code != 401 {
		t.Fatalf("no login: %d", code)
	}
	// A Bot by the machine's name is there already: the executor becomes home-pc-2.
	l.want(200, "socket", "POST", "/api/terminals/tokens", api.NewInvite{Name: "home-pc"}, nil)

	hello := api.ExecPair{Name: "home-pc", OS: "windows", Arch: "x64", Program: "dsh-remote-iridium 0.1.0", Agents: []api.ExecAgent{{Agent: "dsh", Version: "0.2.2"}}}
	var p api.ExecPairing
	if code := l.public("POST", "/api/exec/pair", "", hello, &p); code != 200 || len(p.Code) != 4 || len(p.Request) != 32 {
		t.Fatalf("pair: %d %+v", code, p)
	}
	var st api.ExecPairState
	if l.public("GET", "/api/exec/pair/"+p.Request, "", nil, &st); st.Status != "pending" || st.Token != "" {
		t.Fatalf("pending: %+v", st)
	}
	if code := l.public("GET", "/api/exec/pair/nope", "", nil, nil); code != 404 {
		t.Fatalf("no such request: %d", code)
	}
	var cs api.Confirms
	l.want(200, "socket", "GET", "/api/confirms", nil, &cs)
	if len(cs.Pending) != 1 {
		t.Fatalf("confirms: %+v", cs)
	}
	cf := cs.Pending[0]
	if cf.Kind != "exec-pair" || cf.Code != p.Code || cf.Machine == nil || cf.Machine.OS != "windows" || cf.Machine.Agents[0].Version != "0.2.2" {
		t.Fatalf("the card: %+v %+v", cf, cf.Machine)
	}
	// An agent may not answer; the person at the machine allows it as the owner's own.
	l.want(403, "agent", "POST", "/api/confirms/"+cf.ID, api.ConfirmAnswer{Allow: true, Owner: true}, nil)
	var ans api.ConfirmReply
	var raw string
	if code := l.do("socket", "POST", "/api/confirms/"+cf.ID, api.ConfirmAnswer{Allow: true, Owner: true}, &raw); code != 200 {
		t.Fatalf("allow: %d %s", code, raw)
	}
	json.Unmarshal([]byte(raw), &ans)
	if ans.State != "allowed" || ans.Name != "home-pc-2" || strings.Contains(raw, "ir_") {
		t.Fatalf("the panel's answer: %s", raw)
	}
	l.public("GET", "/api/exec/pair/"+p.Request, "", nil, &st)
	if st.Status != "allowed" || !strings.HasPrefix(st.Token, "ir_") {
		t.Fatalf("allowed: %+v", st)
	}
	if code := l.public("GET", "/api/exec/pair/"+p.Request, "", nil, nil); code != 404 {
		t.Fatalf("the TOKEN twice: %d", code)
	}
	var me api.Me
	if code := l.public("GET", "/api/me", st.Token, nil, &me); code != 200 || me.Name != "home-pc-2" || me.Rules.ClaudeOwner == nil {
		t.Fatalf("the executor's Bot: %d %+v", code, me)
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
	l.public("POST", "/api/exec/pair", "", api.ExecPair{Name: "a b/c"}, &p)
	l.want(200, "socket", "GET", "/api/confirms", nil, &cs)
	if len(cs.Pending) != 1 || cs.Pending[0].Name != "machine" {
		t.Fatalf("a bad name: %+v", cs.Pending)
	}
	l.want(200, "socket", "POST", "/api/confirms/"+cs.Pending[0].ID, api.ConfirmAnswer{Allow: false}, nil)
	st = api.ExecPairState{}
	if l.public("GET", "/api/exec/pair/"+p.Request, "", nil, &st); st.Status != "denied" || st.Token != "" {
		t.Fatalf("denied: %+v", st)
	}

	// Its Bot removed, it is not listed.
	l.want(200, "socket", "DELETE", "/api/terminals/home-pc-2", nil, nil)
	var ms []api.Machine
	if l.want(200, "socket", "GET", "/api/machines", nil, &ms); len(ms) != 0 {
		t.Fatalf("a removed machine: %+v", ms)
	}

	// Four pending a client at most.
	for i, want := range []int{200, 200, 200, 200, 429} {
		if code := l.public("POST", "/api/exec/pair", "", hello, nil); code != want {
			t.Fatalf("ask %d: %d, want %d", i, code, want)
		}
	}
}

// A Bot pairs the same way at /api/pair, with no executors here: a new Bot by its name, or, the
// panel saying so, the one by its name with a new TOKEN (the same terminal, the old TOKEN void).
// The settings' pairClosed turns every request away.
func TestBotPair(t *testing.T) {
	l := startWith(t, nil)
	l.want(200, "socket", "PUT", "/api/public", api.SetPublic{Origin: "https://ir.example.com"}, nil)
	ask := func(name string) (api.ExecPairing, api.Confirm) {
		t.Helper()
		var p api.ExecPairing
		if code := l.public("POST", "/api/pair", "", api.ExecPair{Name: name, OS: "windows", Program: "my-bot 1.0"}, &p); code != 200 || len(p.Code) != 4 {
			t.Fatalf("pair: %d %+v", code, p)
		}
		var cs api.Confirms
		l.want(200, "socket", "GET", "/api/confirms", nil, &cs)
		if len(cs.Pending) != 1 {
			t.Fatalf("confirms: %+v", cs)
		}
		return p, cs.Pending[0]
	}
	token := func(p api.ExecPairing) string {
		t.Helper()
		var st api.ExecPairState
		if l.public("GET", "/api/pair/"+p.Request, "", nil, &st); st.Status != "allowed" || !strings.HasPrefix(st.Token, "ir_") {
			t.Fatalf("allowed: %+v", st)
		}
		return st.Token
	}
	p, cf := ask("helper")
	if cf.Kind != "bot-pair" || cf.Code != p.Code || cf.Replace || cf.Machine == nil || cf.Machine.Program != "my-bot 1.0" || cf.Machine.OS != "" {
		t.Fatalf("the card: %+v %+v", cf, cf.Machine)
	}
	l.want(200, "socket", "POST", "/api/confirms/"+cf.ID, api.ConfirmAnswer{Allow: true}, nil)
	first := token(p)
	var me api.Me
	if code := l.public("GET", "/api/me", first, nil, &me); code != 200 || me.Name != "helper" || me.Rules.ClaudeOwner != nil {
		t.Fatalf("the Bot: %d %+v", code, me)
	}
	id := me.ID

	// Again by the same name: the card says so; not replaced, it is helper-2.
	p, cf = ask("helper")
	if !cf.Replace {
		t.Fatalf("no replace on the card: %+v", cf)
	}
	l.want(200, "socket", "POST", "/api/confirms/"+cf.ID, api.ConfirmAnswer{Allow: true}, nil)
	if l.public("GET", "/api/me", token(p), nil, &me); me.Name != "helper-2" {
		t.Fatalf("the second: %+v", me)
	}
	// Replaced: the same terminal, a new TOKEN, the old one void.
	p, cf = ask("helper")
	l.want(200, "socket", "POST", "/api/confirms/"+cf.ID, api.ConfirmAnswer{Allow: true, Replace: true}, nil)
	second := token(p)
	if code := l.public("GET", "/api/me", second, nil, &me); code != 200 || me.ID != id || me.Name != "helper" {
		t.Fatalf("replaced: %d %+v", code, me)
	}
	if code := l.public("GET", "/api/me", first, nil, nil); code != 401 {
		t.Fatalf("the old TOKEN: %d", code)
	}

	// Closed: turned away, executors too; open again, taken.
	closed, open := true, false
	l.want(200, "socket", "PUT", "/api/settings", api.SettingsPatch{PairClosed: &closed}, nil)
	var set api.Settings
	if l.want(200, "socket", "GET", "/api/settings", nil, &set); !set.PairClosed {
		t.Fatalf("settings: %+v", set)
	}
	if code := l.public("POST", "/api/pair", "", api.ExecPair{Name: "x"}, nil); code != 403 {
		t.Fatalf("closed: %d", code)
	}
	l.want(200, "socket", "PUT", "/api/settings", api.SettingsPatch{PairClosed: &open}, nil)
	if code := l.public("POST", "/api/pair", "", api.ExecPair{Name: "x"}, nil); code != 200 {
		t.Fatalf("open again: %d", code)
	}
}
