package join

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/kumoSleeping/iridium/internal/testagent"
)

func TestAddress(t *testing.T) {
	for in, want := range map[string]string{
		"ir.kumo.ltd":            "https://ir.kumo.ltd",
		"https://IR.kumo.ltd/x/": "https://ir.kumo.ltd",
		"127.0.0.1:7811":         "http://127.0.0.1:7811",
		"localhost:7811":         "http://localhost:7811",
		"http://10.0.0.2:7810":   "http://10.0.0.2:7810",
	} {
		if got, err := Address(in); err != nil || got != want {
			t.Errorf("Address(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "ftp://x", "https://u:p@x"} {
		if _, err := Address(bad); err == nil {
			t.Errorf("Address(%q): no error", bad)
		}
	}
	o := Options{URL: "http://127.0.0.1:7811", Base: "/b"}
	if o.key() != "127.0.0.1-7811" || o.tokenFile() != filepath.FromSlash("/b/tokens/127.0.0.1-7811") {
		t.Errorf("key %q, token file %q", o.key(), o.tokenFile())
	}
}

func write(t *testing.T, path, data string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), mode); err != nil {
		t.Fatal(err)
	}
}

// dshPackage lays out an installed dsh under prefix (npm's lib/node_modules on Unix,
// node_modules beside the shims on Windows): its bin.js.
func dshPackage(t *testing.T, modules string) string {
	pkg := filepath.Join(modules, "@deepseek-ai", "dsh")
	write(t, filepath.Join(pkg, "package.json"), `{"name":"@deepseek-ai/dsh","version":"0.2.2-rc.1"}`, 0o644)
	write(t, filepath.Join(pkg, "node_modules", "@deepseek-ai", "dsh-web-app", "presets", "default.yml"), "", 0o644)
	js := filepath.Join(pkg, "lib", "bin.js")
	write(t, js, "#!/usr/bin/env node\n", 0o755)
	return js
}

// npm's .cmd shim, as cmd-shim writes it.
const shim = `@ECHO off
GOTO start
:find_dp0
SET dp0=%~dp0
EXIT /b
:start
SETLOCAL
CALL :find_dp0

IF EXIST "%dp0%\node.exe" (
  SET "_prog=%dp0%\node.exe"
) ELSE (
  SET "_prog=node"
  SET PATHEXT=%PATHEXT:;.JS;=;%
)

endLocal & goto #_undefined_# 2>NUL || title %COMSPEC% & "%_prog%"  "%dp0%\node_modules\@deepseek-ai\dsh\lib\bin.js" %*
`

// On Windows npm puts a .cmd shim on PATH: dsh starts as node and the package's bin.js, with its
// scope, version and DSH_HOME; on Unix a link to bin.js does the same.
func TestResolveDsh(t *testing.T) {
	t.Setenv("DSH_HOME", "/dsh-home")
	npm := t.TempDir() // %APPDATA%\npm
	js := dshPackage(t, filepath.Join(npm, "node_modules"))
	write(t, filepath.Join(npm, "node.exe"), "", 0o755)
	write(t, filepath.Join(npm, "dsh.cmd"), strings.ReplaceAll(shim, "\n", "\r\n"), 0o644)
	a, err := resolveAgent("dsh", filepath.Join(npm, "dsh.cmd"))
	if err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(npm, "node_modules", "@deepseek-ai", "dsh")
	want := []string{filepath.Join(npm, "node.exe"), js}
	if !slices.Equal(a.Argv, want) || a.Version != "0.2.2-rc.1" || a.Package != pkg ||
		a.Scope != filepath.Join(pkg, "node_modules", "@deepseek-ai") || a.Home != "/dsh-home" {
		t.Fatalf("from the shim: %+v", a)
	}

	prefix := t.TempDir() // ~/.npm-global
	js = dshPackage(t, filepath.Join(prefix, "lib", "node_modules"))
	link := filepath.Join(prefix, "bin", "dsh")
	os.MkdirAll(filepath.Dir(link), 0o755)
	if err := os.Symlink(js, link); err != nil {
		t.Skip(err)
	}
	bin := t.TempDir()
	write(t, filepath.Join(bin, "node"), "#!/bin/sh\n", 0o755)
	t.Setenv("PATH", bin)
	a, err = resolveAgent("dsh", link)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(a.Argv, []string{filepath.Join(bin, "node"), js}) || a.Version != "0.2.2-rc.1" {
		t.Fatalf("from the link: %+v", a)
	}

	// A dsh that is no @deepseek-ai/dsh package is left out.
	write(t, filepath.Join(bin, "dsh"), "#!/bin/sh\n", 0o755)
	if _, err := resolveAgent("dsh", filepath.Join(bin, "dsh")); err == nil {
		t.Fatal("a bare dsh")
	}
}

// Another agent on PATH: its program, and the version it says.
func TestFindAgents(t *testing.T) {
	bin := t.TempDir()
	write(t, filepath.Join(bin, "codex"), "#!/bin/sh\necho codex-cli 0.130.0\n", 0o755)
	t.Setenv("PATH", bin)
	got := FindAgents(t.Logf)
	if len(got) != 1 || got[0].Agent != "codex" || got[0].Version != "0.130.0" || !slices.Equal(got[0].Argv, []string{filepath.Join(bin, "codex")}) {
		t.Fatalf("%+v", got)
	}
}

// A login entry left from before (--install) goes when it runs: stopped and removed, the
// one-time run by it removing it without stopping itself and ending cleanly.
func TestRemoveOld(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	unit := filepath.Join(cfg, "systemd", "user", "iridium-join-ir.example.com.service")
	write := func() {
		if err := os.MkdirAll(filepath.Dir(unit), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(unit, []byte("[Service]\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var ran []string
	run := func(name string, args ...string) error {
		ran = append(ran, name+" "+strings.Join(args, " "))
		return nil
	}
	o := Options{URL: "https://ir.example.com"}
	if ok, err := removeEntry("linux", o.oldName(), run, true); ok || err != nil || len(ran) != 0 {
		t.Fatalf("none there: %v %v %q", ok, err, ran)
	}
	write()
	if ok, err := removeEntry("linux", o.oldName(), run, true); !ok || err != nil {
		t.Fatalf("removed: %v %v", ok, err)
	}
	if _, err := os.Stat(unit); !os.IsNotExist(err) {
		t.Errorf("the unit kept: %v", err)
	}
	if want := []string{"systemctl --user disable --now iridium-join-ir.example.com.service", "systemctl --user daemon-reload"}; !slices.Equal(ran, want) {
		t.Errorf("ran %q", ran)
	}
	ran = nil
	write()
	if ok, _ := removeEntry("linux", o.oldName(), run, false); !ok || slices.ContainsFunc(ran, func(s string) bool { return strings.Contains(s, "--now") }) {
		t.Errorf("not to stop: %v %q", ok, ran)
	}
	ran = nil
	if ok, _ := removeEntry("windows", o.oldName(), run, false); !ok || !slices.Equal(ran, []string{"schtasks /Query /TN iridium-join-ir.example.com", "schtasks /Delete /TN iridium-join-ir.example.com /F"}) {
		t.Errorf("windows: %v %q", ok, ran)
	}

	// Run by the entry: removed, and it ends cleanly.
	if runtime.GOOS == "linux" {
		sysRun = run
		defer func() { sysRun = runCmd }()
		write()
		var out, errs strings.Builder
		if code := IRJoin.Main([]string{"https://ir.example.com", "--resident", "--state", "/s", "--headless"}, &out, &errs); code != 0 {
			t.Fatalf("exit %d: %s", code, errs.String())
		}
		if _, err := os.Stat(unit); !os.IsNotExist(err) || !strings.Contains(out.String(), "ir join https://ir.example.com") {
			t.Errorf("left: %v %q", err, out.String())
		}
	}
}

// The dsh plugin and the dev scripts file an Iridium's TOKEN and holders under the same name ir
// join does, so a machine left for another Iridium and brought back finds them.
func TestKeyAsPlugin(t *testing.T) {
	plugin := testagent.DSHPlugin(t)
	urls := []string{"https://ir.kumo.ltd", "http://127.0.0.1:7811", "https://IR.Kumo.ltd", "http://[::1]:7810", "https://host.example:8443"}
	script := `import(` + strconv.Quote("file://"+filepath.ToSlash(filepath.Join(plugin, "executor.js"))) + `).then((m) => { for (const u of process.argv.slice(1)) console.log(m.iridiumKey(u)) })`
	out, err := exec.Command("node", append([]string{"-e", script}, urls...)...).Output()
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimSpace(string(out)), "\n")
	for i, u := range urls {
		if want := (&Options{URL: u}).key(); i >= len(got) || got[i] != want {
			t.Errorf("%s: plugin %q, ir join %q", u, got, want)
		}
	}
}
