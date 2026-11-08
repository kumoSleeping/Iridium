package main

import (
	"context"
	"fmt"
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

func macPlist() string { return launchd.File(launchd.Dir(macHome()), unit) }
func macLog() string   { return launchd.LogFile(macHome(), unit) }

// macService is the service's state in systemd's words (active, inactive; "" not installed), and
// whether it starts at login.
func macService() (string, bool) {
	_, err := os.Stat(macPlist())
	installed := err == nil
	ctx := context.Background()
	switch launchd.State(ctx, launchd.Domain(ctx), unit) {
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

func macStart(exe string) int {
	var env []string
	if h := os.Getenv("IRIDIUM_QQ_HOME"); h != "" {
		env = append(env, "IRIDIUM_QQ_HOME="+h)
	}
	path, err := launchd.Write(launchd.Dir(macHome()), launchd.Job{Label: unit, Argv: []string{exe, "run"}, Env: env, Log: macLog(), Always: true})
	if err != nil {
		return fail(err)
	}
	ctx := context.Background()
	if err := launchd.Load(ctx, launchd.Run, launchd.Domain(ctx), unit, path); err != nil {
		return fail(err)
	}
	fmt.Printf("服务已启动（%s）。\n", unit)
	return 0
}

// macStop unloads the service and takes its plist away, so it does not come back at login.
func macStop() int {
	if _, err := os.Stat(macPlist()); err != nil {
		fmt.Println("服务没装。")
		return 0
	}
	ctx := context.Background()
	launchd.Unload(ctx, launchd.Run, launchd.Domain(ctx), unit)
	if err := os.Remove(macPlist()); err != nil {
		return fail(err)
	}
	fmt.Println("服务已停止。")
	return 0
}

func macLogs(n string) int {
	cmd := exec.Command("tail", "-n", n, macLog())
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return 1
	}
	return 0
}
