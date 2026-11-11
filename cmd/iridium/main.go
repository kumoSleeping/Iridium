//go:build !windows

// Command iridium is the whole of Iridium in one binary: `iridium serve` is the daemon,
// `iridium hold` the holder it starts for each agent process, and run as ir it is the CLI.
// `iridium join` (ir join) makes the machine an executor of another Iridium; on Windows that is
// all it does (main_windows.go).
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/cli"
	"github.com/kumoSleeping/iridium/internal/config"
	"github.com/kumoSleeping/iridium/internal/discover"
	"github.com/kumoSleeping/iridium/internal/harness"
	hdsh "github.com/kumoSleeping/iridium/internal/harness/dsh"
	"github.com/kumoSleeping/iridium/internal/hold"
	"github.com/kumoSleeping/iridium/internal/hub"
	"github.com/kumoSleeping/iridium/internal/join"
	"github.com/kumoSleeping/iridium/internal/machineid"
	"github.com/kumoSleeping/iridium/internal/pricing"
	"github.com/kumoSleeping/iridium/internal/push"
	"github.com/kumoSleeping/iridium/internal/remote"
	"github.com/kumoSleeping/iridium/internal/server"
	"github.com/kumoSleeping/iridium/internal/session"
	"github.com/kumoSleeping/iridium/internal/store"
	"github.com/kumoSleeping/iridium/internal/version"
)

const usage = `usage:
  iridium serve [-config <file>]   the daemon
  iridium hold <dir>               the holder of one agent process (the daemon starts it)
  iridium ir ...                   the CLI, as when run as ir
  iridium install [-config <file>] [-unit <name>] [-listen <addr>] [-env NAME=VALUE]... [-now]
                                   the user unit iridium.service (started only with -now)
  iridium push [-i <key>] [-listen <addr>] <user@host>
                                   this binary onto another account or machine, installed or restarted
  iridium node new <account> [-listen <addr>]
                                   a new account on this machine with an Iridium instance of its own (root once: sudo or su)
  iridium join <address> [--name <name>] [--roots <dir>,...] [--install | --uninstall]
                                   this machine as an executor of another Iridium (ir join)
  iridium version
`

func main() {
	if filepath.Base(os.Args[0]) == "ir" {
		os.Exit(irMain(os.Args[1:]))
	}
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	switch os.Args[1] {
	case "serve":
		os.Exit(serve(os.Args[2:]))
	case "hold":
		os.Exit(hold.Main(os.Args[2:]))
	case "ir":
		os.Exit(irMain(os.Args[2:]))
	case "join":
		os.Exit(join.Main(os.Args[2:], os.Stdout, os.Stderr))
	case "install":
		os.Exit(installCmd(os.Args[2:]))
	case "push":
		os.Exit(pushCmd(os.Args[2:]))
	case "node":
		os.Exit(nodeCmd(os.Args[2:]))
	case "version", "--version":
		os.Exit(versionCmd())
	}
	fmt.Fprint(os.Stderr, usage)
	os.Exit(2)
}

// irMain is ir: join here, the rest the CLI's.
func irMain(args []string) int {
	if len(args) > 0 && args[0] == "join" {
		return join.Main(args[1:], os.Stdout, os.Stderr)
	}
	if len(args) > 0 && args[0] == "pair" {
		return join.PairMain(args[1:], os.Stdout, os.Stderr)
	}
	return cli.Main(args)
}

func serve(args []string) int {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	path := fs.String("config", config.DefaultPath(), "the config file")
	fs.Parse(args)
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
	lock, err := os.OpenFile(filepath.Join(state, "daemon.lock"), os.O_RDWR|os.O_CREATE, 0o600)
	if err == nil {
		err = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	}
	if err != nil {
		log.Printf("another daemon runs on %s: %v", state, err)
		return 1
	}
	defer lock.Close()

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
	executors := &remote.Hub{Logf: log.Printf, Self: machineid.Hash()}
	// The harnesses' own channels (internal/harness): only here is an agent's own code wired in.
	dshChannel := hdsh.New(hdsh.Options{Remote: executors, Logf: log.Printf})
	defer dshChannel.Close()
	channels := harness.Set{api.AgentDsh: dshChannel}
	m, err := session.New(session.Options{Config: cfg, Store: st, Hub: h, Launcher: launcher(state), UnitPrefix: cfg.UnitPrefix, Discovery: d, Tag: tag, Remote: executors, Channels: channels})
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
	srv.ConfigPath = *path
	srv.CLI = func(args []string, stdout, stderr io.Writer, hc *http.Client, machine, session, cwd string) int {
		return cli.RunAt(args, stdout, stderr, hc, cli.At{Machine: machine, Session: session, Cwd: cwd})
	}
	srv.Updater = newUpdater(cfg, h)
	h.Set("update", srv.Updater.State())
	srv.Prices = pricing.Open(state)
	// Web Push's total switch is the config's: turning it writes the file (with the settings').
	srv.Push, err = push.Open(push.Options{StateDir: state, Store: st, Manager: m, Enabled: cfg.Push, Mask: srv.MaskText, Sees: srv.LoginSees, Save: func(on bool) error {
		return srv.SaveConfig(func(c *config.Config) { c.Push = on })
	}})
	if err != nil {
		log.Print(err)
		return 1
	}
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
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go srv.Updater.Run(ctx)
	go srv.Prices.Run(ctx)
	if err := srv.Serve(ctx, tcp, sock); err != nil {
		log.Print(err)
		return 1
	}
	return 0
}
