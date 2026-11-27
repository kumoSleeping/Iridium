package dsh

import (
	"context"

	"github.com/kumoSleeping/iridium/internal/agent"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/remote"
	"github.com/kumoSleeping/iridium/internal/remote/wire"
)

// The dsh plugin's wire surface (kept inside internal/harness/dsh so core packages never name dsh).
const (
	// CapInproc is announced in Hello.Caps by a dsh plugin running inside a dsh process with a
	// session controller (DSH Desktop / dsh web): holds with argv[0] == "@dsh-desktop" run in-process.
	CapInproc   = "dsh.inproc"
	inprocArgv0 = "@dsh-desktop"

	eventDesktopModelsChanged     = "desktop.models.changed"
	eventDesktopPresetsChanged    = "desktop.presets.changed"
	eventDesktopWorkspacesChanged = "desktop.workspaces.changed"
	eventDesktopSessionsChanged   = "desktop.sessions.changed"
	eventDesktopConfig            = "desktop.config"
)

// DesktopModelsReply answers desktop.models: the models currently registered in DSH Desktop.
type DesktopModelsReply struct {
	Default string         `json:"default,omitempty"`
	Models  []DesktopModel `json:"models"`
}

// DesktopModel is one model entry reported by desktop.models.
type DesktopModel struct {
	Value       string            `json:"value"`
	Name        string            `json:"name,omitempty"`
	Description string            `json:"description,omitempty"`
	Efforts     []string          `json:"efforts,omitempty"`
	NoEffort    bool              `json:"noEffort,omitempty"`
	EffortNames map[string]string `json:"effortNames,omitempty"`
	Group       string            `json:"group,omitempty"`
	Default     bool              `json:"default,omitempty"`
}

func (r DesktopModelsReply) toCatalog() agent.Catalog {
	out := agent.Catalog{Default: r.Default}
	for _, m := range r.Models {
		out.Models = append(out.Models, agent.Model{
			Value:       m.Value,
			Name:        m.Name,
			Description: m.Description,
			Efforts:     m.Efforts,
			NoEffort:    m.NoEffort,
			EffortNames: m.EffortNames,
			Group:       m.Group,
			Default:     m.Default,
		})
	}
	return out
}

// DesktopWorkspace is one workspace reported by desktop.workspaces / desktop.workspaces.changed.
type DesktopWorkspace struct {
	ID        string   `json:"id"`
	Title     string   `json:"title,omitempty"`
	Path      string   `json:"path"`
	Sessions  []string `json:"sessions,omitempty"`
	UpdatedAt int64    `json:"updatedAt,omitempty"`
}

// DesktopWorkspacesReply answers desktop.workspaces.
type DesktopWorkspacesReply struct {
	Workspaces []DesktopWorkspace `json:"workspaces"`
	Archived   []string           `json:"archived,omitempty"`
}

// DesktopSessionsParams queries desktop.sessions (on-demand search or list).
type DesktopSessionsParams struct {
	Query string `json:"query,omitempty"`
	Limit int    `json:"limit,omitempty"`
}

// DesktopSession is one session summary returned by desktop.sessions / desktop.sessions.changed
// (unified schema with discover.DesktopSession).
type DesktopSession struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspaceId,omitempty"`
	Cwd         string `json:"cwd,omitempty"`
	Title       string `json:"title,omitempty"`
	Last        int64  `json:"last,omitempty"`
	UpdatedAt   int64  `json:"updatedAt,omitempty"`
	Sub         bool   `json:"sub,omitempty"`
	Parent      string `json:"parent,omitempty"`
	Ended       bool   `json:"ended,omitempty"`
	Open        bool   `json:"open,omitempty"`
	Running     bool   `json:"running,omitempty"`
	Archived    bool   `json:"archived,omitempty"`
}

// DesktopSessionsReply answers desktop.sessions.
type DesktopSessionsReply struct {
	Sessions []DesktopSession `json:"sessions"`
}

// DesktopConfigEvent is pushed via desktop.config when the plugin connects or settings change.
type DesktopConfigEvent struct {
	SyncNewSessions *bool  `json:"syncNewSessions,omitempty"`
	DesktopPath     string `json:"desktopPath,omitempty"`
	Exe             string `json:"exe,omitempty"`
	HostEntry       string `json:"hostEntry,omitempty"`
	RuntimeDir      string `json:"runtimeDir,omitempty"`
	Scope           string `json:"scope,omitempty"`
	// Keys: the keys a new session there may start with (OMD's Ultra), all off, each with the
	// presets it is for; none: OMD not loaded.
	Keys []api.Attached `json:"keys,omitempty"`
}

// DesktopFreezeReply answers desktop.freeze (sent to a headless host before handing off to real Desktop).
type DesktopFreezeReply struct {
	Frozen  bool `json:"frozen"`
	Running int  `json:"running,omitempty"`
}

func desktopModels(ctx context.Context, mc *remote.Machine) (DesktopModelsReply, error) {
	var r DesktopModelsReply
	err := mc.Call(ctx, "desktop.models", struct{}{}, &r)
	return r, err
}

func desktopWorkspaces(ctx context.Context, mc *remote.Machine) (DesktopWorkspacesReply, error) {
	var r DesktopWorkspacesReply
	err := mc.Call(ctx, "desktop.workspaces", struct{}{}, &r)
	return r, err
}

func desktopSessions(ctx context.Context, mc *remote.Machine, p DesktopSessionsParams) (DesktopSessionsReply, error) {
	var r DesktopSessionsReply
	err := mc.Call(ctx, "desktop.sessions", p, &r)
	return r, err
}

func desktopFreeze(ctx context.Context, mc *remote.Machine) (DesktopFreezeReply, error) {
	var r DesktopFreezeReply
	err := mc.Call(ctx, "desktop.freeze", struct{}{}, &r)
	return r, err
}

type desktopConfigSetParams struct {
	SyncNewSessions *bool `json:"syncNewSessions,omitempty"`
}

func desktopConfigSet(ctx context.Context, mc *remote.Machine, p desktopConfigSetParams) (DesktopConfigEvent, error) {
	var r DesktopConfigEvent
	err := mc.Call(ctx, "desktop.config.set", p, &r)
	return r, err
}

func hostPresets(ctx context.Context, mc *remote.Machine) (wire.PresetsReply, error) {
	var r wire.PresetsReply
	err := mc.Call(ctx, "agent.presets", wire.PresetsParams{Agent: string(api.AgentDsh)}, &r)
	return r, err
}
