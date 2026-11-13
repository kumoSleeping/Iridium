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

	"github.com/kumoSleeping/iridium/internal/access"
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
	return serveLive(t, s, cfg, work, ready)
}

// serveLive serves s (setupWith's) as start does, with ready run on it first.
func serveLive(t *testing.T, s *Server, cfg config.Config, work string, ready func(*Server, config.Config, string)) *live {
	t.Helper()
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
	return l.doWith(as, method, path, body, out, nil)
}

// doWith is do with these headers too.
func (l *live) doWith(as, method, path string, body any, out any, hdr http.Header) int {
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
	for k, v := range hdr {
		req.Header[k] = v
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
		l.do(as, "GET", "/v1/internal/sessions/"+id, nil, &st)
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
	t.Parallel()
	l := start(t)
	// 本机's own session, from the page.
	mine := l.create(200, "web", api.NewSession{Agent: api.AgentClaude, Cwd: l.work, Text: "mine"})
	l.waitReply("web", mine, "ok: mine")

	// A plugin made without --owner: no Claude.
	var tok api.Token
	tok = l.newApp("bot", false)
	bot, botID := tok.Token, tok.App.ID
	if !strings.HasPrefix(bot, "ir_") || tok.App.Rules.ClaudeOwner != nil {
		t.Fatalf("token: %+v", tok.App)
	}
	var me api.Me
	l.want(200, bot, "GET", "/v1/internal/me", nil, &me)
	if me.ID != botID || me.App != botID || me.Login != "" || me.Name != "bot" || me.Web || me.Host {
		t.Fatalf("me: %+v", me)
	}

	// It sees 本机's Claude session and reads it, but may not use it nor start one.
	var list []api.Session
	list = l.sessions(bot)
	if len(list) != 1 || list[0].ID != mine {
		t.Fatalf("its list: %+v", list)
	}
	l.want(200, bot, "GET", "/v1/internal/sessions/"+mine, nil, nil)
	l.wantSat(200, bot, "message.list", map[string]any{"channel_id": mine}, nil)
	if code, _ := l.send(bot, mine, api.Send{Text: "hi"}); code != 403 {
		t.Errorf("send: %d", code)
	}
	l.want(403, bot, "POST", "/v1/internal/sessions/"+mine+"/stop", nil, nil)
	if code := l.answer(bot, api.CardAnswer{Card: "ask:" + mine + ":x", Option: "allow"}); code != 403 {
		t.Errorf("answer: %d", code)
	}
	l.want(403, bot, "PUT", "/v1/internal/sessions/"+mine+"/mode", api.SetMode{Mode: "manual"}, nil)
	l.create(403, bot, api.NewSession{Agent: api.AgentClaude, Cwd: l.work, Text: "x"})
	if raw := l.wantRaw(200, bot, "GET", "/v1/internal/sessions/"+mine, nil); !strings.Contains(raw, mine) {
		t.Errorf("the session: %.300s", raw)
	}

	// A channel manages nothing; there is no table to change any more.
	var pub api.Apps
	if l.want(200, bot, "GET", "/v1/internal/apps", nil, &pub); pub.Full || len(pub.Invites) != 0 {
		t.Errorf("the public list: %+v", pub)
	}
	l.want(403, bot, "POST", "/v1/internal/invites", api.NewInvite{Name: "other"}, nil)
	l.want(403, bot, "DELETE", "/v1/internal/apps/bot", nil, nil)
	if code := l.do("socket", "PUT", "/v1/internal/apps/"+botID+"/rules", map[string]any{"rules": map[string]any{}}, nil); code != 404 && code != 405 {
		t.Errorf("the rules route: %d", code)
	}
	if code, _ := l.send(bot, mine, api.Send{Text: "hi"}); code != 403 {
		t.Errorf("send: %d", code)
	}

	// One made --owner by a person at the machine: Claude, and nothing held back (no approvals
	// forced, every command).
	var own api.Token
	own = l.newApp("mybot", true)
	if own.App.Rules.ClaudeOwner == nil || own.App.Rules.ClaudeOwner.By != "local" {
		t.Fatalf("owner token: %+v", own.App)
	}
	its := l.create(200, own.Token, api.NewSession{Agent: api.AgentClaude, Cwd: l.work, Text: "hello"})
	l.waitReply(own.Token, its, "ok: hello")
	meta, _ := os.ReadFile(filepath.Join(l.cfg.StatePath(), "hold", its, "session.json"))
	if strings.Contains(string(meta), `"mode":"manual"`) {
		t.Errorf("approvals forced on: %s", meta)
	}
	page := l.page(own.Token, its)
	if len(page.Blocks) < 1 || page.Blocks[0].From != "mybot·mybot" || page.Blocks[0].FromLogin != "main" || page.Blocks[0].FromTerminal != own.App.ID || page.Blocks[0].Text != "hello" {
		t.Fatalf("blocks: %+v", page.Blocks)
	}
	inLog, _ := os.ReadFile(filepath.Join(l.cfg.StatePath(), "hold", its, "in.log"))
	if !strings.Contains(string(inLog), `[mybot·mybot] hello`) {
		t.Errorf("the agent did not see the tag: %s", inLog)
	}
	st := l.s.st
	waitUsage(t, st, own.App.ID)
	// The one without Claude sees the owner's plugin's session too.
	l.want(200, bot, "GET", "/v1/internal/sessions/"+its, nil, nil)

	var as api.Apps
	l.want(200, "agent", "GET", "/v1/internal/apps", nil, &as)
	// The host first; no ir_web without a public origin.
	if len(as.Apps) != 3 || !as.Apps[0].Host || as.Apps[0].Name != "主机" || as.Apps[0].Rules.ClaudeOwner == nil || as.Apps[1].Web || as.Apps[2].Web {
		t.Fatalf("apps: %+v", as)
	}

	// The host and ir_web cannot be removed; a removed app's TOKEN stops working, its row gone.
	for _, ref := range []string{"local", "host", "主机", "本机", "ir_web"} {
		l.want(422, "socket", "DELETE", "/v1/internal/apps/"+ref, nil, nil)
	}
	// A login as 应用/登录, escaped in the path (ir apps rm ir_web/名字).
	if raw := ""; l.do("socket", "DELETE", "/v1/internal/apps/ir_web%2Fnobody", nil, &raw) != 422 || !strings.Contains(raw, "no such app") {
		t.Errorf("ir_web/nobody: %s", raw)
	}
	l.want(200, "socket", "DELETE", "/v1/internal/apps/mybot", nil, nil)
	l.want(401, own.Token, "GET", "/v1/internal/me", nil, nil)
	if _, ok, _ := st.TerminalRules(own.App.ID); ok {
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

// A device: the host's and a browser's, a computer until set; its own by /v1/internal/me/device,
// another's only from a panel or the host. An app has none (k_glossary "电脑").
func TestDevice(t *testing.T) {
	t.Parallel()
	l := start(t)
	var tok api.Token
	tok = l.newApp("bot", false)
	bot := tok.Token
	var me api.Me
	l.want(200, bot, "GET", "/v1/internal/me", nil, &me)
	if me.Device != "" {
		t.Fatalf("an app's device: %q", me.Device)
	}
	l.want(422, bot, "PUT", "/v1/internal/me/device", api.SetDevice{Device: api.DevicePhone}, nil)
	l.want(422, "socket", "PUT", "/v1/internal/apps/bot/device", api.SetDevice{Device: api.DeviceComputer}, nil)
	l.want(400, bot, "PUT", "/v1/internal/me/device", api.SetDevice{Device: "tablet"}, nil)
	l.want(403, bot, "PUT", "/v1/internal/apps/local/device", api.SetDevice{Device: api.DevicePhone}, nil)
	l.want(422, "socket", "PUT", "/v1/internal/apps/nobody/device", api.SetDevice{Device: api.DevicePhone}, nil)
	l.want(200, "web", "GET", "/v1/internal/me", nil, &me)
	if me.Device != api.DeviceComputer {
		t.Fatalf("the host's default device: %+v", me)
	}
	var got api.SetDevice
	l.want(200, "web", "PUT", "/v1/internal/me/device", api.SetDevice{Device: api.DevicePhone}, &got)
	l.want(200, "web", "GET", "/v1/internal/me", nil, &me)
	if me.ID != "local" || !me.Host || me.Device != api.DevicePhone || got.Device != api.DevicePhone {
		t.Fatalf("the host's device: %+v", me)
	}
	var as api.Apps
	l.want(200, "socket", "GET", "/v1/internal/apps", nil, &as)
	if len(as.Apps) != 2 || as.Apps[0].Device != api.DevicePhone || as.Apps[1].Device != "" {
		t.Fatalf("listed: %+v", as.Apps)
	}
}

// An agent's ir asks for an invite: no secret comes back to it, a person sees the ask, allows it
// and alone gets the link; the agent learns only that it was allowed.
func TestConfirmations(t *testing.T) {
	t.Parallel()
	l := start(t)
	// Invites need a public origin.
	l.want(403, "agent", "POST", "/v1/internal/invites", api.NewInvite{Name: "pad"}, nil)
	l.want(200, "socket", "PUT", "/v1/internal/public", api.SetPublic{Origin: "https://ir.example.com"}, nil)
	var p api.Pending
	l.want(202, "agent", "POST", "/v1/internal/invites", api.NewInvite{Name: "pad"}, &p)
	if p.Need.ID == "" || p.Need.Kind != "invite" || p.Need.Asker != "主机" || p.Need.Session != "caller-session" {
		t.Fatalf("pending: %+v", p)
	}
	var cs api.Needs
	l.want(403, "agent", "GET", "/v1/internal/needs", nil, nil)
	l.want(200, "socket", "GET", "/v1/internal/needs", nil, &cs)
	if len(cs.Pending) != 1 || cs.Pending[0].ID != p.Need.ID {
		t.Fatalf("confirms: %+v", cs)
	}
	// The agent waits; meanwhile a person allows.
	got := make(chan string, 1)
	go func() {
		var raw string
		l.do("agent", "GET", "/v1/internal/needs/"+p.Need.ID+"?wait=10", nil, &raw)
		got <- raw
	}()
	time.Sleep(100 * time.Millisecond)
	l.want(403, "agent", "POST", "/v1/internal/needs/"+p.Need.ID, api.NeedAnswer{Allow: true}, nil)
	var ans api.NeedReply
	l.want(200, "socket", "POST", "/v1/internal/needs/"+p.Need.ID, api.NeedAnswer{Allow: true}, &ans)
	if ans.State != "allowed" || ans.Invite == nil || !strings.Contains(ans.Invite.URL, "/_ir/join#") {
		t.Fatalf("answer: %+v", ans)
	}
	raw := <-got
	if !strings.Contains(raw, `"state":"allowed"`) || strings.Contains(raw, "/_ir/join") {
		t.Fatalf("the agent's view: %s", raw)
	}
	l.want(422, "socket", "POST", "/v1/internal/needs/"+p.Need.ID, api.NeedAnswer{Allow: true}, nil) // once
	// Denied.
	l.want(202, "agent", "POST", "/v1/internal/invites", api.NewInvite{Name: "other"}, &p)
	ans = api.NeedReply{}
	l.want(200, "socket", "POST", "/v1/internal/needs/"+p.Need.ID, api.NeedAnswer{Allow: false}, &ans)
	var st api.NeedState
	l.want(200, "agent", "GET", "/v1/internal/needs/"+p.Need.ID, nil, &st)
	if st.State != "denied" || ans.Invite != nil {
		t.Fatalf("denied: %+v %+v", st, ans)
	}
	// A bad name is refused at once: a slash, a single letter, a reserved one.
	for _, name := range []string{"a/b", "x", "ir_web", "host"} {
		l.want(422, "agent", "POST", "/v1/internal/invites", api.NewInvite{Name: name}, nil)
	}
}

// Setting public.origin puts the passkey gate in front at once: the loopback page is no longer
// 本机, the public host is the only one served, the setup link is written, and the config file
// keeps it. An invite then is a join link on the public origin. Off again, it is 本机 again.
func TestPublicOrigin(t *testing.T) {
	t.Parallel()
	l := start(t)
	path := filepath.Join(t.TempDir(), "config.json")
	l.s.ConfigPath = path
	stream := watchStates(t, l.base, nil, "list")
	firstState(t, stream)
	l.want(403, "agent", "PUT", "/v1/internal/public", api.SetPublic{Origin: "https://ir.example.com"}, nil)
	l.want(403, "web", "PUT", "/v1/internal/public", api.SetPublic{Origin: "https://ir.example.com"}, nil)
	l.want(400, "socket", "PUT", "/v1/internal/public", api.SetPublic{Origin: "http://ir.example.com"}, nil)
	var ps api.PublicState
	l.want(200, "socket", "PUT", "/v1/internal/public", api.SetPublic{Origin: "https://ir.example.com"}, &ps)
	if ps.Origin != "https://ir.example.com" || ps.Passkeys {
		t.Fatalf("public: %+v", ps)
	}
	if cfg, err := config.Load(path); err != nil || cfg.Public.Origin != "https://ir.example.com" {
		t.Fatalf("config: %+v %v", cfg.Public, err)
	}
	if b, err := os.ReadFile(filepath.Join(l.cfg.StatePath(), "setup-token")); err != nil || !strings.HasPrefix(string(b), "https://ir.example.com/_ir/join#") {
		t.Fatalf("setup link: %v", err)
	}
	l.want(421, "web", "GET", "/v1/internal/agents", nil, nil)
	req, _ := http.NewRequest("GET", l.base+"/v1/internal/agents", nil)
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
	for deadline := time.After(5 * time.Second); ; {
		select {
		case _, ok := <-stream:
			if ok {
				continue
			}
		case <-deadline:
			t.Error("the old gate's stream stayed open")
		}
		break
	}
	// The socket is still 本机; a person's ir gets the link, an agent's a confirmation.
	var inv api.Invite
	l.want(200, "socket", "POST", "/v1/internal/invites", api.NewInvite{Name: "pad"}, &inv)
	if !strings.HasPrefix(inv.URL, "https://ir.example.com/_ir/join#") || inv.Name != "pad" || inv.ExpiresAt == 0 {
		t.Fatalf("invite: %+v", inv)
	}
	if !strings.HasPrefix(inv.QR, "data:image/svg+xml;base64,") {
		t.Fatalf("invite's QR: %.40q", inv.QR)
	}
	var p api.Pending
	l.want(202, "agent", "POST", "/v1/internal/invites", api.NewInvite{}, &p)
	var raw string
	l.do("socket", "POST", "/v1/internal/needs/"+p.Need.ID, api.NeedAnswer{Allow: true}, &raw)
	if !strings.Contains(raw, "https://ir.example.com/_ir/join#") || !strings.Contains(raw, `"qr":"data:image/svg+xml;base64,`) {
		t.Fatalf("the panel's answer: %s", raw)
	}
	l.do("agent", "GET", "/v1/internal/needs/"+p.Need.ID, nil, &raw)
	if !strings.Contains(raw, `"allowed"`) || strings.Contains(raw, "/_ir/join") || strings.Contains(raw, "qr") {
		t.Fatalf("the agent's view: %s", raw)
	}
	var as api.Apps
	l.want(200, "socket", "GET", "/v1/internal/apps", nil, &as)
	if len(as.Invites) != 3 { // setup, pad, the agent's
		t.Errorf("invites: %+v", as.Invites)
	}
	if len(as.Apps) != 2 || !as.Apps[1].Web { // the host, ir_web
		t.Errorf("apps: %+v", as.Apps)
	}
	l.want(200, "socket", "PUT", "/v1/internal/public", api.SetPublic{Origin: ""}, nil)
	l.want(200, "web", "GET", "/v1/internal/agents", nil, nil)
}

// newApp makes app name as a pairing allowed would, with Claude or not (said by a person at the
// host): its TOKEN. Pairing is the one way in (docs/next/decisions.md 9); tests take this short cut.
func (l *live) newApp(name string, claude bool) api.Token {
	l.t.Helper()
	info, tok, err := l.s.gate().CreateNewToken(name)
	if err != nil {
		l.t.Fatal(err)
	}
	if claude {
		if err := l.s.giveClaude(info, access.ByLocal); err != nil {
			l.t.Fatal(err)
		}
	}
	return api.Token{App: l.s.appOf(info), Token: tok}
}

// addPanel adds a login of ir_web named name, as one joined by an invite: its ID.
func (l *live) addPanel(name string) string {
	l.t.Helper()
	info, err := l.s.gate().CreateWebLogin(name)
	if err != nil {
		l.t.Fatal(err)
	}
	return info.ID
}
