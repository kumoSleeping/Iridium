package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/config"
	"github.com/kumoSleeping/iridium/internal/store"
)

// live is a test daemon on a random port and its socket.
type live struct {
	t    *testing.T
	s    *Server
	cfg  config.Config
	work string
	base string // http://127.0.0.1:port
	web  *http.Client
	sock *http.Client
}

func start(t *testing.T) *live { return startWith(t, nil) }

// startWith is start, with ready run on the server before it serves.
func startWith(t *testing.T, ready func(*Server, config.Config, string)) *live {
	t.Helper()
	s, cfg, work := setup(t)
	if ready != nil {
		ready(s, cfg, work)
	}
	tcp, unix, err := s.Listen()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, tcp, unix) }()
	t.Cleanup(func() { cancel(); <-done })
	sock := &http.Client{Timeout: 20 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", cfg.SocketPath())
	}}}
	return &live{t: t, s: s, cfg: cfg, work: work, base: "http://" + tcp.Addr().String(), web: &http.Client{Timeout: 20 * time.Second}, sock: sock}
}

// do sends a request; as is "web" (the page, 本机 while no public origin), "socket" (a person's
// ir), "agent" (an agent's ir), "agent-in:<session>" (an agent's ir in that session),
// "ir-in:<session>" (ir send in that session, which names the session but sets no agent flag) or a
// channel token.
func (l *live) do(as, method, path string, body any, out any) int {
	l.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	c, url := l.web, l.base+path
	if as == "socket" || as == "agent" {
		c, url = l.sock, "http://ir"+path
	}
	req, _ := http.NewRequest(method, url, r)
	switch {
	case as == "agent":
		req.Header.Set(HeaderAgent, "1")
		req.Header.Set(HeaderSession, "caller-session")
	case strings.HasPrefix(as, "agent-in:"): // an agent's ir in that session
		c, url = l.sock, "http://ir"+path
		req, _ = http.NewRequest(method, url, r)
		req.Header.Set(HeaderAgent, "1")
		req.Header.Set(HeaderSession, strings.TrimPrefix(as, "agent-in:"))
	case strings.HasPrefix(as, "ir-in:"): // ir send in that session: the session, no agent flag
		c, url = l.sock, "http://ir"+path
		req, _ = http.NewRequest(method, url, r)
		req.Header.Set(HeaderSession, strings.TrimPrefix(as, "ir-in:"))
	case strings.HasPrefix(as, "ir_"):
		req.Header.Set("Authorization", "Bearer "+as)
	}
	resp, err := c.Do(req)
	if err != nil {
		l.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if out != nil {
		if p, ok := out.(*string); ok {
			*p = string(raw)
		} else {
			json.Unmarshal(raw, out)
		}
	}
	return resp.StatusCode
}

func (l *live) want(code int, as, method, path string, body any, out any) {
	l.t.Helper()
	var raw string
	got := l.do(as, method, path, body, &raw)
	if got != code {
		l.t.Fatalf("%s %s as %.8s: %d, want %d: %s", method, path, as, got, code, raw)
	}
	if out != nil {
		json.Unmarshal([]byte(raw), out)
	}
}

func (l *live) waitReply(as, id, text string) {
	l.t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		var st api.SessionState
		l.do(as, "GET", "/api/sessions/"+id, nil, &st)
		if st.Last != nil && st.Last.Text == text && st.Status == api.StatusIdle {
			return
		}
		if time.Now().After(deadline) {
			l.t.Fatalf("no %q in %s: %+v", text, id, st)
		}
	}
}

// What terminals may do (access.go): everything, but Claude only the owner's own, set as the
// terminal is made and never after. Without it a terminal sees Claude's sessions, nothing more.
func TestClaudeOnlyRule(t *testing.T) {
	l := start(t)
	// 本机's own session, from the page.
	var mine api.NewSessionReply
	l.want(200, "web", "POST", "/api/sessions", api.NewSession{Agent: api.AgentClaude, Cwd: l.work, Text: "mine"}, &mine)
	l.waitReply("web", mine.ID, "ok: mine")

	// A plugin made without --owner: no Claude.
	var tok api.Token
	l.want(200, "socket", "POST", "/api/terminals/tokens", api.NewInvite{Name: "bot"}, &tok)
	bot, botID := tok.Token, tok.Terminal.ID
	if !strings.HasPrefix(bot, "ir_") || tok.Terminal.Kind != api.TerminalChannel || tok.Terminal.Rules.ClaudeOwner != nil {
		t.Fatalf("token: %+v", tok.Terminal)
	}
	var me api.Me
	l.want(200, bot, "GET", "/api/me", nil, &me)
	if me.ID != botID || me.Name != "bot" || me.Panel || me.Local {
		t.Fatalf("me: %+v", me)
	}

	// It sees 本机's Claude session and reads it, but may not use it nor start one.
	var list []api.Session
	l.want(200, bot, "GET", "/api/sessions", nil, &list)
	if len(list) != 1 || list[0].ID != mine.ID {
		t.Fatalf("its list: %+v", list)
	}
	for _, p := range []string{"", "/messages"} {
		l.want(200, bot, "GET", "/api/sessions/"+mine.ID+p, nil, nil)
	}
	l.want(403, bot, "POST", "/api/sessions/"+mine.ID+"/send", api.Send{Text: "hi"}, nil)
	l.want(403, bot, "POST", "/api/sessions/"+mine.ID+"/stop", nil, nil)
	l.want(403, bot, "POST", "/api/sessions/"+mine.ID+"/answer", api.Answer{RequestID: "x", Kind: "permission", Allow: true}, nil)
	l.want(403, bot, "PUT", "/api/sessions/"+mine.ID+"/mode", api.SetMode{Mode: "manual"}, nil)
	l.want(403, bot, "POST", "/api/sessions", api.NewSession{Agent: api.AgentClaude, Cwd: l.work, Text: "x"}, nil)
	resp, err := streamAs(l, bot, "/api/events?sub=list,session:"+mine.ID)
	if err != nil {
		t.Fatal(err)
	}
	if line := firstState(t, resp); !strings.Contains(line, mine.ID) {
		t.Errorf("the stream: %s", line)
	}

	// A channel manages nothing; there is no table to change any more.
	l.want(403, bot, "GET", "/api/terminals", nil, nil)
	l.want(403, bot, "POST", "/api/terminals/tokens", api.NewInvite{Name: "other"}, nil)
	if code := l.do("socket", "PUT", "/api/terminals/"+botID+"/rules", map[string]any{"rules": map[string]any{}}, nil); code != 404 && code != 405 {
		t.Errorf("the rules route: %d", code)
	}
	// Claude is not given after: a new token --owner for it is refused, and it stays without.
	l.want(409, "socket", "POST", "/api/terminals/tokens", api.NewInvite{Name: "bot", Owner: true}, nil)
	l.want(403, bot, "POST", "/api/sessions/"+mine.ID+"/send", api.Send{Text: "hi"}, nil)

	// One made --owner by a person at the machine: Claude, and nothing held back (no approvals
	// forced, every command).
	var own api.Token
	l.want(200, "socket", "POST", "/api/terminals/tokens", api.NewInvite{Name: "mybot", Owner: true}, &own)
	if own.Terminal.Rules.ClaudeOwner == nil || own.Terminal.Rules.ClaudeOwner.By != "local" {
		t.Fatalf("owner token: %+v", own.Terminal)
	}
	var its api.NewSessionReply
	l.want(200, own.Token, "POST", "/api/sessions", api.NewSession{Agent: api.AgentClaude, Cwd: l.work, Text: "hello"}, &its)
	l.waitReply(own.Token, its.ID, "ok: hello")
	meta, _ := os.ReadFile(filepath.Join(l.cfg.StatePath(), "hold", its.ID, "session.json"))
	if strings.Contains(string(meta), `"mode":"manual"`) {
		t.Errorf("approvals forced on: %s", meta)
	}
	var page api.MessagePage
	l.want(200, own.Token, "GET", "/api/sessions/"+its.ID+"/messages", nil, &page)
	if len(page.Blocks) < 1 || page.Blocks[0].From != "mybot" || page.Blocks[0].FromTerminal != own.Terminal.ID || page.Blocks[0].Text != "hello" {
		t.Fatalf("blocks: %+v", page.Blocks)
	}
	inLog, _ := os.ReadFile(filepath.Join(l.cfg.StatePath(), "hold", its.ID, "in.log"))
	if !strings.Contains(string(inLog), `[mybot] hello`) {
		t.Errorf("the agent did not see the tag: %s", inLog)
	}
	st := l.s.st
	waitUsage(t, st, own.Terminal.ID)
	// A new token keeps its Claude.
	l.want(200, "socket", "POST", "/api/terminals/tokens", api.NewInvite{Name: "mybot"}, &own)
	if own.Terminal.Rules.ClaudeOwner == nil {
		t.Error("a new token took Claude away")
	}
	// The one without Claude sees the owner's plugin's session too.
	l.want(200, bot, "GET", "/api/sessions/"+its.ID, nil, nil)

	var ts api.Terminals
	l.want(200, "agent", "GET", "/api/terminals", nil, &ts)
	if len(ts.Terminals) != 3 || !ts.Terminals[0].Local || ts.Terminals[0].Rules.ClaudeOwner == nil {
		t.Fatalf("terminals: %+v", ts)
	}

	// 本机 cannot be revoked; a revoked token stops working, its row gone.
	l.want(409, "socket", "DELETE", "/api/terminals/local", nil, nil)
	l.want(200, "socket", "DELETE", "/api/terminals/mybot", nil, nil)
	l.want(401, own.Token, "GET", "/api/me", nil, nil)
	if _, ok, _ := st.TerminalRules(own.Terminal.ID); ok {
		t.Error("the revoked terminal's row stayed")
	}
}

func waitUsage(t *testing.T, st *store.Store, id string) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if n, _ := st.TokensSince(id, 0); n > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("no usage row for the terminal")
		}
	}
}

func streamAs(l *live, as, path string) (*http.Response, error) {
	req, _ := http.NewRequest("GET", l.base+path, nil)
	req.Header.Set("Authorization", "Bearer "+as)
	return (&http.Client{}).Do(req)
}

func firstState(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 1024)
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		n, err := resp.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if i := strings.Index(string(buf), "event: state\ndata: "); i >= 0 {
			if j := strings.Index(string(buf[i:]), "\n\n"); j >= 0 {
				return string(buf[i : i+j])
			}
		}
		if err != nil {
			break
		}
	}
	t.Fatalf("no state on the stream: %q", buf)
	return ""
}

// A terminal's device: a computer until set; its own by /api/me/device, another's only from a
// panel or 本机; by ID or name.
func TestDevice(t *testing.T) {
	l := start(t)
	var tok api.Token
	l.want(200, "socket", "POST", "/api/terminals/tokens", api.NewInvite{Name: "bot"}, &tok)
	bot, botID := tok.Token, tok.Terminal.ID
	var me api.Me
	l.want(200, bot, "GET", "/api/me", nil, &me)
	if me.Device != api.DeviceComputer {
		t.Fatalf("default device: %q", me.Device)
	}
	var got api.Terminal
	l.want(200, bot, "PUT", "/api/me/device", api.SetDevice{Device: api.DevicePhone}, &got)
	if got.ID != botID || got.Device != api.DevicePhone {
		t.Fatalf("own device: %+v", got)
	}
	l.want(400, bot, "PUT", "/api/me/device", api.SetDevice{Device: "tablet"}, nil)
	l.want(403, bot, "PUT", "/api/terminals/local/device", api.SetDevice{Device: api.DevicePhone}, nil)
	l.want(404, "socket", "PUT", "/api/terminals/nobody/device", api.SetDevice{Device: api.DevicePhone}, nil)
	l.want(200, "socket", "PUT", "/api/terminals/bot/device", api.SetDevice{Device: api.DeviceComputer}, &got)
	l.want(200, "web", "PUT", "/api/me/device", api.SetDevice{Device: api.DevicePhone}, &got)
	if got.ID != "local" || got.Device != api.DevicePhone || !got.Local {
		t.Fatalf("本机's device: %+v", got)
	}
	var ts api.Terminals
	l.want(200, "socket", "GET", "/api/terminals", nil, &ts)
	if len(ts.Terminals) != 2 || ts.Terminals[0].Device != api.DevicePhone || ts.Terminals[1].Device != api.DeviceComputer {
		t.Fatalf("listed: %+v", ts.Terminals)
	}
}

// An agent's ir asks for a token: no secret comes back to it, a panel sees the ask, allows it
// and alone gets the token; the agent learns only that it was allowed.
func TestConfirmations(t *testing.T) {
	l := start(t)
	var p api.Pending
	l.want(202, "agent", "POST", "/api/terminals/tokens", api.NewInvite{Name: "qqbot"}, &p)
	if p.Confirm.ID == "" || p.Confirm.Kind != "token" || p.Confirm.Asker != "本机" || p.Confirm.Session != "caller-session" {
		t.Fatalf("pending: %+v", p)
	}
	var cs api.Confirms
	l.want(403, "agent", "GET", "/api/confirms", nil, nil)
	l.want(200, "web", "GET", "/api/confirms", nil, &cs)
	if len(cs.Pending) != 1 || cs.Pending[0].ID != p.Confirm.ID {
		t.Fatalf("confirms: %+v", cs)
	}
	// The agent waits; meanwhile the page allows.
	got := make(chan string, 1)
	go func() {
		var raw string
		l.do("agent", "GET", "/api/confirms/"+p.Confirm.ID+"?wait=10", nil, &raw)
		got <- raw
	}()
	time.Sleep(100 * time.Millisecond)
	l.want(403, "agent", "POST", "/api/confirms/"+p.Confirm.ID, api.ConfirmAnswer{Allow: true}, nil)
	var ans api.ConfirmReply
	l.want(200, "web", "POST", "/api/confirms/"+p.Confirm.ID, api.ConfirmAnswer{Allow: true}, &ans)
	if ans.State != "allowed" || ans.Token == nil || !strings.HasPrefix(ans.Token.Token, "ir_") {
		t.Fatalf("answer: %+v", ans)
	}
	raw := <-got
	if !strings.Contains(raw, `"state":"allowed"`) || strings.Contains(raw, ans.Token.Token) || strings.Contains(raw, "ir_") {
		t.Fatalf("the agent's view: %s", raw)
	}
	l.want(404, "web", "POST", "/api/confirms/"+p.Confirm.ID, api.ConfirmAnswer{Allow: true}, nil) // once
	l.want(200, ans.Token.Token, "GET", "/api/me", nil, nil)
	// Denied.
	l.want(202, "agent", "POST", "/api/terminals/tokens", api.NewInvite{Name: "other"}, &p)
	ans = api.ConfirmReply{}
	l.want(200, "web", "POST", "/api/confirms/"+p.Confirm.ID, api.ConfirmAnswer{Allow: false}, &ans)
	var st api.ConfirmState
	l.want(200, "agent", "GET", "/api/confirms/"+p.Confirm.ID, nil, &st)
	if st.State != "denied" || ans.Token != nil {
		t.Fatalf("denied: %+v %+v", st, ans)
	}
	// Invites need a public origin.
	l.want(409, "agent", "POST", "/api/terminals/invites", api.NewInvite{Name: "pad"}, nil)
	// A bad name is refused at once.
	l.want(409, "agent", "POST", "/api/terminals/tokens", api.NewInvite{Name: "a/b"}, nil)
}

// Setting public.origin puts the passkey gate in front at once: the loopback page is no longer
// 本机, the public host is the only one served, the setup link is written, and the config file
// keeps it. An invite then is a join link on the public origin. Off again, it is 本机 again.
func TestPublicOrigin(t *testing.T) {
	l := start(t)
	path := filepath.Join(t.TempDir(), "config.json")
	l.s.ConfigPath = path
	stream, err := http.Get(l.base + "/api/events?sub=list")
	if err != nil {
		t.Fatal(err)
	}
	firstState(t, stream)
	l.want(403, "agent", "PUT", "/api/public", api.SetPublic{Origin: "https://ir.example.com"}, nil)
	l.want(400, "socket", "PUT", "/api/public", api.SetPublic{Origin: "http://ir.example.com"}, nil)
	var ps api.PublicState
	l.want(200, "socket", "PUT", "/api/public", api.SetPublic{Origin: "https://ir.example.com"}, &ps)
	if ps.Origin != "https://ir.example.com" || ps.Passkeys {
		t.Fatalf("public: %+v", ps)
	}
	if cfg, err := config.Load(path); err != nil || cfg.Public.Origin != "https://ir.example.com" {
		t.Fatalf("config: %+v %v", cfg.Public, err)
	}
	if b, err := os.ReadFile(filepath.Join(l.cfg.StatePath(), "setup-token")); err != nil || !strings.HasPrefix(string(b), "https://ir.example.com/_ir/join#") {
		t.Fatalf("setup link: %v", err)
	}
	l.want(421, "web", "GET", "/api/sessions", nil, nil)
	req, _ := http.NewRequest("GET", l.base+"/api/sessions", nil)
	req.Host = "ir.example.com"
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != 401 {
		t.Fatalf("no login: %v %v", resp.StatusCode, err)
	}
	req, _ = http.NewRequest("GET", l.base+"/_ir/health", nil)
	req.Host = "ir.example.com"
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != 200 {
		t.Fatalf("health: %v %v", resp.StatusCode, err)
	}
	// The stream the old gate let in is closed.
	done := make(chan struct{})
	go func() { io.Copy(io.Discard, stream.Body); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Error("the old gate's stream stayed open")
	}
	// The socket is still 本机; a person's ir gets the link, an agent's a confirmation.
	var inv api.Invite
	l.want(200, "socket", "POST", "/api/terminals/invites", api.NewInvite{Name: "pad"}, &inv)
	if !strings.HasPrefix(inv.URL, "https://ir.example.com/_ir/join#") || inv.Name != "pad" || inv.ExpiresAt == 0 {
		t.Fatalf("invite: %+v", inv)
	}
	if !strings.HasPrefix(inv.QR, "data:image/svg+xml;base64,") {
		t.Fatalf("invite's QR: %.40q", inv.QR)
	}
	var p api.Pending
	l.want(202, "agent", "POST", "/api/terminals/invites", api.NewInvite{}, &p)
	var raw string
	l.do("socket", "POST", "/api/confirms/"+p.Confirm.ID, api.ConfirmAnswer{Allow: true}, &raw)
	if !strings.Contains(raw, "https://ir.example.com/_ir/join#") || !strings.Contains(raw, `"qr":"data:image/svg+xml;base64,`) {
		t.Fatalf("the panel's answer: %s", raw)
	}
	l.do("agent", "GET", "/api/confirms/"+p.Confirm.ID, nil, &raw)
	if !strings.Contains(raw, `"allowed"`) || strings.Contains(raw, "/_ir/join") || strings.Contains(raw, "qr") {
		t.Fatalf("the agent's view: %s", raw)
	}
	var ts api.Terminals
	l.want(200, "socket", "GET", "/api/terminals", nil, &ts)
	if len(ts.Invites) != 3 { // setup, pad, the agent's
		t.Errorf("invites: %+v", ts.Invites)
	}
	l.want(200, "socket", "PUT", "/api/public", api.SetPublic{Origin: ""}, nil)
	l.want(200, "web", "GET", "/api/sessions", nil, nil)
}
