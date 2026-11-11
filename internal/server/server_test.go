package server

import (
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
	"github.com/kumoSleeping/iridium/internal/satori"
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
	return setupWith(t, nil)
}

// setupWith is setup with the session manager's options changed by edit first.
func setupWith(t *testing.T, edit func(*session.Options)) (*Server, config.Config, string) {
	t.Helper()
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	os.MkdirAll(bin, 0o700)
	cfg := config.Default()
	cfg.Listen = "127.0.0.1:0"
	cfg.StateDir = filepath.Join(root, "state")
	cfg.Socket = filepath.Join(root, "run", "ir.sock")
	cfg.ClaudeHome = filepath.Join(root, "claude")
	cfg.Programs = testagent.Programs(t, bin)
	os.MkdirAll(cfg.StateDir, 0o700)
	st, err := store.Open(filepath.Join(cfg.StateDir, "iridium.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	h := hub.New()
	l := &platform.Direct{}
	var srv *Server
	tag := func(id string) string { return srv.Tag(id) }
	o := session.Options{Config: cfg, Store: st, Hub: h, Launcher: l, Exe: os.Args[0], UnitPrefix: "iridium-test-", Logf: t.Logf, Poll: 10 * time.Millisecond, ReadEvery: 20 * time.Millisecond, Tag: tag}
	if edit != nil {
		edit(&o)
	}
	m, err := session.New(o)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		m.Close()
		testagent.KillAll(l, "iridium-test-")
	})
	work := filepath.Join(root, "work")
	os.MkdirAll(work, 0o700)
	srv = New(cfg, m, h, t.Logf)
	t.Cleanup(srv.Close)
	return srv, cfg, work
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
	t.Parallel()
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
		path := "/v1/internal/notes"
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
	t.Parallel()
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

	created, code := createAt(t, c, base, api.NewSession{Agent: "claude", Cwd: work})
	if code != 200 || created == "" {
		t.Fatalf("create: %d %+v", code, created)
	}
	id := created
	if code := call(t, c, "GET", base+"/v1/internal/sessions/nope", nil, nil); code != 422 {
		t.Errorf("unknown session: %d", code)
	}
	// A key the daemon does not know is left out, and the stream says so; the rest goes on.
	states := watchStates(t, base, nil, "list", "bad", "session:"+id)
	events := make(chan [2]string, 100)
	go func() {
		for st := range states {
			events <- [2]string{st.Type, string(st.Data)}
		}
	}()

	if code, sent := sendAt(t, c, base, id, api.Send{Text: "hello"}); code != 200 || sent.Input == "" {
		t.Fatalf("send: %d", code)
	}
	var draft strings.Builder
	sealed := ""
	var last api.SessionState
	gotList, gotBad := false, false
	for deadline := time.After(10 * time.Second); last.Last == nil || last.Last.Text != "ok: hello" || last.Status != api.StatusIdle; {
		select {
		case e := <-events:
			switch e[0] {
			case "draft":
				var d api.DraftEvent
				json.Unmarshal([]byte(e[1]), &d)
				draft.WriteString(d.Text)
				if d.Done {
					sealed = draft.String()
				}
			case "state":
				var se struct {
					Key     string          `json:"key"`
					Value   json.RawMessage `json:"value"`
					Ignored string          `json:"ignored"`
				}
				json.Unmarshal([]byte(e[1]), &se)
				if se.Key == "bad" {
					gotBad = se.Ignored == "unknown"
				} else if se.Key == "list" {
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
	if draft.String() != "ok: hello" || !gotList || !gotBad || last.End == 0 {
		t.Errorf("draft %q, list %v, bad key told %v, end %d", draft.String(), gotList, gotBad, last.End)
	}
	// The draft is sealed after its text, before the turn's end is told.
	if sealed != "ok: hello" {
		t.Errorf("draft sealed at %q", sealed)
	}

	page := pageAt(t, c, base, id)
	if len(page.Blocks) != 2 || page.Blocks[0].From != "主机" || page.Blocks[0].Text != "hello" || page.End != last.End {
		t.Fatalf("page: %+v", page)
	}
	var full satori.Message
	if code := satAt(t, c, base, "message.get", map[string]any{"channel_id": id, "message_id": page.Blocks[1].At}, &full); code != 200 || !strings.Contains(full.Content, "ok: hello") {
		t.Errorf("full: %d %+v", code, full)
	}
	if code := satAt(t, c, base, "message.get", map[string]any{"channel_id": id, "message_id": "1.0"}, nil); code != 422 {
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
	if code, _ := sendAt(t, uc, "http://ir", id, api.Send{Text: "again"}); code != 200 {
		t.Fatalf("send over the socket: %d", code)
	}
	waitAfter := func(end int64) api.MessagePage {
		t.Helper()
		for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
			var l struct {
				Data satori.PageData `json:"_data"`
			}
			satAt(t, uc, "http://ir", "message.list", map[string]any{"channel_id": id, "next": itoa(end), "direction": "after", "_data": map[string]any{"blocks": true}}, &l)
			after := api.MessagePage{Blocks: l.Data.Blocks, Start: l.Data.Start, End: l.Data.End}
			if len(after.Blocks) == 2 {
				return after
			}
			if time.Now().After(deadline) {
				t.Fatalf("nothing after %d: %+v", end, after)
			}
		}
	}
	after := waitAfter(page.End)
	if after.Blocks[0].From != "主机" || after.Blocks[1].Text != "ok: again" {
		t.Errorf("after: %+v", after.Blocks)
	}
	// An agent's ir in another session: a letter from that session, still 本机's.
	req, _ := http.NewRequest("POST", "http://ir/v1/message.create", strings.NewReader(`{"channel_id":"`+id+`","_data":{"text":"from a session"}}`))
	req.Header.Set(HeaderAgent, "1")
	req.Header.Set(HeaderSession, "caller-1")
	req.Header.Set(satori.HeaderPlatform, "local")
	req.Header.Set(satori.HeaderUser, "local")
	if resp, err := uc.Do(req); err != nil || resp.StatusCode != 200 {
		t.Fatalf("an agent's send: %v %v", resp, err)
	}
	if b := waitAfter(after.End).Blocks; b[0].From != "ir:caller-1" || b[0].FromTerminal != "local" || b[0].Text != "from a session" || b[1].Text != "ok: from a session" {
		t.Errorf("an agent's send: %+v", b)
	}
	// Ultracode, on the running agent (the fake Claude's model takes it).
	if code := call(t, c, "PUT", base+"/v1/internal/sessions/"+id+"/ultracode", api.SetUltracode{Ultracode: true}, nil); code != 200 {
		t.Errorf("ultracode: %d", code)
	}
	var live api.SessionState
	call(t, c, "GET", base+"/v1/internal/sessions/"+id, nil, &live)
	if live.Ultracode == nil || !live.Ultracode.On {
		t.Errorf("ultracode state: %+v", live.Ultracode)
	}
	// Its key, pressed: off; a button it has not is a bad request.
	if len(live.Attached) != 1 || !strings.Contains(live.Attached[0].Content["en"], " pressed") {
		t.Errorf("attached: %+v", live.Attached)
	}
	press := func(bid string) int {
		b, _ := json.Marshal(map[string]string{"id": bid})
		req, _ := http.NewRequest("POST", base+"/v1/internal/button", bytes.NewReader(b))
		req.Header.Set(satori.HeaderPlatform, "local")
		req.Header.Set(satori.HeaderUser, "local")
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := press("att:" + id + ":ir:claude.ultracode"); code != 200 {
		t.Errorf("the key pressed: %d", code)
	}
	call(t, c, "GET", base+"/v1/internal/sessions/"+id, nil, &live)
	if live.Ultracode == nil || live.Ultracode.On || len(live.Attached) != 1 || strings.Contains(live.Attached[0].Content["en"], " pressed") {
		t.Errorf("after the key: %+v %+v", live.Ultracode, live.Attached)
	}
	if code := press("att:" + id + ":ir:x.other"); code != 400 {
		t.Errorf("an unknown button: %d", code)
	}
	// A second daemon on the same socket is refused.
	if _, _, err := s.Listen(); err == nil {
		t.Error("listened twice on one socket")
	}
	// /clear is Iridium's: the reply names the session it goes on in, no input written.
	code, cleared := sendAt(t, c, base, id, api.Send{Text: "/clear"})
	if code != 200 || cleared.Session == "" || cleared.Session == id || cleared.Input != "" {
		t.Fatalf("clear: %d %+v", code, cleared)
	}
	if again := pageAt(t, c, base, cleared.Session); len(again.Blocks) != 0 {
		t.Errorf("the cleared session has words: %+v", again.Blocks)
	}
	// The old one's agent is stopping already (archived): stop the new one.
	if code := call(t, c, "POST", base+"/v1/internal/sessions/"+cleared.Session+"/stop", nil, nil); code != 200 {
		t.Errorf("stop: %d", code)
	}
	// Shutting down with the event stream still open ends it at once, and the socket goes. The
	// clients' spare connections go first: one dialed but never used would hold the shutdown.
	c.CloseIdleConnections()
	uc.CloseIdleConnections()
	http.DefaultClient.CloseIdleConnections()
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
