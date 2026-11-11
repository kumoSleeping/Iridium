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
	top := l.create(200, "web", api.NewSession{Agent: api.AgentClaude, Cwd: l.work})
	child := l.create(200, "agent-in:"+top, api.NewSession{Agent: api.AgentClaude, Cwd: "."})
	var list []api.Session
	list = l.sessions("web")
	got := map[string]api.Session{}
	for _, s := range list {
		got[s.ID] = s
	}
	if s := got[top]; s.Launcher != "" {
		t.Errorf("the page's session has a launcher: %+v", s)
	}
	if s := got[child]; s.Launcher != top || s.Cwd != l.work {
		t.Errorf("the child: launcher %q cwd %q, want %q %q", s.Launcher, s.Cwd, top, l.work)
	}
}
