package session

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kumoSleeping/iridium/internal/agent"
	"github.com/kumoSleeping/iridium/internal/agents"
	"github.com/kumoSleeping/iridium/internal/api"
	"github.com/kumoSleeping/iridium/internal/platform"
	"github.com/kumoSleeping/iridium/internal/remote"
	"github.com/kumoSleeping/iridium/internal/remote/wire"
)

// Sessions whose agent runs at an executor (docs/exec.md): its holder is relayed into the hold
// directory (remote.Launcher), its transcript mirrored into remote/ there.

// fileRemoteTranscript: where the session's transcript is at the executor, and its mirror here.
const fileRemoteTranscript = "remote-transcript.json"

// fileIRToken: the SHA-256 of the session's own TOKEN, which the ir at its executor sends
// (docs/exec.md "远端的 ir"); a new one each start of its process.
const fileIRToken = "remote-ir-token"

// IRTokenPrefix marks a session's TOKEN, apart from a channel's.
const IRTokenPrefix = "irs_"

// fileFiled: a dsh session is filed under DSH Desktop's workspace of its folder there (or that dsh
// cannot file it): not asked again.
const fileFiled = "remote-filed"

type remoteTranscript struct {
	Path  string `json:"path"`  // there
	Local string `json:"local"` // the mirror here
}

// ErrOffline: the session's executor is not connected.
var ErrOffline = errors.New("the machine is not connected")

// HoldDir is the hold directory of a holder unit (remote.Hub's Dir).
func (m *Manager) HoldDir(unit string) string {
	return filepath.Join(m.holdRoot(), strings.TrimPrefix(unit, m.o.UnitPrefix))
}

// machine is the executor the session runs at; nil for this machine.
func (h *Hosted) machine() *remote.Machine {
	if h.meta.Machine == "" || h.m.o.Remote == nil {
		return nil
	}
	return h.m.o.Remote.Machine(h.meta.Machine)
}

// offline: the session runs at an executor not connected now.
func (h *Hosted) offline() bool {
	mc := h.machine()
	if mc == nil {
		return false
	}
	_, ok := mc.Hello()
	return !ok
}

func (h *Hosted) launcher() platform.Launcher {
	if mc := h.machine(); mc != nil {
		return mc.Launcher()
	}
	return h.m.o.Launcher
}

// groupPath is the folder the session is grouped by: its cwd, or for one at an executor
// "<machine>:<cwd with />", a path no folder here holds.
func groupPath(machine, cwd string) string { return remote.GroupPath(machine, cwd) }

// remoteFor is the executor a new session asks for, by terminal ID or name, checked: connected,
// the agent installed there, cwd a directory there.
func (m *Manager) remoteFor(ctx context.Context, machine string, info agents.Info, cwd string) (*remote.Machine, error) {
	if m.o.Remote == nil {
		return nil, fmt.Errorf("no machine %q", machine)
	}
	mc := m.o.Remote.Find(machine)
	if mc == nil {
		return nil, fmt.Errorf("no machine %q", machine)
	}
	hi, ok := mc.Hello()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrOffline, machine)
	}
	if info.FindRemote == nil {
		return nil, fmt.Errorf("%s does not run at another machine yet", info.Name)
	}
	ai, ok := mc.Agent(string(info.Agent))
	if !ok {
		return nil, fmt.Errorf("%w: %s (not at %s)", ErrNotInstalled, info.Name, machine)
	}
	if !info.RemoteVersionOK(ai.Version) {
		return nil, fmt.Errorf("%s %s at %s: %s or later is needed", info.Name, ai.Version, machine, info.MinRemote)
	}
	if !remote.IsAbs(hi.Sep, cwd) {
		return nil, fmt.Errorf("not a directory: %q", cwd)
	}
	st, err := mc.Stat(ctx, cwd)
	if err != nil || st.Type != "dir" {
		return nil, fmt.Errorf("not a directory: %q", cwd)
	}
	return mc, nil
}

// remoteSpawn makes o an agent's launch at mc: its environment, its program and files there.
func (h *Hosted) remoteSpawn(mc *remote.Machine, o *agent.SpawnOptions) error {
	hi, ok := mc.Hello()
	if !ok {
		return ErrOffline
	}
	ai, ok := mc.Agent(string(h.meta.Agent))
	if !ok {
		return fmt.Errorf("%w: %s is not at the machine", agent.ErrNoProgram, h.meta.Agent)
	}
	ctx := h.m.ctx
	info, _ := agents.Get(h.meta.Agent)
	o.Env, o.Program = info.RemoteEnv(hi.Env, ai.Home), ""
	// Its ir (an executor that puts one there): this start's own TOKEN, the last one's void.
	if hi.IRClient {
		tok := IRTokenPrefix + base64.RawURLEncoding.EncodeToString(randomBytes(32))
		if err := os.WriteFile(filepath.Join(h.dir, fileIRToken), []byte(tokenHash(tok)), 0o600); err != nil {
			return err
		}
		o.Env = agent.SetEnv(o.Env, "IRIDIUM_TOKEN", tok)
		if origin := h.m.o.Config.Public.Origin; origin != "" {
			o.Env = agent.SetEnv(o.Env, "IRIDIUM_URL", origin)
		}
	}
	o.Remote = &agent.Remote{
		Argv: ai.Argv, Version: ai.Version, Scope: ai.Scope, Scratch: hi.Scratch,
		Join: func(elem ...string) string { return remote.Join(hi.Sep, elem...) },
		ReadFile: func(path string) ([]byte, error) {
			c, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			return mc.ReadFile(c, path, 16<<20)
		},
		WriteFile: func(path string, data []byte) error {
			c, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			return mc.WriteFile(c, path, data, true)
		},
		Presets: h.m.presetsHook(ctx, h.meta.Agent, h.meta.Machine),
	}
	if h.meta.Agent == api.AgentDsh {
		o.Remote.Patches = append(h.m.desktopLLM(ctx, mc), h.m.desktopSession(ctx, mc)...)
		o.Remote.Bundles = h.m.desktopBundles(ctx, mc)
	}
	return nil
}

// findRemoteTranscript finds the session's transcript at its executor and mirrors it: the
// mirror's path, "" if the agent wrote none yet. Away, the mirror there is.
func (h *Hosted) findRemoteTranscript(mc *remote.Machine) (string, error) {
	var rt remoteTranscript
	_ = readJSONFile(filepath.Join(h.dir, fileRemoteTranscript), &rt)
	hi, ok := mc.Hello()
	ai, has := mc.Agent(string(h.meta.Agent))
	if !ok || !has {
		return rt.Local, nil
	}
	info, _ := agents.Get(h.meta.Agent)
	if info.FindRemote == nil {
		return "", fmt.Errorf("%s does not run at another machine", info.Name)
	}
	ctx, cancel := context.WithTimeout(h.m.ctx, 30*time.Second)
	defer cancel()
	list := func(kind string) func(dir string) ([]string, error) {
		return func(dir string) ([]string, error) {
			ents, err := mc.List(ctx, dir)
			if wire.IsCode(err, wire.CodeNotFound) {
				return nil, nil
			}
			var names []string
			for _, e := range ents {
				if e.Type == kind {
					names = append(names, e.Name)
				}
			}
			return names, err
		}
	}
	path, err := info.FindRemote(agents.RemoteWhere{
		ID: h.meta.agentID(), Cwd: h.meta.Cwd, Home: ai.Home, User: hi.Home, Env: info.RemoteEnv(hi.Env, ai.Home),
		Join: func(elem ...string) string { return remote.Join(hi.Sep, elem...) },
		List: list("file"), Dirs: list("dir"),
	})
	if err != nil || path == "" {
		return rt.Local, err
	}
	h.fileInDesktop(mc)
	if path != rt.Path {
		rt = remoteTranscript{Path: path, Local: filepath.Join(h.dir, "remote", remote.Base(hi.Sep, path))}
		if err := writeJSON(filepath.Join(h.dir, fileRemoteTranscript), rt); err != nil {
			return "", err
		}
	}
	if err := mc.Mirror(ctx, rt.Path, rt.Local); err != nil {
		if _, serr := os.Stat(rt.Local); serr == nil {
			h.logf("mirroring the transcript: %v", err)
			return rt.Local, nil
		}
		return "", err
	}
	return rt.Local, nil
}

// fileInDesktop asks once that a dsh session, its transcript there now, be filed under the workspace of its
// folder in the dsh the executor runs in (dsh.attach: DSH Desktop's sidebar has it then).
func (h *Hosted) fileInDesktop(mc *remote.Machine) {
	if h.meta.Agent != api.AgentDsh {
		return
	}
	h.mu.Lock()
	done := h.filing
	h.filing = true
	h.mu.Unlock()
	marker := filepath.Join(h.dir, fileFiled)
	if done {
		return
	}
	if _, err := os.Stat(marker); err == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(h.m.ctx, 30*time.Second)
		defer cancel()
		err := mc.DshAttach(ctx, h.meta.agentID(), h.meta.Cwd)
		if err != nil && !wire.IsCode(err, wire.CodeUnsupported) {
			h.logf("filing it under DSH Desktop's workspace: %v", err)
			h.mu.Lock()
			h.filing = false // the next look asks again
			h.mu.Unlock()
			return
		}
		if err := os.WriteFile(marker, nil, 0o600); err != nil {
			h.logf("noting it filed: %v", err)
		}
	}()
}

// syncRemote brings the transcript's mirror up to date, at most every second.
func (h *Hosted) syncRemote() {
	mc := h.machine()
	if mc == nil {
		return
	}
	h.mu.Lock()
	due := time.Since(h.mirroredAt) >= time.Second
	if due {
		h.mirroredAt = time.Now()
	}
	h.mu.Unlock()
	if !due {
		return
	}
	var rt remoteTranscript
	if readJSONFile(filepath.Join(h.dir, fileRemoteTranscript), &rt) != nil {
		return // found when first read (transcript)
	}
	if _, ok := mc.Hello(); !ok {
		return
	}
	ctx, cancel := context.WithTimeout(h.m.ctx, 10*time.Second)
	defer cancel()
	if err := mc.Mirror(ctx, rt.Path, rt.Local); err != nil && !errors.Is(err, wire.ErrOffline) {
		h.logf("mirroring the transcript: %v", err)
	}
}

// machineChanged: an executor came or went; its sessions' lamps say so.
func (m *Manager) machineChanged(id string) {
	m.mu.Lock()
	var hs []*Hosted
	for _, h := range m.hosted {
		if h.meta.Machine == id {
			hs = append(hs, h)
		}
	}
	m.mu.Unlock()
	for _, h := range hs {
		h.publish()
	}
	if len(hs) > 0 {
		m.publishList()
	}
}

func readJSONFile(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// SessionToken is the session whose own TOKEN tok is (an ir at its executor), and that executor,
// as the gate asks (gate.Config.SessionToken); ok false for none.
func (m *Manager) SessionToken(tok string) (machine, session string, ok bool) {
	if !strings.HasPrefix(tok, IRTokenPrefix) {
		return "", "", false
	}
	want := tokenHash(tok)
	m.mu.Lock()
	list := make([]*Hosted, 0, len(m.hosted))
	for _, h := range m.hosted {
		if h.meta.Machine != "" {
			list = append(list, h)
		}
	}
	m.mu.Unlock()
	for _, h := range list {
		b, err := os.ReadFile(filepath.Join(h.dir, fileIRToken))
		if err == nil && subtle.ConstantTimeCompare(b, []byte(want)) == 1 {
			return h.meta.Machine, h.id, true
		}
	}
	return "", "", false
}

func tokenHash(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	rand.Read(b)
	return b
}
