package dsh

import (
	"context"

	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/remote"
	"github.com/kumoSleeping/iridium/internal/remote/wire"
)

// The dsh plugin's calls (docs/exec.md "远端 DSH"): held.* for the sessions DSH Desktop holds;
// agent.presets and agent.prepare (wire.CapPrepare) for a launch it composes.

const eventHeldChanged = "held.changed"

// desktopSend gives a message to the agent of a session the dsh the executor runs in holds (DSH
// Desktop's): held.send.
func desktopSend(ctx context.Context, mc *remote.Machine, session, text string, steer bool) error {
	return mc.Call(ctx, "held.send", wire.HeldSendParams{Session: session, Text: text, Steer: steer}, nil)
}

// desktopGet is session's live state in the dsh there (held.get).
func desktopGet(ctx context.Context, mc *remote.Machine, session string) (wire.HeldLive, error) {
	var r wire.HeldLive
	err := mc.Call(ctx, "held.get", wire.HeldGetParams{Session: session}, &r)
	return r, err
}

// desktopSet changes a held session's model or effort there (held.set); the reply is its live
// state after.
func desktopSet(ctx context.Context, mc *remote.Machine, p wire.HeldSetParams) (wire.HeldLive, error) {
	var r wire.HeldLive
	err := mc.Call(ctx, "held.set", p, &r)
	return r, err
}

// hostPresets is the agent presets dsh's host there registers (DSH Desktop's): agent.presets.
func hostPresets(ctx context.Context, mc *remote.Machine) (wire.PresetsReply, error) {
	var r wire.PresetsReply
	err := mc.Call(ctx, "agent.presets", wire.PresetsParams{Agent: string(api.AgentDsh)}, &r)
	return r, err
}

// hostPrepare is what dsh's host there gives a launch: agent.prepare.
func hostPrepare(ctx context.Context, mc *remote.Machine, p wire.PrepareParams) (wire.PrepareReply, error) {
	p.Agent = string(api.AgentDsh)
	var r wire.PrepareReply
	err := mc.Call(ctx, "agent.prepare", p, &r)
	return r, err
}
