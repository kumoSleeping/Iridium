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

func (l *live) rules(as, id string, r api.Rules) int {
	l.t.Helper()
	return l.do(as, "PUT", "/api/terminals/"+id+"/rules", api.SetRules{Rules: r}, nil)
}

// The permission table, on every session route there is.
func TestPermissions(t *testing.T) {
	l := start(t)
	// 本机's own session, from the page.
	var mine api.NewSessionReply
	l.want(200, "web", "POST", "/api/sessions", api.NewSession{Agent: api.AgentClaude, Cwd: l.work, Text: "mine"}, &mine)
	l.waitReply("web", mine.ID, "ok: mine")

	// A plugin: a person's ir gets its token.
	var tok api.Token
	l.want(200, "socket", "POST", "/api/terminals/tokens", api.NewInvite{Name: "bot"}, &tok)
	bot, botID := tok.Token, tok.Terminal.ID
	if !strings.HasPrefix(bot, "ir_") || tok.Terminal.Kind != api.TerminalChannel || !tok.Terminal.Rules.MustApprove || tok.Terminal.Rules.SeeOthers {
		t.Fatalf("token: %+v", tok.Terminal)
	}
	var me api.Me
	l.want(200, bot, "GET", "/api/me", nil, &me)
	if me.ID != botID || me.Name != "bot" || me.Panel || me.Local {
		t.Fatalf("me: %+v", me)
	}

	// It sees nothing of 本机's, and may not use Claude.
	var list []api.Session
	l.want(200, bot, "GET", "/api/sessions", nil, &list)
	if len(list) != 0 {
		t.Fatalf("a channel sees 本机's sessions: %+v", list)
	}
	for _, p := range []string{"", "/messages", "/block?at=0.0"} {
		l.want(404, bot, "GET", "/api/sessions/"+mine.ID+p, nil, nil)
	}
	l.want(404, bot, "POST", "/api/sessions/"+mine.ID+"/send", api.Send{Text: "hi"}, nil)
	l.want(404, bot, "POST", "/api/sessions/"+mine.ID+"/stop", nil, nil)
	l.want(404, bot, "GET", "/api/events?sub=session:"+mine.ID, nil, nil)
	l.want(403, bot, "POST", "/api/sessions", api.NewSession{Agent: api.AgentClaude, Cwd: l.work, Text: "x"}, nil)

	// A channel manages nothing, its own table included.
	full := tok.Terminal.Rules
	full.Agents = append(full.Agents, api.AgentClaude)
	if code := l.rules(bot, botID, full); code != 403 {
		t.Fatalf("a channel set rules: %d", code)
	}
	l.want(403, bot, "GET", "/api/terminals", nil, nil)
	l.want(403, bot, "POST", "/api/terminals/tokens", api.NewInvite{Name: "other"}, nil)

	// Claude only from a person at the machine: not the web (here the local page, 本机 itself), not
	// an agent's ir, whatever owner mark the request carries.
	full.ClaudeOwner = &api.OwnerConfirm{By: "local", At: 1}
	if code := l.rules("web", botID, full); code != 403 {
		t.Fatalf("the web turned claude on: %d", code)
	}
	if code := l.rules("agent", botID, full); code != 403 {
		t.Fatalf("an agent turned claude on: %d", code)
	}
	if code := l.rules("socket", botID, full); code != 200 {
		t.Fatalf("claude from the machine: %d", code)
	}
	var ts api.Terminals
	l.want(200, "agent", "GET", "/api/terminals", nil, &ts)
	var row api.Terminal
	for _, x := range ts.Terminals {
		if x.ID == botID {
			row = x
		}
	}
	if row.Rules.ClaudeOwner == nil || row.Rules.ClaudeOwner.By != "local" || len(ts.Terminals) != 2 || !ts.Terminals[0].Local {
		t.Fatalf("terminals: %+v", ts)
	}
	// An agent narrows (answers off, two commands) but does not widen.
	narrow := row.Rules
	narrow.Answer, narrow.AllCommands, narrow.Commands = false, false, []string{"/compact"}
	if code := l.rules("agent", botID, narrow); code != 200 {
		t.Fatalf("narrowing: %d", code)
	}
	wide := narrow
	wide.SeeOthers = true
	if code := l.rules("agent", botID, wide); code != 403 {
		t.Fatalf("an agent widened: %d", code)
	}

	// Its own session: approvals on (a must-approve terminal), its tag on the message, its usage.
	l.want(403, bot, "POST", "/api/sessions", api.NewSession{Agent: api.AgentClaude, Cwd: l.work, Mode: "bypassPermissions"}, nil)
	var its api.NewSessionReply
	l.want(200, bot, "POST", "/api/sessions", api.NewSession{Agent: api.AgentClaude, Cwd: l.work, Text: "hello"}, &its)
	l.waitReply(bot, its.ID, "ok: hello")
	meta, _ := os.ReadFile(filepath.Join(l.cfg.StatePath(), "hold", its.ID, "session.json"))
	if !strings.Contains(string(meta), `"mode":"manual"`) {
		t.Errorf("a must-approve terminal's session: %s", meta)
	}
	l.want(200, bot, "GET", "/api/sessions", nil, &list)
	if len(list) != 1 || list[0].ID != its.ID {
		t.Fatalf("its list: %+v", list)
	}
	var page api.MessagePage
	l.want(200, bot, "GET", "/api/sessions/"+its.ID+"/messages", nil, &page)
	if len(page.Blocks) < 1 || page.Blocks[0].From != "bot" || page.Blocks[0].FromTerminal != botID || page.Blocks[0].Text != "hello" {
		t.Fatalf("blocks: %+v", page.Blocks)
	}
	inLog, _ := os.ReadFile(filepath.Join(l.cfg.StatePath(), "hold", its.ID, "in.log"))
	if !strings.Contains(string(inLog), `[bot] hello`) {
		t.Errorf("the agent did not see the tag: %s", inLog)
	}
	st := l.s.st
	waitUsage(t, st, botID)

	// Commands and answers as the table says.
	l.want(403, bot, "POST", "/api/sessions/"+its.ID+"/send", api.Send{Text: "/mode bypassPermissions"}, nil)
	l.want(403, bot, "POST", "/api/sessions/"+its.ID+"/answer", api.Answer{RequestID: "x", Kind: "permission", Allow: true}, nil)
	// The page sees the plugin's session too (it sees others'), and answers.
	l.want(200, "web", "GET", "/api/sessions/"+its.ID, nil, nil)

	// The daily cap.
	capped := narrow
	capped.DailyTokens = 1
	if code := l.rules("agent", botID, capped); code != 200 {
		t.Fatalf("a cap: %d", code)
	}
	l.want(429, bot, "POST", "/api/sessions/"+its.ID+"/send", api.Send{Text: "more"}, nil)
	// Directories: none but another.
	dirs := capped
	dirs.DailyTokens, dirs.AllDirs, dirs.Dirs = 0, false, []string{"/nowhere"}
	if code := l.rules("web", botID, dirs); code != 200 {
		t.Fatalf("dirs: %d", code)
	}
	l.want(403, bot, "POST", "/api/sessions", api.NewSession{Agent: api.AgentPi, Cwd: l.work}, nil)
	l.want(404, bot, "GET", "/api/sessions/"+its.ID, nil, nil) // its cwd is out of bounds now
	// The stream's list is the terminal's too.
	resp, err := streamAs(l, bot, "/api/events?sub=list")
	if err != nil {
		t.Fatal(err)
	}
	line := firstState(t, resp)
	if !strings.Contains(line, `"key":"list","value":[]`) {
		t.Errorf("the stream's list: %s", line)
	}

	// 本机 cannot change, nor be revoked; a revoked token stops working.
	if code := l.rules("web", "local", full); code != 409 {
		t.Errorf("本机's rules: %d", code)
	}
	l.want(409, "socket", "DELETE", "/api/terminals/local", nil, nil)
	l.want(200, "socket", "DELETE", "/api/terminals/bot", nil, nil)
	l.want(401, bot, "GET", "/api/me", nil, nil)
	if _, ok, _ := st.TerminalRules(botID); ok {
		t.Error("the revoked terminal's rules stayed")
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

// An agent's ir asks for a token: no secret comes back to it, a panel sees the ask, allows it
// and alone gets the token; the agent learns only that it was allowed.
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
