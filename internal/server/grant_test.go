package server

import (
	"context"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/config"
	"github.com/kumoSleeping/iridium/internal/remote"
	"github.com/kumoSleeping/iridium/internal/remote/executor"
	"github.com/kumoSleeping/iridium/internal/session"
)

// What a computer turns down for an agent becomes a card in its session and a line at its
// terminal; allowed, the computer asks the person there, and only a yes widens it (docs/exec.md
// "放开"). A person widens or narrows it from its page too.
func TestMachineGrant(t *testing.T) {
	t.Parallel()
	hub := &remote.Hub{}
	s, cfg, work := setupWith(t, func(o *session.Options) { o.Remote = hub })
	l := serveLive(t, s, cfg, work, func(s *Server, _ config.Config, _ string) { s.Remote = hub; s.WatchMachines() })
	root, other := t.TempDir(), t.TempDir()

	var mu sync.Mutex
	var told []executor.Denial
	var asked []string
	answer := true
	tok := l.newApp("box")
	exe, err := executor.New(executor.Options{
		URL: "ws" + strings.TrimPrefix(l.base, "http") + "/v1/internal/exec", Token: tok.Token, State: filepath.Join(t.TempDir(), "state"), Logf: t.Logf,
		Roots: []string{root},
		Ask: func(_ context.Context, q string) bool {
			mu.Lock()
			defer mu.Unlock()
			asked = append(asked, q)
			return answer
		},
		Denied: func(d executor.Denial) {
			mu.Lock()
			defer mu.Unlock()
			told = append(told, d)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = exe.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done; exe.Close() })
	box := tok.App.ID
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if _, ok := hub.Machine(box).Hello(); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no executor")
		}
	}
	// Its hello reaches the list a moment after the hub has it: waited for.
	computer := func() *api.Computer {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
			var apps api.Apps
			l.want(200, "socket", "GET", "/v1/internal/apps", nil, &apps)
			for _, a := range apps.Apps {
				if a.ID == box && a.Computer != nil {
					return a.Computer
				}
			}
			if time.Now().After(deadline) {
				t.Fatal("no box")
			}
		}
	}
	if c := computer(); c.Shell || !c.Grant || !slices.Equal(c.Roots, []string{root}) {
		t.Fatalf("listed %+v", c)
	}
	grants := func() []api.Need {
		var ns api.Needs
		l.want(200, "socket", "GET", "/v1/internal/needs", nil, &ns)
		return slices.DeleteFunc(ns.Pending, func(n api.Need) bool { return n.Kind != api.NeedMachineGrant })
	}

	// An agent's command line there, command lines off: refused, a card in its session (once,
	// however often it tries), a line at the computer's terminal.
	for range 2 {
		var raw string
		if code := l.do("agent-in:s1", "POST", "/v1/internal/machines/box/shell", api.ShellRun{Line: "id"}, &raw); code != http.StatusForbidden || !strings.Contains(raw, "no_shell") || !strings.Contains(raw, "card in this session") {
			t.Fatalf("shell off: %d %s", code, raw)
		}
	}
	ns := grants()
	if len(ns) != 1 || ns[0].Session != "s1" || ns[0].Machine != box || !ns[0].Shell || ns[0].Path != "" || ns[0].Name != "box" {
		t.Fatalf("cards %+v", ns)
	}
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		mu.Lock()
		n := len(told)
		mu.Unlock()
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the terminal was not told")
		}
	}
	mu.Lock()
	if told[0].What != executor.DeniedShell || !strings.Contains(told[0].By, "s1") {
		t.Errorf("told %+v", told)
	}
	mu.Unlock()

	// Allowed on the page, yes at the computer: command lines run there.
	var st api.NeedReply
	l.want(200, "socket", "POST", "/v1/internal/needs/"+ns[0].ID, api.NeedAnswer{Allow: true}, &st)
	if st.State != "allowed" || len(asked) != 1 || !strings.Contains(asked[0], "命令行") {
		t.Fatalf("allowed: %+v, asked %q", st, asked)
	}
	if c := computer(); !c.Shell {
		t.Fatalf("listed %+v", c)
	}
	if hi, _ := hub.Machine(box).Hello(); !hi.ShellOn() {
		t.Fatal("the hello kept says no shell")
	}

	// A file outside its folders: refused with the reason, a card for its folder.
	var raw string
	if code := l.do("agent-in:s1", "GET", "/v1/internal/files?machine=box&path="+url.QueryEscape(other), nil, &raw); code != http.StatusForbidden || !strings.Contains(raw, "not under the folders") {
		t.Fatalf("outside: %d %s", code, raw)
	}
	ns = grants()
	if len(ns) != 1 || ns[0].Shell || ns[0].Path != other {
		t.Fatalf("cards %+v", ns)
	}
	// Declined at the computer: the card ends denied, nothing changes.
	mu.Lock()
	answer = false
	mu.Unlock()
	l.want(200, "socket", "POST", "/v1/internal/needs/"+ns[0].ID, api.NeedAnswer{Allow: true}, &st)
	if st.State != "denied" || len(computer().Roots) != 1 {
		t.Fatalf("declined: %+v %+v", st, computer())
	}

	// From its page: a folder (asked there), command lines off (not asked).
	mu.Lock()
	answer = true
	mu.Unlock()
	var c api.Computer
	l.want(200, "socket", "POST", "/v1/internal/machines/box/grant", api.MachineGrant{Roots: []string{other}}, &c)
	if !slices.Equal(c.Roots, []string{root, other}) {
		t.Fatalf("roots %+v", c)
	}
	if code := l.do("agent-in:s1", "GET", "/v1/internal/files?machine=box&path="+url.QueryEscape(other), nil, &raw); code != 200 {
		t.Fatalf("after: %d %s", code, raw)
	}
	n := len(asked)
	off := false
	c = api.Computer{}
	l.want(200, "socket", "POST", "/v1/internal/machines/box/grant", api.MachineGrant{Shell: &off}, &c)
	if c.Shell || len(asked) != n {
		t.Fatalf("off: %+v, asked %d more", c, len(asked)-n)
	}
	mu.Lock()
	answer = false
	mu.Unlock()
	on := true
	var refusal api.Error
	l.want(http.StatusForbidden, "socket", "POST", "/v1/internal/machines/box/grant", api.MachineGrant{Shell: &on}, &refusal)
	if refusal.Code != "declined" {
		t.Fatalf("declined: %+v", refusal)
	}
	l.want(http.StatusUnprocessableEntity, "socket", "POST", "/v1/internal/machines/box/grant", api.MachineGrant{Roots: []string{"rel"}}, nil)
	// Not a person who may approve: refused.
	l.want(http.StatusForbidden, "agent-in:s1", "POST", "/v1/internal/machines/box/grant", api.MachineGrant{Shell: &on}, nil)
}
