package transcript

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kumoSleeping/iridium/internal/api"
)

// testdata/question.jsonl is Rhodium's recorded Claude transcript (Claude 2.1.304): a question
// asked with AskUserQuestion and answered, then the reply.
func TestRecordedTranscript(t *testing.T) {
	f := Open("testdata/question.jsonl", nil)
	page, err := f.Latest()
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, b := range page.Blocks {
		kinds = append(kinds, string(b.Kind))
	}
	if got := strings.Join(kinds, " "); got != "user tool assistant" {
		t.Fatalf("blocks: %s", got)
	}
	u, tool, a := page.Blocks[0], page.Blocks[1], page.Blocks[2]
	if !strings.HasPrefix(u.Text, "Use the AskUserQuestion tool") || u.ID != "00000000-0000-4000-8000-f7c0174ef2fd" || u.Time == 0 {
		t.Errorf("user: %+v", u)
	}
	if tool.Name != "AskUserQuestion" || tool.Result == nil || !strings.Contains(tool.Result.Text, `"Blue"`) || !strings.Contains(tool.Input, "\n  \"questions\"") {
		t.Errorf("tool: %+v", tool)
	}
	if a.Text != "Blue" || page.More || page.Start == 0 {
		t.Errorf("assistant %+v, more %v, start %d", a, page.More, page.Start)
	}
	st, _ := os.Stat("testdata/question.jsonl")
	if page.End != st.Size() {
		t.Errorf("end %d, size %d", page.End, st.Size())
	}
	last, err := f.LastSaid()
	if err != nil || last.Role != "assistant" || last.Text != "Blue" {
		t.Errorf("last: %+v %v", last, err)
	}
	title, _ := f.Title()
	if !strings.HasPrefix(title, "Use the AskUserQuestion tool to ask me one question") || len([]rune(title)) > TitleLen {
		t.Errorf("title: %q", title)
	}
}

type lines []map[string]any

func (ls lines) write(t *testing.T, path string, appendTo bool) {
	t.Helper()
	flag := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	if appendTo {
		flag = os.O_CREATE | os.O_WRONLY | os.O_APPEND
	}
	w, err := os.OpenFile(path, flag, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	for _, l := range ls {
		b, _ := json.Marshal(l)
		w.Write(append(b, '\n'))
	}
}

func user(uuid string, content any) map[string]any {
	return map[string]any{"type": "user", "uuid": uuid, "timestamp": "2026-01-01T00:00:00.000Z", "message": map[string]any{"role": "user", "content": content}}
}

func assistant(content ...map[string]any) map[string]any {
	return map[string]any{"type": "assistant", "message": map[string]any{"role": "assistant", "content": content}}
}

func text(s string) map[string]any { return map[string]any{"type": "text", "text": s} }

func TestRules(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	long := strings.Repeat("é", 2500)
	lines{
		{"type": "api-request-blob", "message": map[string]any{"role": "user", "content": "SECRET CLAUDE.md"}},
		user("u1", "[网页] hello"), // Iridium's: its prefix goes
		user("u2", "[网页] typed in a terminal"),
		user("u3", []any{text("<command-name>/model</command-name>"), text("real words"), text("<pasted_content id=\"1\">x</pasted_content id=\"1\">")}),
		user("u4", []any{text("<task-notification>done</task-notification>")}),
		{"type": "user", "isMeta": true, "message": map[string]any{"role": "user", "content": "skill text"}},
		{"type": "user", "isCompactSummary": true, "message": map[string]any{"role": "user", "content": "summary"}},
		user("u5", []any{text("[Request interrupted by user for tool use]")}),
		user("u6", "<command-name>/compact</command-name>\n            <command-message>compact</command-message>\n            <command-args>keep the tests</command-args>"),
		{"type": "system", "subtype": "local_command", "content": "<local-command-stdout></local-command-stdout>"},
		{"type": "attachment", "attachment": map[string]any{"type": "queued_command", "prompt": "said mid-turn", "source_uuid": "q1", "origin": map[string]any{"kind": "human"}}},
		{"type": "attachment", "attachment": map[string]any{"type": "queued_command", "prompt": "from another session", "source_uuid": "q2", "isMeta": true}},
		{"type": "attachment", "attachment": map[string]any{"type": "queued_command", "prompt": "a task", "source_uuid": "q3", "origin": map[string]any{"kind": "task"}}},
		{"type": "attachment", "attachment": map[string]any{"type": "date"}},
		assistant(map[string]any{"type": "thinking", "thinking": ""}, map[string]any{"type": "thinking", "thinking": long}, text("ok"), map[string]any{"type": "tool_use", "id": "t1", "name": "Bash", "input": map[string]any{"command": long}}),
		user("r1", []any{map[string]any{"type": "tool_result", "tool_use_id": "t1", "content": []any{text(long)}, "is_error": true}}),
		{"type": "system", "subtype": "compact_boundary", "compactMetadata": map[string]any{"trigger": "manual", "preTokens": 353095, "postTokens": 15600, "durationMs": 117301}},
		{"type": "system", "subtype": "api_error", "error": map[string]any{"message": "overloaded"}},
		{"type": "system", "subtype": "local_command", "content": "<local-command-stdout>hi</local-command-stdout>"},
		{"type": "file-history-snapshot"},
		// Two sends queued in a turn, let in at its end as one line: two messages again.
		func() map[string]any {
			l := user("m1", []any{text("[网页] build, then ask me\n"), text("[网页] do not restart")})
			l["mergedSendUuids"] = []any{"m2"}
			return l
		}(),
	}.write(t, path, false)
	w, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	w.WriteString("not json at all\n{\"type\":\"user\",\"message\":{\"role\":\"user\",\"content\":\"half")
	w.Close()

	ours := func(uuid string) (string, string, bool) {
		return "pppppppppppppppp", "网页", uuid == "u1" || uuid == "m1" || uuid == "m2"
	}
	page, err := Open(path, ours).Latest()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, b := range page.Blocks {
		s := string(b.Kind)
		switch b.Kind {
		case api.BlockKindUser:
			s += fmt.Sprintf("(%s|%s|%s)", b.Text, b.From, b.ID)
			if b.From != "" && b.FromTerminal != "pppppppppppppppp" {
				t.Errorf("from terminal %q", b.FromTerminal)
			}
		case api.BlockKindAssistant:
			s += "(" + b.Text + ")"
		case api.BlockKindSystem:
			s += "(" + string(b.Event) + ")"
			if c := b.Compact; b.Event == api.SystemEventCompact && (c == nil || c.Auto || c.Before != 353095 || c.After != 15600 || c.Ms != 117301) {
				t.Errorf("compaction %+v", c)
			}
			if b.Event == api.SystemEventLocalCommand && b.Text != "hi" {
				t.Errorf("local command %q", b.Text)
			}
		case api.BlockKindThinking:
			s += fmt.Sprintf("(%d/%d)", len([]rune(b.Text)), b.Length)
		case api.BlockKindTool:
			s += fmt.Sprintf("(%s %d result %v %d)", b.Name, b.Length, b.Result != nil && b.Result.Error, b.Result.Length)
		}
		got = append(got, s)
	}
	want := []string{
		"user(hello|网页|u1)",
		"user([网页] typed in a terminal||u2)",
		"user(real words\n\n<pasted_content id=\"1\">x</pasted_content id=\"1\">||u3)",
		"system(interrupted)",
		"user(said mid-turn||q1)",
		"thinking(2000/2500)",
		"assistant(ok)",
		fmt.Sprintf("tool(Bash %d result true 2500)", len([]rune(`{
  "command": "`+long+`"
}`))),
		"system(compact)",
		"system(api_error)",
		"system(local_command)",
		"user(build, then ask me|网页|m1)",
		"user(do not restart|网页|m2)",
		"system(unreadable)",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("blocks\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for _, b := range page.Blocks {
		if strings.Contains(b.Text, "SECRET") {
			t.Error("an api-request-blob line was read")
		}
	}

	// The whole text by position: the call's input, its result, the thought.
	f := Open(path, ours)
	tool := page.Blocks[7]
	if full, err := f.Full(tool.At); err != nil || !strings.Contains(full, long) {
		t.Errorf("full input: %v", err)
	}
	if full, err := f.Full(tool.Result.At); err != nil || full != long {
		t.Errorf("full result: %d %v", len(full), err)
	}
	if full, err := f.Full(page.Blocks[5].At); err != nil || full != long {
		t.Errorf("full thought: %v", err)
	}
	if _, err := f.Full("1.0"); err != ErrNoBlock {
		t.Errorf("full at nothing: %v", err)
	}
	// The half line is not read until its newline comes.
	size, _ := f.Size()
	st, _ := os.Stat(path)
	if size >= st.Size() {
		t.Errorf("read the unfinished line: size %d of %d", size, st.Size())
	}
}

func TestPages(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	var ls lines
	for i := range 50 {
		ls = append(ls, user(fmt.Sprintf("u%d", i), fmt.Sprintf("question %d", i)))
		ls = append(ls, map[string]any{"type": "attachment", "attachment": map[string]any{"type": "date"}})
		ls = append(ls, assistant(text(fmt.Sprintf("answer %d", i))))
	}
	ls.write(t, path, false)
	f := Open(path, nil)
	p, err := f.Latest()
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Blocks) != 20 || p.Blocks[0].Text != "question 40" || p.Blocks[19].Text != "answer 49" || !p.More {
		t.Fatalf("latest: %d blocks, %q..%q, more %v", len(p.Blocks), p.Blocks[0].Text, p.Blocks[19].Text, p.More)
	}
	// Back to the start, page by page, with nothing twice and nothing missed.
	seen := 20
	for p.More {
		end := p.Start
		if p, err = f.Before(p.Start); err != nil {
			t.Fatal(err)
		}
		if p.End != end {
			t.Fatalf("page before %d ends at %d", end, p.End)
		}
		seen += len(p.Blocks)
	}
	if seen != 100 || p.Blocks[0].Text != "question 0" {
		t.Fatalf("saw %d blocks, first %q", seen, p.Blocks[0].Text)
	}
	// What comes after the latest page, once more is written.
	latest, _ := f.Latest()
	if p, _ := f.After(latest.End); len(p.Blocks) != 0 || p.End != latest.End {
		t.Fatalf("after the end: %+v", p)
	}
	lines{user("n1", "new question"), assistant(text("new answer"))}.write(t, path, true)
	p, _ = f.After(latest.End)
	if len(p.Blocks) != 2 || p.Blocks[0].Text != "new question" {
		t.Fatalf("after: %+v", p.Blocks)
	}
	if size, _ := f.Size(); p.End != size {
		t.Errorf("after ends at %d, size %d", p.End, size)
	}
	// From the start forward, 20 messages a page.
	p, _ = f.After(0)
	if len(p.Blocks) != 20 || p.Blocks[0].Text != "question 0" || p.More {
		t.Fatalf("first page: %d blocks, %q", len(p.Blocks), p.Blocks[0].Text)
	}
	// A file replaced is read anew.
	lines{user("x", "fresh")}.write(t, path+".new", false)
	if err := os.Rename(path+".new", path); err != nil {
		t.Fatal(err)
	}
	if p, _ := f.Latest(); len(p.Blocks) != 1 || p.Blocks[0].Text != "fresh" {
		t.Fatalf("replaced: %+v", p.Blocks)
	}
}

// A call and its result on different pages: the result comes alone, with the call's id.
func TestResultOnAnotherPage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	ls := lines{assistant(map[string]any{"type": "tool_use", "id": "t1", "name": "Read", "input": map[string]any{}})}
	for i := range 20 {
		ls = append(ls, user(fmt.Sprintf("r%d", i), []any{map[string]any{"type": "tool_result", "tool_use_id": "t1", "content": "x"}, text(fmt.Sprintf("m%d", i))}))
	}
	ls = append(ls, assistant(text("last")))
	ls.write(t, path, false)
	p, _ := Open(path, nil).Latest()
	if p.Blocks[0].Kind != api.BlockKindResult || p.Blocks[0].ID != "t1" || !p.More {
		t.Fatalf("first block: %+v", p.Blocks[0])
	}
}

func TestFind(t *testing.T) {
	home := t.TempDir()
	id := "00000000-0000-4000-8000-645e563d4553"
	dir := filepath.Join(home, "projects", "-tmp-w")
	os.MkdirAll(dir, 0o700)
	if p, err := Find(home, id); p != "" || err != nil {
		t.Fatalf("found %q %v", p, err)
	}
	os.WriteFile(filepath.Join(dir, id+".jsonl"), nil, 0o600)
	if p, _ := Find(home, id); p != filepath.Join(dir, id+".jsonl") {
		t.Fatalf("found %q", p)
	}
	if _, err := Find(home, "*"); err == nil {
		t.Fatal("took a glob")
	}
}

// Claude's project directories: each UTF-16 unit but an ASCII letter or digit a '-'.
func TestClaudeProjectDir(t *testing.T) {
	for cwd, want := range map[string]string{
		"/home/iridium/kumo/Iridium": "-home-iridium-kumo-Iridium",
		`C:\Users\kumo\my.proj`:      "C--Users-kumo-my-proj",
		"/a/文😀":                      "-a----",
	} {
		if got, ok := ClaudeProjectDir(cwd); got != want || !ok {
			t.Errorf("%q: %q %v, want %q", cwd, got, ok, want)
		}
	}
	if _, ok := ClaudeProjectDir("/" + strings.Repeat("a", 200)); ok {
		t.Error("a path past 200 is Claude's to cut")
	}
}
