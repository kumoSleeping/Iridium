package joinscript

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kumoSleeping/iridium/internal/version"
)

func get(t *testing.T, target, origin string) (int, string) {
	t.Helper()
	w := httptest.NewRecorder()
	Serve(w, httptest.NewRequest(http.MethodGet, target, nil), origin)
	return w.Code, w.Body.String()
}

// An Iridium's script installs its version from GitHub; with proxy, through it; the paths of
// before are gone.
func TestServe(t *testing.T) {
	was := version.Version
	defer func() { version.Version = was }()
	version.Version = "v1.2.3"
	for target, want := range map[string]string{
		"/install":             "base='https://github.com/kumoSleeping/Iridium/releases/download/v1.2.3/'",
		"/install?proxy":       "base='https://ir.example.com/install/'",
		"/install?proxy=0":     "base='https://github.com/kumoSleeping/Iridium/releases/download/v1.2.3/'",
		"/install.ps1":         "$base = 'https://github.com/kumoSleeping/Iridium/releases/download/v1.2.3/'",
		"/install.ps1?proxy=1": "$base = 'https://ir.example.com/install/'",
	} {
		code, s := get(t, target, "https://ir.example.com")
		if code != 200 || !strings.Contains(s, want) || strings.Contains(s, "{{") {
			t.Errorf("%s: %d, no %s in:\n%s", target, code, want, s)
		}
		if !strings.Contains(s, "https://ir.example.com") {
			t.Errorf("%s: the origin not in what it says next", target)
		}
	}
	version.Version = "v1.2.4-dev.3+gabc"
	if _, s := get(t, "/install", "https://ir.example.com"); !strings.Contains(s, "base='https://github.com/kumoSleeping/Iridium/releases/latest/download/'") {
		t.Errorf("a build of no release:\n%s", s)
	}
	for _, p := range []string{"/join", "/join.ps1"} {
		if code, _ := get(t, p, "https://ir.example.com"); code != 404 {
			t.Errorf("%s: %d", p, code)
		}
	}
	if code, _ := get(t, "/install", "https://ir.example.com/'x"); code != 400 {
		t.Errorf("a bad origin: %d", code)
	}
}

// The sh script, run: it fetches the archive and SHA256SUMS from base, checks it, puts iridium in
// ~/.local/opt/iridium with ir linked in ~/.local/bin, and runs what it is given as ir; a file that
// does not match is not installed.
func TestInstallSh(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the Linux branch")
	}
	for _, c := range []string{"sh", "curl", "tar", "awk", "sha256sum"} {
		if _, err := exec.LookPath(c); err != nil {
			t.Skip(c, err)
		}
	}
	prog := []byte("#!/bin/sh\necho \"ran: $*\"\n")
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range []struct {
		name string
		b    []byte
	}{{"./iridium", prog}, {"./README.md", []byte("r")}, {"./icon.png", []byte("png")}} {
		tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0o755, Size: int64(len(f.b)), Typeflag: tar.TypeReg})
		tw.Write(f.b)
	}
	tw.Close()
	gz.Close()
	archive := ArchiveName("linux", map[string]string{"amd64": "amd64", "arm64": "arm64"}[runtime.GOARCH])
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		t.Skip(runtime.GOARCH)
	}
	sum := sha256.Sum256(buf.Bytes())
	good := hex.EncodeToString(sum[:]) + "  " + archive + "\n"
	sums := good
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/r/SHA256SUMS":
			w.Write([]byte(sums))
		case "/r/" + archive:
			w.Write(buf.Bytes())
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	script, err := Render(false, srv.URL+"/r/", "v1.2.3", "https://ir.example.com")
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	run := func(args ...string) (string, error) {
		cmd := exec.Command("sh", append([]string{"-s", "--"}, args...)...)
		cmd.Stdin = bytes.NewReader(script)
		cmd.Env = []string{"HOME=" + home, "PATH=" + os.Getenv("PATH")}
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	out, err := run()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	exe := filepath.Join(home, ".local", "opt", "iridium", "iridium")
	if b, _ := os.ReadFile(exe); !bytes.Equal(b, prog) {
		t.Fatalf("not installed: %q\n%s", b, out)
	}
	if l, _ := os.Readlink(filepath.Join(home, ".local", "bin", "ir")); l != exe {
		t.Errorf("ir -> %q", l)
	}
	if !strings.Contains(out, "ir start --no-server --join https://ir.example.com") {
		t.Errorf("what next:\n%s", out)
	}
	if out, err := run("start", "--no-server", "--join", "https://ir.example.com"); err != nil || !strings.Contains(out, "ran: ir start --no-server --join https://ir.example.com") {
		t.Errorf("then ir: %v\n%s", err, out)
	}
	os.Remove(exe)
	sums = strings.Repeat("0", 64) + "  " + archive + "\n"
	if out, err := run(); err == nil || !strings.Contains(out, "SHA256SUMS") {
		t.Errorf("a bad sum installed: %v\n%s", err, out)
	}
	if _, err := os.Stat(exe); err == nil {
		t.Error("a bad one in place")
	}
}

// The PowerShell script stays ASCII (irm may read it in any code page) with its values filled in.
func TestPs1(t *testing.T) {
	b, err := Render(true, "https://ir.example.com/install/", "v1.2.3", "https://ir.example.com")
	if err != nil {
		t.Fatal(err)
	}
	for i, c := range b {
		if c > 0x7f {
			t.Fatalf("not ASCII at %d", i)
		}
	}
	if !strings.Contains(string(b), "} @args") || strings.Contains(string(b), "{{") {
		t.Errorf("script:\n%s", b)
	}
}

// A Mac's sh (bash 3.2) takes the bytes of a character past ASCII as part of a variable's name:
// "$release（" is the variable "release\xef…", unbound under set -u (v0.2.49's install.sh stopped
// there on every Mac). Every $name before such a character is written ${name}.
func TestShNamesBeforeWideChars(t *testing.T) {
	b, err := files.ReadFile("install.sh")
	if err != nil {
		t.Fatal(err)
	}
	for n, line := range strings.Split(string(b), "\n") {
		for i := 0; i < len(line); i++ {
			if line[i] != '$' {
				continue
			}
			j := i + 1
			for j < len(line) && (line[j] == '_' || line[j] >= 'a' && line[j] <= 'z' || line[j] >= 'A' && line[j] <= 'Z' || line[j] >= '0' && line[j] <= '9') {
				j++
			}
			if j > i+1 && j < len(line) && line[j] >= 0x80 {
				t.Errorf("install.sh:%d: %s stands before a character past ASCII: write ${…}", n+1, line[i:j])
			}
		}
	}
}
