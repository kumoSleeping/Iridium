package server

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/config"
	"github.com/kumoSleeping/iridium/internal/webui"
)

// A session's public link: made and revoked by a person who may use the session, only with the
// public address set; anyone with it reads what people and the agent said there (no thinking,
// tools or results, credentials masked), and nothing else: the token opens no other route.
func TestPublicLink(t *testing.T) {
	t.Parallel()
	l := startWith(t, func(s *Server, _ config.Config, _ string) {
		s.App = webui.New(fstest.MapFS{
			"index.html":       {Data: []byte("<!doctype html><title>Iridium</title>")},
			"view.html":        {Data: []byte(`<!doctype html><script src="/theme.js"></script><script type="module" src="/assets/view-1.js"></script>`)},
			"theme.js":         {Data: []byte("1")},
			"assets/view-1.js": {Data: []byte("console.log(1)")},
		})
	})
	const secret = "ghp_s3cretvalue42"
	l.want(200, "socket", "PUT", "/v1/internal/secrets/GITHUB_TOKEN", api.SetSecret{Value: secret}, nil)
	id := l.create(200, "socket", api.NewSession{Agent: api.AgentClaude, Cwd: l.work, Text: "hello there"})
	l.waitReply("socket", id, "ok: hello there")

	// What else the transcript holds: thinking, a call and its result, then words with a credential.
	files, _ := filepath.Glob(filepath.Join(l.cfg.ClaudeHome, "projects", "*", id+".jsonl"))
	if len(files) != 1 {
		t.Fatalf("the transcript: %v", files)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	lines := []map[string]any{
		{"type": "assistant", "timestamp": now, "message": map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "thinking", "thinking": "SECRET-THINKING"},
			map[string]any{"type": "tool_use", "id": "toolu_1", "name": "Bash", "input": map[string]any{"command": "echo SECRET-TOOL"}},
		}}},
		{"type": "user", "timestamp": now, "message": map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "tool_result", "tool_use_id": "toolu_1", "content": "SECRET-RESULT"},
		}}},
		{"type": "assistant", "timestamp": now, "message": map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "text", "text": "the answer is 42, key " + secret},
		}}},
	}
	f, err := os.OpenFile(files[0], os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range lines {
		b, _ := json.Marshal(x)
		f.Write(append(b, '\n'))
	}
	f.Close()
	if p, err := l.s.m.Messages(id, -1, -1); err != nil || !strings.Contains(mustString(p), "SECRET-THINKING") || !strings.Contains(mustString(p), "SECRET-RESULT") {
		t.Fatalf("the transcript as the panel reads it: %v %+v", err, p)
	}

	// Public address off: no link, and no view.
	var pl api.PublicLink
	if l.want(200, "socket", "GET", "/v1/internal/sessions/"+id+"/public", nil, &pl); pl.Ready || pl.On {
		t.Fatalf("off: %+v", pl)
	}
	var e api.Error
	if l.do("socket", "POST", "/v1/internal/sessions/"+id+"/public", nil, &e); e.Code != "not_public" {
		t.Fatalf("made with the public address off: %+v", e)
	}
	if code := l.do("web", "GET", "/p/"+strings.Repeat("a", 43), nil, nil); code != 404 {
		t.Errorf("the view with the public address off: %d", code)
	}

	// The passkey's RP ID is a name: localhost, as the origin.
	base := strings.Replace(l.base, "127.0.0.1", "localhost", 1)
	if err := l.s.SetPublicOrigin(base); err != nil {
		t.Fatal(err)
	}
	// An agent's ir may not make one.
	l.want(403, "agent", "POST", "/v1/internal/sessions/"+id+"/public", nil, nil)
	l.want(200, "socket", "POST", "/v1/internal/sessions/"+id+"/public", nil, &pl)
	token, ok := strings.CutPrefix(pl.URL, base+"/p/")
	if !pl.Ready || !pl.On || !ok || len(token) < 22 {
		t.Fatalf("made: %+v", pl)
	}
	var got api.PublicLink
	if l.want(200, "socket", "GET", "/v1/internal/sessions/"+id+"/public", nil, &got); !got.On || got.URL != "" || got.CreatedAt == 0 {
		t.Fatalf("its state: %+v", got)
	}

	// The view, as anyone: no cookie, no TOKEN.
	get := func(path string, hdr ...string) (*http.Response, string) {
		t.Helper()
		req, _ := http.NewRequest("GET", base+path, nil)
		for i := 0; i+1 < len(hdr); i += 2 {
			req.Header.Set(hdr[i], hdr[i+1])
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp, string(b)
	}
	resp, raw := get("/p/" + token + "/api/messages")
	if resp.StatusCode != 200 {
		t.Fatalf("messages: %d %s", resp.StatusCode, raw)
	}
	for _, no := range []string{"SECRET-THINKING", "SECRET-TOOL", "SECRET-RESULT", secret, l.work} {
		if strings.Contains(raw, no) {
			t.Errorf("the view has %q: %s", no, raw)
		}
	}
	if resp.Header.Get("Set-Cookie") != "" || resp.Header.Get("Referrer-Policy") != "no-referrer" || !strings.Contains(resp.Header.Get("Cache-Control"), "private") {
		t.Errorf("headers: %v", resp.Header)
	}
	var page api.PublicPage
	json.Unmarshal([]byte(raw), &page)
	said := func(p api.PublicPage) []string {
		var out []string
		for _, m := range p.Messages {
			out = append(out, m.Role+":"+m.Text)
		}
		return out
	}
	if s := strings.Join(said(page), "|"); page.Agent != api.AgentClaude || page.End == 0 ||
		!strings.Contains(s, "user:hello there") || !strings.Contains(s, "agent:ok: hello there") || !strings.Contains(s, "agent:the answer is 42") {
		t.Fatalf("the page: %s %+v", s, page)
	}

	// What comes after, by polling from its end.
	l.send("socket", id, api.Send{Text: "more"})
	l.waitReply("socket", id, "ok: more")
	var next api.PublicPage
	_, raw = get("/p/" + token + "/api/messages?after=" + strconv.FormatInt(page.End, 10))
	json.Unmarshal([]byte(raw), &next)
	if s := said(next); len(s) != 2 || s[0] != "user:more" || s[1] != "agent:ok: more" || next.End <= page.End {
		t.Errorf("after: %v %d", s, next.End)
	}

	// The page, its files under the link.
	resp, raw = get("/p/" + token)
	if resp.StatusCode != 200 || !strings.Contains(raw, `"/p/`+token+`/assets/view-1.js"`) || !strings.Contains(raw, `"/p/`+token+`/theme.js"`) || resp.Header.Get("Content-Security-Policy") == "" {
		t.Fatalf("the page: %d %s", resp.StatusCode, raw)
	}
	if resp, _ = get("/p/" + token + "/assets/view-1.js"); resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Cache-Control"), "private") {
		t.Errorf("its script: %d %v", resp.StatusCode, resp.Header)
	}

	// The token opens nothing else: not the API, not the app's own files, not another path under it.
	for _, try := range []struct {
		path string
		hdr  []string
	}{
		{"/v1/internal/sessions/" + id, []string{"Authorization", "Bearer " + token}},
		{"/v1/internal/sessions/" + id, []string{"Cookie", "__Host-ir=" + token}},
		{"/v1/internal/sessions/" + id + "?token=" + token, nil},
		{"/v1/events", []string{"Authorization", "Bearer " + token}},
		{"/assets/view-1.js", []string{"Referer", base + "/p/" + token}},
		{"/p/" + token + "/v1/internal/sessions", nil},
		{"/p/" + token + "/index.html", nil},
		{"/p/" + token + "/assets/../index.html", nil},
		{"/p/" + token + "/files/0.0/abcdefghij", nil},
		{"/p/" + token + "/api/messages/../../../v1/internal/sessions", nil},
	} {
		if resp, raw := get(try.path, try.hdr...); resp.StatusCode == 200 {
			t.Errorf("%s with the token: %d %.200s", try.path, resp.StatusCode, raw)
		}
	}
	req, _ := http.NewRequest("POST", base+"/p/"+token+"/api/messages", strings.NewReader(`{"text":"hi"}`))
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != 405 {
		t.Errorf("a POST: %v %v", resp, err)
	}

	// Another session is not opened by it, nor by any other token.
	if resp, _ := get("/p/" + strings.Repeat("A", len(token)) + "/api/messages"); resp.StatusCode != 404 {
		t.Errorf("an unknown token: %d", resp.StatusCode)
	}

	// Made again: the old link no longer works. Revoked: neither does the new one.
	var again api.PublicLink
	l.want(200, "socket", "POST", "/v1/internal/sessions/"+id+"/public", nil, &again)
	token2 := strings.TrimPrefix(again.URL, base+"/p/")
	if token2 == token {
		t.Fatal("the same token again")
	}
	if resp, _ := get("/p/" + token + "/api/messages"); resp.StatusCode != 404 {
		t.Errorf("the old link: %d", resp.StatusCode)
	}
	if resp, _ := get("/p/" + token2 + "/api/messages"); resp.StatusCode != 200 {
		t.Errorf("the new link: %d", resp.StatusCode)
	}
	l.want(200, "socket", "DELETE", "/v1/internal/sessions/"+id+"/public", nil, nil)
	for _, p := range []string{"", "/api/messages", "/assets/view-1.js"} {
		if resp, _ := get("/p/" + token2 + p); resp.StatusCode != 404 {
			t.Errorf("revoked %q: %d", p, resp.StatusCode)
		}
	}

	// The public address off again: a link made before stops working.
	l.want(200, "socket", "POST", "/v1/internal/sessions/"+id+"/public", nil, &again)
	token3 := strings.TrimPrefix(again.URL, base+"/p/")
	if err := l.s.SetPublicOrigin(""); err != nil {
		t.Fatal(err)
	}
	if code := l.do("web", "GET", "/p/"+token3+"/api/messages", nil, nil); code != 404 {
		t.Errorf("with the public address off: %d", code)
	}
}

func mustString(v any) string { return string(mustJSON(v)) }
