// Package update looks for a newer Iridium and installs it (plan "更新"): once a day it reads a
// manifest from the configured source; Apply downloads the binary, checks its sha256, makes sure
// it runs, puts it in place of the running one and restarts the service. Holders run in their own
// units, so hosted agents keep running through it. GitHub Releases as a source come later.
//
// The manifest is JSON:
//
//	{"version": "1.2.0", "notes": "…", "url": "https://…/iridium-linux-amd64", "sha256": "<hex>",
//	 "platforms": {"linux-arm64": {"url": "…", "sha256": "…"}}}
//
// A platforms entry for this GOOS-GOARCH wins over the top-level url and sha256.
package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
)

// MaxBinary bounds a download.
const MaxBinary = 512 << 20

type Manifest struct {
	Version   string           `json:"version"`
	Notes     string           `json:"notes,omitempty"`
	URL       string           `json:"url,omitempty"`
	SHA256    string           `json:"sha256,omitempty"`
	Platforms map[string]Asset `json:"platforms,omitempty"`
}

type Asset struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}

// Asset is the binary for this platform.
func (m Manifest) Asset() (Asset, error) {
	a, ok := m.Platforms[runtime.GOOS+"-"+runtime.GOARCH]
	if !ok {
		a = Asset{URL: m.URL, SHA256: m.SHA256}
	}
	if a.URL == "" {
		return a, fmt.Errorf("version %s has no binary for %s-%s", m.Version, runtime.GOOS, runtime.GOARCH)
	}
	if b, err := hex.DecodeString(a.SHA256); err != nil || len(b) != sha256.Size {
		return a, fmt.Errorf("version %s: no sha256 to check its binary against", m.Version)
	}
	return a, nil
}

type Options struct {
	Source  string        // the manifest's URL; "" looks for nothing
	Every   time.Duration // 0 is a day
	Current string        // this binary's version
	// Exe is the binary to replace; "" is this process's own (links resolved).
	Exe string
	// Restart runs once the new binary is in place: it restarts the service (or this process).
	Restart func() error
	// Publish gets every new state (the hub's "update").
	Publish func(api.UpdateState)
	Client  *http.Client
	Logf    func(string, ...any)
}

type Updater struct {
	o Options

	applying sync.Mutex // one Apply at a time
	mu       sync.Mutex
	st       api.UpdateState
	latest   *Manifest
}

func New(o Options) *Updater {
	if o.Every <= 0 {
		o.Every = 24 * time.Hour
	}
	if o.Client == nil {
		o.Client = &http.Client{Timeout: 10 * time.Minute}
	}
	if o.Logf == nil {
		o.Logf = func(string, ...any) {}
	}
	if o.Publish == nil {
		o.Publish = func(api.UpdateState) {}
	}
	return &Updater{o: o, st: api.UpdateState{Current: o.Current, Source: o.Source != "", Phase: "idle"}}
}

// State is the update state now.
func (u *Updater) State() api.UpdateState {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.st
}

func (u *Updater) set(f func(*api.UpdateState)) api.UpdateState {
	u.mu.Lock()
	f(&u.st)
	st := u.st
	u.mu.Unlock()
	u.o.Publish(st)
	return st
}

// Run checks a minute after it starts, then every Every, until ctx ends.
func (u *Updater) Run(ctx context.Context) {
	if u.o.Source == "" {
		return
	}
	t := time.NewTimer(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if _, err := u.Check(ctx); err != nil {
			u.o.Logf("update: %v", err)
		}
		t.Reset(u.o.Every)
	}
}

// Check reads the manifest and says whether it is newer than this binary.
func (u *Updater) Check(ctx context.Context) (api.UpdateState, error) {
	if u.o.Source == "" {
		return u.State(), errors.New("no update source configured (update.url)")
	}
	u.set(func(s *api.UpdateState) { s.Phase, s.Error = "checking", "" })
	m, err := u.manifest(ctx)
	if err != nil {
		return u.set(func(s *api.UpdateState) { s.Phase, s.Error = "failed", err.Error() }), err
	}
	u.mu.Lock()
	u.latest = &m
	u.mu.Unlock()
	return u.set(func(s *api.UpdateState) {
		s.Latest, s.Notes, s.Available = m.Version, m.Notes, Newer(m.Version, u.o.Current)
		s.CheckedAt, s.Phase, s.Error = time.Now().UnixMilli(), "idle", ""
	}), nil
}

func (u *Updater) manifest(ctx context.Context) (Manifest, error) {
	var m Manifest
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.o.Source, nil)
	if err != nil {
		return m, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := u.o.Client.Do(req)
	if err != nil {
		return m, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return m, fmt.Errorf("update source: %s", resp.Status)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&m); err != nil {
		return m, fmt.Errorf("update source: %w", err)
	}
	if strings.TrimSpace(m.Version) == "" {
		return m, errors.New("update source: no version")
	}
	return m, nil
}

// ErrUpToDate: nothing newer to install.
var ErrUpToDate = errors.New("already the latest version")

// Apply installs the latest version: download, check the sha256, run it once (`version`), put it
// in place, restart. Unless force, only a newer one.
func (u *Updater) Apply(ctx context.Context, force bool) (api.UpdateState, error) {
	if !u.applying.TryLock() {
		return u.State(), errors.New("an update is already being installed")
	}
	defer u.applying.Unlock()
	if _, err := u.Check(ctx); err != nil {
		return u.State(), err
	}
	u.mu.Lock()
	m := *u.latest
	u.mu.Unlock()
	if !force && !Newer(m.Version, u.o.Current) {
		return u.State(), ErrUpToDate
	}
	fail := func(err error) (api.UpdateState, error) {
		u.o.Logf("update: %v", err)
		return u.set(func(s *api.UpdateState) { s.Phase, s.Error = "failed", err.Error() }), err
	}
	asset, err := m.Asset()
	if err != nil {
		return fail(err)
	}
	exe := u.o.Exe
	if exe == "" {
		if exe, err = os.Executable(); err == nil {
			exe, err = filepath.EvalSymlinks(exe)
		}
		if err != nil {
			return fail(err)
		}
	}
	u.set(func(s *api.UpdateState) { s.Phase = "downloading" })
	tmp, err := u.download(ctx, asset, filepath.Dir(exe))
	if err != nil {
		return fail(err)
	}
	defer os.Remove(tmp) // gone after the rename
	if err := runs(ctx, tmp, m.Version); err != nil {
		return fail(err)
	}
	if err := os.Rename(tmp, exe); err != nil {
		return fail(err)
	}
	u.o.Logf("update: %s installed over %s at %s", m.Version, u.o.Current, exe)
	st := u.set(func(s *api.UpdateState) { s.Phase, s.Available = "restarting", false })
	if u.o.Restart != nil {
		if err := u.o.Restart(); err != nil {
			return fail(fmt.Errorf("installed %s, but the restart failed: %w", m.Version, err))
		}
	}
	return st, nil
}

// download writes the asset to a temp file in dir (so the rename stays on one file system) and
// gives its path once its sha256 matched.
func (u *Updater) download(ctx context.Context, a Asset, dir string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
	if err != nil {
		return "", err
	}
	resp, err := u.o.Client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: %s", a.URL, resp.Status)
	}
	f, err := os.CreateTemp(dir, ".iridium-update-*")
	if err != nil {
		return "", err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, MaxBinary+1))
	if err == nil && n > MaxBinary {
		err = errors.New("the download is too large")
	}
	if err == nil {
		err = f.Chmod(0o755)
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), a.SHA256) {
		err = fmt.Errorf("the download's sha256 is %x, not %s", h.Sum(nil), a.SHA256)
	}
	if err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// runs checks that the new binary runs here and is the version it says.
func runs(ctx context.Context, path, version string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "version").Output()
	if err != nil {
		return fmt.Errorf("the new binary does not run: %w", err)
	}
	if !strings.Contains(string(out), strings.TrimPrefix(version, "v")) {
		return fmt.Errorf("the new binary says it is %q, not %s", strings.TrimSpace(string(out)), version)
	}
	return nil
}

// Newer reports whether version a is newer than b: dotted numbers, an optional leading v, and a
// pre-release (-rc.1) older than its release. A b that is no such version (dev) is never older.
func Newer(a, b string) bool {
	pa, oka := parse(a)
	pb, okb := parse(b)
	if !oka || !okb {
		return false
	}
	for i := range max(len(pa.nums), len(pb.nums)) {
		x, y := at(pa.nums, i), at(pb.nums, i)
		if x != y {
			return x > y
		}
	}
	switch {
	case pa.pre == pb.pre:
		return false
	case pa.pre == "":
		return true
	case pb.pre == "":
		return false
	}
	return comparePre(pa.pre, pb.pre) > 0
}

type ver struct {
	nums []int
	pre  string
}

func parse(s string) (ver, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	s, _, _ = strings.Cut(s, "+")
	core, pre, _ := strings.Cut(s, "-")
	var v ver
	for _, p := range strings.Split(core, ".") {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return v, false
		}
		v.nums = append(v.nums, n)
	}
	v.pre = pre
	return v, len(v.nums) > 0
}

func at(a []int, i int) int {
	if i < len(a) {
		return a[i]
	}
	return 0
}

// comparePre compares pre-releases field by field, numbers as numbers.
func comparePre(a, b string) int {
	fa, fb := strings.Split(a, "."), strings.Split(b, ".")
	for i := range max(len(fa), len(fb)) {
		if i >= len(fa) {
			return -1
		}
		if i >= len(fb) {
			return 1
		}
		x, errx := strconv.Atoi(fa[i])
		y, erry := strconv.Atoi(fb[i])
		switch {
		case errx == nil && erry == nil && x != y:
			if x > y {
				return 1
			}
			return -1
		case (errx == nil) != (erry == nil):
			if errx == nil {
				return -1 // numbers sort before words
			}
			return 1
		case errx != nil && fa[i] != fb[i]:
			return strings.Compare(fa[i], fb[i])
		}
	}
	return 0
}
