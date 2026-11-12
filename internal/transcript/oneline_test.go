package transcript

import "testing"

func TestOneLineLinks(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"看一下 [KDJ仓库概览](ir:session/1ca494a5) 和 [a b](<ir:folder//x/a b>)\n里的": "看一下 KDJ仓库概览 和 a b 里的",
		"see ![plot](ir:abcdefghij) and [web](https://x.y)":                          "see plot and [web](https://x.y)",
	} {
		if got := oneLine(in); got != want {
			t.Errorf("oneLine(%q) = %q, want %q", in, got, want)
		}
	}
}
