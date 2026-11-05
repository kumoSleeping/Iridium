package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestHelp(t *testing.T) {
	var out, errs bytes.Buffer
	if code := run([]string{"--help"}, &out, &errs); code != 0 || out.String() != Help {
		t.Fatalf("--help: %d %q", code, out.String())
	}
	if n := strings.Count(Help, "\n"); n > 20 {
		t.Errorf("help is %d lines, more than 20", n)
	}
	if code := run([]string{"ls"}, &out, &errs); code != 2 || !strings.Contains(errs.String(), "not implemented") {
		t.Errorf("ls: %d %q", code, errs.String())
	}
}
