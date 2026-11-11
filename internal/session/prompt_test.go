package session

import (
	"strings"
	"testing"
)

func TestSystemPrompt(t *testing.T) {
	p := SystemPrompt("zh")
	for _, want := range []string{"ir api [METHOD]", "ir secret set", "ir attach <path>", "](ir:id)", `channel.update channel_id=self data.name="<title>"`, "ir --help.", "web settings: Chinese."} {
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
