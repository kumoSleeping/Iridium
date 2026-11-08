package join

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf16"
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

func TestWinQuote(t *testing.T) {
	for in, want := range map[string]string{
		`C:\x\iridium.exe`:         `C:\x\iridium.exe`,
		`C:\Program Files\a b\`:    `"C:\Program Files\a b\\"`,
		`say "hi"`:                 `"say \"hi\""`,
		``:                         `""`,
		`C:\Users\a b\x\"`:         `"C:\Users\a b\x\\\""`,
		`https://ir.example.com/x`: `https://ir.example.com/x`,
	} {
		if got := winQuote(in); got != want {
			t.Errorf("winQuote(%q) = %s, want %s", in, got, want)
		}
	}
}

// --install writes the system's way of starting at login and starts it; --uninstall undoes it.
func TestResident(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("PATH", "/usr/bin:/a b")
	o := Options{URL: "https://ir.example.com", Name: "home pc", Roots: []string{"/w", "/x"}, Base: "/state/join"}
	var ran []string
	run := func(name string, args ...string) error {
		ran = append(ran, name+" "+strings.Join(args, " "))
		return nil
	}

	r := residentFor(o, "iridium-test-join")
	r.goos = "linux"
	if err := r.install(run); err != nil {
		t.Fatal(err)
	}
	unit, err := os.ReadFile(filepath.Join(cfg, "systemd", "user", "iridium-test-join.service"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		` join https://ir.example.com --resident --state /state/join --name "home pc" --roots /w,/x` + "\n",
		`Environment="PATH=/usr/bin:/a b"`, "Restart=on-failure", "WantedBy=default.target",
	} {
		if !strings.Contains(string(unit), want) {
			t.Errorf("the unit has no %q:\n%s", want, unit)
		}
	}
	if want := []string{"systemctl --user daemon-reload", "systemctl --user enable iridium-test-join.service", "systemctl --user restart iridium-test-join.service"}; !slices.Equal(ran, want) {
		t.Errorf("ran %q", ran)
	}
	ran = nil
	if err := r.uninstall(run); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(r.file()); !os.IsNotExist(err) || len(ran) != 2 {
		t.Errorf("uninstalled: %v %q", err, ran)
	}
	if err := r.uninstall(run); err != errNotInstalled {
		t.Errorf("twice: %v", err)
	}

	r.goos, r.log = "darwin", "/state/join/ir.example.com/join.log"
	p := r.plist()
	for _, want := range []string{"<string>iridium-test-join</string>", "<string>home pc</string>", "<string>--resident</string>", "<key>SuccessfulExit</key>\n\t\t<false/>", "<string>/usr/bin:/a b</string>"} {
		if !strings.Contains(p, want) {
			t.Errorf("the plist has no %q:\n%s", want, p)
		}
	}

	r.goos, r.user = "windows", `PC\kumo`
	r.argv = []string{`C:\Users\kumo\iridium.exe`, "join", "https://ir.example.com", "--name", "home pc"}
	b := r.taskXML()
	if len(b) < 2 || b[0] != 0xFF || b[1] != 0xFE {
		t.Fatal("no UTF-16 BOM")
	}
	u := make([]uint16, (len(b)-2)/2)
	for i := range u {
		u[i] = binary.LittleEndian.Uint16(b[2+2*i:])
	}
	x := string(utf16.Decode(u))
	for _, want := range []string{
		`encoding="UTF-16"`, "<UserId>PC\\kumo</UserId>", "<LogonType>InteractiveToken</LogonType>",
		"<ExecutionTimeLimit>PT0S</ExecutionTimeLimit>", "conhost.exe</Command>",
		`<Arguments>--headless C:\Users\kumo\iridium.exe join https://ir.example.com --name &#34;home pc&#34;</Arguments>`,
	} {
		if !strings.Contains(x, want) {
			t.Errorf("the task has no %q:\n%s", want, x)
		}
	}
}
