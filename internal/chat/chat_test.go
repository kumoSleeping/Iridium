package chat

import (
	"testing"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/session"
)

func TestPlain(t *testing.T) {
	for md, want := range map[string]string{
		"# Title\n**bold** and `code`":    "Title\nbold and code",
		"see [the notes](/w/notes.txt)":   "see the notes",
		"![shot](/w/a.png)":               "shot",
		"```go\nfmt.Println()\n```\ndone": "fmt.Println()\ndone",
	} {
		if got := plain(md); got != want {
			t.Errorf("plain(%q) = %q, want %q", md, got, want)
		}
	}
}

func TestSource(t *testing.T) {
	c := Caller{ID: "x", Name: "QQ"}
	for sender, want := range map[string]string{"": "QQ", "张三": "QQ·张三", "[evil] name": "QQ·evil name", "a/b:c": "QQ·abc"} {
		if got := source(c, sender); got != session.From("x", want) {
			t.Errorf("source(%q) = %q, want %q", sender, got, want)
		}
	}
	if n, a, ok := command("/new@bot claude ~/x"); n != "new" || a != "claude ~/x" || !ok {
		t.Errorf("command: %q %q %v", n, a, ok)
	}
	if !ValidKey("qq:private:123") || ValidKey("") || ValidKey("a\nb") {
		t.Error("ValidKey")
	}
}

// /status names the model in use: by value, else by the full id an alias resolves to.
func TestModelName(t *testing.T) {
	ms := []api.Model{{Value: "default", Name: "Default (recommended)", Resolved: "claude-opus-5-5"}, {Value: "opus", Name: "Opus 5.5", Resolved: "claude-opus-5-5"}, {Value: "haiku", Name: "Haiku 5.5", Resolved: "claude-haiku-5-5"}}
	for model, want := range map[string]string{"claude-opus-5-5": "Opus 5.5", "haiku": "Haiku 5.5", "claude-haiku-5-5": "Haiku 5.5", "other-1": "other-1", "": ""} {
		if got := modelName(&api.SessionState{Model: model, Models: ms}); got != want {
			t.Errorf("modelName(%q) = %q, want %q", model, got, want)
		}
	}
}
