// Command iridium on Windows is an executor only (docs/exec.md): `iridium join` (or ir join) runs
// agents for another Iridium. The daemon, the holder and the rest of ir are not built here.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kumoSleeping/iridium/internal/join"
	"github.com/kumoSleeping/iridium/internal/version"
)

const usage = `usage (on Windows Iridium only runs agents for another one):
  iridium join <address> [--name <name>] [--roots <dir>,...] [--install | --uninstall]
                                   this machine as an executor of that Iridium (iridium join --help)
  iridium version
`

func main() {
	args := os.Args[1:]
	exe := strings.ToLower(filepath.Base(os.Args[0]))
	if strings.TrimSuffix(exe, ".exe") != "ir" && len(args) > 0 && args[0] == "ir" {
		args = args[1:]
	}
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	switch args[0] {
	case "join":
		os.Exit(join.Main(args[1:], os.Stdout, os.Stderr))
	case "version", "--version":
		fmt.Println("iridium", version.Version)
		os.Exit(0)
	case "help", "-h", "--help":
		fmt.Print(usage)
		os.Exit(0)
	}
	fmt.Fprintf(os.Stderr, "iridium: no %q on Windows\n\n%s", args[0], usage)
	os.Exit(2)
}
