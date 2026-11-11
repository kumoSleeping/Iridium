// Package irhelp is ir's help: what ir --help prints, and its agent part, which every hosted
// agent's system prompt carries (session.SystemPrompt), so an agent need not run ir --help first.
package irhelp

import "strings"

// Help is ir --help: at most 20 lines (plan "ir"). The agent part is before Admin.
const Help = `ir: the host's Iridium on the command line (over its unix socket).
  ir api [METHOD] <path|satori.method> [k=v | k:=json | k==query]... [--wait]   Iridium's API as it is: ir api help
  ir run [--secret name,…] -- command …   a command line with secrets in it ({{name}}, {{file:name}}, env vars); their values masked in its output
Admin, a person at a terminal:
  ir public [off] | join <address> | pair <address> --name n --into file   go public through Cloudflare | join this computer to another Iridium | an app's TOKEN by pairing, into a file (--help on each)
`

// Agent is Help's agent part: the commands an agent uses, without Admin.
func Agent() string {
	h, _, _ := strings.Cut(Help, "Admin")
	return h
}

// Remote is the agent part for a session hosted at another computer (docs/exec.md "远端的 ir"):
// its ir reaches the Iridium over HTTPS with the session's own TOKEN, as that computer's app. The
// session commands as Help has them; a folder is this computer's; the host's files, command lines
// and secrets are not reached from here.
func Remote() string {
	var b strings.Builder
	b.WriteString("ir: the Iridium hosting this session, reached over HTTPS as this computer's app. A folder is this computer's.\n")
	for _, l := range strings.Split(Agent(), "\n") {
		if strings.HasPrefix(l, "  ir api ") {
			b.WriteString(l + "\n")
		}
	}
	b.WriteString("  Not from here: ir run, the routes marked socket only, the admin commands; ask a session on the host.\n")
	return b.String()
}
