// Command ir-computer makes this computer run agents for an Iridium (docs/exec.md): ir join as a
// program of its own, small, with its state in the status bar (Windows, macOS). The agents it
// starts get it as their ir (by that name it is the CLI; not on Windows).
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kumoSleeping/iridium/internal/join"
	"github.com/kumoSleeping/iridium/internal/version"
)

const usage = `usage: ir-computer <address> [--name name] [--roots dir,…] [--shell] [--headless] [--install | --uninstall]
  Run agents for an Iridium: pair once (a code to match on its page), then stay connected.
  --name      this computer's name there (default: the host name, 12 characters at most)
  --roots     the directories it may read and write files under (default: the home directory)
  --shell     command lines from that Iridium run here: a person's on its page and its agents' (ir sh);
              without it only the agents start here
  --headless  no icon in the status bar
  --install   start at login (Windows: a scheduled task; macOS: a LaunchAgent; Linux: a systemd user unit)
  --uninstall stop starting at login
`

func main() {
	if strings.TrimSuffix(strings.ToLower(filepath.Base(os.Args[0])), ".exe") == "ir" {
		os.Exit(irMain(os.Args[1:]))
	}
	if len(os.Args) == 2 {
		switch os.Args[1] {
		case "version", "--version":
			fmt.Println("ir-computer", version.Version)
			return
		case "help", "-h", "--help":
			fmt.Print(usage)
			return
		}
	}
	p := join.Program{Name: "ir-computer", Self: "ir-computer", Usage: usage, Tray: tray}
	os.Exit(p.Main(os.Args[1:], os.Stdout, os.Stderr))
}
