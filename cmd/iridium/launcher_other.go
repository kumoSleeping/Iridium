//go:build !linux && !windows

package main

import "github.com/kumoSleeping/iridium/internal/platform"

// launcher starts the holders. Without systemd they are children of the daemon (platform.Direct):
// they end with it. A launchd launcher for macOS is still to come.
func launcher() platform.Launcher { return &platform.Direct{} }
