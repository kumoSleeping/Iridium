package satori

import "testing"

// A merged forward reads as lines to an agent: who said each, its words, one inside set in.
func TestForwardText(t *testing.T) {
	t.Parallel()
	fwd := `<message forward><message id="1"><author id="u1" name="Ann"/>hello<br/>again</message>` +
		`<message><author id="u2"/><message forward><message><author name="Bob"/>inner</message></message></message></message>`
	want := "[聊天记录]\nAnn: hello\n  again\nu2: [聊天记录]\n  Bob: inner"
	if got := Plain(Parse(fwd), nil); got != want {
		t.Errorf("Plain:\n%s\nwant\n%s", got, want)
	}
	// Written in words (a channel's log keeps it so), outside code.
	if got := ForwardWords("look\n" + fwd + " ok"); got != "look\n"+want+"\n ok" {
		t.Errorf("ForwardWords: %q", got)
	}
	for _, s := range []string{"`" + fwd + "`", "```\n" + fwd + "\n```", "<message forward><message>open", "no forward here"} {
		if got := ForwardWords(s); got != s {
			t.Errorf("ForwardWords(%q) = %q", s, got)
		}
	}
}
