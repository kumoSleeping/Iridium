// Command iridium is the whole of Iridium in one binary: `iridium serve` is the daemon,
// `iridium hold` the holder it starts for each agent process, and run as ir it is the CLI.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"

	"github.com/kumoSleeping/iridium/internal/chat"
	"github.com/kumoSleeping/iridium/internal/cli"
	"github.com/kumoSleeping/iridium/internal/config"
	"github.com/kumoSleeping/iridium/internal/discover"
	"github.com/kumoSleeping/iridium/internal/hold"
	"github.com/kumoSleeping/iridium/internal/hub"
	"github.com/kumoSleeping/iridium/internal/platform"
	"github.com/kumoSleeping/iridium/internal/push"
	"github.com/kumoSleeping/iridium/internal/server"
	"github.com/kumoSleeping/iridium/internal/session"
	"github.com/kumoSleeping/iridium/internal/store"
	"github.com/kumoSleeping/iridium/internal/version"
)

const usage = `usage:
  iridium serve [-config <file>]   the daemon
  iridium hold <dir>               the holder of one agent process (the daemon starts it)
  iridium ir ...                   the CLI, as when run as ir
  iridium install [-config <file>] [-now]   the user unit iridium.service (started only with -now)
  iridium version
`

func main() {
	if filepath.Base(os.Args[0]) == "ir" {
		os.Exit(cli.Main(os.Args[1:]))
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
		os.Exit(cli.Main(os.Args[2:]))
	case "install":
		os.Exit(installCmd(os.Args[2:]))
	case "version", "--version":
		os.Exit(versionCmd())
	}
	fmt.Fprint(os.Stderr, usage)
	os.Exit(2)
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
	m, err := session.New(session.Options{Config: cfg, Store: st, Hub: h, Launcher: platform.Systemd{}, UnitPrefix: cfg.UnitPrefix, Discovery: d, Tag: tag})
	if err != nil {
		log.Print(err)
		return 1
	}
	d.SetOnChange(m.Refresh)
	d.SetAlias(m.AgentID)
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
	srv.ConfigPath = *path
	srv.Updater = newUpdater(cfg, h)
	h.Set("update", srv.Updater.State())
	// Web Push's total switch is the config's: turning it writes the file.
	srv.Push, err = push.Open(push.Options{StateDir: state, Store: st, Manager: m, Enabled: cfg.Push, Save: func(on bool) error {
		c, err := config.Load(*path)
		if err != nil {
			return err
		}
		c.Push = on
		return c.Save(*path)
	}})
	if err != nil {
		log.Print(err)
		return 1
	}
	srv.Chat, err = chat.New(chat.Options{Manager: m, Store: st, StateDir: state, ChatDir: cfg.ChatDir, Guard: srv.ChatGuard()})
	if err != nil {
		log.Print(err)
		return 1
	}
	defer srv.Chat.Close()
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
	if err := srv.Serve(ctx, tcp, sock); err != nil {
		log.Print(err)
		return 1
	}
	return 0
}
