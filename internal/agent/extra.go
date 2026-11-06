package agent

import (
	"context"
	"errors"
)

// What only some drivers can do. The host asks with a type assertion, so a driver that cannot
// leaves these out.

// Commander says which texts the driver runs itself rather than writes as words (Codex's and
// pi's /compact): the agent's record keeps no line of them.
type Commander interface {
	RunsItself(text string) bool
}

// Efforter sets the thinking effort of the running process.
type Efforter interface {
	SetEffort(ctx context.Context, level string) error
}

// Applied is what a running process says it applies now (Claude's get_settings): the model it
// runs (a full id), its effort level, and ultracode: asked for (Ultracode), asked for and in effect
// (UltracodeOn), whether the model can take it. Known: it said; an older CLI says nothing.
type Applied struct {
	Model, Effort                              string
	Ultracode, UltracodeOn, UltracodeAvailable bool
	Known                                      bool
}

// Applier reads what the running process applies. It writes a request: never call it on replay.
type Applier interface {
	Applied(ctx context.Context) (Applied, error)
}

// Ultracoder turns Claude's ultracode on or off on the running process, the effort level as it was.
type Ultracoder interface {
	SetUltracode(ctx context.Context, on bool) error
}

// ErrNoUltracode: the process's model cannot take ultracode now.
var ErrNoUltracode = errors.New("the model cannot use ultracode")

// ErrRestart: the running process cannot take the change (dsh's model and effort are its
// process's); the host keeps the setting and restarts the agent with it, now if idle, else when
// the turn ends.
var ErrRestart = errors.New("the change takes effect only with a restart")
