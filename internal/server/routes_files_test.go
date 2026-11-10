package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
)

// The web page's way: upload in chunks, send it with a message, find it in the reply, fetch it.
func TestUploadSendAndFetch(t *testing.T) {
	s, cfg, work := setup(t)
	tcp, unix, err := s.Listen()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Serve(ctx, tcp, unix)
	base := "http://" + tcp.Addr().String()
	c := &http.Client{Timeout: 10 * time.Second}

	var created api.NewSessionReply
	if code := call(t, c, "POST", base+"/api/sessions", api.NewSession{Agent: "claude", Cwd: work, Caller: "some-caller"}, &created); code != 200 {
		t.Fatalf("create: %d", code)
	}
	id := created.ID

	img := image.NewRGBA(image.Rect(0, 0, 40, 20))
	for x := range 40 {
		img.Set(x, x/2, color.RGBA{255, 0, 0, 255})
	}
	var buf bytes.Buffer
	png.Encode(&buf, img)
	data := buf.Bytes()
	sum := sha256.Sum256(data)

	var up api.Upload
	if code := call(t, c, "POST", base+"/api/sessions/"+id+"/uploads", api.NewUpload{Name: "shot.png", Size: int64(len(data))}, &up); code != 200 || up.ID == "" {
		t.Fatalf("new upload: %d %+v", code, up)
	}
	put := func(off int, chunk []byte) (int, api.Upload) {
		req, _ := http.NewRequest("PUT", base+"/api/uploads/"+up.ID+"?offset="+itoa(int64(off)), bytes.NewReader(chunk))
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var u api.Upload
		b, _ := io.ReadAll(resp.Body)
		json.Unmarshal(b, &u)
		return resp.StatusCode, u
	}
	half := len(data) / 2
	if code, u := put(0, data[:half]); code != 200 || u.Received != int64(half) {
		t.Fatalf("chunk 1: %d %+v", code, u)
	}
	if code, u := put(0, data[:half]); code != http.StatusConflict || u.Received != int64(half) {
		t.Errorf("a chunk again: %d %+v", code, u)
	}
	var got api.Upload
	call(t, c, "GET", base+"/api/uploads/"+up.ID, nil, &got)
	if got.Received != int64(half) {
		t.Errorf("received %d", got.Received)
	}
	put(half, data[half:])
	if code := call(t, c, "POST", base+"/api/uploads/"+up.ID+"/complete", api.CompleteUpload{Sha256: hex.EncodeToString(sum[:])}, &got); code != 200 || got.File == nil {
		t.Fatalf("complete: %d %+v", code, got)
	}
	placed := filepath.Join(work, ".iridium", "uploads", "shot.png")
	if got.File.Path != placed || got.File.Kind != api.FileKindImage {
		t.Errorf("file %+v", got.File)
	}

	// Sent with a message: the agent sees the path, and the image itself.
	if code := call(t, c, "POST", base+"/api/sessions/"+id+"/send", api.Send{Text: "look", Attachments: []string{got.File.Ref}}, nil); code != 200 {
		t.Fatalf("send: %d", code)
	}
	var page api.MessagePage
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		call(t, c, "GET", base+"/api/sessions/"+id+"/messages", nil, &page)
		if len(page.Blocks) == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no reply: %+v", page)
		}
	}
	if !strings.Contains(page.Blocks[0].Text, "[附件] `"+placed+"`") {
		t.Errorf("message %q", page.Blocks[0].Text)
	}
	if sent := page.Blocks[0]; len(sent.Files) != 1 || sent.Files[0].Ref != got.File.Ref || !sent.Files[0].Thumb {
		t.Fatalf("message files %+v", sent.Files)
	}
	// The fake agent says the message back, attachment line and all: quoted, it gives no file.
	if reply := page.Blocks[1]; len(reply.Files) != 0 {
		t.Errorf("reply files %+v", reply.Files)
	}
	in, _ := os.ReadFile(filepath.Join(cfg.StatePath(), "hold", id, "in.log"))
	if !bytes.Contains(in, []byte(`"type":"image"`)) || !bytes.Contains(in, []byte(`"media_type":"image/png"`)) {
		t.Errorf("no image block went to the agent")
	}

	resp, err := c.Get(base + "/api/sessions/" + id + "/files/" + got.File.Ref)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !bytes.Equal(body, data) || resp.Header.Get("X-Sha256") != hex.EncodeToString(sum[:]) || resp.Header.Get("Content-Type") != "image/png" {
		t.Errorf("fetch: %d %v", resp.StatusCode, resp.Header)
	}
	var info api.FileRef
	if code := call(t, c, "GET", base+"/api/sessions/"+id+"/files/"+got.File.Ref+"/info", nil, &info); code != 200 || info.Sha256 != hex.EncodeToString(sum[:]) {
		t.Errorf("info: %d %+v", code, info)
	}
	if code := call(t, c, "GET", base+"/api/sessions/"+id+"/files/"+got.File.Ref+"/thumb", nil, nil); code != 200 {
		t.Errorf("thumb: %d", code)
	}
	if code := call(t, c, "GET", base+"/api/sessions/"+id+"/files/aaaaaaaaaaaaaaaa", nil, nil); code != 422 {
		t.Errorf("unknown ref: %d", code)
	}
	// Paths are for the local ir only, not the page.
	if code := call(t, c, "GET", base+"/api/files?path="+work, nil, nil); code != 404 && code != 405 {
		t.Errorf("listing over TCP: %d", code)
	}
	if code := call(t, c, "POST", base+"/api/sessions/"+id+"/attach", api.AttachPath{Path: placed}, nil); code != 404 && code != 405 {
		t.Errorf("attach over TCP: %d", code)
	}
	call(t, c, "POST", base+"/api/sessions/"+id+"/stop", nil, nil)
}
