package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/config"
	"github.com/kumoSleeping/iridium/internal/remote"
	"github.com/kumoSleeping/iridium/internal/remote/executor"
)

// ir cp and files with machine:path (the socket's): a file from here to a machine's folder, back,
// a folder listed there; not over a file, not more than remoteWriteMax to a machine.
func TestCopyAcrossMachines(t *testing.T) {
	hub := &remote.Hub{}
	l := startWith(t, func(s *Server, _ config.Config, _ string) { s.Remote = hub; s.WatchMachines() })
	var tok api.Token
	tok = l.newApp("pc", false)
	there := t.TempDir()
	exe, err := executor.New(executor.Options{URL: "ws" + strings.TrimPrefix(l.base, "http") + "/api/exec", Token: tok.Token, State: filepath.Join(there, "state"), Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = exe.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done; exe.Close() })
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if _, ok := hub.Machine(tok.App.ID).Hello(); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no executor")
		}
	}
	copyLast := func(req api.Copy) (int, api.CopyProgress) {
		t.Helper()
		b, _ := json.Marshal(req)
		r, _ := http.NewRequest("POST", "http://ir/api/files/copy", bytes.NewReader(b))
		resp, err := l.sock.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var last api.CopyProgress
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			json.Unmarshal(sc.Bytes(), &last)
		}
		return resp.StatusCode, last
	}
	src := filepath.Join(l.work, "a.txt")
	os.WriteFile(src, []byte("hello there"), 0o600)
	box := filepath.Join(there, "box")
	os.MkdirAll(box, 0o700)

	// Here to a folder there: it takes the name.
	if code, res := copyLast(api.Copy{From: src, To: box, ToMachine: "pc"}); code != 200 || res.Error != "" || res.Path != filepath.Join(box, "a.txt") || res.Size != 11 {
		t.Fatalf("to pc: %d %+v", code, res)
	}
	if b, _ := os.ReadFile(filepath.Join(box, "a.txt")); string(b) != "hello there" {
		t.Errorf("there: %q", b)
	}
	// Not over a file.
	if code, _ := copyLast(api.Copy{From: src, To: box, ToMachine: "pc"}); code != http.StatusConflict {
		t.Errorf("over a file there: %d", code)
	}
	// Back here.
	back := filepath.Join(l.work, "back.txt")
	if code, res := copyLast(api.Copy{From: filepath.Join(box, "a.txt"), FromMachine: "pc", To: back}); code != 200 || res.Error != "" || res.Path != back {
		t.Fatalf("from pc: %d %+v", code, res)
	}
	if b, _ := os.ReadFile(back); string(b) != "hello there" {
		t.Errorf("here: %q", b)
	}
	// Too large for a machine, for now.
	big := filepath.Join(l.work, "big")
	os.WriteFile(big, make([]byte, remoteWriteMax+1), 0o600)
	if code, _ := copyLast(api.Copy{From: big, To: box, ToMachine: "pc"}); code != http.StatusRequestEntityTooLarge {
		t.Errorf("too large: %d", code)
	}
	// A folder there.
	var lst api.DirListing
	l.want(200, "socket", "GET", "/api/files?machine=pc&path="+box, nil, &lst)
	if len(lst.Entries) != 1 || lst.Entries[0].Name != "a.txt" || lst.Entries[0].Size != 11 {
		t.Errorf("listing there: %+v", lst)
	}
	l.want(422, "socket", "GET", "/api/files?machine=pc&path="+filepath.Join(box, "none"), nil, nil)
}
