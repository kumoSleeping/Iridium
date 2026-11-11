package server

import (
	"strings"
	"testing"

	"github.com/kumoSleeping/iridium/internal/api"
)

func ptr[T any](v T) *T { return &v }

// The credentials: stored from the page, a person's ir, an agent's ir through a panel; used by ir
// run alone, as their scopes and confirmations say; masked in what carries transcripts.
func TestSecrets(t *testing.T) {
	t.Parallel()
	l := start(t)
	const val = "ghp_s3cretvalue42"

	// The page (本机 here) stores one; listing never gives the value.
	var info api.SecretInfo
	l.want(200, "web", "PUT", "/v1/internal/secrets/GITHUB_TOKEN", api.SetSecret{Value: val, Note: ptr("push to kumo/foo")}, &info)
	if info.Name != "GITHUB_TOKEN" || info.Scope != api.ScopeAll || info.Note != "push to kumo/foo" {
		t.Fatalf("stored: %+v", info)
	}
	l.want(400, "web", "PUT", "/v1/internal/secrets/lower", api.SetSecret{Value: "x"}, nil)
	l.want(400, "web", "PUT", "/v1/internal/secrets/NEW_ONE", api.SetSecret{}, nil) // no value
	var raw string
	raw = l.wantRaw(200, "agent", "GET", "/v1/internal/secrets", nil)
	if strings.Contains(raw, val) || !strings.Contains(raw, "GITHUB_TOKEN") {
		t.Fatalf("list: %s", raw)
	}

	// Credentials are the host's and ir_web's logins' to list, set and delete (decision 14): an
	// app does none of it.
	var tok api.Token
	tok = l.newApp("bot", false)
	bot := tok.Token
	l.want(403, bot, "PUT", "/v1/internal/secrets/BOT_KEY", api.SetSecret{Value: "abcdefgh"}, nil)
	l.want(403, bot, "GET", "/v1/internal/secrets", nil, nil)
	l.want(403, bot, "DELETE", "/v1/internal/secrets/GITHUB_TOKEN", nil, nil)
	l.want(200, "web", "PUT", "/v1/internal/secrets/BOT_KEY", api.SetSecret{Value: "abcdefgh"}, nil)
	l.want(200, "web", "DELETE", "/v1/internal/secrets/BOT_KEY", nil, nil)

	// An agent's ir: a confirmation, whose panel types the value; the agent learns only the outcome.
	var p api.Pending
	l.want(202, "agent", "PUT", "/v1/internal/secrets/NAS_PASSWORD", api.SetSecret{Note: ptr("mount the NAS"), Value: "ignored"}, &p)
	if p.Need.Kind != api.NeedSecretSet || p.Need.Name != "NAS_PASSWORD" || p.Need.Note != "mount the NAS" || p.Need.Session != "caller-session" || p.Need.Replace {
		t.Fatalf("pending: %+v", p.Need)
	}
	l.want(422, "web", "POST", "/v1/internal/needs/"+p.Need.ID, api.NeedAnswer{Allow: true}, nil) // no value typed
	var rep string
	rep = l.wantRaw(200, "web", "POST", "/v1/internal/needs/"+p.Need.ID, api.NeedAnswer{Allow: true, Value: "nas-pass-123"})
	if strings.Contains(rep, "nas-pass-123") || !strings.Contains(rep, `"allowed"`) {
		t.Fatalf("answer: %s", rep)
	}
	rep = l.wantRaw(200, "agent", "GET", "/v1/internal/needs/"+p.Need.ID, nil)
	if strings.Contains(rep, "nas-pass-123") || !strings.Contains(rep, `"allowed"`) {
		t.Fatalf("state: %s", rep)
	}

	// Values over the socket only.
	use := api.UseSecrets{Names: []string{"GITHUB_TOKEN", "NAS_PASSWORD"}, Command: "git push {{GITHUB_TOKEN}}"}
	l.want(403, "web", "POST", "/v1/internal/secrets/use", use, nil)
	var vals api.SecretValues
	l.want(200, "socket", "POST", "/v1/internal/secrets/use", use, &vals)
	if vals.Values["GITHUB_TOKEN"] != val || vals.Values["NAS_PASSWORD"] != "nas-pass-123" {
		t.Fatalf("values: %+v", vals)
	}
	// An agent in a session 本机 opened: at once.
	l.want(200, "agent", "POST", "/v1/internal/secrets/use", use, &vals)
	var list api.Secrets
	l.want(200, "web", "GET", "/v1/internal/secrets", nil, &list)
	if len(list.Uses) != 4 || list.Uses[0].Command != "git push {{GITHUB_TOKEN}}" || list.Secrets[0].Uses != 2 || list.Secrets[0].LastUse == nil {
		t.Fatalf("uses: %+v", list)
	}

	// A session a terminal not known to be the owner's opened: a confirmation only the owner's own
	// may allow. (The fake agent is Claude, which only the owner's own open: one opens it and is
	// revoked after.)
	var own api.Token
	own = l.newApp("gone", true)
	its := l.create(200, own.Token, api.NewSession{Agent: api.AgentClaude, Cwd: l.work, Text: "token " + val + " here"})
	l.waitReply("web", its, "ok: token [secret:GITHUB_TOKEN] here") // masked already
	l.want(200, "socket", "DELETE", "/v1/internal/apps/gone", nil, nil)
	in := "agent-in:" + its
	one := api.UseSecrets{Names: []string{"GITHUB_TOKEN"}, Command: "gh auth"}
	l.want(202, in, "POST", "/v1/internal/secrets/use", one, &p)
	if p.Need.Kind != api.NeedSecretUse || !p.Need.Owner || p.Need.Session != its || p.Need.Command != "gh auth" || len(p.Need.Secrets) != 1 {
		t.Fatalf("use pending: %+v", p.Need)
	}
	l.want(403, "web", "POST", "/v1/internal/needs/"+p.Need.ID, api.NeedAnswer{Allow: true}, nil) // the local page is not the owner's say
	l.want(200, "socket", "POST", "/v1/internal/needs/"+p.Need.ID, api.NeedAnswer{Allow: true}, nil)
	once := one
	once.Confirm = p.Need.ID
	l.want(200, in, "POST", "/v1/internal/secrets/use", once, &vals)
	l.want(202, in, "POST", "/v1/internal/secrets/use", once, &p) // spent
	l.want(200, "socket", "POST", "/v1/internal/needs/"+p.Need.ID, api.NeedAnswer{Allow: true, ForSession: true}, nil)
	l.want(200, in, "POST", "/v1/internal/secrets/use", one, nil) // for the rest of the session
	l.want(200, in, "POST", "/v1/internal/secrets/use", one, nil)

	// Every time: the owner's own session asks too, and "this session" does not hold.
	l.want(200, "web", "PUT", "/v1/internal/secrets/GITHUB_TOKEN", api.SetSecret{Always: ptr(true)}, nil)
	l.want(202, "agent", "POST", "/v1/internal/secrets/use", one, &p)
	if !p.Need.Always {
		t.Fatalf("always: %+v", p.Need)
	}
	l.want(200, "socket", "POST", "/v1/internal/needs/"+p.Need.ID, api.NeedAnswer{Allow: true, ForSession: true}, nil)
	l.want(202, "agent", "POST", "/v1/internal/secrets/use", one, nil)
	l.want(200, "socket", "POST", "/v1/internal/secrets/use", one, nil) // a person at the machine: at once

	// Owner only: the channel's session may not, not even through a confirmation.
	l.want(200, "web", "PUT", "/v1/internal/secrets/NAS_PASSWORD", api.SetSecret{Scope: api.ScopeOwner}, nil)
	l.want(403, in, "POST", "/v1/internal/secrets/use", api.UseSecrets{Names: []string{"NAS_PASSWORD"}}, nil)
	l.want(200, "agent", "POST", "/v1/internal/secrets/use", api.UseSecrets{Names: []string{"NAS_PASSWORD"}}, nil)
	// Some workspaces: the session is in none.
	l.want(200, "web", "PUT", "/v1/internal/secrets/NAS_PASSWORD", api.SetSecret{Scope: api.ScopeWorkspaces, Workspaces: []int64{99}}, nil)
	l.want(403, "agent-in:"+its, "POST", "/v1/internal/secrets/use", api.UseSecrets{Names: []string{"NAS_PASSWORD"}}, nil)
	l.want(422, "agent", "POST", "/v1/internal/secrets/use", api.UseSecrets{Names: []string{"NOPE"}}, nil)
	// Storing one is not saying who may use it.
	l.want(403, bot, "PUT", "/v1/internal/secrets/NAS_PASSWORD", api.SetSecret{Scope: api.ScopeAll}, nil)

	// What carries the transcript masks the value: the page of messages (as Satori's messages and
	// as blocks), the state, the list, the stream.
	for _, call := range []struct {
		method string
		body   map[string]any
	}{
		{"message.list", map[string]any{"channel_id": its, "_data": map[string]any{"blocks": true}}},
		{"channel.list", map[string]any{"_data": map[string]any{"all": true}}},
	} {
		l.wantSat(200, "web", call.method, call.body, &raw)
		if strings.Contains(raw, val) || !strings.Contains(raw, "[secret:GITHUB_TOKEN]") {
			t.Fatalf("%s not masked: %.400s", call.method, raw)
		}
	}
	if raw = l.wantRaw(200, "web", "GET", "/v1/internal/sessions/"+its, nil); strings.Contains(raw, val) || !strings.Contains(raw, "[secret:GITHUB_TOKEN]") {
		t.Fatalf("the state not masked: %.400s", raw)
	}
	if ev := firstState(t, watchStates(t, l.base, nil, "session:"+its)); strings.Contains(ev, val) || !strings.Contains(ev, "[secret:GITHUB_TOKEN]") {
		t.Fatalf("stream not masked: %.400s", ev)
	}

	// Deleting is a person's at the host, never an agent's; then it is masked no more.
	l.want(403, "agent", "DELETE", "/v1/internal/secrets/GITHUB_TOKEN", nil, nil)
	l.want(200, "socket", "DELETE", "/v1/internal/secrets/GITHUB_TOKEN", nil, nil)
	l.want(422, "socket", "DELETE", "/v1/internal/secrets/GITHUB_TOKEN", nil, nil)
	l.wantSat(200, "web", "message.list", map[string]any{"channel_id": its, "_data": map[string]any{"blocks": true}}, &raw)
	if !strings.Contains(raw, val) {
		t.Fatalf("still masked after delete: %.300s", raw)
	}
}

// wantRaw is want, giving the reply's body as is.
func (l *live) wantRaw(code int, as, method, path string, body any) string {
	l.t.Helper()
	var raw string
	if got := l.do(as, method, path, body, &raw); got != code {
		l.t.Fatalf("%s %s as %.8s: %d, want %d: %s", method, path, as, got, code, raw)
	}
	return raw
}
