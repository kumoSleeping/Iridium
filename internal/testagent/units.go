package testagent

import (
	"context"
	"time"

	"github.com/kumoSleeping/iridium/internal/platform"
)

// KillAll kills every unit of l named with prefix and waits, up to ten seconds, until each one's
// StopPost ran: what a holder writes in a test's temp dir is done before the dir is removed.
func KillAll(l *platform.Direct, prefix string) {
	ctx := context.Background()
	names, _ := l.List(ctx, prefix)
	for _, n := range names {
		l.Kill(ctx, n, "KILL", true)
	}
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if names, _ := l.List(ctx, prefix); len(names) == 0 {
			return
		}
	}
}
