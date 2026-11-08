package chat

import "testing"

// The directory 未归类's folders are under, as the web's tree.ts finds it.
func TestCommonDir(t *testing.T) {
	for _, c := range []struct {
		in   []string
		want string
	}{
		{[]string{"/home/a/x", "/home/a/y/z"}, "/home/a"},
		{[]string{"/home/a/x", "/home/a/x"}, "/home/a"},
		{[]string{"/home/a/x", "/home/a/x/y"}, "/home/a/x"},
		{[]string{"/a", "/b"}, ""},
		{nil, ""},
	} {
		if got := commonDir(c.in); got != c.want {
			t.Errorf("commonDir(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
