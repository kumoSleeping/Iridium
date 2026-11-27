package assemble

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kumoSleeping/iridium/internal/ctx/strategy"
	"github.com/kumoSleeping/iridium/internal/prompts"
)

// update writes the golden files anew: go test ./internal/ctx/assemble -update.
var update = flag.Bool("update", false, "write testdata/*.golden anew")

// golden compares got with testdata/name, to the byte: what agents are given, for reviewers to
// read as it is.
func golden(t *testing.T, name string, in Injection, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	file := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.WriteFile(file, []byte(in.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if got := in.String(); got != string(want) {
		t.Errorf("%s:\n got %q\nwant %q", name, got, want)
	}
	// Every part says where it came from, and its text is in what the agent gets.
	var b strings.Builder
	for _, p := range in.Parts() {
		if p.Source == "" || p.Hash == "" {
			t.Errorf("%s: a part without a source or a hash: %+v", name, p)
		}
		if id, ok := strings.CutPrefix(p.Source, "prompts:"); ok {
			f, found := prompts.Get(id)
			if !found || f.Hash != p.Hash {
				t.Errorf("%s: part %s: not the fragment's hash", name, p.Source)
			}
			s, err := f.Render(p.Values)
			if err != nil {
				t.Errorf("%s: part %s: %v", name, p.Source, err)
			}
			b.WriteString(s)
		} else {
			b.WriteString(p.text)
		}
	}
	if b.String() != in.String() {
		t.Errorf("%s: its parts' sources and values do not make its text", name)
	}
}

func TestSystemGolden(t *testing.T) {
	t.Parallel()
	for lang, name := range map[string]string{"zh": "zh", "en": "en", "": "none"} {
		in, err := System(Where{Session: "s-1", Lang: lang})
		golden(t, "system."+name, in, err)
		if in.Kind() != KindSystem {
			t.Errorf("kind %q", in.Kind())
		}
		in, err = System(Where{Session: "s-1", Remote: true, IR: true, Lang: lang})
		golden(t, "remote.ir."+name, in, err)
		in, err = System(Where{Session: "s-1", Remote: true, Lang: lang})
		golden(t, "remote.noir."+name, in, err)
	}
	// A language without a name: no line, as for none.
	in, err := System(Where{Session: "s-1", Lang: "fr"})
	golden(t, "system.none", in, err)
}

// Claude (auto mode) is told how to ask for manual approval where it has ir; no other agent is.
func TestSystemManual(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		w    Where
		told bool
	}{{Where{Session: "s-1", Auto: true}, true}, {Where{Session: "s-1", Auto: true, Remote: true, IR: true}, true}, {Where{Session: "s-1", Auto: true, Remote: true}, false}, {Where{}, false}} {
		in, err := System(c.w)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Contains(in.String(), "internal/sessions/s-1/manual"); got != c.told {
			t.Errorf("%+v: told %v", c.w, got)
		}
	}
}

func TestMessageGolden(t *testing.T) {
	t.Parallel()
	in, err := Message("kumo's phone", "t1", "hello\nthere", "in-1")
	golden(t, "message.tag", in, err)
	in, err = Message("", "t1", "hello", "in-2")
	golden(t, "message.terminal", in, err)
	in, err = Message("web", "t1", "/compact now", "in-3")
	golden(t, "message.slash", in, err)
	if len(in.Parts()) != 1 || in.Parts()[0].Source != SourceInput {
		t.Errorf("a slash command with words of Iridium's: %+v", in.Parts())
	}
}

func TestOtherTexts(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		in   func() (Injection, error)
		want string
		kind Kind
	}{
		{"attachment", func() (Injection, error) { return Attachment("/w/a b.png") }, "[附件] `/w/a b.png`", KindMessage},
		{"denied", func() (Injection, error) { return Denied("", "r1") }, "The user said no.", KindToolResult},
		{"denied with words", func() (Injection, error) { return Denied("not {{that}}", "r1") }, "not {{that}}", KindToolResult},
	} {
		in, err := c.in()
		if err != nil || in.String() != c.want || in.Kind() != c.kind {
			t.Errorf("%s: %q (%s), %v", c.name, in.String(), in.Kind(), err)
		}
	}
	// Words are never read for slots.
	in, err := Message("{{tag}}", "t", "{{tag}} {{path}}", "")
	if err != nil || in.String() != "[{{tag}}] {{tag}} {{path}}" {
		t.Errorf("%q, %v", in.String(), err)
	}
}

func TestRecord(t *testing.T) {
	t.Parallel()
	in, err := System(Where{Session: "s-1", Lang: "zh"})
	if err != nil {
		t.Fatal(err)
	}
	var sources []string
	for _, p := range in.Parts() {
		sources = append(sources, p.Source)
	}
	want := "prompts:iridium.open prompts:host.intro prompts:cli prompts:cli.host prompts:cli.discover prompts:host.secrets-files prompts:shell.ir prompts:links prompts:links.files prompts:names prompts:title prompts:self.section prompts:self.helpers prompts:runtime prompts:lang prompts:self.session prompts:iridium.close"
	if got := strings.Join(sources, " "); got != want {
		t.Errorf("parts %s", got)
	}
	b, err := json.Marshal(in.Record())
	if err != nil {
		t.Fatal(err)
	}
	zh, _ := prompts.Get("lang.zh")
	for _, s := range []string{`"kind":"system"`, `"source":"prompts:cli.host"`, `"values":{"language":"Chinese"}`, `"refs":[{"kind":"prompts","id":"lang.zh","hash":"` + zh.Hash + `"}]`} {
		if !strings.Contains(string(b), s) {
			t.Errorf("no %s in the record:\n%s", s, b)
		}
	}
	// Its text whole (kumo, 2026-11-16: the record shows what the agent got).
	if !strings.Contains(string(b), "Hosted by Iridium") {
		t.Error("no text in the record")
	}
	msg, _ := Message("web", "t1", "hi", "in-1")
	b, _ = json.Marshal(msg.Record())
	if want := `{"kind":"message","parts":[{"source":"prompts:message.tag","hash":"` + msg.Parts()[0].Hash + `","values":{"tag":"web"}},{"source":"input","hash":"` + prompts.Hash("hi") + `","refs":[{"kind":"input","id":"in-1"}]}],"text":"[web] hi"}`; string(b) != want {
		t.Errorf("record %s\n  want %s", b, want)
	}
	// Parts are a copy.
	ps := msg.Parts()
	ps[0].Values["tag"] = "x"
	if msg.Parts()[0].Values["tag"] != "web" {
		t.Error("Parts hands out the injection's own maps")
	}
	if (Injection{}).String() != "" {
		t.Error("the zero injection")
	}
}

func TestBuildErrors(t *testing.T) {
	t.Parallel()
	if _, err := build(KindSystem, text("no.such")); err == nil {
		t.Error("an unknown fragment")
	}
	if _, err := build(KindSystem, text(idLang)); err == nil {
		t.Error("a slot without a value")
	}
	if _, err := build(KindSystem, text(idTitle, "x", "y")); err == nil {
		t.Error("a value without a slot")
	}
	if _, err := build(KindSystem, help("no")); err == nil {
		t.Error("an unknown part of ir's help")
	}
	if _, err := build(KindSystem, text(idLang).filled("language", "no.such")); err == nil {
		t.Error("a slot filled from an unknown fragment")
	}
}

// The policy is a function of where the session runs alone.
func TestPolicy(t *testing.T) {
	t.Parallel()
	ids := func(w Where) string {
		var s []string
		for _, p := range systemPieces(w) {
			s = append(s, p.id+p.help)
		}
		return strings.Join(s, " ")
	}
	for w, want := range map[Where]string{
		{}:                                   "iridium.open host.intro cli cli.host cli.discover host.secrets-files shell.ir links links.files names title self.section self.helpers iridium.close",
		{IR: true, Lang: "en"}:               "iridium.open host.intro cli cli.host cli.discover host.secrets-files shell.ir links links.files names title self.section self.helpers runtime lang iridium.close",
		{Remote: true, IR: true}:             "iridium.open remote.intro cli cli.remote cli.discover shell.ir links names title self.section self.helpers iridium.close",
		{Remote: true, Lang: "zh"}:           "iridium.open remote.intro-bare links names title runtime lang iridium.close",
		{Remote: true, IR: true, Lang: "xx"}: "iridium.open remote.intro cli cli.remote cli.discover shell.ir links names title self.section self.helpers iridium.close",
	} {
		if got := ids(w); got != want {
			t.Errorf("%+v: %s", w, got)
		}
	}
}

// A typical agent's first time, for reviewers to read whole: Claude with ir at the host, a
// default nick, called in a channel of two agents (testdata/typical.*.golden).
func TestTypicalGolden(t *testing.T) {
	t.Parallel()
	self := &Self{Nick: "Claude-1234", IR: true, Memory: []SelfMemory{{At: 1764000000000, From: "#12", Text: "kumo wants short UI copy"}},
		Channels: []SelfChannel{{ID: "ch-uuid", Name: "ir 问题", At: 1764000000000, With: []string{"Codex-5678"}, Last: "build fixed"}}}
	in, err := System(Where{Session: "s-1", IR: true, Lang: "zh", Self: self, Auto: true, Folder: "/home/k/ir", OS: "linux"})
	golden(t, "typical.system", in, err)
	var p strategy.Plan
	p.Full = []strategy.Full{{Msg: strategy.Msg{Seq: 2, Who: "kumo", Text: "CI is red since this morning", At: 3600_000}}}
	in, err = ChannelTurn(Channel{Session: "s-1", Rename: true, ID: "ch-uuid", Name: "ir 问题", You: "Claude-1234", Pull: true,
		Agents: []ChannelAgent{{Nick: "Claude-1234", Agent: "claude", Model: "opus"}, {Nick: "Codex-5678", Agent: "codex", Model: "gpt-5", Effort: "low", Recv: "every message"}},
		Plan:   p}, 3, "@Claude-1234 why does the build fail?", "in-3")
	golden(t, "typical.channel", in, err)
}
