package transcript

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestNameLine(t *testing.T) {
	for _, c := range []struct{ text, want string }{
		{"done.\n\n[标题] 侧边栏单行标题", "侧边栏单行标题"},
		{"done.\n[标题]  「推送去重」。\n", "推送去重"},
		{"[标题] **Fix login redirect**", "Fix login redirect"},
		{"[标题] 第一个\nmore\n[标题] 第二个", "第二个"},
		{"[标题] 好的\n[标题] ", "好的"},
		{"see:\n```\n[标题] an example\n```\n", ""},
		{"no title here", ""},
		{"inline [标题] not a line", ""},
		{"[标题] " + strings.Repeat("长", 30), strings.Repeat("长", 23) + "…"},
	} {
		if got := NameLine(c.text); got != c.want {
			t.Errorf("%q: %q, want %q", c.text, got, c.want)
		}
	}
}

func TestWithoutNameLines(t *testing.T) {
	in := "done.\n\n```\n[标题] kept\n```\n[标题] 新标题\n"
	if got, want := WithoutNameLines(in), "done.\n\n```\n[标题] kept\n```"; got != want {
		t.Errorf("%q, want %q", got, want)
	}
	if got := WithoutNameLines("plain"); got != "plain" {
		t.Error(got)
	}
}

func TestTitleFromNameLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	ls := lines{user("u1", "能不能把侧边栏改成一行")}
	ls.write(t, path, false)
	f := Open(path, nil)
	if title, _ := f.Title(); title != "能不能把侧边栏改成一行" {
		t.Errorf("before a title line: %q", title)
	}
	ls = append(ls, assistant(text("可以。\n\n[标题] 侧边栏单行显示")))
	ls.write(t, path, false)
	if title, _ := f.Title(); title != "侧边栏单行显示" {
		t.Errorf("after: %q", title)
	}
	if last, _ := f.LastSaid(); last == nil || last.Text != "可以。" {
		t.Errorf("last said: %+v", last)
	}
	// A later reply without one keeps it; the next one replaces it.
	ls = append(ls, user("u2", "好"), assistant(text("ok")), user("u3", "换个话题"), assistant(text("好。\n[标题] 推送去重")))
	ls.write(t, path, false)
	if title, _ := f.Title(); title != "推送去重" {
		t.Errorf("replaced: %q", title)
	}
	// What you say never names it.
	ls = append(ls, user("u4", "[标题] mine"))
	ls.write(t, path, false)
	if title, _ := f.Title(); title != "推送去重" {
		t.Errorf("from you: %q", title)
	}
}
