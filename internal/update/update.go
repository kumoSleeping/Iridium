// Package update looks for a newer Iridium and installs it (plan "更新"): once a day it reads the
// latest release from the configured source; with auto download on it fetches the new binary
// ahead (checked against its sha256 and run once), so Apply only puts it in place of the running
// one and restarts the service. Holders run in their own units, so hosted agents keep running.
//
// A source is a GitHub repository ("github:owner/repo", or https://github.com/owner/repo), whose
// latest release has an asset iridium-<GOOS>-<GOARCH>.tar.gz (the binary named iridium inside) or
// iridium-<GOOS>-<GOARCH>; its sha256 is the asset's digest, else the line for it in the release's
// SHA256SUMS. Any other URL answers a manifest, JSON:
//
//	{"version": "1.2.0", "notes": "…", "url": "https://…/iridium-linux-amd64", "sha256": "<hex>",
//	 "platforms": {"linux-arm64": {"url": "…", "sha256": "…"}}}
//
// A platforms entry for this GOOS-GOARCH wins over the top-level url and sha256.
package update

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
)

// DefaultSource is where a build looks when the config names none.
const DefaultSource = "github:kumoSleeping/Iridium"

// NotesEnd ends what a release's notes say to the settings page; after it (the sizes) is for
// the release page only (scripts/release.sh).
const NotesEnd = "<!-- iridium:assets -->"

// MaxBinary bounds a download.
const MaxBinary = 512 << 20

// staging names a downloaded binary waiting in the executable's directory.
const staging = ".iridium-update-"

type Manifest struct {
	Version   string           `json:"version"`
	Notes     string           `json:"notes,omitempty"`
	URL       string           `json:"url,omitempty"`
	SHA256    string           `json:"sha256,omitempty"`
	Size      int64            `json:"size,omitempty"`
	Platforms map[string]Asset `json:"platforms,omitempty"`
}

type Asset struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size,omitempty"`
	// API: a GitHub release asset's API URL, the same bytes by another way (Accept:
	// application/octet-stream), tried when URL keeps failing: github.com's release downloads
	// answered 504 for days while the API served (2026-11-08).
	API string `json:"-"`
}

// Asset is the binary for this platform.
func (m Manifest) Asset() (Asset, error) {
	a, ok := m.Platforms[runtime.GOOS+"-"+runtime.GOARCH]
	if !ok {
		a = Asset{URL: m.URL, SHA256: m.SHA256, Size: m.Size}
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
	Source  string        // a GitHub repository or a manifest's URL; "" looks for nothing
	Every   time.Duration // 0 is a day
	Current string        // this binary's version
	// Auto: download a newer version as soon as it is seen; Apply then only swaps and restarts.
	Auto bool
	// Exe is the binary to replace; "" is this process's own (links resolved).
	Exe string
	// Restart runs once the new binary is in place: it restarts the service (or this process).
	Restart func() error
	// Publish gets every new state (the hub's "update").
	Publish func(api.UpdateState)
	Client  *http.Client
	// GitHubAPI is the API's base; "" is https://api.github.com.
	GitHubAPI string
	Logf      func(string, ...any)
}

type Updater struct {
	o Options

	applying sync.Mutex // one Apply at a time
	prep     sync.Mutex // one download at a time
	mu       sync.Mutex
	st       api.UpdateState
	latest   *Manifest
	auto     bool
	staged   string // the path of the downloaded st.Latest, "" for none
	stagedV  string
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
	if o.GitHubAPI == "" {
		o.GitHubAPI = "https://api.github.com"
	}
	return &Updater{o: o, auto: o.Auto, st: api.UpdateState{Current: o.Current, Source: o.Source != "", Auto: o.Auto, Phase: "idle"}}
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

// Run drops what an earlier run left staged, checks a minute after it starts, then every Every,
// until ctx ends.
func (u *Updater) Run(ctx context.Context) {
	if u.o.Source == "" {
		return
	}
	if exe, err := u.exe(); err == nil {
		old, _ := filepath.Glob(filepath.Join(filepath.Dir(exe), staging+"*"))
		for _, p := range old {
			os.Remove(p)
		}
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

// Check reads the latest release and says whether it is newer than this binary; with auto
// download on, a newer one starts downloading.
func (u *Updater) Check(ctx context.Context) (api.UpdateState, error) {
	if u.o.Source == "" {
		return u.State(), errors.New("no update source configured (update.url)")
	}
	u.set(func(s *api.UpdateState) {
		if s.Phase != "downloading" {
			s.Phase = "checking"
		}
		s.Error = ""
	})
	m, err := u.manifest(ctx)
	if err != nil {
		return u.set(func(s *api.UpdateState) {
			if s.Phase != "downloading" {
				s.Phase = "failed"
			}
			s.Error = err.Error()
		}), err
	}
	var size int64
	if a, err := m.Asset(); err == nil {
		size = a.Size
	}
	u.mu.Lock()
	u.latest = &m
	u.mu.Unlock()
	st := u.set(func(s *api.UpdateState) {
		s.Latest, s.Notes, s.Size, s.Available = m.Version, m.Notes, size, Newer(m.Version, u.o.Current)
		s.Ready = s.Available && u.stagedV == m.Version && u.staged != ""
		s.CheckedAt, s.Phase, s.Error = time.Now().UnixMilli(), u.idle(), ""
	})
	u.fetchAhead()
	return st, nil
}

// idle is the phase with nothing asked: "downloading" while one runs ahead. With u.mu held.
func (u *Updater) idle() string {
	if u.st.Phase == "downloading" {
		return "downloading"
	}
	return "idle"
}

// fetchAhead downloads the latest in the background, if it is newer, auto download is on and it
// is not already here or on its way.
func (u *Updater) fetchAhead() {
	u.mu.Lock()
	m, want := u.latest, u.auto && u.latest != nil && Newer(u.latest.Version, u.o.Current) && u.stagedV != u.latest.Version
	u.mu.Unlock()
	if !want || !u.prep.TryLock() {
		return
	}
	go func() {
		defer u.prep.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		if _, err := u.stageLocked(ctx, *m); err != nil {
			u.o.Logf("update: download %s: %v", m.Version, err)
		}
	}()
}

func (u *Updater) manifest(ctx context.Context) (Manifest, error) {
	if repo, ok := GitHubRepo(u.o.Source); ok {
		return u.release(ctx, repo)
	}
	var m Manifest
	if err := u.getJSON(ctx, u.o.Source, "application/json", &m); err != nil {
		return m, fmt.Errorf("update source: %w", err)
	}
	if strings.TrimSpace(m.Version) == "" {
		return m, errors.New("update source: no version")
	}
	return m, nil
}

func (u *Updater) getJSON(ctx context.Context, url, accept string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("User-Agent", "iridium/"+u.o.Current)
	resp, err := u.o.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errors.New(resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(v)
}

// GitHubRepo is the "owner/repo" a source names, if it is a GitHub repository.
func GitHubRepo(source string) (string, bool) {
	s := strings.TrimSpace(source)
	if r, ok := strings.CutPrefix(s, "github:"); ok {
		s = r
	} else if r, ok := strings.CutPrefix(s, "https://github.com/"); ok {
		s = r
	} else {
		return "", false
	}
	s = strings.TrimSuffix(strings.TrimSuffix(s, "/"), ".git")
	s, _, _ = strings.Cut(s, "/releases")
	parts := strings.Split(s, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", false
	}
	return parts[0] + "/" + parts[1], true
}

type release struct {
	Tag    string `json:"tag_name"`
	Body   string `json:"body"`
	Assets []struct {
		Name   string `json:"name"`
		URL    string `json:"browser_download_url"`
		API    string `json:"url"` // the asset's API URL
		Size   int64  `json:"size"`
		Digest string `json:"digest"` // "sha256:<hex>"
	} `json:"assets"`
}

// release reads the repository's latest release as a manifest.
func (u *Updater) release(ctx context.Context, repo string) (Manifest, error) {
	var r release
	api := strings.TrimSuffix(u.o.GitHubAPI, "/") + "/repos/" + repo + "/releases/latest"
	if err := u.getJSON(ctx, api, "application/vnd.github+json", &r); err != nil {
		return Manifest{}, fmt.Errorf("github %s: latest release: %w", repo, err)
	}
	notes, _, _ := strings.Cut(r.Body, NotesEnd)
	m := Manifest{Version: r.Tag, Notes: strings.TrimSpace(notes), Platforms: map[string]Asset{}}
	if strings.TrimSpace(m.Version) == "" {
		return m, fmt.Errorf("github %s: the latest release has no tag", repo)
	}
	var sums Asset
	for _, a := range r.Assets {
		if a.Name == "SHA256SUMS" || a.Name == "SHA256SUMS.txt" {
			sums = Asset{URL: a.URL, API: a.API}
		}
	}
	var listed map[string]string
	for _, a := range r.Assets {
		plat, ok := assetPlatform(a.Name)
		if !ok {
			continue
		}
		if old, dup := m.Platforms[plat]; dup && strings.HasSuffix(old.URL, ".tar.gz") {
			continue // the archive is the smaller one
		}
		sum, _ := strings.CutPrefix(a.Digest, "sha256:")
		if a.Digest == "" && sums.URL != "" {
			if listed == nil {
				if listed, _ = u.sums(ctx, sums); listed == nil {
					listed = map[string]string{}
				}
			}
			sum = listed[a.Name]
		}
		m.Platforms[plat] = Asset{URL: a.URL, SHA256: sum, Size: a.Size, API: a.API}
	}
	return m, nil
}

// Extension downloads the binary bin (an official plugin, iridium-qq) of release tag into a new
// file in dir: the asset bin-<GOOS>-<GOARCH>.tar.gz, checked against its digest or SHA256SUMS,
// run once (bin version). The caller renames it into place.
func (u *Updater) Extension(ctx context.Context, bin, tag, dir string) (string, error) {
	repo, ok := GitHubRepo(u.o.Source)
	if !ok {
		return "", errors.New("no GitHub repository to download from")
	}
	var r release
	api := strings.TrimSuffix(u.o.GitHubAPI, "/") + "/repos/" + repo + "/releases/tags/" + url.PathEscape(tag)
	if err := u.getJSON(ctx, api, "application/vnd.github+json", &r); err != nil {
		return "", fmt.Errorf("github %s: release %s: %w", repo, tag, err)
	}
	want := bin + "-" + runtime.GOOS + "-" + runtime.GOARCH + ".tar.gz"
	var a *Asset
	var sums Asset
	for _, x := range r.Assets {
		switch x.Name {
		case want:
			sum, _ := strings.CutPrefix(x.Digest, "sha256:")
			a = &Asset{URL: x.URL, SHA256: sum, Size: x.Size, API: x.API}
		case "SHA256SUMS", "SHA256SUMS.txt":
			sums = Asset{URL: x.URL, API: x.API}
		}
	}
	if a == nil {
		return "", fmt.Errorf("release %s has no %s", tag, want)
	}
	if a.SHA256 == "" && sums.URL != "" {
		listed, err := u.sums(ctx, sums)
		if err != nil {
			return "", err
		}
		a.SHA256 = listed[want]
	}
	if a.SHA256 == "" {
		return "", fmt.Errorf("release %s: no sha256 for %s", tag, want)
	}
	tmp, err := u.fetch(ctx, *a, dir, bin, false)
	if err != nil {
		return "", err
	}
	if err := runs(ctx, tmp, tag); err != nil {
		os.Remove(tmp)
		return "", err
	}
	return tmp, nil
}

// assetPlatform is the GOOS-GOARCH of iridium-<GOOS>-<GOARCH>[.tar.gz].
func assetPlatform(name string) (string, bool) {
	rest, ok := strings.CutPrefix(name, "iridium-")
	if !ok {
		return "", false
	}
	rest = strings.TrimSuffix(rest, ".tar.gz")
	if strings.ContainsAny(rest, ".") || strings.Count(rest, "-") != 1 {
		return "", false
	}
	return rest, true
}

// sums reads a SHA256SUMS: "<hex>  <name>" lines.
func (u *Updater) sums(ctx context.Context, a Asset) (map[string]string, error) {
	resp, err := u.get(ctx, a)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	out := map[string]string{}
	sc := bufio.NewScanner(io.LimitReader(resp.Body, 1<<20))
	for sc.Scan() {
		if f := strings.Fields(sc.Text()); len(f) == 2 {
			out[strings.TrimPrefix(f[1], "*")] = f[0]
		}
	}
	return out, sc.Err()
}

// ErrUpToDate: nothing newer to install.
var ErrUpToDate = errors.New("already the latest version")

// Apply installs the latest version: the one downloaded ahead, or download it now (check the
// sha256, run it once), put it in place, restart. Unless force, only a newer one.
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
	exe, err := u.exe()
	if err != nil {
		return fail(err)
	}
	u.prep.Lock() // waits for a download ahead
	bin, err := u.stageLocked(ctx, m)
	u.prep.Unlock()
	if err != nil {
		return u.State(), err
	}
	if err := keepPrev(exe); err != nil {
		return fail(fmt.Errorf("keeping the binary before: %w", err))
	}
	// A new file renamed over the old, never written into it: the running binary keeps its inode
	// (macOS kills a process whose signed binary changes under it).
	if err := os.Rename(bin, exe); err != nil {
		return fail(err)
	}
	u.mu.Lock()
	u.staged, u.stagedV = "", ""
	u.mu.Unlock()
	u.o.Logf("update: %s installed over %s at %s", m.Version, u.o.Current, exe)
	st := u.set(func(s *api.UpdateState) { s.Phase, s.Available, s.Ready = "restarting", false, false })
	if u.o.Restart != nil {
		if err := u.o.Restart(); err != nil {
			return fail(fmt.Errorf("installed %s, but the restart failed: %w", m.Version, err))
		}
	}
	return st, nil
}

// Prev is where Apply keeps the binary it replaced: beside it, named <exe>.prev (iridium.prev), a
// way back together with the database's copy (store: <db>.v<N>.bak).
func Prev(exe string) string { return exe + ".prev" }

// keepPrev makes exe's present file Prev(exe), in place of an older one: a hard link to the same
// inode (the rename over exe then leaves it there), else a copy. Made under another name and
// renamed, so a failure leaves the older one whole.
func keepPrev(exe string) error {
	prev := Prev(exe)
	tmp := prev + ".tmp"
	os.Remove(tmp)
	if err := os.Link(exe, tmp); err != nil {
		if err := copyFile(exe, tmp); err != nil {
			os.Remove(tmp)
			return err
		}
	}
	if err := os.Rename(tmp, prev); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func copyFile(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	fi, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fi.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func (u *Updater) exe() (string, error) {
	if u.o.Exe != "" {
		return u.o.Exe, nil
	}
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	return exe, err
}

// stageLocked gives the path of m's binary next to the executable, checked and run once:
// downloaded already, or now. With u.prep held.
func (u *Updater) stageLocked(ctx context.Context, m Manifest) (string, error) {
	u.mu.Lock()
	if u.stagedV == m.Version && u.staged != "" {
		p := u.staged
		u.mu.Unlock()
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
		u.mu.Lock()
		u.staged, u.stagedV = "", ""
	}
	old := u.staged
	u.mu.Unlock()
	fail := func(err error) (string, error) {
		u.set(func(s *api.UpdateState) { s.Phase, s.Error, s.Got = "failed", err.Error(), 0 })
		return "", err
	}
	asset, err := m.Asset()
	if err != nil {
		return fail(err)
	}
	exe, err := u.exe()
	if err != nil {
		return fail(err)
	}
	u.set(func(s *api.UpdateState) { s.Phase, s.Error, s.Got, s.Size = "downloading", "", 0, asset.Size })
	tmp, err := u.download(ctx, asset, filepath.Dir(exe))
	if err != nil {
		return fail(err)
	}
	if err := runs(ctx, tmp, m.Version); err != nil {
		os.Remove(tmp)
		return fail(err)
	}
	if old != "" {
		os.Remove(old)
	}
	u.mu.Lock()
	u.staged, u.stagedV = tmp, m.Version
	u.mu.Unlock()
	u.set(func(s *api.UpdateState) {
		s.Phase, s.Got = "idle", 0
		s.Ready = s.Latest == m.Version
	})
	return tmp, nil
}

// download writes the asset's binary to a file in dir (so the rename stays on one file system),
// once its sha256 matched; an archive gives the binary named iridium in it.
func (u *Updater) download(ctx context.Context, a Asset, dir string) (string, error) {
	return u.fetch(ctx, a, dir, "iridium", true)
}

// fetch is download for the binary named bin; track: publish the progress (the update's own).
func (u *Updater) fetch(ctx context.Context, a Asset, dir, bin string, track bool) (string, error) {
	resp, err := u.get(ctx, a)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if a.Size == 0 && resp.ContentLength > 0 {
		a.Size = resp.ContentLength
		if track {
			u.set(func(s *api.UpdateState) { s.Size = a.Size })
		}
	}
	f, err := os.CreateTemp(dir, staging+"*")
	if err != nil {
		return "", err
	}
	h := sha256.New()
	var w io.Writer = io.MultiWriter(f, h)
	if track {
		w = io.MultiWriter(f, h, &progress{u: u, size: a.Size})
	}
	n, err := io.Copy(w, io.LimitReader(resp.Body, MaxBinary+1))
	if err == nil && n > MaxBinary {
		err = errors.New("the download is too large")
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), a.SHA256) {
		err = fmt.Errorf("the download's sha256 is %x, not %s", h.Sum(nil), a.SHA256)
	}
	if err == nil && isArchive(a.URL) {
		var out string
		out, err = unpack(f.Name(), dir, bin)
		os.Remove(f.Name())
		if err != nil {
			return "", err
		}
		return out, nil
	}
	if err == nil {
		err = os.Chmod(f.Name(), 0o755)
	}
	if err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// retryWait is the wait before the first retry of a download; it doubles each time.
var retryWait = time.Second

// get opens asset a's download: its URL, again after a moment while the answer is a server's or a
// gateway's failure (5xx, 429) or no answer at all, then the same through its API URL (a GitHub
// release asset's), which may serve while github.com's downloads fail.
func (u *Updater) get(ctx context.Context, a Asset) (*http.Response, error) {
	type way struct{ url, accept string }
	ways := []way{{a.URL, ""}}
	if a.API != "" {
		ways = append(ways, way{a.API, "application/octet-stream"})
	}
	var last error
	for i, w := range ways {
		// Fewer tries where another way follows: a failing gateway may take its time to say so.
		tries := 3
		if i < len(ways)-1 {
			tries = 2
		}
		wait := retryWait
		for try := 0; try < tries; try++ {
			if try > 0 {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(wait):
				}
				wait *= 2
			}
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, w.url, nil)
			if err != nil {
				return nil, err
			}
			req.Header.Set("User-Agent", "iridium/"+u.o.Current)
			if w.accept != "" {
				req.Header.Set("Accept", w.accept)
			}
			resp, err := u.o.Client.Do(req)
			if err != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				last = fmt.Errorf("download %s: %w", w.url, err)
				continue
			}
			if resp.StatusCode == http.StatusOK {
				return resp, nil
			}
			resp.Body.Close()
			last = fmt.Errorf("download %s: %s", w.url, resp.Status)
			if resp.StatusCode < 500 && resp.StatusCode != http.StatusTooManyRequests {
				break // not to be had this way: the next way, if any
			}
			if try < tries-1 {
				u.o.Logf("update: %v, trying again", last)
			} else if i < len(ways)-1 {
				u.o.Logf("update: %v, trying the API", last)
			}
		}
	}
	return nil, last
}

func isArchive(rawURL string) bool {
	p := rawURL
	if pu, err := url.Parse(rawURL); err == nil {
		p = pu.Path
	}
	return strings.HasSuffix(p, ".tar.gz") || strings.HasSuffix(p, ".tgz")
}

// unpack takes the file named bin out of a .tar.gz into a new file in dir.
func unpack(archive, dir, bin string) (string, error) {
	in, err := os.Open(archive)
	if err != nil {
		return "", err
	}
	defer in.Close()
	zr, err := gzip.NewReader(in)
	if err != nil {
		return "", fmt.Errorf("the archive: %w", err)
	}
	tr := tar.NewReader(zr)
	for {
		hd, err := tr.Next()
		if err == io.EOF {
			return "", fmt.Errorf("the archive has no %s in it", bin)
		}
		if err != nil {
			return "", fmt.Errorf("the archive: %w", err)
		}
		if hd.Typeflag != tar.TypeReg || path.Base(hd.Name) != bin {
			continue
		}
		out, err := os.CreateTemp(dir, staging+"*")
		if err != nil {
			return "", err
		}
		n, err := io.Copy(out, io.LimitReader(tr, MaxBinary+1))
		if err == nil && n > MaxBinary {
			err = errors.New("the binary is too large")
		}
		if err == nil {
			err = out.Chmod(0o755)
		}
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			os.Remove(out.Name())
			return "", err
		}
		return out.Name(), nil
	}
}

// progress publishes how much has come, at most every 1%.
type progress struct {
	u         *Updater
	size, got int64
	last      int64
}

func (p *progress) Write(b []byte) (int, error) {
	p.got += int64(len(b))
	if p.size > 0 && (p.got-p.last)*100 >= p.size {
		p.last = p.got
		got := p.got
		p.u.set(func(s *api.UpdateState) { s.Got = got })
	}
	return len(b), nil
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
