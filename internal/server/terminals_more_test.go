package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/chat"
	"github.com/kumoSleeping/iridium/internal/config"
	"github.com/kumoSleeping/iridium/internal/discover"
)

// A plugin's chat requests are its terminal's, by its token: its permissions (no Claude until the
// owner says so; approvals on), its tag with the sender, its usage. Its files go in and out as its
// table says, and what an agent does through ir in its session is the plugin's too.
func TestChatFilesAndAgents(t *testing.T) {
	l := startWith(t, func(s *Server, cfg config.Config, work string) {
		var err error
		s.Chat, err = chat.New(chat.Options{Manager: s.m, Store: s.st, StateDir: cfg.StateDir, ChatDir: work, Logf: t.Logf, Guard: s.ChatGuard()})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(s.Chat.Close)
		empty := t.TempDir()
		s.Discover = discover.New(discover.Options{ClaudeDir: cfg.ClaudeHome, CodexHome: empty, PiSessions: empty, DshHome: empty, Store: s.st, Hub: s.hub, Procs: discover.ProcFS{Root: empty}, Logf: t.Logf})
		s.Folders = discover.DefaultFolders("/")
	})
	var tok api.Token
	l.want(200, "socket", "POST", "/api/terminals/tokens", api.NewInvite{Name: "bot"}, &tok)
	bot, botID := tok.Token, tok.Terminal.ID

	// The chat opens Claude by default: not for this plugin yet.
	l.want(403, bot, "POST", "/api/chat/qq:1/input", api.ChatInput{Text: "hi", Sender: "张三"}, nil)
	rules := tok.Terminal.Rules
	rules.Agents = append(rules.Agents, api.AgentClaude)
	if code := l.rules("web", botID, rules, true); code != 200 {
		t.Fatalf("claude for the plugin: %d", code)
	}
	var in api.ChatInputReply
	l.want(200, bot, "POST", "/api/chat/qq:1/input", api.ChatInput{Text: "hi", Sender: "张三"}, &in)
	l.waitReply(bot, in.Session, "ok: hi")
	var page api.MessagePage
	l.want(200, bot, "GET", "/api/sessions/"+in.Session+"/messages", nil, &page)
	if page.Blocks[0].From != "bot·张三" || page.Blocks[0].FromTerminal != botID || page.Blocks[0].Text != "hi" {
		t.Fatalf("the chat's message: %+v", page.Blocks[0])
	}
	meta, _ := os.ReadFile(filepath.Join(l.cfg.StatePath(), "hold", in.Session, "session.json"))
	if !strings.Contains(string(meta), `"mode":"manual"`) {
		t.Errorf("the plugin's session asks for no approvals: %s", meta)
	}
	waitUsage(t, l.s.st, botID)
	// /mode to one that skips approvals: refused; a command not allowed: refused.
	l.want(403, bot, "POST", "/api/chat/qq:1/input", api.ChatInput{Text: "/mode bypassPermissions"}, nil)
	narrow := rules
	narrow.Agents = append([]api.Agent{}, rules.Agents...)
	narrow.AllCommands, narrow.Commands = false, []string{"/status"}
	if code := l.rules("agent", botID, narrow, false); code != 200 {
		t.Fatalf("narrowing: %d", code)
	}
	l.want(403, bot, "POST", "/api/chat/qq:1/input", api.ChatInput{Text: "/stop"}, nil)
	l.want(200, bot, "POST", "/api/chat/qq:1/input", api.ChatInput{Text: "/status"}, nil)

	// Files: out and in as the table says; the guard comes before the lookup.
	l.want(404, bot, "GET", "/api/sessions/"+in.Session+"/files/nope", nil, nil)
	l.want(200, bot, "POST", "/api/sessions/"+in.Session+"/uploads", api.NewUpload{Name: "a.txt", Size: 3}, nil)
	closed := narrow
	closed.FilesIn, closed.FilesOut = false, false
	if code := l.rules("agent", botID, closed, false); code != 200 {
		t.Fatalf("closing files: %d", code)
	}
	l.want(403, bot, "GET", "/api/sessions/"+in.Session+"/files/nope", nil, nil)
	l.want(403, bot, "GET", "/api/sessions/"+in.Session+"/files/nope/thumb", nil, nil)
	l.want(403, bot, "POST", "/api/sessions/"+in.Session+"/uploads", api.NewUpload{Name: "a.txt", Size: 3}, nil)
	l.want(403, bot, "PUT", "/api/uploads/whatever?offset=0", nil, nil)
	l.want(403, bot, "POST", "/api/sessions/"+in.Session+"/send", api.Send{Text: "see", Attachments: []string{"x"}}, nil)

	// An agent's ir in the plugin's session is the plugin: its usage, its directories, no managing.
	as := "agent-in:" + in.Session
	var sent api.SendReply
	l.want(200, as, "POST", "/api/sessions/"+in.Session+"/send", api.Send{Text: "from inside"}, &sent)
	if id, tag, ok := l.s.st.Source(sent.Input); !ok || id != botID || tag != "bot" {
		t.Errorf("the agent's message: %q %q %v", id, tag, ok)
	}
	l.want(403, as, "GET", "/api/terminals", nil, nil)
	l.want(403, as, "POST", "/api/terminals/tokens", api.NewInvite{Name: "x"}, nil)
	dirs := closed
	dirs.AllDirs, dirs.Dirs = false, []string{l.work}
	if code := l.rules("agent", botID, dirs, false); code != 200 {
		t.Fatalf("dirs: %d", code)
	}
	l.want(200, as, "GET", "/api/files?path="+l.work, nil, nil)
	l.want(403, as, "GET", "/api/files?path=/etc", nil, nil)
	l.want(403, as, "POST", "/api/files/copy", api.Copy{From: "/etc/hostname", To: filepath.Join(l.work, "h")}, nil)
	l.want(403, as, "GET", "/api/folders?path=/etc", nil, nil)
	l.want(403, as, "POST", "/api/workspaces", api.NewWorkspace{Path: l.work}, nil)
	// One in a session 本机 opened stays 本机.
	var mine api.NewSessionReply
	l.want(200, "socket", "POST", "/api/sessions", api.NewSession{Agent: api.AgentClaude, Cwd: l.work, Text: "mine"}, &mine)
	l.waitReply("socket", mine.ID, "ok: mine")
	l.want(200, "agent-in:"+mine.ID, "POST", "/api/sessions/"+mine.ID+"/send", api.Send{Text: "again"}, &sent)
	if id, _, _ := l.s.st.Source(sent.Input); id != "local" {
		t.Errorf("本机's session's agent: %q", id)
	}
	l.want(200, "agent-in:"+mine.ID, "GET", "/api/terminals", nil, nil)
}
