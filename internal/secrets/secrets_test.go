package secrets

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/url"
	"strings"
	"testing"
)

func TestValidName(t *testing.T) {
	for n, ok := range map[string]bool{"GITHUB_TOKEN": true, "A": true, "A1_B": true, "1A": false, "_A": false, "a": false, "A-B": false, "": false, strings.Repeat("A", 64): true, strings.Repeat("A", 65): false} {
		if ValidName(n) != ok {
			t.Errorf("ValidName(%q) = %v", n, !ok)
		}
	}
}

func TestMaskForms(t *testing.T) {
	v := "s3cr3t/value+with&chars"
	m := NewMasker(map[string]string{"A": v}, 1, false)
	for _, out := range []string{
		"x " + v + " y",
		"x " + url.QueryEscape(v) + " y",
		"x " + url.PathEscape(v) + " y",
		base64.StdEncoding.EncodeToString([]byte(v)),
		base64.StdEncoding.EncodeToString([]byte("user:" + v + "\n")),
		base64.URLEncoding.EncodeToString([]byte("ab" + v)),
		base64.StdEncoding.EncodeToString([]byte("abc" + v + "tail")),
	} {
		got := m.MaskString(out)
		if !strings.Contains(got, "[secret:A]") {
			t.Errorf("not masked: %q -> %q", out, got)
		}
	}
	if got := m.MaskString("nothing here"); got != "nothing here" {
		t.Errorf("masked %q", got)
	}
	// Short values are left alone where a minimum is set.
	if got := NewMasker(map[string]string{"P": "1234"}, MinShown, false).MaskString("pin 1234"); got != "pin 1234" {
		t.Errorf("short masked: %q", got)
	}
}

func TestMaskJSON(t *testing.T) {
	v := `pa"ss<w>rd&\x`
	m := NewMasker(map[string]string{"A": v}, MinShown, true)
	for _, html := range []bool{true, false} {
		var b bytes.Buffer
		enc := jsonEncoder(&b, html)
		_ = enc.Encode(map[string]string{"text": "it is " + v})
		got := string(m.Mask(b.Bytes()))
		if !strings.Contains(got, `"it is [secret:A]"`) {
			t.Errorf("json (html %v): %s", html, got)
		}
	}
}

func TestWriterAcrossWrites(t *testing.T) {
	v := "hunter2-hunter2"
	m := NewMasker(map[string]string{"PW": v}, 1, false)
	in := "a " + v + " b " + v + v + " c hunt"
	for size := 1; size <= len(in); size++ {
		var out bytes.Buffer
		w := NewWriter(&out, func() *Masker { return m })
		for i := 0; i < len(in); i += size {
			_, _ = w.Write([]byte(in[i:min(i+size, len(in))]))
		}
		_ = w.Close()
		want := "a [secret:PW] b [secret:PW][secret:PW] c hunt"
		if out.String() != want {
			t.Fatalf("size %d: %q", size, out.String())
		}
	}
	// What cannot begin a value goes out at once.
	var out bytes.Buffer
	w := NewWriter(&out, func() *Masker { return m })
	_, _ = w.Write([]byte("prompt: "))
	if out.String() != "prompt: " {
		t.Fatalf("held back %q", out.String())
	}
	_, _ = w.Write([]byte("xx hun"))
	if out.String() != "prompt: xx " {
		t.Fatalf("should hold the start of a value: %q", out.String())
	}
}

func TestRefsExpand(t *testing.T) {
	args := []string{"curl", "-H", "Authorization: Bearer {{TOKEN}}", "-K", "{{file:CFG}}", "{{lower}}", "{{TOKEN}}"}
	refs := Refs(args)
	if strings.Join(refs, ",") != "TOKEN,CFG" {
		t.Fatalf("refs %v", refs)
	}
	got := Expand(args[2], func(n string) string { return "<" + n + ">" }, func(n string) string { return "/tmp/" + n })
	if got != "Authorization: Bearer <TOKEN>" {
		t.Fatal(got)
	}
	if got := Expand(args[4], nil, func(n string) string { return "/tmp/" + n }); got != "/tmp/CFG" {
		t.Fatal(got)
	}
}

func jsonEncoder(w io.Writer, html bool) *json.Encoder {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(html)
	return enc
}
