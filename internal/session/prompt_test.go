package session

import (
	"strings"
	"testing"
)

func TestSystemPrompt(t *testing.T) {
	p := SystemPrompt("zh")
	for _, want := range []string{"ir send <id>", "ir secret set", "[文件] `/absolute/path`", "[标题] <title>", "ir --help.", "web settings: Chinese."} {
		if !strings.Contains(p, want) {
			t.Errorf("no %q in:\n%s", want, p)
		}
	}
	// The admin commands are left to ir --help.
	if strings.Contains(p, "ir invite") || strings.Contains(p, "ir public") {
		t.Errorf("admin commands in the prompt:\n%s", p)
	}
	if p := SystemPrompt(""); strings.Contains(p, "user's language, as set") {
		t.Error("a language line with none known")
	}
	if p := SystemPrompt("en"); !strings.Contains(p, "web settings: English.") {
		t.Error("English")
	}
}
