package chat

import "testing"

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
		if got := source(c, sender); got != want {
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
