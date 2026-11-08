//go:build !linux && !windows && !darwin

package main

import "github.com/kumoSleeping/iridium/internal/platform"

// launcher starts the holders. Without systemd or launchd they are children of the daemon
// (platform.Direct): they end with it.
func launcher(string) platform.Launcher { return &platform.Direct{} }
