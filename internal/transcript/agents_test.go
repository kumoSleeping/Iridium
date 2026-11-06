package transcript

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kumoSleeping/iridium/internal/api"
)

func kindsOf(blocks []api.Block) string {
	var k []string
	for _, b := range blocks {
		s := string(b.Kind)
		if b.Event != "" {
			s += ":" + string(b.Event)
		}
		k = append(k, s)
	}
	return strings.Join(k, " ")
}

// testdata/codex-*.jsonl are Rhodium's recorded rollouts (codex-cli 0.162.1): only the
// item_completed and turn_aborted events give blocks; response_item (what Codex sends the model)
// none.
func TestCodexTranscript(t *testing.T) {
	sources := func(id string) (string, string, bool) {
		if id == "00000000-0000-4000-8000-1eb4566700b7" {
			return "pppppppppppppppp", "网页", true
		}
		return "", "", false
	}
	f := OpenAs("testdata/codex-thread.jsonl", Codex, sources)
	page, err := f.Latest()
	if err != nil {
		t.Fatal(err)
	}
	if got := kindsOf(page.Blocks); got != "user tool tool assistant user assistant system:compact user assistant" {
		t.Fatalf("blocks: %s", got)
	}
	cmd, patch := page.Blocks[1], page.Blocks[2]
	if cmd.Name != "exec_command" || !strings.Contains(cmd.Input, `"command": "env | grep ^RHODIUM_ | sort"`) || cmd.Result == nil || !strings.HasPrefix(cmd.Result.Text, "RHODIUM_SLOT=") || cmd.Result.Error {
		t.Errorf("command: %+v %+v", cmd, cmd.Result)
	}
	if patch.Name != "apply_patch" || !strings.Contains(patch.Input, `"path": "/tmp/codex-probe/work/project/hello.txt"`) || patch.Result == nil || !strings.HasPrefix(patch.Result.Text, "Success.") {
		t.Errorf("patch: %+v", patch)
	}
	if a := page.Blocks[3]; a.Text != "ok" || a.Time == 0 {
		t.Errorf("assistant: %+v", a)
	}
	// The last message went in with clientUserMessageId; its client_id names who sent it.
	if u := page.Blocks[7]; u.ID != "00000000-0000-4000-8000-1eb4566700b7" || u.From != "网页" || u.Text != "Reply with the single word: pong." {
		t.Errorf("user: %+v", u)
	}
	if last, _ := f.LastSaid(); last == nil || last.Text != "pong" {
		t.Errorf("last: %+v", last)
	}
	if title, _ := f.Title(); !strings.HasPrefix(title, "Run the shell command") {
		t.Errorf("title: %q", title)
	}
	// Full gives the whole input of a call at its position.
	full, err := f.Full(cmd.At)
	if err != nil || !strings.Contains(full, "env | grep") {
		t.Errorf("full: %q %v", full, err)
	}

	// Killed mid-turn: the command that was running has no item_completed, so no block.
	page, err = OpenAs("testdata/codex-killed-mid-turn.jsonl", Codex, nil).Latest()
	if err != nil {
		t.Fatal(err)
	}
	if got := kindsOf(page.Blocks); got != "user user assistant" {
		t.Errorf("killed: %s", got)
	}
}

func TestCodexItems(t *testing.T) {
	line := func(item string) []byte {
		return []byte(`{"timestamp":"2026-01-01T00:00:00Z","type":"event_msg","payload":{"type":"item_completed","item":` + item + `}}`)
	}
	cases := map[string]string{
		`{"type":"Reasoning","id":"r","summary_text":["thinking hard"]}`: "thinking",
		`{"type":"Reasoning","id":"r","summary_text":[]}`:                "",
		`{"type":"McpToolCall","id":"m","server":"ida","tool":"list","arguments":{},"status":"completed","result":{"content":[{"type":"text","text":"x"}],"isError":false}}`: "tool result",
		`{"type":"ImageView","id":"i","path":"file:///tmp/a.png"}`:                       "tool result",
		`{"type":"Extension","kind":"clock.sleep","id":"c"}`:                             "",
		`{"type":"Extension","kind":"web.search","id":"w","query":"go"}`:                 "tool result",
		`{"type":"SubAgentActivity","id":"s","kind":"started","agent_path":"/root/x"}`:   "tool result",
		`{"type":"SubAgentActivity","id":"s","kind":"completed","agent_path":"/root/x"}`: "",
	}
	for item, want := range cases {
		if got := kindsOf(Codex(0, line(item), Clip, nil)); got != want {
			t.Errorf("%s: %q, want %q", item, got, want)
		}
	}
	b := Codex(0, line(`{"type":"McpToolCall","id":"m","server":"ida","tool":"list","arguments":{"a":1},"status":"completed","result":{"content":[],"isError":true}}`), Clip, nil)
	if b[0].Name != "mcp__ida__list" || !b[1].Error {
		t.Errorf("mcp: %+v", b)
	}
	if b := Codex(0, []byte(`{"type":"event_msg","payload":{"type":"turn_aborted"}}`), Clip, nil); kindsOf(b) != "system:interrupted" {
		t.Errorf("aborted: %+v", b)
	}
	if b := Codex(0, []byte(`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"secret"}]}}`), Clip, nil); len(b) != 0 {
		t.Errorf("response_item: %+v", b)
	}
}

// testdata/pi-session.jsonl is Rhodium's recorded pi session (pi 1.3.0): a tool call and its
// result, the reply, then a compaction.
func TestPiTranscript(t *testing.T) {
	f := OpenAs("testdata/pi-session.jsonl", Pi, nil)
	page, err := f.Latest()
	if err != nil {
		t.Fatal(err)
	}
	if got := kindsOf(page.Blocks); got != "user tool assistant system:compact" {
		t.Fatalf("blocks: %s", got)
	}
	tool := page.Blocks[1]
	if tool.Name != "bash" || !strings.Contains(tool.Input, `"command": "echo hello from bash"`) || tool.Result == nil || tool.Result.Text != "hello from bash\n" {
		t.Errorf("tool: %+v %+v", tool, tool.Result)
	}
	if page.Blocks[2].Text != "done" || page.Blocks[3].Text == "" {
		t.Errorf("blocks: %+v", page.Blocks)
	}
	// A message Iridium wrote has its source prefix; pi keeps no id of it.
	b := Pi(0, []byte(`{"type":"message","timestamp":"2026-01-01T00:00:00Z","message":{"role":"user","content":[{"type":"text","text":"[网页] hi"}]}}`), Clip, func(string) (string, string, bool) { return "", "", false })
	if len(b) != 1 || b[0].From != "网页" || b[0].Text != "hi" {
		t.Errorf("source: %+v", b)
	}
	// An extension's [Image #n]: the paths again, as written.
	b = Pi(0, []byte(`{"type":"message","message":{"role":"user","content":[{"type":"text","text":"songs\n\n[附件] `+"`[Image #1]`"+`\n[附件] `+"`[Image #2]`"+`\n\n[Image #1]: /u/a.png\n[Image #2]: /u/b.png"},{"type":"image","data":"x"}]}}`), Clip, nil)
	if len(b) != 1 || b[0].Text != "songs\n\n[附件] `/u/a.png`\n[附件] `/u/b.png`" {
		t.Errorf("images: %+v", b)
	}
	b = Pi(0, []byte(`{"type":"message","message":{"role":"bashExecution","command":"ls","output":"a\n","exitCode":0}}`), Clip, nil)
	if kindsOf(b) != "system:local_command" || b[0].Text != "$ ls\na\n" {
		t.Errorf("bash: %+v", b)
	}
}

func TestFindAgents(t *testing.T) {
	home := t.TempDir()
	day := filepath.Join(home, "sessions", "2026", "11", "05")
	os.MkdirAll(day, 0o700)
	id := "01a1aa7a-68da-78d3-98f5-8df0e5f80a39"
	want := filepath.Join(day, "rollout-2026-11-05T10-00-00-"+id+".jsonl")
	os.WriteFile(want, nil, 0o600)
	if got, err := FindCodex(home, id); err != nil || got != want {
		t.Errorf("codex: %q %v", got, err)
	}
	if got, _ := FindCodex(home, "01a1aa7a-0000-0000-0000-000000000000"); got != "" {
		t.Errorf("codex found %q", got)
	}
	if CodexHome([]string{"CODEX_HOME=/c"}) != "/c" {
		t.Error("CODEX_HOME")
	}

	root := t.TempDir()
	dir := PiSessions([]string{"PI_CODING_AGENT_DIR=" + root}, "/home/user/notes")
	if dir != filepath.Join(root, "sessions", "--home-user-notes--") {
		t.Errorf("pi dir %s", dir)
	}
	if PiSessions([]string{"PI_CODING_AGENT_SESSION_DIR=/s", "PI_CODING_AGENT_DIR=" + root}, "/x") != "/s" {
		t.Error("PI_CODING_AGENT_SESSION_DIR")
	}
	os.MkdirAll(dir, 0o700)
	sid := "00000000-0000-4000-8000-4c4a2a950dc8"
	file := filepath.Join(dir, "2026-01-01T00-00-00-386Z_"+sid+".jsonl")
	os.WriteFile(file, nil, 0o600)
	if got, err := FindPi(dir, sid); err != nil || got != file {
		t.Errorf("pi: %q %v", got, err)
	}
}
