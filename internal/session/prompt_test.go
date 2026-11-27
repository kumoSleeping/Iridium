package session

import (
	"strings"
	"testing"
)

func TestSystemPrompt(t *testing.T) {
	t.Parallel()
	p := SystemPrompt("zh")
	for _, want := range []string{"ir api [METHOD]", "ir api help secrets", "ir api POST internal/links path=<path>", "](ir:id)", "](ir:session/<id>)", "`ir api channel.update channel_id=<id> data.name=\"<title>\"` (this session: `s1`", "Discover routes progressively", "`ir api help <METHOD path | satori.method>`", "`POST internal/channels/<its id>/members`", "- User's language: Chinese\n"} {
		if !strings.Contains(p, want) {
			t.Errorf("no %q in:\n%s", want, p)
		}
	}
	// The admin commands and the routes past common are left to ir --help and ir api help.
	if strings.Contains(p, "ir public") || strings.Contains(p, "# common") || len(p) > 3000 {
		t.Errorf("admin commands in the prompt:\n%s", p)
	}
	if p := SystemPrompt(""); strings.Contains(p, "User's language") {
		t.Error("a language line with none known")
	}
	if p := SystemPrompt("en"); !strings.Contains(p, "- User's language: English\n") {
		t.Error("English")
	}
}
