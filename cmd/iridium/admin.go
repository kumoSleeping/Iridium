package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/config"
	"github.com/kumoSleeping/iridium/internal/hub"
	"github.com/kumoSleeping/iridium/internal/install"
	"github.com/kumoSleeping/iridium/internal/node"
	"github.com/kumoSleeping/iridium/internal/update"
	"github.com/kumoSleeping/iridium/internal/version"
)

// installCmd is `iridium install`: the user unit iridium.service, the config and the state
// directory. It starts nothing unless -now.
func installCmd(args []string) int {
	fs := flag.NewFlagSet("install", flag.ExitOnError)
	path := fs.String("config", config.DefaultPath(), "the config file the service reads")
	now := fs.Bool("now", false, "enable and start the service too")
	listen := fs.String("listen", "", "the address a config made now listens on (default 127.0.0.1:7810)")
	reload := fs.Bool("reload", true, "run systemctl --user daemon-reload")
	unit := fs.String("unit", "iridium", "the unit's name (tests: iridium-test-*)")
	home, _ := os.UserHomeDir()
	prefix := fs.String("prefix", filepath.Join(home, ".local", "opt", "iridium"), "where the binary is kept (copied there when it runs from elsewhere)")
	bin := fs.String("bin", filepath.Join(home, ".local", "bin"), `where the ir link goes; "" none`)
	var env envFlag
	fs.Var(&env, "env", "NAME=VALUE the service runs with too (repeatable)")
	fs.Parse(args)
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err == nil {
		exe, err = install.Place(exe, *prefix, *bin)
	}
	if err != nil {
		log.Print(err)
		return 1
	}
	cfgPath, _ := filepath.Abs(*path)
	unitDir := filepath.Join(home, ".config", "systemd", "user")
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		unitDir = filepath.Join(d, "systemd", "user")
	}
	o := install.Options{Exe: exe, ConfigPath: cfgPath, Listen: *listen, UnitDir: unitDir, Unit: *unit, Path: os.Getenv("PATH"), Env: env, Now: *now, Out: os.Stdout}
	if *reload || *now {
		o.Systemctl = func(args ...string) error {
			cmd := exec.Command("systemctl", append([]string{"--user"}, args...)...)
			cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
			return cmd.Run()
		}
	}
	if err := install.Install(o); err != nil {
		log.Print(err)
		return 1
	}
	return 0
}

// pushCmd is `iridium push [-i key] [-listen addr] <user@host>`.
func pushCmd(args []string) int {
	fs := flag.NewFlagSet("push", flag.ExitOnError)
	key := fs.String("i", "", "the ssh identity")
	listen := fs.String("listen", "", "the address a first install there listens on")
	fs.Parse(args)
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: iridium push [-i <key>] [-listen <addr>] <user@host>")
		return 2
	}
	if err := node.Push(context.Background(), node.PushOptions{Target: fs.Arg(0), Key: *key, Listen: *listen, Out: os.Stdout}); err != nil {
		log.Print(err)
		return 1
	}
	return 0
}

// nodeCmd is `iridium node new <account> [-listen addr]`.
func nodeCmd(args []string) int {
	if len(args) < 2 || args[0] != "new" {
		fmt.Fprintln(os.Stderr, "usage: iridium node new <account> [-listen <addr>]")
		return 2
	}
	fs := flag.NewFlagSet("node new", flag.ExitOnError)
	listen := fs.String("listen", "", "its address (default the first free 127.0.0.1 port from 7811)")
	fs.Parse(args[2:])
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: iridium node new <account> [-listen <addr>]")
		return 2
	}
	if err := node.New(context.Background(), node.NewOptions{User: args[1], Listen: *listen, Out: os.Stdout}); err != nil {
		log.Print(err)
		return 1
	}
	return 0
}

// envFlag is -env NAME=VALUE, once for each.
type envFlag []string

func (e *envFlag) String() string     { return strings.Join(*e, " ") }
func (e *envFlag) Set(v string) error { *e = append(*e, v); return nil }

// newUpdater looks for new versions where the config says, carrying its state on h.
func newUpdater(cfg config.Config, h *hub.Hub) *update.Updater {
	return update.New(update.Options{
		Source:  cfg.Update.Source(update.DefaultSource),
		Every:   cfg.Update.Every.D(),
		Current: version.Version,
		Auto:    version.Release(), // a build from source is not replaced unasked
		Publish: func(s api.UpdateState) { h.Set("update", s) },
		Restart: func() error {
			// After the reply that says so has gone out.
			time.AfterFunc(500*time.Millisecond, func() {
				if err := restart(); err != nil {
					log.Printf("update: restart: %v", err)
				}
			})
			return nil
		},
		Logf: log.Printf,
	})
}

// restart restarts the daemon with the new binary: its systemd unit, if it runs in one, else this
// process in place. The holders run in units of their own and keep running either way.
func restart() error {
	if unit := ownUnit(); unit != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return exec.CommandContext(ctx, "systemctl", "--user", "restart", "--no-block", unit).Run()
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return syscall.Exec(exe, os.Args, os.Environ())
}

// ownUnit is the user service this process runs in, from its cgroup; "" for none.
func ownUnit() string {
	if os.Getenv("INVOCATION_ID") == "" {
		return ""
	}
	f, err := os.Open("/proc/self/cgroup")
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		_, path, _ := strings.Cut(sc.Text(), "::")
		if name := filepath.Base(path); strings.HasSuffix(name, ".service") && strings.Contains(path, "/user@") {
			return name
		}
	}
	return ""
}

func versionCmd() int {
	fmt.Println("iridium", version.Version)
	return 0
}
