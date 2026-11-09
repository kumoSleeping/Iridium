package server

import (
	"bytes"
	"encoding/base64"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/testagent"
)

// A picture a tool's result gave the model is served by its result's at, as a picture only, and
// asked again by its ETag.
func TestImageRoute(t *testing.T) {
	l := start(t)
	var created api.NewSessionReply
	l.want(200, "web", "POST", "/api/sessions", api.NewSession{Agent: api.AgentClaude, Cwd: l.work, Text: "PICTURE"}, &created)
	at := ""
	for deadline := time.Now().Add(10 * time.Second); at == "" && time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		var page api.MessagePage
		l.do("web", "GET", "/api/sessions/"+created.ID+"/messages", nil, &page)
		for _, b := range page.Blocks {
			if b.Result != nil && len(b.Result.Images) > 0 {
				at = b.Result.At
			}
		}
	}
	if at == "" {
		t.Fatal("no result with a picture")
	}
	url := l.base + "/api/sessions/" + created.ID + "/image?at=" + at
	get := func(n string, etag string) *http.Response {
		req, _ := http.NewRequest("GET", url+"&n="+n, nil)
		if etag != "" {
			req.Header.Set("If-None-Match", etag)
		}
		resp, err := l.web.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	resp := get("0", "")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	tiny, _ := base64.StdEncoding.DecodeString(testagent.TinyPNG)
	h := resp.Header
	if resp.StatusCode != 200 || !bytes.Equal(body, tiny) || h.Get("Content-Type") != "image/png" || h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Content-Security-Policy") == "" {
		t.Fatalf("picture: %d %q %v", resp.StatusCode, body, h)
	}
	if again := get("0", h.Get("ETag")); again.StatusCode != http.StatusNotModified {
		t.Errorf("asked again: %d", again.StatusCode)
	} else {
		again.Body.Close()
	}
	// One by a handle, with no channel to read it; none there; n not a number.
	for n, want := range map[string]int{"1": 404, "9": 404, "x": 400} {
		resp := get(n, "")
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("n=%s: %d, want %d", n, resp.StatusCode, want)
		}
	}
}
