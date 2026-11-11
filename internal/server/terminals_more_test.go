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
	t.Parallel()
	l := startWith(t, func(s *Server, cfg config.Config, work string) {
		empty := t.TempDir()
		s.Discover = discover.New(discover.Options{ClaudeDir: cfg.ClaudeHome, CodexHome: empty, PiSessions: empty, DshHome: empty, Store: s.st, Hub: s.hub, Procs: discover.ProcFS{Root: empty}, Logf: t.Logf})
		s.Folders = discover.DefaultFolders("/")
	})
	tok := l.newApp("nobot", false)
	l.create(403, tok.Token, api.NewSession{Agent: api.AgentClaude, Cwd: l.work, Text: "hi"})

	tok = l.newApp("bot", true)
	bot, botID := tok.Token, tok.App.ID
	created := l.create(200, bot, api.NewSession{Agent: api.AgentClaude, Cwd: l.work, Text: "hi"})
	sid := created
	l.waitReply(bot, sid, "ok: hi")
	page := l.page(bot, sid)
	// As its login (an app speaks Satori as one): app·login.
	if b := page.Blocks[0]; b.From != "bot·bot" || b.FromLogin != "main" || b.FromTerminal != botID || b.Text != "hi" {
		t.Fatalf("the app's message: %+v", page.Blocks[0])
	}
	meta, _ := os.ReadFile(filepath.Join(l.cfg.StatePath(), "hold", sid, "session.json"))
	if strings.Contains(string(meta), `"mode":"manual"`) {
		t.Errorf("approvals forced on an app's session: %s", meta)
	}
	waitUsage(t, l.s.st, botID)

	// Files: out and in; a file there is not is no such file.
	l.want(422, bot, "GET", "/v1/internal/sessions/"+sid+"/files/nope", nil, nil)
	l.want(200, bot, "POST", "/v1/internal/sessions/"+sid+"/uploads", api.NewUpload{Name: "a.txt", Size: 3}, nil)

	// An agent's ir in the app's session is the app: its usage, no managing.
	as := "agent-in:" + sid
	_, sent := l.send(as, sid, api.Send{Text: "from inside"})
	if id, tag, ok := l.s.st.Source(sent.Input); !ok || id != botID || tag != "ir:"+sid {
		t.Errorf("the agent's message: %q %q %v", id, tag, ok)
	}
	// ir send names the session it runs in but no agent flag: still a letter from that session,
	// still the app's to count.
	_, sent = l.send("ir-in:"+sid, sid, api.Send{Text: "by ir send"})
	if id, tag, ok := l.s.st.Source(sent.Input); !ok || id != botID || tag != "ir:"+sid {
		t.Errorf("ir send's message: %q %q %v", id, tag, ok)
	}
	l.waitReply(bot, sid, "ok: by ir send")
	page = l.page(bot, sid)
	if b := page.Blocks[len(page.Blocks)-2]; b.From != "ir:"+sid || b.FromTerminal != botID {
		t.Errorf("ir send's block: %+v", b)
	}
	// The list is public; what manages it is not.
	var pub api.Apps
	if l.want(200, as, "GET", "/v1/internal/apps", nil, &pub); pub.Full || pub.Apps[0].UsedToday != 0 {
		t.Errorf("the public list: %+v", pub)
	}
	l.want(403, as, "DELETE", "/v1/internal/apps/nobot", nil, nil)
	l.want(200, as, "GET", "/v1/internal/files?path="+l.work, nil, nil)
	l.want(403, as, "PATCH", "/v1/internal/guilds/1", api.GuildPatch{Name: new("x")}, nil)
	// One in a session 本机 opened stays 本机.
	mine := l.create(200, "socket", api.NewSession{Agent: api.AgentClaude, Cwd: l.work, Text: "mine"})
	l.waitReply("socket", mine, "ok: mine")
	_, sent = l.send("agent-in:"+mine, mine, api.Send{Text: "again"})
	if id, tag, _ := l.s.st.Source(sent.Input); id != "local" || tag != "ir:"+mine {
		t.Errorf("本机's session's agent: %q %q", id, tag)
	}
	l.want(200, "agent-in:"+mine, "GET", "/v1/internal/apps", nil, nil)
}
