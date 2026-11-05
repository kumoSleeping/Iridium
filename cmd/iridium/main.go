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

	"github.com/kumoSleeping/iridium/internal/cli"
	"github.com/kumoSleeping/iridium/internal/config"
	"github.com/kumoSleeping/iridium/internal/hold"
	"github.com/kumoSleeping/iridium/internal/hub"
	"github.com/kumoSleeping/iridium/internal/platform"
	"github.com/kumoSleeping/iridium/internal/server"
	"github.com/kumoSleeping/iridium/internal/session"
	"github.com/kumoSleeping/iridium/internal/store"
)

const usage = `usage:
  iridium serve [-config <file>]   the daemon
  iridium hold <dir>               the holder of one agent process (the daemon starts it)
  iridium ir ...                   the CLI, as when run as ir
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
	m, err := session.New(session.Options{Config: cfg, Store: st, Hub: h, Launcher: platform.Systemd{}, UnitPrefix: cfg.UnitPrefix})
	if err != nil {
		log.Print(err)
		return 1
	}
	if err := m.Start(); err != nil {
		log.Print(err)
		return 1
	}
	defer m.Close()
	srv := server.New(cfg, m, h, nil)
	tcp, sock, err := srv.Listen()
	if err != nil {
		log.Print(err)
		return 1
	}
	log.Printf("iridium: serving http://%s and %s", tcp.Addr(), cfg.SocketPath())
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := srv.Serve(ctx, tcp, sock); err != nil {
		log.Print(err)
		return 1
	}
	return 0
}
