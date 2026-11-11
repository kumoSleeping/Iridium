package server

import (
	"testing"

	"github.com/kumoSleeping/iridium/internal/api"
)

// A session started by ir inside another session (no caller in the body) hangs under that one,
// in its folder for "."; one the page starts has no launcher.
func TestLauncherFromSession(t *testing.T) {
	t.Parallel()
	l := start(t)
	var top api.NewSessionReply
	l.want(200, "web", "POST", "/v1/internal/sessions", api.NewSession{Agent: api.AgentClaude, Cwd: l.work}, &top)
	var child api.NewSessionReply
	l.want(200, "agent-in:"+top.ID, "POST", "/v1/internal/sessions", api.NewSession{Agent: api.AgentClaude, Cwd: "."}, &child)
	var list []api.Session
	l.want(200, "web", "GET", "/v1/internal/sessions", nil, &list)
	got := map[string]api.Session{}
	for _, s := range list {
		got[s.ID] = s
	}
	if s := got[top.ID]; s.Launcher != "" {
		t.Errorf("the page's session has a launcher: %+v", s)
	}
	if s := got[child.ID]; s.Launcher != top.ID || s.Cwd != l.work {
		t.Errorf("the child: launcher %q cwd %q, want %q %q", s.Launcher, s.Cwd, top.ID, l.work)
	}
}
