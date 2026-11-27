package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
