// Package irhelp is ir's help: what ir --help prints, and its agent part, which every hosted
// agent's system prompt carries (session.SystemPrompt), so an agent need not run ir --help first.
package irhelp

import "strings"

// Help is ir --help: at most 20 lines (plan "ir"). The agent part is before Admin.
const Help = `ir: this machine's Iridium on the command line (over its unix socket). An <id> may be its first few characters.
  ir new <agent> [place] "task"       start a session, print its ID (--model, --approve <mode>, --workspace <group>); place: a folder, app:path at that app's computer, host:path
  ir send <id> "message" [-a file]...  send a message, with attachments
  ir wait <id>                        wait for the turn to end, print its last reply
  ir read <id> [-n count] [--full]    each message and its final reply, cut short (last 10; --full: the page as written)
  ir ls | find <word> [--all]         list sessions | find by name, title, last thing said (--all: archived, finished subagents too; ls --here: your group's)
  ir stop | archive | unarchive <id> | rename [<id>] <name>   stop its agent | archive | unarchive | name it (no <id>: your own session)
  ir ws [ls] | rename <group> <name>   folder groups, made as sessions go in their folders (<group>: ID or name)
  ir cp <src> <dst> | files <dir> | attach <file>...   copy files, print path, size, sha256 | list one level: name, size, time, type | its ir: link, the Markdown to put the file in your reply
  ir machines | sh <app>[:dir] -- command   other computers connected here | run a command line on one (--cwd dir, --timeout 2m)
  ir secret ls | set <name> [--note text] | rm <name>   secrets: set has the user type the value on the web page
  ir run [--secret name,…] -- command …   {{name}}, {{file:name}} become the value, a temp file, and env vars; values in the output are masked
  ir usage [name|me] [-d days] [--by day,terminal,agent,model]   tokens and cost per day, terminal, agent, model
  new and send take --wait to wait for the reply. send during a turn joins that turn (an agent's always does; a person at a terminal may add --queue to run it after). Exit codes: 0 done, 2 needs the user (a question, an approval), 1 error or exited. A path may be app:path (host:path, the host's)
  ir bot "message" [--to bot]         tell the user through a bot (QQ); to add one: the web's 终端管理 +, or iridium-qq setup (--new for one more; a QR image: ir attach it; run the same again once scanned)
Admin (an agent calling these never sees a secret; a person on the web or the host confirms):
  ir apps [logins [app]] | rename <app> <name> | rm <app>   apps, the host and ir_web | their logins | rename | remove (a login: app/login)
  ir invite [name] [--claude] | profile <app> [--avatar image] [--device computer|laptop|phone|none]   a link for a new browser (--claude: the user's own) | avatar, device
  ir public [off] | update [--check] | join <address> | pair <address> --name n --into file   go public through Cloudflare (run in a terminal) | update Iridium | run agents for another Iridium | a Bot's TOKEN from it, by its panel's approval (--help on each)
`

// Agent is Help's agent part: the commands an agent uses, without Admin.
func Agent() string {
	h, _, _ := strings.Cut(Help, "Admin")
	return h
}

// Remote is the agent part for a session hosted at another machine (docs/exec.md "远端的 ir"):
// its ir reaches the Iridium over HTTPS with the session's own TOKEN, as that machine's Bot. The
// session commands as Help has them; a folder is this machine's; the Iridium's own files, command
// lines, secrets and bots are not reached from here.
func Remote() string {
	var b strings.Builder
	b.WriteString("ir: the Iridium hosting this session, reached over HTTPS as this machine. An <id> may be its first few characters. A folder is this machine's.\n")
	for _, l := range strings.Split(Agent(), "\n") {
		for _, p := range []string{"  ir new ", "  ir send ", "  ir wait ", "  ir read ", "  ir ls ", "  ir stop ", "  ir ws ", "  new and send "} {
			if strings.HasPrefix(l, p) {
				if p == "  new and send " {
					l, _, _ = strings.Cut(l, ". A path may be")
				}
				b.WriteString(l + "\n")
			}
		}
	}
	b.WriteString("  ir machines                        the machines connected to it\n")
	b.WriteString("  Not from here: cp, files, attach, sh, run, secret, bot, usage and the admin commands; ask a session on the Iridium's machine.\n")
	return b.String()
}
