// Package access is what the interactive terminals may do (plan "交互终端"). Everything, every
// terminal alike (the user, 2026-11-08), but Claude: Claude's terms give it only to the owner's own
// terminals. A terminal is found the owner's own as it is made (本机 always, the first panel, an
// invite or a token made --owner by a person) and that never changes. Without Claude a terminal
// still sees Claude's sessions; it may not start one, write to one, or answer one.
package access

import (
	"errors"

	"github.com/kumoSleeping/iridium/internal/api"
)

// Denied is what a check refuses; the server answers 403.
type Denied struct{ Reason string }

func (d *Denied) Error() string { return d.Reason }

// IsDenied reports whether err is (or wraps) a Denied.
func IsDenied(err error) bool {
	var d *Denied
	return errors.As(err, &d)
}

// Errors the checks give.
var (
	ErrClaude = &Denied{"claude is only for the owner's own terminals, found so as the terminal was made (ir invite --owner, ir token --owner)"}
	ErrManage = &Denied{"only a web panel or 本机 manages this"}
)

// Full is 本机's: the owner's machine, so Claude.
func Full() api.Rules {
	return api.Rules{ClaudeOwner: &api.OwnerConfirm{By: api.LocalTerminal}}
}

// Default is a terminal's that was not made the owner's own: no Claude.
func Default() api.Rules { return api.Rules{} }

// Ways a terminal was found the owner's own (api.OwnerConfirm.By).
const (
	ByLocal = api.LocalTerminal // a person's ir on the machine
	ByFirst = "first"           // the first panel registered
	ByPanel = "panel"           // a person on a panel that has Claude (or that confirmed an agent's ask)
)

// HasClaude reports whether r gives its terminal Claude.
func HasClaude(r api.Rules) bool { return r.ClaudeOwner != nil }

// WithClaude is r with Claude, found the owner's own by (ByLocal, ByFirst, ByPanel) at at (ms). One
// that has it already keeps how it got it.
func WithClaude(r api.Rules, by string, at int64) api.Rules {
	if !HasClaude(r) {
		r.ClaudeOwner = &api.OwnerConfirm{By: by, At: at}
	}
	return r
}

// Agent checks that r lets its terminal use agent a: any but Claude ("" is Claude), which needs
// the owner's own.
func Agent(r api.Rules, a api.Agent) error {
	if (a == "" || a == api.AgentClaude) && !HasClaude(r) {
		return ErrClaude
	}
	return nil
}
