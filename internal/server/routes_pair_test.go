package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/config"
	"github.com/kumoSleeping/iridium/internal/remote"
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
	l := startWith(t, func(s *Server, _ config.Config, _ string) { s.Remote = &remote.Hub{} })
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

	// Two pending a client at most.
	for i, want := range []int{200, 200, 429} {
		if code := l.public("POST", "/api/exec/pair", "", hello, nil); code != want {
			t.Fatalf("ask %d: %d, want %d", i, code, want)
		}
	}
}
