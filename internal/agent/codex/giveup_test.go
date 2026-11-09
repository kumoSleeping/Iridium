package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/agent"
)

// A thread that does not open (here: held by another writer, Rhodium resume-locked) leaves the
// process nothing to do: the driver gives it up, saying why, and messages fail.
func TestGiveUpWhenTheThreadDoesNotOpen(t *testing.T) {
	var mu sync.Mutex
	var events []agent.Event
	d := New(Options{Emit: func(e agent.Event) { mu.Lock(); events = append(events, e); mu.Unlock() }, Logf: t.Logf})
	bin := t.TempDir()
	os.WriteFile(filepath.Join(bin, "codex"), []byte("#!/bin/sh\n"), 0o755)
	if _, err := d.Spawn(agent.SpawnOptions{Session: "s1", Cwd: "/w", Env: []string{"PATH=" + bin}, Program: "codex", Resume: true, AgentSession: "t1"}); err != nil {
		t.Fatal(err)
	}
	d.Attach(func(line []byte) error {
		var m map[string]any
		json.Unmarshal(line, &m)
		res := ""
		switch m["method"] {
		case "initialize":
			res = `"result":{"userAgent":"iridium/0.165.0 (Linux)"}`
		case "model/list":
			res = `"result":{"data":[]}`
		case "thread/resume":
			res = `"error":{"code":-32600,"message":"thread t1 already has an active writer"}`
		default:
			return nil
		}
		go d.Feed([]byte(fmt.Sprintf(`{"id":%q,%s}`, m["id"], res)), false)
		return nil
	}, 1)
	var why string
	for deadline := time.Now().Add(3 * time.Second); why == "" && time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		mu.Lock()
		for _, e := range events {
			if g, ok := e.(agent.GiveUp); ok {
				why = g.Why
			}
		}
		mu.Unlock()
	}
	if !strings.Contains(why, "another program has the thread open") {
		t.Fatalf("gave up: %q", why)
	}
	if err := d.Send(context.Background(), agent.Message{ID: "m1", Text: "hi"}); err == nil || !strings.Contains(err.Error(), "active writer") {
		t.Errorf("send: %v", err)
	}
}

// A process that goes during the handshake (stopped, or it exits: Detach) ends the handshake
// quietly; the next process's handshake is its own.
func TestDetachDuringHandshake(t *testing.T) {
	started := make(chan string, 4)
	d := New(Options{Logf: t.Logf, Emit: func(e agent.Event) {
		if s, ok := e.(agent.Started); ok {
			started <- s.Session
		}
	}})
	if _, err := d.Spawn(agent.SpawnOptions{Session: "s1", Cwd: "/w", Remote: &agent.Remote{Argv: []string{"codex"}}}); err != nil {
		t.Fatal(err)
	}
	asked := make(chan struct{}, 1)
	d.Attach(func([]byte) error { asked <- struct{}{}; return nil }, 1) // never answered
	<-asked
	d.Detach()
	time.Sleep(50 * time.Millisecond) // the handshake's goroutine sees it fail
	if _, err := d.Spawn(agent.SpawnOptions{Session: "s1", Cwd: "/w", Remote: &agent.Remote{Argv: []string{"codex"}}}); err != nil {
		t.Fatal(err)
	}
	d.Attach(func(line []byte) error {
		var m map[string]any
		json.Unmarshal(line, &m)
		res := map[string]string{"initialize": `{"userAgent":"codex/0.165.0"}`, "model/list": `{"data":[]}`, "thread/start": `{"thread":{"id":"t2"}}`}[fmt.Sprint(m["method"])]
		if res != "" {
			go d.Feed([]byte(fmt.Sprintf(`{"id":%q,"result":%s}`, m["id"], res)), false)
		}
		return nil
	}, 2)
	select {
	case s := <-started:
		if s != "t2" {
			t.Errorf("the next process's thread: %q", s)
		}
	case <-time.After(3 * time.Second):
		t.Error("the next process's thread did not open")
	}
}
