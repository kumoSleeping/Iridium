// Command iridium is the whole of Iridium in one binary: with no arguments (or `iridium app`) the
// desktop app (internal/desktop), `iridium serve` the daemon alone, `iridium hold` the holder it
// starts for each agent process, and run as ir it is the CLI.
// `iridium join` (ir join) makes the machine an executor of another Iridium.
package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/cli"
	"github.com/kumoSleeping/iridium/internal/config"
	"github.com/kumoSleeping/iridium/internal/desktop"
	"github.com/kumoSleeping/iridium/internal/discover"
	"github.com/kumoSleeping/iridium/internal/harness"
	hdsh "github.com/kumoSleeping/iridium/internal/harness/dsh"
	"github.com/kumoSleeping/iridium/internal/hold"
	"github.com/kumoSleeping/iridium/internal/hub"
	"github.com/kumoSleeping/iridium/internal/join"
	"github.com/kumoSleeping/iridium/internal/machineid"
	"github.com/kumoSleeping/iridium/internal/platform"
	"github.com/kumoSleeping/iridium/internal/pricing"
	"github.com/kumoSleeping/iridium/internal/remote"
	"github.com/kumoSleeping/iridium/internal/server"
	"github.com/kumoSleeping/iridium/internal/session"
	"github.com/kumoSleeping/iridium/internal/store"
	"github.com/kumoSleeping/iridium/internal/version"
)

// usage is iridium's; ir's own commands are in its help (internal/irhelp) and runtimeUsage.
const usage = `usage: ir <command>   (iridium is the same program: iridium ir <command>)
` + runtimeUsage + `
  iridium                          the window (from the app's icon), ir started under it
  iridium install [options] [-public <host>], iridium uninstall
                                   the daemon alone as the user's service (a server), as before ir
                                   start; -public: on the internet through Cloudflare
  iridium push [-i <key>] [-listen <addr>] <user@host>
                                   this binary onto another account or machine, installed or restarted
  iridium node new <account> [-listen <addr>]
                                   a new account on this machine with an Iridium of its own (root once)
`

func main() {
	switch strings.TrimSuffix(strings.ToLower(filepath.Base(os.Args[0])), ".exe") {
	case "ir":
		os.Exit(irMain(os.Args[1:]))
	case "gh":
		// An agent's gh (ghenv): the real one, as the person its turn is for.
		os.Exit(irMain(append([]string{"github", "gh"}, os.Args[1:]...)))
	}
	args := os.Args[1:]
	// Opened from the Finder, an older macOS adds -psn_<process serial number>.
	if len(args) == 1 && strings.HasPrefix(args[0], "-psn_") {
		args = nil
	}
	if len(args) == 0 {
		if desktop.CanShow() {
			os.Exit(desktop.Main(desktop.Options{}))
		}
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if code, ok := runtimeCmd(args); ok {
		os.Exit(code)
	}
	if strings.HasPrefix(args[0], "-") && args[0] != "-h" && args[0] != "--help" && args[0] != "--version" {
		os.Exit(startCmd(args)) // iridium [options]: ir start [options]
	}
	switch args[0] {
	case "run":
		os.Exit(runtimeMain(args[1:]))
	case "app":
		os.Exit(desktop.Main(desktop.Options{}))
	case "hold":
		os.Exit(hold.Main(args[1:]))
	case "ir":
		os.Exit(irMain(args[1:]))
	case "install":
		os.Exit(installCmd(args[1:]))
	case "uninstall":
		os.Exit(uninstallCmd(args[1:]))
	case "push":
		os.Exit(pushCmd(args[1:]))
	case "node":
		os.Exit(nodeCmd(args[1:]))
	case "help", "-h", "--help":
		fmt.Print(usage)
		os.Exit(0)
	// Older words, kept for what runs them (services, scripts): not in the usage.
	case "version", "--version":
		os.Exit(versionCmd())
	case "headless":
		os.Exit(run(append([]string{"--no-server"}, joinArgs(args[1:])...), false))
	}
	fmt.Fprint(os.Stderr, usage)
	os.Exit(2)
}

// irMain is ir: join here, the rest the CLI's.
func irMain(args []string) int {
	if code, ok := runtimeCmd(args); ok {
		return code
	}
	if len(args) > 0 && args[0] == "pair" {
		return join.PairMain(args[1:], os.Stdout, os.Stderr)
	}
	return cli.Main(args)
}

// serve is this computer's Iridium, the daemon, with its config file at path, till a signal.
func serve(cfgPath string) int {
	path := &cfgPath
	cfg, err := config.Load(*path)
	if err != nil {
		log.Print(err)
		return 1
	}
	state := cfg.StatePath()
	if err := os.MkdirAll(state, 0o700); err != nil {
		log.Print(err)
		return 1
	}
	// One daemon per state directory.
	unlock, err := platform.TryLock(filepath.Join(state, "daemon.lock"))
	if err != nil {
		log.Printf("another daemon runs on %s: %v", state, err)
		return 1
	}
	defer unlock()

	st, err := store.Open(filepath.Join(state, "iridium.db"))
	if err != nil {
		log.Print(err)
		return 1
	}
	defer st.Close()
	h := hub.New()
	do := discover.Defaults(cfg)
	do.Store, do.Hub = st, h
	d := discover.New(do)
	var srv *server.Server // names terminals in source tags, once it is there
	tag := func(id string) string { return srv.Tag(id) }
	owner := func(id string) string { return srv.Owner(id) }
	executors := &remote.Hub{Logf: log.Printf, Self: machineid.Hash()}
	// The harnesses' own channels (internal/harness): only here is an agent's own code wired in.
	dshChannel := hdsh.New(hdsh.Options{Remote: executors, Logf: log.Printf})
	defer dshChannel.Close()
	channels := harness.Set{api.AgentDsh: dshChannel}
	m, err := session.New(session.Options{Config: cfg, Store: st, Hub: h, Launcher: launcher(state), UnitPrefix: cfg.UnitPrefix, Discovery: d, Tag: tag, Owner: owner, Remote: executors, Channels: channels})
	if err != nil {
		log.Print(err)
		return 1
	}
	d.SetOnChange(m.Refresh)
	d.SetAlias(m.AgentID)
	d.SetRemote(executors)
	if err := d.Start(); err != nil {
		log.Print(err)
		return 1
	}
	defer d.Close()
	if err := m.Start(); err != nil {
		log.Print(err)
		return 1
	}
	defer m.Close()
	srv = server.New(cfg, m, h, nil)
	defer srv.Close()
	srv.Remote = executors
	srv.WatchMachines()
	srv.PrefetchBinaries()
	srv.ConfigPath = *path
	srv.CLI = func(args []string, stdout, stderr io.Writer, hc *http.Client, machine, session, cwd string) int {
		return cli.RunAt(args, stdout, stderr, hc, cli.At{Machine: machine, Session: session, Cwd: cwd})
	}
	srv.Updater = newUpdater(cfg, h)
	h.Set("update", srv.Updater.State())
	srv.Prices = pricing.Open(state)
	home, _ := os.UserHomeDir()
	srv.Discover, srv.Folders = d, discover.DefaultFolders(home, state, filepath.Dir(*path))
	if len(cfg.FolderRoots) > 0 {
		srv.Folders.Roots = cfg.FolderRoots
	}
	tcp, sock, err := srv.Listen()
	if err != nil {
		log.Print(err)
		return 1
	}
	log.Printf("iridium %s: serving http://%s and %s", version.Version, tcp.Addr(), cfg.SocketPath())
	dumpStacksOn(state)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go srv.Updater.Clean()
	go srv.Prices.Run(ctx)
	if err := srv.Serve(ctx, tcp, sock); err != nil {
		log.Print(err)
		return 1
	}
	return 0
}
