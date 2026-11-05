package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/config"
	"github.com/kumoSleeping/iridium/internal/hub"
	"github.com/kumoSleeping/iridium/internal/platform"
	"github.com/kumoSleeping/iridium/internal/session"
	"github.com/kumoSleeping/iridium/internal/store"
	"github.com/kumoSleeping/iridium/internal/testagent"
)

func TestMain(m *testing.M) {
	if code, ok := testagent.Main(); ok {
		os.Exit(code)
	}
	os.Exit(m.Run())
}

func setup(t *testing.T) (*Server, config.Config, string) {
	t.Helper()
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	os.MkdirAll(bin, 0o700)
	os.Symlink(os.Args[0], filepath.Join(bin, "claude"))
	cfg := config.Default()
	cfg.Listen = "127.0.0.1:0"
	cfg.StateDir = filepath.Join(root, "state")
	cfg.Socket = filepath.Join(root, "run", "ir.sock")
	cfg.ClaudeHome = filepath.Join(root, "claude")
	cfg.Programs["claude"] = filepath.Join(bin, "claude")
	os.MkdirAll(cfg.StateDir, 0o700)
	st, err := store.Open(filepath.Join(cfg.StateDir, "iridium.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	h := hub.New()
	l := &platform.Direct{}
	m, err := session.New(session.Options{Config: cfg, Store: st, Hub: h, Launcher: l, Exe: os.Args[0], UnitPrefix: "iridium-test-", Logf: t.Logf, Poll: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		m.Close()
		names, _ := l.List(context.Background(), "iridium-test-")
		for _, n := range names {
			l.Kill(context.Background(), n, "KILL", true)
		}
	})
	work := filepath.Join(root, "work")
	os.MkdirAll(work, 0o700)
	return New(cfg, m, h, t.Logf), cfg, work
}

func call(t *testing.T, c *http.Client, method, url string, body any, out any) int {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, url, r)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		json.NewDecoder(resp.Body).Decode(out)
	}
	return resp.StatusCode
}

func TestGuard(t *testing.T) {
	s, _, _ := setup(t)
	h := s.Handler(false)
	for _, c := range []struct {
		method, host, origin string
		want                 int
	}{
		{"GET", "127.0.0.1:7810", "", 200},
		{"GET", "localhost:7810", "", 200},
		{"GET", "evil.example:7810", "", http.StatusMisdirectedRequest},
		{"POST", "127.0.0.1:7810", "http://evil.example", http.StatusForbidden},
		{"POST", "127.0.0.1:7810", "http://127.0.0.1:7810", http.StatusBadRequest}, // let through, then a bad body
	} {
		path := "/api/sessions"
		req := httptest.NewRequest(c.method, "http://"+c.host+path, strings.NewReader("{"))
		if c.origin != "" {
			req.Header.Set("Origin", c.origin)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != c.want {
			t.Errorf("%s %s origin %q: %d, want %d", c.method, c.host, c.origin, w.Code, c.want)
		}
	}
}

// A turn over HTTP: the stream gives the session's state whole and the drafts; the page then
// fetches the blocks after the end it knew.
func TestTurnOverHTTP(t *testing.T) {
	s, cfg, work := setup(t)
	tcp, unix, err := s.Listen()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	served := make(chan error, 1)
	go func() { served <- s.Serve(ctx, tcp, unix) }()
	base := "http://" + tcp.Addr().String()
	c := &http.Client{Timeout: 10 * time.Second}

	var created api.NewSessionReply
	if code := call(t, c, "POST", base+"/api/sessions", api.NewSession{Agent: "claude", Cwd: work}, &created); code != 200 || created.ID == "" {
		t.Fatalf("create: %d %+v", code, created)
	}
	id := created.ID
	if code := call(t, c, "GET", base+"/api/sessions/nope", nil, nil); code != 404 {
		t.Errorf("unknown session: %d", code)
	}
	if code := call(t, c, "GET", base+"/api/events?sub=bad", nil, nil); code != 400 {
		t.Errorf("bad sub: %d", code)
	}

	resp, err := http.Get(base + "/api/events?sub=list&sub=session:" + id)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type %q", ct)
	}
	events := make(chan [2]string, 100)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		name := ""
		for sc.Scan() {
			if v, ok := strings.CutPrefix(sc.Text(), "event: "); ok {
				name = v
			}
			if v, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
				events <- [2]string{name, v}
			}
		}
	}()

	var sent api.SendReply
	if code := call(t, c, "POST", base+"/api/sessions/"+id+"/send", api.Send{Text: "hello"}, &sent); code != 200 || sent.Input == "" {
		t.Fatalf("send: %d", code)
	}
	var draft strings.Builder
	var last api.SessionState
	gotList := false
	for deadline := time.After(10 * time.Second); last.Last == nil || last.Last.Text != "ok: hello" || last.Status != api.StatusIdle; {
		select {
		case e := <-events:
			switch e[0] {
			case "draft":
				var d api.DraftEvent
				json.Unmarshal([]byte(e[1]), &d)
				draft.WriteString(d.Text)
			case "state":
				var se struct {
					Key   string          `json:"key"`
					Value json.RawMessage `json:"value"`
				}
				json.Unmarshal([]byte(e[1]), &se)
				if se.Key == "list" {
					gotList = true
				} else {
					last = api.SessionState{}
					json.Unmarshal(se.Value, &last)
				}
			}
		case <-deadline:
			t.Fatalf("no reply on the stream; last state %+v", last)
		}
	}
	if draft.String() != "ok: hello" || !gotList || last.End == 0 {
		t.Errorf("draft %q, list %v, end %d", draft.String(), gotList, last.End)
	}

	var page api.MessagePage
	call(t, c, "GET", base+"/api/sessions/"+id+"/messages", nil, &page)
	if len(page.Blocks) != 2 || page.Blocks[0].From != TerminalWeb || page.Blocks[0].Text != "hello" || page.End != last.End {
		t.Fatalf("page: %+v", page)
	}
	var full api.FullText
	if code := call(t, c, "GET", base+"/api/sessions/"+id+"/block?at="+page.Blocks[1].At, nil, &full); code != 200 || full.Text != "ok: hello" {
		t.Errorf("full: %d %+v", code, full)
	}
	if code := call(t, c, "GET", base+"/api/sessions/"+id+"/block?at=1.0", nil, nil); code != 404 {
		t.Errorf("full at nothing: %d", code)
	}

	// The same API on the unix socket, as the local terminal.
	st, err := os.Stat(cfg.SocketPath())
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("socket: %v %v", st.Mode(), err)
	}
	uc := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", cfg.SocketPath())
	}}}
	if code := call(t, uc, "POST", "http://ir/api/sessions/"+id+"/send", api.Send{Text: "again"}, nil); code != 200 {
		t.Fatalf("send over the socket: %d", code)
	}
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		var after api.MessagePage
		call(t, uc, "GET", "http://ir/api/sessions/"+id+"/messages?after="+itoa(page.End), nil, &after)
		if len(after.Blocks) == 2 {
			if after.Blocks[0].From != TerminalLocal || after.Blocks[1].Text != "ok: again" {
				t.Errorf("after: %+v", after.Blocks)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("nothing after %d: %+v", page.End, after)
		}
	}
	// A second daemon on the same socket is refused.
	if _, _, err := s.Listen(); err == nil {
		t.Error("listened twice on one socket")
	}
	if code := call(t, c, "POST", base+"/api/sessions/"+id+"/stop", nil, nil); code != 200 {
		t.Errorf("stop: %d", code)
	}
	// Shutting down with the event stream still open ends it at once, and the socket goes.
	start := time.Now()
	cancel()
	if err := <-served; err != nil || time.Since(start) > time.Second {
		t.Errorf("shutdown took %v: %v", time.Since(start), err)
	}
	if _, err := os.Stat(cfg.SocketPath()); !os.IsNotExist(err) {
		t.Errorf("socket left: %v", err)
	}
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }
