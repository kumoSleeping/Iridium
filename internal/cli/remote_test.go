package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kumoSleeping/iridium/internal/api"
)

// ir at another machine sends its arguments with the session's TOKEN and prints what comes back.
func TestForwardAt(t *testing.T) {
	var got api.CLIRun
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer irs_x" {
			http.Error(w, `{"error":"unauthenticated"}`, http.StatusUnauthorized)
			return
		}
		json.NewDecoder(r.Body).Decode(&got)
		two := 2
		for _, o := range []api.CLIOutput{{Out: "a\n"}, {Err: "b\n"}, {Exit: &two}} {
			b, _ := json.Marshal(o)
			w.Write(append(b, '\n'))
		}
	}))
	defer srv.Close()
	var out, errs bytes.Buffer
	if code := ForwardAt(srv.URL, "irs_x", []string{"wait", "abc"}, &out, &errs); code != 2 || out.String() != "a\n" || errs.String() != "b\n" || len(got.Args) != 2 {
		t.Errorf("%d %q %q %+v", code, out.String(), errs.String(), got)
	}
	errs.Reset()
	if code := ForwardAt(srv.URL, "irs_bad", nil, &out, &errs); code != 1 || errs.String() != "ir: unauthenticated\n" {
		t.Errorf("refused: %d %q", code, errs.String())
	}
}
