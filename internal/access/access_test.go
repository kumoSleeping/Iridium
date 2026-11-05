package access

import (
	"errors"
	"testing"

	"github.com/kumoSleeping/iridium/internal/api"
)

func TestDefaults(t *testing.T) {
	full := Full()
	if Agent(full, api.AgentClaude) != nil || Agent(full, "") != nil || !full.SeeOthers || full.MustApprove {
		t.Errorf("本机: %+v", full)
	}
	panel := Default(api.TerminalPanel)
	if Agent(panel, api.AgentClaude) == nil || Agent(panel, api.AgentPi) != nil || !panel.SeeOthers || panel.MustApprove {
		t.Errorf("panel: %+v", panel)
	}
	ch := Default(api.TerminalChannel)
	if Agent(ch, api.AgentClaude) == nil || ch.SeeOthers || !ch.MustApprove {
		t.Errorf("channel: %+v", ch)
	}
	// claude in the list without the owner's confirmation is still no claude.
	ch.Agents = append(ch.Agents, api.AgentClaude)
	if Agent(ch, api.AgentClaude) == nil {
		t.Error("claude without a confirmation")
	}
	if Clean(ch).ClaudeOwner != nil || Clean(Default(api.TerminalPanel)).ClaudeOwner != nil {
		t.Error("an owner confirmation without claude")
	}
}

func TestWidens(t *testing.T) {
	base := Clean(api.Rules{Agents: []api.Agent{api.AgentPi}, Dirs: []string{"/w"}, Workspaces: []int64{1}, DailyTokens: 1000, Commands: []string{"/compact"}, MustApprove: true})
	for _, c := range []struct {
		name string
		edit func(*api.Rules)
		want bool
	}{
		{"same", func(r *api.Rules) {}, false},
		{"fewer agents", func(r *api.Rules) { r.Agents = nil }, false},
		{"another agent", func(r *api.Rules) { r.Agents = append(r.Agents, api.AgentCodex) }, true},
		{"a subdirectory", func(r *api.Rules) { r.Dirs = []string{"/w/sub"} }, false},
		{"a sibling directory", func(r *api.Rules) { r.Dirs = []string{"/wx"} }, true},
		{"every directory", func(r *api.Rules) { r.AllDirs = true }, true},
		{"another workspace", func(r *api.Rules) { r.Workspaces = []int64{2} }, true},
		{"others' sessions", func(r *api.Rules) { r.SeeOthers = true }, true},
		{"files in", func(r *api.Rules) { r.FilesIn = true }, true},
		{"answers", func(r *api.Rules) { r.Answer = true }, true},
		{"no approvals", func(r *api.Rules) { r.MustApprove = false }, true},
		{"a lower cap", func(r *api.Rules) { r.DailyTokens = 10 }, false},
		{"a higher cap", func(r *api.Rules) { r.DailyTokens = 10000 }, true},
		{"no cap", func(r *api.Rules) { r.DailyTokens = 0 }, true},
		{"another command", func(r *api.Rules) { r.Commands = append(r.Commands, "/mode") }, true},
		{"every command", func(r *api.Rules) { r.AllCommands = true }, true},
	} {
		next := base
		next.Agents = append([]api.Agent{}, base.Agents...)
		next.Commands = append([]string{}, base.Commands...)
		c.edit(&next)
		if got := Widens(base, Clean(next)); got != c.want {
			t.Errorf("%s: widens %v, want %v", c.name, got, c.want)
		}
	}
}

func TestChecks(t *testing.T) {
	r := Clean(api.Rules{Agents: []api.Agent{api.AgentPi}, Dirs: []string{"/w/a"}, Workspaces: []int64{7}, Commands: []string{"compact"}, MustApprove: true})
	paths := map[int64]string{7: "/ws7"}
	for _, c := range []struct {
		cwd  string
		ws   int64
		want error
	}{
		{"/w/a", 0, nil}, {"/w/a/b/c", 0, nil}, {"/w/ab", 0, ErrDir}, {"/w", 0, ErrDir},
		{"/w/a/../b", 0, ErrDir}, {"/elsewhere", 7, nil}, {"/ws7/x", 0, nil}, {"", 0, ErrDir},
	} {
		if err := Dir(r, c.cwd, c.ws, paths); !errors.Is(err, c.want) && err != c.want {
			t.Errorf("dir %q %d: %v", c.cwd, c.ws, err)
		}
	}
	for text, want := range map[string]error{"hello": nil, "/compact": nil, "/compact now": nil, "/mode bypassPermissions": ErrCommand, " /model\nx": ErrCommand} {
		if err := Command(r, text); err != want {
			t.Errorf("command %q: %v", text, err)
		}
	}
	if Mode(r, "bypassPermissions") != ErrMustApprove || Mode(r, "default") != nil || Mode(r, "plan") != nil || Mode(Full(), "bypassPermissions") != nil {
		t.Error("modes")
	}
	if Visible(r, "me", api.AgentPi, "/w/a", 0, "") {
		t.Error("a channel sees 本机's session")
	}
	r.SeeOthers = true
	if !Visible(r, "me", api.AgentPi, "/w/a", 0, "") || Visible(r, "me", api.AgentClaude, "/w/a", 0, "") || Visible(r, "me", api.AgentPi, "/x", 0, "") {
		t.Error("visible")
	}
	r.SeeOthers = false
	if !Visible(r, "me", api.AgentPi, "/w/a", 0, "me") || Visible(r, "me", api.AgentPi, "/w/a", 0, "you") {
		t.Error("own sessions")
	}
	if Validate(api.Rules{Agents: []api.Agent{"gpt"}}) == nil || Validate(api.Rules{Dirs: []string{"rel"}}) == nil || Validate(Full()) != nil {
		t.Error("validate")
	}
}
