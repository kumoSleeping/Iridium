package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/config"
	"github.com/kumoSleeping/iridium/internal/gate"
	"github.com/kumoSleeping/iridium/internal/remote"
	"github.com/kumoSleeping/iridium/internal/remote/executor"
	"github.com/kumoSleeping/iridium/internal/session"
)

// An agent's ir sh (POST /v1/internal/machines/{id}/shell): streamed through run at an executor that
// speaks it, as a holder at one that does not or when not asked so; refused where its owner keeps
// command lines off; the machine by its app's name now.
func TestMachineShell(t *testing.T) {
	t.Parallel()
	hub := &remote.Hub{}
	s, cfg, work := setupWith(t, func(o *session.Options) { o.Remote = hub })
	l := serveLive(t, s, cfg, work, func(s *Server, _ config.Config, _ string) { s.Remote = hub; s.WatchMachines() })
	join := func(name string, o executor.Options) string {
		t.Helper()
		tok := l.newApp(name, false)
		o.URL, o.Token, o.State, o.Logf = "ws"+strings.TrimPrefix(l.base, "http")+"/v1/internal/exec", tok.Token, filepath.Join(t.TempDir(), "state"), t.Logf
		exe, err := executor.New(o)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { _ = exe.Run(ctx); close(done) }()
		t.Cleanup(func() { cancel(); <-done; exe.Close() })
		for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
			if _, ok := hub.Machine(tok.App.ID).Hello(); ok {
				return tok.App.ID
			}
			if time.Now().After(deadline) {
				t.Fatal("no executor")
			}
		}
	}
	pc := join("pc", executor.Options{Shell: true})
	join("old", executor.Options{Shell: true, NoRun: true})
	join("closed", executor.Options{})

	sh := func(machine, line string, stream bool) (int, string, []api.ShellEvent) {
		t.Helper()
		b, _ := json.Marshal(api.ShellRun{Line: line})
		req, _ := http.NewRequest("POST", "http://ir/v1/internal/machines/"+machine+"/shell", bytes.NewReader(b))
		req.Header.Set(HeaderAgent, "1")
		if stream {
			req.Header.Set("Accept", ndjson)
		}
		resp, err := l.sock.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.Header.Get("Content-Type") != ndjson {
			var raw bytes.Buffer
			raw.ReadFrom(resp.Body)
			return resp.StatusCode, raw.String(), nil
		}
		var evs []api.ShellEvent
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			var ev api.ShellEvent
			json.Unmarshal(sc.Bytes(), &ev)
			evs = append(evs, ev)
		}
		return resp.StatusCode, "", evs
	}
	joined := func(evs []api.ShellEvent) (out, errs string, last api.ShellEvent) {
		for _, ev := range evs {
			out += ev.Out
			errs += ev.Err
			last = ev
		}
		return
	}

	// Streamed: its output and its code.
	code, raw, evs := sh("pc", "echo out; echo err >&2; exit 3", true)
	if out, errs, last := joined(evs); code != 200 || out != "out\n" || errs != "err\n" || !last.Done || last.Exit == nil || *last.Exit != 3 {
		t.Fatalf("streamed: %d %q %+v", code, raw, evs)
	}
	// Not asked to stream, or an executor without run: a holder, read as before.
	for _, m := range []string{"pc", "old"} {
		var st api.ShellStarted
		code, raw, _ := sh(m, "echo held", m == "old")
		if json.Unmarshal([]byte(raw), &st); code != 200 || !strings.HasPrefix(st.Run, "sh-") {
			t.Fatalf("%s as a holder: %d %s", m, code, raw)
		}
		var o api.ShellOutput
		for deadline := time.Now().Add(10 * time.Second); !o.Done && time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
			l.want(200, "socket", "GET", "/v1/internal/shell/"+st.Run, nil, &o)
		}
		if o.Data != "held\n" || o.Code == nil || *o.Code != 0 {
			t.Errorf("%s held: %+v", m, o)
		}
	}
	// Its owner keeps command lines off: no_shell, either way.
	for _, stream := range []bool{true, false} {
		if code, raw, _ := sh("closed", "id", stream); code != http.StatusForbidden || !strings.Contains(raw, "no_shell") {
			t.Errorf("shell off: %d %s", code, raw)
		}
	}
	// No such machine.
	if code, raw, _ := sh("nowhere", "id", true); code != http.StatusUnprocessableEntity || !strings.Contains(raw, "no_such_machine") {
		t.Errorf("nowhere: %d %s", code, raw)
	}
	// Renamed: found by its new name, not its old one.
	name := "mini"
	if _, err := l.s.gate().UpdateProfile(pc, gate.Profile{Name: &name}); err != nil {
		t.Fatal(err)
	}
	if code, raw, evs := sh("mini", "echo renamed", true); code != 200 || len(evs) == 0 || evs[0].Out != "renamed\n" {
		t.Errorf("by the new name: %d %s %+v", code, raw, evs)
	}
	if code, _, _ := sh("pc", "echo renamed", true); code == 200 {
		t.Error("found by the old name")
	}
}
