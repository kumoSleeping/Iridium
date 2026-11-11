package server

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/cli"
	"github.com/kumoSleeping/iridium/internal/config"
)

// ir at another machine (docs/exec.md "远端的 ir"): its session's TOKEN makes it that machine's
// Bot, for that session; the session commands run, the Iridium's own machine's are refused, and
// what a Bot may not do (Claude, not being the owner's) neither may it.
func TestCLIAtMachine(t *testing.T) {
	const tok = "irs_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	var botID string
	l := startWith(t, func(s *Server, _ config.Config, _ string) {
		s.tokenFor = func(got string) (string, string, bool) { return botID, "remote-session", got == tok && botID != "" }
		s.CLI = func(args []string, stdout, stderr io.Writer, hc *http.Client, machine, session, cwd string) int {
			return cli.RunAt(args, stdout, stderr, hc, cli.At{Machine: machine, Session: session, Cwd: cwd})
		}
	})
	var b api.Token
	b = l.newApp("pc", false)
	botID = b.App.ID
	var own api.NewSessionReply
	l.want(200, "socket", "POST", "/v1/internal/sessions", api.NewSession{Agent: api.AgentClaude, Cwd: l.work, Text: "hello"}, &own)
	l.waitReply("socket", own.ID, "ok: hello")

	run := func(token string, args ...string) (int, string, string, int) {
		t.Helper()
		body, _ := json.Marshal(api.CLIRun{Args: args, Cwd: "/x"})
		req, _ := http.NewRequest("POST", l.base+"/v1/internal/cli", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := l.web.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out, errs strings.Builder
		exit := -1
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			var o api.CLIOutput
			json.Unmarshal(sc.Bytes(), &o)
			out.WriteString(o.Out)
			errs.WriteString(o.Err)
			if o.Exit != nil {
				exit = *o.Exit
			}
		}
		return resp.StatusCode, out.String(), errs.String(), exit
	}

	if code, out, errs, exit := run(tok, "api", "channel.list"); code != 200 || exit != 0 || !strings.Contains(out, own.ID[:8]) {
		t.Fatalf("channel.list: %d %d %q %q", code, exit, out, errs)
	}
	if _, _, errs, exit := run(tok, "sh", "pc", "--", "id"); exit != 1 || !strings.Contains(errs, "runs on the host") {
		t.Errorf("sh from there: %d %q", exit, errs)
	}
	if _, _, errs, exit := run(tok, "cp", "a", "b"); exit != 1 || errs == "" {
		t.Errorf("cp from there: %d %q", exit, errs)
	}
	// Claude is the owner's own terminals': that Bot is not.
	if _, _, errs, exit := run(tok, "api", "message.create", "channel_id="+own.ID[:8], "_data.text=hi"); exit != 1 || errs == "" {
		t.Errorf("send to Claude from a Bot not the owner's: %d %q", exit, errs)
	}
	if _, out, errs, exit := run(tok, "--help"); exit != 0 || !strings.Contains(out, "ir new") || strings.Contains(out, "ir secret") {
		t.Errorf("help there: %d %q %q", exit, out, errs)
	}
	// Not with a channel's own TOKEN, nor a session TOKEN no session has.
	if code, _, _, _ := run(b.Token, "api", "channel.list"); code != http.StatusForbidden {
		t.Errorf("/v1/internal/cli with the Bot's TOKEN: %d", code)
	}
	if code, _, _, _ := run("irs_BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", "api", "channel.list"); code != http.StatusUnauthorized {
		t.Errorf("an unknown session TOKEN: %d", code)
	}
	// Its Bot revoked: its sessions' TOKENs go with it.
	l.want(200, "socket", "DELETE", "/v1/internal/apps/"+botID, nil, nil)
	if code, _, _, _ := run(tok, "api", "channel.list"); code != http.StatusUnauthorized {
		t.Errorf("after the Bot is revoked: %d", code)
	}
}
