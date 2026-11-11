package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/config"
	"github.com/kumoSleeping/iridium/internal/discover"
	"github.com/kumoSleeping/iridium/internal/satori"
)

// irSatori is ir api: a Satori method on the socket, as the host's ir ("socket") or an agent's in
// a session ("agent-in:<id>"), speaking as login (platform/user; "" for no headers).
func (l *live) irSatori(as, login, method string, body, out any) (int, string) {
	l.t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", "http://ir/v1/"+method, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if p, u, ok := strings.Cut(login, "/"); ok {
		req.Header.Set(satori.HeaderPlatform, p)
		req.Header.Set(satori.HeaderUser, u)
	}
	if id, ok := strings.CutPrefix(as, "agent-in:"); ok {
		req.Header.Set(HeaderAgent, "1")
		req.Header.Set(HeaderSession, id)
	}
	resp, err := l.sock.Do(req)
	if err != nil {
		l.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if out != nil {
		json.Unmarshal(raw, out)
	}
	return resp.StatusCode, string(raw)
}

// ir speaks Satori on the socket (ir api): the host's ir as local/local, an agent's as the login
// its session's turn is for; channels carry Iridium's own in _data; channel.update
// archives and brings back; an agent's name never covers one a person gave.
func TestSatoriOnSocket(t *testing.T) {
	t.Parallel()
	l := startWith(t, func(s *Server, cfg config.Config, work string) {
		empty := t.TempDir()
		s.Discover = discover.New(discover.Options{ClaudeDir: cfg.ClaudeHome, CodexHome: empty, PiSessions: empty, DshHome: empty, Store: s.st, Hub: s.hub, Procs: discover.ProcFS{Root: empty}, Logf: t.Logf})
	})
	made := l.create(200, "web", api.NewSession{Agent: api.AgentClaude, Cwd: l.work, Text: "one"})
	l.waitReply("web", made, "ok: one")

	// Who ir is: meta names the one login it speaks as.
	var meta satori.Meta
	if code, raw := l.irSatori("socket", "", "meta", nil, &meta); code != 200 || len(meta.Logins) != 1 ||
		meta.Logins[0].Platform != "local" || meta.Logins[0].User == nil || meta.Logins[0].User.ID != "local" {
		t.Fatalf("meta: %d %s", code, raw)
	}
	// The headers are Satori's: none, refused.
	if code, raw := l.irSatori("socket", "", "channel.list", nil, nil); code != 403 || !strings.Contains(raw, "no_such_login") {
		t.Fatalf("no headers: %d %s", code, raw)
	}

	var list satori.List[satori.Channel]
	if code, raw := l.irSatori("socket", "local/local", "channel.list", nil, &list); code != 200 {
		t.Fatalf("channel.list: %d %s", code, raw)
	}
	var ch *satori.Channel
	for i := range list.Data {
		if list.Data[i].ID == made {
			ch = &list.Data[i]
		}
	}
	if ch == nil || ch.Data == nil || ch.Data.Agent != api.AgentClaude || ch.Data.Cwd != l.work || ch.Data.Last == nil {
		t.Fatalf("channel: %+v", ch)
	}

	// message.create from the host's ir: into the session, as POST .../send had it.
	if code, raw := l.irSatori("socket", "local/local", "message.create", map[string]any{"channel_id": made, "content": "two &lt; three"}, nil); code != 200 {
		t.Fatalf("message.create: %d %s", code, raw)
	}
	l.waitReply("web", made, "ok: two < three")

	// Archived: out of the list, but for _data.all; and back.
	yes, no := true, false
	l.irSatori("socket", "local/local", "channel.update", map[string]any{"channel_id": made, "_data": map[string]any{"archived": yes}}, nil)
	listed := func(all bool) bool {
		var lst satori.List[satori.Channel]
		l.irSatori("socket", "local/local", "channel.list", map[string]any{"_data": map[string]any{"all": all}}, &lst)
		for _, c := range lst.Data {
			if c.ID == made {
				return true
			}
		}
		return false
	}
	if listed(false) || !listed(true) {
		t.Fatalf("archived: listed %v, all %v", listed(false), listed(true))
	}
	l.irSatori("socket", "local/local", "channel.update", map[string]any{"channel_id": made, "_data": map[string]any{"archived": no}}, nil)
	if !listed(false) {
		t.Fatal("not back")
	}

	// A name a person gave (the host's ir, not an agent's) stands against an agent's.
	l.irSatori("socket", "local/local", "channel.update", map[string]any{"channel_id": made, "data": map[string]any{"name": "Mine"}}, nil)
	if code, raw := l.irSatori("agent-in:"+made, "local/local", "channel.update", map[string]any{"channel_id": made, "data": map[string]any{"name": "Agent's"}}, nil); code != 409 {
		t.Fatalf("agent over a person's name: %d %s", code, raw)
	}
	var st api.SessionState
	l.want(200, "socket", "GET", "/v1/internal/sessions/"+made, nil, &st)
	if st.Name != "Mine" {
		t.Fatalf("name: %q", st.Name)
	}
}
