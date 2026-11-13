package join

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/remote/wire"
)

// ir pair asks at /v1/internal/pair as an app (its name and program, nothing of the machine), waits while
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
		case denied && r.URL.Path == "/v1/internal/pair/r1":
			json.NewEncoder(w).Encode(api.PairState{Status: "denied"})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/internal/pair":
			json.NewDecoder(r.Body).Decode(&asked)
			json.NewEncoder(w).Encode(api.Pairing{Code: "1234", Request: "r1"})
		case r.URL.Path == "/v1/internal/pair/r1":
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
		strings.Count(out.String()+errs.String(), "没开配对") != 1 {
		t.Fatalf("output: %s / %s", out.String(), errs.String())
	}
	denied = true
	errs.Reset()
	if code := PairMain([]string{srv.URL, "--name", "helper", "--into", into}, &out, &errs); code != 1 || !strings.Contains(errs.String(), "拒绝") {
		t.Fatalf("denied: %d %s", code, errs.String())
	}
}

// ir join asks as a computer: its system, agents, home and computer's hash (decision 17: its home
// is its scope; one on the host carries no agents).
func TestPairComputer(t *testing.T) {
	var asked api.PairRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			json.NewDecoder(r.Body).Decode(&asked)
			json.NewEncoder(w).Encode(api.Pairing{Code: "1234", Request: "r1"})
			return
		}
		json.NewEncoder(w).Encode(api.PairState{Status: "allowed", Token: "ir_x"})
	}))
	defer srv.Close()
	var out bytes.Buffer
	o := Options{URL: srv.URL, Name: "pc", Out: &out, Logf: t.Logf, Agents: []wire.AgentInfo{{Agent: "dsh", Version: "1"}}}
	if _, err := o.pair(context.Background()); err != nil {
		t.Fatal(err)
	}
	home, _ := os.UserHomeDir()
	if asked.Type != "computer" || asked.Home != home || asked.MachineID == "" || len(asked.Agents) != 1 || asked.Wants != nil {
		t.Fatalf("asked as %+v", asked)
	}
}

// ir pair with no address asks this host's Iridium through its socket, with its login (bot by
// default); --env puts the TOKEN as one line of a .env file, the others kept, and says where.
func TestPairEnvSocket(t *testing.T) {
	pairPoll = 10 * time.Millisecond
	var asked api.PairRequest
	sock := filepath.Join(t.TempDir(), "ir.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/internal/pair":
			asked = api.PairRequest{}
			json.NewDecoder(r.Body).Decode(&asked)
			json.NewEncoder(w).Encode(api.Pairing{Code: "1234", Request: "r1"})
		case r.URL.Path == "/v1/internal/pair/r1":
			json.NewEncoder(w).Encode(api.PairState{Status: "allowed", Token: "ir_secret"})
		default:
			http.NotFound(w, r)
		}
	}))
	srv.Listener = ln
	srv.Start()
	defer srv.Close()
	t.Setenv("IRIDIUM_SOCKET", sock)
	t.Setenv("IRIDIUM_CONFIG", filepath.Join(t.TempDir(), "none.json"))
	env := filepath.Join(t.TempDir(), ".env")
	os.WriteFile(env, []byte("A=1\nIR_TOKEN=old\nB=2\n"), 0o644)
	var out, errs bytes.Buffer
	if code := PairMain([]string{"--name", "koishi", "--env", env + ":IR_TOKEN"}, &out, &errs); code != 0 {
		t.Fatalf("exit %d: %s", code, errs.String())
	}
	if asked.Name != "koishi" || asked.Login == nil || asked.Login.ID != "bot" {
		t.Fatalf("asked as %+v", asked)
	}
	b, _ := os.ReadFile(env)
	st, _ := os.Stat(env)
	if string(b) != "A=1\nIR_TOKEN=ir_secret\nB=2\n" || st.Mode().Perm() != 0o600 {
		t.Fatalf("the .env: %q %v", b, st.Mode())
	}
	if o := out.String(); !strings.Contains(o, "本机") || !strings.Contains(o, "http://127.0.0.1:7810") || !strings.Contains(o, "IR_TOKEN") || !strings.Contains(o, "登录 bot") || strings.Contains(o+errs.String(), "ir_secret") {
		t.Fatalf("output: %s / %s", o, errs.String())
	}
	// --login= asks with none; a file that is not there yet is made.
	fresh := filepath.Join(t.TempDir(), "d", ".env")
	if code := PairMain([]string{"--name", "koishi", "--login=", "--env", fresh + ":IR_TOKEN"}, &out, &errs); code != 0 || asked.Login != nil {
		t.Fatalf("no login: exit %d, %+v: %s", code, asked, errs.String())
	}
	if b, _ := os.ReadFile(fresh); string(b) != "IR_TOKEN=ir_secret\n" {
		t.Fatalf("a new .env: %q", b)
	}
	for _, bad := range [][]string{{"--name", "x"}, {"--name", "x", "--into", "a", "--env", "b:C"}, {"--name", "x", "--env", "b"}, {"--name", "x", "--env", "b:1C"}} {
		if code := PairMain(bad, &out, &errs); code != 2 {
			t.Fatalf("%v: exit %d", bad, code)
		}
	}
}

// A root written "~/work" (quoted, so the shell left the ~) is under the home.
func TestHomePath(t *testing.T) {
	home, _ := os.UserHomeDir()
	for in, want := range map[string]string{"~": home, "~/work": filepath.Join(home, "work"), "/srv/x": "/srv/x", "~x": "~x"} {
		if got := homePath(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}
