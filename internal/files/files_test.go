package files

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/pathsafe"
)

func TestDetect(t *testing.T) {
	// Only a line of its own gives a file: links, pictures and paths in prose are words.
	text := "See ![shot](/w/a.png) and [the log](/w/log.txt), `/w/b.go:12`, `~/notes.md`.\n" +
		"[文件] `/w/report.pdf`\n" +
		"  [文件] `~/out/c.png`  \n" +
		"inline [文件] `/w/not-alone.txt` is words\n" +
		"[附件] `/w/quoted.png`\n" +
		"```\n[文件] `/w/fenced.txt`\n```\n" +
		"[文件] `/w/report.pdf`"
	got := Detect(api.Block{Kind: api.BlockKindAssistant, Text: text})
	want := []Mention{{"/w/report.pdf", MentionFile}, {"~/out/c.png", MentionFile}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("detect:\n got %v\nwant %v", got, want)
	}
	// What you sent: the attachment lines Iridium writes count too.
	you := Detect(api.Block{Kind: api.BlockKindUser, Text: "look\n\n[附件] `/w/a.png`\n[文件] `/w/b.txt`"})
	if !reflect.DeepEqual(you, []Mention{{"/w/a.png", MentionFile}, {"/w/b.txt", MentionFile}}) {
		t.Errorf("user: %v", you)
	}
	w := Detect(api.Block{Kind: api.BlockKindTool, Name: "Write", Input: "{\n  \"file_path\": \"/w/out \\\"q\\\".txt\",\n  \"content\": \"cut sho"})
	if len(w) != 1 || w[0] != (Mention{`/w/out "q".txt`, MentionWrite}) {
		t.Errorf("write: %v", w)
	}
	if m := Detect(api.Block{Kind: api.BlockKindTool, Name: "Bash", Input: `{"file_path": "/w/x"}`}); m != nil {
		t.Errorf("other tools: %v", m)
	}
	if m := Detect(api.Block{Kind: api.BlockKindThinking, Text: "[文件] `/w/x`"}); m != nil {
		t.Errorf("thinking: %v", m)
	}
}

func TestDropFileLines(t *testing.T) {
	text := "Here it is.\n\n[文件] `/w/a.pdf`\n[文件] `/w/gone.txt`\n\n```\n[文件] `/w/a.pdf`\n```\n\nDone."
	got := DropFileLines(text, map[string]bool{"/w/a.pdf": true})
	want := "Here it is.\n\n[文件] `/w/gone.txt`\n\n```\n[文件] `/w/a.pdf`\n```\n\nDone."
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestLocate(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a b.png"), []byte("x"), 0o600)
	os.Mkdir(filepath.Join(dir, "sub"), 0o700)
	for _, c := range []struct {
		m    Mention
		want string
	}{
		{Mention{filepath.Join(dir, "a b.png"), MentionFile}, filepath.Join(dir, "a b.png")},
		{Mention{filepath.Join(dir, "sub", "..", "a b.png"), MentionFile}, filepath.Join(dir, "a b.png")},
		{Mention{"a b.png", MentionFile}, ""},                      // a file line's path is absolute
		{Mention{filepath.Join(dir, "sub"), MentionFile}, ""},      // not a file
		{Mention{filepath.Join(dir, "gone.txt"), MentionFile}, ""}, // not there
		{Mention{"b.go", MentionWrite}, ""},                        // a Write path is absolute
		{Mention{filepath.Join(dir, "a b.png"), MentionWrite}, filepath.Join(dir, "a b.png")},
	} {
		got, _ := Locate(c.m)
		if got != c.want {
			t.Errorf("%v: %q, want %q", c.m, got, c.want)
		}
	}
}

// fakeSessions is a session manager with one transcript of blocks, a page per block.
type fakeSessions struct {
	cwd    map[string]string
	blocks map[string][]api.Block
}

func (f *fakeSessions) Cwd(id string) (string, error) {
	c, ok := f.cwd[id]
	if !ok {
		return "", os.ErrNotExist
	}
	return c, nil
}

func (f *fakeSessions) After(id string, at int64) (api.MessagePage, error) {
	b := f.blocks[id]
	if int(at) >= len(b) {
		return api.MessagePage{Blocks: []api.Block{}, Start: at, End: at}, nil
	}
	return api.MessagePage{Blocks: b[at : at+1], Start: at, End: at + 1}, nil
}

type rig struct {
	s     *Service
	fs    *fakeSessions
	root  string
	work  string
	state string
}

func newRig(t *testing.T) *rig {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	r := &rig{root: root, work: filepath.Join(root, "work"), state: filepath.Join(root, "state")}
	os.MkdirAll(r.work, 0o700)
	r.fs = &fakeSessions{cwd: map[string]string{"s1": r.work, "s2": r.work}, blocks: map[string][]api.Block{}}
	g := pathsafe.New(nil, []string{r.state, filepath.Join(root, "secret")}, []string{UploadsFallback(r.state)})
	r.s = New(Options{State: r.state, Guard: g, Sessions: r.fs, Keep: 30 * 24 * time.Hour, MaxFile: 1 << 20, Logf: t.Logf})
	return r
}

func (r *rig) file(name, body string) string {
	p := filepath.Join(r.work, name)
	os.MkdirAll(filepath.Dir(p), 0o700)
	os.WriteFile(p, []byte(body), 0o600)
	return p
}

// Refs are made only for paths a session's transcript names, and are good only in that session.
func TestRefsOnlyForTranscriptPaths(t *testing.T) {
	r := newRig(t)
	named := r.file("named.txt", "named")
	other := r.file("other.txt", "other")
	os.MkdirAll(filepath.Join(r.root, "secret"), 0o700)
	os.WriteFile(filepath.Join(r.root, "secret", "key"), []byte("k"), 0o600)
	os.Symlink(filepath.Join(r.root, "secret", "key"), filepath.Join(r.work, "escape.txt"))
	r.fs.blocks["s1"] = []api.Block{
		{Kind: api.BlockKindUser, Text: "hi"},
		{Kind: api.BlockKindAssistant, Text: "wrote it\n[文件] `" + named + "`\n[文件] `" + filepath.Join(r.work, "escape.txt") + "`"},
	}
	// A fresh daemon has seen no page: the transcript is read for the ref.
	ref := r.s.ref("s1", named)
	if p, err := r.s.Lookup("s1", ref); err != nil || p != named {
		t.Fatalf("named: %q %v", p, err)
	}
	if _, err := r.s.Lookup("s2", ref); !errors.Is(err, ErrNoRef) {
		t.Errorf("another session's ref: %v", err)
	}
	if _, err := r.s.Lookup("s1", r.s.ref("s1", other)); !errors.Is(err, ErrNoRef) {
		t.Errorf("a path the transcript does not name: %v", err)
	}
	if _, err := r.s.Lookup("s1", r.s.ref("s1", filepath.Join(r.work, "escape.txt"))); !errors.Is(err, ErrNoRef) {
		t.Errorf("a link into a denied directory: %v", err)
	}
	// Annotate gives the page's blocks their files.
	page := api.MessagePage{Blocks: append([]api.Block(nil), r.fs.blocks["s1"]...)}
	r.s.Annotate("s1", &page)
	if f := page.Blocks[1].Files; len(f) != 1 || f[0].Ref != ref || f[0].Name != "named.txt" || f[0].Size != 5 || f[0].Kind != api.FileKindFile {
		t.Errorf("annotated: %+v", f)
	}
	// Written later: read on from where the transcript was read.
	r.fs.blocks["s1"] = append(r.fs.blocks["s1"], api.Block{Kind: api.BlockKindTool, Name: "Write", Input: `{"file_path": "` + other + `"}`})
	if p, err := r.s.Lookup("s1", r.s.ref("s1", other)); err != nil || p != other {
		t.Errorf("written later: %q %v", p, err)
	}
	// A ref named before but now a link out to a denied file is refused.
	os.Remove(named)
	os.Symlink(filepath.Join(r.root, "secret", "key"), named)
	if _, err := r.s.Lookup("s1", ref); !errors.Is(err, pathsafe.ErrDenied) {
		t.Errorf("replaced by a link: %v", err)
	}
	// Refs outlive a restart: the key is kept.
	again := New(Options{State: r.state, Sessions: r.fs})
	if again.ref("s1", other) != r.s.ref("s1", other) {
		t.Error("the key changed")
	}
}

func TestServeRangeAndETag(t *testing.T) {
	r := newRig(t)
	body := strings.Repeat("012345678\x00", 100)
	p := r.file("data.bin", body)
	get := func(h map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/x", nil)
		for k, v := range h {
			req.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		r.s.ServeFile(w, req, p)
		return w
	}
	full := get(nil)
	sum := sha256.Sum256([]byte(body))
	if full.Code != 200 || full.Body.String() != body || full.Header().Get("X-Sha256") != hex.EncodeToString(sum[:]) || full.Header().Get("Content-Security-Policy") == "" {
		t.Fatalf("full: %d %v", full.Code, full.Header())
	}
	etag := full.Header().Get("ETag")
	if !strings.HasPrefix(full.Header().Get("Content-Disposition"), "attachment") {
		t.Errorf("a binary shows inline: %s", full.Header().Get("Content-Disposition"))
	}
	// Resume from byte 990 while unchanged.
	part := get(map[string]string{"Range": "bytes=990-", "If-Range": etag})
	if part.Code != http.StatusPartialContent || part.Body.String() != body[990:] {
		t.Errorf("range: %d %q", part.Code, part.Body.String())
	}
	if w := get(map[string]string{"If-None-Match": etag}); w.Code != http.StatusNotModified {
		t.Errorf("if-none-match: %d", w.Code)
	}
	// Changed: If-Range no longer holds, the whole file comes.
	time.Sleep(10 * time.Millisecond)
	os.WriteFile(p, []byte(body+"more"), 0o600)
	if w := get(map[string]string{"Range": "bytes=990-", "If-Range": etag}); w.Code != 200 || w.Body.Len() != len(body)+4 {
		t.Errorf("stale if-range: %d %d", w.Code, w.Body.Len())
	}
	// An SVG is an image, inline, sandboxed.
	svg := r.file("a.svg", `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)
	req := httptest.NewRequest("GET", "/x", nil)
	w := httptest.NewRecorder()
	r.s.ServeFile(w, req, svg)
	if w.Header().Get("Content-Type") != "image/svg+xml" || !strings.Contains(w.Header().Get("Content-Security-Policy"), "sandbox") || !strings.HasPrefix(w.Header().Get("Content-Disposition"), "inline") {
		t.Errorf("svg: %v", w.Header())
	}
}

// errAfter is a body cut off after n bytes.
type errAfter struct {
	r io.Reader
	n int
}

func (e *errAfter) Read(p []byte) (int, error) {
	if e.n <= 0 {
		return 0, io.ErrUnexpectedEOF
	}
	if len(p) > e.n {
		p = p[:e.n]
	}
	k, err := e.r.Read(p)
	e.n -= k
	return k, err
}

func TestUploadResume(t *testing.T) {
	r := newRig(t)
	data := bytes.Repeat([]byte("abcdefgh"), 20000) // 160000 bytes
	sum := sha256.Sum256(data)
	u, err := r.s.CreateUpload("s1", api.NewUpload{Name: "../evil/data.bin", Size: int64(len(data))})
	if err != nil || u.Name != "data.bin" || u.Received != 0 {
		t.Fatalf("create: %+v %v", u, err)
	}
	gi, err := os.ReadFile(filepath.Join(r.work, ".iridium", "uploads", ".gitignore"))
	if err != nil || string(gi) != "*\n" {
		t.Errorf(".gitignore: %q %v", gi, err)
	}
	// The first chunk is cut off midway: what came is kept.
	if _, err := r.s.Put(u.ID, 0, &errAfter{r: bytes.NewReader(data[:100000]), n: 70000}, 100000); err == nil {
		t.Fatal("a cut-off chunk went through")
	}
	st, _ := r.s.UploadState(u.ID)
	if st.Received != 70000 {
		t.Fatalf("received %d", st.Received)
	}
	// A chunk at the wrong offset is refused and says where to go on.
	if st, err := r.s.Put(u.ID, 100000, bytes.NewReader(data[100000:]), -1); !errors.Is(err, ErrOffset) || st.Received != 70000 {
		t.Errorf("wrong offset: %v %+v", err, st)
	}
	if _, err := r.s.Put(u.ID, 70000, bytes.NewReader(data[70000:]), int64(len(data)-70000)+1); !errors.Is(err, ErrTooLong) {
		t.Errorf("past the end: %v", err)
	}
	if _, err := r.s.Complete(u.ID, hex.EncodeToString(sum[:])); !errors.Is(err, ErrIncomplete) {
		t.Errorf("complete early: %v", err)
	}
	if st, err := r.s.Put(u.ID, 70000, bytes.NewReader(data[70000:]), -1); err != nil || st.Received != int64(len(data)) {
		t.Fatalf("rest: %+v %v", st, err)
	}
	// A file of the name is there already: never overwritten.
	os.WriteFile(filepath.Join(r.work, ".iridium", "uploads", "data.bin"), []byte("mine"), 0o600)
	done, err := r.s.Complete(u.ID, hex.EncodeToString(sum[:]))
	if err != nil || !done.Done || done.File == nil {
		t.Fatalf("complete: %+v %v", done, err)
	}
	want := filepath.Join(r.work, ".iridium", "uploads", "data (2).bin")
	if done.File.Path != want || done.File.Sha256 != hex.EncodeToString(sum[:]) {
		t.Errorf("placed at %s", done.File.Path)
	}
	if b, _ := os.ReadFile(filepath.Join(r.work, ".iridium", "uploads", "data.bin")); string(b) != "mine" {
		t.Error("overwrote the file there")
	}
	if b, _ := os.ReadFile(want); !bytes.Equal(b, data) {
		t.Error("the upload's bytes differ")
	}
	if p, err := r.s.Lookup("s1", done.File.Ref); err != nil || p != want {
		t.Errorf("the upload's ref: %q %v", p, err)
	}
	// After a restart the record still gives the ref.
	again := New(Options{State: r.state, Sessions: r.fs, Guard: r.s.o.Guard, Keep: r.s.o.Keep})
	if p, err := again.Lookup("s1", done.File.Ref); err != nil || p != want {
		t.Errorf("after a restart: %q %v", p, err)
	}
	if again.Sweep(time.Now()); true {
		if _, err := os.Stat(want); err != nil {
			t.Error("swept a new upload")
		}
	}
	again.Sweep(time.Now().Add(31 * 24 * time.Hour))
	if _, err := os.Stat(want); !os.IsNotExist(err) {
		t.Error("an old upload was kept")
	}
}

func TestUploadChecksumAndLimits(t *testing.T) {
	r := newRig(t)
	if _, err := r.s.CreateUpload("s1", api.NewUpload{Name: "big", Size: 2 << 20}); !errors.Is(err, ErrTooLarge) {
		t.Errorf("over the cap: %v", err)
	}
	u, _ := r.s.CreateUpload("s1", api.NewUpload{Name: "a.txt", Size: 5})
	r.s.Put(u.ID, 0, strings.NewReader("hello"), 5)
	wrong := sha256.Sum256([]byte("jello"))
	if _, err := r.s.Complete(u.ID, hex.EncodeToString(wrong[:])); !errors.Is(err, ErrChecksum) {
		t.Fatalf("mismatch: %v", err)
	}
	if st, _ := r.s.UploadState(u.ID); st.Received != 0 || st.Done {
		t.Errorf("after a mismatch: %+v", st)
	}
	if _, err := os.Stat(filepath.Join(r.work, ".iridium", "uploads", "a.txt")); !os.IsNotExist(err) {
		t.Error("a mismatched upload was placed")
	}
	// It can start again.
	r.s.Put(u.ID, 0, strings.NewReader("hello"), 5)
	good := sha256.Sum256([]byte("hello"))
	if st, err := r.s.Complete(u.ID, hex.EncodeToString(good[:])); err != nil || !st.Done {
		t.Errorf("again: %+v %v", st, err)
	}
}

// Uploads go to the state directory when the working directory is $HOME or cannot be written.
func TestUploadFallback(t *testing.T) {
	r := newRig(t)
	home, _ := os.UserHomeDir()
	r.fs.cwd["home"] = home
	ro := filepath.Join(r.root, "ro")
	os.MkdirAll(ro, 0o500)
	r.fs.cwd["ro"] = ro
	for _, id := range []string{"home", "ro"} {
		d, err := r.s.UploadDir(id)
		if err != nil || d != filepath.Join(UploadsFallback(r.state), id) {
			t.Errorf("%s: %q %v", id, d, err)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".iridium")); err == nil {
		t.Log("note: ~/.iridium exists (not made by this test)")
	}
	// The fallback is in the state directory, which is denied, but excepted.
	u, _ := r.s.CreateUpload("ro", api.NewUpload{Name: "f.txt", Size: 1})
	r.s.Put(u.ID, 0, strings.NewReader("x"), 1)
	sum := sha256.Sum256([]byte("x"))
	done, err := r.s.Complete(u.ID, hex.EncodeToString(sum[:]))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.Lookup("ro", done.File.Ref); err != nil {
		t.Errorf("fallback ref: %v", err)
	}
}

func TestCopy(t *testing.T) {
	r := newRig(t)
	data := bytes.Repeat([]byte("0123456789abcdef"), 1<<14)
	src := r.file("src.bin", string(data))
	dst := filepath.Join(r.work, "out")
	os.Mkdir(dst, 0o700)
	st, _ := os.Stat(src)
	job, err := r.s.Copy(src, dst)
	if err != nil {
		t.Fatal(err)
	}
	<-job.Done()
	res, err := job.Result()
	sum := sha256.Sum256(data)
	if err != nil || res.Path != filepath.Join(dst, "src.bin") || res.Sha256 != hex.EncodeToString(sum[:]) || res.Size != st.Size() {
		t.Fatalf("copy: %+v %v", res, err)
	}
	if _, err := r.s.Copy(src, dst); !errors.Is(err, ErrExists) {
		t.Errorf("copy over: %v", err)
	}
	// Resume: plant a part file as a cut-off run would leave it.
	to := filepath.Join(dst, "again.bin")
	k := sha256.Sum256([]byte(src + "\x00" + to + "\x00" + itoa(st.Size()) + "\x00" + itoa(st.ModTime().UnixNano())))
	part := filepath.Join(dst, ".again.bin."+hex.EncodeToString(k[:8])+".part")
	os.WriteFile(part, data[:100000], 0o600)
	job, err = r.s.Copy(src, to)
	if err != nil {
		t.Fatal(err)
	}
	<-job.Done()
	if res, err := job.Result(); err != nil || res.Sha256 != hex.EncodeToString(sum[:]) {
		t.Errorf("resumed: %+v %v", res, err)
	}
	if b, _ := os.ReadFile(to); !bytes.Equal(b, data) {
		t.Error("resumed copy differs")
	}
	if _, err := os.Stat(part); !os.IsNotExist(err) {
		t.Error("part file left")
	}
	os.MkdirAll(filepath.Join(r.root, "secret"), 0o700)
	if _, err := r.s.Copy(src, filepath.Join(r.root, "secret", "x")); !errors.Is(err, pathsafe.ErrDenied) {
		t.Errorf("into a denied directory: %v", err)
	}
	l, err := r.s.List(r.work)
	if err != nil || len(l.Entries) != 2 || l.Entries[0].Name != "out" || l.Entries[0].Type != "dir" || l.Entries[1].Type != "file" {
		t.Errorf("list: %+v %v", l, err)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func writePNG(t *testing.T, path string, w, h int) {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), uint8(x ^ y), 255})
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	png.Encode(f, img)
}

func TestThumbAndClaudeImage(t *testing.T) {
	r := newRig(t)
	big := filepath.Join(r.work, "big.png")
	writePNG(t, big, 2400, 1200)
	req := httptest.NewRequest("GET", "/x", nil)
	w := httptest.NewRecorder()
	r.s.Thumb(w, req, big, ThumbSide)
	if w.Code != 200 || w.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("thumb: %d %v", w.Code, w.Header())
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(w.Body.Bytes()))
	if err != nil || cfg.Width != 1024 || cfg.Height != 512 {
		t.Errorf("thumb size %+v %v", cfg, err)
	}
	thumbs, _ := os.ReadDir(filepath.Join(r.state, "thumbs"))
	if len(thumbs) != 1 {
		t.Errorf("%d thumbnails kept", len(thumbs))
	}
	w = httptest.NewRecorder()
	r.s.Thumb(w, req, r.file("x.txt", "text"), ThumbSide)
	if w.Code != http.StatusUnsupportedMediaType {
		t.Errorf("thumb of text: %d", w.Code)
	}
	// Within Claude's limits: the file's own bytes. Past them: a smaller copy.
	small := filepath.Join(r.work, "small.png")
	writePNG(t, small, 100, 50)
	if img, ok := ForClaude(small); !ok || img.MediaType != "image/png" {
		t.Errorf("small: %v %s", ok, img.MediaType)
	}
	huge := filepath.Join(r.work, "huge.png")
	writePNG(t, huge, 8200, 100)
	img, ok := ForClaude(huge)
	if !ok {
		t.Fatal("huge: not sent")
	}
	cfg, _, _ = image.DecodeConfig(bytes.NewReader(img.Data))
	if cfg.Width != 1568 {
		t.Errorf("huge scaled to %d", cfg.Width)
	}
	if _, ok := ForClaude(r.file("y.txt", "no")); ok {
		t.Error("text as an image")
	}
}

func TestMessageAttachments(t *testing.T) {
	r := newRig(t)
	pic := filepath.Join(r.work, "pic.png")
	writePNG(t, pic, 10, 10)
	doc := r.file("doc.txt", "d")
	a, _ := r.s.Attach("s1", pic)
	b, _ := r.s.Attach("s1", doc)
	text, images, err := r.s.Message("s1", "look", []string{a.Ref, b.Ref})
	if err != nil || text != "look\n\n[附件] `"+pic+"`\n[附件] `"+doc+"`" || len(images) != 1 || images[0].MediaType != "image/png" {
		t.Errorf("message: %q %d %v", text, len(images), err)
	}
	if _, _, err := r.s.Message("s2", "x", []string{a.Ref}); !errors.Is(err, ErrNoRef) {
		t.Errorf("another session's attachment: %v", err)
	}
	if _, err := r.s.Attach("s1", "rel.txt"); !errors.Is(err, ErrBad) {
		t.Errorf("relative: %v", err)
	}
}

// An image's size comes with its reference, upright, so the page keeps its place before it loads.
func TestImageSize(t *testing.T) {
	r := newRig(t)
	p := filepath.Join(r.work, "wide.png")
	writePNG(t, p, 300, 120)
	if f, err := r.s.Info(p); err != nil || f.W != 300 || f.H != 120 {
		t.Errorf("png: %+v %v", f, err)
	}
	// A JPEG 40×20 whose EXIF says it is turned a quarter (orientation 6): 20×40 upright.
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 40, 20)), nil); err != nil {
		t.Fatal(err)
	}
	tiff := []byte{'M', 'M', 0, 42, 0, 0, 0, 8, 0, 1, 0x01, 0x12, 0, 3, 0, 0, 0, 1, 0, 6, 0, 0, 0, 0, 0, 0, 0, 0}
	seg := append([]byte("Exif\x00\x00"), tiff...)
	app1 := append([]byte{0xFF, 0xE1, byte((len(seg) + 2) >> 8), byte(len(seg) + 2)}, seg...)
	data := append(append([]byte{0xFF, 0xD8}, app1...), buf.Bytes()[2:]...)
	q := filepath.Join(r.work, "turned.jpg")
	if err := os.WriteFile(q, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if f, err := r.s.Info(q); err != nil || f.W != 20 || f.H != 40 {
		t.Errorf("jpeg turned: %+v %v", f, err)
	}
	if f, _ := r.s.Info(r.file("z.txt", "text")); f.W != 0 || f.H != 0 {
		t.Errorf("text has a size: %+v", f)
	}
}
