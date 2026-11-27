package session

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/config"
	"github.com/kumoSleeping/iridium/internal/remote"
	"github.com/kumoSleeping/iridium/internal/remote/wire"
	"github.com/kumoSleeping/iridium/internal/transcript"
)

// The dsh plugin's ir (ir.js, put on the PATH by its executor's withIR, the address and the TOKEN
// written in): an agent's `ir ...` goes to the Iridium's POST /v1/internal/cli with the session's TOKEN,
// from the folder it ran in, even with no IRIDIUM_* in its commands' environment (dsh's have
// none), and what comes back is written out as it comes, the exit code last.
func TestPluginIR(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("no node")
	}
	var got api.CLIRun
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/internal/cli" || r.Header.Get("Authorization") != "Bearer irs_x" {
			http.Error(w, `{"error":"no"}`, http.StatusUnauthorized)
			return
		}
		json.NewDecoder(r.Body).Decode(&got)
		three := 3
		for _, o := range []api.CLIOutput{{Out: "one\n"}, {Err: "two\n"}, {Exit: &three}} {
			b, _ := json.Marshal(o)
			w.Write(append(b, '\n'))
		}
	}))
	t.Cleanup(srv.Close)
	plugin, _ := filepath.Abs("../../plugins/dsh-remote-iridium")
	work := t.TempDir()
	script := `
import { Executor } from ` + jsString(filepath.Join(plugin, "executor.js")) + `
import { spawnSync } from 'node:child_process'
const ex = new Executor({ url: process.argv[1], token: 't', state: process.argv[2], ir: process.argv[3] })
const env = { ...process.env, IRIDIUM_TOKEN: 'irs_x' }
delete env.IRIDIUM_URL
ex.withIR(env, process.argv[2] + '/holds/h1')
// As dsh runs an agent's commands: its PATH, not its IRIDIUM_* (ir has them written in).
const r = spawnSync('ir', ['ls', '--here'], { env: { PATH: env.PATH }, cwd: process.argv[4], encoding: 'utf8' })
console.log(JSON.stringify({ status: r.status, out: r.stdout, err: r.stderr, url: env.IRIDIUM_URL }))
`
	cmd := exec.Command("node", "--input-type=module", "-e", script, "ws"+strings.TrimPrefix(srv.URL, "http")+"/v1/internal/exec", filepath.Join(t.TempDir(), "state"), filepath.Join(plugin, "ir.js"), work)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	var res struct {
		Status        int
		Out, Err, URL string
	}
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if res.Status != 3 || res.Out != "one\n" || res.Err != "two\n" || res.URL != srv.URL {
		t.Errorf("ir: %+v", res)
	}
	if strings.Join(got.Args, " ") != "ls --here" || got.Cwd != work {
		t.Errorf("sent: %+v", got)
	}
}

func jsString(s string) string { b, _ := json.Marshal(s); return string(b) }

// A session at a machine whose executor has ir: its TOKEN, given at each start, names it and
// that machine; a TOKEN given before does not, nor one with another prefix.
func TestSessionToken(t *testing.T) {
	t.Parallel()
	r := newRig(t, nil)
	h := r.m.newHosted(meta{ID: "s1", Agent: api.AgentDsh, Cwd: "/w", Machine: "m1"})
	if err := os.MkdirAll(h.dir, 0o700); err != nil {
		t.Fatal(err)
	}
	r.m.mu.Lock()
	r.m.hosted["s1"] = h
	r.m.mu.Unlock()
	tok := IRTokenPrefix + "this-start"
	os.WriteFile(filepath.Join(h.dir, fileIRToken), []byte(tokenHash(tok)), 0o600)
	if m, s, ok := r.m.SessionToken(tok); !ok || m != "m1" || s != "s1" {
		t.Errorf("its TOKEN: %q %q %v", m, s, ok)
	}
	os.WriteFile(filepath.Join(h.dir, fileIRToken), []byte(tokenHash(tok+"2")), 0o600) // started again
	if _, _, ok := r.m.SessionToken(tok); ok {
		t.Error("the TOKEN of an earlier start still taken")
	}
	if _, _, ok := r.m.SessionToken("ir_" + tok[4:]); ok {
		t.Error("a channel's prefix taken")
	}
}

// desktopFinder stands in for discovery: one dsh session at m1, open in DSH Desktop there.
type desktopFinder struct {
	id  string
	cwd string
}

func (f desktopFinder) Sessions(context.Context) ([]api.Session, error) { return nil, nil }
func (f desktopFinder) Free(string) error                               { return nil }
func (f desktopFinder) Found(id string) (api.Session, *transcript.File, bool) {
	if id != f.id {
		return api.Session{}, nil, false
	}
	cwd := f.cwd
	if cwd == "" {
		cwd = `C:\w`
	}
	return api.Session{ID: id, Agent: api.AgentDsh, Machine: "m1", Status: api.StatusElsewhere, Live: true, Cwd: cwd}, nil, true
}

// A dsh session on a remote Desktop speaking dsh.inproc runs through an @dsh-desktop hold (no
// held.send forwarding, no --profile sdk or --patch files): the fake inproc handler receives the
// @dsh-desktop argv directly.
func TestToDesktop(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("no node")
	}
	const id = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	there := t.TempDir()
	hub := &remote.Hub{Logf: t.Logf}
	r := newRig(t, func(_ *config.Config, o *Options) { o.Remote = hub; o.Discovery = desktopFinder{id: id, cwd: there} })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { hub.Serve(w, req, "m1", "pc") }))
	t.Cleanup(srv.Close)
	desktop := filepath.Join(there, "desktop")
	os.MkdirAll(desktop, 0o700)
	os.WriteFile(filepath.Join(desktop, "open.json"), []byte(`["`+id+`"]`), 0o600)
	os.WriteFile(filepath.Join(desktop, "models.json"), []byte(`{"default":"p/m","models":[{"value":"p/m","name":"M"}]}`), 0o600)
	argvFile := filepath.Join(there, "inproc-argv.json")
	script := filepath.Join(there, "fake-inproc.mjs")
	os.WriteFile(script, []byte(`
import fs from 'node:fs'
import readline from 'node:readline'
fs.writeFileSync(`+jsonStr(argvFile)+`, process.env.IRIDIUM_TEST_INPROC_ARGV || '[]')
const rl = readline.createInterface({ input: process.stdin })
rl.on('line', (line) => {
  if (!line.trim()) return
  const msg = JSON.parse(line)
  if (msg.id === undefined) return
  let result = {}
  if (msg.method === 'initialize') result = { serverInfo: { version: '0.2.2-rc.5' } }
  else if (msg.method === 'session/prompt') result = { promptId: 'p1' }
  process.stdout.write(JSON.stringify({ jsonrpc: '2.0', id: msg.id, result }) + '\n')
  if (msg.method === 'shutdown') process.exit(0)
})
`), 0o600)
	inprocCmd, _ := json.Marshal([]string{"node", script})
	agents, _ := json.Marshal([]wire.AgentInfo{{Agent: "dsh", Argv: []string{"@dsh-desktop"}, Version: "0.2.2-rc.5", Home: filepath.Join(there, ".dsh")}})
	cmd := exec.Command("node", "../../plugins/dsh-remote-iridium/test/run.mjs", "ws"+strings.TrimPrefix(srv.URL, "http"), "tok", filepath.Join(there, "executor"))
	cmd.Env = append(os.Environ(), "IRIDIUM_TEST_AGENTS="+string(agents), "IRIDIUM_TEST_DSH="+desktop, "IRIDIUM_TEST_INPROC="+string(inprocCmd))
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Signal(syscall.SIGTERM); _ = cmd.Wait() })
	waitFor(t, "the executor", func() bool { _, ok := hub.Machine("m1").Hello(); return ok })

	if _, _, err := r.m.SendWith(bg, id, "go on", "web", false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "inproc hold started", func() bool {
		b, err := os.ReadFile(argvFile)
		return err == nil && len(b) > 2
	})
	b, _ := os.ReadFile(argvFile)
	var argv []string
	if err := json.Unmarshal(b, &argv); err != nil || len(argv) == 0 || argv[0] != "@dsh-desktop" {
		t.Fatalf("expected @dsh-desktop inproc argv, got %s (%v)", b, err)
	}
	for _, bad := range []string{"--profile", "--patch"} {
		if strings.Contains(string(b), bad) {
			t.Errorf("inproc argv must not include %s: %s", bad, b)
		}
	}
}

func jsonStr(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
