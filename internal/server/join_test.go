package server

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/config"
	"github.com/kumoSleeping/iridium/internal/join"
	"github.com/kumoSleeping/iridium/internal/remote"
	"github.com/kumoSleeping/iridium/internal/remote/wire"
)

// asHost sends every request by the public host name.
type asHost string

func (h asHost) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Host = string(h)
	return http.DefaultTransport.RoundTrip(r)
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

// ir join, end to end: a machine with dsh on its PATH pairs with a public Iridium, a panel allows
// it, and it connects with its agents; run again it connects with the TOKEN it kept; once its Bot
// is removed, a resident run forgets the TOKEN and ends.
func TestJoin(t *testing.T) {
	executors := &remote.Hub{Logf: t.Logf}
	l := startWith(t, func(s *Server, _ config.Config, _ string) { s.Remote = executors })
	l.want(200, "socket", "PUT", "/api/public", api.SetPublic{Origin: "https://ir.example.com"}, nil)

	// dsh as npm installs it on Unix: a link on PATH to the package's bin.js.
	prefix := t.TempDir()
	pkg := filepath.Join(prefix, "lib", "node_modules", "@deepseek-ai", "dsh")
	for path, data := range map[string]string{
		filepath.Join(pkg, "package.json"):  `{"name":"@deepseek-ai/dsh","version":"0.2.2"}`,
		filepath.Join(pkg, "lib", "bin.js"): "#!/usr/bin/env node\n",
		filepath.Join(pkg, "node_modules", "@deepseek-ai", "dsh-web-app", "presets", "default.yml"): "",
	} {
		os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path, []byte(data), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	os.MkdirAll(filepath.Join(prefix, "bin"), 0o755)
	if err := os.Symlink(filepath.Join(pkg, "lib", "bin.js"), filepath.Join(prefix, "bin", "dsh")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Join(prefix, "bin"))

	var out lockedBuffer
	base := t.TempDir()
	o := join.Options{URL: l.base, Name: "win-pc", Base: base, Roots: []string{base}, HTTP: &http.Client{Transport: asHost("ir.example.com")}, Out: &out, Logf: t.Logf}
	run := func(o join.Options) (context.CancelFunc, chan error) {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- o.Run(ctx) }()
		return cancel, done
	}
	online := func() *wire.Hello {
		t.Helper()
		for end := time.Now().Add(10 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
			if m := executors.Find("win-pc"); m != nil {
				if hi, ok := m.Hello(); ok {
					return hi
				}
			}
		}
		t.Fatalf("not connected; it said:\n%s", out.String())
		return nil
	}

	cancel, done := run(o)
	var cs api.Confirms
	for end := time.Now().Add(10 * time.Second); len(cs.Pending) == 0; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatalf("no pairing request; it said:\n%s", out.String())
		}
		l.want(200, "socket", "GET", "/api/confirms", nil, &cs)
	}
	cf := cs.Pending[0]
	if cf.Kind != "exec-pair" || cf.Machine == nil || cf.Machine.Agents[0].Agent != "dsh" || cf.Machine.Agents[0].Version != "0.2.2" ||
		!strings.Contains(out.String(), "配对码 "+cf.Code+"：") {
		t.Fatalf("the card %+v %+v; it said:\n%s", cf, cf.Machine, out.String())
	}
	l.want(200, "socket", "POST", "/api/confirms/"+cf.ID, api.ConfirmAnswer{Allow: true}, nil)
	hi := online()
	if len(hi.Agents) != 1 || hi.Agents[0].Agent != "dsh" || hi.Agents[0].Version != "0.2.2" || hi.Agents[0].Scope == "" ||
		!strings.HasPrefix(hi.Program, "iridium ") || hi.Scratch == "" {
		t.Fatalf("hello: %+v", hi)
	}
	tokFile := filepath.Join(o.Base, "tokens", strings.NewReplacer(":", "-").Replace(strings.TrimPrefix(l.base, "http://")))
	st, err := os.Stat(tokFile)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("the TOKEN's file: %v %v", st, err)
	}
	tok, _ := os.ReadFile(tokFile)
	if strings.Contains(out.String(), strings.TrimSpace(string(tok))) {
		t.Fatal("the TOKEN printed")
	}
	// Its TOKEN's directory is not served, the rest of its roots is.
	if _, err := executors.Find("win-pc").Stat(context.Background(), tokFile); !wire.IsCode(err, wire.CodeDenied) {
		t.Fatalf("the TOKEN served: %v", err)
	}
	if _, err := executors.Find("win-pc").Stat(context.Background(), filepath.Dir(hi.Scratch)); err != nil {
		t.Fatalf("its own directory: %v", err)
	}
	cancel()
	<-done
	for end := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if _, ok := executors.Find("win-pc").Hello(); !ok {
			break
		}
		if time.Now().After(end) {
			t.Fatal("still connected")
		}
	}

	// Again: the kept TOKEN, no pairing.
	said := strings.Count(out.String(), "配对码")
	cancel, done = run(o)
	online()
	if n := strings.Count(out.String(), "配对码"); n != said {
		t.Fatalf("paired again:\n%s", out.String())
	}
	l.want(200, "socket", "GET", "/api/confirms", nil, &cs)
	if len(cs.Pending) != 0 {
		t.Fatalf("asked again: %+v", cs.Pending)
	}
	cancel()
	<-done

	// Its Bot removed: a resident forgets the TOKEN and ends, unpaired.
	l.want(200, "socket", "DELETE", "/api/terminals/win-pc", nil, nil)
	o.Resident = true
	_, done = run(o)
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "未配对") {
			t.Fatalf("refused: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a refused resident runs on")
	}
	if _, err := os.Stat(tokFile); !os.IsNotExist(err) {
		t.Fatalf("the TOKEN kept: %v", err)
	}
}
