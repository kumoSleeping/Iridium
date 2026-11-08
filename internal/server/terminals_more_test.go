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

// A plugin's chat requests are its terminal's, by its token: Claude only if it was made the
// owner's own, nothing else held back; its tag with the sender, its usage; files in and out. What
// an agent does through ir in its session is the plugin's too.
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
	l.want(200, "socket", "POST", "/api/terminals/tokens", api.NewInvite{Name: "nobot"}, &tok)
	// The chat opens Claude by default: not for a plugin made without --owner.
	l.want(403, tok.Token, "POST", "/api/chat/qq:1/input", api.ChatInput{Text: "/new claude"}, nil)

	l.want(200, "socket", "POST", "/api/terminals/tokens", api.NewInvite{Name: "bot", Owner: true}, &tok)
	bot, botID := tok.Token, tok.Terminal.ID
	var in api.ChatInputReply
	l.want(200, bot, "POST", "/api/chat/qq:1/input", api.ChatInput{Text: "/new claude"}, nil)
	l.want(200, bot, "POST", "/api/chat/qq:1/input", api.ChatInput{Text: "hi", Sender: "张三"}, &in)
	l.waitReply(bot, in.Session, "ok: hi")
	var page api.MessagePage
	l.want(200, bot, "GET", "/api/sessions/"+in.Session+"/messages", nil, &page)
	if page.Blocks[0].From != "bot·张三" || page.Blocks[0].FromTerminal != botID || page.Blocks[0].Text != "hi" {
		t.Fatalf("the chat's message: %+v", page.Blocks[0])
	}
	meta, _ := os.ReadFile(filepath.Join(l.cfg.StatePath(), "hold", in.Session, "session.json"))
	if strings.Contains(string(meta), `"mode":"manual"`) {
		t.Errorf("approvals forced on a plugin's session: %s", meta)
	}
	waitUsage(t, l.s.st, botID)
	// Every command.
	l.want(200, bot, "POST", "/api/chat/qq:1/input", api.ChatInput{Text: "/status"}, nil)
	l.want(200, bot, "POST", "/api/chat/qq:1/input", api.ChatInput{Text: "/model"}, nil)

	// Files: out and in; a file there is not is no such file.
	l.want(404, bot, "GET", "/api/sessions/"+in.Session+"/files/nope", nil, nil)
	l.want(200, bot, "POST", "/api/sessions/"+in.Session+"/uploads", api.NewUpload{Name: "a.txt", Size: 3}, nil)

	// An agent's ir in the plugin's session is the plugin: its usage, no managing.
	as := "agent-in:" + in.Session
	var sent api.SendReply
	l.want(200, as, "POST", "/api/sessions/"+in.Session+"/send", api.Send{Text: "from inside"}, &sent)
	if id, tag, ok := l.s.st.Source(sent.Input); !ok || id != botID || tag != "ir:"+in.Session {
		t.Errorf("the agent's message: %q %q %v", id, tag, ok)
	}
	// ir send names the session it runs in but no agent flag: still a letter from that session,
	// still the plugin's to count.
	l.want(200, "ir-in:"+in.Session, "POST", "/api/sessions/"+in.Session+"/send", api.Send{Text: "by ir send"}, &sent)
	if id, tag, ok := l.s.st.Source(sent.Input); !ok || id != botID || tag != "ir:"+in.Session {
		t.Errorf("ir send's message: %q %q %v", id, tag, ok)
	}
	l.waitReply(bot, in.Session, "ok: by ir send")
	l.want(200, bot, "GET", "/api/sessions/"+in.Session+"/messages", nil, &page)
	if b := page.Blocks[len(page.Blocks)-2]; b.From != "ir:"+in.Session || b.FromTerminal != botID {
		t.Errorf("ir send's block: %+v", b)
	}
	l.want(403, as, "GET", "/api/terminals", nil, nil)
	l.want(403, as, "POST", "/api/terminals/tokens", api.NewInvite{Name: "x"}, nil)
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
	l.want(200, "agent-in:"+mine.ID, "GET", "/api/terminals", nil, nil)
}
