package public

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// The test binary is also a fake cloudflared, through a link of that name.
func TestMain(m *testing.M) {
	if filepath.Base(os.Args[0]) == "cloudflared" {
		os.Exit(fakeCloudflared(os.Args[1:]))
	}
	os.Exit(m.Run())
}

// fakeCloudflared does what ir public asks of cloudflared, in $FAKE_CF_STATE, logging each call
// to $FAKE_CF_STATE/calls. $FAKE_CF_DNS_EXISTS: the host has a record already.
func fakeCloudflared(args []string) int {
	state := os.Getenv("FAKE_CF_STATE")
	if f, err := os.OpenFile(filepath.Join(state, "calls"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
		fmt.Fprintln(f, strings.Join(args, " "))
		f.Close()
	}
	flag := func(name string) string {
		for i, a := range args {
			if a == name && i+1 < len(args) {
				return args[i+1]
			}
		}
		return ""
	}
	has := func(name string) bool { return slices.Contains(args, name) }
	last := args[len(args)-1]
	tunnels := map[string]string{} // name → id
	tf := filepath.Join(state, "tunnels.json")
	if b, err := os.ReadFile(tf); err == nil {
		json.Unmarshal(b, &tunnels)
	}
	save := func() { b, _ := json.Marshal(tunnels); os.WriteFile(tf, b, 0o600) }
	needCert := func() bool {
		if _, err := os.Stat(flag("--origincert")); err != nil {
			fmt.Fprintln(os.Stderr, "Cannot determine default origin certificate path")
			return false
		}
		return true
	}
	switch {
	case has("--version"):
		fmt.Println("cloudflared version 2026.10.0 (fake)")
	case has("login"):
		fmt.Println("Please open the following URL and log in with your Cloudflare account:\n\nhttps://dash.cloudflare.com/argotunnel?aud=&callback=https%3A%2F%2Flogin.cloudflareaccess.org%2Ffake")
		dir := filepath.Join(os.Getenv("HOME"), ".cloudflared")
		os.MkdirAll(dir, 0o700)
		tok, _ := json.Marshal(map[string]string{"zoneID": "z1", "accountID": "a1", "apiToken": "cert-api-token"})
		os.WriteFile(filepath.Join(dir, "cert.pem"), pem.EncodeToMemory(&pem.Block{Type: "ARGO TUNNEL TOKEN", Bytes: tok}), 0o600)
	case has("create"):
		if !needCert() {
			return 1
		}
		if _, ok := tunnels[last]; ok {
			fmt.Fprintln(os.Stderr, "failed to create tunnel: Create Tunnel API call failed: tunnel with name already exists")
			return 1
		}
		id := fmt.Sprintf("0000000%d-0000-4000-8000-000000000000", len(tunnels)+1)
		tunnels[last] = id
		save()
		os.WriteFile(flag("--credentials-file"), []byte(`{"AccountTag":"a1","TunnelID":"`+id+`","TunnelSecret":"c2VjcmV0"}`), 0o400)
		fmt.Printf(`{"id":"%s","name":"%s","created_at":"2026-11-05T00:00:00Z","connections":[]}`+"\n", id, last)
	case has("list"):
		if !needCert() {
			return 1
		}
		fmt.Printf(`[{"id":"%s","name":"%s"}]`+"\n", tunnels[flag("--name")], flag("--name"))
	case has("token"):
		os.WriteFile(flag("--cred-file"), []byte(`{"TunnelID":"`+last+`"}`), 0o400)
	case has("route"):
		if !needCert() {
			return 1
		}
		if os.Getenv("FAKE_CF_DNS_EXISTS") != "" && !has("--overwrite-dns") {
			fmt.Fprintf(os.Stderr, "Failed to add route: code: 1003, reason: Failed to create record %s with err An A, AAAA, or CNAME record with that host already exists.\n", last)
			return 1
		}
		fmt.Printf("Added CNAME %s which will route to this tunnel tunnelID=%s\n", last, args[len(args)-2])
	case has("validate"):
		b, err := os.ReadFile(flag("--config"))
		if err != nil || !strings.Contains(string(b), "service: http_status:404") {
			fmt.Fprintln(os.Stderr, "Validation failed")
			return 1
		}
		fmt.Println("OK")
	case has("delete"):
		for n, id := range tunnels {
			if id == last {
				delete(tunnels, n)
			}
		}
		save()
	default:
		fmt.Fprintln(os.Stderr, "fake cloudflared: what is", args)
		return 2
	}
	return 0
}

type fakeDaemon struct {
	mu      sync.Mutex
	origins []string
}

func (d *fakeDaemon) SetOrigin(_ context.Context, o string) error {
	d.mu.Lock()
	d.origins = append(d.origins, o)
	d.mu.Unlock()
	return nil
}

func (d *fakeDaemon) Invite(context.Context) (string, time.Time, error) {
	return "https://ir-test.example.com/_ir/join#INVITE-SECRET", time.Now().Add(10 * time.Minute), nil
}

// rig: the fake cloudflared in bin, Cloudflare's API and the public host served by one fake.
type rig struct {
	t       *testing.T
	o       Options
	out     *bytes.Buffer
	answers []string
	asked   []string
	units   [][]string
	daemon  *fakeDaemon
	state   string
	home    string
	health  int // health checks so far
}

func newRig(t *testing.T) *rig {
	root := t.TempDir()
	r := &rig{t: t, out: &bytes.Buffer{}, daemon: &fakeDaemon{}, state: filepath.Join(root, "fake"), home: filepath.Join(root, "home")}
	os.MkdirAll(r.state, 0o700)
	os.MkdirAll(r.home, 0o700)
	t.Setenv("FAKE_CF_STATE", r.state)
	bin := filepath.Join(root, "bin")
	os.MkdirAll(bin, 0o755)
	if err := os.Symlink(os.Args[0], filepath.Join(bin, "cloudflared")); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		host := req.Header.Get("X-Test-Host")
		switch {
		case host == "api.cloudflare.com" && req.URL.Path == "/client/v4/zones/z1":
			if req.Header.Get("Authorization") != "Bearer cert-api-token" {
				http.Error(w, "no", 403)
				return
			}
			fmt.Fprint(w, `{"success":true,"result":{"id":"z1","name":"Example.com"}}`)
		case req.URL.Path == "/_ir/health" && strings.HasSuffix(host, ".example.com"):
			mu.Lock()
			r.health++
			n := r.health
			mu.Unlock()
			if n < 3 { // DNS takes a moment
				http.Error(w, "origin unreachable", 530)
				return
			}
			fmt.Fprint(w, `{"ok":true}`)
		default:
			http.NotFound(w, req)
		}
	}))
	t.Cleanup(srv.Close)
	target, _ := url.Parse(srv.URL)
	client := &http.Client{Transport: roundTrip(func(req *http.Request) (*http.Response, error) {
		req = req.Clone(req.Context())
		req.Header.Set("X-Test-Host", req.URL.Hostname())
		req.URL.Scheme, req.URL.Host = target.Scheme, target.Host
		return http.DefaultTransport.RoundTrip(req)
	})}
	r.o = Options{
		Dir: filepath.Join(root, "state", "cloudflared"), BinDir: bin, Path: filepath.Join(root, "nothing"),
		UnitDir: filepath.Join(root, "config", "systemd", "user"), Unit: "iridium-test-cloudflared",
		Port: 7810, Machine: "Kumo2.local", Home: r.home, HTTP: client,
		Systemctl: func(_ context.Context, args ...string) error { r.units = append(r.units, args); return nil },
		Ask: func(q, def string) (string, error) {
			r.asked = append(r.asked, q)
			if len(r.answers) == 0 {
				return def, nil
			}
			a := r.answers[0]
			r.answers = r.answers[1:]
			return a, nil
		},
		Out: r.out, Daemon: r.daemon, HealthWait: 5 * time.Second, HealthEvery: 10 * time.Millisecond,
	}
	return r
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func (r *rig) calls() string { b, _ := os.ReadFile(filepath.Join(r.state, "calls")); return string(b) }

func TestOnAndOff(t *testing.T) {
	r := newRig(t)
	r.answers = []string{"ir-test"}
	if err := On(context.Background(), r.o); err != nil {
		t.Fatalf("on: %v\n%s", err, r.out)
	}
	out := r.out.String()
	for _, want := range []string{"https://dash.cloudflare.com/argotunnel", "地址：https://ir-test.example.com", "iridium-kumo2-local", "通了", "https://ir-test.example.com/_ir/join#INVITE-SECRET"} {
		if !strings.Contains(out, want) {
			t.Errorf("the output has no %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "cert-api-token") {
		t.Error("the login's API token was printed")
	}
	// The login went to Iridium's own directory, not the user's ~/.cloudflared.
	if _, err := os.Stat(filepath.Join(r.o.Dir, ".cloudflared", "cert.pem")); err != nil {
		t.Errorf("no cert in Iridium's directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(r.home, ".cloudflared")); err == nil {
		t.Error("the user's ~/.cloudflared was written")
	}
	cfg, _ := os.ReadFile(filepath.Join(r.o.Dir, "config.yml"))
	if !strings.Contains(string(cfg), "hostname: ir-test.example.com\n    service: http://127.0.0.1:7810\n  - service: http_status:404") ||
		!strings.Contains(string(cfg), "tunnel: 00000001-") || !strings.Contains(string(cfg), "credentials-file: "+filepath.Join(r.o.Dir, "tunnel.json")) {
		t.Errorf("config:\n%s", cfg)
	}
	unit, _ := os.ReadFile(filepath.Join(r.o.UnitDir, "iridium-test-cloudflared.service"))
	if !strings.Contains(string(unit), "ExecStart="+filepath.Join(r.o.BinDir, "cloudflared")+" tunnel --no-autoupdate --config "+filepath.Join(r.o.Dir, "config.yml")+" run") ||
		!strings.Contains(string(unit), "WantedBy=default.target") {
		t.Errorf("unit:\n%s", unit)
	}
	if fmt.Sprint(r.units) != "[[daemon-reload] [enable iridium-test-cloudflared.service] [restart iridium-test-cloudflared.service]]" {
		t.Errorf("systemctl: %v", r.units)
	}
	if fmt.Sprint(r.daemon.origins) != "[https://ir-test.example.com]" {
		t.Errorf("origins %v", r.daemon.origins)
	}
	calls := r.calls()
	for _, want := range []string{"tunnel login", "create --credentials-file", "route dns 00000001-0000-4000-8000-000000000000 ir-test.example.com", "ingress validate"} {
		if !strings.Contains(calls, want) {
			t.Errorf("no call %q:\n%s", want, calls)
		}
	}
	if strings.Contains(calls, "/etc/cloudflared") {
		t.Error("touched /etc/cloudflared")
	}

	// Again: the login and the tunnel are reused.
	r.answers = []string{"n", "ir-test"}
	r.units = nil
	if err := On(context.Background(), r.o); err != nil {
		t.Fatalf("on again: %v\n%s", err, r.out)
	}
	if strings.Count(r.calls(), "tunnel login") != 1 || strings.Count(r.calls(), " create ") != 1 {
		t.Errorf("not reused:\n%s", r.calls())
	}

	// Off.
	r.out.Reset()
	r.units = nil
	if err := Off(context.Background(), r.o); err != nil {
		t.Fatalf("off: %v", err)
	}
	if fmt.Sprint(r.units) != "[[disable --now iridium-test-cloudflared.service] [daemon-reload]]" {
		t.Errorf("systemctl: %v", r.units)
	}
	if _, err := os.Stat(filepath.Join(r.o.UnitDir, "iridium-test-cloudflared.service")); !os.IsNotExist(err) {
		t.Error("the unit stayed")
	}
	if r.daemon.origins[len(r.daemon.origins)-1] != "" || !strings.Contains(r.calls(), "delete -f 00000001-") {
		t.Errorf("off: %v\n%s", r.daemon.origins, r.calls())
	}
	if !strings.Contains(r.out.String(), "DNS 记录 ir-test.example.com 还在") {
		t.Errorf("no word on the DNS record:\n%s", r.out)
	}
	for _, f := range []string{"tunnel.json", "config.yml", "public.json"} {
		if _, err := os.Stat(filepath.Join(r.o.Dir, f)); !os.IsNotExist(err) {
			t.Errorf("%s stayed", f)
		}
	}
}

// A record already there is replaced only when you say so; a tunnel of the name made elsewhere
// is taken up with fresh credentials.
func TestExistingRecordAndTunnel(t *testing.T) {
	r := newRig(t)
	t.Setenv("FAKE_CF_DNS_EXISTS", "1")
	os.WriteFile(filepath.Join(r.state, "tunnels.json"), []byte(`{"iridium-kumo2-local":"aaaaaaaa-0000-4000-8000-000000000000"}`), 0o600)
	r.answers = []string{"", "n"} // the default subdomain, then no
	err := On(context.Background(), r.o)
	if err == nil || !strings.Contains(err.Error(), "已经有别的记录") {
		t.Fatalf("no overwrite: %v\n%s", err, r.out)
	}
	if !strings.Contains(strings.Join(r.asked, "\n"), "ir.example.com 已经有 DNS 记录") || len(r.daemon.origins) != 0 {
		t.Fatalf("asked %v, origins %v", r.asked, r.daemon.origins)
	}
	if !strings.Contains(r.calls(), "token --cred-file") || !strings.Contains(r.calls(), "list -o json --name iridium-kumo2-local") {
		t.Errorf("the tunnel there was not taken up:\n%s", r.calls())
	}
	r.answers = []string{"n", "ir", "y"}
	if err := On(context.Background(), r.o); err != nil {
		t.Fatalf("overwrite: %v\n%s", err, r.out)
	}
	if !strings.Contains(r.calls(), "route dns --overwrite-dns aaaaaaaa-0000-4000-8000-000000000000 ir.example.com") {
		t.Errorf("no overwrite:\n%s", r.calls())
	}
}

// Without a domain on Cloudflare the login gives no cert: stop and say what is missing.
func TestNoZone(t *testing.T) {
	r := newRig(t)
	failing := filepath.Join(t.TempDir(), "bin")
	os.MkdirAll(failing, 0o755)
	os.WriteFile(filepath.Join(failing, "cloudflared"), []byte("#!/bin/sh\ncase \"$*\" in *--version*) echo cloudflared version 1;; *) echo 'no zones' >&2; exit 1;; esac\n"), 0o755)
	r.o.BinDir = failing
	err := On(context.Background(), r.o)
	if err == nil || !strings.Contains(err.Error(), "托管在 Cloudflare 上的域名") {
		t.Fatalf("no zone: %v", err)
	}
}

func TestDownload(t *testing.T) {
	bin := []byte("#!/bin/sh\necho 'cloudflared version 2026.10.0 (built 2026-10-05)'\n")
	sum := sha256.Sum256(bin)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	notes := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/cloudflare/cloudflared/releases/latest":
			json.NewEncoder(w).Encode(map[string]any{"tag_name": "2026.10.0", "body": notes, "assets": []any{
				map[string]any{"name": AssetName(), "browser_download_url": "http://" + r.Host + "/dl", "digest": digest},
				map[string]any{"name": "cloudflared-windows-amd64.exe", "browser_download_url": "http://" + r.Host + "/no"},
			}})
		case "/dl":
			w.Write(bin)
		}
	}))
	defer srv.Close()
	dir := t.TempDir()
	o := Options{BinDir: filepath.Join(dir, "bin"), Path: filepath.Join(dir, "none"), GitHub: srv.URL, Out: &bytes.Buffer{}}
	o.defaults()
	// A wrong digest: nothing installed.
	digest = "sha256:" + strings.Repeat("ab", 32)
	if _, err := o.cloudflared(context.Background()); err == nil || !strings.Contains(err.Error(), "sha256") {
		t.Fatalf("bad digest: %v", err)
	}
	if left, _ := os.ReadDir(o.BinDir); len(left) != 0 {
		t.Fatalf("left %v", left)
	}
	// No digest, no checksum in the notes: not downloaded.
	digest = ""
	if _, err := o.cloudflared(context.Background()); err == nil || !strings.Contains(err.Error(), "no sha256") {
		t.Fatalf("nothing to check: %v", err)
	}
	// The checksum the notes list.
	notes = "SHA256 Checksums:\n```\n" + AssetName() + ": " + hex.EncodeToString(sum[:]) + "\ncloudflared-windows-amd64.exe: " + strings.Repeat("0", 64) + "\n```"
	p, err := o.cloudflared(context.Background())
	if err != nil || p != filepath.Join(o.BinDir, "cloudflared") {
		t.Fatalf("download: %q %v", p, err)
	}
	// Found there the next time, without a download.
	srv.Close()
	if p2, err := o.cloudflared(context.Background()); err != nil || p2 != p {
		t.Fatalf("found: %q %v", p2, err)
	}
}

func TestNames(t *testing.T) {
	for in, want := range map[string]string{"kumo2": "iridium-kumo2", "Kumo2.local": "iridium-kumo2-local", "x_y": "iridium-x-y", "": "iridium-host", "--": "iridium-host"} {
		if got := TunnelName(in); got != want {
			t.Errorf("TunnelName(%q) = %q", in, got)
		}
	}
	if q := systemdQuote("/home/a b/cloudflared"); q != `"/home/a b/cloudflared"` {
		t.Errorf("quote %s", q)
	}
}
