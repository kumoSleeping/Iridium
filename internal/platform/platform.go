// Package platform is what differs by operating system: how a holder is started away from the
// daemon, stopped and looked at (plan "平台相关代码单独一层"): systemd on Linux, a supervisor
// of each holder's own on macOS (Detached).
package platform

import (
	"context"
	"time"
)

// Unit is one holder to run away from the daemon, so restarting the daemon leaves it running.
type Unit struct {
	Name string   // unique while it runs: iridium-hold-<session>; tests use iridium-test-*
	Argv []string // the holder's command line, absolute path first
	// Runs after the holder ends, however it ends, with EXIT_CODE and EXIT_STATUS set as systemd
	// sets them for ExecStopPost.
	StopPost []string
	// How long a stop may take before everything left in the unit is killed.
	StopTimeout time.Duration
}

// State is what the platform knows of a unit. Exists is false once it is gone.
type State struct {
	Exists  bool
	Active  string // systemd's ActiveState: active, activating, deactivating, failed...
	MainPID int
}

// Launcher starts, stops and looks at holder units.
type Launcher interface {
	// Start returns once the holder runs. A unit of the same name must not exist.
	Start(ctx context.Context, u Unit) error
	// Stop asks the holder to end: SIGTERM, which closes the agent's stdin. It does not wait.
	Stop(ctx context.Context, name string) error
	// Kill sends sig to the holder alone (main) or to every process of the unit.
	Kill(ctx context.Context, name string, sig string, main bool) error
	State(ctx context.Context, name string) (State, error)
	// List names the units whose name starts with prefix.
	List(ctx context.Context, prefix string) ([]string, error)
}
