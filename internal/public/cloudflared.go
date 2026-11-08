package public

import (
	"archive/tar"
	"compress/gzip"
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
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// cloudflared finds cloudflared (BinDir first, then the PATH) or downloads it into BinDir.
func (o *Options) cloudflared(ctx context.Context) (string, error) {
	path := o.Path
	if path == "" {
		path = os.Getenv("PATH")
	}
	for _, dir := range append([]string{o.BinDir}, filepath.SplitList(path)...) {
		if dir == "" {
			continue
		}
		p := filepath.Join(dir, "cloudflared")
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
			if v, err := cloudflaredVersion(ctx, p); err == nil {
				o.say("cloudflared：%s（%s）", p, v)
				return p, nil
			}
		}
	}
	if o.BinDir == "" {
		return "", errors.New("没有 cloudflared")
	}
	o.say("没有 cloudflared：从 GitHub 下载到 %s……", o.BinDir)
	p, err := o.download(ctx)
	if err != nil {
		return "", fmt.Errorf("下载 cloudflared：%w", err)
	}
	v, _ := cloudflaredVersion(ctx, p)
	o.say("cloudflared：%s（%s，sha256 对上了）", p, v)
	return p, nil
}

func cloudflaredVersion(ctx context.Context, p string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, p, "--version").Output()
	if err != nil {
		return "", err
	}
	v := strings.TrimSpace(string(out))
	if !strings.Contains(v, "cloudflared") {
		return "", fmt.Errorf("%s --version said %q", p, v)
	}
	return v, nil
}

// AssetName is the release file for this machine: the binary itself, on macOS a .tgz with it.
func AssetName() string {
	name := "cloudflared-" + runtime.GOOS + "-" + runtime.GOARCH
	if runtime.GOOS == "darwin" {
		name += ".tgz"
	}
	return name
}

type release struct {
	TagName string `json:"tag_name"`
	Body    string `json:"body"`
	Assets  []struct {
		Name   string `json:"name"`
		URL    string `json:"browser_download_url"`
		Digest string `json:"digest"` // "sha256:<hex>"
	} `json:"assets"`
}

// download fetches the latest release's binary for this machine, checks its sha256 (the asset's
// digest, else the checksum the release notes list for it), makes sure it runs, and only then
// puts it in BinDir.
func (o *Options) download(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.GitHub+"/repos/cloudflare/cloudflared/releases/latest", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := o.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	var rel release
	err = json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&rel)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || err != nil {
		return "", fmt.Errorf("the latest release: %s %v", resp.Status, err)
	}
	name := AssetName()
	var url, want string
	for _, a := range rel.Assets {
		if a.Name == name {
			url = a.URL
			want, _ = strings.CutPrefix(strings.ToLower(a.Digest), "sha256:")
		}
	}
	if url == "" {
		return "", fmt.Errorf("release %s has no %s", rel.TagName, name)
	}
	if !hexSum(want) {
		want = notedSum(rel.Body, name)
	}
	if !hexSum(want) {
		return "", fmt.Errorf("release %s gives no sha256 for %s: not downloading what cannot be checked", rel.TagName, name)
	}
	if err := os.MkdirAll(o.BinDir, 0o755); err != nil {
		return "", err
	}
	req, err = http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err = o.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s: %s", url, resp.Status)
	}
	f, err := os.CreateTemp(o.BinDir, ".cloudflared-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name()) // gone after the rename
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, 512<<20))
	if err == nil {
		err = f.Chmod(0o755)
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return "", fmt.Errorf("sha256 of %s is %s, the release says %s", name, got, want)
	}
	bin := f.Name()
	if strings.HasSuffix(name, ".tgz") {
		if bin, err = untgz(bin, o.BinDir); err != nil {
			return "", fmt.Errorf("%s: %w", name, err)
		}
		defer os.Remove(bin) // gone after the rename
	}
	if _, err := cloudflaredVersion(ctx, bin); err != nil {
		return "", fmt.Errorf("the download does not run here: %w", err)
	}
	p := filepath.Join(o.BinDir, "cloudflared")
	if err := os.Rename(bin, p); err != nil {
		return "", err
	}
	return p, nil
}

func hexSum(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == sha256.Size
}

// notedSum finds "<name>: <sha256>" in a release's notes, as cloudflared's list them.
func notedSum(body, name string) string {
	re := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(name) + `\s*:\s*([0-9a-fA-F]{64})\s*$`)
	if m := re.FindStringSubmatch(body); m != nil {
		return strings.ToLower(m[1])
	}
	return ""
}

// untgz takes the file named cloudflared out of a .tgz into a new executable file in dir.
func untgz(archive, dir string) (string, error) {
	in, err := os.Open(archive)
	if err != nil {
		return "", err
	}
	defer in.Close()
	zr, err := gzip.NewReader(in)
	if err != nil {
		return "", err
	}
	tr := tar.NewReader(zr)
	for {
		hd, err := tr.Next()
		if err == io.EOF {
			return "", errors.New("no cloudflared in it")
		}
		if err != nil {
			return "", err
		}
		if hd.Typeflag != tar.TypeReg || path.Base(hd.Name) != "cloudflared" {
			continue
		}
		out, err := os.CreateTemp(dir, ".cloudflared-*")
		if err != nil {
			return "", err
		}
		_, err = io.Copy(out, io.LimitReader(tr, 512<<20))
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
