package session

import (
	"strings"
	"testing"
)

func TestSystemPrompt(t *testing.T) {
	t.Parallel()
	p := SystemPrompt("zh")
	for _, want := range []string{"ir api [METHOD]", "ir api help secrets", "ir api POST internal/links path=<path>", "](ir:id)", `channel.update channel_id=self data.name="<title>"`, "Run ir api help before", "web settings: Chinese."} {
		if !strings.Contains(p, want) {
			t.Errorf("no %q in:\n%s", want, p)
		}
	}
	// The admin commands and the routes past common are left to ir --help and ir api help.
	if strings.Contains(p, "ir public") || strings.Contains(p, "# common") || len(p) > 2000 {
		t.Errorf("admin commands in the prompt:\n%s", p)
	}
	if p := SystemPrompt(""); strings.Contains(p, "user's language, as set") {
		t.Error("a language line with none known")
	}
	if p := SystemPrompt("en"); !strings.Contains(p, "web settings: English.") {
		t.Error("English")
	}
}
