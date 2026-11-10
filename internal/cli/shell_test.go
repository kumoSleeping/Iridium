package cli

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"
)

// ir sh's streamed answer: printed as it comes, its code the exit; past --timeout it hangs up
// (which stops the run) and exits 124.
func TestShStream(t *testing.T) {
	var out, errs bytes.Buffer
	e := &env{out: &out, err: &errs}
	body := io.NopCloser(strings.NewReader(`{"out":"a\n"}` + "\n" + `{"err":"b\n"}` + "\n" + `{"done":true,"exit":3}` + "\n"))
	if code := e.shStream(body, time.Now().Add(time.Minute), time.Minute); code != 3 || out.String() != "a\n" || errs.String() != "b\n" {
		t.Errorf("streamed: %d %q %q", code, out.String(), errs.String())
	}
	errs.Reset()
	body = io.NopCloser(strings.NewReader(`{"done":true,"error":"the machine's connection ended"}` + "\n"))
	if code := e.shStream(body, time.Now().Add(time.Minute), time.Minute); code != exitError || !strings.Contains(errs.String(), "connection ended") {
		t.Errorf("cut off: %d %q", code, errs.String())
	}
	errs.Reset()
	pr, pw := io.Pipe()
	defer pw.Close()
	go pw.Write([]byte(`{"out":"started\n"}` + "\n"))
	if code := e.shStream(pr, time.Now().Add(200*time.Millisecond), 200*time.Millisecond); code != exitTimeout || !strings.Contains(errs.String(), "stopped") {
		t.Errorf("timed out: %d %q", code, errs.String())
	}
}
