package agent

import "context"

// What only some drivers can do. The host asks with a type assertion, so a driver that cannot
// leaves these out.

// Efforter sets the thinking effort of the running process.
type Efforter interface {
	SetEffort(ctx context.Context, level string) error
}
