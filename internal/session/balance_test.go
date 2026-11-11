package session

import (
	"context"
	"testing"

	"github.com/kumoSleeping/iridium/internal/agent"
	"github.com/kumoSleeping/iridium/internal/api"
)

type balancer struct {
	agent.Driver
	asked bool
}

func (b *balancer) Balance(context.Context) ([]api.Balance, error) {
	b.asked = true
	return nil, nil
}

// A session at another machine reads no balance here: that would take its key and read its files
// at paths of this machine.
func TestRemoteNoBalance(t *testing.T) {
	t.Parallel()
	b := &balancer{}
	h := &Hosted{drv: b, meta: meta{Machine: "m1"}}
	if h.refreshBalance(context.Background()) || b.asked {
		t.Fatal("asked the balance for a session at another machine")
	}
	h.meta.Machine = ""
	h.refreshBalance(context.Background())
	if !b.asked {
		t.Fatal("a session here was not asked")
	}
}
