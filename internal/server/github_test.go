package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
)

// GitHub (routes_github.go): a person binds their own account from a panel, its token checked
// with GitHub; an agent's git and gh get the token of the person its turn is for, or of one named;
// none bound, nothing (the machine's own login). Never listed; masked as the secrets are.
func TestGitHub(t *testing.T) {
	const tok = "github_pat_0123456789abcdef"
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user" || r.Header.Get("Authorization") != "Bearer "+tok {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"login":"otae-1204","id":77999307,"avatar_url":"https://a/x.png"}`))
	}))
	defer gh.Close()
	was := githubAPI
	githubAPI = gh.URL
	defer func() { githubAPI = was }()

	l := start(t)
	ch := l.create(200, "web", api.NewSession{Agent: api.AgentClaude, Cwd: l.work, Text: "first"})
	agent := "agent-in:" + ch

	// Nothing bound: the agent's git and gh go on as the machine's.
	l.want(204, agent, "GET", "/v1/internal/github/token", nil, nil)

	// Bound by a person, the token checked; an agent binds none, a bad token is refused.
	l.want(403, agent, "PUT", "/v1/internal/github", api.SetGitHub{Token: tok}, nil)
	l.want(422, "web", "PUT", "/v1/internal/github", api.SetGitHub{Token: "nope"}, nil)
	var b api.GitHubBinding
	l.want(200, "web", "PUT", "/v1/internal/github", api.SetGitHub{Token: tok}, &b)
	if b.Login != "otae-1204" || b.ID != 77999307 || b.Member != "local" {
		t.Fatalf("bound: %+v", b)
	}

	// Listed without the token; the agent's turn is the host's here.
	raw := l.wantRaw(200, agent, "GET", "/v1/internal/github", nil)
	if strings.Contains(raw, tok) || !strings.Contains(raw, `"turn":"local"`) || !strings.Contains(raw, `"login":"otae-1204"`) {
		t.Fatalf("list: %s", raw)
	}

	// The agent's git and gh: the token of its turn's person; or of one named, by GitHub login.
	var got api.GitHubToken
	l.want(200, agent, "GET", "/v1/internal/github/token", nil, &got)
	if got.Token != tok || got.Login != "otae-1204" {
		t.Fatalf("token: %+v", got)
	}
	l.want(200, agent, "GET", "/v1/internal/github/token?user=@otae-1204", nil, &got)
	l.want(422, agent, "GET", "/v1/internal/github/token?user=nobody", nil, nil)
	// Not to a page.
	l.want(403, "web", "GET", "/v1/internal/github/token", nil, nil)

	// Masked as a secret is.
	if m := l.s.MaskText("push with " + tok); strings.Contains(m, tok) {
		t.Fatalf("not masked: %s", m)
	}

	// Unbound: none again.
	l.want(200, "web", "DELETE", "/v1/internal/github", nil, nil)
	l.want(204, agent, "GET", "/v1/internal/github/token", nil, nil)
}

// Bound by a link (github_device.go): a code from GitHub, the page asking again till GitHub says
// it was allowed; never sooner than GitHub's interval; the token kept as a pasted one is.
func TestGitHubDevice(t *testing.T) {
	const tok = "gho_0123456789abcdef"
	allowed := false
	asked := 0
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		switch r.URL.Path {
		case "/login/device/code":
			if r.Form.Get("client_id") != githubClientID || !strings.Contains(r.Form.Get("scope"), "repo") {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			_, _ = w.Write([]byte(`{"device_code":"dev-1","user_code":"ABCD-1234","verification_uri":"https://github.com/login/device","expires_in":900,"interval":5}`))
		case "/login/oauth/access_token":
			asked++
			if r.Form.Get("device_code") != "dev-1" {
				_, _ = w.Write([]byte(`{"error":"incorrect_device_code"}`))
			} else if !allowed {
				_, _ = w.Write([]byte(`{"error":"authorization_pending"}`))
			} else {
				_, _ = w.Write([]byte(`{"access_token":"` + tok + `","token_type":"bearer"}`))
			}
		case "/user":
			if r.Header.Get("Authorization") != "Bearer "+tok {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"login":"otae-1204","id":77999307}`))
		}
	}))
	defer gh.Close()
	wasAPI, wasWeb := githubAPI, githubWeb
	githubAPI, githubWeb = gh.URL, gh.URL
	defer func() { githubAPI, githubWeb = wasAPI, wasWeb }()

	l := start(t)
	ch := l.create(200, "web", api.NewSession{Agent: api.AgentClaude, Cwd: l.work, Text: "first"})
	l.want(403, "agent-in:"+ch, "POST", "/v1/internal/github/device", nil, nil)

	var d api.GitHubDevice
	l.want(200, "web", "POST", "/v1/internal/github/device", nil, &d)
	if d.Code != "ABCD-1234" || d.URI == "" || d.Interval != 5 || d.ID == "" {
		t.Fatalf("device: %+v", d)
	}
	raw := l.wantRaw(200, "web", "POST", "/v1/internal/github/device/"+d.ID, nil)
	if !strings.Contains(raw, `"waiting"`) || asked != 0 || strings.Contains(raw, "dev-1") {
		t.Fatalf("too soon: GitHub asked %d times: %s", asked, raw)
	}
	due := func() {
		githubFlows.Lock()
		githubFlows.m[d.ID].next = time.Now().Add(-time.Second)
		githubFlows.Unlock()
	}
	due()
	var st api.GitHubDeviceState
	l.want(200, "web", "POST", "/v1/internal/github/device/"+d.ID, nil, &st)
	if st.State != "waiting" || asked != 1 {
		t.Fatalf("pending: %+v, asked %d", st, asked)
	}
	allowed = true
	due()
	l.want(200, "web", "POST", "/v1/internal/github/device/"+d.ID, nil, &st)
	if st.State != "done" || st.Binding == nil || st.Binding.Login != "otae-1204" || st.Binding.Member != "local" {
		t.Fatalf("done: %+v", st)
	}
	var got api.GitHubToken
	l.want(200, "agent-in:"+ch, "GET", "/v1/internal/github/token", nil, &got)
	if got.Token != tok {
		t.Fatalf("token kept: %+v", got)
	}
	// Done, it is gone.
	l.want(404, "web", "POST", "/v1/internal/github/device/"+d.ID, nil, nil)
}
