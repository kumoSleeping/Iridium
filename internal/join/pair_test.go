package join

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
)

// ir pair asks at /api/pair as an app (its name and program, nothing of the machine), waits while
// pairing is closed, shows the code, and writes the TOKEN into the file, never to the output;
// denied, it says so.
func TestPairMain(t *testing.T) {
	pairPoll, pairClosedWait = 10*time.Millisecond, 10*time.Millisecond
	closed, denied := 2, false
	var asked api.PairRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case closed > 0 && r.Method == http.MethodPost:
			closed--
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(api.Error{Code: "pair_closed", Message: "closed"})
		case denied && r.URL.Path == "/api/pair/r1":
			json.NewEncoder(w).Encode(api.PairState{Status: "denied"})
		case r.Method == http.MethodPost && r.URL.Path == "/api/pair":
			json.NewDecoder(r.Body).Decode(&asked)
			json.NewEncoder(w).Encode(api.Pairing{Code: "1234", Request: "r1"})
		case r.URL.Path == "/api/pair/r1":
			json.NewEncoder(w).Encode(api.PairState{Status: "allowed", Token: "ir_secret"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	into := filepath.Join(t.TempDir(), "d", "token")
	var out, errs bytes.Buffer
	if code := PairMain([]string{srv.URL, "--name", "helper", "--into", into, "--program", "my-bot 1.0"}, &out, &errs); code != 0 {
		t.Fatalf("exit %d: %s", code, errs.String())
	}
	if asked.Name != "helper" || asked.Program != "my-bot 1.0" || asked.OS != "" || len(asked.Agents) != 0 {
		t.Fatalf("asked as %+v", asked)
	}
	b, err := os.ReadFile(into)
	st, _ := os.Stat(into)
	if err != nil || strings.TrimSpace(string(b)) != "ir_secret" || st.Mode().Perm() != 0o600 {
		t.Fatalf("the file: %q %v", b, err)
	}
	if !strings.Contains(out.String(), "1234") || strings.Contains(out.String()+errs.String(), "ir_secret") ||
		strings.Count(out.String()+errs.String(), "没有开放配对") != 1 {
		t.Fatalf("output: %s / %s", out.String(), errs.String())
	}
	denied = true
	errs.Reset()
	if code := PairMain([]string{srv.URL, "--name", "helper", "--into", into}, &out, &errs); code != 1 || !strings.Contains(errs.String(), "拒绝") {
		t.Fatalf("denied: %d %s", code, errs.String())
	}
}
