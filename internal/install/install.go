// Package install is `iridium install`: the user systemd unit iridium.service that runs the daemon
// (plan "测试部署"), its config file and its state directory. It never starts the service: that
// is yours to do.
package install

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/kumoSleeping/iridium/internal/config"
)

type Options struct {
	Exe        string // the binary the unit runs
	ConfigPath string // the daemon's config file; made with the defaults if missing
	UnitDir    string // user units: ~/.config/systemd/user
	Unit       string // "" is iridium
	Path       string // the PATH the daemon and its agents run with; "" is none set
	// Systemctl runs systemctl --user with args; nil runs nothing.
	Systemctl func(args ...string) error
	Now       bool   // enable and start it too
	Linger    string // the file that says the user lingers; "" is /var/lib/systemd/linger/<user>
	Out       io.Writer
}

// Unit is the unit file.
func Unit(exe, configPath, path string) string {
	env := ""
	if path != "" {
		env = "Environment=" + quote("PATH="+path) + "\n"
	}
	return fmt.Sprintf(`# Written by iridium install.
[Unit]
Description=Iridium
After=network.target

[Service]
Type=exec
ExecStart=%s serve -config %s
%sRestart=on-failure
RestartSec=2
# The holders are units of their own (systemd-run --user): a restart or an update leaves the
# agents running.

[Install]
WantedBy=default.target
`, quote(exe), quote(configPath), env)
}

// quote quotes a word for a unit file when it needs it.
func quote(s string) string {
	if !strings.ContainsAny(s, " \t\"'\\%$;") {
		return s
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "%", "%%", "$", "$$").Replace(s) + `"`
}

func Install(o Options) error {
	if o.Unit == "" {
		o.Unit = "iridium"
	}
	if o.Out == nil {
		o.Out = io.Discard
	}
	if !filepath.IsAbs(o.Exe) || !filepath.IsAbs(o.ConfigPath) {
		return errors.New("install: the binary and the config need absolute paths")
	}
	cfg, err := config.Load(o.ConfigPath)
	if err != nil {
		return err
	}
	if _, err := os.Stat(o.ConfigPath); errors.Is(err, os.ErrNotExist) {
		if err := cfg.Save(o.ConfigPath); err != nil {
			return err
		}
		fmt.Fprintf(o.Out, "写了配置 %s（听 %s）。\n", o.ConfigPath, cfg.Listen)
	}
	if err := os.MkdirAll(cfg.StatePath(), 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(o.UnitDir, 0o755); err != nil {
		return err
	}
	unit := filepath.Join(o.UnitDir, o.Unit+".service")
	tmp := unit + ".tmp"
	if err := os.WriteFile(tmp, []byte(Unit(o.Exe, o.ConfigPath, o.Path)), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, unit); err != nil {
		return err
	}
	fmt.Fprintf(o.Out, "写了 %s：%s serve，状态在 %s。\n", unit, o.Exe, cfg.StatePath())
	if o.Systemctl != nil {
		steps := [][]string{{"daemon-reload"}}
		if o.Now {
			steps = append(steps, []string{"enable", "--now", o.Unit + ".service"})
		}
		for _, s := range steps {
			if err := o.Systemctl(s...); err != nil {
				return fmt.Errorf("systemctl --user %s: %w", strings.Join(s, " "), err)
			}
		}
	}
	if !o.Now {
		fmt.Fprintf(o.Out, "启动：systemctl --user enable --now %s.service\n", o.Unit)
	}
	linger := o.Linger
	if linger == "" {
		if u, err := user.Current(); err == nil {
			linger = filepath.Join("/var/lib/systemd/linger", u.Username)
		}
	}
	if _, err := os.Stat(linger); linger != "" && err != nil {
		fmt.Fprintf(o.Out, "注意：没开 linger，你退出登录后服务和托管的 agent 都会停。要一直跑：sudo loginctl enable-linger %s\n", filepath.Base(linger))
	}
	return nil
}
