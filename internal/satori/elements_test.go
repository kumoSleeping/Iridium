package satori

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	for _, c := range []struct {
		in   string
		want []Element
	}{
		{"hello", []Element{Text("hello")}},
		{"a &lt;b&gt; &amp; &quot;c&quot; &#39;&#x41;", []Element{Text(`a <b> & "c" 'A`)}},
		{"&bogus; &#xZZ; & alone", []Element{Text("&bogus; &#xZZ; & alone")}},
		{`hi <sharp id="s1" name="x &amp; y"/>!`, []Element{Text("hi "), Tag("sharp", []string{"id", "s1", "name", "x & y"}), Text("!")}},
		{`<a href='https://e.x/?a=1&amp;b=2'>link</a>`, []Element{Tag("a", []string{"href", "https://e.x/?a=1&b=2"}, Text("link"))}},
		{`<img src=internal:x/y/z/>`, []Element{Tag("img", []string{"src", "internal:x/y/z"})}},
		{`<button type="action" id="ask:s:r#allow" disabled>允许</button>`, []Element{
			{Type: "button", Attrs: []Attr{{Key: "type", Value: "action"}, {Key: "id", Value: "ask:s:r#allow"}, {Key: "disabled", Bare: true}}, Children: []Element{Text("允许")}}}},
		// Unpaired tags are text; so is what is not a tag.
		{"<b>bold", []Element{Text("<b>bold")}},
		{"x</b>y", []Element{Text("x</b>y")}},
		{"1 < 2 > 0", []Element{Text("1 < 2 > 0")}},
		{"<p><b>x</p>", []Element{Tag("p", nil, Text("<b>x"))}},
		// Comments go; whitespace with a line break around text is layout.
		{"a<!-- no -->b", []Element{Text("ab")}},
		{"<p>\n  one\n</p>\n<p>two</p>", []Element{Tag("p", nil, Text("one")), Tag("p", nil, Text("two"))}},
		{"  spaced  ", []Element{Text("  spaced  ")}},
		{"line1\n\nline2", []Element{Text("line1\n\nline2")}},
		{"<ir:ref id='x'/>", []Element{Tag("ir:ref", []string{"id", "x"})}},
	} {
		if got := Parse(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("Parse(%q):\n got %#v\nwant %#v", c.in, got, c.want)
		}
	}
}

func TestRoundTrip(t *testing.T) {
	for _, els := range [][]Element{
		{Text(`a & b < c > d "e"`)},
		{Tag("p", nil, Tag("b", nil, Text("提问"))), Tag("p", nil, Text("which?")),
			button("ask:s1:r1#0.0", "yes", true), inputButton("ask:s1:r1#w0", "自己填写")},
		{Tag("img", []string{"src", "internal:app/login%2Fx/files/s/ref", "title", `a "b".png`})},
		{Text("x"), Tag("sharp", []string{"id", "s"}), Text(" y")},
		{{Type: "file", Attrs: []Attr{{Key: "src", Value: "u"}, {Key: "cache", Bare: true}}}},
	} {
		s := String(els)
		if got := Parse(s); !reflect.DeepEqual(got, els) {
			t.Errorf("round trip of %q:\n got %#v\nwant %#v", s, got, els)
		}
	}
}

func TestPlain(t *testing.T) {
	refs := 0
	got := Plain(Parse(`<p>look</p><p>at <a href="https://x.y">this</a> and <sharp id="s2" name="build"/>, <at id="u" name="kumo"/></p>`+
		`<img src="internal:a/l/files/s/r1"/><file src="https://f" title="f.txt"/><quote>q1<br/>q2</quote><button>ok</button>`),
		func(e Element) string {
			if e.Attr("src") == "internal:a/l/files/s/r1" {
				refs++
				return ""
			}
			return e.Attr("title")
		})
	want := "look\nat this (https://x.y) and [build](ir:session/s2), @kumo\nf.txt> q1\n> q2\nok"
	if got != want || refs != 1 {
		t.Errorf("Plain: %q (%d refs), want %q", got, refs, want)
	}
	if got := Plain(Parse("a<br/>b"), nil); got != "a\nb" {
		t.Errorf("br: %q", got)
	}
}

func FuzzParse(f *testing.F) {
	for _, s := range []string{"", "a", "<a href='x'>y</a>", "<b><i>x</b></i>", "&amp;&#x1F600;&#9999999999;", "<p>\n x \n</p>",
		"<img src=a/>", "<!-- x", "<x a=\"1\" b='2' c/>", "</a><a>", "<a><a></a>", "<a b=>", "<\n>", "a&;b"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		once := Parse(s)
		enc := String(once)
		twice := Parse(enc)
		if String(twice) != enc {
			t.Fatalf("not stable: %q -> %q -> %q", s, enc, String(twice))
		}
		_ = Plain(once, nil)
	})
}

// Parse is linear in what an app may send: no quadratic scan for an unclosed quote or tag.
func TestParseLinear(t *testing.T) {
	for _, s := range []string{
		strings.Repeat(`<a b="`, 50000),
		strings.Repeat(`<a b='x' `, 50000),
		strings.Repeat("<a>", 50000) + strings.Repeat("</b>", 50000),
		strings.Repeat("<a><b>", 25000) + strings.Repeat("</a>", 25000),
	} {
		start := time.Now()
		Parse(s)
		if d := time.Since(start); d > 2*time.Second {
			t.Errorf("%.20q…: %v", s, d)
		}
	}
}
