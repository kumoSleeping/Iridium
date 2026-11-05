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
	"github.com/kumoSleeping/iridium/internal/update"
	"github.com/kumoSleeping/iridium/internal/version"
)

// installCmd is `iridium install`: the user unit iridium.service, the config and the state
// directory. It starts nothing unless -now.
func installCmd(args []string) int {
	fs := flag.NewFlagSet("install", flag.ExitOnError)
	path := fs.String("config", config.DefaultPath(), "the config file the service reads")
	now := fs.Bool("now", false, "enable and start the service too")
	reload := fs.Bool("reload", true, "run systemctl --user daemon-reload")
	fs.Parse(args)
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		log.Print(err)
		return 1
	}
	cfgPath, _ := filepath.Abs(*path)
	home, _ := os.UserHomeDir()
	unitDir := filepath.Join(home, ".config", "systemd", "user")
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		unitDir = filepath.Join(d, "systemd", "user")
	}
	o := install.Options{Exe: exe, ConfigPath: cfgPath, UnitDir: unitDir, Path: os.Getenv("PATH"), Now: *now, Out: os.Stdout}
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

// newUpdater looks for new versions where the config says, carrying its state on h.
func newUpdater(cfg config.Config, h *hub.Hub) *update.Updater {
	return update.New(update.Options{
		Source:  cfg.Update.URL,
		Every:   cfg.Update.Every.D(),
		Current: version.Version,
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
