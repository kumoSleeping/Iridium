package server

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	pngpkg "image/png"
	"net/http"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/gate"
)

// A face asked for under its own version (the state "faces") is kept for good; under none or
// another, asked again each time. Another picture is another version; the faces of the channels
// a login does not see are not in its state.
func TestFacesKeptForGood(t *testing.T) {
	t.Parallel()
	l := start(t)
	a := l.create(200, "web", api.NewSession{Agent: api.AgentClaude, Cwd: l.work, Text: "first"})
	l.waitReply("web", a, "ok: first")
	picture := func(c uint8) string {
		var b bytes.Buffer
		img := image.NewRGBA(image.Rect(0, 0, 8, 8))
		img.Set(0, 0, color.RGBA{c, 0, 0, 255})
		_ = pngpkg.Encode(&b, img)
		return base64.StdEncoding.EncodeToString(b.Bytes())
	}
	faces := func() map[string]string {
		var m map[string]string
		_ = json.Unmarshal(l.s.hub.Get(facesKey), &m)
		return m
	}
	// The version of a's face once the state has it, other than was.
	versionOf := func(key, was string) string {
		for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
			if v := faces()[key]; v != "" && v != was {
				return v
			}
			if time.Now().After(deadline) {
				t.Fatalf("no new version of %s in the state: %v", key, faces())
			}
		}
	}
	cache := func(q string) string {
		req, _ := http.NewRequest("GET", l.base+"/v1/internal/channels/"+a+"/avatar"+q, nil)
		resp, err := l.web.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("avatar%s: %d", q, resp.StatusCode)
		}
		return resp.Header.Get("Cache-Control")
	}
	const forGood, again = "private, max-age=31536000, immutable", "private, no-cache"

	l.want(200, "web", "PUT", "/v1/internal/channels/"+a+"/avatar", map[string]string{"avatar": picture(1)}, nil)
	v := versionOf(a, "")
	if got := cache("?v=" + v); got != forGood {
		t.Errorf("under its version: %q", got)
	}
	if got := cache("?of=channel&v=" + faces()["c:"+a]); got != forGood {
		t.Errorf("as a channel, under its version: %q", got)
	}
	if got := cache(""); got != again {
		t.Errorf("under none: %q", got)
	}
	if got := cache("?v=7"); got != again {
		t.Errorf("under the count: %q", got)
	}
	// The state's versions are those the faces are served under.
	st, _ := l.s.m.State(a)
	if _, live, _ := l.s.avatarOf(a, st); live != v {
		t.Errorf("the state has %q, the face is %q", v, live)
	}

	l.want(200, "web", "PUT", "/v1/internal/channels/"+a+"/avatar", map[string]string{"avatar": picture(2)}, nil)
	v2 := versionOf(a, v)
	if got := cache("?v=" + v); got != again {
		t.Errorf("under the version before: %q", got)
	}
	if got := cache("?v=" + v2); got != forGood {
		t.Errorf("under the new version: %q", got)
	}

	// A login that sees no folder sees none of the faces.
	bot := l.newApp("bot")
	now := time.Now()
	l.s.st.AddLogin(bot.App.ID, "u1", "u1", 20, now)
	if ok, err := l.s.st.ApproveLogin(bot.App.ID, "u1", now); !ok || err != nil {
		t.Fatal(ok, err)
	}
	none, _ := json.Marshal(api.Perms{Scope: []string{"/nowhere"}})
	l.s.st.SetLoginPermsOf(bot.App.ID, "u1", none)
	u1 := caller{Identity: gate.Identity{App: bot.App.ID, Login: "u1"}}
	if got := string(l.s.streamFilter(u1)(facesKey, l.s.hub.Get(facesKey))); got != "{}" {
		t.Errorf("a login that sees nothing: %s", got)
	}
	if got := string(l.s.streamFilter(caller{Identity: gate.Identity{App: gate.WebApp, Login: gate.HostID}})(facesKey, l.s.hub.Get(facesKey))); got == "{}" {
		t.Errorf("the host sees no faces")
	}
}
