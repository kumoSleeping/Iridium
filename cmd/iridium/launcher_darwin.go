package main

import (
	"path/filepath"

	"github.com/kumoSleeping/iridium/internal/platform"
)

// launcher starts the holders: each under a supervisor of its own, left to launchd, so that a
// restart or an update of the daemon leaves them running; their files are in state/units.
func launcher(state string) platform.Launcher {
	return &platform.Detached{Dir: filepath.Join(state, "units")}
}
