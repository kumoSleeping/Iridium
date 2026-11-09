package transcript

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"

	"github.com/kumoSleeping/iridium/internal/api"
)

// testdata/dsh is a DSH_HOME of sessions dsh 0.2.2-rc.1 wrote against a fake model: zstd, a
// frame per batch of events.
const dshHome = "testdata/dsh"

func dshFile(t *testing.T, id string, sources Sources) *File {
	t.Helper()
	p, err := FindDsh(dshHome, id)
	if err != nil || p == "" {
		t.Fatalf("find %s: %q %v", id, p, err)
	}
	return OpenAs(p, Dsh, sources)
}

func TestDshTranscript(t *testing.T) {
	// The highest session.vN counts; sess-allow also has an older session.jsonl.
	if p, _ := FindDsh(dshHome, "sess-allow"); filepath.Base(p) != "session.v4.jsonl.zstd" {
		t.Errorf("find: %q", p)
	}
	if p, err := FindDsh(dshHome, "sess-none"); p != "" || err != nil {
		t.Errorf("find none: %q %v", p, err)
	}
	if _, err := FindDsh(dshHome, "../x"); err == nil {
		t.Error("find ../x: no error")
	}

	sources := func(id string) (string, string, bool) {
		if id == "eca90a6e-219e-4693-be4f-8b87b80fd076" {
			return "tttttttttttttttt", "网页", true
		}
		return "", "", false
	}
	f := dshFile(t, "sess-allow", sources)
	page, err := f.Latest()
	if err != nil {
		t.Fatal(err)
	}
	// The runtime and time snapshots dsh writes as user messages are not yours.
	if got := kindsOf(page.Blocks); got != "user assistant tool assistant" {
		t.Fatalf("blocks: %s", got)
	}
	u, tool := page.Blocks[0], page.Blocks[2]
	if u.Text != "RUNTOOL:bash please" || u.From != "网页" || u.FromTerminal != "tttttttttttttttt" || u.Time != 1793952145839 {
		t.Errorf("user: %+v", u)
	}
	if tool.Name != "bash" || tool.ID != "toolu_1" || !strings.Contains(tool.Input, `"command": "touch made-by-dsh && echo hi"`) ||
		tool.Result == nil || tool.Result.Text != "hi\n" || tool.Result.Error {
		t.Errorf("tool: %+v %+v", tool, tool.Result)
	}
	if title, _ := f.Title(); title != "RUNTOOL:bash please" {
		t.Errorf("title: %q", title)
	}
	if last, _ := f.LastSaid(); last == nil || last.Role != "assistant" || last.Text != "Done after the tool." {
		t.Errorf("last: %+v", last)
	}
	full, err := f.Full(tool.At)
	if err != nil || !strings.Contains(full, `"description": "Make a file"`) {
		t.Errorf("full: %q %v", full, err)
	}
	if full, err := f.Full(u.At); err != nil || full != "RUNTOOL:bash please" {
		t.Errorf("full user: %q %v", full, err)
	}

	// Two turns, then commands: /permission and /plan are the approval mode (the driver sets it at
	// every start; the page shows it as a setting), left out with their answers; a /compact as the
	// compaction (its summary) and its answer; the notices dsh splices in for the model and the
	// compaction's checkpoint are not shown.
	page, err = dshFile(t, "sess-steer", nil).Latest()
	if err != nil {
		t.Fatal(err)
	}
	want := "user assistant user assistant system:compact system:local_command user assistant"
	if got := kindsOf(page.Blocks); got != want {
		t.Fatalf("steer: %s", got)
	}
	if b := page.Blocks[4]; !strings.HasPrefix(b.Text, "Echo: steered in") || b.Compact != nil {
		t.Errorf("compact: %+v", b)
	}
	if b := page.Blocks[6]; b.Text != "/nosuch" {
		t.Errorf("unknown command, a prompt: %+v", b)
	}

	// Killed mid tool call: the call's error result, the turn aborted; then resumed.
	page, err = dshFile(t, "sess-kill", nil).Latest()
	if err != nil {
		t.Fatal(err)
	}
	if got := kindsOf(page.Blocks); got != "user assistant tool system:interrupted user assistant" {
		t.Fatalf("kill: %s", got)
	}
	if r := page.Blocks[2].Result; r == nil || !r.Error || r.Text != "Error: tool call aborted" {
		t.Errorf("aborted call: %+v", r)
	}

	// /plan with a message: the mode command left out, its message as yours; the plan is the
	// call's input.
	page, err = dshFile(t, "sess-plan3", nil).Latest()
	if err != nil {
		t.Fatal(err)
	}
	if got := kindsOf(page.Blocks); got != "user assistant tool assistant tool assistant" {
		t.Fatalf("plan: %s", got)
	}
	if b := page.Blocks[2]; b.Name != "exit_plan_mode" || !strings.Contains(b.Input, `"plan": "# Plan`) || b.Result == nil || !strings.HasPrefix(b.Result.Text, "Plan approved") {
		t.Errorf("plan call: %+v", b)
	}
}

func TestDshEvents(t *testing.T) {
	line := func(typ, data string) []byte {
		return []byte(`{"type":"` + typ + `","seq":1,"time":5,"data":` + data + `}`)
	}
	for _, c := range []struct {
		raw  []byte
		want string
	}{
		{line("user/message", `{"content":[{"type":"text","text":"x"}],"source":{"kind":"runtime-context"},"role":"user"}`), ""},
		{line("user/message", `{"content":[{"type":"text","text":"x"}],"source":{"kind":"compact-checkpoint"},"role":"user"}`), ""},
		{line("assistant/message", `{"message":{"role":"assistant","content":[{"type":"reasoning","text":"hmm"},{"type":"text","text":"ok"}]}}`), "thinking assistant"},
		{line("turn/end", `{"turn":1,"reason":{"kind":"interrupted"}}`), "system:interrupted"},
		{line("turn/end", `{"turn":1,"reason":{"kind":"error","error":{"message":"rate limited","code":"429"}}}`), "system:api_error"},
		{line("turn/end", `{"turn":1,"reason":{"kind":"completed"}}`), ""},
		{line("compaction/summary", `{"compactionId":"c","turn":3,"summary":[{"type":"text","text":"s"}]}`), "system:compact"},
		{line("request/header", `{"header":{}}`), ""},
		{[]byte(`not json`), "system:unreadable"},
	} {
		blocks := Dsh(7, c.raw, Clip, nil)
		if got := kindsOf(blocks); got != c.want {
			t.Errorf("%s: %q, want %q", c.raw, got, c.want)
		}
		for _, b := range blocks {
			if b.Event == api.SystemEventCompact && (b.Compact == nil || !b.Compact.Auto) {
				t.Errorf("compaction of the agent's own: %+v", b)
			}
			if b.Event == api.SystemEventAPIError && b.Text != "rate limited" {
				t.Errorf("error: %+v", b)
			}
		}
	}
}

// What Iridium sent: dsh gives a prompt an id of its own, so its tag tells who sent it; and the
// answers to the mode commands the driver runs at every start are not shown.
func TestDshSourceAndModes(t *testing.T) {
	none := func(string) (string, string, bool) { return "", "", false }
	b := Dsh(1, []byte(`{"type":"user/message","seq":1,"time":5,"data":{"content":[{"text":"[kumo] 你好","type":"text"}],"source":{"kind":"user"},"role":"user","id":"f4771296"}}`), Clip, none)
	if len(b) != 1 || b[0].From != "kumo" || b[0].Text != "你好" {
		t.Errorf("tagged: %+v", b)
	}
	for _, text := range []string{"preset danger-full-access", "Plan mode off.", "Plan mode is already inactive.", "Plan mode entry cancelled.", "Leaving plan mode (applies from the next step).", "Entering plan mode (applies from the next step). Use /plan off to leave."} {
		line := `{"type":"command/done","seq":1,"time":5,"data":{"commandId":"c","kind":"success","text":"` + text + `"}}`
		if b := Dsh(1, []byte(line), Clip, nil); len(b) != 0 {
			t.Errorf("%q: %+v", text, b)
		}
	}
	if b := Dsh(1, []byte(`{"type":"command/done","seq":1,"time":5,"data":{"commandId":"c","kind":"success","text":"Goal set."}}`), Clip, nil); kindsOf(b) != "system:local_command" {
		t.Errorf("another answer: %+v", b)
	}
}

// A dsh file is read as dsh writes it: frame by frame, the last one possibly half there.
func TestDshIncremental(t *testing.T) {
	src, _ := FindDsh(dshHome, "sess-allow")
	all, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	var ends []int
	for at := 0; at < len(all); {
		n, ok := frameLen(all[at:])
		if !ok {
			t.Fatalf("frame at %d", at)
		}
		at += n
		ends = append(ends, at)
	}
	if len(ends) < 4 {
		t.Fatalf("frames: %d", len(ends))
	}
	dir := filepath.Join(t.TempDir(), "sessions", "--w--", "s1")
	_ = os.MkdirAll(dir, 0o755)
	path := filepath.Join(dir, "session.v4.jsonl.zstd")
	// The header and the first batches whole, the next cut in half.
	cut := ends[2] + (ends[3]-ends[2])/2
	if err := os.WriteFile(path, all[:cut], 0o644); err != nil {
		t.Fatal(err)
	}
	f := OpenAs(path, Dsh, nil)
	size, err := f.Size()
	if err != nil {
		t.Fatal(err)
	}
	first, err := f.Latest()
	if err != nil {
		t.Fatal(err)
	}
	if first.End != size || len(first.Blocks) == 0 || len(first.Blocks) >= 4 {
		t.Fatalf("first: %d %s", size, kindsOf(first.Blocks))
	}
	// Read again: nothing new.
	if again, _ := f.Size(); again != size {
		t.Errorf("size: %d, then %d", size, again)
	}
	// The rest is written: what came after the page's end is the rest of the conversation.
	w, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(all[cut:]); err != nil {
		t.Fatal(err)
	}
	w.Close()
	next, err := f.After(first.End)
	if err != nil {
		t.Fatal(err)
	}
	if a, b := kindsOf(first.Blocks), kindsOf(next.Blocks); a != "user" || b != "assistant tool assistant" || next.Start != first.End {
		t.Errorf("first %s, then %s (%d, %d)", a, b, first.End, next.Start)
	}
	whole, _ := f.Latest()
	if got := kindsOf(whole.Blocks); got != "user assistant tool assistant" {
		t.Errorf("whole: %s", got)
	}
	if title, _ := f.Title(); title != "RUNTOOL:bash please" {
		t.Errorf("title: %q", title)
	}
	for _, b := range whole.Blocks {
		if _, err := f.Full(b.At); err != nil {
			t.Errorf("full %s: %v", b.At, err)
		}
	}
}

func TestGlanceDsh(t *testing.T) {
	p, _ := FindDsh(dshHome, "sess-steer")
	g, err := GlanceDsh(p, nil)
	if err != nil {
		t.Fatal(err)
	}
	if g.Cwd != "/tmp/dsh-probe/w3" || g.Title != "SLOW first message" || g.Sidechain || g.Last == nil || g.Last.Role != "assistant" {
		t.Errorf("glance: %+v %+v", g, g.Last)
	}
	if DshHome([]string{"DSH_HOME=/x"}) != "/x" || !strings.HasSuffix(DshHome(nil), "/.dsh") {
		t.Errorf("home: %q", DshHome(nil))
	}
}

// A dsh subagent has ended once its turn has: the list stops showing it at work then.
func TestGlanceDshSubagentEnded(t *testing.T) {
	enc, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close()
	path := filepath.Join(t.TempDir(), "session.v4.jsonl.zstd")
	var data []byte
	write := func(lines ...string) Glimpse {
		t.Helper()
		for _, l := range lines {
			data = enc.EncodeAll([]byte(l+"\n"), data)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		g, err := GlanceDsh(path, nil)
		if err != nil {
			t.Fatal(err)
		}
		return g
	}
	if g := write(`{"type":"session","version":4,"cwd":"/w","origin":"subagent","parentSession":"p1"}`); !g.Sidechain || g.Parent != "p1" || g.Ended {
		t.Errorf("not begun: %+v", g)
	}
	if g := write(`{"type":"turn/start","seq":1,"time":1,"data":{"turn":1}}`, `{"type":"step/start","seq":2,"time":2,"data":{"turn":1,"step":1}}`); g.Ended {
		t.Error("at work: ended")
	}
	if g := write(`{"type":"turn/end","seq":3,"time":3,"data":{"turn":1,"reason":{"kind":"completed"}}}`); !g.Ended {
		t.Error("its turn over: not ended")
	}
	if g := write(`{"type":"turn/start","seq":4,"time":4,"data":{"turn":2}}`); g.Ended {
		t.Error("sent another message: ended")
	}
}

// dsh's fallback title is the first message as written: Iridium's tag comes off; a title the
// model or you gave stays as it is.
func TestDshTitleTag(t *testing.T) {
	for _, c := range []struct{ line, want string }{
		{`{"type":"session/title","seq":1,"time":1,"data":{"title":"[本机] hello there","source":{"kind":"fallback"}}}`, "hello there"},
		{`{"type":"session/title","seq":1,"time":1,"data":{"title":"[ir:3f898af4] run it","source":{"kind":"fallback"}}}`, "run it"},
		{`{"type":"session/title","seq":1,"time":1,"data":{"title":"[WIP] a title","source":{"kind":"user"}}}`, "[WIP] a title"},
	} {
		if got, ok := dshTitle([]byte(c.line)); !ok || got != c.want {
			t.Errorf("%s: %q %v", c.line, got, ok)
		}
	}
	if _, ok := dshTitle([]byte(`{"type":"session/title","seq":1,"time":1,"data":{"title":"[本机] ","source":{"kind":"fallback"}}}`)); ok {
		t.Error("a title of a tag alone")
	}
}

// dsh 0.2.2's projectKey, by its source (session-persistence-jsonl), and as seen on a Mac.
func TestDshProjectDir(t *testing.T) {
	for cwd, want := range map[string]string{
		"/tmp/irexec.60QK/cwd":         "--tmp-irexec.60QK-cwd--",
		`C:\Users\kumo\a--b`:           "--C-Users-kumo-a--b--",
		"/home/x/我的 项目":                "--home-x-~6211~7684~0020~9879~76EE--",
		"/":                            "--root--",
		"":                             "_no-cwd",
		"/a/~b//c":                     "--a-~007Eb-c--",
		"/" + strings.Repeat("a", 300): "--" + strings.Repeat("a", 251) + "--",
	} {
		if got := DshProjectDir(cwd); got != want {
			t.Errorf("DshProjectDir(%q) = %q, want %q", cwd, got, want)
		}
	}
}
