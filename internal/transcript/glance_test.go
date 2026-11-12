package transcript

import (
	"github.com/kumoSleeping/iridium/internal/api"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A subagent is done once its transcript ends with its final answer, as Claude writes them: a
// reply that calls no tool, or the tool result that ends the turn (a workflow agent's
// StructuredOutput), or once it is stopped. Progress lines after it do not count.
func TestGlanceSubagentEnded(t *testing.T) {
	const (
		prompt  = `{"type":"user","isSidechain":true,"sessionId":"p","cwd":"/w","message":{"role":"user","content":"look"}}`
		call    = `{"type":"assistant","isSidechain":true,"message":{"role":"assistant","stop_reason":"tool_use","content":[{"type":"tool_use","id":"t","name":"Bash","input":{}}]}}`
		result  = `{"type":"user","isSidechain":true,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t","content":"ok"}]}}`
		answer  = `{"type":"assistant","isSidechain":true,"message":{"role":"assistant","stop_reason":"end_turn","content":[{"type":"text","text":"done"}]}}`
		final   = `{"type":"user","isSidechain":true,"toolEndsTurn":true,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"s","content":"Structured output provided successfully"}]}}`
		request = `{"type":"api-request","sessionId":"p"}`
		attach  = `{"type":"attachment","isSidechain":true,"attachment":{}}`
		stop    = `{"type":"user","isSidechain":true,"message":{"role":"user","content":[{"type":"text","text":"[Request interrupted by user]"}]}}`
		stopUse = `{"type":"user","isSidechain":true,"message":{"role":"user","content":"[Request interrupted by user for tool use]"}}`
		again   = `{"type":"user","isSidechain":true,"message":{"role":"user","content":"go on"}}`
		// A fork's first line (Claude Code, 2026-11): its parent's history by reference.
		forkRef = `{"type":"fork-context-ref","agentId":"a","parentSessionId":"p","parentLastUuid":"u","contextLength":111}`
	)
	cases := []struct {
		name  string
		lines []string
		ended bool
	}{
		{"just asked", []string{prompt}, false},
		{"calling a tool", []string{prompt, call}, false},
		{"tool answered", []string{prompt, call, result, request}, false},
		{"final answer", []string{prompt, call, result, answer}, true},
		{"final answer, then progress", []string{prompt, answer, attach, request}, true},
		{"structured output", []string{prompt, call, result, attach, final}, true},
		{"stopped", []string{prompt, call, result, stop, attach}, true},
		{"stopped in a tool call", []string{prompt, call, stopUse}, true},
		{"stopped, then woken", []string{prompt, call, stop, again, call}, false},
		{"a fork, at work", []string{forkRef, prompt, call}, false},
		{"a fork, answered", []string{forkRef, prompt, call, result, answer}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "agent-a.jsonl")
			if err := os.WriteFile(p, []byte(strings.Join(c.lines, "\n")+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			g, err := Glance(p, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !g.Sidechain || g.Parent != "p" {
				t.Fatalf("sidechain %v parent %q", g.Sidechain, g.Parent)
			}
			if g.Ended != c.ended {
				t.Fatalf("ended %v, want %v", g.Ended, c.ended)
			}
		})
	}
}

// A background task's end as one line: its summary, "Dynamic workflow" said Workflow, how many
// agents and how long.
func TestTaskLine(t *testing.T) {
	note := "<task-notification>\n<task-id>w1</task-id>\n<status>completed</status>\n<summary>Dynamic workflow \"Fix the caret\" completed</summary>\n<result>{\"a\":\"<summary>not this</summary>\"}</result>\n<usage><agent_count>9</agent_count><tool_uses>3</tool_uses><duration_ms>2187375</duration_ms></usage>\n</task-notification>"
	if got := taskLine([]string{note}); got != `Workflow "Fix the caret" completed · 9 agents · 36m` {
		t.Errorf("workflow: %q", got)
	}
	if got := taskLine([]string{"<task-notification><status>killed</status><usage><agent_count>1</agent_count><duration_ms>4000</duration_ms></usage></task-notification>"}); got != "Task killed · 1 agent · 4s" {
		t.Errorf("no summary: %q", got)
	}
	if got := taskLine([]string{"nothing here"}); got != "" {
		t.Errorf("not a notice: %q", got)
	}
	if took(72*time.Minute) != "1h 12m" {
		t.Errorf("took: %s", took(72*time.Minute))
	}
}

// Both ways Claude records a task's end become the same quiet line: an attachment mid-turn, a user
// line marked by its origin while idle; neither is your words.
func TestTaskNotificationBlocks(t *testing.T) {
	note := `<task-notification>\n<status>completed</status>\n<summary>Dynamic workflow \"UI test\" completed</summary>\n<usage><agent_count>5</agent_count><duration_ms>46581</duration_ms></usage>\n</task-notification>`
	for name, line := range map[string]string{
		"idle":     `{"type":"user","origin":{"kind":"task-notification","producer":"session-task"},"message":{"role":"user","content":"` + note + `"},"timestamp":"2026-11-06T02:50:00Z"}`,
		"mid-turn": `{"type":"attachment","attachment":{"type":"queued_command","origin":{"kind":"task-notification"},"prompt":"` + note + `"},"timestamp":"2026-11-06T02:50:00Z"}`,
	} {
		blocks := blocksOf(0, []byte(line), Clip, nil)
		if len(blocks) != 1 || blocks[0].Kind != api.BlockKindSystem || blocks[0].Event != api.SystemEventTask || blocks[0].Text != `Workflow "UI test" completed · 5 agents · 47s` {
			t.Errorf("%s: %+v", name, blocks)
		}
	}
}
