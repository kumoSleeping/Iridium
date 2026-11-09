package dsh

import (
	"context"

	"github.com/kumoSleeping/iridium/internal/remote"
	"github.com/kumoSleeping/iridium/internal/remote/wire"
)

// The dsh plugin's calls (docs/exec.md "远端 DSH"). The held.* names are historical: only the dsh
// plugin answers them; an older one answers the dsh.* names of the first two instead.

const eventHeldChanged = "held.changed"

// desktopSend gives a message to the agent of a session the dsh the executor runs in holds (DSH
// Desktop's): held.send, or dsh.prompt of a plugin without CapHeld.
func desktopSend(ctx context.Context, mc *remote.Machine, session, text string, steer bool) error {
	return mc.Call(ctx, mc.Pick(wire.CapHeld, "held.send", "dsh.prompt"), wire.HeldSendParams{Session: session, Text: text, Steer: steer}, nil)
}

// desktopGet is session's live state in the dsh there (held.get; the executor has
// wire.CapHeldFeatures), its features' texts in lang.
func desktopGet(ctx context.Context, mc *remote.Machine, session, lang string) (wire.HeldLive, error) {
	var r wire.HeldLive
	err := mc.Call(ctx, "held.get", wire.HeldGetParams{Session: session, Lang: lang}, &r)
	return r, err
}

// desktopSet changes a held session's model, effort or a feature there (held.set); the reply is
// its live state after.
func desktopSet(ctx context.Context, mc *remote.Machine, p wire.HeldSetParams) (wire.HeldLive, error) {
	var r wire.HeldLive
	err := mc.Call(ctx, "held.set", p, &r)
	return r, err
}

// desktopPresets is the agent presets the dsh the executor runs in registers (DSH Desktop's); an
// error of wire.CodeUnsupported from one that cannot say.
func desktopPresets(ctx context.Context, mc *remote.Machine) (wire.DshPresetsReply, error) {
	var r wire.DshPresetsReply
	err := mc.Call(ctx, "dsh.presets", struct{}{}, &r)
	return r, err
}

// desktopLLMPatch is the path there of a patch with the models and providers the dsh the executor
// runs in has in its settings (DSH Desktop's, written anew); an error of wire.CodeUnsupported from
// one that cannot say.
func desktopLLMPatch(ctx context.Context, mc *remote.Machine) (string, error) {
	var r wire.DshLLMReply
	err := mc.Call(ctx, "dsh.llm", struct{}{}, &r)
	return r.Patch, err
}

// desktopBundleList is the bundles of DSH Desktop's profile besides dsh's own and the dsh plugin,
// with their patches (dsh.bundles); an error of wire.CodeUnsupported from one that cannot say.
func desktopBundleList(ctx context.Context, mc *remote.Machine) ([]wire.DshBundle, error) {
	var r wire.DshBundlesReply
	err := mc.Call(ctx, "dsh.bundles", struct{}{}, &r)
	return r.Bundles, err
}

// desktopSessionPatch is the path there of the patch that puts the dsh plugin's part into a
// session's dsh process (dsh.session, written anew); an error of wire.CodeUnsupported from one
// that cannot.
func desktopSessionPatch(ctx context.Context, mc *remote.Machine) (string, error) {
	var r wire.DshSessionReply
	err := mc.Call(ctx, "dsh.session", struct{}{}, &r)
	return r.Patch, err
}

// desktopAttach files session under the workspace of its folder cwd in the dsh the executor runs
// in (DSH Desktop's sidebar), made if there is none.
func desktopAttach(ctx context.Context, mc *remote.Machine, session, cwd string) error {
	return mc.Call(ctx, "dsh.attach", wire.DshAttachParams{Session: session, Cwd: cwd}, nil)
}
