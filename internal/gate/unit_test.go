package gate

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNormalizeName(t *testing.T) {
	for in, want := range map[string]any{
		"Kumo Mac":           "Kumo Mac",
		"  kumo   m2\tmac  ": "kumo m2 mac",
		"ｋｕｍｏ　ＭＡＣ":           "kumo MAC", // full width, ideographic space
		"我的手机":               "我的手机",
		"m2_mac.1-x":         "m2_mac.1-x",
		"123456789012":       "123456789012",
		"一二三四五六七八九十一二":       "一二三四五六七八九十一二",
		"Ünïcödé":            "Ünïcödé",
		"":                   ErrNameEmpty,
		"   ":                ErrNameEmpty,
		"1234567890123":      ErrNameTooLong,
		"一二三四五六七八九十一二三": ErrNameTooLong,
		"[mac]":      ErrNameInvalid,
		"QQ·张三":      ErrNameInvalid,
		"mac:~/dir":  ErrNameInvalid,
		"a/b":        ErrNameInvalid,
		"bell\a":     ErrNameInvalid,
		"rtl‮evil":   ErrNameInvalid, // bidi override
		"zero​width": ErrNameInvalid,
		"emoji😀":     ErrNameInvalid,
		"本机":         ErrNameReserved,
		"未知":         ErrNameReserved,
		"LOCAL":      ErrNameReserved,
		"Ｕｎｋｎｏｗｎ":    ErrNameReserved,
		"host":       ErrNameReserved,
		"HOST":       ErrNameReserved,
		"主机":         ErrNameReserved,
		"ir_web":     ErrNameReserved,
		"IR_Web":     ErrNameReserved,
		"c":          ErrNameLetter,
		"Ｃ":          ErrNameLetter, // full width, narrowed first
		" Z ":        ErrNameLetter,
		"中":          "中",
		"é":          "é", // a letter, but not ASCII: no drive letter
		"7":          "7",
		"ab":         "ab",
	} {
		got, err := NormalizeName(in)
		if want, ok := want.(error); ok {
			if err != want {
				t.Errorf("NormalizeName(%q) = %q, %v; want %v", in, got, err, want)
			}
			continue
		}
		if err != nil || got != want {
			t.Errorf("NormalizeName(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if nameKey("Kumo MAC") != nameKey("kumo mac") || nameKey("Straße") == nameKey("strasse") {
		t.Error("nameKey folding")
	}
	if nameKey("ΣΊΣΥΦΟΣ") != nameKey("σίσυφος") {
		t.Error("nameKey: Greek final sigma")
	}
}

func TestTokens(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		tok := newToken()
		if !wellFormed(tok) || seen[tok] {
			t.Fatalf("token %d malformed or repeated", i)
		}
		seen[tok] = true
		ch := newChannelToken()
		if !wellFormedChannel(ch) || wellFormed(ch) || wellFormedChannel(tok) {
			t.Fatalf("channel token format")
		}
	}
	for _, bad := range []string{"", "short", strings.Repeat("a", tokenLen+1), strings.Repeat("=", tokenLen), strings.Repeat("é", tokenLen/2)} {
		if wellFormed(bad) {
			t.Errorf("wellFormed(%q)", bad)
		}
	}
	h := hashToken("x")
	if len(h) != 64 || h != hashToken("x") || h == hashToken("y") || !hashEqual(h, hashToken("x")) {
		t.Error("hashToken")
	}
	if id := newID(10); len(id) != 16 || strings.ToLower(id) != id || len(id) <= MaxNameLen {
		t.Errorf("endpoint id %q", id)
	}
}

func TestLimiter(t *testing.T) {
	l := NewLimiter()
	now := time.Unix(1e9, 0)
	for i := 0; i < maxFailures-1; i++ {
		l.Fail("a", now)
	}
	if l.Wait("a", now) != 0 {
		t.Fatal("locked too early")
	}
	l.Fail("a", now)
	if l.Wait("a", now) != lockout || l.Wait("b", now) != 0 {
		t.Fatal("lockout")
	}
	if l.Wait("a", now.Add(lockout)) != 0 {
		t.Fatal("lockout did not lift")
	}
	// Failures spread over more than the window never lock.
	for i := 0; i < 3*maxFailures; i++ {
		now = now.Add(failureWindow / maxFailures * 2)
		l.Fail("c", now)
		if l.Wait("c", now) != 0 {
			t.Fatalf("slow failures locked at %d", i)
		}
	}
	l.Fail("d", now)
	l.Success("d")
	if _, ok := l.buckets["d"]; ok {
		t.Fatal("success kept the bucket")
	}
	// The table is bounded.
	for i := 0; i < maxBuckets+50; i++ {
		l.Fail(strings.Repeat("k", 1+i%7)+string(rune('a'+i%26))+time.Duration(i).String(), now)
	}
	if len(l.buckets) > maxBuckets {
		t.Fatalf("%d buckets", len(l.buckets))
	}
}

func TestClientKey(t *testing.T) {
	for _, c := range []struct{ remote, header, value, want string }{
		{"198.51.100.7:1", "CF-Connecting-IP", "", "198.51.100.7"},
		{"198.51.100.7:1", "CF-Connecting-IP", "192.0.2.1", "198.51.100.7"}, // not from loopback
		{"127.0.0.1:1", "CF-Connecting-IP", "192.0.2.1", "192.0.2.1"},
		{"[::1]:1", "CF-Connecting-IP", "192.0.2.1", "192.0.2.1"},
		{"127.0.0.1:1", "CF-Connecting-IP", "junk", "127.0.0.1"},
		{"127.0.0.1:1", "X-Forwarded-For", "10.0.0.1, 192.0.2.7", "192.0.2.7"},
		{"[2001:db8:1:2:3:4:5:6]:1", "CF-Connecting-IP", "", "2001:db8:1:2::/64"},
		{"[::ffff:198.51.100.7]:1", "CF-Connecting-IP", "", "198.51.100.7"},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = c.remote
		if c.value != "" {
			r.Header.Set(c.header, c.value)
		}
		if got := ClientKey(r, c.header); got != c.want {
			t.Errorf("clientKey(%s, %s=%s) = %s, want %s", c.remote, c.header, c.value, got, c.want)
		}
	}
}

func TestJSONStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "gate.json")
	s, err := OpenJSONStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(filepath.Dir(path)); fi.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode %v", fi.Mode().Perm())
	}
	now := time.Unix(1e9, 0).UTC()
	e := Endpoint{ID: "e1", Name: "mac", Kind: KindPanel, Avatar: []byte{1, 2}, CreatedAt: now}
	e2 := Endpoint{ID: "e2", Name: "qq", Kind: KindChannel, CreatedAt: now}
	if err := s.Apply(Change{Endpoints: []Endpoint{e, e2}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Apply(Change{DeleteEndpoints: []string{"e2"}}); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Fatalf("file mode %v", fi.Mode().Perm())
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatalf("temp files left: %v", entries)
	}
	s2, err := OpenJSONStore(path)
	if err != nil {
		t.Fatal(err)
	}
	st, _ := s2.Load()
	if len(st.Endpoints) != 1 || st.Endpoints[0].Name != "mac" || string(st.Endpoints[0].Avatar) != "\x01\x02" {
		t.Fatalf("reloaded %+v", st)
	}
	// A failed write leaves the old state in place.
	os.Chmod(filepath.Dir(path), 0o500)
	defer os.Chmod(filepath.Dir(path), 0o700)
	if os.Getuid() != 0 {
		if err := s2.Apply(Change{DeleteEndpoints: []string{"e1"}}); err == nil {
			t.Fatal("write into a read-only dir succeeded")
		}
		if st, _ := s2.Load(); len(st.Endpoints) != 1 {
			t.Fatal("failed Apply changed the state")
		}
	}
	bad := filepath.Join(t.TempDir(), "bad.json")
	os.WriteFile(bad, []byte(`{"version":9}`), 0o600)
	if _, err := OpenJSONStore(bad); err == nil {
		t.Fatal("unknown version accepted")
	}
}
