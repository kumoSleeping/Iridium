package main

import (
	"context"
	"os"
	"os/exec"
	"runtime"

	"github.com/kumoSleeping/iridium/internal/launchd"
)

// onMac: the service is a LaunchAgent (~/Library/LaunchAgents/<unit>.plist), not a systemd unit.
var onMac = runtime.GOOS == "darwin"

func macHome() string {
	h, _ := os.UserHomeDir()
	return h
}

func macPlist(u string) string { return launchd.File(launchd.Dir(macHome()), u) }
func macLog(u string) string   { return launchd.LogFile(macHome(), u) }

// macService is service u's state in systemd's words (active, inactive; "" not installed), and
// whether it starts at login.
func macService(u string) (string, bool) {
	_, err := os.Stat(macPlist(u))
	installed := err == nil
	ctx := context.Background()
	switch launchd.State(ctx, launchd.Domain(ctx), u) {
	case "running":
		return "active", installed
	case "":
		if !installed {
			return "", false
		}
		return "inactive", installed
	}
	return "inactive", installed
}

func macStart(u, exe, id string) error {
	var env []string
	if h := os.Getenv("IRIDIUM_QQ_HOME"); h != "" {
		env = append(env, "IRIDIUM_QQ_HOME="+h)
	}
	path, err := launchd.Write(launchd.Dir(macHome()), launchd.Job{Label: u, Argv: []string{exe, "run", "--bot", id}, Env: env, Log: macLog(u), Always: true})
	if err != nil {
		return err
	}
	ctx := context.Background()
	return launchd.Load(ctx, launchd.Run, launchd.Domain(ctx), u, path)
}

// macStop unloads service u and takes its plist away, so it does not come back at login.
func macStop(u string) error {
	if _, err := os.Stat(macPlist(u)); err != nil {
		return nil
	}
	ctx := context.Background()
	launchd.Unload(ctx, launchd.Run, launchd.Domain(ctx), u)
	return os.Remove(macPlist(u))
}

func macLogs(u, n string) int {
	cmd := exec.Command("tail", "-n", n, macLog(u))
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return 1
	}
	return 0
}
