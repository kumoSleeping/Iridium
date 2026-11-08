package main

import "github.com/kumoSleeping/iridium/internal/platform"

// launcher starts the holders: systemd user units.
func launcher(string) platform.Launcher { return platform.Systemd{} }
