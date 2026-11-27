package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/satori"
	"github.com/kumoSleeping/iridium/internal/session"
)

// Claude's ir asks for manual approval with --wait: it waits for a person's answer and says it in
// a line; its system prompt tells it how.
func TestManualWait(t *testing.T) {
	cfg, work := rig(t)
	r := ir("api", "channel.create", "_data.agent=claude", "_data.cwd="+work, "_data.text=go HOLD", "_data.mode=auto")
	sid := strings.TrimSpace(strings.Split(r.out, "\n")[0])
	if r.code != 0 || len(sid) != 36 {
		t.Fatalf("channel.create: %+v", r)
	}
	prompt, err := os.ReadFile(filepath.Join(cfg.StatePath(), "hold", sid, "system-prompt.md"))
	if err != nil || !strings.Contains(string(prompt), "internal/sessions/"+sid+"/manual") {
		t.Errorf("system prompt %q %v", prompt, err)
	}
	t.Setenv("IRIDIUM_SESSION", sid)
	done := make(chan result, 1)
	go func() { done <- ir("api", "POST", "internal/sessions/"+sid+"/manual", "reason=npx is needed", "--wait") }()

	// The person answers, at the socket (no agent, no session).
	sock := cfg.SocketPath()
	hc := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", sock)
	}}}
	var ask api.Ask
	for end := time.Now().Add(10 * time.Second); ask.RequestID == ""; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatal("no ask")
		}
		resp, err := hc.Get("http://ir/v1/internal/sessions/" + sid)
		if err != nil {
			t.Fatal(err)
		}
		var st api.SessionState
		json.NewDecoder(resp.Body).Decode(&st)
		resp.Body.Close()
		for _, a := range st.Asks {
			if a.Kind == session.AskManual {
				ask = a
			}
		}
	}
	b, _ := json.Marshal(map[string]any{"id": session.AskCardID(sid, ask.RequestID), "option": session.OptionAllow})
	req, _ := http.NewRequest("POST", "http://ir/v1/internal/button", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(satori.HeaderPlatform, "local")
	req.Header.Set(satori.HeaderUser, "local")
	resp, err := hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("answer: %d", resp.StatusCode)
	}
	select {
	case r := <-done:
		if r.code != 0 || !strings.HasPrefix(r.out, "approved: manual approval till this turn ends or 3 steps are answered") {
			t.Fatalf("--wait: %+v", r)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("--wait did not end")
	}
}
