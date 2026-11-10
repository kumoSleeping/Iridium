// Package cli is `ir`, the command line agents and you use to reach the local daemon over its
// unix socket (plan "ir"), with the same API as the web page. The commands that manage
// interactive terminals, ir public and ir update are in admin.go.
package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/kumoSleeping/iridium/internal/irhelp"
)

// Help is ir --help (irhelp.Help).
const Help = irhelp.Help

// Main runs ir with args, giving the exit code.
// At another machine, in a session hosted there (IRIDIUM_URL and the session's own TOKEN in its
// environment), ir goes to that Iridium over HTTPS (remote.go).
func Main(args []string) int {
	if url, tok := os.Getenv("IRIDIUM_URL"), os.Getenv("IRIDIUM_TOKEN"); url != "" && strings.HasPrefix(tok, "irs_") {
		return ForwardAt(url, tok, args, os.Stdout, os.Stderr)
	}
	return run(args, os.Stdout, os.Stderr)
}

// commands are ir's commands by name. Each gets its arguments after the name.
var commands = map[string]func(e *env, args []string) int{
	"new":       cmdNew,
	"send":      cmdSend,
	"share":     cmdShare, // share.go
	"wait":      cmdWait,
	"read":      cmdRead,
	"ls":        cmdLs,
	"find":      cmdFind,
	"stop":      cmdStop,
	"archive":   func(e *env, args []string) int { return cmdArchive(e, args, true) },
	"unarchive": func(e *env, args []string) int { return cmdArchive(e, args, false) },
	"rename":    cmdRename,
	"cp":        cmdCp,
	"files":     cmdFiles,
	"attach":    cmdAttach,   // files.go
	"ws":        cmdWs,       // workspaces.go
	"machines":  cmdMachines, // shell.go
	"sh":        cmdSh,
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Fprint(stdout, Help)
		return 0
	}
	if admin, ok := adminCommands[args[0]]; ok { // admin.go
		e, err := detect()
		if err != nil {
			return failed(stderr, args[0], err)
		}
		return admin(e, args[1:], stdout, stderr)
	}
	cmd := commands[args[0]]
	if cmd == nil {
		fmt.Fprintf(stderr, "ir: no command %q\n\n%s", args[0], Help)
		return 1
	}
	return cmd(newEnv(stdout, stderr), args[1:])
}
