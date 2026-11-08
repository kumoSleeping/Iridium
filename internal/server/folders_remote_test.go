package server

import (
	"testing"

	"github.com/kumoSleeping/iridium/internal/remote/wire"
)

// Paths at another machine: the page's '/' form, its own separator there, the parent of each.
func TestRemotePaths(t *testing.T) {
	win := &wire.Hello{Sep: `\`, Home: `C:\Users\kumo`}
	unix := &wire.Hello{Sep: "/", Home: "/Users/kumo"}
	for _, c := range []struct {
		hi               *wire.Hello
		page, native, up string
	}{
		{win, "~", `C:\Users\kumo`, `C:\Users`},
		{win, "~/a/b", `C:\Users\kumo\a\b`, `C:\Users\kumo\a`},
		{win, "C:/Users", `C:\Users`, `C:\`},
		{win, "C:", `C:\`, ""},
		{unix, "", "/Users/kumo", "/Users"},
		{unix, "/Users", "/Users", "/"},
		{unix, "/", "/", ""},
	} {
		n := nativePath(c.hi, c.page)
		if n != c.native || parentPath(c.hi, n) != c.up {
			t.Errorf("%q: %q (up %q), want %q (up %q)", c.page, n, parentPath(c.hi, n), c.native, c.up)
		}
	}
	if pagePath(win, `C:\Users\kumo`) != "C:/Users/kumo" {
		t.Error("the page's form")
	}
}
