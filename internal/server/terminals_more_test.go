package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/config"
	"github.com/kumoSleeping/iridium/internal/discover"
)

// An app's requests are its own, by its TOKEN: Claude only if it was made the owner's own,
// nothing else held back; its tag, its usage; files in and out. What an agent does through ir in
// its session is the app's too.
func TestAppSessionsAndAgents(t *testing.T) {
	l := startWith(t, func(s *Server, cfg config.Config, work string) {
		empty := t.TempDir()
		s.Discover = discover.New(discover.Options{ClaudeDir: cfg.ClaudeHome, CodexHome: empty, PiSessions: empty, DshHome: empty, Store: s.st, Hub: s.hub, Procs: discover.ProcFS{Root: empty}, Logf: t.Logf})
		s.Folders = discover.DefaultFolders("/")
	})
	tok := l.newApp("nobot", false)
	l.want(403, tok.Token, "POST", "/api/sessions", api.NewSession{Agent: api.AgentClaude, Cwd: l.work, Text: "hi"}, nil)

	tok = l.newApp("bot", true)
	bot, botID := tok.Token, tok.App.ID
	var created api.NewSessionReply
	l.want(200, bot, "POST", "/api/sessions", api.NewSession{Agent: api.AgentClaude, Cwd: l.work, Text: "hi"}, &created)
	sid := created.ID
	l.waitReply(bot, sid, "ok: hi")
	var page api.MessagePage
	l.want(200, bot, "GET", "/api/sessions/"+sid+"/messages", nil, &page)
	if page.Blocks[0].From != "bot" || page.Blocks[0].FromTerminal != botID || page.Blocks[0].Text != "hi" {
		t.Fatalf("the app's message: %+v", page.Blocks[0])
	}
	meta, _ := os.ReadFile(filepath.Join(l.cfg.StatePath(), "hold", sid, "session.json"))
	if strings.Contains(string(meta), `"mode":"manual"`) {
		t.Errorf("approvals forced on an app's session: %s", meta)
	}
	waitUsage(t, l.s.st, botID)

	// Files: out and in; a file there is not is no such file.
	l.want(404, bot, "GET", "/api/sessions/"+sid+"/files/nope", nil, nil)
	l.want(200, bot, "POST", "/api/sessions/"+sid+"/uploads", api.NewUpload{Name: "a.txt", Size: 3}, nil)

	// An agent's ir in the app's session is the app: its usage, no managing.
	as := "agent-in:" + sid
	var sent api.SendReply
	l.want(200, as, "POST", "/api/sessions/"+sid+"/send", api.Send{Text: "from inside"}, &sent)
	if id, tag, ok := l.s.st.Source(sent.Input); !ok || id != botID || tag != "ir:"+sid {
		t.Errorf("the agent's message: %q %q %v", id, tag, ok)
	}
	// ir send names the session it runs in but no agent flag: still a letter from that session,
	// still the app's to count.
	l.want(200, "ir-in:"+sid, "POST", "/api/sessions/"+sid+"/send", api.Send{Text: "by ir send"}, &sent)
	if id, tag, ok := l.s.st.Source(sent.Input); !ok || id != botID || tag != "ir:"+sid {
		t.Errorf("ir send's message: %q %q %v", id, tag, ok)
	}
	l.waitReply(bot, sid, "ok: by ir send")
	l.want(200, bot, "GET", "/api/sessions/"+sid+"/messages", nil, &page)
	if b := page.Blocks[len(page.Blocks)-2]; b.From != "ir:"+sid || b.FromTerminal != botID {
		t.Errorf("ir send's block: %+v", b)
	}
	l.want(403, as, "GET", "/api/apps", nil, nil)
	l.want(403, as, "DELETE", "/api/apps/nobot", nil, nil)
	l.want(200, as, "GET", "/api/files?path="+l.work, nil, nil)
	l.want(403, as, "PATCH", "/api/workspaces/1", api.WorkspacePatch{Name: new("x")}, nil)
	// One in a session 本机 opened stays 本机.
	var mine api.NewSessionReply
	l.want(200, "socket", "POST", "/api/sessions", api.NewSession{Agent: api.AgentClaude, Cwd: l.work, Text: "mine"}, &mine)
	l.waitReply("socket", mine.ID, "ok: mine")
	l.want(200, "agent-in:"+mine.ID, "POST", "/api/sessions/"+mine.ID+"/send", api.Send{Text: "again"}, &sent)
	if id, tag, _ := l.s.st.Source(sent.Input); id != "local" || tag != "ir:"+mine.ID {
		t.Errorf("本机's session's agent: %q %q", id, tag)
	}
	l.want(200, "agent-in:"+mine.ID, "GET", "/api/apps", nil, nil)
}
