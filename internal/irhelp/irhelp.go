// Package irhelp is ir's help: what ir --help prints, and its agent part, which every hosted
// agent's system prompt carries (session.SystemPrompt), so an agent need not run ir --help first.
package irhelp

import "strings"

// Help is ir --help: at most 20 lines (plan "ir"). The agent part is before Admin.
const Help = `ir: this machine's Iridium on the command line (over its unix socket). An <id> may be its first few characters.
  ir new <agent> [dir] "task"         start a session, print its ID (--model <model>, --approve <mode>)
  ir send <id> "message" [-a file]...  send a message, with attachments
  ir wait <id>                        wait for the turn to end, print its last reply
  ir read <id> [-n count] [--full]    each message and its final reply, cut short (last 10; --full: the page as written)
  ir ls | find <word> [--all]         list sessions | find by name, title, last thing said (--all: archived, finished subagents too)
  ir stop | archive | unarchive <id>  stop its agent | archive | unarchive
  ir ws [ls] | rename <group> <name>   folder groups, made as sessions go in their folders (<group>: ID or name)
  ir cp <src> <dst> | files <dir>     copy files, print path, size, sha256 | list one level: name, size, time, type
  ir secret ls | set <name> [--note text] | rm <name>   secrets: set has the user type the value on the web page
  ir run [--secret name,…] -- command …   {{name}}, {{file:name}} become the value, a temp file, and env vars; values in the output are masked
  ir usage [name|me] [-d days] [--by day,terminal,agent,model]   tokens and cost per day, terminal, agent, model
  new and send take --wait to wait for the reply. send during a turn joins that turn (an agent's always does; a person at a terminal may add --queue to run it after)
  Exit codes: 0 done, 2 needs the user (a question, an approval), 1 error or exited. A path may be machine:path
Admin (an agent calling these never sees a TOKEN; a web panel confirms):
  ir invite [name] | token <name>     a link for a new web panel | a plugin TOKEN
    --owner: the user's own terminal, with Claude
  ir revoke <name> | profile <name> [--name new] [--avatar image] [--device computer|laptop|phone|none]
  ir public [off] | update [--check]  go public through Cloudflare (run in a terminal) | update Iridium
`

// Agent is Help's agent part: the commands an agent uses, without Admin.
func Agent() string {
	h, _, _ := strings.Cut(Help, "Admin")
	return h
}
