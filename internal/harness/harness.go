// Package harness is the core's side of a harness's own channel: what the session layer asks the
// code that is one agent's alone (dsh's, internal/harness/dsh) instead of naming that agent. The
// core never knows which agent it asks; a channel is registered for its agent at wiring
// (cmd/iridium) and has only the capabilities it implements (Stater, Sender, Setter…), as a
// driver its optional interfaces (agent.Efforter…). A capability is added here only where the
// core has a decision to make (a state shown, a message sent, a setting set, a launch, a picture
// shown), shaped by that decision, not by one harness's protocol (docs/exec.md "通道").
package harness

import (
	"context"
	"errors"

	"github.com/kumoSleeping/iridium/internal/agent"
	"github.com/kumoSleeping/iridium/internal/api"
)

// The core's errors, as a channel says them: the session layer's are these.
var (
	ErrBadRequest   = errors.New("bad request")
	ErrOffline      = errors.New("the machine is not connected")
	ErrNotInstalled = errors.New("agent not installed")
	ErrGone         = errors.New("no longer there")
)

// Ref is a session as a channel sees it.
type Ref struct {
	ID      string // Iridium's
	As      string // the agent's own id for it (Codex's thread; else ID)
	Agent   api.Agent
	Machine string // the executor it runs at; "" this machine
	Cwd     string
	Hosted  bool // Iridium hosts it (ran it once)
	Live    bool // its agent runs here, hosted, now
	// Found: discovery has it (by As), Status as found there.
	Found  bool
	Status api.Status
}

// Source is who a message is from: the terminal, and the tag it is marked with.
type Source struct{ Terminal, Tag string }

// Change is one setting changed on a session: a model, an effort, or a feature switched.
type Change struct {
	Model, Effort string
	Feature       *FeatureChange
}

// FeatureChange is feature ID switched On, at Option (one of its options; "" none).
type FeatureChange struct {
	ID     string
	On     bool
	Option string
}

// Host is what a channel asks the core (session.Manager is one).
type Host interface {
	// Ref is session id (Iridium's, or the agent's own) as the core knows it.
	Ref(id string) (Ref, bool)
	// ShowAgain publishes session id's state again.
	ShowAgain(id string)
	// Lang is the user's language ("zh", "en").
	Lang() string
	// HeldElsewhere: discovery says another process at its machine holds session as (the
	// agent's id), idle or not; where it cannot tell idle, while that process runs it.
	HeldElsewhere(as string) bool
	// Fetch mirrors session as's transcript from its machine now.
	Fetch(ctx context.Context, as string)
	// Refresh shows sessions ids again (their state recomputed).
	Refresh(ids []string)
	// Sessions are the sessions of agent a at machine, hosted or found.
	Sessions(a api.Agent, machine string) []string
	// RunOnce runs r once in a unit of its own, at machine ("" here), and gives what it printed
	// (its output, its errors); it is stopped when ctx ends.
	RunOnce(ctx context.Context, machine string, r Run) (out, errOut []byte, err error)
	// Program is agent a's program here (the config's, else its name), AgentEnv the environment
	// agents start from here.
	Program(a api.Agent) string
	AgentEnv() []string
	// Rescan has discovery look again now at machine ("" this one).
	Rescan(ctx context.Context, machine string) error
	Logf(format string, args ...any)
}

// Run is what RunOnce runs: Argv in Env at Cwd, as the machine it runs at has them.
type Run struct {
	Argv []string
	Env  []string
	Cwd  string
}

// Channel is one agent's own code, bound to the core once at its start.
type Channel interface {
	Agent() api.Agent
	Bind(Host)
}

// Stater adds to a session's state what its channel knows (a holder's model, effort and
// features; the models offered where it runs). Called with the session's own lock held: it uses
// what it keeps, never waits, and asks again in the background (Host.ShowAgain when told).
type Stater interface {
	State(r Ref, s *api.SessionState)
}

// Sender gives a message to someone else than the core, the session held there (DSH Desktop's
// agent of it). done false: not such a session; the core goes its usual way. to: whose (shown).
type Sender interface {
	Send(ctx context.Context, r Ref, text string, from Source, steer, images bool) (to string, done bool, err error)
}

// Setter sets a setting where the session is held (a holder's model, effort or feature), or one
// the session's own process has the say over though it runs here (an effort a feature there
// owns). done false: not such a session; the core goes its usual way (the driver's; a feature,
// none).
type Setter interface {
	Set(ctx context.Context, r Ref, c Change) (done bool, err error)
}

// Retuner is told the user's language changed.
type Retuner interface {
	Retuned()
}

// Launcher fills a launch's options the agent's own way (its models' catalog, a patch, presets)
// before the driver starts, and is told once its transcript is found there.
type Launcher interface {
	Prepare(ctx context.Context, r Ref, o *agent.SpawnOptions) error
	Recorded(ctx context.Context, r Ref, dir string)
}

// Models are the models an agent offers at a machine, as it says them (a probe), kept by the
// channel. Warm asks again in the background if what is kept is old.
type Models interface {
	Catalog(ctx context.Context, machine string) (agent.Catalog, error)
	// Cached is the catalog as last had, old or not; false if never.
	Cached(machine string) (agent.Catalog, bool)
	Warm(machine string)
}

// Presets are the agent presets an agent has at a machine, and the default.
type Presets interface {
	Presets(ctx context.Context, machine string) ([]agent.Preset, string, error)
}

// Imager reads a picture a session's record names by a handle of its agent's own (dsh's
// attachments) instead of carrying it, where the session runs. done false: not a handle of its;
// the core has none to show.
type Imager interface {
	Image(ctx context.Context, r Ref, handle string) (data []byte, done bool, err error)
}

// Syncer reads again at once what an agent at a machine offers (its presets, models,
// workspaces and sessions), not as kept.
type Syncer interface {
	Sync(ctx context.Context, machine string) error
}

// Set is the channels by agent.
type Set map[api.Agent]Channel

// As is a's channel as capability T, if it has one.
func As[T any](s Set, a api.Agent) (T, bool) {
	var zero T
	c, ok := s[a]
	if !ok {
		return zero, false
	}
	t, ok := c.(T)
	return t, ok
}
